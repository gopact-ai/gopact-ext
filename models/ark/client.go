// Package ark adapts Volcengine Ark chat completion APIs.
package ark

import (
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact/provider"
	"github.com/volcengine/volcengine-go-sdk/service/arkruntime"
	arkmodel "github.com/volcengine/volcengine-go-sdk/service/arkruntime/model"
)

const (
	DefaultProvider = "ark"
	DefaultBaseURL  = "https://ark.cn-beijing.volces.com/api/v3"
	DefaultRegion   = "cn-beijing"

	apiV3Path        = "/api/v3"
	versionedAPIPath = "/api/v"
)

// Options configures an Ark provider client.
type Options struct {
	Provider   string
	BaseURL    string
	Region     string
	APIKey     string
	AccessKey  string
	SecretKey  string
	HTTPClient *http.Client
	Models     []provider.ModelInfo
}

// Client is a minimal Ark provider adapter.
type Client struct {
	provider string
	client   *arkruntime.Client
	models   []provider.ModelInfo
}

// New creates an Ark provider client.
func New(opts Options) (*Client, error) {
	providerName := opts.Provider
	if providerName == "" {
		providerName = DefaultProvider
	}
	baseURL, err := normalizeBaseURL(opts.BaseURL)
	if err != nil {
		return nil, err
	}
	region := strings.TrimSpace(opts.Region)
	if region == "" {
		region = DefaultRegion
	}

	config := []arkruntime.ConfigOption{
		arkruntime.WithBaseUrl(baseURL),
		arkruntime.WithRegion(region),
	}
	if opts.HTTPClient != nil {
		config = append(config, arkruntime.WithHTTPClient(opts.HTTPClient))
	}

	var sdkClient *arkruntime.Client
	switch {
	case opts.APIKey != "":
		sdkClient = arkruntime.NewClientWithApiKey(opts.APIKey, config...)
	case opts.AccessKey != "" && opts.SecretKey != "":
		sdkClient = arkruntime.NewClientWithAkSk(opts.AccessKey, opts.SecretKey, config...)
	default:
		return nil, errors.New("ark: set api key or both access key and secret key")
	}

	return &Client{
		provider: providerName,
		client:   sdkClient,
		models:   append([]provider.ModelInfo(nil), opts.Models...),
	}, nil
}

func normalizeBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = DefaultBaseURL
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("ark: parse base url: %w", err)
	}
	baseURL := strings.TrimRight(parsed.String(), "/")
	if strings.HasSuffix(baseURL, apiV3Path) || strings.Contains(baseURL, versionedAPIPath) {
		return baseURL, nil
	}
	return baseURL + apiV3Path, nil
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
		return gopact.ModelResponse{}, errors.New("ark: client is nil")
	}
	if err := ctx.Err(); err != nil {
		return gopact.ModelResponse{}, err
	}
	req = gopact.ApplyModelRequestOptions(req)

	resp, err := c.client.CreateChatCompletion(ctx, newChatCompletionRequest(req))
	if err != nil {
		return gopact.ModelResponse{}, c.wrapError(err, req.Model)
	}
	if len(resp.Choices) == 0 {
		return gopact.ModelResponse{}, provider.NewError(provider.ErrorUnavailable, errors.New("empty choices"), provider.WithErrorProvider(c.provider), provider.WithErrorModel(req.Model))
	}

	return gopact.ModelResponse{
		Message: toGopactMessage(resp.Choices[0].Message),
		Usage: gopact.Usage{
			InputTokens:  resp.Usage.PromptTokens,
			OutputTokens: resp.Usage.CompletionTokens,
			TotalTokens:  resp.Usage.TotalTokens,
		},
	}, nil
}

