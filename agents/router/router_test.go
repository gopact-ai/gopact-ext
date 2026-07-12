package router

import (
	"context"
	"errors"
	"testing"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact/agent"
	"github.com/gopact-ai/gopact/workflow"
)

type testChild struct {
	identity agent.Identity
	text     string
}

func (child testChild) Identity() agent.Identity { return child.identity }
func (child testChild) Invoke(_ context.Context, _ agent.Request, _ ...gopact.RunOption) (agent.Response, error) {
	return agent.Response{Message: gopact.UserMessage(child.text)}, nil
}

type mutatingSelector struct{}

func (mutatingSelector) Select(_ context.Context, request agent.Request, _ []agent.Identity) (Selection, error) {
	request.Messages[0].Parts[0].Text = "mutated"
	request.Metadata["mutated"] = "true"
	return Selection{Child: "worker"}, nil
}

type capturingChild struct {
	identity agent.Identity
	request  agent.Request
}

func (child *capturingChild) Identity() agent.Identity { return child.identity }
func (child *capturingChild) Invoke(_ context.Context, request agent.Request, _ ...gopact.RunOption) (agent.Response, error) {
	child.request = request
	return agent.Response{Message: gopact.UserMessage("done")}, nil
}

func TestRouterInvokesOnlySelectedChild(t *testing.T) {
	first := testChild{identity: agent.Identity{Name: "first", Description: "first", Version: "v1"}, text: "first"}
	second := testChild{identity: agent.Identity{Name: "second", Description: "second", Version: "v1"}, text: "second"}
	catalog := agent.NewCatalog()
	if err := catalog.Add(first); err != nil {
		t.Fatal(err)
	}
	if err := catalog.Add(second); err != nil {
		t.Fatal(err)
	}
	directory, err := catalog.Compile()
	if err != nil {
		t.Fatal(err)
	}
	target, err := New(
		agent.Identity{Name: "router", Description: "one route", Version: "v1"},
		directory,
		SelectorFunc(func(_ context.Context, _ agent.Request, candidates []agent.Identity) (Selection, error) {
			if len(candidates) != 2 {
				t.Fatalf("candidates = %d, want 2", len(candidates))
			}
			return Selection{Child: "second"}, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	var events []gopact.Event
	response, err := target.Invoke(context.Background(), agent.Request{Messages: []gopact.Message{gopact.UserMessage("go")}}, gopact.WithSessionID("router-session"), gopact.WithStrictEventHandler(func(_ context.Context, event gopact.Event) error {
		events = append(events, event)
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if response.Message.Parts[0].Text != "second" {
		t.Fatalf("response = %+v", response)
	}
	requireEventOrder(t, events, workflow.EventWorkflowStarted, workflow.EventNodeCompleted, workflow.EventNodeCompleted, workflow.EventWorkflowCompleted)
	for index, event := range events {
		if event.Sequence != int64(index+1) || event.SessionID != "router-session" || event.RunID == "" || event.ParentRunID != "" || event.DefinitionID != "router" {
			t.Fatalf("event[%d] = %+v, want root envelope", index, event)
		}
	}
}

func requireEventOrder(t *testing.T, events []gopact.Event, wanted ...string) {
	t.Helper()
	position := 0
	for _, event := range events {
		if position < len(wanted) && event.Type == wanted[position] {
			position++
		}
	}
	if position != len(wanted) {
		t.Fatalf("event types = %v, want ordered subsequence %v", eventTypes(events), wanted)
	}
}

func eventTypes(events []gopact.Event) []string {
	types := make([]string, len(events))
	for index, event := range events {
		types[index] = event.Type
	}
	return types
}

func TestRouterRejectsUnknownSelection(t *testing.T) {
	catalog := agent.NewCatalog()
	child := testChild{identity: agent.Identity{Name: "known", Description: "known", Version: "v1"}, text: "known"}
	if err := catalog.Add(child); err != nil {
		t.Fatal(err)
	}
	directory, err := catalog.Compile()
	if err != nil {
		t.Fatal(err)
	}
	target, err := New(
		agent.Identity{Name: "router", Description: "one route", Version: "v1"}, directory,
		SelectorFunc(func(context.Context, agent.Request, []agent.Identity) (Selection, error) {
			return Selection{Child: "missing"}, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := target.Invoke(context.Background(), agent.Request{}); err == nil {
		t.Fatal("Invoke() error = nil")
	}
}

func TestRouterProtectsDurableRequestFromCustomSelectorMutation(t *testing.T) {
	child := &capturingChild{identity: agent.Identity{Name: "worker", Description: "works", Version: "v1"}}
	catalog := agent.NewCatalog()
	if err := catalog.Add(child); err != nil {
		t.Fatal(err)
	}
	directory, err := catalog.Compile()
	if err != nil {
		t.Fatal(err)
	}
	target, err := New(
		agent.Identity{Name: "router", Description: "routes", Version: "v1"},
		directory,
		mutatingSelector{},
	)
	if err != nil {
		t.Fatal(err)
	}
	request := agent.Request{
		Messages: []gopact.Message{gopact.UserMessage("original")},
		Metadata: map[string]string{"caller": "test"},
	}
	if _, err := target.Invoke(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if got := child.request.Messages[0].Parts[0].Text; got != "original" {
		t.Fatalf("child message = %q, want original", got)
	}
	if _, exists := child.request.Metadata["mutated"]; exists {
		t.Fatalf("child metadata = %v, contains selector mutation", child.request.Metadata)
	}
}

func TestRouterResumeDoesNotRepeatAcceptedSelection(t *testing.T) {
	catalog := agent.NewCatalog()
	if err := catalog.Add(testChild{identity: agent.Identity{Name: "worker", Description: "worker", Version: "v1"}, text: "done"}); err != nil {
		t.Fatal(err)
	}
	directory, err := catalog.Compile()
	if err != nil {
		t.Fatal(err)
	}
	selectorCalls := 0
	target, err := New(
		agent.Identity{Name: "router", Description: "routes", Version: "v1"}, directory,
		SelectorFunc(func(context.Context, agent.Request, []agent.Identity) (Selection, error) {
			selectorCalls++
			return Selection{Child: "worker"}, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	sinkErr := errors.New("route sink failed")
	failed := false
	_, err = target.Invoke(context.Background(), agent.Request{}, gopact.WithRunID("router-resume"), gopact.WithStrictEventHandler(func(_ context.Context, event gopact.Event) error {
		if event.RunID == "router-resume" && event.NodeID == "select" && event.Type == workflow.EventNodeCompleted && !failed {
			failed = true
			return sinkErr
		}
		return nil
	}))
	if !errors.Is(err, sinkErr) {
		t.Fatalf("Invoke() error = %v, want sink failure", err)
	}
	response, err := target.Invoke(context.Background(), agent.Request{}, workflow.WithResume(workflow.ResumeRequest{RunID: "router-resume"}))
	if err != nil {
		t.Fatalf("resumed Invoke() error = %v", err)
	}
	if selectorCalls != 1 || response.Message.Parts[0].Text != "done" {
		t.Fatalf("selector calls = %d, response = %+v", selectorCalls, response)
	}
}

func TestRouterResumesInterruptedSelectedChild(t *testing.T) {
	childIdentity := agent.Identity{Name: "worker", Description: "worker", Version: "v1"}
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
	catalog := agent.NewCatalog()
	if err := catalog.Add(child); err != nil {
		t.Fatal(err)
	}
	directory, err := catalog.Compile()
	if err != nil {
		t.Fatal(err)
	}
	selections := 0
	target, err := New(agent.Identity{Name: "router", Description: "routes", Version: "v1"}, directory, SelectorFunc(func(context.Context, agent.Request, []agent.Identity) (Selection, error) {
		selections++
		return Selection{Child: "worker"}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	var events []gopact.Event
	_, err = target.Invoke(context.Background(), agent.Request{}, gopact.WithRunID("router-parent"), gopact.WithEventHandler(func(_ context.Context, event gopact.Event) error {
		events = append(events, event)
		return nil
	}))
	var interrupted workflow.InterruptError
	if !errors.As(err, &interrupted) {
		t.Fatalf("Invoke() error = %v, want interrupt", err)
	}
	response, err := target.Invoke(context.Background(), agent.Request{}, workflow.WithResume(workflow.ResumeRequest{
		RunID: interrupted.RunID, CheckpointID: interrupted.CheckpointID,
		Resolutions: []workflow.InterruptResolution{{InterruptID: interrupted.Request.ID, PayloadRef: "approved"}},
	}))
	if err != nil {
		t.Fatalf("resumed Invoke() error = %v; first events = %+v", err, events)
	}
	if selections != 1 || childRuns != 1 || response.Message.Parts[0].Text != "approved" {
		t.Fatalf("selections = %d, child runs = %d, response = %+v", selections, childRuns, response)
	}
}
