package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"testing"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact-ext/agents/agentnode"
	"github.com/gopact-ai/gopact-ext/agents/agenttool"
	"github.com/gopact-ai/gopact-ext/agents/humanreview"
	"github.com/gopact-ai/gopact-ext/agents/planexec"
	"github.com/gopact-ai/gopact-ext/agents/react"
	"github.com/gopact-ai/gopact-ext/agents/supervisor"
	"github.com/gopact-ai/gopact/a2a"
	"github.com/gopact-ai/gopact/checkpoint"
	"github.com/gopact-ai/gopact/gopacttest"
	"github.com/gopact-ai/gopact/graph"
)

func TestReActTemplateRunsToolThenFinalWithMockModel(t *testing.T) {
	wantIDs := gopact.RuntimeIDs{RunID: "run-react-mock", ThreadID: "thread-templates", UserID: "user-1", TraceID: "trace-templates"}
	model := &mockResponseModel{responses: []gopact.ModelResponse{
		{Message: gopact.Message{
			Role: gopact.RoleAssistant,
			ToolCalls: []gopact.ToolCall{{
				ID:        "call-uppercase",
				Name:      "local.uppercase",
				Arguments: []byte(`{"text":"gopact"}`),
			}},
		}},
		{Message: gopact.AssistantMessage("GOPACT")},
	}}
	agent, err := react.NewModelAgent(model, react.WithTools(context.Background(), gopact.ToolFunc{
		SpecValue: gopact.ObjectToolSpec("uppercase", "Uppercase text.", gopact.RequiredStringField("text", "Text to uppercase.")),
		InvokeFunc: func(_ context.Context, args json.RawMessage) (gopact.ToolResult, error) {
			var input struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(args, &input); err != nil {
				return gopact.ToolResult{}, err
			}
			return gopact.ToolResult{Content: "GOPACT"}, nil
		},
	}), react.WithModelOptions(
		gopact.WithMaxOutputTokens(1024),
		gopact.WithTemperature(0.2),
		gopact.WithThinkingType("disabled"),
	))
	if err != nil {
		t.Fatalf("react.NewModelAgent() error = %v", err)
	}

	events, err := gopacttest.CollectEvents(agent.Run(context.Background(), "uppercase gopact", gopact.WithRuntimeIDs(wantIDs)))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
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
		gopact.EventNodeStarted,
		gopact.EventModelMessage,
		gopact.EventNodeCompleted,
		gopact.EventRunCompleted,
	)
	if len(model.requests) != 2 {
		t.Fatalf("model requests = %d, want tool call and final call", len(model.requests))
	}
	requireMockModelRuntimeIDs(t, model.requests, model.contextIDs, wantIDs)
	requireMockModelOptions(t, model.requests, 1024, 0.2, "disabled")
}

func TestPlanExecTemplateRunsWithMockModel(t *testing.T) {
	wantIDs := gopact.RuntimeIDs{RunID: "run-planexec-mock", ThreadID: "thread-templates", UserID: "user-1", TraceID: "trace-templates"}
	model := &mockResponseModel{responses: []gopact.ModelResponse{
		{Message: gopact.AssistantMessage("STEP: draft\nSTEP: review")},
		{Message: gopact.AssistantMessage("drafted")},
		{Message: gopact.AssistantMessage("reviewed")},
	}}
	agent, err := planexec.NewModelAgent(
		model,
		planexec.WithModelOptions(
			gopact.WithMaxOutputTokens(1024),
			gopact.WithTemperature(0.2),
			gopact.WithThinkingType("disabled"),
		),
	)
	if err != nil {
		t.Fatalf("planexec.NewModelAgent() error = %v", err)
	}

	events, err := gopacttest.CollectEvents(agent.Run(context.Background(), "ship example", gopact.WithRuntimeIDs(wantIDs)))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	gopacttest.RequireEventTypes(t, events,
		gopact.EventRunStarted,
		gopact.EventNodeStarted,
		gopact.EventNodeCompleted,
		gopact.EventNodeStarted,
		gopact.EventNodeCompleted,
		gopact.EventNodeStarted,
		gopact.EventNodeCompleted,
		gopact.EventRunCompleted,
	)
	state, ok := events[6].StepSnapshot.Output.(planexec.State)
	if !ok {
		t.Fatalf("summary output type = %T, want planexec.State", events[6].StepSnapshot.Output)
	}
	if len(state.Steps) != 2 || len(state.Results) != 2 || state.Summary != "completed 2 steps" {
		t.Fatalf("state = %+v, want two completed steps", state)
	}
	if len(model.requests) != 3 {
		t.Fatalf("model requests = %d, want planner and two executors", len(model.requests))
	}
	requireMockModelRuntimeIDs(t, model.requests, model.contextIDs, wantIDs)
	requireMockModelOptions(t, model.requests, 1024, 0.2, "disabled")
}

