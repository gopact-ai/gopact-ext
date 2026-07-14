package supervisor

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact/agent"
	"github.com/gopact-ai/gopact/gopacttest"
	"github.com/gopact-ai/gopact/workflow"
)

func TestSupervisorDelegatesThenFinalizesThroughWorkflowFacts(t *testing.T) {
	var rounds []int
	store := workflow.NewMemoryStore()
	target := newTestAgent(t, testDirectory(t, testChild("research", func(_ context.Context, request agent.Request) (agent.Response, error) {
		return agent.Response{Message: gopact.UserMessage("evidence:" + request.Messages[0].Parts[0].Text)}, nil
	})), DeciderFunc(func(_ context.Context, input DecisionInput) (Decision, error) {
		rounds = append(rounds, input.Round)
		if len(input.Results) == 0 {
			return Decision{
				Kind: DecisionDelegate, Child: "research",
				Request: agent.Request{Messages: []gopact.Message{gopact.UserMessage("topic")}},
			}, nil
		}
		return Decision{Kind: DecisionFinal, Response: &input.Results[0].Response}, nil
	}), WithWorkflowOptions(workflow.WithStore(store)))
	var nodes []string
	response, err := target.Invoke(
		context.Background(),
		agent.Request{Messages: []gopact.Message{gopact.UserMessage("start")}},
		gopact.WithRunID("supervisor-workflow"),
		gopact.WithStrictEventHandler(func(_ context.Context, event gopact.Event) error {
			if event.RunID == "supervisor-workflow" && event.Type == workflow.EventNodeCompleted {
				nodes = append(nodes, event.NodeID)
			}
			return nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := response.Message.Parts[0].Text; got != "evidence:topic" {
		t.Fatalf("response = %q, want evidence:topic", got)
	}
	wantNodes := []string{"start", "decide", "delegate", "record", "decide", "finish"}
	if !reflect.DeepEqual(nodes, wantNodes) || !reflect.DeepEqual(rounds, []int{1, 2}) {
		t.Fatalf("nodes/rounds = %v/%v, want %v/[1 2]", nodes, rounds, wantNodes)
	}
	checkpoint, err := store.Load(context.Background(), "supervisor-workflow")
	if err != nil || checkpoint.Status != workflow.CheckpointCompleted {
		t.Fatalf("Load() = %+v, %v, want completed checkpoint", checkpoint, err)
	}
}

func TestDecisionValidationIsClosed(t *testing.T) {
	tests := []struct {
		name     string
		decision Decision
	}{
		{name: "delegate requires child", decision: Decision{Kind: DecisionDelegate}},
		{name: "delegate rejects response", decision: Decision{Kind: DecisionDelegate, Child: "research", Response: &agent.Response{}}},
		{name: "final requires response", decision: Decision{Kind: DecisionFinal}},
		{name: "final rejects child", decision: Decision{Kind: DecisionFinal, Child: "research", Response: &agent.Response{}}},
		{name: "unknown kind", decision: Decision{Kind: "other"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.decision.validate(); err == nil {
				t.Fatalf("validate(%+v) error = nil", tt.decision)
			}
		})
	}
}

func TestSupervisorRoundLimitAllowsFinalButRejectsAnotherDelegation(t *testing.T) {
	for _, tt := range []struct {
		name      string
		decision  Decision
		wantError error
	}{
		{name: "final", decision: Decision{Kind: DecisionFinal, Response: &agent.Response{Message: gopact.UserMessage("done")}}},
		{name: "delegate", decision: Decision{Kind: DecisionDelegate, Child: "research"}, wantError: ErrRoundLimit},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			target, err := New(
				testIdentity(),
				testDirectory(t, testChild("research", func(context.Context, agent.Request) (agent.Response, error) {
					calls++
					return agent.Response{Message: gopact.UserMessage("evidence")}, nil
				})),
				DeciderFunc(func(_ context.Context, input DecisionInput) (Decision, error) {
					if len(input.Results) == 0 {
						return Decision{Kind: DecisionDelegate, Child: "research"}, nil
					}
					return tt.decision, nil
				}),
				WithMaxRounds(1),
			)
			if err != nil {
				t.Fatal(err)
			}
			response, err := target.Invoke(context.Background(), agent.Request{})
			if !errors.Is(err, tt.wantError) {
				t.Fatalf("Invoke() error = %v, want %v", err, tt.wantError)
			}
			if calls != 1 {
				t.Fatalf("child calls = %d, want 1", calls)
			}
			if tt.wantError == nil && response.Message.Parts[0].Text != "done" {
				t.Fatalf("response = %+v, want done", response)
			}
		})
	}
}

func TestSupervisorRejectsUnknownChildBeforeDelegation(t *testing.T) {
	target := newTestAgent(t, testDirectory(t), DeciderFunc(func(context.Context, DecisionInput) (Decision, error) {
		return Decision{Kind: DecisionDelegate, Child: "missing"}, nil
	}))
	var completed []string
	_, err := target.Invoke(context.Background(), agent.Request{}, gopact.WithStrictEventHandler(
		func(_ context.Context, event gopact.Event) error {
			if event.Type == workflow.EventNodeCompleted {
				completed = append(completed, event.NodeID)
			}
			return nil
		},
	))
	if err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("Invoke() error = %v, want unknown child", err)
	}
	if !reflect.DeepEqual(completed, []string{"start"}) {
		t.Fatalf("completed nodes = %v, want no accepted decision or delegation", completed)
	}
}

