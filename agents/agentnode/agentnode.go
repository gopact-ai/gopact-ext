// Package agentnode adapts A2A agents into typed graph nodes.
package agentnode

import (
	"context"
	"errors"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact/a2a"
	"github.com/gopact-ai/gopact/graph"
)

const (
	metadataAgentName  = "agent_name"
	metadataAgentURL   = "agent_url"
	metadataA2ATaskID  = "a2a_task_id"
	metadataA2AStatus  = "a2a_status"
	metadataA2AMessage = "a2a_message"
)

var (
	// ErrAgentRequired is returned when no child agent is supplied.
	ErrAgentRequired = errors.New("agentnode: agent is required")
	// ErrTaskMapperRequired is returned when no state-to-task mapper is supplied.
	ErrTaskMapperRequired = errors.New("agentnode: task mapper is required")
	// ErrResultMapperRequired is returned when no result-to-state mapper is supplied.
	ErrResultMapperRequired = errors.New("agentnode: result mapper is required")
)

// TaskMapper converts graph state into an A2A task.
type TaskMapper[S any] func(ctx context.Context, state S) (a2a.Task, error)

// ResultMapper applies a terminal A2A result to graph state.
type ResultMapper[S any] func(ctx context.Context, state S, result a2a.Result) (S, error)

// New returns a graph node that delegates the current state to an A2A agent.
func New[S any](agent a2a.Agent, mapTask TaskMapper[S], mapResult ResultMapper[S]) (graph.NodeFunc[S], error) {
	if agent == nil {
		return nil, ErrAgentRequired
	}
	if mapTask == nil {
		return nil, ErrTaskMapperRequired
	}
	if mapResult == nil {
		return nil, ErrResultMapperRequired
	}
	card := agent.Card()
	return func(ctx context.Context, state S) (S, error) {
		if ctx == nil {
			ctx = context.TODO()
		}
		if err := ctx.Err(); err != nil {
			return state, err
		}
		task, err := mapTask(ctx, state)
		if err != nil {
			return state, err
		}
		task = taskWithContextDefaults(ctx, task)
		callCtx := ctx
		if !task.IDs.IsZero() {
			callCtx = gopact.ContextWithRuntimeIDs(callCtx, task.IDs)
		}

		if streamer, ok := agent.(a2a.StreamingAgent); ok {
			return runStreaming(callCtx, state, streamer, card, task, mapResult)
		}
		return runSend(callCtx, state, agent, card, task, mapResult)
	}, nil
}

func runStreaming[S any](
	ctx context.Context,
	state S,
	agent a2a.StreamingAgent,
	card a2a.AgentCard,
	task a2a.Task,
	mapResult ResultMapper[S],
) (S, error) {
	result := a2a.Result{TaskID: task.ID}
	for taskEvent, err := range agent.Stream(ctx, task) {
		taskEvent = taskEvent.WithDefaults(task)
		if err != nil && taskEvent.Err == nil {
			taskEvent.Err = err
		}
		if taskEvent.Result != nil {
			result = *taskEvent.Result
		}
		if !graph.EmitNodeEvent(ctx, runtimeEvent(taskEvent, card), err) {
			return state, graph.ErrNodeEventYieldStopped
		}
		if err != nil {
			return state, err
		}
		if taskEvent.Err != nil {
			return state, taskEvent.Err
		}
	}
	return mapResult(ctx, state, result)
}

func runSend[S any](
	ctx context.Context,
	state S,
	agent a2a.Agent,
	card a2a.AgentCard,
	task a2a.Task,
	mapResult ResultMapper[S],
) (S, error) {
	result, err := agent.Send(ctx, task)
	event := a2a.TaskEvent{
		TaskID:    result.TaskID,
		IDs:       task.IDs,
		Status:    a2a.TaskStatusCompleted,
		Result:    &result,
		Artifacts: result.Artifacts,
	}
	if err != nil {
		event.Status = a2a.TaskStatusFailed
		event.Err = err
	}
	event = event.WithDefaults(task)
	if !graph.EmitNodeEvent(ctx, runtimeEvent(event, card), err) {
		return state, graph.ErrNodeEventYieldStopped
	}
	if err != nil {
		return state, err
	}
	return mapResult(ctx, state, result)
}