func TestPlanExecTemplateResumesApprovalCheckpointWithMockModel(t *testing.T) {
	wantIDs := gopact.RuntimeIDs{RunID: "run-planexec-checkpoint-mock", ThreadID: "thread-planexec-checkpoint", UserID: "user-1"}
	store := checkpoint.NewMemory[planexec.State]()
	model := &mockResponseModel{responses: []gopact.ModelResponse{
		{Message: gopact.AssistantMessage("STEP: draft")},
		{Message: gopact.AssistantMessage("drafted")},
	}}
	agent, err := planexec.NewModelAgent(
		model,
		planexec.WithModelOptions(
			gopact.WithMaxOutputTokens(1024),
			gopact.WithTemperature(0.2),
			gopact.WithThinkingType("disabled"),
		),
		planexec.WithApprovalPolicy(gopact.PolicyFunc(func(context.Context, gopact.PolicyRequest) (gopact.PolicyDecision, error) {
			return gopact.PolicyDecision{Action: gopact.PolicyReview, Reason: "needs approval"}, nil
		})),
		planexec.WithCheckpointStore(store),
	)
	if err != nil {
		t.Fatalf("planexec.NewModelAgent() error = %v", err)
	}

	events, err := gopacttest.CollectEvents(agent.Run(context.Background(), "ship example", gopact.WithRuntimeIDs(wantIDs)))
	if !errors.Is(err, gopact.ErrInterrupted) {
		t.Fatalf("Run() error = %v, want ErrInterrupted", err)
	}
	gopacttest.RequireEventTypes(t, events,
		gopact.EventRunStarted,
		gopact.EventNodeStarted,
		gopact.EventNodeCompleted,
		gopact.EventNodeStarted,
		gopact.EventInterrupted,
		gopact.EventRunInterrupted,
	)
	if len(model.requests) != 1 {
		t.Fatalf("model requests before resume = %d, want planner only", len(model.requests))
	}
	latest, ok, err := store.Latest(context.Background(), wantIDs.ThreadID)
	if err != nil || !ok || latest.Pending == nil {
		t.Fatalf("Latest() = checkpoint:%+v ok:%v err:%v, want interrupted checkpoint", latest, ok, err)
	}

	resumed, err := gopacttest.CollectEvents(agent.Run(context.Background(), planexec.State{},
		gopact.WithRuntimeIDs(wantIDs),
		gopact.WithResumeRequest(gopact.ResumeRequest{
			CheckpointID: latest.ID,
			InterruptID:  latest.Pending.ID,
			Payload:      map[string]any{"approved": true},
		}),
	))
	if err != nil {
		t.Fatalf("resume Run() error = %v", err)
	}
	gopacttest.RequireEventTypes(t, resumed,
		gopact.EventRunStarted,
		gopact.EventCheckpointLoaded,
		gopact.EventResumeReceived,
		gopact.EventNodeResumed,
		gopact.EventNodeCompleted,
		gopact.EventNodeStarted,
		gopact.EventNodeCompleted,
		gopact.EventRunCompleted,
	)
	if len(model.requests) != 2 {
		t.Fatalf("model requests after resume = %d, want planner and executor", len(model.requests))
	}
	requireMockModelRuntimeIDs(t, model.requests, model.contextIDs, wantIDs)
	requireMockModelOptions(t, model.requests, 1024, 0.2, "disabled")
}

