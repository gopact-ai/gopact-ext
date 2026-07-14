// Package fake provides a deterministic offline model adapter.
package fake

import (
	"context"
	"errors"
	"fmt"

	"github.com/gopact-ai/gopact"
)

// Model is a deterministic offline model adapter for tests and examples.
type Model struct {
	defaultRequest gopact.ModelRequest
	response       string
}

// Option configures a Model.
type Option func(*Model)

// WithDefaultRequest sets the request template used by NewRequest.
func WithDefaultRequest(req gopact.ModelRequest) Option {
	return func(m *Model) {
		m.defaultRequest = req
	}
}

// WithResponse sets the assistant text returned by Invoke.
func WithResponse(text string) Option {
	return func(m *Model) {
		m.response = text
	}
}

// New creates a deterministic offline model.
func New(opts ...Option) *Model {
	m := &Model{response: "ok"}
	for _, opt := range opts {
		if opt != nil {
			opt(m)
		}
	}
	return m
}

// NewRequest returns a request copied from the model default template.
func (m *Model) NewRequest(messages ...gopact.Message) gopact.ModelRequest {
	req := m.defaultRequest
	req.Messages = append([]gopact.Message(nil), messages...)
	req.Tools = append([]gopact.ToolSpec(nil), m.defaultRequest.Tools...)
	req.Modalities = append([]gopact.Modality(nil), m.defaultRequest.Modalities...)
	req.Stop = append([]string(nil), m.defaultRequest.Stop...)
	req.OutputProtocols = append([]gopact.OutputProtocol(nil), m.defaultRequest.OutputProtocols...)
	if m.defaultRequest.Metadata != nil {
		req.Metadata = map[string]string{}
		for k, v := range m.defaultRequest.Metadata {
			req.Metadata[k] = v
		}
	}
	if m.defaultRequest.Extensions != nil {
		req.Extensions = map[string]any{}
		for k, v := range m.defaultRequest.Extensions {
			req.Extensions[k] = v
		}
	}
	return req
}

// Invoke returns the configured response and emits a message delta event.
func (m *Model) Invoke(ctx context.Context, req gopact.ModelRequest, opts ...gopact.ModelCallOption) (gopact.ModelResponse, error) {
	if ctx == nil {
		ctx = context.TODO()
	}
	if err := ctx.Err(); err != nil {
		return gopact.ModelResponse{}, err
	}
	if len(req.Messages) == 0 {
		return gopact.ModelResponse{}, errors.New("fake: request has no messages")
	}
	for key := range req.Extensions {
		return gopact.ModelResponse{}, fmt.Errorf("fake: unknown request extension %q", key)
	}
	cfg := gopact.ResolveModelCallOptions(opts...)
	for key := range cfg.Extensions {
		return gopact.ModelResponse{}, fmt.Errorf("fake: unknown call extension %q", key)
	}
	for _, sink := range cfg.ModelEventSinks {
		err := sink.EmitModelEvent(ctx, gopact.ModelEvent{
			Type:    gopact.ModelEventMessageDelta,
			Source:  "fake",
			Summary: m.response,
		})
		if err != nil {
			return gopact.ModelResponse{}, err
		}
	}
	return gopact.ModelResponse{
		Message: gopact.Message{
			Role:  gopact.MessageRoleAssistant,
			Parts: []gopact.MessagePart{{Type: gopact.MessagePartTypeText, Text: m.response}},
		},
		Intent:       gopact.FinalIntent{},
		FinishReason: "stop",
	}, nil
}
