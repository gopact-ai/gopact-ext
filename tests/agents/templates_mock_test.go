package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact-ext/agents/agenttool"
	"github.com/gopact-ai/gopact-ext/agents/planexec"
	"github.com/gopact-ai/gopact-ext/agents/react"
	"github.com/gopact-ai/gopact/a2a"
	"github.com/gopact-ai/gopact/checkpoint"
	"github.com/gopact-ai/gopact/gopacttest"
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