func TestReActTemplateCanUsePlanExecAgentAsToolWithMockModel(t *testing.T) {
	parentIDs := gopact.RuntimeIDs{
		RunID:    "run-agent-as-tool-mock",
		ThreadID: "thread-agent-as-tool",
		UserID:   "user-1",
		CallID:   "parent-call",
		TraceID:  "trace-agent-as-tool",
	}
	childIDs := parentIDs
	childIDs.ParentCallID = parentIDs.CallID
	childIDs.CallID = "call-delegate-plan"

	parentModel := &mockResponseModel{responses: []gopact.ModelResponse{
		{Message: gopact.Message{
			Role: gopact.RoleAssistant,
			ToolCalls: []gopact.ToolCall{{
				ID:        childIDs.CallID,
				Name:      "local.delegate_plan",
				Arguments: []byte(`{"input":"ship the example repository","task_id":"child-task-1"}`),
			}},
		}},
		{Message: gopact.AssistantMessage("delegated")},
	}}
	childModel := &mockResponseModel{responses: []gopact.ModelResponse{
		{Message: gopact.AssistantMessage("STEP: draft")},
		{Message: gopact.AssistantMessage("draft ready")},
	}}
	child, err := planexec.NewModelAgent(
		childModel,
		planexec.WithModelOptions(
			gopact.WithMaxOutputTokens(1024),
			gopact.WithTemperature(0.2),
			gopact.WithThinkingType("disabled"),
		),
	)
	if err != nil {
		t.Fatalf("planexec.NewModelAgent() error = %v", err)
	}
	childA2A, err := a2a.NewRunnableAgent(
		a2a.AgentCard{Name: "planexec_child", Description: "Delegated planning agent."},
		child,
		a2a.WithRunnableInputMapper(func(_ context.Context, task a2a.Task) (any, error) {
			return task.Input, nil
		}),
		a2a.WithRunnableResultMapper(func(_ context.Context, task a2a.Task, events []gopact.Event) (a2a.Result, error) {
			for i := len(events) - 1; i >= 0; i-- {
				if events[i].StepSnapshot == nil {
					continue
				}
				state, ok := events[i].StepSnapshot.Output.(planexec.State)
				if ok && state.Summary != "" {
					return a2a.Result{TaskID: task.ID, Output: state.Summary}, nil
				}
			}
			return a2a.Result{TaskID: task.ID}, nil
		}),
	)
	if err != nil {
		t.Fatalf("a2a.NewRunnableAgent() error = %v", err)
	}
	childTool, err := agenttool.New(childA2A, agenttool.WithName("delegate_plan"))
	if err != nil {
		t.Fatalf("agenttool.New() error = %v", err)
	}
	parent, err := react.NewModelAgent(
		parentModel,
		react.WithTools(context.Background(), childTool),
		react.WithModelOptions(
			gopact.WithMaxOutputTokens(1024),
			gopact.WithTemperature(0.2),
			gopact.WithThinkingType("disabled"),
		),
	)
	if err != nil {
		t.Fatalf("react.NewModelAgent() error = %v", err)
	}

	events, err := gopacttest.CollectEvents(parent.Run(context.Background(), "delegate planning", gopact.WithRuntimeIDs(parentIDs)))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	gopacttest.RequireEventTypes(t, events,
		gopact.EventRunStarted,
		gopact.EventNodeStarted,
		gopact.EventModelMessage,
		gopact.EventNodeCompleted,
		gopact.EventNodeStarted,
		gopact.EventToolCall,
		gopact.EventA2ATaskCompleted,
		gopact.EventToolResult,
		gopact.EventNodeCompleted,
		gopact.EventNodeStarted,
		gopact.EventModelMessage,
		gopact.EventNodeCompleted,
		gopact.EventRunCompleted,
	)
	if events[6].Result == nil || events[6].Result.Content != "completed 1 steps" {
		t.Fatalf("child completion event = %+v, want planexec summary", events[6])
	}
	if events[6].Metadata["agent_name"] != "planexec_child" ||
		events[6].Metadata["a2a_task_id"] != "child-task-1" {
		t.Fatalf("child completion metadata = %+v, want agent-as-tool evidence", events[6].Metadata)
	}
	requireMockModelRuntimeIDs(t, parentModel.requests, parentModel.contextIDs, parentIDs)
	requireMockModelRuntimeIDs(t, childModel.requests, childModel.contextIDs, childIDs)
	requireMockModelOptions(t, parentModel.requests, 1024, 0.2, "disabled")
	requireMockModelOptions(t, childModel.requests, 1024, 0.2, "disabled")
}

