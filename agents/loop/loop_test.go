package loop

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact/agent"
	"github.com/gopact-ai/gopact/gopacttest"
	"github.com/gopact-ai/gopact/workflow"
)

func TestAgentLoopsUntilConditionStops(t *testing.T) {
	var childInputs []string
	child := loopChild("worker", func(_ context.Context, request agent.Request) (agent.Response, error) {
		input := request.Messages[0].Parts[0].Text
		childInputs = append(childInputs, input)
		return agent.Response{Message: gopact.UserMessage(input + "!")}, nil
	})
	var conditionIterations []int
	condition := ConditionFunc(func(_ context.Context, iteration Iteration) (Decision, error) {
		conditionIterations = append(conditionIterations, iteration.Number)
		iteration.Response.Message.Parts[0].Text = "condition-mutation"
		if iteration.Number < 3 {
			return DecisionContinue, nil
		}
		return DecisionStop, nil
	})
	target, err := New(testIdentity(), child, condition, WithMaxIterations(5))
	if err != nil {
		t.Fatal(err)
	}
	var events []gopact.Event
	response, err := target.Invoke(context.Background(), agent.Request{
		Messages: []gopact.Message{gopact.UserMessage("start")},
	}, gopact.WithStrictEventHandler(func(_ context.Context, event gopact.Event) error {
		if event.Type == workflow.EventNodeStarted {
			events = append(events, event)
		}
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if response.Message.Parts[0].Text != "start!!!" {
		t.Fatalf("response = %+v, want third child result", response)
	}
	if !reflect.DeepEqual(childInputs, []string{"start", "start!", "start!!"}) {
		t.Fatalf("child inputs = %v, want response mapping", childInputs)
	}
	if !reflect.DeepEqual(conditionIterations, []int{1, 2, 3}) {
		t.Fatalf("condition iterations = %v, want 1..3", conditionIterations)
	}
	wantNodes := []string{"child.worker", "condition", "child.worker", "condition", "child.worker", "condition", "finish"}
	gotNodes := make([]string, len(events))
	for index, event := range events {
		gotNodes[index] = event.NodeID
	}
	if !reflect.DeepEqual(gotNodes, wantNodes) {
		t.Fatalf("node starts = %v, want %v", gotNodes, wantNodes)
	}
}

func TestAgentEnforcesMaxIterations(t *testing.T) {
	var calls int
	child := loopChild("worker", func(_ context.Context, request agent.Request) (agent.Response, error) {
		calls++
		return agent.Response{Message: gopact.UserMessage("again")}, nil
	})
	target, err := New(
		testIdentity(),
		child,
		ConditionFunc(func(context.Context, Iteration) (Decision, error) {
			return DecisionContinue, nil
		}),
		WithMaxIterations(2),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := target.Invoke(context.Background(), agent.Request{}); !errors.Is(err, ErrMaxIterations) {
		t.Fatalf("Invoke() error = %v, want ErrMaxIterations", err)
	}
	if calls != 2 {
		t.Fatalf("child calls = %d, want 2", calls)
	}
}

func TestAgentPropagatesChildAndConditionErrors(t *testing.T) {
	t.Run("child", func(t *testing.T) {
		boom := errors.New("child failed")
		target, err := New(
			testIdentity(),
			loopChild("worker", func(context.Context, agent.Request) (agent.Response, error) {
				return agent.Response{}, boom
			}),
			ConditionFunc(func(context.Context, Iteration) (Decision, error) {
				return DecisionStop, nil
			}),
		)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := target.Invoke(context.Background(), agent.Request{}); !errors.Is(err, boom) {
			t.Fatalf("Invoke() error = %v, want child failure", err)
		}
	})

	t.Run("condition", func(t *testing.T) {
		boom := errors.New("condition failed")
		target, err := New(
			testIdentity(),
			loopChild("worker", nil),
			ConditionFunc(func(context.Context, Iteration) (Decision, error) {
				return 0, boom
			}),
		)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := target.Invoke(context.Background(), agent.Request{}); !errors.Is(err, boom) {
			t.Fatalf("Invoke() error = %v, want condition failure", err)
		}
	})
}

func TestNewRejectsInvalidLoopConfiguration(t *testing.T) {
	child := loopChild("worker", nil)
	condition := ConditionFunc(func(context.Context, Iteration) (Decision, error) {
		return DecisionStop, nil
	})
	if _, err := New(testIdentity(), nil, condition); err == nil {
		t.Fatal("New(nil child) error = nil")
	}
	if _, err := New(testIdentity(), child, nil); err == nil {
		t.Fatal("New(nil condition) error = nil")
	}
	if _, err := New(testIdentity(), child, condition, WithMaxIterations(0)); err == nil {
		t.Fatal("New(zero max iterations) error = nil")
	}
	invalidChild := loopChild("worker", nil)
	invalidChild.identity.Description = ""
	if _, err := New(testIdentity(), invalidChild, condition, WithMaxIterations(0)); !errors.Is(err, agent.ErrInvalidIdentity) {
		t.Fatalf("New(invalid child, invalid option) error = %v, want ErrInvalidIdentity", err)
	}
}

func TestAgentResumesInterruptedChildBeforeEvaluatingCondition(t *testing.T) {
	childIdentity := agent.Identity{Name: "worker", Description: "interrupts once", Version: "v1"}
	childRuns := 0
	childWorkflow := workflow.New[agent.Request, agent.Response](childIdentity.Name, workflow.WithTopologyVersion(childIdentity.Version))
	childNode := childWorkflow.Node("work", func(context.Context, agent.Request) (agent.Response, error) {
		childRuns++
		return agent.Response{Message: gopact.UserMessage("approved")}, nil
	})
	childNode.Guard(workflow.BeforeRun("approval", workflow.GuardFunc[agent.Request, agent.Response](
		func(context.Context, workflow.GuardContext[agent.Request, agent.Response]) (workflow.GuardDecision[agent.Request, agent.Response], error) {
			return workflow.GuardInterrupt[agent.Request, agent.Response]{Request: workflow.InterruptRequest{ID: "child-approval"}}, nil
		},
	)))
	childWorkflow.Entry(childNode)
	childWorkflow.Exit(childNode)
	child, err := agent.NewWorkflowAgent(childIdentity, childWorkflow)
	if err != nil {
		t.Fatal(err)
	}
	var conditionCalls int
	target, err := New(
		testIdentity(),
		child,
		ConditionFunc(func(_ context.Context, iteration Iteration) (Decision, error) {
			conditionCalls++
			if iteration.Response.Message.Parts[0].Text != "approved" {
				t.Fatalf("iteration = %+v, want resumed response", iteration)
			}
			return DecisionStop, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	_, err = target.Invoke(
		context.Background(),
		agent.Request{Messages: []gopact.Message{gopact.UserMessage("start")}},
		gopact.WithRunID("loop-parent"),
	)
	var interrupted workflow.InterruptError
	if !errors.As(err, &interrupted) {
		t.Fatalf("Invoke() error = %v, want parent Workflow interrupt", err)
	}
	if childRuns != 0 || conditionCalls != 0 {
		t.Fatalf("before resume = child:%d condition:%d, want 0/0", childRuns, conditionCalls)
	}

	response, err := target.Invoke(context.Background(), agent.Request{}, workflow.WithResume(workflow.ResumeRequest{
		RunID:        interrupted.RunID,
		CheckpointID: interrupted.CheckpointID,
		Resolutions: []workflow.InterruptResolution{{
			InterruptID: interrupted.Request.ID,
			PayloadRef:  "resolution://approved",
		}},
	}))
	if err != nil {
		t.Fatalf("resumed Invoke() error = %v", err)
	}
	if response.Message.Parts[0].Text != "approved" {
		t.Fatalf("response = %+v, want approved", response)
	}
	if childRuns != 1 || conditionCalls != 1 {
		t.Fatalf("after resume = child:%d condition:%d, want 1/1", childRuns, conditionCalls)
	}
}

func TestAgentResumeDoesNotRepeatCommittedIterationPhase(t *testing.T) {
	for _, nodeID := range []string{"child.worker", "condition"} {
		t.Run(nodeID, func(t *testing.T) {
			childCalls, conditionCalls := 0, 0
			child := loopChild("worker", func(context.Context, agent.Request) (agent.Response, error) {
				childCalls++
				return agent.Response{Message: gopact.UserMessage("done")}, nil
			})
			target, err := New(testIdentity(), child, ConditionFunc(func(context.Context, Iteration) (Decision, error) {
				conditionCalls++
				return DecisionStop, nil
			}))
			if err != nil {
				t.Fatal(err)
			}
			sinkErr := errors.New("iteration sink failed")
			failed := false
			runID := "loop-" + nodeID
			_, err = target.Invoke(context.Background(), agent.Request{}, gopact.WithRunID(runID), gopact.WithStrictEventHandler(func(_ context.Context, event gopact.Event) error {
				if event.RunID == runID && event.NodeID == nodeID && event.Type == workflow.EventNodeCompleted && !failed {
					failed = true
					return sinkErr
				}
				return nil
			}))
			if !errors.Is(err, sinkErr) {
				t.Fatalf("Invoke() error = %v, want sink failure", err)
			}
			response, err := target.Invoke(context.Background(), agent.Request{}, workflow.WithResume(workflow.ResumeRequest{RunID: runID}))
			if err != nil {
				t.Fatalf("resumed Invoke() error = %v", err)
			}
			if childCalls != 1 || conditionCalls != 1 || response.Message.Parts[0].Text != "done" {
				t.Fatalf("child calls = %d, condition calls = %d, response = %+v", childCalls, conditionCalls, response)
			}
		})
	}
}

func TestAgentConformance(t *testing.T) {
	child := loopChild("worker", func(context.Context, agent.Request) (agent.Response, error) {
		return agent.Response{Message: gopact.UserMessage("done")}, nil
	})
	target, err := New(
		testIdentity(),
		child,
		ConditionFunc(func(context.Context, Iteration) (Decision, error) {
			return DecisionStop, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	gopacttest.RequireAgentConformance(t, gopacttest.AgentConformanceCase{
		Agent:   target,
		Request: agent.Request{Messages: []gopact.Message{gopact.UserMessage("work")}},
		Validate: func(response agent.Response) error {
			if len(response.Message.Parts) != 1 || response.Message.Parts[0].Text != "done" {
				return errors.New("unexpected response")
			}
			return nil
		},
	})
}

type testChild struct {
	identity agent.Identity
	invoke   func(context.Context, agent.Request) (agent.Response, error)
}

func loopChild(name string, invoke func(context.Context, agent.Request) (agent.Response, error)) *testChild {
	if invoke == nil {
		invoke = func(context.Context, agent.Request) (agent.Response, error) {
			return agent.Response{Message: gopact.UserMessage("done")}, nil
		}
	}
	return &testChild{
		identity: agent.Identity{Name: name, Description: name + " child", Version: "v1"},
		invoke:   invoke,
	}
}

func (child *testChild) Identity() agent.Identity { return child.identity }

func (child *testChild) Invoke(ctx context.Context, request agent.Request, _ ...gopact.RunOption) (agent.Response, error) {
	return child.invoke(ctx, request)
}

func testIdentity() agent.Identity {
	return agent.Identity{Name: "iteration", Description: "repeats one child", Version: "v1"}
}
