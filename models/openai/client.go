// Package openai adapts OpenAI-shaped chat completion APIs.
package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"net/http"
	"net/url"
	"strings"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact/provider"
)

// Options configures an OpenAI-compatible provider client.
type Options struct {
	Provider   string
	BaseURL    string
	APIKey     string
	HTTPClient *http.Client
	Models     []provider.ModelInfo
}

// Client is a minimal OpenAI-compatible provider adapter.
type Client struct {
	provider   string
	baseURL    string
	apiKey     string
	httpClient *http.Client
	models     []provider.ModelInfo
}

// New creates an OpenAI-compatible provider client.
func New(opts Options) (*Client, error) {
	if opts.Provider == "" {
		return nil, errors.New("openai: provider is required")
	}
	if opts.BaseURL == "" {
		return nil, errors.New("openai: base url is required")
	}
	if opts.APIKey == "" {
		return nil, errors.New("openai: api key is required")
	}
	parsed, err := url.Parse(opts.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("openai: parse base url: %w", err)
	}
	client := opts.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	return &Client{
		provider:   opts.Provider,
		baseURL:    strings.TrimRight(parsed.String(), "/"),
		apiKey:     opts.APIKey,
		httpClient: client,
		models:     append([]provider.ModelInfo(nil), opts.Models...),
	}, nil
}

func (c *Client) Name() string {
	if c == nil {
		return ""
	}
	return c.provider
}

func (c *Client) Models(ctx context.Context) ([]provider.ModelInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c == nil {
		return nil, nil
	}
	return append([]provider.ModelInfo(nil), c.models...), nil
}

func (c *Client) Generate(ctx context.Context, req gopact.ModelRequest) (gopact.ModelResponse, error) {
	if c == nil {
		return gopact.ModelResponse{}, errors.New("openai: client is nil")
	}
	if err := ctx.Err(); err != nil {
		return gopact.ModelResponse{}, err
	}

	payload := chatCompletionRequest{
		Model:    req.Model,
		Messages: convertMessages(req.Messages),
		Tools:    convertTools(req.Tools),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return gopact.ModelResponse{}, fmt.Errorf("openai: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return gopact.ModelResponse{}, fmt.Errorf("openai: create request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return gopact.ModelResponse{}, provider.NewError(provider.ErrorUnavailable, err, provider.WithErrorProvider(c.provider), provider.WithErrorModel(req.Model))
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return gopact.ModelResponse{}, fmt.Errorf("openai: read response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return gopact.ModelResponse{}, provider.NewError(classForStatus(resp.StatusCode), errors.New(strings.TrimSpace(string(respBody))), provider.WithErrorProvider(c.provider), provider.WithErrorModel(req.Model))
	}

	var decoded chatCompletionResponse
	if err := json.Unmarshal(respBody, &decoded); err != nil {
		return gopact.ModelResponse{}, fmt.Errorf("openai: decode response: %w", err)
	}
	if len(decoded.Choices) == 0 {
		return gopact.ModelResponse{}, provider.NewError(provider.ErrorUnavailable, errors.New("empty choices"), provider.WithErrorProvider(c.provider), provider.WithErrorModel(req.Model))
	}

	message := decoded.Choices[0].Message.toGopact()
	return gopact.ModelResponse{
		Message: message,
		Usage: gopact.Usage{
			InputTokens:  decoded.Usage.PromptTokens,
			OutputTokens: decoded.Usage.CompletionTokens,
			TotalTokens:  decoded.Usage.TotalTokens,
		},
	}, nil
}

func (c *Client) Stream(ctx context.Context, req gopact.ModelRequest) iter.Seq2[gopact.Event, error] {
	return func(yield func(gopact.Event, error) bool) {
		response, err := c.Generate(ctx, req)
		if err != nil {
			yield(gopact.Event{Type: gopact.EventModelProviderAttemptFailed, IDs: req.IDs, Err: err}, err)
			return
		}
		yield(gopact.Event{Type: gopact.EventModelMessage, IDs: req.IDs, Message: &response.Message, Usage: &response.Usage}, nil)
	}
}

type chatCompletionRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Tools    []chatTool    `json:"tools,omitempty"`
}

type chatMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content,omitempty"`
	Name       string         `json:"name,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	ToolCalls  []chatToolCall `json:"tool_calls,omitempty"`
}

type chatTool struct {
	Type     string       `json:"type"`
	Function chatFunction `json:"function"`
}

type chatFunction struct {
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Parameters  gopact.JSONSchema `json:"parameters,omitempty"`
}

type chatToolCall struct {
	ID       string               `json:"id,omitempty"`
	Type     string               `json:"type"`
	Function chatToolCallFunction `json:"function"`
}

type chatToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type chatCompletionResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

func convertMessages(messages []gopact.Message) []chatMessage {
	converted := make([]chatMessage, 0, len(messages))
	for _, message := range messages {
		converted = append(converted, chatMessage{
			Role:       string(message.Role),
			Content:    message.Text(),
			Name:       message.Name,
			ToolCallID: message.ToolCallID,
			ToolCalls:  convertToolCalls(message.ToolCalls),
		})
	}
	return converted
}

func convertTools(tools []gopact.ToolSpec) []chatTool {
	converted := make([]chatTool, 0, len(tools))
	for _, tool := range tools {
		converted = append(converted, chatTool{
			Type: "function",
			Function: chatFunction{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.InputSchema,
			},
		})
	}
	return converted
}

func convertToolCalls(toolCalls []gopact.ToolCall) []chatToolCall {
	converted := make([]chatToolCall, 0, len(toolCalls))
	for _, toolCall := range toolCalls {
		converted = append(converted, chatToolCall{
			ID:   toolCall.ID,
			Type: "function",
			Function: chatToolCallFunction{
				Name:      toolCall.Name,
				Arguments: string(toolCall.Arguments),
			},
		})
	}
	return converted
}

func (m chatMessage) toGopact() gopact.Message {
	return gopact.Message{
		Role:       gopact.Role(m.Role),
		Content:    m.Content,
		Name:       m.Name,
		ToolCallID: m.ToolCallID,
		ToolCalls:  m.toGopactToolCalls(),
	}
}

func (m chatMessage) toGopactToolCalls() []gopact.ToolCall {
	converted := make([]gopact.ToolCall, 0, len(m.ToolCalls))
	for _, toolCall := range m.ToolCalls {
		converted = append(converted, gopact.ToolCall{
			ID:        toolCall.ID,
			Name:      toolCall.Function.Name,
			Arguments: []byte(toolCall.Function.Arguments),
		})
	}
	return converted
}

func classForStatus(status int) provider.ErrorClass {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return provider.ErrorUnauthorized
	case http.StatusTooManyRequests:
		return provider.ErrorRateLimited
	case http.StatusBadRequest:
		return provider.ErrorInvalidRequest
	case http.StatusServiceUnavailable, http.StatusBadGateway, http.StatusGatewayTimeout:
		return provider.ErrorUnavailable
	default:
		if status >= 500 {
			return provider.ErrorUnavailable
		}
		return provider.ErrorUnknown
	}
}
