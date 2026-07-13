package sequential

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact/agent"
	"github.com/gopact-ai/gopact/gopacttest"
	"github.com/gopact-ai/gopact/workflow"
)

func TestAgentInvokesChildrenInDeclaredOrder(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	first := childAgent("first", func(_ context.Context, request agent.Request) (agent.Response, error) {
		mu.Lock()
		calls = append(calls, "first:"+request.Messages[0].Parts[0].Text)
		mu.Unlock()
		return agent.Response{
			Message:   gopact.UserMessage("first-result"),
			Artifacts: []gopact.ArtifactRef{{URI: "artifact://first"}},
			Metadata:  map[string]string{"stage": "first"},
		}, nil
	})
	second := childAgent("second", func(_ context.Context, request agent.Request) (agent.Response, error) {
		if len(request.Messages) != 1 || request.Messages[0].Parts[0].Text != "first-result" ||
			len(request.Artifacts) != 1 || request.Artifacts[0].URI != "artifact://first" ||
			request.Metadata["stage"] != "first" {
			t.Fatalf("second request = %+v, want first response mapping", request)
		}
		mu.Lock()
		calls = append(calls, "second:first-result")
		mu.Unlock()
		return agent.Response{Message: gopact.UserMessage("done")}, nil
	})
	directory := compileDirectory(t, first, second)
	store := workflow.NewMemoryStore()
	target, err := New(testIdentity(), directory, []string{"first", "second"}, WithWorkflowOptions(
		workflow.WithCheckpointer(store), workflow.WithJournal(store),
	))
	if err != nil {
		t.Fatal(err)
	}
	response, err := target.Invoke(context.Background(), agent.Request{
		Messages: []gopact.Message{gopact.UserMessage("start")},
	}, gopact.WithRunID("sequential-persistence"))
	if err != nil {
		t.Fatal(err)
	}
	if response.Message.Parts[0].Text != "done" {
		t.Fatalf("response = %+v, want done", response)
	}
	want := []string{"first:start", "second:first-result"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	checkpoint, err := store.Load(context.Background(), "sequential-persistence")
	if err != nil || checkpoint.Status != workflow.CheckpointCompleted {
		t.Fatalf("Load() = %+v, %v, want completed checkpoint", checkpoint, err)
	}
}

func TestAgentStopsAfterChildFailure(t *testing.T) {
	boom := errors.New("child failed")
	var secondCalls int
	first := childAgent("first", func(context.Context, agent.Request) (agent.Response, error) {
		return agent.Response{}, boom
	})
	second := childAgent("second", func(context.Context, agent.Request) (agent.Response, error) {
		secondCalls++
		return agent.Response{}, nil
	})
	target, err := New(testIdentity(), compileDirectory(t, first, second), []string{"first", "second"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := target.Invoke(context.Background(), agent.Request{}); !errors.Is(err, boom) {
		t.Fatalf("Invoke() error = %v, want child failure", err)
	}
	if secondCalls != 0 {
		t.Fatalf("second calls = %d, want 0", secondCalls)
	}
}

func TestAgentUsesWorkflowFacts(t *testing.T) {
	child := childAgent("only", func(context.Context, agent.Request) (agent.Response, error) {
		return agent.Response{Message: gopact.UserMessage("done")}, nil
	})
	target, err := New(testIdentity(), compileDirectory(t, child), []string{"only"})
	if err != nil {
		t.Fatal(err)
	}
	var events []gopact.Event
	_, err = target.Invoke(context.Background(), agent.Request{}, gopact.WithRunID("sequential-workflow"), gopact.WithStrictEventHandler(func(_ context.Context, event gopact.Event) error {
		if event.RunID == "sequential-workflow" {
			events = append(events, event)
		}
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	var startedNode bool
	for _, event := range events {
		if event.Type == workflow.EventNodeStarted && event.NodeID == "only" {
			startedNode = true
		}
	}
	if len(events) == 0 || events[0].Type != workflow.EventWorkflowStarted ||
		events[len(events)-1].Type != workflow.EventWorkflowCompleted || !startedNode {
		t.Fatalf("events = %+v, want Workflow lifecycle and child node fact", events)
	}
}

func TestNewRejectsInvalidChildSequence(t *testing.T) {
	directory := compileDirectory(t, childAgent("first", nil))
	if _, err := New(testIdentity(), directory, nil); err == nil {
		t.Fatal("New(empty) error = nil")
	}
	if _, err := New(testIdentity(), directory, []string{"missing"}); err == nil {
		t.Fatal("New(unknown) error = nil")
	}
	if _, err := New(testIdentity(), nil, []string{"first"}); err == nil {
		t.Fatal("New(nil directory) error = nil")
	}
}

func TestAgentResumesInterruptedChildAtCurrentStep(t *testing.T) {
	firstIdentity := agent.Identity{Name: "first", Description: "interrupts once", Version: "v1"}
	firstRuns := 0
	childWorkflow := workflow.New[agent.Request, agent.Response](firstIdentity.Name, workflow.WithTopologyVersion(firstIdentity.Version))
	childNode := childWorkflow.Node("work", func(context.Context, agent.Request) (agent.Response, error) {
		firstRuns++
		return agent.Response{Message: gopact.UserMessage("approved")}, nil
	})
	childNode.Guard(workflow.BeforeRun("approval", workflow.GuardFunc[agent.Request, agent.Response](
		func(context.Context, workflow.GuardContext[agent.Request, agent.Response]) (workflow.GuardDecision[agent.Request, agent.Response], error) {
			return workflow.GuardInterrupt[agent.Request, agent.Response]{Request: workflow.InterruptRequest{
				ID: "child-approval", Subject: "approve child",
			}}, nil
		},
	)))
	childWorkflow.Entry(childNode)
	childWorkflow.Exit(childNode)
	first, err := agent.NewWorkflowAgent(firstIdentity, childWorkflow)
	if err != nil {
		t.Fatal(err)
	}
	var secondCalls int
	second := childAgent("second", func(_ context.Context, request agent.Request) (agent.Response, error) {
		secondCalls++
		if request.Messages[0].Parts[0].Text != "approved" {
			t.Fatalf("second request = %+v, want resumed child response", request)
		}
		return agent.Response{Message: gopact.UserMessage("done")}, nil
	})
	target, err := New(
		testIdentity(),
		compileDirectory(t, first, second),
		[]string{"first", "second"},
	)
	if err != nil {
		t.Fatal(err)
	}

	_, err = target.Invoke(
		context.Background(),
		agent.Request{Messages: []gopact.Message{gopact.UserMessage("start")}},
		gopact.WithRunID("parent-run"),
	)
	var interrupted workflow.InterruptError
	if !errors.As(err, &interrupted) {
		t.Fatalf("Invoke() error = %v, want parent workflow InterruptError", err)
	}
	if firstRuns != 0 || secondCalls != 0 {
		t.Fatalf("calls before resume = first:%d second:%d, want 0/0", firstRuns, secondCalls)
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
	if response.Message.Parts[0].Text != "done" {
		t.Fatalf("response = %+v, want done", response)
	}
	if firstRuns != 1 || secondCalls != 1 {
		t.Fatalf("calls after resume = first:%d second:%d, want 1/1", firstRuns, secondCalls)
	}
}

func TestAgentResumeDoesNotRepeatCommittedStepPhase(t *testing.T) {
	for _, eventType := range []string{workflow.EventNodeStarted, workflow.EventNodeCompleted} {
		t.Run(eventType, func(t *testing.T) {
			calls := 0
			child := childAgent("only", func(context.Context, agent.Request) (agent.Response, error) {
				calls++
				return agent.Response{Message: gopact.UserMessage("done")}, nil
			})
			target, err := New(testIdentity(), compileDirectory(t, child), []string{"only"})
			if err != nil {
				t.Fatal(err)
			}
			sinkErr := errors.New("step sink failed")
			failed := false
			runID := "sequential-" + eventType
			_, err = target.Invoke(context.Background(), agent.Request{}, gopact.WithRunID(runID), gopact.WithStrictEventHandler(func(_ context.Context, event gopact.Event) error {
				if event.RunID == runID && event.NodeID == "only" && event.Type == eventType && !failed {
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
			if calls != 1 || response.Message.Parts[0].Text != "done" {
				t.Fatalf("child calls = %d, response = %+v", calls, response)
			}
		})
	}
}

func TestAgentConformance(t *testing.T) {
	child := childAgent("only", func(context.Context, agent.Request) (agent.Response, error) {
		return agent.Response{Message: gopact.UserMessage("done")}, nil
	})
	target, err := New(testIdentity(), compileDirectory(t, child), []string{"only"})
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

func childAgent(name string, invoke func(context.Context, agent.Request) (agent.Response, error)) *testChild {
	if invoke == nil {
		invoke = func(_ context.Context, request agent.Request) (agent.Response, error) {
			return agent.Response{Message: firstMessage(request)}, nil
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

func compileDirectory(t *testing.T, children ...agent.Agent) *agent.Directory {
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

func firstMessage(request agent.Request) gopact.Message {
	if len(request.Messages) == 0 {
		return gopact.UserMessage("")
	}
	return request.Messages[0]
}

func testIdentity() agent.Identity {
	return agent.Identity{Name: "pipeline", Description: "runs children in order", Version: "v1"}
}
