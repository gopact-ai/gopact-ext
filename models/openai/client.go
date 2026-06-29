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
	API        API
	HTTPClient *http.Client
	Models     []provider.ModelInfo
}

// API selects the OpenAI API surface used by Generate.
type API string

const (
	APIChatCompletions API = "chat_completions"
	APIResponses       API = "responses"
)

// Client is a minimal OpenAI-compatible provider adapter.
type Client struct {
	provider   string
	baseURL    string
	apiKey     string
	api        API
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
	api := opts.API
	if api == "" {
		api = APIChatCompletions
	}
	if api != APIChatCompletions && api != APIResponses {
		return nil, fmt.Errorf("openai: unsupported api %q", api)
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
		api:        api,
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

	path, body, err := c.marshalRequest(req)
	if err != nil {
		return gopact.ModelResponse{}, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
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

	return c.decodeResponse(respBody, req.Model)
}

func (c *Client) marshalRequest(req gopact.ModelRequest) (string, []byte, error) {
	if c.api == APIResponses {
		body, err := json.Marshal(responsesRequest{
			Model: req.Model,
			Input: convertResponsesInput(req.Messages),
			Tools: convertResponsesTools(req.Tools),
		})
		return "/responses", body, wrapMarshalErr(err)
	}

	body, err := json.Marshal(chatCompletionRequest{
		Model:    req.Model,
		Messages: convertMessages(req.Messages),
		Tools:    convertTools(req.Tools),
	})
	return "/chat/completions", body, wrapMarshalErr(err)
}

func wrapMarshalErr(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("openai: marshal request: %w", err)
}

func (c *Client) decodeResponse(body []byte, model string) (gopact.ModelResponse, error) {
	if c.api == APIResponses {
		return decodeResponsesResponse(body)
	}
	return decodeChatCompletionResponse(body, c.provider, model)
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

type responsesRequest struct {
	Model string               `json:"model"`
	Input []responsesInputItem `json:"input"`
	Tools []responsesTool      `json:"tools,omitempty"`
}

type responsesInputItem struct {
	Type      string `json:"type"`
	Role      string `json:"role,omitempty"`
	Content   string `json:"content,omitempty"`
	CallID    string `json:"call_id,omitempty"`
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
	Output    string `json:"output,omitempty"`
}

type responsesTool struct {
	Type        string            `json:"type"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Parameters  gopact.JSONSchema `json:"parameters,omitempty"`
}

type responsesResponse struct {
	OutputText string                `json:"output_text"`
	Output     []responsesOutputItem `json:"output"`
	Usage      struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
}

type responsesOutputItem struct {
	Type      string                   `json:"type"`
	Role      string                   `json:"role,omitempty"`
	Content   []responsesOutputContent `json:"content,omitempty"`
	CallID    string                   `json:"call_id,omitempty"`
	ID        string                   `json:"id,omitempty"`
	Name      string                   `json:"name,omitempty"`
	Arguments string                   `json:"arguments,omitempty"`
}

type responsesOutputContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func decodeChatCompletionResponse(body []byte, providerName, model string) (gopact.ModelResponse, error) {
	var decoded chatCompletionResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return gopact.ModelResponse{}, fmt.Errorf("openai: decode response: %w", err)
	}
	if len(decoded.Choices) == 0 {
		return gopact.ModelResponse{}, provider.NewError(provider.ErrorUnavailable, errors.New("empty choices"), provider.WithErrorProvider(providerName), provider.WithErrorModel(model))
	}

	return gopact.ModelResponse{
		Message: decoded.Choices[0].Message.toGopact(),
		Usage: gopact.Usage{
			InputTokens:  decoded.Usage.PromptTokens,
			OutputTokens: decoded.Usage.CompletionTokens,
			TotalTokens:  decoded.Usage.TotalTokens,
		},
	}, nil
}

func decodeResponsesResponse(body []byte) (gopact.ModelResponse, error) {
	var decoded responsesResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return gopact.ModelResponse{}, fmt.Errorf("openai: decode response: %w", err)
	}

	message := gopact.Message{Role: gopact.RoleAssistant, Content: decoded.responsesText()}
	for _, item := range decoded.Output {
		if item.Type != "function_call" {
			continue
		}
		id := item.CallID
		if id == "" {
			id = item.ID
		}
		message.ToolCalls = append(message.ToolCalls, gopact.ToolCall{
			ID:        id,
			Name:      item.Name,
			Arguments: []byte(item.Arguments),
		})
	}

	return gopact.ModelResponse{
		Message: message,
		Usage: gopact.Usage{
			InputTokens:  decoded.Usage.InputTokens,
			OutputTokens: decoded.Usage.OutputTokens,
			TotalTokens:  decoded.Usage.TotalTokens,
		},
	}, nil
}

func (r responsesResponse) responsesText() string {
	if r.OutputText != "" {
		return r.OutputText
	}
	var b strings.Builder
	for _, item := range r.Output {
		for _, content := range item.Content {
			if content.Type == "output_text" || content.Type == "text" {
				b.WriteString(content.Text)
			}
		}
	}
	return b.String()
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

func convertResponsesTools(tools []gopact.ToolSpec) []responsesTool {
	converted := make([]responsesTool, 0, len(tools))
	for _, tool := range tools {
		converted = append(converted, responsesTool{
			Type:        "function",
			Name:        tool.Name,
			Description: tool.Description,
			Parameters:  tool.InputSchema,
		})
	}
	return converted
}

func convertResponsesInput(messages []gopact.Message) []responsesInputItem {
	converted := make([]responsesInputItem, 0, len(messages))
	for _, message := range messages {
		if message.Role == gopact.RoleTool {
			converted = append(converted, responsesInputItem{
				Type:   "function_call_output",
				CallID: message.ToolCallID,
				Output: message.Text(),
			})
			continue
		}
		if text := message.Text(); text != "" || len(message.ToolCalls) == 0 {
			converted = append(converted, responsesInputItem{
				Type:    "message",
				Role:    string(message.Role),
				Content: text,
			})
		}
		for _, toolCall := range message.ToolCalls {
			converted = append(converted, responsesInputItem{
				Type:      "function_call",
				CallID:    toolCall.ID,
				ID:        toolCall.ID,
				Name:      toolCall.Name,
				Arguments: string(toolCall.Arguments),
			})
		}
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
