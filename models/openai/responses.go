package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const maxResponseInputItems = 100

// ResponseRequest configures a call to the OpenAI Responses API. Provider
// unions such as input, tools, reasoning, and text stay as JSON-shaped values so
// new OpenAI variants do not require this adapter to mirror the full schema.
type ResponseRequest struct {
	Model                string            `json:"model,omitempty"`
	Input                any               `json:"input,omitempty"`
	Instructions         string            `json:"instructions,omitempty"`
	Background           *bool             `json:"background,omitempty"`
	Conversation         any               `json:"conversation,omitempty"`
	Include              []string          `json:"include,omitempty"`
	MaxOutputTokens      int               `json:"max_output_tokens,omitempty"`
	MaxToolCalls         int               `json:"max_tool_calls,omitempty"`
	Metadata             map[string]string `json:"metadata,omitempty"`
	ParallelToolCalls    *bool             `json:"parallel_tool_calls,omitempty"`
	PreviousResponseID   string            `json:"previous_response_id,omitempty"`
	PromptCacheKey       string            `json:"prompt_cache_key,omitempty"`
	PromptCacheRetention string            `json:"prompt_cache_retention,omitempty"`
	SafetyIdentifier     string            `json:"safety_identifier,omitempty"`
	ServiceTier          string            `json:"service_tier,omitempty"`
	Store                *bool             `json:"store,omitempty"`
	Temperature          *float64          `json:"temperature,omitempty"`
	TopLogprobs          int               `json:"top_logprobs,omitempty"`
	TopP                 *float64          `json:"top_p,omitempty"`
	Truncation           string            `json:"truncation,omitempty"`
	User                 string            `json:"user,omitempty"`
	ContextManagement    []json.RawMessage `json:"context_management,omitempty"`
	Moderation           json.RawMessage   `json:"moderation,omitempty"`
	Prompt               json.RawMessage   `json:"prompt,omitempty"`
	PromptCacheOptions   json.RawMessage   `json:"prompt_cache_options,omitempty"`
	Reasoning            json.RawMessage   `json:"reasoning,omitempty"`
	StreamOptions        json.RawMessage   `json:"stream_options,omitempty"`
	Text                 json.RawMessage   `json:"text,omitempty"`
	ToolChoice           any               `json:"tool_choice,omitempty"`
	Tools                []json.RawMessage `json:"tools,omitempty"`
}

// Response is a Responses API result. Output items are retained as raw JSON
// because OpenAI's discriminated output union evolves independently.
type Response struct {
	ID                string            `json:"id"`
	Object            string            `json:"object"`
	CreatedAt         float64           `json:"created_at"`
	CompletedAt       float64           `json:"completed_at"`
	Status            string            `json:"status"`
	Model             string            `json:"model"`
	Output            []json.RawMessage `json:"output"`
	Error             *ResponseError    `json:"error"`
	IncompleteDetails json.RawMessage   `json:"incomplete_details"`
	Metadata          map[string]string `json:"metadata"`
	Usage             ResponseUsage     `json:"usage"`
}

