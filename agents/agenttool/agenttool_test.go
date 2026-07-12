package agenttool

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact/agent"
	"github.com/gopact-ai/gopact/workflow"
)

func TestToolMapsCallAndForwardsWorkflowOptions(t *testing.T) {
	var childRequest agent.Request
	var childRunID string
	child := testAgent("delegate", func(_ context.Context, request agent.Request, options ...gopact.RunOption) (agent.Response, error) {
		childRequest = request
		childRunID = gopact.ResolveRunOptions(options...).RunID
		return agent.Response{
			Message: gopact.UserMessage("child-result"), Artifacts: []gopact.ArtifactRef{{URI: "artifact://child"}},
		}, nil
	})
	target, err := New(
		gopact.ToolSpec{Name: "delegate", Schema: json.RawMessage(`{"type":"object"}`)},
		child,
		AdapterFuncs{
			InputFunc: func(_ context.Context, call gopact.ToolCall) (agent.Request, error) {
				var arguments struct {
					Task string `json:"task"`
				}
				if err := json.Unmarshal(call.Arguments, &arguments); err != nil {
					return agent.Request{}, err
				}
				return agent.Request{Messages: []gopact.Message{gopact.UserMessage(arguments.Task)}}, nil
			},
			OutputFunc: func(_ context.Context, response agent.Response) (gopact.ToolResult, error) {
				return gopact.ToolResult{
					DataRef: "data://result", ArtifactRefs: response.Artifacts, Preview: response.Message.Parts[0].Text,
				}, nil
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	call := gopact.ToolCall{ID: "call-1", Name: "delegate", Arguments: json.RawMessage(`{"task":"review"}`)}
	outcome, err := target.Invoke(context.Background(), call, gopact.WithRunID("child-run"))
	if err != nil {
		t.Fatal(err)
	}
	result, ok := outcome.(gopact.ToolResultOutcome)
	if !ok || result.Result.Preview != "child-result" || result.Result.DataRef != "data://result" ||
		len(result.Result.ArtifactRefs) != 1 || childRunID != "child-run" {
		t.Fatalf("outcome/run id = %+v/%q, want mapped result and forwarded options", outcome, childRunID)
	}
	if childRequest.Messages[0].Parts[0].Text != "review" {
		t.Fatalf("child request = %+v, want mapped arguments", childRequest)
	}
}

func TestToolResumesInterruptedWorkflowChild(t *testing.T) {
	var childRuns int
	identity := agent.Identity{Name: "delegate", Description: "requires approval", Version: "v1"}
	wf := workflow.New[agent.Request, agent.Response](identity.Name, workflow.WithTopologyVersion(identity.Version))
	work := wf.Node("work", func(context.Context, agent.Request) (agent.Response, error) {
		childRuns++
		return agent.Response{Message: gopact.UserMessage("approved")}, nil
	})
	work.Guard(workflow.BeforeRun("approval", workflow.GuardFunc[agent.Request, agent.Response](
		func(context.Context, workflow.GuardContext[agent.Request, agent.Response]) (workflow.GuardDecision[agent.Request, agent.Response], error) {
			return workflow.GuardInterrupt[agent.Request, agent.Response]{Request: workflow.InterruptRequest{ID: "approval"}}, nil
		},
	)))
	wf.Entry(work)
	wf.Exit(work)
	child, err := agent.NewWorkflowAgent(identity, wf)
	if err != nil {
		t.Fatal(err)
	}
	target, err := New(gopact.ToolSpec{Name: "delegate"}, child, identityAdapter())
	if err != nil {
		t.Fatal(err)
	}
	call := gopact.ToolCall{ID: "call-1", Name: "delegate"}
	_, err = target.Invoke(context.Background(), call, gopact.WithRunID("tool-child"))
	var interrupted workflow.InterruptError
	if !errors.As(err, &interrupted) {
		t.Fatalf("Invoke() error = %v, want Workflow InterruptError", err)
	}
	outcome, err := target.Invoke(context.Background(), call, workflow.WithResume(workflow.ResumeRequest{
		RunID: interrupted.RunID, CheckpointID: interrupted.CheckpointID,
		Resolutions: []workflow.InterruptResolution{{InterruptID: "approval", PayloadRef: "resolution://approved"}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	result, ok := outcome.(gopact.ToolResultOutcome)
	if !ok || result.Result.Preview != "approved" || childRuns != 1 {
		t.Fatalf("outcome/runs = %+v/%d, want resumed child result once", outcome, childRuns)
	}
}

func TestToolPropagatesAdapterAndChildFailures(t *testing.T) {
	boom := errors.New("boom")
	tests := []struct {
		name    string
		child   agent.Agent
		adapter Adapter
	}{
		{
			name: "input adapter", child: testAgent("delegate", nil),
			adapter: AdapterFuncs{
				InputFunc:  func(context.Context, gopact.ToolCall) (agent.Request, error) { return agent.Request{}, boom },
				OutputFunc: func(context.Context, agent.Response) (gopact.ToolResult, error) { return gopact.ToolResult{}, nil },
			},
		},
		{
			name: "child",
			child: testAgent("delegate", func(context.Context, agent.Request, ...gopact.RunOption) (agent.Response, error) {
				return agent.Response{}, boom
			}),
			adapter: identityAdapter(),
		},
		{
			name: "output adapter", child: testAgent("delegate", nil),
			adapter: AdapterFuncs{
				InputFunc:  identityAdapter().InputFunc,
				OutputFunc: func(context.Context, agent.Response) (gopact.ToolResult, error) { return gopact.ToolResult{}, boom },
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target, err := New(gopact.ToolSpec{Name: "delegate"}, tt.child, tt.adapter)
			if err != nil {
				t.Fatal(err)
			}
			_, err = target.Invoke(context.Background(), gopact.ToolCall{ID: "call-1", Name: "delegate"})
			if !errors.Is(err, boom) {
				t.Fatalf("Invoke() error = %v, want underlying failure", err)
			}
		})
	}
}

func TestNewRejectsInvalidToolConfiguration(t *testing.T) {
	child := testAgent("delegate", nil)
	tests := []struct {
		name    string
		spec    gopact.ToolSpec
		child   agent.Agent
		adapter Adapter
	}{
		{name: "empty name", child: child, adapter: identityAdapter()},
		{name: "invalid schema", spec: gopact.ToolSpec{Name: "delegate", Schema: []byte(`{"type":`)}, child: child, adapter: identityAdapter()},
		{name: "nil child", spec: gopact.ToolSpec{Name: "delegate"}, adapter: identityAdapter()},
		{name: "nil adapter", spec: gopact.ToolSpec{Name: "delegate"}, child: child},
		{name: "missing output", spec: gopact.ToolSpec{Name: "delegate"}, child: child, adapter: AdapterFuncs{InputFunc: identityAdapter().InputFunc}},
		{name: "missing input", spec: gopact.ToolSpec{Name: "delegate"}, child: child, adapter: AdapterFuncs{OutputFunc: identityAdapter().OutputFunc}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := New(tt.spec, tt.child, tt.adapter); err == nil {
				t.Fatal("New() error = nil")
			}
		})
	}
}

func TestToolHasImmutableSpecAndValidatesCallBeforeAdapter(t *testing.T) {
	var adapterCalls int
	adapter := identityAdapter()
	adapter.InputFunc = func(context.Context, gopact.ToolCall) (agent.Request, error) {
		adapterCalls++
		return agent.Request{}, nil
	}
	spec := gopact.ToolSpec{
		Name: "delegate", Schema: json.RawMessage(`{"type":"object"}`), Metadata: map[string]string{"owner": "original"},
	}
	target, err := New(spec, testAgent("delegate", nil), adapter)
	if err != nil {
		t.Fatal(err)
	}
	spec.Schema[0] = '['
	spec.Metadata["owner"] = "mutated"
	first := target.Spec()
	first.Schema[0] = '['
	first.Metadata["owner"] = "caller"
	second := target.Spec()
	if string(second.Schema) != `{"type":"object"}` || second.Metadata["owner"] != "original" {
		t.Fatalf("Spec() = %+v, want immutable snapshot", second)
	}
	_, err = target.Invoke(context.Background(), gopact.ToolCall{
		ID: "call-1", Name: "delegate", Arguments: []byte(`{"task":`),
	})
	if err == nil || !strings.Contains(err.Error(), "invalid JSON") || adapterCalls != 0 {
		t.Fatalf("Invoke() error/adapter calls = %v/%d, want validation before adapter", err, adapterCalls)
	}
	if _, ok := any(target).(agent.InvokableTool); !ok {
		t.Fatal("Agent-backed Tool does not implement InvokableTool")
	}
}

type testChild struct {
	identity agent.Identity
	invoke   func(context.Context, agent.Request, ...gopact.RunOption) (agent.Response, error)
}

func testAgent(name string, invoke func(context.Context, agent.Request, ...gopact.RunOption) (agent.Response, error)) *testChild {
	if invoke == nil {
		invoke = func(context.Context, agent.Request, ...gopact.RunOption) (agent.Response, error) {
			return agent.Response{Message: gopact.UserMessage("done")}, nil
		}
	}
	return &testChild{
		identity: agent.Identity{Name: name, Description: name + " agent", Version: "v1"}, invoke: invoke,
	}
}

func (child *testChild) Identity() agent.Identity { return child.identity }

func (child *testChild) Invoke(ctx context.Context, request agent.Request, options ...gopact.RunOption) (agent.Response, error) {
	return child.invoke(ctx, request, options...)
}

func identityAdapter() AdapterFuncs {
	return AdapterFuncs{
		InputFunc: func(context.Context, gopact.ToolCall) (agent.Request, error) {
			return agent.Request{Messages: []gopact.Message{gopact.UserMessage("work")}}, nil
		},
		OutputFunc: func(_ context.Context, response agent.Response) (gopact.ToolResult, error) {
			return gopact.ToolResult{Preview: response.Message.Parts[0].Text}, nil
		},
	}
}
