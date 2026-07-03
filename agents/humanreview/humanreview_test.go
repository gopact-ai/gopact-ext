package humanreview

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact/checkpoint"
	"github.com/gopact-ai/gopact/gopacttest"
	"github.com/gopact-ai/gopact/graph"
)

type reviewState struct {
	Task  string
	Trace []string
}

func TestNewApprovalNodeInterruptsAndResumesSuccessor(t *testing.T) {
	mapCalls := 0
	applyCalls := 0
	node, err := New(func(ctx context.Context, state reviewState) (Request, error) {
		mapCalls++
		ids, _ := gopact.RuntimeIDsFromContext(ctx)
		if ids.RunID != "run-review" {
			t.Fatalf("runtime ids = %+v, want run-review", ids)
		}
		if state.Task != "ship release" {
			t.Fatalf("state task = %q, want ship release", state.Task)
		}
		return Request{
			ID:         "review:release",
			Reason:     "release approval required",
			RequiredBy: "release",
			Prompt:     gopact.UserMessage("Approve release?"),
			Metadata:   map[string]any{"risk": "medium"},
		}, nil
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	g := graph.New[reviewState]()
	g.AddNode("review", node)
	g.AddNode("apply", func(_ context.Context, state reviewState) (reviewState, error) {
		applyCalls++
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

	ids := gopact.RuntimeIDs{RunID: "run-review", ThreadID: "thread-review"}
	events, err := gopacttest.CollectEvents(run.Run(context.Background(), reviewState{Task: "ship release"}, graph.WithRuntimeIDs(ids)))
	if !errors.Is(err, gopact.ErrInterrupted) {
		t.Fatalf("Run() error = %v, want ErrInterrupted", err)
	}
	if mapCalls != 1 || applyCalls != 0 {
		t.Fatalf("before resume map/apply calls = %d/%d, want 1/0", mapCalls, applyCalls)
	}
	gopacttest.RequireEventTypes(t, events,
		gopact.EventRunStarted,
		gopact.EventNodeStarted,
		gopact.EventInterrupted,
		gopact.EventRunInterrupted,
	)
	interrupted := events[2].StepSnapshot
	if interrupted == nil || interrupted.Pending == nil {
		t.Fatalf("interrupted snapshot = %+v, want pending approval", interrupted)
	}
	pending := interrupted.Pending
	if pending.ID != "review:release" ||
		pending.Type != gopact.InterruptApproval ||
		pending.Reason != "release approval required" ||
		pending.RequiredBy != "release" {
		t.Fatalf("pending = %+v, want release approval", pending)
	}
	if pending.Prompt.Text() != "Approve release?" {
		t.Fatalf("pending prompt = %q, want approval prompt", pending.Prompt.Text())
	}
	if pending.Metadata["template"] != "humanreview" || pending.Metadata["risk"] != "medium" {
		t.Fatalf("pending metadata = %+v, want template and custom metadata", pending.Metadata)
	}

	resumed, err := gopacttest.CollectEvents(run.Run(context.Background(), reviewState{},
		graph.WithRuntimeIDs(ids),
		graph.WithStepExport(gopact.StepExport{Version: gopact.RunExportVersion, Step: *interrupted}),
		graph.WithResumeRequest(gopact.ResumeRequest{
			StepID:      interrupted.ID,
			InterruptID: pending.ID,
			Payload:     map[string]any{"approved": true},
		}),
	))
	if err != nil {
		t.Fatalf("resume Run() error = %v", err)
	}
	if mapCalls != 1 || applyCalls != 1 {
		t.Fatalf("after resume map/apply calls = %d/%d, want 1/1", mapCalls, applyCalls)
	}
	gopacttest.RequireEventTypes(t, resumed,
		gopact.EventRunStarted,
		gopact.EventStepImported,
		gopact.EventResumeReceived,
		gopact.EventNodeResumed,
		gopact.EventNodeCompleted,
		gopact.EventRunCompleted,
	)
	output, ok := resumed[4].StepSnapshot.Output.(reviewState)
	if !ok {
		t.Fatalf("resumed output type = %T, want reviewState", resumed[4].StepSnapshot.Output)
	}
	if !reflect.DeepEqual(output.Trace, []string{"apply"}) {
		t.Fatalf("resumed trace = %v, want [apply]", output.Trace)
	}
}

func TestNewApprovalNodeResumesFromCheckpoint(t *testing.T) {
	store := checkpoint.NewMemory[reviewState]()
	applyCalls := 0
	node, err := New(func(context.Context, reviewState) (Request, error) {
		return Request{ID: "review:checkpoint", Reason: "checkpoint approval required"}, nil
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	g := graph.New[reviewState]()
	g.AddNode("review", node)
	g.AddNode("apply", func(_ context.Context, state reviewState) (reviewState, error) {
		applyCalls++
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

	ids := gopact.RuntimeIDs{RunID: "run-checkpoint", ThreadID: "thread-checkpoint"}
	_, err = gopacttest.CollectEvents(run.Run(context.Background(), reviewState{Task: "ship"},
		graph.WithRuntimeIDs(ids),
		graph.WithCheckpointStore(store),
	))
	if !errors.Is(err, gopact.ErrInterrupted) {
		t.Fatalf("Run() error = %v, want ErrInterrupted", err)
	}
	if applyCalls != 0 {
		t.Fatalf("apply calls before resume = %d, want 0", applyCalls)
	}
	latest, ok, err := store.Latest(context.Background(), ids.ThreadID)
	if err != nil || !ok {
		t.Fatalf("Latest() = ok:%v err:%v, want checkpoint", ok, err)
	}
	if latest.Phase != gopact.StepInterrupted || latest.Pending == nil || latest.Pending.ID != "review:checkpoint" {
		t.Fatalf("latest checkpoint = %+v, want interrupted human review", latest)
	}

	resumed, err := gopacttest.CollectEvents(run.Run(context.Background(), reviewState{},
		graph.WithRuntimeIDs(ids),
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
	if applyCalls != 1 {
		t.Fatalf("apply calls after resume = %d, want 1", applyCalls)
	}
	gopacttest.RequireEventTypes(t, resumed,
		gopact.EventRunStarted,
		gopact.EventCheckpointLoaded,
		gopact.EventResumeReceived,
		gopact.EventNodeResumed,
		gopact.EventNodeCompleted,
		gopact.EventRunCompleted,
	)
}

func TestNewUsesDefaultApprovalResumeSchema(t *testing.T) {
	node, err := New(func(context.Context, reviewState) (Request, error) {
		return Request{ID: "review:default-schema"}, nil
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = node(context.Background(), reviewState{})
	var interruptErr *gopact.InterruptError
	if !errors.As(err, &interruptErr) {
		t.Fatalf("node error = %T %v, want InterruptError", err, err)
	}
	schema := interruptErr.Record.ResumeSchema
	if schema["type"] != "object" {
		t.Fatalf("resume schema = %#v, want object schema", schema)
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("resume schema properties = %T, want map", schema["properties"])
	}
	approved, ok := props["approved"].(map[string]any)
	if !ok || approved["const"] != true {
		t.Fatalf("approved schema = %#v, want const true", props["approved"])
	}
}

func TestNewReturnsContextAndMapperErrors(t *testing.T) {
	mapperErr := errors.New("mapper failed")
	node, err := New(func(context.Context, reviewState) (Request, error) {
		return Request{}, mapperErr
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = node(context.Background(), reviewState{})
	if !errors.Is(err, mapperErr) {
		t.Fatalf("node error = %v, want mapper error", err)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	mapCalls := 0
	node, err = New(func(context.Context, reviewState) (Request, error) {
		mapCalls++
		return Request{ID: "review:unused"}, nil
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = node(canceled, reviewState{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("node error = %v, want context.Canceled", err)
	}
	if mapCalls != 0 {
		t.Fatalf("mapper calls = %d, want 0 after canceled context", mapCalls)
	}
}

func TestNewCopiesCustomResumeSchemaAndMetadata(t *testing.T) {
	schema := gopact.JSONSchema{
		"type":     "object",
		"required": []any{"decision"},
		"properties": map[string]any{
			"decision": gopact.JSONSchema{
				"type": "string",
				"enum": []any{"approve", "reject"},
			},
			"tags": []string{"release"},
		},
		"allOf": []any{
			map[string]any{
				"properties": map[string]any{
					"approved": map[string]any{"type": "boolean"},
				},
			},
		},
	}
	metadata := map[string]any{
		"owner":  "release",
		"audit":  map[string]any{"level": "high"},
		"labels": []any{"manual"},
	}
	node, err := New(func(context.Context, reviewState) (Request, error) {
		return Request{
			ID:           "review:custom-schema",
			ResumeSchema: schema,
			Metadata:     metadata,
		}, nil
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = node(context.Background(), reviewState{})
	var interruptErr *gopact.InterruptError
	if !errors.As(err, &interruptErr) {
		t.Fatalf("node error = %T %v, want InterruptError", err, err)
	}

	schema["type"] = "mutated"
	properties := schema["properties"].(map[string]any)
	properties["tags"].([]string)[0] = "mutated"
	properties["decision"].(gopact.JSONSchema)["type"] = "number"
	allOf := schema["allOf"].([]any)
	allOf[0].(map[string]any)["properties"].(map[string]any)["approved"].(map[string]any)["type"] = "string"
	metadata["owner"] = "mutated"
	metadata["audit"].(map[string]any)["level"] = "low"
	metadata["labels"].([]any)[0] = "mutated"

	record := interruptErr.Record
	if record.ResumeSchema["type"] != "object" {
		t.Fatalf("record schema type = %q, want object", record.ResumeSchema["type"])
	}
	recordProperties := record.ResumeSchema["properties"].(map[string]any)
	decision := recordProperties["decision"].(gopact.JSONSchema)
	if decision["type"] != "string" {
		t.Fatalf("decision schema = %#v, want copied string type", decision)
	}
	tags := recordProperties["tags"].([]string)
	if tags[0] != "release" {
		t.Fatalf("tags = %#v, want copied release tag", tags)
	}
	recordAllOf := record.ResumeSchema["allOf"].([]any)
	approved := recordAllOf[0].(map[string]any)["properties"].(map[string]any)["approved"].(map[string]any)
	if approved["type"] != "boolean" {
		t.Fatalf("approved schema = %#v, want copied boolean type", approved)
	}
	if record.Metadata["owner"] != "release" || record.Metadata["template"] != "humanreview" {
		t.Fatalf("metadata = %+v, want copied owner and template", record.Metadata)
	}
	audit := record.Metadata["audit"].(map[string]any)
	if audit["level"] != "high" {
		t.Fatalf("audit metadata = %+v, want copied high level", audit)
	}
	labels := record.Metadata["labels"].([]any)
	if labels[0] != "manual" {
		t.Fatalf("labels metadata = %+v, want copied manual label", labels)
	}
}

func TestNewRejectsMissingDependenciesAndInvalidRequests(t *testing.T) {
	if _, err := New[reviewState](nil); !errors.Is(err, ErrRequestMapperRequired) {
		t.Fatalf("New(nil) error = %v, want ErrRequestMapperRequired", err)
	}

	node, err := New(func(context.Context, reviewState) (Request, error) {
		return Request{}, nil
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = node(context.Background(), reviewState{})
	if !errors.Is(err, ErrInterruptIDRequired) {
		t.Fatalf("node error = %v, want ErrInterruptIDRequired", err)
	}
}
