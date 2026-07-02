package agentnode

import (
	"context"
	"errors"
	"iter"
	"reflect"
	"testing"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact/a2a"
	"github.com/gopact-ai/gopact/gopacttest"
	"github.com/gopact-ai/gopact/graph"
)

type workflowState struct {
	Input     string
	Output    string
	Artifacts []gopact.ArtifactRef
}

func TestNewStreamsAgentAsGraphNodeWithNestedEvidence(t *testing.T) {
	artifact := gopact.ArtifactRef{ID: "plan-1", URI: "memory://plan-1"}
	wantIDs := gopact.RuntimeIDs{
		RunID:    "run-1",
		ThreadID: "thread-1",
		TraceID:  "trace-1",
	}
	var gotTask a2a.Task
	var gotContextIDs gopact.RuntimeIDs
	agent := &streamAgent{
		card: a2a.AgentCard{Name: "planner", Description: "Plans work.", URL: "memory://planner"},
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
						Output:    "planned",
						Artifacts: []gopact.ArtifactRef{artifact},
						Metadata:  map[string]any{"phase": "final"},
					},
				}, nil)
			}
		},
	}
	node, err := New[workflowState](
		agent,
		func(ctx context.Context, state workflowState) (a2a.Task, error) {
			ids, _ := gopact.RuntimeIDsFromContext(ctx)
			return a2a.Task{
				ID:       "task-1",
				IDs:      ids,
				Input:    state.Input,
				Metadata: map[string]any{"tenant": "acme"},
			}, nil
		},
		func(_ context.Context, state workflowState, result a2a.Result) (workflowState, error) {
			state.Output = result.Output
			state.Artifacts = append(state.Artifacts, result.Artifacts...)
			return state, nil
		},
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	g := graph.New[workflowState]()
	g.AddNode("planner", node)
	g.AddEdge(graph.Start, "planner")
	g.AddEdge("planner", graph.End)
	run, err := g.Compile()
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	events, err := gopacttest.CollectEvents(run.Run(context.Background(), workflowState{Input: "ship it"}, graph.WithRuntimeIDs(wantIDs)))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if gotTask.ID != "task-1" || gotTask.Input != "ship it" || gotTask.IDs != wantIDs {
		t.Fatalf("task = %+v, want mapped task with runtime ids", gotTask)
	}
	if gotTask.Metadata["tenant"] != "acme" {
		t.Fatalf("task metadata = %+v, want tenant", gotTask.Metadata)
	}
	if gotContextIDs != wantIDs {
		t.Fatalf("context ids = %+v, want %+v", gotContextIDs, wantIDs)
	}
	gopacttest.RequireEventTypes(t, events,
		gopact.EventRunStarted,
		gopact.EventNodeStarted,
		gopact.EventA2AMessageReceived,
		gopact.EventA2AArtifactUpdated,
		gopact.EventA2ATaskCompleted,
		gopact.EventNodeCompleted,
		gopact.EventRunCompleted,
	)
	for _, event := range events[2:5] {
		if event.Metadata[graph.EventMetadataParentNode] != "planner" ||
			event.Metadata[graph.EventMetadataParentStep] != 1 {
			t.Fatalf("nested event metadata = %+v, want parent planner step 1", event.Metadata)
		}
		if event.Metadata["agent_name"] != "planner" || event.Metadata["agent_url"] != "memory://planner" {
			t.Fatalf("nested event metadata = %+v, want agent card evidence", event.Metadata)
		}
	}
	if events[2].Message == nil || events[2].Message.Text() != "draft ready" {
		t.Fatalf("message event = %+v, want draft ready", events[2])
	}
	if len(events[3].Artifacts) != 1 || events[3].Artifacts[0].ID != artifact.ID {
		t.Fatalf("artifact event = %+v, want artifact", events[3])
	}
	if events[4].Result == nil || events[4].Result.Content != "planned" ||
		events[4].Result.Metadata["phase"] != "final" {
		t.Fatalf("completed event result = %+v, want child result", events[4].Result)
	}
	output, ok := events[5].StepSnapshot.Output.(workflowState)
	if !ok {
		t.Fatalf("node output type = %T, want workflowState", events[5].StepSnapshot.Output)
	}
	if output.Output != "planned" || !reflect.DeepEqual(output.Artifacts, []gopact.ArtifactRef{artifact}) {
		t.Fatalf("state output = %+v, want mapped result", output)
	}
}