func TestSupervisorTemplateRoutesToPlanExecChildWithMockModel(t *testing.T) {
	parentIDs := gopact.RuntimeIDs{
		RunID:    "run-supervisor-mock",
		ThreadID: "thread-supervisor",
		UserID:   "user-1",
		TraceID:  "trace-supervisor",
	}
	childIDs := parentIDs
	childIDs.AgentID = "planner"

	childModel := &mockResponseModel{responses: []gopact.ModelResponse{
		{Message: gopact.AssistantMessage("STEP: validate supervisor routing")},
		{Message: gopact.AssistantMessage("routing validated")},
	}}
	child, err := planexec.NewModelAgent(
		childModel,
		planexec.WithModelOptions(
			gopact.WithMaxOutputTokens(1024),
			gopact.WithTemperature(0.2),
			gopact.WithThinkingType("disabled"),
		),
	)
	if err != nil {
		t.Fatalf("planexec.NewModelAgent() error = %v", err)
	}
	agent, err := supervisor.New(
		supervisor.RouterFunc(func(_ context.Context, request supervisor.Request) (supervisor.Route, error) {
			if request.Task != "ship supervised plan" {
				return supervisor.Route{}, fmt.Errorf("task = %q, want supervised task", request.Task)
			}
			return supervisor.Route{Agent: "planner", Input: request.Task}, nil
		}),
		supervisor.Child{Name: "planner", Runnable: child},
	)
	if err != nil {
		t.Fatalf("supervisor.New() error = %v", err)
	}

	events, err := gopacttest.CollectEvents(agent.Run(context.Background(), "ship supervised plan", gopact.WithRuntimeIDs(parentIDs)))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	gopacttest.RequireEventTypes(t, events,
		gopact.EventRunStarted,
		gopact.EventNodeStarted,
		gopact.EventNodeCompleted,
		gopact.EventRunStarted,
		gopact.EventNodeStarted,
		gopact.EventNodeCompleted,
		gopact.EventNodeStarted,
		gopact.EventNodeCompleted,
		gopact.EventNodeStarted,
		gopact.EventNodeCompleted,
		gopact.EventRunCompleted,
		gopact.EventRunCompleted,
	)
	routeState, ok := events[2].StepSnapshot.Output.(supervisor.State)
	if !ok || routeState.SelectedAgent != "planner" {
		t.Fatalf("route output = %#v, want selected planner", events[2].StepSnapshot.Output)
	}
	childState, ok := events[9].StepSnapshot.Output.(planexec.State)
	if !ok || childState.Summary != "completed 1 steps" || len(childState.Results) != 1 {
		t.Fatalf("child output = %#v, want completed planexec state", events[9].StepSnapshot.Output)
	}
	if events[11].Metadata["selected_agent"] != "planner" {
		t.Fatalf("supervisor completion metadata = %+v, want selected planner", events[11].Metadata)
	}
	requireMockModelRuntimeIDs(t, childModel.requests, childModel.contextIDs, childIDs)
	requireMockModelOptions(t, childModel.requests, 1024, 0.2, "disabled")
}

