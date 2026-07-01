// Package agenttool adapts A2A agents into ordinary gopact tools.
package agenttool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact/a2a"
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
	ErrAgentRequired = errors.New("agenttool: agent is required")
	// ErrToolNameRequired is returned when neither the agent card nor options provide a tool name.
	ErrToolNameRequired = errors.New("agenttool: tool name is required")
	// ErrInputRequired is returned when a tool invocation has no task input.
	ErrInputRequired = errors.New("agenttool: input is required")
)

// Option configures an agent-backed tool.
type Option func(*config)

type config struct {
	name        string
	description string
}

// WithName overrides the tool name derived from the agent card.
func WithName(name string) Option {
	return func(cfg *config) {
		cfg.name = strings.TrimSpace(name)
	}
}

// WithDescription overrides the tool description derived from the agent card.
func WithDescription(description string) Option {
	return func(cfg *config) {
		cfg.description = description
	}
}

// New returns a gopact tool that delegates invocations to an A2A agent.
func New(agent a2a.Agent, opts ...Option) (gopact.ToolFunc, error) {
	if agent == nil {
		return gopact.ToolFunc{}, ErrAgentRequired
	}
	card := agent.Card()
	cfg := config{
		name:        strings.TrimSpace(card.Name),
		description: card.Description,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	if cfg.name == "" {
		return gopact.ToolFunc{}, ErrToolNameRequired
	}

	spec := gopact.ObjectToolSpec(
		cfg.name,
		cfg.description,
		gopact.RequiredStringField("input", "Task input for the child agent."),
		gopact.ToolField{
			Name:   "task_id",
			Schema: gopact.JSONSchema{"type": "string", "description": "Optional child task id."},
		},
		gopact.ToolField{
			Name:   "metadata",
			Schema: gopact.JSONSchema{"type": "object", "description": "Optional metadata passed to the child task."},
		},
	)
	return gopact.ToolFunc{
		SpecValue: spec,
		InvokeFunc: func(ctx context.Context, raw json.RawMessage) (gopact.ToolResult, error) {
			return invoke(ctx, agent, card, raw)
		},
	}, nil
}

type toolInput struct {
	Input    string         `json:"input"`
	TaskID   string         `json:"task_id,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

func invoke(ctx context.Context, agent a2a.Agent, card a2a.AgentCard, raw json.RawMessage) (gopact.ToolResult, error) {
	input, err := decodeInput(raw)
	if err != nil {
		return gopact.ToolResult{}, err
	}
	if ctx == nil {
		ctx = context.TODO()
	}
	if err := ctx.Err(); err != nil {
		return gopact.ToolResult{}, err
	}
	ids, _ := gopact.RuntimeIDsFromContext(ctx)
	task := a2a.Task{
		ID:       input.TaskID,
		IDs:      ids,
		Input:    input.Input,
		Metadata: copyAnyMap(input.Metadata),
	}
	if task.ID == "" {
		task.ID = fallbackTaskID(ids)
	}

	streamer, ok := agent.(a2a.StreamingAgent)
	if ok {
		return invokeStreaming(ctx, streamer, card, task)
	}
	return invokeSend(ctx, agent, card, task)
}

func decodeInput(raw json.RawMessage) (toolInput, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return toolInput{}, ErrInputRequired
	}
	if raw[0] == '"' {
		var input string
		if err := json.Unmarshal(raw, &input); err != nil {
			return toolInput{}, fmt.Errorf("agenttool: decode input: %w", err)
		}
		if input == "" {
			return toolInput{}, ErrInputRequired
		}
		return toolInput{Input: input}, nil
	}

	var input toolInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return toolInput{}, fmt.Errorf("agenttool: decode input: %w", err)
	}
	if input.Input == "" {
		return toolInput{}, ErrInputRequired
	}
	input.TaskID = strings.TrimSpace(input.TaskID)
	return input, nil
}

func invokeStreaming(
	ctx context.Context,
	agent a2a.StreamingAgent,
	card a2a.AgentCard,
	task a2a.Task,
) (gopact.ToolResult, error) {
	var out gopact.ToolResult
	for taskEvent, err := range agent.Stream(ctx, task) {
		taskEvent = taskEvent.WithDefaults(task)
		if err != nil && taskEvent.Err == nil {
			taskEvent.Err = err
		}
		mergeTaskEvent(&out, taskEvent, card)
		if err != nil {
			return finalizeResult(out, card), err
		}
		if taskEvent.Err != nil {
			return finalizeResult(out, card), taskEvent.Err
		}
	}
	return finalizeResult(out, card), nil
}

func invokeSend(ctx context.Context, agent a2a.Agent, card a2a.AgentCard, task a2a.Task) (gopact.ToolResult, error) {
	result, err := agent.Send(ctx, task)
	out := toolResultFromTaskResult(result, card)
	event := a2a.TaskEvent{
		TaskID:    result.TaskID,
		IDs:       task.IDs,
		Status:    a2a.TaskStatusCompleted,
		Result:    &result,
		Artifacts: result.Artifacts,
	}
	if event.TaskID == "" {
		event.TaskID = task.ID
	}
	if err != nil {
		event.Status = a2a.TaskStatusFailed
		event.Err = err
	}
	out.Events = append(out.Events, taskEventRuntimeEvent(event.WithDefaults(task), card))
	return finalizeResult(out, card), err
}

func mergeTaskEvent(out *gopact.ToolResult, event a2a.TaskEvent, card a2a.AgentCard) {
	out.Events = append(out.Events, taskEventRuntimeEvent(event, card))
	out.Artifacts = append(out.Artifacts, copyArtifactRefs(event.Artifacts)...)
	if event.Message != "" {
		out.Content = event.Message
	}
	if event.Result != nil {
		mergeTaskResult(out, *event.Result, card)
	}
}

func mergeTaskResult(out *gopact.ToolResult, result a2a.Result, card a2a.AgentCard) {
	if result.Output != "" {
		out.Content = result.Output
	}
	out.Artifacts = append(out.Artifacts, copyArtifactRefs(result.Artifacts)...)
	out.Metadata = mergeMetadata(out.Metadata, result.Metadata, card)
}

func toolResultFromTaskResult(result a2a.Result, card a2a.AgentCard) gopact.ToolResult {
	return gopact.ToolResult{
		Content:   result.Output,
		Artifacts: copyArtifactRefs(result.Artifacts),
		Metadata:  mergeMetadata(nil, result.Metadata, card),
	}
}

func finalizeResult(result gopact.ToolResult, card a2a.AgentCard) gopact.ToolResult {
	result.Artifacts = dedupeArtifactRefs(result.Artifacts)
	result.Metadata = mergeMetadata(result.Metadata, nil, card)
	return result
}

func taskEventRuntimeEvent(event a2a.TaskEvent, card a2a.AgentCard) gopact.Event {
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
