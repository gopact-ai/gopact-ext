package planexec

import (
	"context"
	"reflect"
	"testing"

	"github.com/gopact-ai/gopact"
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
