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
		return nil, errors.New("ark: set APIKey or both AccessKey and SecretKey")
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
	if strings.HasSuffix(baseURL, "/api/v3") || strings.Contains(baseURL, "/api/v") {
		return baseURL, nil
	}
	return baseURL + "/api/v3", nil
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

	resp, err := c.client.CreateChatCompletion(ctx, arkmodel.CreateChatCompletionRequest{
		Model:    req.Model,
		Messages: convertMessages(req.Messages),
		Tools:    convertTools(req.Tools),
	})
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
		response, err := c.Generate(ctx, req)
		if err != nil {
			yield(gopact.Event{Type: gopact.EventModelProviderAttemptFailed, IDs: req.IDs, Err: err}, err)
			return
		}
		yield(gopact.Event{Type: gopact.EventModelMessage, IDs: req.IDs, Message: &response.Message, Usage: &response.Usage}, nil)
	}
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