func TestNewFallsBackToSendAndPropagatesFailureEvidence(t *testing.T) {
	wantErr := errors.New("child failed")
	agent := &sendAgent{
		card: a2a.AgentCard{Name: "reviewer"},
		sendFunc: func(_ context.Context, task a2a.Task) (a2a.Result, error) {
			if task.Input != "review this" {
				t.Fatalf("task input = %q, want review this", task.Input)
			}
			return a2a.Result{TaskID: task.ID}, wantErr
		},
	}
	node, err := New[workflowState](
		agent,
		func(ctx context.Context, state workflowState) (a2a.Task, error) {
			ids, _ := gopact.RuntimeIDsFromContext(ctx)
			return a2a.Task{ID: "review-task", IDs: ids, Input: state.Input}, nil
		},
		func(_ context.Context, state workflowState, result a2a.Result) (workflowState, error) {
			state.Output = result.Output
			return state, nil
		},
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	g := graph.New[workflowState]()
	g.AddNode("review", node)
	g.AddEdge(graph.Start, "review")
	g.AddEdge("review", graph.End)
	run, err := g.Compile()
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	events, err := gopacttest.CollectEvents(run.Run(context.Background(), workflowState{Input: "review this"}))
	if !errors.Is(err, wantErr) {
		t.Fatalf("Run() error = %v, want %v", err, wantErr)
	}
	gopacttest.RequireEventTypes(t, events,
		gopact.EventRunStarted,
		gopact.EventNodeStarted,
		gopact.EventA2ATaskFailed,
	)
	if events[2].Err == nil || events[2].Metadata["a2a_status"] != string(a2a.TaskStatusFailed) {
		t.Fatalf("failed event = %+v, want failed A2A evidence", events[2])
	}
	if events[2].Metadata[graph.EventMetadataParentNode] != "review" {
		t.Fatalf("failed event metadata = %+v, want graph parent", events[2].Metadata)
	}
}

func TestNewRejectsMissingDependencies(t *testing.T) {
	agent := &sendAgent{card: a2a.AgentCard{Name: "planner"}}
	mapTask := func(context.Context, workflowState) (a2a.Task, error) {
		return a2a.Task{Input: "task"}, nil
	}
	mapResult := func(_ context.Context, state workflowState, _ a2a.Result) (workflowState, error) {
		return state, nil
	}

	tests := []struct {
		name      string
		agent     a2a.Agent
		mapTask   TaskMapper[workflowState]
		mapResult ResultMapper[workflowState]
		want      error
	}{
		{name: "nil agent", agent: nil, mapTask: mapTask, mapResult: mapResult, want: ErrAgentRequired},
		{name: "nil task mapper", agent: agent, mapTask: nil, mapResult: mapResult, want: ErrTaskMapperRequired},
		{name: "nil result mapper", agent: agent, mapTask: mapTask, mapResult: nil, want: ErrResultMapperRequired},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(tt.agent, tt.mapTask, tt.mapResult)
			if !errors.Is(err, tt.want) {
				t.Fatalf("New() error = %v, want %v", err, tt.want)
			}
		})
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
		return a2a.Result{}, errors.New("missing send function")
	}
	return a.sendFunc(ctx, task)
}

func (a *sendAgent) Cancel(context.Context, string) error {
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

func (a *streamAgent) Cancel(context.Context, string) error {
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