func taskWithContextDefaults(ctx context.Context, task a2a.Task) a2a.Task {
	if ids, ok := gopact.RuntimeIDsFromContext(ctx); ok && !ids.IsZero() {
		task.IDs = task.IDs.WithDefaults(ids)
	}
	if task.ID == "" {
		task.ID = fallbackTaskID(task.IDs)
	}
	return task
}

func runtimeEvent(event a2a.TaskEvent, card a2a.AgentCard) gopact.Event {
	metadata := mergeMetadata(event.Metadata, nil, card)
	if event.TaskID != "" {
		metadata[metadataA2ATaskID] = event.TaskID
	}
	if event.Status != "" {
		metadata[metadataA2AStatus] = string(event.Status)
	}
	if event.Message != "" {
		metadata[metadataA2AMessage] = event.Message
	}

	artifacts := copyArtifactRefs(event.Artifacts)
	var result *gopact.ToolResult
	if event.Result != nil {
		child := *event.Result
		artifacts = append(artifacts, copyArtifactRefs(child.Artifacts)...)
		result = &gopact.ToolResult{
			Content:   child.Output,
			Artifacts: copyArtifactRefs(child.Artifacts),
			Metadata:  mergeMetadata(nil, child.Metadata, card),
		}
	}
	artifacts = dedupeArtifactRefs(artifacts)

	var message *gopact.Message
	if event.Message != "" {
		msg := gopact.AssistantMessage(event.Message)
		message = &msg
	}

	return gopact.Event{
		Type:      taskEventType(event),
		IDs:       event.IDs,
		Message:   message,
		Result:    result,
		Artifacts: artifacts,
		Metadata:  metadata,
		Err:       event.Err,
	}.WithRuntimeDefaults(event.IDs)
}

func taskEventType(event a2a.TaskEvent) gopact.EventType {
	switch event.Status {
	case a2a.TaskStatusCompleted:
		return gopact.EventA2ATaskCompleted
	case a2a.TaskStatusFailed:
		return gopact.EventA2ATaskFailed
	case a2a.TaskStatusCanceled:
		return gopact.EventA2ATaskCanceled
	case a2a.TaskStatusSubmitted, a2a.TaskStatusRunning:
		return gopact.EventA2ATaskStatusUpdated
	}
	if event.Err != nil {
		return gopact.EventA2ATaskFailed
	}
	if event.Result != nil {
		return gopact.EventA2ATaskCompleted
	}
	if len(event.Artifacts) > 0 {
		return gopact.EventA2AArtifactUpdated
	}
	if event.Message != "" {
		return gopact.EventA2AMessageReceived
	}
	return gopact.EventA2ATaskStatusUpdated
}

func mergeMetadata(dst map[string]any, src map[string]any, card a2a.AgentCard) map[string]any {
	out := copyAnyMap(dst)
	if out == nil {
		out = make(map[string]any)
	}
	for key, value := range src {
		out[key] = value
	}
	if card.Name != "" {
		out[metadataAgentName] = card.Name
	}
	if card.URL != "" {
		out[metadataAgentURL] = card.URL
	}
	return out
}

func copyAnyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func copyArtifactRefs(in []gopact.ArtifactRef) []gopact.ArtifactRef {
	if len(in) == 0 {
		return nil
	}
	out := make([]gopact.ArtifactRef, len(in))
	copy(out, in)
	for i := range out {
		out[i].Metadata = copyAnyMap(out[i].Metadata)
	}
	return out
}

func dedupeArtifactRefs(in []gopact.ArtifactRef) []gopact.ArtifactRef {
	if len(in) == 0 {
		return nil
	}
	out := make([]gopact.ArtifactRef, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, ref := range in {
		key := ref.ID
		if key == "" {
			key = ref.URI
		}
		if key != "" {
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
		}
		out = append(out, ref)
	}
	return out
}

func fallbackTaskID(ids gopact.RuntimeIDs) string {
	if ids.CallID != "" {
		return ids.CallID
	}
	return ids.RunID
}
