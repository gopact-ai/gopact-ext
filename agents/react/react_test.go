package react

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact/checkpoint"
	"github.com/gopact-ai/gopact/gopacttest"
	"github.com/gopact-ai/gopact/graph"
	"github.com/gopact-ai/gopact/memory"
	"github.com/gopact-ai/gopact/provider"
	"github.com/gopact-ai/gopact/tools"
)

func TestAgentDirectFinalMatchesGoldenTrajectory(t *testing.T) {
	model := &scriptedModel{
		responses: []gopact.Message{
			{Role: gopact.RoleAssistant, Content: "done"},
		},
	}
	agent, err := New(model, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	events, err := gopacttest.CollectEvents(agent.Run(context.Background(), State{
		Messages: []gopact.Message{{Role: gopact.RoleUser, Content: "hi"}},
	}))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	gopacttest.RequireGoldenTrajectoryFrames(t, "testdata/direct_final.golden.json", events)
	if len(model.requests) != 1 {
		t.Fatalf("model request count = %d, want 1", len(model.requests))
	}
}

func TestAgentToolCallThenFinalMatchesGoldenTrajectory(t *testing.T) {
	ctx := context.Background()
	registry := tools.NewRegistry()
	if err := registry.Register(ctx, gopact.ToolFunc{
		SpecValue: gopact.ToolSpec{Name: "echo", Description: "echoes input"},
		InvokeFunc: func(_ context.Context, args json.RawMessage) (gopact.ToolResult, error) {
			var input struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(args, &input); err != nil {
				return gopact.ToolResult{}, err
			}
			return gopact.ToolResult{Content: input.Text}, nil
		},
	}, tools.RegisterOptions{Namespace: "local", Visibility: tools.VisibleTool}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	model := &scriptedModel{
		responses: []gopact.Message{
			{
				Role: gopact.RoleAssistant,
				ToolCalls: []gopact.ToolCall{
					{ID: "call-1", Name: "local.echo", Arguments: []byte(`{"text":"hello"}`)},
				},
			},
			{Role: gopact.RoleAssistant, Content: "final"},
		},
	}
	agent, err := New(model, registry)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	events, err := gopacttest.CollectEvents(agent.Run(ctx, State{
		Messages: []gopact.Message{{Role: gopact.RoleUser, Content: "hi"}},
	}))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	gopacttest.RequireGoldenTrajectoryFrames(t, "testdata/tool_then_final.golden.json", events)
	if len(model.requests) != 2 {
		t.Fatalf("model request count = %d, want 2", len(model.requests))
	}
	if len(model.requests[0].Tools) != 1 || model.requests[0].Tools[0].Name != "local.echo" {
		t.Fatalf("first model request tools = %+v, want local.echo", model.requests[0].Tools)
	}
	lastMessage := model.requests[1].Messages[len(model.requests[1].Messages)-1]
	if lastMessage.Role != gopact.RoleTool || lastMessage.Content != "hello" || lastMessage.ToolCallID != "call-1" {
		t.Fatalf("second model request last message = %+v, want tool result", lastMessage)
	}
	for _, event := range events {
		if event.Type == gopact.EventToolCall || event.Type == gopact.EventToolResult {
			if event.IDs.CallID != "call-1" {
				t.Fatalf("%s CallID = %q, want call-1", event.Type, event.IDs.CallID)
			}
		}
	}
}

func TestAgentMultiToolCallThenFinalMatchesGoldenTrajectory(t *testing.T) {
	ctx := context.Background()
	registry := tools.NewRegistry()
	for _, name := range []string{"first", "second"} {
		name := name
		if err := registry.Register(ctx, gopact.ToolFunc{
			SpecValue: gopact.ToolSpec{Name: name, Description: name + " tool"},
			InvokeFunc: func(_ context.Context, _ json.RawMessage) (gopact.ToolResult, error) {
				return gopact.ToolResult{Content: name + "-result"}, nil
			},
		}, tools.RegisterOptions{Namespace: "local", Visibility: tools.VisibleTool}); err != nil {
			t.Fatalf("Register(%s) error = %v", name, err)
		}
	}
	model := &scriptedModel{
		responses: []gopact.Message{
			{
				Role: gopact.RoleAssistant,
				ToolCalls: []gopact.ToolCall{
					{ID: "call-1", Name: "local.first", Arguments: []byte(`{}`)},
					{ID: "call-2", Name: "local.second", Arguments: []byte(`{}`)},
				},
			},
			{Role: gopact.RoleAssistant, Content: "final"},
		},
	}
	agent, err := New(model, registry)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	events, err := gopacttest.CollectEvents(agent.Run(ctx, State{
		Messages: []gopact.Message{{Role: gopact.RoleUser, Content: "hi"}},
	}))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	gopacttest.RequireGoldenTrajectoryFrames(t, "testdata/multi_tool_then_final.golden.json", events)
	if len(model.requests) != 2 {
		t.Fatalf("model request count = %d, want 2", len(model.requests))
	}
	messages := model.requests[1].Messages
	if len(messages) < 2 {
		t.Fatalf("second model request messages = %+v, want two tool result messages", messages)
	}
	firstResult := messages[len(messages)-2]
	secondResult := messages[len(messages)-1]
	if firstResult.Role != gopact.RoleTool || firstResult.ToolCallID != "call-1" || firstResult.Content != "first-result" {
		t.Fatalf("first tool result message = %+v, want call-1 result", firstResult)
	}
	if secondResult.Role != gopact.RoleTool || secondResult.ToolCallID != "call-2" || secondResult.Content != "second-result" {
		t.Fatalf("second tool result message = %+v, want call-2 result", secondResult)
	}
	for _, event := range events {
		switch event.Type {
		case gopact.EventToolCall, gopact.EventToolResult:
			if event.IDs.CallID != "call-1" && event.IDs.CallID != "call-2" {
				t.Fatalf("%s CallID = %q, want call-1 or call-2", event.Type, event.IDs.CallID)
			}
		}
	}
}

func TestAgentMultiToolBatchStopsOnToolErrorMatchesGoldenTrajectory(t *testing.T) {
	ctx := context.Background()
	toolErr := errors.New("second tool failed")
	registry := tools.NewRegistry()
	invoked := map[string]int{}
	for _, spec := range []struct {
		name string
		err  error
	}{
		{name: "first"},
		{name: "second", err: toolErr},
	} {
		spec := spec
		if err := registry.Register(ctx, gopact.ToolFunc{
			SpecValue: gopact.ToolSpec{Name: spec.name, Description: spec.name + " tool"},
			InvokeFunc: func(_ context.Context, _ json.RawMessage) (gopact.ToolResult, error) {
				invoked[spec.name]++
				if spec.err != nil {
					return gopact.ToolResult{}, spec.err
				}
				return gopact.ToolResult{Content: spec.name + "-result"}, nil
			},
		}, tools.RegisterOptions{Namespace: "local", Visibility: tools.VisibleTool}); err != nil {
			t.Fatalf("Register(%s) error = %v", spec.name, err)
		}
	}
	model := &scriptedModel{
		responses: []gopact.Message{
			{
				Role: gopact.RoleAssistant,
				ToolCalls: []gopact.ToolCall{
					{ID: "call-1", Name: "local.first", Arguments: []byte(`{}`)},
					{ID: "call-2", Name: "local.second", Arguments: []byte(`{}`)},
				},
			},
			{Role: gopact.RoleAssistant, Content: "should-not-run"},
		},
	}
	agent, err := New(model, registry)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	events, err := gopacttest.CollectEvents(agent.Run(ctx, State{
		Messages: []gopact.Message{{Role: gopact.RoleUser, Content: "hi"}},
	}, gopact.WithRuntimeIDs(gopact.RuntimeIDs{RunID: "run-1"})))
	if !errors.Is(err, toolErr) {
		t.Fatalf("Run() error = %v, want toolErr", err)
	}

	gopacttest.RequireGoldenTrajectoryFrames(t, "testdata/multi_tool_error.golden.json", events)
	if invoked["first"] != 1 || invoked["second"] != 1 {
		t.Fatalf("tool invocations = %+v, want first and second once", invoked)
	}
	if len(model.requests) != 1 {
		t.Fatalf("model request count = %d, want only initial model call", len(model.requests))
	}
	export := requireRunExport(t, events)
	if len(export.Failures) != 1 || export.Failures[0].Kind != gopact.FailureTool {
		t.Fatalf("run export failures = %+v, want one tool failure", export.Failures)
	}
}

func TestAgentOnlyExposesVisibleToolsToModel(t *testing.T) {
	ctx := context.Background()
	registry := tools.NewRegistry()
	if err := registry.Register(ctx, gopact.ToolFunc{
		SpecValue: gopact.ToolSpec{Name: "visible", Description: "visible tool"},
		InvokeFunc: func(_ context.Context, _ json.RawMessage) (gopact.ToolResult, error) {
			return gopact.ToolResult{}, nil
		},
	}, tools.RegisterOptions{Namespace: "local", Visibility: tools.VisibleTool}); err != nil {
		t.Fatalf("Register(visible) error = %v", err)
	}
	if err := registry.Register(ctx, gopact.ToolFunc{
		SpecValue: gopact.ToolSpec{Name: "deferred", Description: "deferred tool"},
		InvokeFunc: func(ctx context.Context, args json.RawMessage) (gopact.ToolResult, error) {
			return gopact.ToolResult{}, nil
		},
	}, tools.RegisterOptions{Namespace: "local", Visibility: tools.DeferredTool}); err != nil {
		t.Fatalf("Register(deferred) error = %v", err)
	}
	model := &scriptedModel{
		responses: []gopact.Message{{Role: gopact.RoleAssistant, Content: "done"}},
	}
	agent, err := New(model, registry)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if _, err := gopacttest.CollectEvents(agent.Run(ctx, State{
		Messages: []gopact.Message{{Role: gopact.RoleUser, Content: "hi"}},
	})); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(model.requests) != 1 {
		t.Fatalf("model request count = %d, want 1", len(model.requests))
	}
	if len(model.requests[0].Tools) != 1 || model.requests[0].Tools[0].Name != "local.visible" {
		t.Fatalf("first request tools = %+v, want only local.visible", model.requests[0].Tools)
	}

	if err := registry.Promote(ctx, []string{"local.deferred"}, tools.Scope{}); err != nil {
		t.Fatalf("Promote() error = %v", err)
	}
	modelAfterPromote := &scriptedModel{
		responses: []gopact.Message{{Role: gopact.RoleAssistant, Content: "done"}},
	}
	agentAfterPromote, err := New(modelAfterPromote, registry)
	if err != nil {
		t.Fatalf("New(after promote) error = %v", err)
	}
	if _, err := gopacttest.CollectEvents(agentAfterPromote.Run(ctx, State{
		Messages: []gopact.Message{{Role: gopact.RoleUser, Content: "hi"}},
	})); err != nil {
		t.Fatalf("Run(after promote) error = %v", err)
	}
	if len(modelAfterPromote.requests[0].Tools) != 2 {
		t.Fatalf("promoted request tools = %+v, want visible + promoted deferred", modelAfterPromote.requests[0].Tools)
	}
}

func TestAgentRejectsUnpromotedDeferredToolCalls(t *testing.T) {
	ctx := context.Background()
	registry := tools.NewRegistry()
	deferredCalled := false
	if err := registry.Register(ctx, gopact.ToolFunc{
		SpecValue: gopact.ToolSpec{Name: "apply_patch", Description: "deferred write tool"},
		InvokeFunc: func(ctx context.Context, args json.RawMessage) (gopact.ToolResult, error) {
			deferredCalled = true
			return gopact.ToolResult{Content: "patched"}, nil
		},
	}, tools.RegisterOptions{Namespace: "local", Visibility: tools.DeferredTool}); err != nil {
		t.Fatalf("Register(deferred) error = %v", err)
	}
	model := &scriptedModel{
		responses: []gopact.Message{
			{
				Role: gopact.RoleAssistant,
				ToolCalls: []gopact.ToolCall{
					{ID: "call-1", Name: "local.apply_patch", Arguments: []byte(`{}`)},
				},
			},
		},
	}
	agent, err := New(model, registry)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	events, err := gopacttest.CollectEvents(agent.Run(ctx, State{
		Messages: []gopact.Message{{Role: gopact.RoleUser, Content: "hi"}},
	}))
	if !errors.Is(err, tools.ErrToolNotVisible) {
		t.Fatalf("Run() error = %v, want ErrToolNotVisible", err)
	}
	if deferredCalled {
		t.Fatal("unpromoted deferred tool was invoked")
	}
	gopacttest.RequireEventTypes(t, events,
		gopact.EventRunStarted,
		gopact.EventNodeStarted,
		gopact.EventModelMessage,
		gopact.EventNodeCompleted,
		gopact.EventNodeStarted,
		gopact.EventToolCall,
		gopact.EventNodeFailed,
		gopact.EventRunFailed,
	)
	gopacttest.RequireGoldenTrajectoryFrames(t, "testdata/unpromoted_deferred_tool.golden.json", events)
}

func TestAgentRunExportRecordsCompletedStepSnapshots(t *testing.T) {
	ctx := context.Background()
	registry := tools.NewRegistry()
	if err := registry.Register(ctx, gopact.ToolFunc{
		SpecValue: gopact.ToolSpec{Name: "echo", Description: "echoes input"},
		InvokeFunc: func(ctx context.Context, args json.RawMessage) (gopact.ToolResult, error) {
			return gopact.ToolResult{Content: "hello"}, nil
		},
	}, tools.RegisterOptions{Namespace: "local", Visibility: tools.VisibleTool}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	model := &scriptedModel{
		responses: []gopact.Message{
			{
				Role: gopact.RoleAssistant,
				ToolCalls: []gopact.ToolCall{
					{ID: "call-1", Name: "local.echo", Arguments: []byte(`{}`)},
				},
			},
			{Role: gopact.RoleAssistant, Content: "final"},
		},
	}
	agent, err := New(model, registry)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	events, err := gopacttest.CollectEvents(agent.Run(ctx, State{
		Messages: []gopact.Message{{Role: gopact.RoleUser, Content: "hi"}},
	}, gopact.WithRuntimeIDs(gopact.RuntimeIDs{RunID: "run-1", ThreadID: "thread-1"})))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	recorder := gopact.NewRunRecorder()
	for _, event := range events {
		if err := recorder.Record(event); err != nil {
			t.Fatalf("Record() error = %v", err)
		}
	}
	export, err := recorder.Export()
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}

	if export.IDs.RunID != "run-1" || export.IDs.ThreadID != "thread-1" {
		t.Fatalf("export IDs = %+v, want run/thread ids", export.IDs)
	}
	if len(export.Steps) != 3 {
		t.Fatalf("export steps = %d, want 3", len(export.Steps))
	}
	expectedNodes := []string{nodeCallModel, nodeCallTool, nodeCallModel}
	expectedIDs := []string{"run-1:1", "run-1:2", "run-1:3"}
	for i, step := range export.Steps {
		if step.ID != expectedIDs[i] || step.Node != expectedNodes[i] || step.Step != i+1 || step.Phase != gopact.StepCompleted {
			t.Fatalf("export step %d = %+v, want completed %s step", i, step, expectedNodes[i])
		}
		output, ok := step.Output.(State)
		if !ok {
			t.Fatalf("export step %d output type = %T, want react.State", i, step.Output)
		}
		if len(output.Messages) != i+2 {
			t.Fatalf("export step %d output messages = %d, want %d", i, len(output.Messages), i+2)
		}
	}
}

func TestAgentMaxIterationsExceededFailsRun(t *testing.T) {
	ctx := context.Background()
	registry := tools.NewRegistry()
	if err := registry.Register(ctx, gopact.ToolFunc{
		SpecValue: gopact.ToolSpec{Name: "echo", Description: "echoes input"},
		InvokeFunc: func(ctx context.Context, args json.RawMessage) (gopact.ToolResult, error) {
			return gopact.ToolResult{Content: "hello"}, nil
		},
	}, tools.RegisterOptions{Namespace: "local", Visibility: tools.VisibleTool}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	agent, err := New(&scriptedModel{
		responses: []gopact.Message{
			{
				Role: gopact.RoleAssistant,
				ToolCalls: []gopact.ToolCall{
					{ID: "call-1", Name: "local.echo", Arguments: []byte(`{}`)},
				},
			},
		},
	}, registry, WithMaxIterations(1))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	events, err := gopacttest.CollectEvents(agent.Run(ctx, State{
		Messages: []gopact.Message{{Role: gopact.RoleUser, Content: "hi"}},
	}))
	if !errors.Is(err, ErrMaxIterations) {
		t.Fatalf("Run() error = %v, want ErrMaxIterations", err)
	}
	gopacttest.RequireEventTypes(t, events,
		gopact.EventRunStarted,
		gopact.EventNodeStarted,
		gopact.EventModelMessage,
		gopact.EventNodeCompleted,
		gopact.EventNodeStarted,
		gopact.EventToolCall,
		gopact.EventToolResult,
		gopact.EventNodeCompleted,
		gopact.EventRunFailed,
	)
	gopacttest.RequireGoldenTrajectoryFrames(t, "testdata/max_iterations.golden.json", events)
}

func TestAgentMemoryRecallInjectsContextAndEmitsEvent(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	memoryID, err := store.Put(ctx, memory.Memory{
		Scope:   memory.Scope{UserID: "user-1"},
		Type:    memory.TypeProfile,
		Content: "concise status updates",
	})
	if err != nil {
		t.Fatalf("Put(memory) error = %v", err)
	}
	model := &scriptedModel{
		responses: []gopact.Message{
			{Role: gopact.RoleAssistant, Content: "done"},
		},
	}
	agent, err := New(model, nil, WithMemory(store))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	events, err := gopacttest.CollectEvents(agent.Run(ctx, State{
		Messages: []gopact.Message{{Role: gopact.RoleUser, Content: "concise"}},
	}, gopact.WithRuntimeIDs(gopact.RuntimeIDs{RunID: "run-1", UserID: "user-1"})))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	gopacttest.RequireEventTypes(t, events,
		gopact.EventRunStarted,
		gopact.EventNodeStarted,
		gopact.EventMemorySearched,
		gopact.EventModelMessage,
		gopact.EventNodeCompleted,
		gopact.EventRunCompleted,
	)
	gopacttest.RequireGoldenTrajectoryFrames(t, "testdata/memory_recall.golden.json", events)
	memoryEvent := events[2]
	if memoryEvent.Node != nodeCallModel || memoryEvent.Step != 1 {
		t.Fatalf("memory event node/step = %s/%d, want call_model/1", memoryEvent.Node, memoryEvent.Step)
	}
	if memoryEvent.Metadata["memory_count"] != 1 {
		t.Fatalf("memory count metadata = %#v, want 1", memoryEvent.Metadata["memory_count"])
	}
	memoryIDs, ok := memoryEvent.Metadata["memory_ids"].([]string)
	if !ok || len(memoryIDs) != 1 || memoryIDs[0] != string(memoryID) {
		t.Fatalf("memory ids metadata = %#v, want %q", memoryEvent.Metadata["memory_ids"], memoryID)
	}
	if len(model.requests) != 1 {
		t.Fatalf("model request count = %d, want 1", len(model.requests))
	}
	requestMessages := model.requests[0].Messages
	if len(requestMessages) != 2 {
		t.Fatalf("model request messages = %d, want memory context + user", len(requestMessages))
	}
	memoryMessage := requestMessages[0]
	if memoryMessage.Role != gopact.RoleSystem || memoryMessage.Name != "gopact.memory" {
		t.Fatalf("memory message = %+v, want named system memory context", memoryMessage)
	}
	if !strings.Contains(memoryMessage.Text(), "concise status updates") {
		t.Fatalf("memory message text = %q, want recalled memory content", memoryMessage.Text())
	}
	if requestMessages[1].Role != gopact.RoleUser || requestMessages[1].Content != "concise" {
		t.Fatalf("second model message = %+v, want original user message", requestMessages[1])
	}
}

func TestAgentMemoryRecallUsesRunnerRuntimeIDs(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	if _, err := store.Put(ctx, memory.Memory{
		Scope:   memory.Scope{UserID: "user-1"},
		Type:    memory.TypeProfile,
		Content: "concise status updates",
	}); err != nil {
		t.Fatalf("Put(memory) error = %v", err)
	}
	model := &scriptedModel{
		responses: []gopact.Message{
			{Role: gopact.RoleAssistant, Content: "done"},
		},
	}
	agent, err := New(model, nil, WithMemory(store))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	runner, err := gopact.NewRunner(agent, gopact.WithRunnerRuntimeIDs(gopact.RuntimeIDs{UserID: "user-1"}))
	if err != nil {
		t.Fatalf("NewRunner() error = %v", err)
	}

	_, err = gopacttest.CollectEvents(runner.Run(ctx, State{
		Messages: []gopact.Message{{Role: gopact.RoleUser, Content: "concise"}},
	}, gopact.WithRuntimeIDs(gopact.RuntimeIDs{RunID: "run-1"})))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if len(model.requests) != 1 {
		t.Fatalf("model request count = %d, want 1", len(model.requests))
	}
	if got := model.requests[0].Messages[0].Name; got != memoryMessageName {
		t.Fatalf("first model message name = %q, want memory context", got)
	}
}

func TestAgentMemoryExtractorWritesMemoryAndRecordsEffect(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	extracted := 0
	agent, err := New(&scriptedModel{
		responses: []gopact.Message{
			{Role: gopact.RoleAssistant, Content: "noted"},
		},
	}, nil, WithMemory(
		store,
		WithMemoryQuery(func(ctx context.Context, state State, ids gopact.RuntimeIDs) (memory.Query, bool, error) {
			return memory.Query{}, false, nil
		}),
		WithMemoryExtractor(func(ctx context.Context, state State, ids gopact.RuntimeIDs) ([]memory.Memory, error) {
			extracted++
			if len(state.Messages) == 0 || state.Messages[len(state.Messages)-1].Text() != "noted" {
				t.Fatalf("extract state messages = %+v, want final assistant message", state.Messages)
			}
			return []memory.Memory{
				{Type: memory.TypeProfile, Content: "prefers concise updates"},
			}, nil
		}),
	))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	events, err := gopacttest.CollectEvents(agent.Run(ctx, State{
		Messages: []gopact.Message{{Role: gopact.RoleUser, Content: "remember this"}},
	}, gopact.WithRuntimeIDs(gopact.RuntimeIDs{
		RunID:     "run-1",
		UserID:    "user-1",
		SessionID: "session-1",
		ThreadID:  "thread-1",
		AgentID:   "agent-1",
		AppID:     "app-1",
	})))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if extracted != 1 {
		t.Fatalf("extract count = %d, want 1", extracted)
	}
	gopacttest.RequireEventTypes(t, events,
		gopact.EventRunStarted,
		gopact.EventNodeStarted,
		gopact.EventModelMessage,
		gopact.EventMemoryPut,
		gopact.EventNodeCompleted,
		gopact.EventRunCompleted,
	)
	gopacttest.RequireGoldenTrajectoryFrames(t, "testdata/memory_write.golden.json", events)
	if events[3].Metadata["memory_id"] == "" {
		t.Fatalf("memory put metadata = %+v, want memory_id", events[3].Metadata)
	}
	stored, err := store.Search(ctx, memory.Query{
		Scope: memory.Scope{UserID: "user-1", SessionID: "session-1", ThreadID: "thread-1", AgentID: "agent-1", AppID: "app-1"},
		Text:  "concise",
	})
	if err != nil {
		t.Fatalf("Search(memory) error = %v", err)
	}
	if len(stored.Memories) != 1 {
		t.Fatalf("stored memories = %+v, want one extracted memory", stored.Memories)
	}
	var completed gopact.StepSnapshot
	for _, event := range events {
		if event.Type == gopact.EventNodeCompleted && event.Node == nodeCallModel && event.StepSnapshot != nil {
			completed = *event.StepSnapshot
		}
	}
	if len(completed.Effects) != 1 || completed.Effects[0].Type != memory.EffectTypeMemoryPut {
		t.Fatalf("completed model effects = %+v, want memory_put effect", completed.Effects)
	}
}

func TestAgentMemoryMergeRunsAfterExtractBeforeWrite(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	mergeCalls := 0
	agent, err := New(&scriptedModel{
		responses: []gopact.Message{
			{Role: gopact.RoleAssistant, Content: "noted"},
		},
	}, nil, WithMemory(
		store,
		WithMemoryQuery(func(ctx context.Context, state State, ids gopact.RuntimeIDs) (memory.Query, bool, error) {
			return memory.Query{}, false, nil
		}),
		WithMemoryExtractor(func(ctx context.Context, state State, ids gopact.RuntimeIDs) ([]memory.Memory, error) {
			return []memory.Memory{
				{Type: memory.TypeSemantic, Content: "prefers concise updates"},
				{Type: memory.TypeSemantic, Content: "works on go agent sdk"},
			}, nil
		}),
		WithMemoryMerge(func(ctx context.Context, request MemoryMergeRequest) ([]memory.Memory, error) {
			mergeCalls++
			if request.IDs.RunID != "run-1" || request.IDs.UserID != "user-1" || request.IDs.ThreadID != "thread-1" {
				t.Fatalf("merge runtime IDs = %+v, want run/user/thread IDs", request.IDs)
			}
			if len(request.State.Messages) == 0 || request.State.Messages[len(request.State.Messages)-1].Text() != "noted" {
				t.Fatalf("merge state = %+v, want final assistant message", request.State)
			}
			if len(request.Memories) != 2 {
				t.Fatalf("merge memories = %+v, want extracted memories", request.Memories)
			}
			request.Memories[0].Content = "mutated by merge"
			return []memory.Memory{
				{
					ID:      "merged-profile",
					Type:    memory.TypeProfile,
					Content: "prefers concise go sdk updates",
				},
			}, nil
		}),
	))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	events, err := gopacttest.CollectEvents(agent.Run(ctx, State{
		Messages: []gopact.Message{{Role: gopact.RoleUser, Content: "remember this"}},
	}, gopact.WithRuntimeIDs(gopact.RuntimeIDs{
		RunID:    "run-1",
		UserID:   "user-1",
		ThreadID: "thread-1",
	})))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	gopacttest.RequireGoldenTrajectoryFrames(t, "testdata/memory_merge.golden.json", events)
	if mergeCalls != 1 {
		t.Fatalf("merge calls = %d, want 1", mergeCalls)
	}
	stored, err := store.Search(ctx, memory.Query{
		Scope: memory.Scope{UserID: "user-1", ThreadID: "thread-1"},
		Text:  "go sdk",
	})
	if err != nil {
		t.Fatalf("Search(merged memory) error = %v", err)
	}
	if len(stored.Memories) != 1 || stored.Memories[0].ID != "merged-profile" {
		t.Fatalf("stored memories = %+v, want only merged memory", stored.Memories)
	}
	originals, err := store.Search(ctx, memory.Query{
		Scope: memory.Scope{UserID: "user-1", ThreadID: "thread-1"},
		Text:  "works on go agent sdk",
	})
	if err != nil {
		t.Fatalf("Search(original memory) error = %v", err)
	}
	if len(originals.Memories) != 0 {
		t.Fatalf("original memories = %+v, want none after merge", originals.Memories)
	}

	var completed gopact.StepSnapshot
	for _, event := range events {
		if event.Type == gopact.EventNodeCompleted && event.Node == nodeCallModel && event.StepSnapshot != nil {
			completed = *event.StepSnapshot
		}
	}
	if len(completed.Effects) != 1 || completed.Effects[0].Target != "memory://merged-profile" {
		t.Fatalf("completed effects = %+v, want one merged memory effect", completed.Effects)
	}
	recorded, ok := completed.Effects[0].Metadata[memory.EffectMetadataMemory].(memory.Memory)
	if !ok || recorded.Content != "prefers concise go sdk updates" {
		t.Fatalf("effect memory metadata = %#v, want merged memory", completed.Effects[0].Metadata[memory.EffectMetadataMemory])
	}
}

func TestAgentDeferredMemoryWritesRecordReplayableEffectWithoutPuttingMemory(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	agent, err := New(&scriptedModel{
		responses: []gopact.Message{
			{Role: gopact.RoleAssistant, Content: "noted"},
		},
	}, nil, WithMemory(
		store,
		WithMemoryQuery(func(ctx context.Context, state State, ids gopact.RuntimeIDs) (memory.Query, bool, error) {
			return memory.Query{}, false, nil
		}),
		WithMemoryExtractor(func(ctx context.Context, state State, ids gopact.RuntimeIDs) ([]memory.Memory, error) {
			return []memory.Memory{
				{Type: memory.TypeProfile, Content: "prefers background memory writes"},
			}, nil
		}),
		WithMemoryWriteMode(MemoryWriteDeferred),
	))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	events, err := gopacttest.CollectEvents(agent.Run(ctx, State{
		Messages: []gopact.Message{{Role: gopact.RoleUser, Content: "remember this"}},
	}, gopact.WithRuntimeIDs(gopact.RuntimeIDs{
		RunID:    "run-1",
		UserID:   "user-1",
		ThreadID: "thread-1",
	})))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	gopacttest.RequireGoldenTrajectoryFrames(t, "testdata/memory_deferred_write.golden.json", events)
	stored, err := store.Search(ctx, memory.Query{
		Scope: memory.Scope{UserID: "user-1", ThreadID: "thread-1"},
		Text:  "background",
	})
	if err != nil {
		t.Fatalf("Search(before replay) error = %v", err)
	}
	if len(stored.Memories) != 0 {
		t.Fatalf("stored memories before replay = %+v, want none", stored.Memories)
	}

	var completed gopact.StepSnapshot
	var memoryEvent gopact.Event
	for _, event := range events {
		if event.Type == gopact.EventMemoryPut {
			memoryEvent = event
		}
		if event.Type == gopact.EventNodeCompleted && event.Node == nodeCallModel && event.StepSnapshot != nil {
			completed = *event.StepSnapshot
		}
	}
	if memoryEvent.Metadata["memory_write_mode"] != string(MemoryWriteDeferred) || memoryEvent.Metadata["memory_pending"] != true {
		t.Fatalf("memory event metadata = %+v, want deferred pending write", memoryEvent.Metadata)
	}
	if len(completed.Effects) != 1 {
		t.Fatalf("completed effects = %+v, want one memory effect", completed.Effects)
	}
	effect := completed.Effects[0]
	if effect.Type != memory.EffectTypeMemoryPut || effect.Applied || effect.ReplayPolicy != gopact.EffectReplayIdempotent || effect.IdempotencyKey == "" {
		t.Fatalf("memory effect = %+v, want unapplied idempotent memory_put", effect)
	}

	plan, err := gopact.PlanEffectReplay(completed)
	if err != nil {
		t.Fatalf("PlanEffectReplay() error = %v", err)
	}
	if plan.ReplayCount != 1 {
		t.Fatalf("replay count = %d, want 1", plan.ReplayCount)
	}
	if _, err := gopact.ExecuteEffectReplay(ctx, plan, memory.NewReplayHandler(store)); err != nil {
		t.Fatalf("ExecuteEffectReplay(memory) error = %v", err)
	}
	stored, err = store.Search(ctx, memory.Query{
		Scope: memory.Scope{UserID: "user-1", ThreadID: "thread-1"},
		Text:  "background",
	})
	if err != nil {
		t.Fatalf("Search(after replay) error = %v", err)
	}
	if len(stored.Memories) != 1 {
		t.Fatalf("stored memories after replay = %+v, want one replayed memory", stored.Memories)
	}
}

func TestAgentDeferredMemoryExtractionRecordsEffectWithoutCallingExtractor(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	extracted := 0
	agent, err := New(&scriptedModel{
		responses: []gopact.Message{
			{Role: gopact.RoleAssistant, Content: "noted"},
		},
	}, nil, WithMemory(
		store,
		WithMemoryQuery(func(ctx context.Context, state State, ids gopact.RuntimeIDs) (memory.Query, bool, error) {
			return memory.Query{}, false, nil
		}),
		WithMemoryExtractor(func(ctx context.Context, state State, ids gopact.RuntimeIDs) ([]memory.Memory, error) {
			extracted++
			return []memory.Memory{
				{Type: memory.TypeProfile, Content: "this should be extracted later"},
			}, nil
		}),
		WithMemoryExtractMode(MemoryExtractDeferred),
	))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	events, err := gopacttest.CollectEvents(agent.Run(ctx, State{
		Messages: []gopact.Message{{Role: gopact.RoleUser, Content: "remember this later"}},
	}, gopact.WithRuntimeIDs(gopact.RuntimeIDs{
		RunID:    "run-1",
		UserID:   "user-1",
		ThreadID: "thread-1",
	})))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	gopacttest.RequireGoldenTrajectoryFrames(t, "testdata/memory_deferred_extract.golden.json", events)
	if extracted != 0 {
		t.Fatalf("extract count = %d, want 0", extracted)
	}
	stored, err := store.Search(ctx, memory.Query{
		Scope: memory.Scope{UserID: "user-1", ThreadID: "thread-1"},
		Text:  "later",
	})
	if err != nil {
		t.Fatalf("Search(memory) error = %v", err)
	}
	if len(stored.Memories) != 0 {
		t.Fatalf("stored memories = %+v, want none before background extraction", stored.Memories)
	}

	var completed gopact.StepSnapshot
	for _, event := range events {
		if event.Type == gopact.EventMemoryPut {
			t.Fatalf("unexpected memory put event in deferred extraction mode: %+v", event)
		}
		if event.Type == gopact.EventNodeCompleted && event.Node == nodeCallModel && event.StepSnapshot != nil {
			completed = *event.StepSnapshot
		}
	}
	if len(completed.Effects) != 1 {
		t.Fatalf("completed effects = %+v, want one memory extract effect", completed.Effects)
	}
	effect := completed.Effects[0]
	if effect.Type != memory.EffectTypeMemoryExtract || effect.Applied || effect.ReplayPolicy != gopact.EffectReplayIdempotent || effect.IdempotencyKey == "" {
		t.Fatalf("memory effect = %+v, want unapplied idempotent memory_extract", effect)
	}
	extractState, ok := effect.Metadata[memory.EffectMetadataMemoryExtractState].(State)
	if !ok {
		t.Fatalf("memory extract state metadata = %#v, want react.State", effect.Metadata[memory.EffectMetadataMemoryExtractState])
	}
	if len(extractState.Messages) == 0 || extractState.Messages[len(extractState.Messages)-1].Text() != "noted" {
		t.Fatalf("memory extract state = %+v, want final assistant message", extractState)
	}

	plan, err := gopact.PlanEffectReplay(completed)
	if err != nil {
		t.Fatalf("PlanEffectReplay() error = %v", err)
	}
	if plan.ReplayCount != 1 {
		t.Fatalf("replay count = %d, want 1", plan.ReplayCount)
	}
	replayed := 0
	if _, err := gopact.ExecuteEffectReplay(ctx, plan, memory.NewExtractionReplayHandler(memory.ExtractorFunc(func(ctx context.Context, request memory.ExtractionRequest) ([]memory.Memory, error) {
		replayed++
		gotState, ok := request.State.(State)
		if !ok {
			t.Fatalf("extract replay state = %#v, want react.State", request.State)
		}
		if len(gotState.Messages) == 0 || gotState.Messages[len(gotState.Messages)-1].Text() != "noted" {
			t.Fatalf("extract replay state = %+v, want final assistant message", gotState)
		}
		if request.IDs.UserID != "user-1" || request.IDs.ThreadID != "thread-1" || request.IDs.RunID != "run-1" {
			t.Fatalf("extract replay IDs = %+v, want runtime IDs from effect", request.IDs)
		}
		return []memory.Memory{
			{Type: memory.TypeProfile, Content: "this was extracted later"},
		}, nil
	}), store)); err != nil {
		t.Fatalf("ExecuteEffectReplay(memory_extract) error = %v", err)
	}
	if replayed != 1 {
		t.Fatalf("extract replay count = %d, want 1", replayed)
	}
	stored, err = store.Search(ctx, memory.Query{
		Scope: memory.Scope{UserID: "user-1", ThreadID: "thread-1"},
		Text:  "extracted later",
	})
	if err != nil {
		t.Fatalf("Search(after replay) error = %v", err)
	}
	if len(stored.Memories) != 1 {
		t.Fatalf("stored memories after extraction replay = %+v, want one replayed memory", stored.Memories)
	}
}

func TestPlanDeferredMemoryWorkFiltersPendingMemoryEffects(t *testing.T) {
	export := gopact.RunExport{
		Version: gopact.RunExportVersion,
		IDs:     gopact.RuntimeIDs{RunID: "run-1", ThreadID: "thread-1"},
		Outcome: gopact.RunCompleted,
		Steps: []gopact.StepSnapshot{
			{
				ID:    "step-1",
				Step:  1,
				Node:  nodeCallModel,
				Phase: gopact.StepCompleted,
				Effects: []gopact.EffectRecord{
					{
						ID:             "applied-memory",
						Type:           memory.EffectTypeMemoryPut,
						Applied:        true,
						ReplayPolicy:   gopact.EffectReplayIdempotent,
						IdempotencyKey: "memory:applied",
						Metadata: map[string]any{
							memory.EffectMetadataMemory: memory.Memory{ID: "applied", Content: "already written"},
							memoryMetadataPending:       true,
						},
					},
				},
			},
			{
				ID:    "step-2",
				Step:  2,
				Node:  nodeCallModel,
				Phase: gopact.StepCompleted,
				Effects: []gopact.EffectRecord{
					{
						ID:             "pending-put",
						Type:           memory.EffectTypeMemoryPut,
						ReplayPolicy:   gopact.EffectReplayIdempotent,
						IdempotencyKey: "memory:pending-put",
						Metadata: map[string]any{
							memory.EffectMetadataMemory: memory.Memory{ID: "pending-put", Content: "write later"},
							memoryMetadataPending:       true,
						},
					},
					{
						ID:             "pending-tool",
						Type:           "tool_call",
						ReplayPolicy:   gopact.EffectReplayIdempotent,
						IdempotencyKey: "tool:pending",
						Metadata:       map[string]any{memoryMetadataPending: true},
					},
				},
			},
			{
				ID:    "step-3",
				Step:  3,
				Node:  nodeCallModel,
				Phase: gopact.StepCompleted,
				Effects: []gopact.EffectRecord{
					{
						ID:             "pending-extract",
						Type:           memory.EffectTypeMemoryExtract,
						ReplayPolicy:   gopact.EffectReplayIdempotent,
						IdempotencyKey: "memory_extract:pending-extract",
						Metadata: map[string]any{
							memory.EffectMetadataMemoryExtractIDs:   gopact.RuntimeIDs{RunID: "run-1"},
							memory.EffectMetadataMemoryExtractState: State{},
							memoryMetadataPending:                   true,
						},
					},
				},
			},
		},
	}

	plan, err := PlanDeferredMemoryWork(export)
	if err != nil {
		t.Fatalf("PlanDeferredMemoryWork() error = %v", err)
	}
	if plan.RunID != "run-1" || plan.ThreadID != "thread-1" {
		t.Fatalf("plan IDs = %q/%q, want run/thread IDs", plan.RunID, plan.ThreadID)
	}
	if plan.ReplayCount != 2 || len(plan.Decisions) != 2 {
		t.Fatalf("plan = %+v, want two replay decisions", plan)
	}
	if plan.Decisions[0].StepID != "step-2" || plan.Decisions[0].Decision.Effect.ID != "pending-put" {
		t.Fatalf("first decision = %+v, want pending memory_put from step-2", plan.Decisions[0])
	}
	if plan.Decisions[1].StepID != "step-3" || plan.Decisions[1].Decision.Effect.ID != "pending-extract" {
		t.Fatalf("second decision = %+v, want pending memory_extract from step-3", plan.Decisions[1])
	}

	plan.Decisions[0].Decision.Effect.ID = "mutated"
	again, err := PlanDeferredMemoryWork(export)
	if err != nil {
		t.Fatalf("PlanDeferredMemoryWork(second) error = %v", err)
	}
	if again.Decisions[0].Decision.Effect.ID != "pending-put" {
		t.Fatalf("PlanDeferredMemoryWork returned mutable backing effect")
	}
}

func TestExecuteDeferredMemoryWorkUsesInjectedReplayExecutor(t *testing.T) {
	ctx := context.Background()
	store := memory.New()
	export := gopact.RunExport{
		Version: gopact.RunExportVersion,
		IDs:     gopact.RuntimeIDs{RunID: "run-1", ThreadID: "thread-1", UserID: "user-1"},
		Outcome: gopact.RunCompleted,
		Steps: []gopact.StepSnapshot{
			{
				ID:    "step-1",
				Step:  1,
				Node:  nodeCallModel,
				Phase: gopact.StepCompleted,
				Effects: []gopact.EffectRecord{
					{
						ID:             "pending-put",
						Type:           memory.EffectTypeMemoryPut,
						ReplayPolicy:   gopact.EffectReplayIdempotent,
						IdempotencyKey: "memory:pending-put",
						Metadata: map[string]any{
							memory.EffectMetadataMemory: memory.Memory{
								ID:      "pending-put",
								Type:    memory.TypeProfile,
								Content: "background replay works",
								Scope:   memory.Scope{UserID: "user-1", ThreadID: "thread-1"},
							},
							memoryMetadataPending: true,
						},
					},
				},
			},
		},
	}
	plan, err := PlanDeferredMemoryWork(export)
	if err != nil {
		t.Fatalf("PlanDeferredMemoryWork() error = %v", err)
	}

	results, err := ExecuteDeferredMemoryWork(ctx, plan, memory.NewReplayHandler(store))
	if err != nil {
		t.Fatalf("ExecuteDeferredMemoryWork() error = %v", err)
	}
	if len(results) != 1 || results[0].StepID != "step-1" || results[0].Result.EffectID != "pending-put" {
		t.Fatalf("results = %+v, want one replay result with step identity", results)
	}
	stored, err := store.Search(ctx, memory.Query{
		Scope: memory.Scope{UserID: "user-1", ThreadID: "thread-1"},
		Text:  "background replay",
	})
	if err != nil {
		t.Fatalf("Search(memory) error = %v", err)
	}
	if len(stored.Memories) != 1 {
		t.Fatalf("stored memories = %+v, want one replayed memory", stored.Memories)
	}
}

func TestAgentEmitsProviderFallbackEventsFromStreamingModel(t *testing.T) {
	ctx := context.Background()
	registry := provider.NewRegistry()
	if err := registry.Register(ctx, &provider.Fake{
		NameValue:     "primary",
		ModelsValue:   []provider.ModelInfo{{Name: "fast"}},
		GenerateError: provider.NewError(provider.ErrorRateLimited, errors.New("rate limited")),
	}); err != nil {
		t.Fatalf("Register(primary) error = %v", err)
	}
	if err := registry.Register(ctx, &provider.Fake{
		NameValue:   "fallback",
		ModelsValue: []provider.ModelInfo{{Name: "steady"}},
		Response:    provider.ResponseText("fallback response"),
	}); err != nil {
		t.Fatalf("Register(fallback) error = %v", err)
	}
	router, err := provider.NewRouter(registry, provider.RouteSet{
		Default: "coding",
		Routes: []provider.Route{
			{
				Name: "coding",
				Candidates: []provider.Candidate{
					{Provider: "primary", Model: "fast"},
					{Provider: "fallback", Model: "steady"},
				},
				Fallback: provider.FallbackPolicy{
					OnErrors:    []provider.ErrorClass{provider.ErrorRateLimited},
					MaxAttempts: 2,
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}
	agent, err := New(gopact.AdaptStreamingModel(router), nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	events, err := gopacttest.CollectEvents(agent.Run(ctx, State{
		Messages: []gopact.Message{{Role: gopact.RoleUser, Content: "hi"}},
	}, gopact.WithRuntimeIDs(gopact.RuntimeIDs{RunID: "run-1", ThreadID: "thread-1"})))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	gopacttest.RequireEventTypes(t, events,
		gopact.EventRunStarted,
		gopact.EventNodeStarted,
		gopact.EventModelRoutePlanned,
		gopact.EventModelProviderAttemptStarted,
		gopact.EventModelProviderAttemptFailed,
		gopact.EventModelProviderFallbackStarted,
		gopact.EventModelProviderAttemptStarted,
		gopact.EventModelProviderAttemptCompleted,
		gopact.EventModelMessage,
		gopact.EventNodeCompleted,
		gopact.EventRunCompleted,
	)
	gopacttest.RequireGoldenTrajectoryFrames(t, "testdata/provider_fallback.golden.json", events)
	if events[2].Node != nodeCallModel || events[2].Step != 1 {
		t.Fatalf("route event node/step = %q/%d, want call_model/1", events[2].Node, events[2].Step)
	}
	if events[5].ModelRoute.Provider != "fallback" {
		t.Fatalf("fallback route provider = %q, want fallback", events[5].ModelRoute.Provider)
	}
	if events[8].Message == nil || events[8].Message.Text() != "fallback response" {
		t.Fatalf("model message event = %+v, want fallback response", events[8])
	}

	runRecorder := gopact.NewRunRecorder()
	for _, event := range events {
		if err := runRecorder.Record(event); err != nil {
			t.Fatalf("Record(run event) error = %v", err)
		}
	}
	export, err := runRecorder.Export()
	if err != nil {
		t.Fatalf("Export(run) error = %v", err)
	}
	verificationRecorder := gopact.NewVerificationRecorder()
	if err := gopacttest.RecordGoldenTrajectoryCheck(verificationRecorder, "testdata/provider_fallback.golden.json", events); err != nil {
		t.Fatalf("RecordGoldenTrajectoryCheck() error = %v", err)
	}
	report, err := verificationRecorder.Report(export)
	if err != nil {
		t.Fatalf("Report() error = %v", err)
	}
	if report.Status != gopact.VerificationStatusPassed || report.PassedCount != 1 {
		t.Fatalf("verification report status/count = %q/%d, want passed/1", report.Status, report.PassedCount)
	}
	if len(report.Checks) != 1 || report.Checks[0].Evidence[0].Type != gopacttest.VerificationEvidenceTypeTrajectoryGolden {
		t.Fatalf("verification report checks = %+v, want trajectory golden evidence", report.Checks)
	}
}

func TestAgentRunsVerifierBeforeCompletingRun(t *testing.T) {
	ctx := context.Background()
	verified := false
	agent, err := New(&scriptedModel{
		responses: []gopact.Message{{Role: gopact.RoleAssistant, Content: "done"}},
	}, nil, WithVerifier(func(ctx context.Context, export gopact.RunExport, recorder *gopact.VerificationRecorder) error {
		verified = true
		if export.Outcome != gopact.RunCompleted {
			t.Fatalf("verification export outcome = %q, want completed", export.Outcome)
		}
		if len(export.Events) == 0 || export.Events[len(export.Events)-1].Type != gopact.EventRunCompleted {
			t.Fatalf("verification export events = %+v, want terminal run_completed", export.Events)
		}
		if len(export.Steps) != 1 || export.Steps[0].Node != nodeCallModel {
			t.Fatalf("verification export steps = %+v, want completed model step only", export.Steps)
		}
		if len(export.Tasks) != 1 {
			t.Fatalf("verification export tasks = %+v, want one template task record", export.Tasks)
		}
		task := export.Tasks[0]
		if task.ID != "run-1:task" || task.Name != "react" || task.Status != gopact.TaskCompleted {
			t.Fatalf("verification export task = %+v, want completed react task", task)
		}
		if task.IDs.RunID != "run-1" || task.IDs.ThreadID != "thread-1" {
			t.Fatalf("verification export task ids = %+v, want run/thread ids", task.IDs)
		}
		output, ok := task.Output.(State)
		if !ok || len(output.Messages) != 2 || output.Messages[1].Content != "done" {
			t.Fatalf("verification export task output = %#v, want final react state", task.Output)
		}
		if len(export.Inputs) != 1 {
			t.Fatalf("verification export inputs = %+v, want one run input record", export.Inputs)
		}
		input := export.Inputs[0]
		if input.ID != "run-1:input" || input.Kind != gopact.InputUser || input.Source != "react.run" {
			t.Fatalf("verification export input = %+v, want user run input", input)
		}
		value, ok := input.Value.(State)
		if !ok || len(value.Messages) != 1 || value.Messages[0].Content != "hi" {
			t.Fatalf("verification export input value = %#v, want original run state", input.Value)
		}
		return recorder.Record(gopact.VerificationCheck{
			ID:       "template-gate",
			Status:   gopact.VerificationStatusPassed,
			Evidence: []gopact.VerificationEvidence{{Type: "unit", Ref: "react verifier", Summary: "passed"}},
		})
	}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	events, err := gopacttest.CollectEvents(agent.Run(ctx, State{
		Messages: []gopact.Message{{Role: gopact.RoleUser, Content: "hi"}},
	}, gopact.WithRuntimeIDs(gopact.RuntimeIDs{RunID: "run-1", ThreadID: "thread-1"})))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !verified {
		t.Fatal("verifier was not called")
	}
	gopacttest.RequireEventTypes(t, events,
		gopact.EventRunStarted,
		gopact.EventNodeStarted,
		gopact.EventModelMessage,
		gopact.EventNodeCompleted,
		gopact.EventNodeStarted,
		gopact.EventNodeCompleted,
		gopact.EventRunCompleted,
	)
	gopacttest.RequireGoldenTrajectoryFrames(t, "testdata/verifier_passed_report.golden.json", events)
	if events[4].Node != nodeVerify || events[4].Step != 2 {
		t.Fatalf("verification started node/step = %s/%d, want verify/2", events[4].Node, events[4].Step)
	}
	report, ok := events[5].Metadata[gopact.EventMetadataVerificationReport].(gopact.VerificationReport)
	if !ok {
		t.Fatalf("verification metadata = %+v, want verification report", events[5].Metadata)
	}
	if report.Status != gopact.VerificationStatusPassed || report.PassedCount != 1 {
		t.Fatalf("verification report status/count = %q/%d, want passed/1", report.Status, report.PassedCount)
	}
	if got, ok := events[5].StepSnapshot.Output.(gopact.VerificationReport); !ok || got.Status != gopact.VerificationStatusPassed {
		t.Fatalf("verification step output = %#v, want passed report", events[5].StepSnapshot.Output)
	}

	runRecorder := gopact.NewRunRecorder()
	for _, event := range events {
		if err := runRecorder.Record(event); err != nil {
			t.Fatalf("Record(%s) error = %v", event.Type, err)
		}
	}
	runExport, err := runRecorder.Export()
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	if len(runExport.VerificationReports) != 1 || runExport.VerificationReports[0].Status != gopact.VerificationStatusPassed {
		t.Fatalf("run export verification reports = %+v, want passed report", runExport.VerificationReports)
	}
}

func TestAgentVerificationExportRecordsResumeInterventionProcessRecords(t *testing.T) {
	ctx := context.Background()
	toolInvoked := 0
	verified := false
	policy := gopact.PolicyFunc(func(ctx context.Context, req gopact.PolicyRequest) (gopact.PolicyDecision, error) {
		resume, ok := req.Metadata[gopact.MetadataResumeRequest].(gopact.ResumeRequest)
		if ok {
			payload, ok := resume.Payload.(map[string]any)
			if ok && payload["approved"] == true {
				return gopact.PolicyDecision{Action: gopact.PolicyAllow, Reason: "approved"}, nil
			}
		}
		return gopact.PolicyDecision{Action: gopact.PolicyReview, Reason: "needs approval"}, nil
	})
	registry := tools.NewRegistry(tools.WithToolMiddleware(gopact.ToolPolicyMiddleware(policy)))
	if err := registry.Register(ctx, gopact.ToolFunc{
		SpecValue: gopact.ToolSpec{Name: "echo", Description: "echoes input"},
		InvokeFunc: func(ctx context.Context, args json.RawMessage) (gopact.ToolResult, error) {
			toolInvoked++
			return gopact.ToolResult{Content: "hello"}, nil
		},
	}, tools.RegisterOptions{Namespace: "local", Visibility: tools.VisibleTool}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	model := &scriptedModel{
		responses: []gopact.Message{
			{
				Role: gopact.RoleAssistant,
				ToolCalls: []gopact.ToolCall{
					{ID: "call-1", Name: "local.echo", Arguments: []byte(`{"text":"hello"}`)},
				},
			},
			{Role: gopact.RoleAssistant, Content: "final"},
		},
	}
	agent, err := New(model, registry, WithVerifier(func(ctx context.Context, export gopact.RunExport, recorder *gopact.VerificationRecorder) error {
		verified = true
		if len(export.Inputs) != 1 {
			t.Fatalf("verification export inputs = %+v, want one resume input record", export.Inputs)
		}
		input := export.Inputs[0]
		if input.ID != "run-2:resume:policy:call-1" || input.Kind != gopact.InputResume || input.Source != "react.resume" {
			t.Fatalf("verification export resume input = %+v, want resume input", input)
		}
		if input.Resume == nil || input.Resume.InterruptID != "policy:call-1" {
			t.Fatalf("verification export resume input request = %+v, want policy:call-1", input.Resume)
		}
		if len(export.Interventions) != 1 {
			t.Fatalf("verification export interventions = %+v, want one resolved intervention", export.Interventions)
		}
		intervention := export.Interventions[0]
		if intervention.ID != "policy:call-1" || intervention.Type != gopact.InterruptApproval || intervention.Status != gopact.InterventionResolved {
			t.Fatalf("verification export intervention = %+v, want resolved approval", intervention)
		}
		if intervention.Request == nil || intervention.Request.ID != "policy:call-1" {
			t.Fatalf("verification export intervention request = %+v, want pending request", intervention.Request)
		}
		if intervention.Resume == nil || intervention.Resume.InterruptID != "policy:call-1" {
			t.Fatalf("verification export intervention resume = %+v, want resume request", intervention.Resume)
		}
		return recorder.Record(gopact.VerificationCheck{
			ID:       "template-gate",
			Status:   gopact.VerificationStatusPassed,
			Evidence: []gopact.VerificationEvidence{{Type: "unit", Ref: "react verifier", Summary: "passed"}},
		})
	}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	interruptedEvents, err := gopacttest.CollectEvents(agent.Run(ctx, State{
		Messages: []gopact.Message{{Role: gopact.RoleUser, Content: "hi"}},
	}, gopact.WithRuntimeIDs(gopact.RuntimeIDs{RunID: "run-1", ThreadID: "thread-1"})))
	if !errors.Is(err, gopact.ErrInterrupted) {
		t.Fatalf("first Run() error = %v, want ErrInterrupted", err)
	}
	if toolInvoked != 0 {
		t.Fatalf("tool invoked before approval = %d, want 0", toolInvoked)
	}
	recorder := gopact.NewRunRecorder()
	for _, event := range interruptedEvents {
		if err := recorder.Record(event); err != nil {
			t.Fatalf("Record(interrupted) error = %v", err)
		}
	}
	export, err := recorder.Export()
	if err != nil {
		t.Fatalf("Export(interrupted) error = %v", err)
	}
	if len(export.Steps) != 2 || export.Steps[1].Pending == nil {
		t.Fatalf("interrupted export steps = %+v, want pending tool step", export.Steps)
	}
	step := gopact.StepExport{Version: gopact.RunExportVersion, Step: export.Steps[1]}
	resume := gopact.ResumeRequest{
		StepID:      step.Step.ID,
		InterruptID: "policy:call-1",
		Payload:     map[string]any{"approved": true},
	}

	_, err = gopacttest.CollectEvents(agent.Run(ctx, State{},
		gopact.WithRuntimeIDs(gopact.RuntimeIDs{RunID: "run-2", ThreadID: "thread-1"}),
		gopact.WithStepExport(step),
		gopact.WithResumeRequest(resume),
	))
	if err != nil {
		t.Fatalf("resume Run() error = %v", err)
	}
	if !verified {
		t.Fatal("verifier was not called")
	}
	if toolInvoked != 1 {
		t.Fatalf("tool invoked after approval = %d, want 1", toolInvoked)
	}
}

func TestAgentVerifierFailedCheckFailsRun(t *testing.T) {
	ctx := context.Background()
	agent, err := New(&scriptedModel{
		responses: []gopact.Message{{Role: gopact.RoleAssistant, Content: "done"}},
	}, nil, WithVerifier(func(ctx context.Context, export gopact.RunExport, recorder *gopact.VerificationRecorder) error {
		return recorder.Record(gopact.VerificationCheck{
			ID:       "template-gate",
			Status:   gopact.VerificationStatusFailed,
			Evidence: []gopact.VerificationEvidence{{Type: "unit", Ref: "react verifier", Summary: "failed"}},
		})
	}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	events, err := gopacttest.CollectEvents(agent.Run(ctx, State{
		Messages: []gopact.Message{{Role: gopact.RoleUser, Content: "hi"}},
	}, gopact.WithRuntimeIDs(gopact.RuntimeIDs{RunID: "run-1", ThreadID: "thread-1"})))
	if !errors.Is(err, ErrVerificationFailed) {
		t.Fatalf("Run() error = %v, want ErrVerificationFailed", err)
	}
	gopacttest.RequireEventTypes(t, events,
		gopact.EventRunStarted,
		gopact.EventNodeStarted,
		gopact.EventModelMessage,
		gopact.EventNodeCompleted,
		gopact.EventNodeStarted,
		gopact.EventNodeFailed,
		gopact.EventRunFailed,
	)
	gopacttest.RequireGoldenTrajectoryFrames(t, "testdata/verifier_failed_report.golden.json", events)
	if events[5].Node != nodeVerify || events[5].StepSnapshot == nil || events[5].StepSnapshot.Phase != gopact.StepFailed {
		t.Fatalf("verification failure event = %+v, want failed verify node", events[5])
	}
	report, ok := events[5].Metadata[gopact.EventMetadataVerificationReport].(gopact.VerificationReport)
	if !ok || report.Status != gopact.VerificationStatusFailed {
		t.Fatalf("verification metadata = %+v, want failed report", events[5].Metadata)
	}

	recorder := gopact.NewRunRecorder()
	for _, event := range events {
		if err := recorder.Record(event); err != nil {
			t.Fatalf("Record(%s) error = %v", event.Type, err)
		}
	}
	export, err := recorder.Export()
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	if len(export.VerificationReports) != 1 || export.VerificationReports[0].Status != gopact.VerificationStatusFailed {
		t.Fatalf("run export verification reports = %+v, want failed report", export.VerificationReports)
	}
	if len(export.Failures) != 1 || export.Failures[0].Kind != gopact.FailureVerification {
		t.Fatalf("run export failures = %+v, want verification failure", export.Failures)
	}
}

func TestAgentVerifierErrorWithoutChecksIsVerificationFailure(t *testing.T) {
	ctx := context.Background()
	verifierErr := errors.New("verifier crashed")
	agent, err := New(&scriptedModel{
		responses: []gopact.Message{{Role: gopact.RoleAssistant, Content: "done"}},
	}, nil, WithVerifier(func(ctx context.Context, export gopact.RunExport, recorder *gopact.VerificationRecorder) error {
		return verifierErr
	}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	events, err := gopacttest.CollectEvents(agent.Run(ctx, State{
		Messages: []gopact.Message{{Role: gopact.RoleUser, Content: "hi"}},
	}, gopact.WithRuntimeIDs(gopact.RuntimeIDs{RunID: "run-1"})))
	if !errors.Is(err, ErrVerificationFailed) || !errors.Is(err, verifierErr) {
		t.Fatalf("Run() error = %v, want ErrVerificationFailed and verifier error", err)
	}
	gopacttest.RequireEventTypes(t, events,
		gopact.EventRunStarted,
		gopact.EventNodeStarted,
		gopact.EventModelMessage,
		gopact.EventNodeCompleted,
		gopact.EventNodeStarted,
		gopact.EventNodeFailed,
		gopact.EventRunFailed,
	)
	gopacttest.RequireGoldenTrajectoryFrames(t, "testdata/verifier_error.golden.json", events)
}

func TestAgentRecordsToolArtifactsInEventsAndRunExport(t *testing.T) {
	ctx := context.Background()
	artifactRef := gopact.ArtifactRef{
		ID:       "artifact-1",
		Name:     "result.json",
		URI:      "memory://artifact-1",
		MIMEType: "application/json",
		Scope:    gopact.ArtifactScopeRun,
	}
	registry := tools.NewRegistry()
	if err := registry.Register(ctx, gopact.ToolFunc{
		SpecValue: gopact.ToolSpec{Name: "write_artifact", Description: "writes an artifact"},
		InvokeFunc: func(ctx context.Context, args json.RawMessage) (gopact.ToolResult, error) {
			return gopact.ToolResult{
				Content:   "artifact ready",
				Artifacts: []gopact.ArtifactRef{artifactRef},
			}, nil
		},
	}, tools.RegisterOptions{Namespace: "local", Visibility: tools.VisibleTool}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	model := &scriptedModel{
		responses: []gopact.Message{
			{
				Role: gopact.RoleAssistant,
				ToolCalls: []gopact.ToolCall{
					{ID: "call-1", Name: "local.write_artifact", Arguments: []byte(`{}`)},
				},
			},
			{Role: gopact.RoleAssistant, Content: "final"},
		},
	}
	agent, err := New(model, registry)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	events, err := gopacttest.CollectEvents(agent.Run(ctx, State{
		Messages: []gopact.Message{{Role: gopact.RoleUser, Content: "write"}},
	}, gopact.WithRuntimeIDs(gopact.RuntimeIDs{RunID: "run-1"})))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	gopacttest.RequireGoldenTrajectoryFrames(t, "testdata/tool_artifact_result.golden.json", events)
	var toolResultEvent gopact.Event
	var toolStep gopact.StepSnapshot
	for _, event := range events {
		if event.Type == gopact.EventToolResult {
			toolResultEvent = event
		}
		if event.Type == gopact.EventNodeCompleted && event.Node == nodeCallTool && event.StepSnapshot != nil {
			toolStep = *event.StepSnapshot
		}
	}
	if len(toolResultEvent.Artifacts) != 1 || toolResultEvent.Artifacts[0].ID != artifactRef.ID {
		t.Fatalf("tool result event artifacts = %+v, want artifact ref", toolResultEvent.Artifacts)
	}
	if toolResultEvent.Result == nil || len(toolResultEvent.Result.Artifacts) != 1 || toolResultEvent.Result.Artifacts[0].ID != artifactRef.ID {
		t.Fatalf("tool result payload = %+v, want artifact ref", toolResultEvent.Result)
	}
	if len(toolStep.Artifacts) != 1 || toolStep.Artifacts[0].ID != artifactRef.ID {
		t.Fatalf("tool step artifacts = %+v, want artifact ref", toolStep.Artifacts)
	}

	recorder := gopact.NewRunRecorder()
	for _, event := range events {
		if err := recorder.Record(event); err != nil {
			t.Fatalf("Record() error = %v", err)
		}
	}
	export, err := recorder.Export()
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	if len(export.Steps) < 2 || len(export.Steps[1].Artifacts) != 1 || export.Steps[1].Artifacts[0].ID != artifactRef.ID {
		t.Fatalf("exported tool step artifacts = %+v, want artifact ref", export.Steps)
	}
}

func TestAgentResumesToolsFromCompletedModelStepExport(t *testing.T) {
	ctx := context.Background()
	toolInvoked := 0
	registry := tools.NewRegistry()
	if err := registry.Register(ctx, gopact.ToolFunc{
		SpecValue: gopact.ToolSpec{Name: "echo", Description: "echoes input"},
		InvokeFunc: func(ctx context.Context, args json.RawMessage) (gopact.ToolResult, error) {
			toolInvoked++
			return gopact.ToolResult{Content: "hello"}, nil
		},
	}, tools.RegisterOptions{Namespace: "local", Visibility: tools.VisibleTool}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	model := &scriptedModel{
		responses: []gopact.Message{
			{
				Role: gopact.RoleAssistant,
				ToolCalls: []gopact.ToolCall{
					{ID: "call-1", Name: "local.echo", Arguments: []byte(`{"text":"hello"}`)},
				},
			},
			{Role: gopact.RoleAssistant, Content: "final"},
		},
	}
	agent, err := New(model, registry)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	var modelStep gopact.StepSnapshot
	for event, err := range agent.Run(ctx, State{
		Messages: []gopact.Message{{Role: gopact.RoleUser, Content: "hi"}},
	}, gopact.WithRuntimeIDs(gopact.RuntimeIDs{RunID: "run-1", ThreadID: "thread-1"})) {
		if err != nil {
			t.Fatalf("initial Run() error = %v", err)
		}
		if event.Type == gopact.EventNodeCompleted && event.Node == nodeCallModel {
			if event.StepSnapshot == nil {
				t.Fatal("completed model event StepSnapshot = nil")
			}
			modelStep = *event.StepSnapshot
			break
		}
	}
	if modelStep.ID == "" {
		t.Fatal("completed model step was not captured")
	}
	if toolInvoked != 0 {
		t.Fatalf("tool invoked before export = %d, want 0", toolInvoked)
	}
	if len(model.requests) != 1 {
		t.Fatalf("model request count before resume = %d, want 1", len(model.requests))
	}
	step := gopact.StepExport{Version: gopact.RunExportVersion, Step: modelStep}

	resumedEvents, err := gopacttest.CollectEvents(agent.Run(ctx, State{}, gopact.WithStepExport(step)))
	if err != nil {
		t.Fatalf("resume Run() error = %v", err)
	}
	if toolInvoked != 1 {
		t.Fatalf("tool invoked after resume = %d, want 1", toolInvoked)
	}
	gopacttest.RequireEventTypes(t, resumedEvents,
		gopact.EventRunStarted,
		gopact.EventStepImported,
		gopact.EventNodeResumed,
		gopact.EventToolCall,
		gopact.EventToolResult,
		gopact.EventNodeCompleted,
		gopact.EventNodeStarted,
		gopact.EventModelMessage,
		gopact.EventNodeCompleted,
		gopact.EventRunCompleted,
	)
	gopacttest.RequireGoldenTrajectoryFrames(t, "testdata/completed_model_step_export_resume.golden.json", resumedEvents)
	if resumedEvents[2].Node != nodeCallTool || resumedEvents[2].Step != 2 {
		t.Fatalf("resumed node = %s/%d, want call_tool/2", resumedEvents[2].Node, resumedEvents[2].Step)
	}
	if len(model.requests) != 2 {
		t.Fatalf("model request count after resume = %d, want 2", len(model.requests))
	}
	messages := model.requests[1].Messages
	if len(messages) == 0 || messages[len(messages)-1].Role != gopact.RoleTool || messages[len(messages)-1].ToolCallID != "call-1" {
		t.Fatalf("resumed model messages = %+v, want tool result included", messages)
	}
}

func TestAgentResumesModelFromCompletedToolStepExport(t *testing.T) {
	ctx := context.Background()
	toolInvoked := 0
	registry := tools.NewRegistry()
	if err := registry.Register(ctx, gopact.ToolFunc{
		SpecValue: gopact.ToolSpec{Name: "echo", Description: "echoes input"},
		InvokeFunc: func(ctx context.Context, args json.RawMessage) (gopact.ToolResult, error) {
			toolInvoked++
			return gopact.ToolResult{Content: "hello"}, nil
		},
	}, tools.RegisterOptions{Namespace: "local", Visibility: tools.VisibleTool}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	model := &scriptedModel{
		responses: []gopact.Message{
			{
				Role: gopact.RoleAssistant,
				ToolCalls: []gopact.ToolCall{
					{ID: "call-1", Name: "local.echo", Arguments: []byte(`{"text":"hello"}`)},
				},
			},
			{Role: gopact.RoleAssistant, Content: "final"},
		},
	}
	agent, err := New(model, registry)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	var toolStep gopact.StepSnapshot
	for event, err := range agent.Run(ctx, State{
		Messages: []gopact.Message{{Role: gopact.RoleUser, Content: "hi"}},
	}, gopact.WithRuntimeIDs(gopact.RuntimeIDs{RunID: "run-1", ThreadID: "thread-1"})) {
		if err != nil {
			t.Fatalf("initial Run() error = %v", err)
		}
		if event.Type == gopact.EventNodeCompleted && event.Node == nodeCallTool {
			if event.StepSnapshot == nil {
				t.Fatal("completed tool event StepSnapshot = nil")
			}
			toolStep = *event.StepSnapshot
			break
		}
	}
	if toolStep.ID == "" {
		t.Fatal("completed tool step was not captured")
	}
	if toolInvoked != 1 {
		t.Fatalf("tool invoked before resume = %d, want 1", toolInvoked)
	}
	step := gopact.StepExport{Version: gopact.RunExportVersion, Step: toolStep}

	resumedEvents, err := gopacttest.CollectEvents(agent.Run(ctx, State{}, gopact.WithStepExport(step)))
	if err != nil {
		t.Fatalf("resume Run() error = %v", err)
	}
	if toolInvoked != 1 {
		t.Fatalf("tool reinvoked after resume = %d, want still 1", toolInvoked)
	}
	gopacttest.RequireEventTypes(t, resumedEvents,
		gopact.EventRunStarted,
		gopact.EventStepImported,
		gopact.EventNodeResumed,
		gopact.EventModelMessage,
		gopact.EventNodeCompleted,
		gopact.EventRunCompleted,
	)
	gopacttest.RequireGoldenTrajectoryFrames(t, "testdata/completed_tool_step_export_resume.golden.json", resumedEvents)
	if resumedEvents[2].Node != nodeCallModel || resumedEvents[2].Step != 3 {
		t.Fatalf("resumed node = %s/%d, want call_model/3", resumedEvents[2].Node, resumedEvents[2].Step)
	}
	if len(model.requests) != 2 {
		t.Fatalf("model request count after resume = %d, want 2", len(model.requests))
	}
	messages := model.requests[1].Messages
	if len(messages) == 0 || messages[len(messages)-1].Role != gopact.RoleTool || messages[len(messages)-1].ToolCallID != "call-1" {
		t.Fatalf("resumed model messages = %+v, want imported tool result included", messages)
	}
}

func TestAgentCheckpointStoreResumesToolsFromCompletedModelStep(t *testing.T) {
	ctx := context.Background()
	store := checkpoint.NewMemory[State]()
	toolInvoked := 0
	registry := tools.NewRegistry()
	if err := registry.Register(ctx, gopact.ToolFunc{
		SpecValue: gopact.ToolSpec{Name: "echo", Description: "echoes input"},
		InvokeFunc: func(ctx context.Context, args json.RawMessage) (gopact.ToolResult, error) {
			toolInvoked++
			return gopact.ToolResult{Content: "hello"}, nil
		},
	}, tools.RegisterOptions{Namespace: "local", Visibility: tools.VisibleTool}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	initialModel := &scriptedModel{
		responses: []gopact.Message{
			{
				Role: gopact.RoleAssistant,
				ToolCalls: []gopact.ToolCall{
					{ID: "call-1", Name: "local.echo", Arguments: []byte(`{"text":"hello"}`)},
				},
			},
		},
	}
	initialAgent, err := New(initialModel, registry, WithCheckpointStore(store))
	if err != nil {
		t.Fatalf("New(initial) error = %v", err)
	}

	for event, err := range initialAgent.Run(ctx, State{
		Messages: []gopact.Message{{Role: gopact.RoleUser, Content: "hi"}},
	}, gopact.WithRuntimeIDs(gopact.RuntimeIDs{RunID: "run-1", ThreadID: "thread-1"})) {
		if err != nil {
			t.Fatalf("initial Run() error = %v", err)
		}
		if event.Type == gopact.EventNodeStarted && event.Node == nodeCallTool {
			break
		}
	}
	if toolInvoked != 0 {
		t.Fatalf("tool invoked before resume = %d, want 0", toolInvoked)
	}

	resumeModel := &scriptedModel{
		responses: []gopact.Message{{Role: gopact.RoleAssistant, Content: "final"}},
	}
	resumeAgent, err := New(resumeModel, registry, WithCheckpointStore(store))
	if err != nil {
		t.Fatalf("New(resume) error = %v", err)
	}
	resumedEvents, err := gopacttest.CollectEvents(resumeAgent.Run(ctx, State{}, gopact.WithRuntimeIDs(gopact.RuntimeIDs{
		RunID:    "run-2",
		ThreadID: "thread-1",
	})))
	if err != nil {
		t.Fatalf("resume Run() error = %v", err)
	}
	if toolInvoked != 1 {
		t.Fatalf("tool invoked after resume = %d, want 1", toolInvoked)
	}
	gopacttest.RequireEventTypes(t, resumedEvents,
		gopact.EventRunStarted,
		gopact.EventCheckpointLoaded,
		gopact.EventNodeResumed,
		gopact.EventToolCall,
		gopact.EventToolResult,
		gopact.EventNodeCompleted,
		gopact.EventNodeStarted,
		gopact.EventModelMessage,
		gopact.EventNodeCompleted,
		gopact.EventRunCompleted,
	)
	gopacttest.RequireGoldenTrajectoryFrames(t, "testdata/completed_model_checkpoint_resume.golden.json", resumedEvents)
	if resumedEvents[1].Node != nodeCallModel || resumedEvents[1].Step != 1 {
		t.Fatalf("loaded checkpoint node/step = %s/%d, want call_model/1", resumedEvents[1].Node, resumedEvents[1].Step)
	}
	if resumedEvents[1].Metadata["checkpoint_id"] == "" {
		t.Fatalf("checkpoint loaded metadata = %+v, want checkpoint_id", resumedEvents[1].Metadata)
	}
	if resumedEvents[2].Node != nodeCallTool || resumedEvents[2].Step != 2 {
		t.Fatalf("resumed node = %s/%d, want call_tool/2", resumedEvents[2].Node, resumedEvents[2].Step)
	}
	if len(resumeModel.requests) != 1 {
		t.Fatalf("resume model request count = %d, want 1", len(resumeModel.requests))
	}
}

func TestAgentCheckpointStoreResumesModelFromCompletedToolStep(t *testing.T) {
	ctx := context.Background()
	store := checkpoint.NewMemory[State]()
	toolInvoked := 0
	registry := tools.NewRegistry()
	if err := registry.Register(ctx, gopact.ToolFunc{
		SpecValue: gopact.ToolSpec{Name: "echo", Description: "echoes input"},
		InvokeFunc: func(ctx context.Context, args json.RawMessage) (gopact.ToolResult, error) {
			toolInvoked++
			return gopact.ToolResult{Content: "hello"}, nil
		},
	}, tools.RegisterOptions{Namespace: "local", Visibility: tools.VisibleTool}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	initialModel := &scriptedModel{
		responses: []gopact.Message{
			{
				Role: gopact.RoleAssistant,
				ToolCalls: []gopact.ToolCall{
					{ID: "call-1", Name: "local.echo", Arguments: []byte(`{"text":"hello"}`)},
				},
			},
		},
	}
	initialAgent, err := New(initialModel, registry, WithCheckpointStore(store))
	if err != nil {
		t.Fatalf("New(initial) error = %v", err)
	}

	for event, err := range initialAgent.Run(ctx, State{
		Messages: []gopact.Message{{Role: gopact.RoleUser, Content: "hi"}},
	}, gopact.WithRuntimeIDs(gopact.RuntimeIDs{RunID: "run-1", ThreadID: "thread-1"})) {
		if err != nil {
			t.Fatalf("initial Run() error = %v", err)
		}
		if event.Type == gopact.EventNodeStarted && event.Node == nodeCallModel && event.Step == 3 {
			break
		}
	}
	if toolInvoked != 1 {
		t.Fatalf("tool invoked before resume = %d, want 1", toolInvoked)
	}

	resumeModel := &scriptedModel{
		responses: []gopact.Message{{Role: gopact.RoleAssistant, Content: "final"}},
	}
	resumeAgent, err := New(resumeModel, registry, WithCheckpointStore(store))
	if err != nil {
		t.Fatalf("New(resume) error = %v", err)
	}
	resumedEvents, err := gopacttest.CollectEvents(resumeAgent.Run(ctx, State{}, gopact.WithRuntimeIDs(gopact.RuntimeIDs{
		RunID:    "run-2",
		ThreadID: "thread-1",
	})))
	if err != nil {
		t.Fatalf("resume Run() error = %v", err)
	}
	if toolInvoked != 1 {
		t.Fatalf("tool reinvoked after resume = %d, want still 1", toolInvoked)
	}
	gopacttest.RequireEventTypes(t, resumedEvents,
		gopact.EventRunStarted,
		gopact.EventCheckpointLoaded,
		gopact.EventNodeResumed,
		gopact.EventModelMessage,
		gopact.EventNodeCompleted,
		gopact.EventRunCompleted,
	)
	gopacttest.RequireGoldenTrajectoryFrames(t, "testdata/completed_tool_checkpoint_resume.golden.json", resumedEvents)
	if resumedEvents[1].Node != nodeCallTool || resumedEvents[1].Step != 2 {
		t.Fatalf("loaded checkpoint node/step = %s/%d, want call_tool/2", resumedEvents[1].Node, resumedEvents[1].Step)
	}
	if resumedEvents[2].Node != nodeCallModel || resumedEvents[2].Step != 3 {
		t.Fatalf("resumed node = %s/%d, want call_model/3", resumedEvents[2].Node, resumedEvents[2].Step)
	}
	if len(resumeModel.requests) != 1 {
		t.Fatalf("resume model request count = %d, want 1", len(resumeModel.requests))
	}
	messages := resumeModel.requests[0].Messages
	if len(messages) == 0 || messages[len(messages)-1].Role != gopact.RoleTool || messages[len(messages)-1].ToolCallID != "call-1" {
		t.Fatalf("resumed model messages = %+v, want imported tool result included", messages)
	}
}

func TestAgentCheckpointStoreResumesToolApprovalFromInterruptedCheckpoint(t *testing.T) {
	ctx := context.Background()
	store := checkpoint.NewMemory[State]()
	toolInvoked := 0
	policy := gopact.PolicyFunc(func(ctx context.Context, req gopact.PolicyRequest) (gopact.PolicyDecision, error) {
		resume, ok := req.Metadata[gopact.MetadataResumeRequest].(gopact.ResumeRequest)
		if ok {
			payload, ok := resume.Payload.(map[string]any)
			if ok && payload["approved"] == true {
				return gopact.PolicyDecision{Action: gopact.PolicyAllow, Reason: "approved"}, nil
			}
		}
		return gopact.PolicyDecision{Action: gopact.PolicyReview, Reason: "needs approval"}, nil
	})
	registry := tools.NewRegistry(tools.WithToolMiddleware(gopact.ToolPolicyMiddleware(policy)))
	if err := registry.Register(ctx, gopact.ToolFunc{
		SpecValue: gopact.ToolSpec{Name: "echo", Description: "echoes input"},
		InvokeFunc: func(ctx context.Context, args json.RawMessage) (gopact.ToolResult, error) {
			toolInvoked++
			return gopact.ToolResult{Content: "hello"}, nil
		},
	}, tools.RegisterOptions{Namespace: "local", Visibility: tools.VisibleTool}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	initialModel := &scriptedModel{
		responses: []gopact.Message{
			{
				Role: gopact.RoleAssistant,
				ToolCalls: []gopact.ToolCall{
					{ID: "call-1", Name: "local.echo", Arguments: []byte(`{"text":"hello"}`)},
				},
			},
		},
	}
	initialAgent, err := New(initialModel, registry, WithCheckpointStore(store))
	if err != nil {
		t.Fatalf("New(initial) error = %v", err)
	}

	interruptedEvents, err := gopacttest.CollectEvents(initialAgent.Run(ctx, State{
		Messages: []gopact.Message{{Role: gopact.RoleUser, Content: "hi"}},
	}, gopact.WithRuntimeIDs(gopact.RuntimeIDs{RunID: "run-1", ThreadID: "thread-1"})))
	if !errors.Is(err, gopact.ErrInterrupted) {
		t.Fatalf("initial Run() error = %v, want ErrInterrupted", err)
	}
	if toolInvoked != 0 {
		t.Fatalf("tool invoked before approval = %d, want 0", toolInvoked)
	}
	gopacttest.RequireEventTypes(t, interruptedEvents,
		gopact.EventRunStarted,
		gopact.EventNodeStarted,
		gopact.EventModelMessage,
		gopact.EventNodeCompleted,
		gopact.EventNodeStarted,
		gopact.EventToolCall,
		gopact.EventPolicyRequested,
		gopact.EventPolicyDecided,
		gopact.EventInterrupted,
		gopact.EventRunInterrupted,
	)
	checkpoints := store.List(ctx, "thread-1")
	if len(checkpoints) != 2 {
		t.Fatalf("checkpoint count = %d, want completed model + interrupted tool", len(checkpoints))
	}
	interrupted := checkpoints[1]
	if interrupted.Phase != gopact.StepInterrupted || interrupted.Pending == nil || interrupted.Pending.ID != "policy:call-1" {
		t.Fatalf("interrupted checkpoint = %+v, want pending approval", interrupted)
	}
	if len(interrupted.Queue) != 1 || interrupted.Queue[0] != nodeCallTool {
		t.Fatalf("interrupted checkpoint queue = %v, want call_tool", interrupted.Queue)
	}

	resumeModel := &scriptedModel{
		responses: []gopact.Message{{Role: gopact.RoleAssistant, Content: "final"}},
	}
	resumeAgent, err := New(resumeModel, registry, WithCheckpointStore(store))
	if err != nil {
		t.Fatalf("New(resume) error = %v", err)
	}
	resume := gopact.ResumeRequest{
		CheckpointID: interrupted.ID,
		InterruptID:  "policy:call-1",
		Payload:      map[string]any{"approved": true},
	}

	resumedEvents, err := gopacttest.CollectEvents(resumeAgent.Run(ctx, State{}, gopact.WithRuntimeIDs(gopact.RuntimeIDs{
		RunID:    "run-2",
		ThreadID: "thread-1",
	}), gopact.WithResumeRequest(resume)))
	if err != nil {
		t.Fatalf("resume Run() error = %v", err)
	}
	if toolInvoked != 1 {
		t.Fatalf("tool invoked after approval = %d, want 1", toolInvoked)
	}
	gopacttest.RequireEventTypes(t, resumedEvents,
		gopact.EventRunStarted,
		gopact.EventCheckpointLoaded,
		gopact.EventResumeReceived,
		gopact.EventNodeResumed,
		gopact.EventToolCall,
		gopact.EventPolicyRequested,
		gopact.EventPolicyDecided,
		gopact.EventToolResult,
		gopact.EventNodeCompleted,
		gopact.EventNodeStarted,
		gopact.EventModelMessage,
		gopact.EventNodeCompleted,
		gopact.EventRunCompleted,
	)
	gopacttest.RequireGoldenTrajectoryFrames(t, "testdata/interrupted_checkpoint_resume.golden.json", resumedEvents)
	if resumedEvents[1].Node != nodeCallTool || resumedEvents[1].Step != 2 {
		t.Fatalf("loaded checkpoint node/step = %s/%d, want call_tool/2", resumedEvents[1].Node, resumedEvents[1].Step)
	}
	if resumedEvents[1].Metadata["checkpoint_id"] != interrupted.ID {
		t.Fatalf("checkpoint loaded metadata = %+v, want checkpoint id %q", resumedEvents[1].Metadata, interrupted.ID)
	}
	if resumedEvents[2].Metadata["checkpoint_id"] != interrupted.ID || resumedEvents[2].Metadata["interrupt_id"] != "policy:call-1" {
		t.Fatalf("resume event metadata = %+v, want checkpoint and interrupt ids", resumedEvents[2].Metadata)
	}
	if resumedEvents[3].Node != nodeCallTool || resumedEvents[3].Step != 3 {
		t.Fatalf("resumed node = %s/%d, want call_tool/3", resumedEvents[3].Node, resumedEvents[3].Step)
	}
	if len(resumeModel.requests) != 1 {
		t.Fatalf("resume model request count = %d, want 1", len(resumeModel.requests))
	}
	messages := resumeModel.requests[0].Messages
	if len(messages) == 0 || messages[len(messages)-1].Role != gopact.RoleTool || messages[len(messages)-1].ToolCallID != "call-1" {
		t.Fatalf("resumed model messages = %+v, want approved tool result included", messages)
	}
}

func TestAgentVerifiesStepExportArtifactsBeforeResume(t *testing.T) {
	ctx := context.Background()
	model := &scriptedModel{}
	agent, err := New(model, nil, WithArtifactVerifier(graph.ArtifactVerifierFunc(func(ctx context.Context, refs []gopact.ArtifactRef) error {
		expectedIDs := []string{"step-artifact", "effect-artifact"}
		if got := artifactIDs(refs); !stringsEqual(got, expectedIDs) {
			t.Fatalf("verified artifact ids = %v, want %v", got, expectedIDs)
		}
		return nil
	})))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	step := gopact.StepExport{
		Version: gopact.RunExportVersion,
		Step: gopact.StepSnapshot{
			ID:     "run-1:1",
			Step:   1,
			Node:   nodeCallModel,
			Phase:  gopact.StepCompleted,
			IDs:    gopact.RuntimeIDs{RunID: "run-1", ThreadID: "thread-1"},
			Output: State{Messages: []gopact.Message{{Role: gopact.RoleAssistant, Content: "final"}}},
			Artifacts: []gopact.ArtifactRef{
				{ID: "step-artifact", SHA256: "sha-1"},
			},
			Effects: []gopact.EffectRecord{
				{
					ID:      "artifact-effect",
					Type:    "artifact_write",
					Applied: true,
					Artifacts: []gopact.ArtifactRef{
						{ID: "effect-artifact", SHA256: "sha-2"},
					},
				},
			},
		},
	}

	events, err := gopacttest.CollectEvents(agent.Run(ctx, State{}, gopact.WithStepExport(step)))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	gopacttest.RequireEventTypes(t, events,
		gopact.EventRunStarted,
		gopact.EventStepImported,
		gopact.EventRunCompleted,
	)
	gopacttest.RequireGoldenTrajectoryFrames(t, "testdata/step_export_artifact_import.golden.json", events)
	if len(model.requests) != 0 {
		t.Fatalf("model requests = %d, want no model call after final step import", len(model.requests))
	}
}

func TestAgentVerifiesCheckpointArtifactsBeforeResume(t *testing.T) {
	ctx := context.Background()
	store := checkpoint.NewMemory[State]()
	err := store.Put(ctx, graph.Checkpoint[State]{
		ID:       "checkpoint-1",
		IDs:      gopact.RuntimeIDs{RunID: "run-1", ThreadID: "thread-1"},
		ThreadID: "thread-1",
		Step:     1,
		Node:     nodeCallModel,
		Phase:    gopact.StepCompleted,
		State: State{Messages: []gopact.Message{
			{
				Role: gopact.RoleAssistant,
				ToolCalls: []gopact.ToolCall{
					{ID: "call-1", Name: "local.echo", Arguments: []byte(`{"text":"hello"}`)},
				},
			},
		}},
		Queue: []string{nodeCallTool},
		Effects: []gopact.EffectRecord{
			{
				ID:      "artifact-effect",
				Type:    "artifact_write",
				Applied: true,
				Artifacts: []gopact.ArtifactRef{
					{ID: "effect-artifact", SHA256: "sha-1"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Put(checkpoint) error = %v", err)
	}
	toolInvoked := 0
	registry := tools.NewRegistry()
	if err := registry.Register(ctx, gopact.ToolFunc{
		SpecValue: gopact.ToolSpec{Name: "echo", Description: "echoes input"},
		InvokeFunc: func(ctx context.Context, args json.RawMessage) (gopact.ToolResult, error) {
			toolInvoked++
			return gopact.ToolResult{Content: "hello"}, nil
		},
	}, tools.RegisterOptions{Namespace: "local", Visibility: tools.VisibleTool}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	verifierCalled := 0
	resumeModel := &scriptedModel{
		responses: []gopact.Message{{Role: gopact.RoleAssistant, Content: "final"}},
	}
	agent, err := New(resumeModel, registry,
		WithCheckpointStore(store),
		WithArtifactVerifier(graph.ArtifactVerifierFunc(func(ctx context.Context, refs []gopact.ArtifactRef) error {
			verifierCalled++
			if got := artifactIDs(refs); !stringsEqual(got, []string{"effect-artifact"}) {
				t.Fatalf("verified artifact ids = %v, want effect-artifact", got)
			}
			return nil
		})),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	events, err := gopacttest.CollectEvents(agent.Run(ctx, State{}, gopact.WithRuntimeIDs(gopact.RuntimeIDs{
		RunID:    "run-2",
		ThreadID: "thread-1",
	})))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if verifierCalled != 1 {
		t.Fatalf("verifier called = %d, want 1", verifierCalled)
	}
	if toolInvoked != 1 {
		t.Fatalf("tool invoked after verified checkpoint resume = %d, want 1", toolInvoked)
	}
	gopacttest.RequireEventTypes(t, events,
		gopact.EventRunStarted,
		gopact.EventCheckpointLoaded,
		gopact.EventNodeResumed,
		gopact.EventToolCall,
		gopact.EventToolResult,
		gopact.EventNodeCompleted,
		gopact.EventNodeStarted,
		gopact.EventModelMessage,
		gopact.EventNodeCompleted,
		gopact.EventRunCompleted,
	)
	gopacttest.RequireGoldenTrajectoryFrames(t, "testdata/checkpoint_artifact_import.golden.json", events)
	if len(resumeModel.requests) != 1 {
		t.Fatalf("resume model request count = %d, want 1", len(resumeModel.requests))
	}
}

func TestAgentRejectsCheckpointWhenArtifactIntegrityFails(t *testing.T) {
	ctx := context.Background()
	wantErr := errors.New("integrity mismatch")
	store := checkpoint.NewMemory[State]()
	err := store.Put(ctx, graph.Checkpoint[State]{
		ID:       "checkpoint-1",
		IDs:      gopact.RuntimeIDs{RunID: "run-1", ThreadID: "thread-1"},
		ThreadID: "thread-1",
		Step:     1,
		Node:     nodeCallModel,
		Phase:    gopact.StepCompleted,
		State: State{Messages: []gopact.Message{
			{
				Role: gopact.RoleAssistant,
				ToolCalls: []gopact.ToolCall{
					{ID: "call-1", Name: "local.echo", Arguments: []byte(`{}`)},
				},
			},
		}},
		Queue: []string{nodeCallTool},
		Effects: []gopact.EffectRecord{
			{
				ID:      "artifact-effect",
				Type:    "artifact_write",
				Applied: true,
				Artifacts: []gopact.ArtifactRef{
					{ID: "effect-artifact", SHA256: "sha-1"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Put(checkpoint) error = %v", err)
	}
	toolInvoked := 0
	registry := tools.NewRegistry()
	if err := registry.Register(ctx, gopact.ToolFunc{
		SpecValue: gopact.ToolSpec{Name: "echo", Description: "echoes input"},
		InvokeFunc: func(ctx context.Context, args json.RawMessage) (gopact.ToolResult, error) {
			toolInvoked++
			return gopact.ToolResult{Content: "hello"}, nil
		},
	}, tools.RegisterOptions{Namespace: "local", Visibility: tools.VisibleTool}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	agent, err := New(&scriptedModel{}, registry,
		WithCheckpointStore(store),
		WithArtifactVerifier(graph.ArtifactVerifierFunc(func(ctx context.Context, refs []gopact.ArtifactRef) error {
			if got := artifactIDs(refs); !stringsEqual(got, []string{"effect-artifact"}) {
				t.Fatalf("verified artifact ids = %v, want effect-artifact", got)
			}
			return wantErr
		})),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	events, err := gopacttest.CollectEvents(agent.Run(ctx, State{}, gopact.WithRuntimeIDs(gopact.RuntimeIDs{
		RunID:    "run-2",
		ThreadID: "thread-1",
	})))
	if !errors.Is(err, wantErr) {
		t.Fatalf("Run() error = %v, want %v", err, wantErr)
	}
	gopacttest.RequireEventTypes(t, events,
		gopact.EventRunStarted,
		gopact.EventRunFailed,
	)
	gopacttest.RequireGoldenTrajectoryFrames(t, "testdata/artifact_verifier_failure.golden.json", events)
	if toolInvoked != 0 {
		t.Fatalf("tool invoked after failed verification = %d, want 0", toolInvoked)
	}
}

func TestAgentToolPolicyReviewInterruptsRun(t *testing.T) {
	ctx := context.Background()
	registry := tools.NewRegistry(tools.WithToolMiddleware(gopact.ToolPolicyMiddleware(gopact.PolicyFunc(func(ctx context.Context, req gopact.PolicyRequest) (gopact.PolicyDecision, error) {
		if req.Boundary != gopact.PolicyBoundaryTool {
			t.Fatalf("policy boundary = %q, want tool", req.Boundary)
		}
		return gopact.PolicyDecision{Action: gopact.PolicyReview, Reason: "needs approval"}, nil
	}))))
	if err := registry.Register(ctx, gopact.ToolFunc{
		SpecValue: gopact.ToolSpec{Name: "echo", Description: "echoes input"},
		InvokeFunc: func(ctx context.Context, args json.RawMessage) (gopact.ToolResult, error) {
			t.Fatal("tool should not be invoked before approval")
			return gopact.ToolResult{}, nil
		},
	}, tools.RegisterOptions{Namespace: "local", Visibility: tools.VisibleTool}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	model := &scriptedModel{
		responses: []gopact.Message{
			{
				Role: gopact.RoleAssistant,
				ToolCalls: []gopact.ToolCall{
					{ID: "call-1", Name: "local.echo", Arguments: []byte(`{"text":"hello"}`)},
				},
			},
		},
	}
	agent, err := New(model, registry)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	events, err := gopacttest.CollectEvents(agent.Run(ctx, State{
		Messages: []gopact.Message{{Role: gopact.RoleUser, Content: "hi"}},
	}, gopact.WithRuntimeIDs(gopact.RuntimeIDs{RunID: "run-1", ThreadID: "thread-1"})))
	if !errors.Is(err, gopact.ErrInterrupted) {
		t.Fatalf("Run() error = %v, want ErrInterrupted", err)
	}
	var interruptErr *gopact.InterruptError
	if !errors.As(err, &interruptErr) {
		t.Fatalf("Run() error type = %T, want *InterruptError", err)
	}
	if interruptErr.Record.ID != "policy:call-1" {
		t.Fatalf("interrupt id = %q, want policy:call-1", interruptErr.Record.ID)
	}

	gopacttest.RequireEventTypes(t, events,
		gopact.EventRunStarted,
		gopact.EventNodeStarted,
		gopact.EventModelMessage,
		gopact.EventNodeCompleted,
		gopact.EventNodeStarted,
		gopact.EventToolCall,
		gopact.EventPolicyRequested,
		gopact.EventPolicyDecided,
		gopact.EventInterrupted,
		gopact.EventRunInterrupted,
	)
	gopacttest.RequireGoldenTrajectoryFrames(t, "testdata/approval_interrupt.golden.json", events)
	if events[6].IDs.CallID != "call-1" || events[7].IDs.CallID != "call-1" {
		t.Fatalf("policy event call ids = %q/%q, want call-1", events[6].IDs.CallID, events[7].IDs.CallID)
	}
	interrupted := events[8]
	if interrupted.IDs.CallID != "call-1" {
		t.Fatalf("interrupted event CallID = %q, want call-1", interrupted.IDs.CallID)
	}
	if interrupted.StepSnapshot == nil {
		t.Fatal("interrupted event StepSnapshot = nil, want paused step snapshot")
	}
	snapshot := interrupted.StepSnapshot
	if snapshot.Phase != gopact.StepInterrupted || snapshot.Pending == nil || snapshot.Pending.ID != "policy:call-1" {
		t.Fatalf("interrupted snapshot = %+v, want pending approval", snapshot)
	}
	if len(snapshot.Queue) != 1 || snapshot.Queue[0] != nodeCallTool {
		t.Fatalf("interrupted snapshot queue = %v, want call_tool", snapshot.Queue)
	}
	output, ok := snapshot.Output.(State)
	if !ok {
		t.Fatalf("interrupted snapshot output type = %T, want react.State", snapshot.Output)
	}
	if len(output.Messages) != 2 {
		t.Fatalf("interrupted snapshot output messages = %d, want user + assistant tool call", len(output.Messages))
	}

	recorder := gopact.NewRunRecorder()
	for _, event := range events {
		if err := recorder.Record(event); err != nil {
			t.Fatalf("Record() error = %v", err)
		}
	}
	export, err := recorder.Export()
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	if export.Outcome != gopact.RunInterrupted {
		t.Fatalf("export outcome = %q, want interrupted", export.Outcome)
	}
	if len(export.Steps) != 2 {
		t.Fatalf("export steps = %d, want completed model + interrupted tool", len(export.Steps))
	}
	if export.Steps[1].Phase != gopact.StepInterrupted || export.Steps[1].Pending == nil {
		t.Fatalf("export interrupted step = %+v, want pending interrupted step", export.Steps[1])
	}
}

func TestAgentResumesToolApprovalFromInterruptedStepExport(t *testing.T) {
	ctx := context.Background()
	toolInvoked := 0
	policy := gopact.PolicyFunc(func(ctx context.Context, req gopact.PolicyRequest) (gopact.PolicyDecision, error) {
		resume, ok := req.Metadata[gopact.MetadataResumeRequest].(gopact.ResumeRequest)
		if ok {
			payload, ok := resume.Payload.(map[string]any)
			if ok && payload["approved"] == true {
				return gopact.PolicyDecision{Action: gopact.PolicyAllow, Reason: "approved"}, nil
			}
		}
		return gopact.PolicyDecision{Action: gopact.PolicyReview, Reason: "needs approval"}, nil
	})
	registry := tools.NewRegistry(tools.WithToolMiddleware(gopact.ToolPolicyMiddleware(policy)))
	if err := registry.Register(ctx, gopact.ToolFunc{
		SpecValue: gopact.ToolSpec{Name: "echo", Description: "echoes input"},
		InvokeFunc: func(ctx context.Context, args json.RawMessage) (gopact.ToolResult, error) {
			toolInvoked++
			return gopact.ToolResult{Content: "hello"}, nil
		},
	}, tools.RegisterOptions{Namespace: "local", Visibility: tools.VisibleTool}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	model := &scriptedModel{
		responses: []gopact.Message{
			{
				Role: gopact.RoleAssistant,
				ToolCalls: []gopact.ToolCall{
					{ID: "call-1", Name: "local.echo", Arguments: []byte(`{"text":"hello"}`)},
				},
			},
			{Role: gopact.RoleAssistant, Content: "final"},
		},
	}
	agent, err := New(model, registry)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	interruptedEvents, err := gopacttest.CollectEvents(agent.Run(ctx, State{
		Messages: []gopact.Message{{Role: gopact.RoleUser, Content: "hi"}},
	}, gopact.WithRuntimeIDs(gopact.RuntimeIDs{RunID: "run-1", ThreadID: "thread-1"})))
	if !errors.Is(err, gopact.ErrInterrupted) {
		t.Fatalf("first Run() error = %v, want ErrInterrupted", err)
	}
	if toolInvoked != 0 {
		t.Fatalf("tool invoked before approval = %d, want 0", toolInvoked)
	}
	recorder := gopact.NewRunRecorder()
	for _, event := range interruptedEvents {
		if err := recorder.Record(event); err != nil {
			t.Fatalf("Record(interrupted) error = %v", err)
		}
	}
	export, err := recorder.Export()
	if err != nil {
		t.Fatalf("Export(interrupted) error = %v", err)
	}
	if len(export.Steps) != 2 || export.Steps[1].Phase != gopact.StepInterrupted {
		t.Fatalf("export steps = %+v, want interrupted tool step", export.Steps)
	}
	step := gopact.StepExport{Version: gopact.RunExportVersion, Step: export.Steps[1]}
	resume := gopact.ResumeRequest{
		StepID:      step.Step.ID,
		InterruptID: "policy:call-1",
		Payload:     map[string]any{"approved": true},
	}

	resumedEvents, err := gopacttest.CollectEvents(agent.Run(ctx, State{}, gopact.WithStepExport(step), gopact.WithResumeRequest(resume)))
	if err != nil {
		t.Fatalf("resume Run() error = %v", err)
	}
	if toolInvoked != 1 {
		t.Fatalf("tool invoked after approval = %d, want 1", toolInvoked)
	}
	gopacttest.RequireEventTypes(t, resumedEvents,
		gopact.EventRunStarted,
		gopact.EventStepImported,
		gopact.EventResumeReceived,
		gopact.EventNodeResumed,
		gopact.EventToolCall,
		gopact.EventPolicyRequested,
		gopact.EventPolicyDecided,
		gopact.EventToolResult,
		gopact.EventNodeCompleted,
		gopact.EventNodeStarted,
		gopact.EventModelMessage,
		gopact.EventNodeCompleted,
		gopact.EventRunCompleted,
	)
	gopacttest.RequireGoldenTrajectoryFrames(t, "testdata/approval_step_resume.golden.json", resumedEvents)
	if resumedEvents[2].Metadata["interrupt_id"] != "policy:call-1" {
		t.Fatalf("resume event metadata = %+v, want interrupt id", resumedEvents[2].Metadata)
	}
	if resumedEvents[6].PolicyDecision == nil || resumedEvents[6].PolicyDecision.Action != gopact.PolicyAllow {
		t.Fatalf("policy decided event = %+v, want allow", resumedEvents[6])
	}
	if model.requests[len(model.requests)-1].Messages[len(model.requests[len(model.requests)-1].Messages)-1].Role != gopact.RoleTool {
		t.Fatalf("final model request messages = %+v, want tool result included", model.requests[len(model.requests)-1].Messages)
	}
}

func TestAgentResumesOnlyPendingToolCallsFromInterruptedBatch(t *testing.T) {
	ctx := context.Background()
	invoked := map[string]int{}
	policy := gopact.PolicyFunc(func(ctx context.Context, req gopact.PolicyRequest) (gopact.PolicyDecision, error) {
		if req.IDs.CallID == "call-1" {
			return gopact.PolicyDecision{Action: gopact.PolicyAllow}, nil
		}
		resume, ok := req.Metadata[gopact.MetadataResumeRequest].(gopact.ResumeRequest)
		if ok {
			payload, ok := resume.Payload.(map[string]any)
			if ok && payload["approved"] == true {
				return gopact.PolicyDecision{Action: gopact.PolicyAllow, Reason: "approved"}, nil
			}
		}
		return gopact.PolicyDecision{Action: gopact.PolicyReview, Reason: "needs approval"}, nil
	})
	registry := tools.NewRegistry(tools.WithToolMiddleware(gopact.ToolPolicyMiddleware(policy)))
	for _, name := range []string{"first", "second"} {
		name := name
		if err := registry.Register(ctx, gopact.ToolFunc{
			SpecValue: gopact.ToolSpec{Name: name, Description: name + " tool"},
			InvokeFunc: func(_ context.Context, _ json.RawMessage) (gopact.ToolResult, error) {
				invoked[name]++
				return gopact.ToolResult{Content: name + "-result"}, nil
			},
		}, tools.RegisterOptions{Namespace: "local", Visibility: tools.VisibleTool}); err != nil {
			t.Fatalf("Register(%s) error = %v", name, err)
		}
	}
	model := &scriptedModel{
		responses: []gopact.Message{
			{
				Role: gopact.RoleAssistant,
				ToolCalls: []gopact.ToolCall{
					{ID: "call-1", Name: "local.first", Arguments: []byte(`{}`)},
					{ID: "call-2", Name: "local.second", Arguments: []byte(`{}`)},
				},
			},
			{Role: gopact.RoleAssistant, Content: "final"},
		},
	}
	agent, err := New(model, registry)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	interruptedEvents, err := gopacttest.CollectEvents(agent.Run(ctx, State{
		Messages: []gopact.Message{{Role: gopact.RoleUser, Content: "hi"}},
	}, gopact.WithRuntimeIDs(gopact.RuntimeIDs{RunID: "run-1"})))
	if !errors.Is(err, gopact.ErrInterrupted) {
		t.Fatalf("first Run() error = %v, want ErrInterrupted", err)
	}
	if invoked["first"] != 1 || invoked["second"] != 0 {
		t.Fatalf("initial invocations = %+v, want first once and second zero", invoked)
	}
	recorder := gopact.NewRunRecorder()
	for _, event := range interruptedEvents {
		if err := recorder.Record(event); err != nil {
			t.Fatalf("Record(interrupted) error = %v", err)
		}
	}
	export, err := recorder.Export()
	if err != nil {
		t.Fatalf("Export(interrupted) error = %v", err)
	}
	step := gopact.StepExport{Version: gopact.RunExportVersion, Step: export.Steps[1]}
	resume := gopact.ResumeRequest{
		StepID:      step.Step.ID,
		InterruptID: "policy:call-2",
		Payload:     map[string]any{"approved": true},
	}

	resumedEvents, err := gopacttest.CollectEvents(agent.Run(ctx, State{}, gopact.WithStepExport(step), gopact.WithResumeRequest(resume)))
	if err != nil {
		t.Fatalf("resume Run() error = %v", err)
	}
	if invoked["first"] != 1 || invoked["second"] != 1 {
		t.Fatalf("resumed invocations = %+v, want no duplicate first and one second", invoked)
	}
	gopacttest.RequireEventTypes(t, resumedEvents,
		gopact.EventRunStarted,
		gopact.EventStepImported,
		gopact.EventResumeReceived,
		gopact.EventNodeResumed,
		gopact.EventToolCall,
		gopact.EventPolicyRequested,
		gopact.EventPolicyDecided,
		gopact.EventToolResult,
		gopact.EventNodeCompleted,
		gopact.EventNodeStarted,
		gopact.EventModelMessage,
		gopact.EventNodeCompleted,
		gopact.EventRunCompleted,
	)
	gopacttest.RequireGoldenTrajectoryFrames(t, "testdata/multi_tool_pending_resume.golden.json", resumedEvents)
	if resumedEvents[4].IDs.CallID != "call-2" {
		t.Fatalf("resumed tool call id = %q, want call-2", resumedEvents[4].IDs.CallID)
	}
	if len(model.requests) != 2 {
		t.Fatalf("model request count = %d, want 2", len(model.requests))
	}
	messages := model.requests[1].Messages
	if len(messages) < 2 {
		t.Fatalf("resumed model messages = %+v, want prior and resumed tool results", messages)
	}
	firstResult := messages[len(messages)-2]
	secondResult := messages[len(messages)-1]
	if firstResult.Role != gopact.RoleTool || firstResult.ToolCallID != "call-1" || firstResult.Content != "first-result" {
		t.Fatalf("first resumed model tool result = %+v, want completed call-1", firstResult)
	}
	if secondResult.Role != gopact.RoleTool || secondResult.ToolCallID != "call-2" || secondResult.Content != "second-result" {
		t.Fatalf("second resumed model tool result = %+v, want resumed call-2", secondResult)
	}
}

func TestAgentToolPolicyDenyFailsRunWithPolicyEvents(t *testing.T) {
	ctx := context.Background()
	registry := tools.NewRegistry(tools.WithToolMiddleware(gopact.ToolPolicyMiddleware(gopact.PolicyFunc(func(ctx context.Context, req gopact.PolicyRequest) (gopact.PolicyDecision, error) {
		return gopact.PolicyDecision{Action: gopact.PolicyDeny, Reason: "blocked"}, nil
	}))))
	if err := registry.Register(ctx, gopact.ToolFunc{
		SpecValue: gopact.ToolSpec{Name: "echo", Description: "echoes input"},
		InvokeFunc: func(_ context.Context, _ json.RawMessage) (gopact.ToolResult, error) {
			t.Fatal("tool should not be invoked after policy denial")
			return gopact.ToolResult{}, nil
		},
	}, tools.RegisterOptions{Namespace: "local", Visibility: tools.VisibleTool}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	agent, err := New(&scriptedModel{
		responses: []gopact.Message{
			{
				Role: gopact.RoleAssistant,
				ToolCalls: []gopact.ToolCall{
					{ID: "call-1", Name: "local.echo", Arguments: []byte(`{"text":"hello"}`)},
				},
			},
		},
	}, registry)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	events, err := gopacttest.CollectEvents(agent.Run(ctx, State{
		Messages: []gopact.Message{{Role: gopact.RoleUser, Content: "hi"}},
	}, gopact.WithRuntimeIDs(gopact.RuntimeIDs{RunID: "run-1", ThreadID: "thread-1"})))
	if !errors.Is(err, gopact.ErrPolicyDenied) {
		t.Fatalf("Run() error = %v, want ErrPolicyDenied", err)
	}
	var deniedErr *gopact.PolicyDeniedError
	if !errors.As(err, &deniedErr) {
		t.Fatalf("Run() error type = %T, want *PolicyDeniedError", err)
	}
	if deniedErr.Decision.Action != gopact.PolicyDeny {
		t.Fatalf("policy decision = %+v, want deny", deniedErr.Decision)
	}

	gopacttest.RequireEventTypes(t, events,
		gopact.EventRunStarted,
		gopact.EventNodeStarted,
		gopact.EventModelMessage,
		gopact.EventNodeCompleted,
		gopact.EventNodeStarted,
		gopact.EventToolCall,
		gopact.EventPolicyRequested,
		gopact.EventPolicyDecided,
		gopact.EventNodeFailed,
		gopact.EventRunFailed,
	)
	gopacttest.RequireGoldenTrajectoryFrames(t, "testdata/policy_deny.golden.json", events)
	if events[7].PolicyDecision == nil || events[7].PolicyDecision.Action != gopact.PolicyDeny {
		t.Fatalf("policy decided event = %+v, want deny decision", events[7])
	}
	if events[8].StepSnapshot == nil || events[8].StepSnapshot.Phase != gopact.StepFailed {
		t.Fatalf("failed node event = %+v, want failed step snapshot", events[8])
	}
}

func TestAgentRunExportAttributesFailureKindFromRuntimeEvents(t *testing.T) {
	ctx := context.Background()
	modelErr := errors.New("model unavailable")
	toolErr := errors.New("tool failed")
	ids := gopact.RuntimeIDs{RunID: "run-1", ThreadID: "thread-1"}

	tests := []struct {
		name    string
		agent   *Agent
		wantErr error
		want    gopact.FailureKind
		golden  string
	}{
		{
			name: "model",
			agent: mustNewAgent(t, &scriptedModel{
				errors: []error{modelErr},
			}, nil),
			wantErr: modelErr,
			want:    gopact.FailureModel,
		},
		{
			name: "tool",
			agent: mustNewAgent(t, &scriptedModel{
				responses: []gopact.Message{
					{
						Role: gopact.RoleAssistant,
						ToolCalls: []gopact.ToolCall{
							{ID: "call-1", Name: "local.fail", Arguments: []byte(`{}`)},
						},
					},
				},
			}, mustRegistry(t, gopact.ToolFunc{
				SpecValue: gopact.ToolSpec{Name: "fail", Description: "fails"},
				InvokeFunc: func(_ context.Context, _ json.RawMessage) (gopact.ToolResult, error) {
					return gopact.ToolResult{}, toolErr
				},
			})),
			wantErr: toolErr,
			want:    gopact.FailureTool,
			golden:  "testdata/tool_error.golden.json",
		},
		{
			name: "policy",
			agent: mustNewAgent(t, &scriptedModel{
				responses: []gopact.Message{
					{
						Role: gopact.RoleAssistant,
						ToolCalls: []gopact.ToolCall{
							{ID: "call-1", Name: "local.echo", Arguments: []byte(`{}`)},
						},
					},
				},
			}, mustRegistry(t, gopact.ToolFunc{
				SpecValue: gopact.ToolSpec{Name: "echo", Description: "echoes"},
				InvokeFunc: func(ctx context.Context, args json.RawMessage) (gopact.ToolResult, error) {
					return gopact.ToolResult{Content: "ok"}, nil
				},
			}, tools.WithToolMiddleware(gopact.ToolPolicyMiddleware(gopact.PolicyFunc(func(ctx context.Context, req gopact.PolicyRequest) (gopact.PolicyDecision, error) {
				return gopact.PolicyDecision{Action: gopact.PolicyDeny, Reason: "blocked"}, nil
			}))))),
			wantErr: gopact.ErrPolicyDenied,
			want:    gopact.FailurePolicy,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events, err := gopacttest.CollectEvents(tt.agent.Run(ctx, State{
				Messages: []gopact.Message{{Role: gopact.RoleUser, Content: "hi"}},
			}, gopact.WithRuntimeIDs(ids)))
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Run() error = %v, want %v", err, tt.wantErr)
			}
			export := requireRunExport(t, events)
			if len(export.Failures) != 1 {
				t.Fatalf("Failures = %+v, want one failure", export.Failures)
			}
			if export.Failures[0].Kind != tt.want {
				t.Fatalf("failure kind = %q, want %q", export.Failures[0].Kind, tt.want)
			}
			if tt.golden != "" {
				gopacttest.RequireGoldenTrajectoryFrames(t, tt.golden, events)
			}
		})
	}
}

func TestAgentToolCallWithoutRegistryEmitsNodeAndRunFailure(t *testing.T) {
	agent, err := New(&scriptedModel{
		responses: []gopact.Message{
			{
				Role: gopact.RoleAssistant,
				ToolCalls: []gopact.ToolCall{
					{ID: "call-1", Name: "local.echo", Arguments: []byte(`{"text":"hello"}`)},
				},
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	events, err := gopacttest.CollectEvents(agent.Run(context.Background(), State{
		Messages: []gopact.Message{{Role: gopact.RoleUser, Content: "hi"}},
	}))
	if !errors.Is(err, ErrToolRegistryMissing) {
		t.Fatalf("Run() error = %v, want ErrToolRegistryMissing", err)
	}
	gopacttest.RequireEventTypes(t, events,
		gopact.EventRunStarted,
		gopact.EventNodeStarted,
		gopact.EventModelMessage,
		gopact.EventNodeCompleted,
		gopact.EventNodeStarted,
		gopact.EventNodeFailed,
		gopact.EventRunFailed,
	)
	gopacttest.RequireGoldenTrajectoryFrames(t, "testdata/missing_tool_registry.golden.json", events)
}

type scriptedModel struct {
	responses []gopact.Message
	errors    []error
	requests  []gopact.ModelRequest
}

func (m *scriptedModel) Generate(ctx context.Context, request gopact.ModelRequest) (gopact.Message, error) {
	if err := ctx.Err(); err != nil {
		return gopact.Message{}, err
	}
	m.requests = append(m.requests, request)
	if len(m.errors) > 0 {
		err := m.errors[0]
		m.errors = m.errors[1:]
		return gopact.Message{}, err
	}
	if len(m.responses) == 0 {
		return gopact.Message{Role: gopact.RoleAssistant, Content: "done"}, nil
	}
	response := m.responses[0]
	m.responses = m.responses[1:]
	return response, nil
}

func mustNewAgent(t *testing.T, model gopact.ChatModel, registry *tools.Registry, opts ...Option) *Agent {
	t.Helper()
	agent, err := New(model, registry, opts...)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return agent
}

func mustRegistry(t *testing.T, tool gopact.Tool, opts ...tools.RegistryOption) *tools.Registry {
	t.Helper()
	registry := tools.NewRegistry(opts...)
	if err := registry.Register(context.Background(), tool, tools.RegisterOptions{
		Namespace:  "local",
		Visibility: tools.VisibleTool,
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	return registry
}

func requireRunExport(t *testing.T, events []gopact.Event) gopact.RunExport {
	t.Helper()
	recorder := gopact.NewRunRecorder()
	for _, event := range events {
		if err := recorder.Record(event); err != nil {
			t.Fatalf("Record(%s) error = %v", event.Type, err)
		}
	}
	export, err := recorder.Export()
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	return export
}

func artifactIDs(refs []gopact.ArtifactRef) []string {
	if len(refs) == 0 {
		return nil
	}
	ids := make([]string, 0, len(refs))
	for _, ref := range refs {
		ids = append(ids, ref.ID)
	}
	return ids
}

func stringsEqual(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