func TestSupervisorResumesInterruptedChildWithoutRedecidingOrReplay(t *testing.T) {
	var childRuns, decisions int
	childIdentity := agent.Identity{Name: "research", Description: "requires approval", Version: "v1"}
	childWorkflow := workflow.New[agent.Request, agent.Response](childIdentity.Name, workflow.WithTopologyVersion(childIdentity.Version))
	work := childWorkflow.Node("work", func(context.Context, agent.Request) (agent.Response, error) {
		childRuns++
		return agent.Response{Message: gopact.UserMessage("approved")}, nil
	})
	work.Guard(workflow.BeforeRun("approval", workflow.GuardFunc[agent.Request, agent.Response](
		func(context.Context, workflow.GuardContext[agent.Request, agent.Response]) (workflow.GuardDecision[agent.Request, agent.Response], error) {
			return workflow.GuardInterrupt[agent.Request, agent.Response]{Request: workflow.InterruptRequest{ID: "child-approval"}}, nil
		},
	)))
	childWorkflow.Entry(work)
	childWorkflow.Exit(work)
	child, err := agent.NewWorkflowAgent(childIdentity, childWorkflow)
	if err != nil {
		t.Fatal(err)
	}
	target := newTestAgent(t, testDirectory(t, child), DeciderFunc(func(_ context.Context, input DecisionInput) (Decision, error) {
		decisions++
		if len(input.Results) == 0 {
			return Decision{Kind: DecisionDelegate, Child: "research"}, nil
		}
		return Decision{Kind: DecisionFinal, Response: &input.Results[0].Response}, nil
	}))
	_, err = target.Invoke(context.Background(), agent.Request{}, gopact.WithRunID("supervisor-parent"))
	var interrupted workflow.InterruptError
	if !errors.As(err, &interrupted) {
		t.Fatalf("Invoke() error = %v, want Workflow InterruptError", err)
	}
	response, err := target.Invoke(context.Background(), agent.Request{}, workflow.WithResume(workflow.ResumeRequest{
		RunID: "supervisor-parent", CheckpointID: interrupted.CheckpointID,
		Resolutions: []workflow.InterruptResolution{{InterruptID: "child-approval", PayloadRef: "resolution://approved"}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if response.Message.Parts[0].Text != "approved" || childRuns != 1 || decisions != 2 {
		t.Fatalf("response/child/decisions = %+v/%d/%d, want same child continuation", response, childRuns, decisions)
	}
}

func TestSupervisorResumeDoesNotRepeatRecordedDelegation(t *testing.T) {
	var childCalls, decisions int
	target := newTestAgent(t, testDirectory(t, testChild("research", func(context.Context, agent.Request) (agent.Response, error) {
		childCalls++
		return agent.Response{Message: gopact.UserMessage("evidence")}, nil
	})), DeciderFunc(func(_ context.Context, input DecisionInput) (Decision, error) {
		decisions++
		if len(input.Results) == 0 {
			return Decision{Kind: DecisionDelegate, Child: "research"}, nil
		}
		return Decision{Kind: DecisionFinal, Response: &input.Results[0].Response}, nil
	}))
	sinkErr := errors.New("record sink failed")
	failed := false
	_, err := target.Invoke(
		context.Background(), agent.Request{}, gopact.WithRunID("supervisor-resume"),
		gopact.WithStrictEventHandler(func(_ context.Context, event gopact.Event) error {
			if event.RunID == "supervisor-resume" && event.Type == workflow.EventNodeCompleted && event.NodeID == "record" && !failed {
				failed = true
				return sinkErr
			}
			return nil
		}),
	)
	if !errors.Is(err, sinkErr) {
		t.Fatalf("Invoke() error = %v, want sink failure", err)
	}
	response, err := target.Invoke(
		context.Background(), agent.Request{}, workflow.WithResume(workflow.ResumeRequest{RunID: "supervisor-resume"}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if response.Message.Parts[0].Text != "evidence" || childCalls != 1 || decisions != 2 {
		t.Fatalf("response/child/decisions = %+v/%d/%d, want committed delegation reused", response, childCalls, decisions)
	}
}

func TestSupervisorAgentConformance(t *testing.T) {
	target := newTestAgent(t, testDirectory(t), DeciderFunc(func(context.Context, DecisionInput) (Decision, error) {
		return Decision{Kind: DecisionFinal, Response: &agent.Response{Message: gopact.UserMessage("done")}}, nil
	}))
	gopacttest.RequireAgentConformance(t, gopacttest.AgentConformanceCase{
		Agent: target, Request: agent.Request{},
		Validate: func(response agent.Response) error {
			if len(response.Message.Parts) == 0 {
				return errors.New("empty response")
			}
			return nil
		},
	})
}

type testAgentChild struct {
	identity agent.Identity
	invoke   func(context.Context, agent.Request) (agent.Response, error)
}

func testChild(name string, invoke func(context.Context, agent.Request) (agent.Response, error)) agent.Agent {
	return &testAgentChild{identity: agent.Identity{Name: name, Description: name + " child", Version: "v1"}, invoke: invoke}
}

func (child *testAgentChild) Identity() agent.Identity { return child.identity }

func (child *testAgentChild) Invoke(ctx context.Context, request agent.Request, _ ...gopact.RunOption) (agent.Response, error) {
	return child.invoke(ctx, request)
}

func testDirectory(t *testing.T, children ...agent.Agent) *agent.Directory {
	t.Helper()
	catalog := agent.NewCatalog()
	for _, child := range children {
		if err := catalog.Add(child); err != nil {
			t.Fatal(err)
		}
	}
	directory, err := catalog.Compile()
	if err != nil {
		t.Fatal(err)
	}
	return directory
}

func newTestAgent(t *testing.T, directory *agent.Directory, decider Decider, options ...Option) *Agent {
	t.Helper()
	target, err := New(testIdentity(), directory, decider, options...)
	if err != nil {
		t.Fatal(err)
	}
	return target
}

func testIdentity() agent.Identity {
	return agent.Identity{Name: "supervisor", Description: "supervises", Version: "v1"}
}