func (c *Client) Stream(ctx context.Context, req gopact.ModelRequest) iter.Seq2[gopact.Event, error] {
	return func(yield func(gopact.Event, error) bool) {
		if c == nil {
			err := errors.New("ark: client is nil")
			yield(gopact.Event{Type: gopact.EventModelProviderAttemptFailed, IDs: req.IDs, Err: err}, err)
			return
		}
		if err := ctx.Err(); err != nil {
			yield(gopact.Event{Type: gopact.EventModelProviderAttemptFailed, IDs: req.IDs, Err: err}, err)
			return
		}
		req = gopact.ApplyModelRequestOptions(req)
		arkReq := newChatCompletionRequest(req)
		arkReq.StreamOptions = &arkmodel.StreamOptions{IncludeUsage: true}

		stream, err := c.client.CreateChatCompletionStream(ctx, arkReq)
		if err != nil {
			err = c.wrapError(err, req.Model)
			yield(gopact.Event{Type: gopact.EventModelProviderAttemptFailed, IDs: req.IDs, Err: err}, err)
			return
		}
		defer func() {
			_ = stream.Close()
		}()

		message := gopact.Message{Role: gopact.RoleAssistant}
		var content strings.Builder
		toolCalls := map[int]*streamToolCall{}
		var usage *gopact.Usage
		for {
			chunk, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				err = c.wrapError(err, req.Model)
				yield(gopact.Event{Type: gopact.EventModelProviderAttemptFailed, IDs: req.IDs, Err: err}, err)
				return
			}
			if chunk.Usage != nil {
				u := toUsage(*chunk.Usage)
				usage = &u
			}
			for _, choice := range chunk.Choices {
				if choice == nil {
					continue
				}
				if choice.Delta.Role != "" {
					message.Role = gopact.Role(choice.Delta.Role)
				}
				content.WriteString(choice.Delta.Content)
				applyStreamToolCallDeltas(toolCalls, choice.Delta.ToolCalls)
			}
		}
		message.Content = content.String()
		message.ToolCalls = streamToolCalls(toolCalls)
		yield(gopact.Event{Type: gopact.EventModelMessage, IDs: req.IDs, Message: &message, Usage: usage}, nil)
	}
}

func newChatCompletionRequest(req gopact.ModelRequest) arkmodel.CreateChatCompletionRequest {
	arkReq := arkmodel.CreateChatCompletionRequest{
		Model:    req.Model,
		Messages: convertMessages(req.Messages),
		Tools:    convertTools(req.Tools),
	}
	applyModelRequestOptions(&arkReq, req)
	return arkReq
}

func convertMessages(messages []gopact.Message) []*arkmodel.ChatCompletionMessage {
	converted := make([]*arkmodel.ChatCompletionMessage, 0, len(messages))
	for _, message := range messages {
		converted = append(converted, &arkmodel.ChatCompletionMessage{
			Role:       string(message.Role),
			Content:    arkContent(message.Text()),
			Name:       stringPtr(message.Name),
			ToolCallID: message.ToolCallID,
			ToolCalls:  convertToolCalls(message.ToolCalls),
		})
	}
	return converted
}

func arkContent(text string) *arkmodel.ChatCompletionMessageContent {
	if text == "" {
		return nil
	}
	return &arkmodel.ChatCompletionMessageContent{StringValue: &text}
}