func TestReActTemplateFailsWhenPlanExecAgentToolFailsWithMockModel(t *testing.T) {
	parentIDs := gopact.RuntimeIDs{
		RunID:    "run-agent-as-tool-failure-mock",
		ThreadID: "thread-agent-as-tool",
		CallID:   "parent-call",
		TraceID:  "trace-agent-as-tool",
	}
	childIDs := parentIDs
	childIDs.ParentCallID = parentIDs.CallID
	childIDs.CallID = "call-delegate-plan"
	childErr := errors.New("child executor failed")

	parentModel := &mockResponseModel{responses: []gopact.ModelResponse{
		{Message: gopact.Message{
			Role: gopact.RoleAssistant,
			ToolCalls: []gopact.ToolCall{{
				ID:        childIDs.CallID,
				Name:      "local.delegate_plan",
				Arguments: []byte(`{"input":"ship the example repository","task_id":"child-task-1"}`),
			}},
		}},
		{Message: gopact.AssistantMessage("should not run")},
	}}
	child, err := planexec.New(
		planexec.PlannerFunc(func(context.Context, planexec.PlanRequest) ([]planexec.Step, error) {
			return []planexec.Step{{ID: "draft", Instruction: "draft example"}}, nil
		}),
		planexec.ExecutorFunc(func(context.Context, planexec.Step) (planexec.StepResult, error) {
			return planexec.StepResult{}, childErr
		}),
	)
	if err != nil {
		t.Fatalf("planexec.New() error = %v", err)
	}
	childA2A, err := a2a.NewRunnableAgent(
		a2a.AgentCard{Name: "planexec_child", Description: "Delegated planning agent."},
		child,
		a2a.WithRunnableInputMapper(func(_ context.Context, task a2a.Task) (any, error) {
			return task.Input, nil
		}),
	)
	if err != nil {
		t.Fatalf("a2a.NewRunnableAgent() error = %v", err)
	}
	childTool, err := agenttool.New(childA2A, agenttool.WithName("delegate_plan"))
	if err != nil {
		t.Fatalf("agenttool.New() error = %v", err)
	}
	parent, err := react.NewModelAgent(parentModel, react.WithTools(context.Background(), childTool))
	if err != nil {
		t.Fatalf("react.NewModelAgent() error = %v", err)
	}

	events, err := gopacttest.CollectEvents(parent.Run(context.Background(), "delegate planning", gopact.WithRuntimeIDs(parentIDs)))
	if !errors.Is(err, childErr) {
		t.Fatalf("Run() error = %v, want child executor failure", err)
	}
	gopacttest.RequireEventTypes(t, events,
		gopact.EventRunStarted,
		gopact.EventNodeStarted,
		gopact.EventModelMessage,
		gopact.EventNodeCompleted,
		gopact.EventNodeStarted,
		gopact.EventToolCall,
		gopact.EventA2ATaskFailed,
		gopact.EventNodeFailed,
		gopact.EventRunFailed,
	)
	if events[6].Metadata["agent_name"] != "planexec_child" ||
		events[6].Metadata["a2a_task_id"] != "child-task-1" ||
		events[6].Metadata["a2a_status"] != string(a2a.TaskStatusFailed) {
		t.Fatalf("child failure metadata = %+v, want agent-as-tool failure evidence", events[6].Metadata)
	}
	if len(parentModel.requests) != 1 {
		t.Fatalf("parent model requests = %d, want no final call after child failure", len(parentModel.requests))
	}
	requireMockModelRuntimeIDs(t, parentModel.requests, parentModel.contextIDs, parentIDs)
}

func TestAgentNodeDelegatesA2AAgentInsideGraphWithMock(t *testing.T) {
	wantIDs := gopact.RuntimeIDs{RunID: "run-agentnode-mock", ThreadID: "thread-agentnode", TraceID: "trace-agentnode"}
	child := a2a.FakeAgent{
		CardValue: a2a.AgentCard{Name: "planner-agent", URL: "memory://planner-agent"},
		StreamFunc: func(_ context.Context, task a2a.Task) iter.Seq2[a2a.TaskEvent, error] {
			return func(yield func(a2a.TaskEvent, error) bool) {
				yield(a2a.TaskEvent{
					TaskID: task.ID,
					IDs:    task.IDs,
					Status: a2a.TaskStatusCompleted,
					Result: &a2a.Result{TaskID: task.ID, Output: "plan accepted"},
				}, nil)
			}
		},
	}
	node, err := agentnode.New[agentNodeState](
		child,
		func(ctx context.Context, state agentNodeState) (a2a.Task, error) {
			ids, _ := gopact.RuntimeIDsFromContext(ctx)
			return a2a.Task{ID: "task-agentnode", IDs: ids, Input: state.Input}, nil
		},
		func(_ context.Context, state agentNodeState, result a2a.Result) (agentNodeState, error) {
			state.Output = result.Output
			return state, nil
		},
	)
	if err != nil {
		t.Fatalf("agentnode.New() error = %v", err)
	}
	g := graph.New[agentNodeState]()
	g.AddNode("delegate", node)
	g.AddEdge(graph.Start, "delegate")
	g.AddEdge("delegate", graph.End)
	run, err := g.Compile()
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	events, err := gopacttest.CollectEvents(run.Run(context.Background(), agentNodeState{Input: "ship examples"}, graph.WithRuntimeIDs(wantIDs)))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	gopacttest.RequireEventTypes(t, events,
		gopact.EventRunStarted,
		gopact.EventNodeStarted,
		gopact.EventA2ATaskCompleted,
		gopact.EventNodeCompleted,
		gopact.EventRunCompleted,
	)
	if events[2].Metadata["agent_name"] != "planner-agent" ||
		events[2].Metadata[graph.EventMetadataParentNode] != "delegate" {
		t.Fatalf("A2A graph node metadata = %+v, want child and parent evidence", events[2].Metadata)
	}
	output, ok := events[3].StepSnapshot.Output.(agentNodeState)
	if !ok {
		t.Fatalf("output type = %T, want agentNodeState", events[3].StepSnapshot.Output)
	}
	if output.Output != "plan accepted" {
		t.Fatalf("output = %+v, want mapped A2A result", output)
	}
}

