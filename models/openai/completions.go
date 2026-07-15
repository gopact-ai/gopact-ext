package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"net/http"
	"strings"
)

const maxCompletionLogprobs = 5

// CompletionRequest configures the legacy text Completions API. Prompt may be a
// string, string array, token array, or token-array list.
type CompletionRequest struct {
	Model            string         `json:"model,omitempty"`
	Prompt           any            `json:"prompt"`
	BestOf           int            `json:"best_of,omitempty"`
	Echo             bool           `json:"echo,omitempty"`
	FrequencyPenalty *float64       `json:"frequency_penalty,omitempty"`
	LogitBias        map[string]int `json:"logit_bias,omitempty"`
	Logprobs         *int           `json:"logprobs,omitempty"`
	MaxTokens        int            `json:"max_tokens,omitempty"`
	N                int            `json:"n,omitempty"`
	PresencePenalty  *float64       `json:"presence_penalty,omitempty"`
	Seed             *int64         `json:"seed,omitempty"`
	Stop             any            `json:"stop,omitempty"`
	Suffix           string         `json:"suffix,omitempty"`
	Temperature      *float64       `json:"temperature,omitempty"`
	TopP             *float64       `json:"top_p,omitempty"`
	User             string         `json:"user,omitempty"`
}

// Completion is a legacy text completion result or stream chunk.
type Completion struct {
	ID      string             `json:"id"`
	Object  string             `json:"object"`
	Created int64              `json:"created"`
	Model   string             `json:"model"`
	Choices []CompletionChoice `json:"choices"`
	Usage   CompletionUsage    `json:"usage"`
}

// CompletionChoice is one generated text alternative.
type CompletionChoice struct {
	Text         string          `json:"text"`
	Index        int             `json:"index"`
	FinishReason string          `json:"finish_reason"`
	Logprobs     json.RawMessage `json:"logprobs"`
}

// CompletionUsage reports legacy completion token usage.
type CompletionUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// CompletionEvent is one legacy completion SSE event.
type CompletionEvent struct {
	Completion Completion
	Raw        json.RawMessage
}

// Complete calls the non-streaming legacy Completions API.
func (c *Model) Complete(ctx context.Context, request CompletionRequest) (Completion, error) {
	if err := c.prepareCompletionRequest(&request); err != nil {
		return Completion{}, err
	}
	var response Completion
	err := c.requestJSON(ctx, http.MethodPost, "/completions", request, &response)
	return response, err
}

// StreamCompletion calls the streaming legacy Completions API.
func (c *Model) StreamCompletion(ctx context.Context, request CompletionRequest) iter.Seq2[CompletionEvent, error] {
	return func(yield func(CompletionEvent, error) bool) {
		if err := c.prepareCompletionRequest(&request); err != nil {
			yield(CompletionEvent{}, err)
			return
		}
		payload := struct {
			CompletionRequest
			Stream bool `json:"stream"`
		}{CompletionRequest: request, Stream: true}
		encoded, err := json.Marshal(payload)
		if err != nil {
			yield(CompletionEvent{}, fmt.Errorf("openai: encode completion stream request: %w", err))
			return
		}
		for event, err := range c.streamJSON(ctx, "/completions", encoded, "application/json") {
			if err != nil {
				yield(CompletionEvent{}, err)
				return
			}
			var completion Completion
			if err := json.Unmarshal(event.Data, &completion); err != nil {
				yield(CompletionEvent{}, fmt.Errorf("openai: decode completion stream: %w", err))
				return
			}
			if !yield(CompletionEvent{Completion: completion, Raw: event.Data}, nil) {
				return
			}
		}
	}
}

func (c *Model) prepareCompletionRequest(request *CompletionRequest) error {
	if c == nil {
		return errors.New("openai: model is nil")
	}
	if request.Model == "" {
		request.Model = c.defaultRequest.Model
	}
	if strings.TrimSpace(request.Model) == "" {
		return errors.New("openai: completion model is required")
	}
	if request.Prompt == nil {
		return errors.New("openai: completion prompt is required")
	}
	if prompt, ok := request.Prompt.(string); ok && prompt == "" {
		return errors.New("openai: completion prompt is required")
	}
	if request.BestOf < 0 || request.MaxTokens < 0 || request.N < 0 {
		return errors.New("openai: completion counts must not be negative")
	}
	if request.Logprobs != nil && (*request.Logprobs < 0 || *request.Logprobs > maxCompletionLogprobs) {
		return errors.New("openai: completion logprobs must be between 0 and 5")
	}
	return nil
}
