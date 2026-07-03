// Package humanreview provides reusable approval-gate graph nodes.
package humanreview

import (
	"context"
	"errors"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact/graph"
)

const (
	defaultReason     = "human review required"
	defaultRequiredBy = "humanreview"
)

var (
	// ErrRequestMapperRequired reports a missing state-to-review mapper.
	ErrRequestMapperRequired = errors.New("humanreview: request mapper is required")
	// ErrInterruptIDRequired reports a review request without a stable interrupt ID.
	ErrInterruptIDRequired = errors.New("humanreview: interrupt id is required")
)

// Request describes one human approval boundary.
type Request struct {
	ID           string
	Reason       string
	Prompt       gopact.Message
	RequiredBy   string
	ResumeSchema gopact.JSONSchema
	Metadata     map[string]any
}

// RequestMapper converts graph state into a human approval request.
type RequestMapper[S any] func(ctx context.Context, state S) (Request, error)

// New returns a graph node that interrupts execution until an approval resume
// request is imported by graph step export or checkpoint resume.
func New[S any](mapRequest RequestMapper[S]) (graph.NodeFunc[S], error) {
	if mapRequest == nil {
		return nil, ErrRequestMapperRequired
	}
	return func(ctx context.Context, state S) (S, error) {
		if ctx == nil {
			ctx = context.TODO()
		}
		if err := ctx.Err(); err != nil {
			return state, err
		}
		request, err := mapRequest(ctx, state)
		if err != nil {
			return state, err
		}
		record, err := interruptRecord(request)
		if err != nil {
			return state, err
		}
		return state, gopact.Interrupt(record)
	}, nil
}

func interruptRecord(request Request) (gopact.InterruptRecord, error) {
	if request.ID == "" {
		return gopact.InterruptRecord{}, ErrInterruptIDRequired
	}
	reason := request.Reason
	if reason == "" {
		reason = defaultReason
	}
	prompt := request.Prompt
	if prompt.Role == "" && prompt.Content == "" && len(prompt.Parts) == 0 {
		prompt = gopact.AssistantMessage(reason)
	}
	requiredBy := request.RequiredBy
	if requiredBy == "" {
		requiredBy = defaultRequiredBy
	}
	schema := copyJSONSchema(request.ResumeSchema)
	if len(schema) == 0 {
		schema = DefaultApprovalResumeSchema()
	}
	metadata := copyAnyMap(request.Metadata)
	if metadata == nil {
		metadata = make(map[string]any, 1)
	}
	metadata["template"] = "humanreview"
	return gopact.InterruptRecord{
		ID:           request.ID,
		Type:         gopact.InterruptApproval,
		Reason:       reason,
		Prompt:       prompt,
		RequiredBy:   requiredBy,
		ResumeSchema: schema,
		Metadata:     metadata,
	}, nil
}

// DefaultApprovalResumeSchema returns the standard approval payload contract.
func DefaultApprovalResumeSchema() gopact.JSONSchema {
	return gopact.JSONSchema{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"approved"},
		"properties": map[string]any{
			"approved": map[string]any{
				"type":  "boolean",
				"const": true,
			},
		},
	}
}

func copyJSONSchema(in gopact.JSONSchema) gopact.JSONSchema {
	if len(in) == 0 {
		return nil
	}
	out := make(gopact.JSONSchema, len(in))
	for key, value := range in {
		out[key] = copyJSONSchemaValue(value)
	}
	return out
}

func copyJSONSchemaValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[key] = copyJSONSchemaValue(item)
		}
		return out
	case gopact.JSONSchema:
		return copyJSONSchema(typed)
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = copyJSONSchemaValue(item)
		}
		return out
	case []string:
		return append([]string(nil), typed...)
	default:
		return value
	}
}

func copyAnyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = copyJSONSchemaValue(value)
	}
	return out
}