// ResponseError describes a failed Responses API generation.
type ResponseError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ResponseUsage reports token accounting for a response or compaction.
type ResponseUsage struct {
	InputTokens        int `json:"input_tokens"`
	OutputTokens       int `json:"output_tokens"`
	TotalTokens        int `json:"total_tokens"`
	InputTokensDetails struct {
		CachedTokens     int `json:"cached_tokens"`
		CacheWriteTokens int `json:"cache_write_tokens"`
	} `json:"input_tokens_details"`
	OutputTokensDetails struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"output_tokens_details"`
}

// ResponseEvent is one raw Responses API SSE event.
type ResponseEvent struct {
	Type string
	Data json.RawMessage
}

// ResponseQuery selects optional data when retrieving a response.
type ResponseQuery struct {
	Include            []string
	IncludeObfuscation *bool
	StartingAfter      int64
}

// ResponseInputItemQuery configures response-input pagination.
type ResponseInputItemQuery struct {
	After   string
	Include []string
	Limit   int
	Order   string
}

// ResponseInputItemList is one page of items used to create a response.
type ResponseInputItemList struct {
	Object  string            `json:"object"`
	Data    []json.RawMessage `json:"data"`
	FirstID string            `json:"first_id"`
	LastID  string            `json:"last_id"`
	HasMore bool              `json:"has_more"`
}

// ResponseInputTokenRequest configures preflight token counting.
type ResponseInputTokenRequest struct {
	Model              string            `json:"model,omitempty"`
	Input              any               `json:"input,omitempty"`
	Instructions       string            `json:"instructions,omitempty"`
	Conversation       any               `json:"conversation,omitempty"`
	ParallelToolCalls  *bool             `json:"parallel_tool_calls,omitempty"`
	PreviousResponseID string            `json:"previous_response_id,omitempty"`
	Personality        string            `json:"personality,omitempty"`
	Reasoning          json.RawMessage   `json:"reasoning,omitempty"`
	Text               json.RawMessage   `json:"text,omitempty"`
	ToolChoice         any               `json:"tool_choice,omitempty"`
	Tools              []json.RawMessage `json:"tools,omitempty"`
	Truncation         string            `json:"truncation,omitempty"`
}

// ResponseInputTokenCount is a Responses API preflight token count.
type ResponseInputTokenCount struct {
	Object      string `json:"object"`
	InputTokens int    `json:"input_tokens"`
}

// ResponseCompactRequest configures server-side conversation compaction.
type ResponseCompactRequest struct {
	Model                string          `json:"model,omitempty"`
	Input                any             `json:"input,omitempty"`
	Instructions         string          `json:"instructions,omitempty"`
	PreviousResponseID   string          `json:"previous_response_id,omitempty"`
	PromptCacheKey       string          `json:"prompt_cache_key,omitempty"`
	PromptCacheOptions   json.RawMessage `json:"prompt_cache_options,omitempty"`
	PromptCacheRetention string          `json:"prompt_cache_retention,omitempty"`
	ServiceTier          string          `json:"service_tier,omitempty"`
}

// CompactedResponse is a server-compacted response context.
type CompactedResponse struct {
	ID        string            `json:"id"`
	Object    string            `json:"object"`
	CreatedAt int64             `json:"created_at"`
	Output    []json.RawMessage `json:"output"`
	Usage     ResponseUsage     `json:"usage"`
}

// CreateResponse creates one non-streaming Responses API result.
func (c *Model) CreateResponse(ctx context.Context, request ResponseRequest) (Response, error) {
	if err := c.prepareResponseRequest(&request); err != nil {
		return Response{}, err
	}
	var response Response
	err := c.requestJSON(ctx, http.MethodPost, "/responses", request, &response)
	return response, err
}

// StreamResponse creates a Responses API SSE stream.
func (c *Model) StreamResponse(ctx context.Context, request ResponseRequest) iter.Seq2[ResponseEvent, error] {
	return func(yield func(ResponseEvent, error) bool) {
		if err := c.prepareResponseRequest(&request); err != nil {
			yield(ResponseEvent{}, err)
			return
		}
		payload := struct {
			ResponseRequest
			Stream bool `json:"stream"`
		}{ResponseRequest: request, Stream: true}
		encoded, err := json.Marshal(payload)
		if err != nil {
			yield(ResponseEvent{}, fmt.Errorf("openai: encode response stream request: %w", err))
			return
		}
		for event, err := range c.streamJSON(ctx, "/responses", encoded, "application/json") {
			if !yield(ResponseEvent(event), err) {
				return
			}
		}
	}
}

// GetResponse retrieves a stored or background response.
func (c *Model) GetResponse(ctx context.Context, responseID string, query ResponseQuery) (Response, error) {
	if strings.TrimSpace(responseID) == "" {
		return Response{}, errors.New("openai: response id is required")
	}
	values, err := responseQueryValues(query)
	if err != nil {
		return Response{}, err
	}
	var response Response
	err = c.requestJSON(ctx, http.MethodGet, withQuery("/responses/"+url.PathEscape(responseID), values), nil, &response)
	return response, err
}

// StreamStoredResponse resumes SSE delivery for a stored background response.
func (c *Model) StreamStoredResponse(ctx context.Context, responseID string, query ResponseQuery) iter.Seq2[ResponseEvent, error] {
	return func(yield func(ResponseEvent, error) bool) {
		if strings.TrimSpace(responseID) == "" {
			yield(ResponseEvent{}, errors.New("openai: response id is required"))
			return
		}
		values, err := responseQueryValues(query)
		if err != nil {
			yield(ResponseEvent{}, err)
			return
		}
		body := []byte(`{"stream":true}`)
		path := withQuery("/responses/"+url.PathEscape(responseID), values)
		call := runtimeCall{method: http.MethodGet, path: path, body: body, contentType: "application/json"}
		for event, err := range c.streamEncodedJSON(ctx, call) {
			if !yield(ResponseEvent(event), err) {
				return
			}
		}
	}
}

// CancelResponse cancels a background response.
func (c *Model) CancelResponse(ctx context.Context, responseID string) (Response, error) {
	if strings.TrimSpace(responseID) == "" {
		return Response{}, errors.New("openai: response id is required")
	}
	var response Response
	err := c.requestJSON(ctx, http.MethodPost, "/responses/"+url.PathEscape(responseID)+"/cancel", nil, &response)
	return response, err
}

// DeleteResponse deletes a stored response.
func (c *Model) DeleteResponse(ctx context.Context, responseID string) error {
	if strings.TrimSpace(responseID) == "" {
		return errors.New("openai: response id is required")
	}
	return c.requestJSON(ctx, http.MethodDelete, "/responses/"+url.PathEscape(responseID), nil, nil)
}

// ResponseInputItems returns one page of a response's input items.
func (c *Model) ResponseInputItems(ctx context.Context, responseID string, query ResponseInputItemQuery) (ResponseInputItemList, error) {
	if strings.TrimSpace(responseID) == "" {
		return ResponseInputItemList{}, errors.New("openai: response id is required")
	}
	if query.Limit < 0 || query.Limit > maxResponseInputItems {
		return ResponseInputItemList{}, errors.New("openai: response input item limit must be between 1 and 100")
	}
	if query.Order != "" && query.Order != "asc" && query.Order != "desc" {
		return ResponseInputItemList{}, errors.New("openai: response input item order must be asc or desc")
	}
	values := url.Values{}
	if query.After != "" {
		values.Set("after", query.After)
	}
	if query.Limit > 0 {
		values.Set("limit", strconv.Itoa(query.Limit))
	}
	if query.Order != "" {
		values.Set("order", query.Order)
	}
	addArrayQuery(values, "include[]", query.Include)
	var response ResponseInputItemList
	path := "/responses/" + url.PathEscape(responseID) + "/input_items"
	err := c.requestJSON(ctx, http.MethodGet, withQuery(path, values), nil, &response)
	return response, err
}

// CountResponseInputTokens counts input tokens without creating a response.
func (c *Model) CountResponseInputTokens(ctx context.Context, request ResponseInputTokenRequest) (ResponseInputTokenCount, error) {
	if c == nil {
		return ResponseInputTokenCount{}, errors.New("openai: model is nil")
	}
	if request.Model == "" {
		request.Model = c.defaultRequest.Model
	}
	if strings.TrimSpace(request.Model) == "" {
		return ResponseInputTokenCount{}, errors.New("openai: response model is required")
	}
	var response ResponseInputTokenCount
	err := c.requestJSON(ctx, http.MethodPost, "/responses/input_tokens", request, &response)
	return response, err
}

// CompactResponse compacts a long-running response context.
func (c *Model) CompactResponse(ctx context.Context, request ResponseCompactRequest) (CompactedResponse, error) {
	if c == nil {
		return CompactedResponse{}, errors.New("openai: model is nil")
	}
	if request.Model == "" {
		request.Model = c.defaultRequest.Model
	}
	if strings.TrimSpace(request.Model) == "" {
		return CompactedResponse{}, errors.New("openai: response model is required")
	}
	var response CompactedResponse
	err := c.requestJSON(ctx, http.MethodPost, "/responses/compact", request, &response)
	return response, err
}

func (c *Model) prepareResponseRequest(request *ResponseRequest) error {
	if c == nil {
		return errors.New("openai: model is nil")
	}
	if request.Model == "" {
		request.Model = c.defaultRequest.Model
	}
	if strings.TrimSpace(request.Model) == "" {
		return errors.New("openai: response model is required")
	}
	if request.MaxOutputTokens < 0 || request.MaxToolCalls < 0 || request.TopLogprobs < 0 {
		return errors.New("openai: response token and tool limits must not be negative")
	}
	return nil
}

func addArrayQuery(values url.Values, key string, items []string) {
	for _, item := range items {
		values.Add(key, item)
	}
}

func responseQueryValues(query ResponseQuery) (url.Values, error) {
	if query.StartingAfter < 0 {
		return nil, errors.New("openai: response starting_after must not be negative")
	}
	values := url.Values{}
	addArrayQuery(values, "include[]", query.Include)
	if query.IncludeObfuscation != nil {
		values.Set("include_obfuscation", strconv.FormatBool(*query.IncludeObfuscation))
	}
	if query.StartingAfter > 0 {
		values.Set("starting_after", strconv.FormatInt(query.StartingAfter, decimalRadix))
	}
	return values, nil
}

func withQuery(path string, values url.Values) string {
	if len(values) == 0 {
		return path
	}
	return path + "?" + values.Encode()
}