func TestHumanReviewTemplateGatesGraphAndResumesWithMock(t *testing.T) {
	wantIDs := gopact.RuntimeIDs{RunID: "run-humanreview-mock", ThreadID: "thread-humanreview", TraceID: "trace-humanreview"}
	store := checkpoint.NewMemory[humanReviewState]()
	mapCalls := 0
	applyCalls := 0

	gate, err := humanreview.New(func(ctx context.Context, state humanReviewState) (humanreview.Request, error) {
		mapCalls++
		ids, _ := gopact.RuntimeIDsFromContext(ctx)
		if ids != wantIDs {
			t.Fatalf("runtime ids = %+v, want %+v", ids, wantIDs)
		}
		if state.Task != "ship self-bootstrap" {
			t.Fatalf("state task = %q, want ship self-bootstrap", state.Task)
		}
		return humanreview.Request{
			ID:         "approval:self-bootstrap",
			Reason:     "human approval required",
			RequiredBy: "release-manager",
			Prompt:     gopact.UserMessage("Approve self-bootstrap release?"),
			Metadata:   map[string]any{"workflow": "release"},
		}, nil
	})
	if err != nil {
		t.Fatalf("humanreview.New() error = %v", err)
	}

	g := graph.New[humanReviewState]()
	g.AddNode("review", gate)
	g.AddNode("apply", func(_ context.Context, state humanReviewState) (humanReviewState, error) {
		applyCalls++
		state.Approved = true
		state.Trace = append(state.Trace, "apply")
		return state, nil
	})
	g.AddEdge(graph.Start, "review")
	g.AddEdge("review", "apply")
	g.AddEdge("apply", graph.End)
	run, err := g.Compile()
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	events, err := gopacttest.CollectEvents(run.Run(context.Background(), humanReviewState{Task: "ship self-bootstrap"},
		graph.WithRuntimeIDs(wantIDs),
		graph.WithCheckpointStore(store),
	))
	if !errors.Is(err, gopact.ErrInterrupted) {
		t.Fatalf("Run() error = %v, want ErrInterrupted", err)
	}
	gopacttest.RequireEventTypes(t, events,
		gopact.EventRunStarted,
		gopact.EventNodeStarted,
		gopact.EventInterrupted,
		gopact.EventRunInterrupted,
	)
	if mapCalls != 1 || applyCalls != 0 {
		t.Fatalf("before resume map/apply calls = %d/%d, want 1/0", mapCalls, applyCalls)
	}
	latest, ok, err := store.Latest(context.Background(), wantIDs.ThreadID)
	if err != nil || !ok || latest.Pending == nil {
		t.Fatalf("Latest() = checkpoint:%+v ok:%v err:%v, want interrupted checkpoint", latest, ok, err)
	}
	if latest.Pending.ID != "approval:self-bootstrap" ||
		latest.Pending.Type != gopact.InterruptApproval ||
		latest.Pending.RequiredBy != "release-manager" ||
		latest.Pending.Metadata["template"] != "humanreview" ||
		latest.Pending.Metadata["workflow"] != "release" {
		t.Fatalf("pending = %+v, want humanreview approval metadata", latest.Pending)
	}

	resumed, err := gopacttest.CollectEvents(run.Run(context.Background(), humanReviewState{},
		graph.WithRuntimeIDs(wantIDs),
		graph.WithCheckpointStore(store),
		graph.WithResumeRequest(gopact.ResumeRequest{
			CheckpointID: latest.ID,
			InterruptID:  latest.Pending.ID,
			Payload:      map[string]any{"approved": true},
		}),
	))
	if err != nil {
		t.Fatalf("resume Run() error = %v", err)
	}
	gopacttest.RequireEventTypes(t, resumed,
		gopact.EventRunStarted,
		gopact.EventCheckpointLoaded,
		gopact.EventResumeReceived,
		gopact.EventNodeResumed,
		gopact.EventNodeCompleted,
		gopact.EventRunCompleted,
	)
	if mapCalls != 1 || applyCalls != 1 {
		t.Fatalf("after resume map/apply calls = %d/%d, want 1/1", mapCalls, applyCalls)
	}
	output, ok := resumed[4].StepSnapshot.Output.(humanReviewState)
	if !ok {
		t.Fatalf("resumed output type = %T, want humanReviewState", resumed[4].StepSnapshot.Output)
	}
	if !output.Approved || len(output.Trace) != 1 || output.Trace[0] != "apply" {
		t.Fatalf("resumed output = %+v, want approved apply trace", output)
	}
}

