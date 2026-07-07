package supervisor

import (
	"context"
	"errors"
	"iter"
	"testing"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact/gopacttest"
)

func TestAgentRoutesToSelectedChild(t *testing.T) {
	ids := gopact.RuntimeIDs{RunID: "run-1", ThreadID: "thread-1", TraceID: "trace-1"}
	child := &recordingRunnable{}
	agent, err := New(
		RouterFunc(func(_ context.Context, request Request) (Route, error) {
			if request.Task != "ship example" {
				t.Fatalf("router task = %q", request.Task)
			}
			return Route{Agent: "coder", Input: "write code"}, nil
		}),
		Child{Name: "coder", Runnable: child},
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	events, err := gopacttest.CollectEvents(agent.Run(context.Background(), "ship example", gopact.WithRuntimeIDs(ids)))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	gopacttest.RequireEventTypes(t, events,
		gopact.EventRunStarted,
		gopact.EventNodeStarted,
		gopact.EventNodeCompleted,
		gopact.EventRunStarted,
		gopact.EventRunCompleted,
		gopact.EventRunCompleted,
	)
	if child.input != "write code" {
		t.Fatalf("child input = %q, want routed input", child.input)
	}
	if child.ids.RunID != ids.RunID || child.ids.ThreadID != ids.ThreadID || child.ids.AgentID != "coder" {
		t.Fatalf("child IDs = %+v, want parent run/thread and selected agent", child.ids)
	}
	if events[2].StepSnapshot == nil || events[2].StepSnapshot.Output.(State).SelectedAgent != "coder" {
		t.Fatalf("route output = %#v, want selected coder", events[2].StepSnapshot)
	}
	if events[5].Metadata["selected_agent"] != "coder" {
		t.Fatalf("completion metadata = %+v, want selected agent", events[5].Metadata)
	}
}

func TestAgentInvokeReturnsTypedStateAndEmitsEvents(t *testing.T) {
	agent, err := New(
		RouterFunc(func(context.Context, Request) (Route, error) {
			return Route{Agent: "coder", Input: "write code"}, nil
		}),
		Child{Name: "coder", Runnable: &recordingRunnable{}},
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	var events []gopact.Event
	output, err := agent.Invoke(context.Background(), State{Task: "ship example"}, gopact.WithEvents(gopact.EventSinkFunc(func(_ context.Context, event gopact.Event) error {
		events = append(events, event)
		return nil
	})))
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if output.SelectedAgent != "coder" {
		t.Fatalf("selected agent = %q, want coder", output.SelectedAgent)
	}
	if len(events) == 0 || events[len(events)-1].Type != gopact.EventRunCompleted {
		t.Fatalf("sink events = %+v, want terminal completion", events)
	}
}

func TestAgentFailsWhenChildFails(t *testing.T) {
	childErr := errors.New("child failed")
	agent, err := New(
		RouterFunc(func(context.Context, Request) (Route, error) {
			return Route{Agent: "coder", Input: "write code"}, nil
		}),
		Child{Name: "coder", Runnable: failingRunnable{err: childErr}},
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	events, err := gopacttest.CollectEvents(agent.Run(context.Background(), "ship example"))
	if !errors.Is(err, childErr) {
		t.Fatalf("Run() error = %v, want child error", err)
	}
	gopacttest.RequireEventTypes(t, events,
		gopact.EventRunStarted,
		gopact.EventNodeStarted,
		gopact.EventNodeCompleted,
		gopact.EventRunFailed,
		gopact.EventRunFailed,
	)
	if events[4].Err == nil || events[4].Metadata["selected_agent"] != "coder" {
		t.Fatalf("failure event = %+v, want child failure evidence", events[4])
	}
}

func TestAgentFailsWhenRouteTargetsUnknownChild(t *testing.T) {
	agent, err := New(
		RouterFunc(func(context.Context, Request) (Route, error) {
			return Route{Agent: "missing"}, nil
		}),
		Child{Name: "coder", Runnable: &recordingRunnable{}},
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	events, err := gopacttest.CollectEvents(agent.Run(context.Background(), "ship example"))
	if !errors.Is(err, ErrRouteAgentUnknown) {
		t.Fatalf("Run() error = %v, want ErrRouteAgentUnknown", err)
	}
	gopacttest.RequireEventTypes(t, events,
		gopact.EventRunStarted,
		gopact.EventNodeStarted,
		gopact.EventRunFailed,
	)
	if events[2].Metadata["selected_agent"] != "missing" {
		t.Fatalf("failure metadata = %+v, want selected missing", events[2].Metadata)
	}
}

type recordingRunnable struct {
	input any
	ids   gopact.RuntimeIDs
}

func (r *recordingRunnable) Run(ctx context.Context, input any, opts ...gopact.RunOption) iter.Seq2[gopact.Event, error] {
	return func(yield func(gopact.Event, error) bool) {
		r.input = input
		r.ids = gopact.ResolveRunOptions(opts...).IDs
		if !yield(gopact.Event{Type: gopact.EventRunStarted, IDs: r.ids}, nil) {
			return
		}
		yield(gopact.Event{Type: gopact.EventRunCompleted, IDs: r.ids}, nil)
	}
}

type failingRunnable struct {
	err error
}

func (r failingRunnable) Run(_ context.Context, _ any, _ ...gopact.RunOption) iter.Seq2[gopact.Event, error] {
	return func(yield func(gopact.Event, error) bool) {
		yield(gopact.Event{Type: gopact.EventRunFailed, Err: r.err}, r.err)
	}
}
