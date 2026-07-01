package planexec

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact/checkpoint"
	"github.com/gopact-ai/gopact/gopacttest"
)

func TestAgentRunsPlanExecuteSummarize(t *testing.T) {
	agent, err := New(
		PlannerFunc(func(_ context.Context, request PlanRequest) ([]Step, error) {
			if request.Task != "ship example" {
				t.Fatalf("task = %q, want ship example", request.Task)
			}
			return []Step{
				{ID: "draft", Instruction: "draft example"},
				{ID: "review", Instruction: "review example"},
			}, nil
		}),
		ExecutorFunc(func(_ context.Context, step Step) (StepResult, error) {
			return StepResult{StepID: step.ID, Output: "done " + step.ID}, nil
		}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	events, err := gopacttest.CollectEvents(agent.Run(context.Background(), State{Task: "ship example"}))
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

	output, ok := events[6].StepSnapshot.Output.(State)
	if !ok {
		t.Fatalf("summary output type = %T, want State", events[6].StepSnapshot.Output)
	}
	if !reflect.DeepEqual(output.Trace, []string{"plan", "execute", "summarize"}) {
		t.Fatalf("trace = %v, want plan execute summarize", output.Trace)
	}
	if output.Summary != "completed 2 steps" {
		t.Fatalf("summary = %q, want completed 2 steps", output.Summary)
	}
}

func TestNewModelAgentPlansAndExecutesWithModel(t *testing.T) {
	model := &scriptedResponseModel{
		responses: []gopact.ModelResponse{
			{Message: gopact.Message{Role: gopact.RoleAssistant, Content: "STEP: draft example\nSTEP: review example"}},
			{Message: gopact.Message{Role: gopact.RoleAssistant, Content: "done draft"}},
			{Message: gopact.Message{Role: gopact.RoleAssistant, Content: "done review"}},
		},
	}
	agent, err := NewModelAgent(model)
	if err != nil {
		t.Fatalf("NewModelAgent() error = %v", err)
	}

	events, err := gopacttest.CollectEvents(agent.Run(context.Background(), "ship example"))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	output, ok := events[6].StepSnapshot.Output.(State)
	if !ok {
		t.Fatalf("summary output type = %T, want State", events[6].StepSnapshot.Output)
	}
	if len(output.Steps) != 2 || output.Steps[0].Instruction != "draft example" || output.Steps[1].Instruction != "review example" {
		t.Fatalf("steps = %+v, want parsed model plan", output.Steps)
	}
	if len(output.Results) != 2 || output.Results[0].Output != "done draft" || output.Results[1].Output != "done review" {
		t.Fatalf("results = %+v, want model-backed execution results", output.Results)
	}
	if len(model.requests) != 3 {
		t.Fatalf("model requests = %d, want planner + two executor calls", len(model.requests))
	}
}

func TestNewModelAgentAcceptsTemplateOptions(t *testing.T) {
	store := checkpoint.NewMemory[State]()
	model := &scriptedResponseModel{
		responses: []gopact.ModelResponse{
			{Message: gopact.AssistantMessage("STEP: draft example")},
			{Message: gopact.AssistantMessage("done draft")},
		},
	}
	agent, err := NewModelAgent(
		model,
		WithModelOptions(gopact.WithMaxOutputTokens(256), gopact.WithTemperature(0.3)),
		WithApprovalPolicy(gopact.PolicyFunc(func(context.Context, gopact.PolicyRequest) (gopact.PolicyDecision, error) {
			return gopact.PolicyDecision{Action: gopact.PolicyAllow, Reason: "approved"}, nil
		})),
		WithCheckpointStore(store),
	)
	if err != nil {
		t.Fatalf("NewModelAgent() error = %v", err)
	}

	ids := gopact.RuntimeIDs{RunID: "run-planexec-model-options", ThreadID: "thread-planexec-model-options"}
	events, err := gopacttest.CollectEvents(agent.Run(context.Background(), "ship example", gopact.WithRuntimeIDs(ids)))
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
		gopact.EventNodeStarted,
		gopact.EventNodeCompleted,
		gopact.EventRunCompleted,
	)
	if len(model.requests) != 2 {
		t.Fatalf("model requests = %d, want planner and executor", len(model.requests))
	}
	for i, request := range model.requests {
		if request.Budget.MaxOutputTokens != 256 {
			t.Fatalf("request %d max output tokens = %d, want 256", i, request.Budget.MaxOutputTokens)
		}
		if request.Temperature == nil || *request.Temperature != 0.3 {
			t.Fatalf("request %d temperature = %v, want 0.3", i, request.Temperature)
		}
	}
	latest, ok, err := store.Latest(context.Background(), ids.ThreadID)
	if err != nil || !ok {
		t.Fatalf("Latest() = ok:%v err:%v, want checkpoint", ok, err)
	}
	if latest.Node != "summarize" || latest.Phase != gopact.StepCompleted {
		t.Fatalf("latest checkpoint = %+v, want completed summarize", latest)
	}
}

func TestAgentApprovalPolicyInterruptsAndResumesFromStepExport(t *testing.T) {
	executions := 0
	policyCalls := 0
	agent, err := New(
		PlannerFunc(func(context.Context, PlanRequest) ([]Step, error) {
			return []Step{{ID: "draft", Instruction: "draft example"}}, nil
		}),
		ExecutorFunc(func(_ context.Context, step Step) (StepResult, error) {
			executions++
			return StepResult{StepID: step.ID, Output: "done " + step.ID}, nil
		}),
		WithApprovalPolicy(gopact.PolicyFunc(func(_ context.Context, req gopact.PolicyRequest) (gopact.PolicyDecision, error) {
			policyCalls++
			if req.Boundary != gopact.PolicyBoundaryNode || req.Action != gopact.PolicyActionExec {
				t.Fatalf("policy request = %+v, want node exec", req)
			}
			return gopact.PolicyDecision{Action: gopact.PolicyReview, Reason: "needs approval"}, nil
		})),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ids := gopact.RuntimeIDs{RunID: "run-planexec-approval", ThreadID: "thread-planexec-approval"}
	events, err := gopacttest.CollectEvents(agent.Run(context.Background(), "ship example", gopact.WithRuntimeIDs(ids)))
	if !errors.Is(err, gopact.ErrInterrupted) {
		t.Fatalf("Run() error = %v, want ErrInterrupted", err)
	}
	if executions != 0 {
		t.Fatalf("executions before approval = %d, want 0", executions)
	}
	gopacttest.RequireEventTypes(t, events,
		gopact.EventRunStarted,
		gopact.EventNodeStarted,
		gopact.EventNodeCompleted,
		gopact.EventNodeStarted,
		gopact.EventInterrupted,
		gopact.EventRunInterrupted,
	)
	interrupted := events[4].StepSnapshot
	if interrupted == nil || interrupted.Phase != gopact.StepInterrupted || interrupted.Pending == nil {
		t.Fatalf("interrupted step = %+v, want pending approval", interrupted)
	}
	if interrupted.Pending.Type != gopact.InterruptApproval {
		t.Fatalf("pending interrupt type = %q, want approval", interrupted.Pending.Type)
	}

	resumedEvents, err := gopacttest.CollectEvents(agent.Run(context.Background(), State{},
		gopact.WithRuntimeIDs(ids),
		gopact.WithStepExport(gopact.StepExport{Version: 1, Step: *interrupted}),
		gopact.WithResumeRequest(gopact.ResumeRequest{
			StepID:      interrupted.ID,
			InterruptID: interrupted.Pending.ID,
			Payload:     map[string]any{"approved": true},
		}),
	))
	if err != nil {
		t.Fatalf("resumed Run() error = %v", err)
	}
	gopacttest.RequireEventTypes(t, resumedEvents,
		gopact.EventRunStarted,
		gopact.EventStepImported,
		gopact.EventResumeReceived,
		gopact.EventNodeResumed,
		gopact.EventNodeCompleted,
		gopact.EventNodeStarted,
		gopact.EventNodeCompleted,
		gopact.EventRunCompleted,
	)
	if executions != 1 {
		t.Fatalf("executions after approval = %d, want 1", executions)
	}
	if policyCalls != 1 {
		t.Fatalf("policy calls = %d, want initial review only", policyCalls)
	}
	output, ok := resumedEvents[6].StepSnapshot.Output.(State)
	if !ok || output.Summary != "completed 1 steps" {
		t.Fatalf("resumed output = %#v, want completed summary", resumedEvents[6].StepSnapshot.Output)
	}
}

func TestAgentCheckpointStoreResumesApprovalInterrupt(t *testing.T) {
	store := checkpoint.NewMemory[State]()
	plans := 0
	executions := 0
	agent, err := New(
		PlannerFunc(func(context.Context, PlanRequest) ([]Step, error) {
			plans++
			return []Step{{ID: "draft", Instruction: "draft example"}}, nil
		}),
		ExecutorFunc(func(_ context.Context, step Step) (StepResult, error) {
			executions++
			return StepResult{StepID: step.ID, Output: "done " + step.ID}, nil
		}),
		WithApprovalPolicy(gopact.PolicyFunc(func(context.Context, gopact.PolicyRequest) (gopact.PolicyDecision, error) {
			return gopact.PolicyDecision{Action: gopact.PolicyReview, Reason: "needs approval"}, nil
		})),
		WithCheckpointStore(store),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ids := gopact.RuntimeIDs{RunID: "run-planexec-checkpoint", ThreadID: "thread-planexec-checkpoint"}
	events, err := gopacttest.CollectEvents(agent.Run(context.Background(), "ship example", gopact.WithRuntimeIDs(ids)))
	if !errors.Is(err, gopact.ErrInterrupted) {
		t.Fatalf("Run() error = %v, want ErrInterrupted", err)
	}
	if plans != 1 || executions != 0 {
		t.Fatalf("before resume plans/executions = %d/%d, want 1/0", plans, executions)
	}
	latest, ok, err := store.Latest(context.Background(), ids.ThreadID)
	if err != nil || !ok {
		t.Fatalf("Latest() = ok:%v err:%v, want interrupted checkpoint", ok, err)
	}
	if latest.Phase != gopact.StepInterrupted || latest.Pending == nil {
		t.Fatalf("latest checkpoint = %+v, want interrupted approval", latest)
	}
	if events[4].StepSnapshot == nil || events[4].StepSnapshot.Pending == nil || events[4].StepSnapshot.Pending.ID != latest.Pending.ID {
		t.Fatalf("interrupted event = %+v, want checkpoint pending id", events[4])
	}

	resumed, err := gopacttest.CollectEvents(agent.Run(context.Background(), State{},
		gopact.WithRuntimeIDs(ids),
		gopact.WithResumeRequest(gopact.ResumeRequest{
			CheckpointID: latest.ID,
			InterruptID:  latest.Pending.ID,
			Payload:      map[string]any{"approved": true},
		}),
	))
	if err != nil {
		t.Fatalf("resumed Run() error = %v", err)
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
	if plans != 1 || executions != 1 {
		t.Fatalf("after resume plans/executions = %d/%d, want 1/1", plans, executions)
	}
}

func TestAgentCancelStopsBeforeSummary(t *testing.T) {
	executions := 0
	agent, err := New(
		PlannerFunc(func(context.Context, PlanRequest) ([]Step, error) {
			return []Step{{ID: "draft", Instruction: "draft example"}}, nil
		}),
		ExecutorFunc(func(context.Context, Step) (StepResult, error) {
			executions++
			return StepResult{}, context.Canceled
		}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	events, err := gopacttest.CollectEvents(agent.Run(context.Background(), "ship example"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context canceled", err)
	}
	gopacttest.RequireEventTypes(t, events,
		gopact.EventRunStarted,
		gopact.EventNodeStarted,
		gopact.EventNodeCompleted,
		gopact.EventNodeStarted,
		gopact.EventRunCanceled,
	)
	if executions != 1 {
		t.Fatalf("executions = %d, want 1", executions)
	}
	canceled := events[4].StepSnapshot
	if canceled == nil || canceled.Node != "execute" || canceled.Phase != gopact.StepCanceled {
		t.Fatalf("canceled step = %+v, want execute step_canceled", canceled)
	}
	output, ok := canceled.Output.(State)
	if !ok {
		t.Fatalf("canceled output type = %T, want State", canceled.Output)
	}
	if output.Summary != "" || !reflect.DeepEqual(output.Trace, []string{"plan"}) {
		t.Fatalf("canceled output = %+v, want no summary after plan", output)
	}
}

func TestNewRequiresPlannerAndExecutor(t *testing.T) {
	if _, err := New(nil, ExecutorFunc(func(context.Context, Step) (StepResult, error) {
		return StepResult{}, nil
	})); err == nil {
		t.Fatal("New() planner error = nil")
	}
	if _, err := New(PlannerFunc(func(context.Context, PlanRequest) ([]Step, error) {
		return nil, nil
	}), nil); err == nil {
		t.Fatal("New() executor error = nil")
	}
}

type scriptedResponseModel struct {
	responses []gopact.ModelResponse
	errors    []error
	requests  []gopact.ModelRequest
}

func (m *scriptedResponseModel) Generate(ctx context.Context, request gopact.ModelRequest) (gopact.ModelResponse, error) {
	if err := ctx.Err(); err != nil {
		return gopact.ModelResponse{}, err
	}
	m.requests = append(m.requests, request)
	if len(m.errors) > 0 {
		err := m.errors[0]
		m.errors = m.errors[1:]
		return gopact.ModelResponse{}, err
	}
	if len(m.responses) == 0 {
		return gopact.ModelResponse{Message: gopact.Message{Role: gopact.RoleAssistant, Content: "done"}}, nil
	}
	response := m.responses[0]
	m.responses = m.responses[1:]
	return response, nil
}
