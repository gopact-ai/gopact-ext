package agenttool

import (
	"context"
	"errors"
	"iter"
	"testing"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact/a2a"
)

func TestNewStreamsAgentAndReturnsToolResultWithEvidence(t *testing.T) {
	artifact := gopact.ArtifactRef{ID: "plan-1", Name: "plan.md", URI: "memory://plan-1"}
	wantIDs := gopact.RuntimeIDs{
		RunID:    "run-1",
		ThreadID: "thread-1",
		CallID:   "parent-call-1",
		TraceID:  "trace-1",
	}
	var gotTask a2a.Task
	var gotContextIDs gopact.RuntimeIDs

	agent := &streamAgent{
		card: a2a.AgentCard{
			Name:        "planner",
			Description: "Drafts plans.",
			URL:         "memory://planner",
		},
		streamFunc: func(ctx context.Context, task a2a.Task) iter.Seq2[a2a.TaskEvent, error] {
			gotTask = task
			gotContextIDs, _ = gopact.RuntimeIDsFromContext(ctx)
			return func(yield func(a2a.TaskEvent, error) bool) {
				if !yield(a2a.TaskEvent{TaskID: task.ID, IDs: task.IDs, Message: "draft ready"}, nil) {
					return
				}
				if !yield(a2a.TaskEvent{TaskID: task.ID, IDs: task.IDs, Artifacts: []gopact.ArtifactRef{artifact}}, nil) {
					return
				}
				yield(a2a.TaskEvent{
					TaskID: task.ID,
					IDs:    task.IDs,
					Status: a2a.TaskStatusCompleted,
					Result: &a2a.Result{
						TaskID:    task.ID,
						Output:    "done",
						Artifacts: []gopact.ArtifactRef{artifact},
						Metadata:  map[string]any{"phase": "final"},
					},
				}, nil)
			}
		},
	}

	tool, err := New(agent)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	spec, err := tool.Spec(context.Background())
	if err != nil {
		t.Fatalf("Spec() error = %v", err)
	}
	if spec.Name != "planner" || spec.Description != "Drafts plans." {
		t.Fatalf("spec = %+v, want card-derived tool spec", spec)
	}

	result, err := tool.Invoke(
		gopact.ContextWithRuntimeIDs(context.Background(), wantIDs),
		[]byte(`{"input":"ship it","task_id":"task-1"}`),
	)
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if gotTask.ID != "task-1" || gotTask.Input != "ship it" || gotTask.IDs != wantIDs {
		t.Fatalf("task = %+v, want input, task id, and runtime ids", gotTask)
	}
	if gotContextIDs != wantIDs {
		t.Fatalf("context ids = %+v, want %+v", gotContextIDs, wantIDs)
	}
	if result.Content != "done" {
		t.Fatalf("content = %q, want done", result.Content)
	}
	if len(result.Artifacts) != 1 || result.Artifacts[0].ID != artifact.ID {
		t.Fatalf("artifacts = %+v, want deduped child artifact", result.Artifacts)
	}
	requireEventTypes(t, result.Events,
		gopact.EventA2AMessageReceived,
		gopact.EventA2AArtifactUpdated,
		gopact.EventA2ATaskCompleted,
	)
	if result.Events[0].Message == nil || result.Events[0].Message.Text() != "draft ready" {
		t.Fatalf("message event = %+v, want assistant message", result.Events[0])
	}
	if result.Events[2].Result == nil || result.Events[2].Result.Content != "done" {
		t.Fatalf("completed event = %+v, want tool result content", result.Events[2])
	}
	if result.Events[2].Metadata["agent_name"] != "planner" ||
		result.Events[2].Metadata["agent_url"] != "memory://planner" ||
		result.Events[2].Metadata["a2a_task_id"] != "task-1" ||
		result.Events[2].Metadata["a2a_status"] != string(a2a.TaskStatusCompleted) {
		t.Fatalf("completed metadata = %+v, want A2A child evidence", result.Events[2].Metadata)
	}
}