type agentNodeState struct {
	Input  string
	Output string
}

type humanReviewState struct {
	Task     string
	Approved bool
	Trace    []string
}

type mockResponseModel struct {
	responses  []gopact.ModelResponse
	requests   []gopact.ModelRequest
	contextIDs []gopact.RuntimeIDs
}

func (m *mockResponseModel) Generate(ctx context.Context, request gopact.ModelRequest) (gopact.ModelResponse, error) {
	if err := ctx.Err(); err != nil {
		return gopact.ModelResponse{}, err
	}
	m.requests = append(m.requests, request)
	ids, _ := gopact.RuntimeIDsFromContext(ctx)
	m.contextIDs = append(m.contextIDs, ids)
	if len(m.responses) == 0 {
		return gopact.ModelResponse{}, fmt.Errorf("missing mock response")
	}
	response := m.responses[0]
	m.responses = m.responses[1:]
	return response, nil
}

func requireMockModelRuntimeIDs(t *testing.T, requests []gopact.ModelRequest, contextIDs []gopact.RuntimeIDs, want gopact.RuntimeIDs) {
	t.Helper()
	if len(requests) == 0 {
		t.Fatal("model requests are empty")
	}
	if len(contextIDs) != len(requests) {
		t.Fatalf("model context ids = %d, want %d", len(contextIDs), len(requests))
	}
	for i := range requests {
		if requests[i].IDs != want {
			t.Fatalf("model request %d IDs = %+v, want %+v", i, requests[i].IDs, want)
		}
		if contextIDs[i] != want {
			t.Fatalf("model context %d IDs = %+v, want %+v", i, contextIDs[i], want)
		}
	}
}

func requireMockModelOptions(t *testing.T, requests []gopact.ModelRequest, wantMaxOutputTokens int, wantTemperature float64, wantThinkingType string) {
	t.Helper()
	for i, request := range requests {
		if request.Budget.MaxOutputTokens != wantMaxOutputTokens {
			t.Fatalf("model request %d max output tokens = %d, want %d", i, request.Budget.MaxOutputTokens, wantMaxOutputTokens)
		}
		if request.Temperature == nil || *request.Temperature != wantTemperature {
			t.Fatalf("model request %d temperature = %v, want %v", i, request.Temperature, wantTemperature)
		}
		if request.ThinkingType != wantThinkingType {
			t.Fatalf("model request %d thinking type = %q, want %q", i, request.ThinkingType, wantThinkingType)
		}
	}
}
