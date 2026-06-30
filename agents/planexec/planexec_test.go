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