func TestNewFallsBackToSendForNonStreamingAgent(t *testing.T) {
	artifact := gopact.ArtifactRef{ID: "review-1", URI: "memory://review-1"}
	agent := &sendAgent{
		card: a2a.AgentCard{Name: "reviewer", Description: "Reviews work."},
		sendFunc: func(ctx context.Context, task a2a.Task) (a2a.Result, error) {
			if task.Input != "check this" {
				t.Fatalf("task input = %q, want check this", task.Input)
			}
			return a2a.Result{
				TaskID:    task.ID,
				Output:    "approved",
				Artifacts: []gopact.ArtifactRef{artifact},
				Metadata:  map[string]any{"score": "pass"},
			}, nil
		},
	}

	tool, err := New(agent, WithName("quality_review"))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	spec, err := tool.Spec(context.Background())
	if err != nil {
		t.Fatalf("Spec() error = %v", err)
	}
	if spec.Name != "quality_review" || spec.Description != "Reviews work." {
		t.Fatalf("spec = %+v, want overridden name and card description", spec)
	}

	result, err := tool.Invoke(context.Background(), []byte(`"check this"`))
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if result.Content != "approved" {
		t.Fatalf("content = %q, want approved", result.Content)
	}
	if len(result.Artifacts) != 1 || result.Artifacts[0].ID != artifact.ID {
		t.Fatalf("artifacts = %+v, want send artifact", result.Artifacts)
	}
	requireEventTypes(t, result.Events, gopact.EventA2ATaskCompleted)
	if result.Metadata["agent_name"] != "reviewer" || result.Metadata["score"] != "pass" {
		t.Fatalf("metadata = %+v, want merged child result metadata", result.Metadata)
	}
}

func TestNewPropagatesStreamingFailureWithEvidence(t *testing.T) {
	wantErr := errors.New("child failed")
	agent := &streamAgent{
		card: a2a.AgentCard{Name: "breaker"},
		streamFunc: func(ctx context.Context, task a2a.Task) iter.Seq2[a2a.TaskEvent, error] {
			return func(yield func(a2a.TaskEvent, error) bool) {
				yield(a2a.TaskEvent{
					TaskID: task.ID,
					IDs:    task.IDs,
					Status: a2a.TaskStatusFailed,
					Err:    wantErr,
				}, wantErr)
			}
		},
	}
	tool, err := New(agent)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := tool.Invoke(context.Background(), []byte(`{"input":"fail"}`))
	if !errors.Is(err, wantErr) {
		t.Fatalf("Invoke() error = %v, want %v", err, wantErr)
	}
	requireEventTypes(t, result.Events, gopact.EventA2ATaskFailed)
	if result.Events[0].Err == nil || result.Events[0].Metadata["a2a_status"] != string(a2a.TaskStatusFailed) {
		t.Fatalf("failed event = %+v, want failed evidence", result.Events[0])
	}
}

func TestNewRejectsNilAgent(t *testing.T) {
	_, err := New(nil)
	if !errors.Is(err, ErrAgentRequired) {
		t.Fatalf("New(nil) error = %v, want %v", err, ErrAgentRequired)
	}
}

func requireEventTypes(t *testing.T, events []gopact.Event, want ...gopact.EventType) {
	t.Helper()
	if len(events) != len(want) {
		t.Fatalf("events = %d, want %d: %+v", len(events), len(want), events)
	}
	for i := range want {
		if events[i].Type != want[i] {
			t.Fatalf("event[%d] type = %s, want %s", i, events[i].Type, want[i])
		}
	}
}

type sendAgent struct {
	card     a2a.AgentCard
	sendFunc func(ctx context.Context, task a2a.Task) (a2a.Result, error)
}

func (a *sendAgent) Card() a2a.AgentCard {
	return a.card
}

func (a *sendAgent) Send(ctx context.Context, task a2a.Task) (a2a.Result, error) {
	if a.sendFunc == nil {
		return a2a.Result{}, errors.New("unexpected Send call")
	}
	return a.sendFunc(ctx, task)
}

func (a *sendAgent) Cancel(_ context.Context, _ string) error {
	return nil
}

type streamAgent struct {
	card       a2a.AgentCard
	streamFunc func(ctx context.Context, task a2a.Task) iter.Seq2[a2a.TaskEvent, error]
}

func (a *streamAgent) Card() a2a.AgentCard {
	return a.card
}

func (a *streamAgent) Send(context.Context, a2a.Task) (a2a.Result, error) {
	return a2a.Result{}, errors.New("unexpected Send call")
}

func (a *streamAgent) Cancel(_ context.Context, _ string) error {
	return nil
}

func (a *streamAgent) Stream(ctx context.Context, task a2a.Task) iter.Seq2[a2a.TaskEvent, error] {
	if a.streamFunc == nil {
		return func(yield func(a2a.TaskEvent, error) bool) {
			yield(a2a.TaskEvent{}, errors.New("missing stream function"))
		}
	}
	return a.streamFunc(ctx, task)
}