func stringPtr(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func convertTools(tools []gopact.ToolSpec) []*arkmodel.Tool {
	converted := make([]*arkmodel.Tool, 0, len(tools))
	for _, tool := range tools {
		converted = append(converted, &arkmodel.Tool{
			Type: arkmodel.ToolTypeFunction,
			Function: &arkmodel.FunctionDefinition{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.InputSchema,
			},
		})
	}
	return converted
}

func applyModelRequestOptions(out *arkmodel.CreateChatCompletionRequest, req gopact.ModelRequest) {
	if req.Budget.MaxOutputTokens > 0 {
		out.MaxTokens = &req.Budget.MaxOutputTokens
	}
	if req.Temperature != nil {
		temperature := float32(*req.Temperature)
		out.Temperature = &temperature
	}
	if req.TopP != nil {
		topP := float32(*req.TopP)
		out.TopP = &topP
	}
	if req.ThinkingType != "" {
		out.Thinking = &arkmodel.Thinking{Type: arkmodel.ThinkingType(req.ThinkingType)}
	}
	if req.ReasoningEffort != "" {
		effort := arkmodel.ReasoningEffort(req.ReasoningEffort)
		out.ReasoningEffort = &effort
	}
	if len(req.ResponseSchema) > 0 {
		out.ResponseFormat = &arkmodel.ResponseFormat{
			Type: arkmodel.ResponseFormatJSONSchema,
			JSONSchema: &arkmodel.ResponseFormatJSONSchemaJSONSchemaParam{
				Name:   "gopact_response",
				Schema: req.ResponseSchema,
				Strict: true,
			},
		}
	}
}

type streamToolCall struct {
	ID        string
	Name      string
	Arguments strings.Builder
}

func applyStreamToolCallDeltas(calls map[int]*streamToolCall, deltas []*arkmodel.ToolCall) {
	for _, delta := range deltas {
		if delta == nil {
			continue
		}
		index := len(calls)
		if delta.Index != nil {
			index = *delta.Index
		}
		call := calls[index]
		if call == nil {
			call = &streamToolCall{}
			calls[index] = call
		}
		if delta.ID != "" {
			call.ID = delta.ID
		}
		if delta.Function.Name != "" {
			call.Name = delta.Function.Name
		}
		call.Arguments.WriteString(delta.Function.Arguments)
	}
}

func streamToolCalls(calls map[int]*streamToolCall) []gopact.ToolCall {
	if len(calls) == 0 {
		return nil
	}
	indexes := make([]int, 0, len(calls))
	for index := range calls {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	out := make([]gopact.ToolCall, 0, len(indexes))
	for _, index := range indexes {
		call := calls[index]
		if call == nil || (call.ID == "" && call.Name == "" && call.Arguments.Len() == 0) {
			continue
		}
		out = append(out, gopact.ToolCall{
			ID:        call.ID,
			Name:      call.Name,
			Arguments: []byte(call.Arguments.String()),
		})
	}
	return out
}

func convertToolCalls(toolCalls []gopact.ToolCall) []*arkmodel.ToolCall {
	converted := make([]*arkmodel.ToolCall, 0, len(toolCalls))
	for _, toolCall := range toolCalls {
		converted = append(converted, &arkmodel.ToolCall{
			ID:   toolCall.ID,
			Type: arkmodel.ToolTypeFunction,
			Function: arkmodel.FunctionCall{
				Name:      toolCall.Name,
				Arguments: string(toolCall.Arguments),
			},
		})
	}
	return converted
}

func toGopactMessage(message arkmodel.ChatCompletionMessage) gopact.Message {
	return gopact.Message{
		Role:       gopact.Role(message.Role),
		Content:    arkText(message.Content),
		Name:       derefString(message.Name),
		ToolCallID: message.ToolCallID,
		ToolCalls:  toGopactToolCalls(message.ToolCalls),
	}
}

func arkText(content *arkmodel.ChatCompletionMessageContent) string {
	if content == nil {
		return ""
	}
	if content.StringValue != nil {
		return *content.StringValue
	}
	var b strings.Builder
	for _, part := range content.ListValue {
		if part != nil && part.Type == arkmodel.ChatCompletionMessageContentPartTypeText {
			b.WriteString(part.Text)
		}
	}
	return b.String()
}

func toUsage(usage arkmodel.Usage) gopact.Usage {
	return gopact.Usage{
		InputTokens:  usage.PromptTokens,
		OutputTokens: usage.CompletionTokens,
		TotalTokens:  usage.TotalTokens,
	}
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func toGopactToolCalls(toolCalls []*arkmodel.ToolCall) []gopact.ToolCall {
	converted := make([]gopact.ToolCall, 0, len(toolCalls))
	for _, toolCall := range toolCalls {
		if toolCall == nil {
			continue
		}
		converted = append(converted, gopact.ToolCall{
			ID:        toolCall.ID,
			Name:      toolCall.Function.Name,
			Arguments: []byte(toolCall.Function.Arguments),
		})
	}
	return converted
}

func (c *Client) wrapError(err error, modelName string) error {
	class := provider.ErrorUnknown
	text := strings.ToLower(err.Error())
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		class = provider.ErrorTimeout
	case strings.Contains(text, "401"), strings.Contains(text, "403"), strings.Contains(text, "unauthorized"):
		class = provider.ErrorUnauthorized
	case strings.Contains(text, "429"), strings.Contains(text, "rate"):
		class = provider.ErrorRateLimited
	case strings.Contains(text, "400"), strings.Contains(text, "bad request"):
		class = provider.ErrorInvalidRequest
	case errors.Is(err, io.EOF), strings.Contains(text, "timeout"), strings.Contains(text, "unavailable"):
		class = provider.ErrorUnavailable
	}
	return provider.NewError(class, err, provider.WithErrorProvider(c.provider), provider.WithErrorModel(modelName))
}
