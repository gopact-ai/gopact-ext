// Package openai adapts OpenAI-shaped chat completion APIs.
package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
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
)

// Options configures an OpenAI-compatible provider client.
type Options struct {
	Provider        string
	BaseURL         string
	APIKey          string
	API             API
	MaxOutputTokens int
	Temperature     *float64
	TopP            *float64
	ThinkingType    string
	ReasoningEffort string
	HTTPClient      *http.Client
	Models          []provider.ModelInfo
}

// Option updates Options for NewClient.
type Option func(*Options)

// API selects the OpenAI API surface used by Generate.
type API string

const (
	APIChatCompletions API = "chat_completions"
	APIResponses       API = "responses"
)

const (
	DefaultProvider = "openai"
	ProviderOpenAI  = DefaultProvider
	ProviderArk     = "ark"

	CapabilityToolCalling      = provider.CapabilityToolCalling
	CapabilityStreaming        = provider.CapabilityStreaming
	CapabilityJSONSchema       = provider.CapabilityJSONSchema
	CapabilityVision           = provider.CapabilityVision
	CapabilityReasoning        = provider.CapabilityReasoning
	CapabilityStructuredOutput = provider.CapabilityStructuredOutput
)

const (
	endpointChatCompletions = "/chat/completions"
	endpointResponses       = "/responses"

	headerAccept        = "Accept"
	headerAuthorization = "Authorization"
	headerContentType   = "Content-Type"

	authBearerPrefix       = "Bearer "
	contentTypeEventStream = "text/event-stream"
	contentTypeJSON        = "application/json"

	maxResponseBodyBytes = 4 << 20
	sseBufferBytes       = 64 * 1024
	sseDataPrefix        = "data:"
	sseDonePayload       = "[DONE]"
)

const (
	toolTypeFunction = "function"

	responsesItemFunctionCall       = "function_call"
	responsesItemFunctionCallOutput = "function_call_output"
	responsesItemMessage            = "message"
	responsesItemReasoning          = "reasoning"

	responsesContentInputImage  = "input_image"
	responsesContentInputText   = "input_text"
	responsesContentOutputText  = "output_text"
	responsesContentSummaryText = "summary_text"
	responsesContentText        = "text"

	responsesEventCompleted                  = "response.completed"
	responsesEventFunctionCallArgumentsDelta = "response.function_call_arguments.delta"
	responsesEventOutputItemAdded            = "response.output_item.added"
	responsesEventOutputItemDone             = "response.output_item.done"
	responsesEventOutputTextDelta            = "response.output_text.delta"
	responsesEventReasoningSummaryTextDelta  = "response.reasoning_summary_text.delta"
	responsesEventReasoningTextDelta         = "response.reasoning_text.delta"
)

// Client is a minimal OpenAI-compatible provider adapter.
type Client struct {
	provider   string
	baseURL    string
	apiKey     string
	api        API
	params     requestParams
	httpClient *http.Client
	models     []provider.ModelInfo
}

type requestParams struct {
	maxOutputTokens int
	temperature     *float64
	topP            *float64
	thinkingType    string
	reasoningEffort string
}

// NewClient creates an OpenAI-compatible provider client with feature options.
func NewClient(providerName, baseURL, apiKey string, opts ...Option) (*Client, error) {
	cfg := Options{
		Provider: providerName,
		BaseURL:  baseURL,
		APIKey:   apiKey,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	return New(cfg)
}

func WithAPI(api API) Option {
	return func(opts *Options) {
		opts.API = api
	}
}

func WithChatCompletionsAPI() Option {
	return WithAPI(APIChatCompletions)
}

func WithResponsesAPI() Option {
	return WithAPI(APIResponses)
}

func WithMaxOutputTokens(tokens int) Option {
	return func(opts *Options) {
		opts.MaxOutputTokens = tokens
	}
}

func WithTemperature(temperature float64) Option {
	return func(opts *Options) {
		opts.Temperature = &temperature
	}
}

func WithTopP(topP float64) Option {
	return func(opts *Options) {
		opts.TopP = &topP
	}
}

func WithThinkingType(thinkingType string) Option {
	return func(opts *Options) {
		opts.ThinkingType = thinkingType
	}
}

func WithReasoningEffort(effort string) Option {
	return func(opts *Options) {
		opts.ReasoningEffort = effort
	}
}

func WithHTTPClient(client *http.Client) Option {
	return func(opts *Options) {
		opts.HTTPClient = client
	}
}

func WithModels(models ...provider.ModelInfo) Option {
	return func(opts *Options) {
		opts.Models = append([]provider.ModelInfo(nil), models...)
	}
}

func WithModel(name string, capabilities ...provider.Capability) Option {
	return func(opts *Options) {
		opts.Models = []provider.ModelInfo{ProviderModel(opts.Provider, name, capabilities...)}
	}
}

func Model(name string, capabilities ...provider.Capability) provider.ModelInfo {
	return ProviderModel(DefaultProvider, name, capabilities...)
}

func ProviderModel(providerName, name string, capabilities ...provider.Capability) provider.ModelInfo {
	return provider.ModelInfo{
		Name:         name,
		Provider:     providerName,
		Capabilities: append([]provider.Capability(nil), capabilities...),
	}
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
		provider: opts.Provider,
		baseURL:  strings.TrimRight(parsed.String(), "/"),
		apiKey:   opts.APIKey,
		api:      api,
		params: requestParams{
			maxOutputTokens: opts.MaxOutputTokens,
			temperature:     opts.Temperature,
			topP:            opts.TopP,
			thinkingType:    opts.ThinkingType,
			reasoningEffort: opts.ReasoningEffort,
		},
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

	path, body, err := c.marshalRequest(req, false)
	if err != nil {
		return gopact.ModelResponse{}, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return gopact.ModelResponse{}, fmt.Errorf("openai: create request: %w", err)
	}
	httpReq.Header.Set(headerAuthorization, authBearerPrefix+c.apiKey)
	httpReq.Header.Set(headerContentType, contentTypeJSON)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return gopact.ModelResponse{}, provider.NewError(provider.ErrorUnavailable, err, provider.WithErrorProvider(c.provider), provider.WithErrorModel(req.Model))
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes))
	if err != nil {
		return gopact.ModelResponse{}, fmt.Errorf("openai: read response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return gopact.ModelResponse{}, provider.NewError(classForStatus(resp.StatusCode), errors.New(strings.TrimSpace(string(respBody))), provider.WithErrorProvider(c.provider), provider.WithErrorModel(req.Model))
	}

	return c.decodeResponse(respBody, req.Model)
}

func (c *Client) marshalRequest(req gopact.ModelRequest, stream bool) (string, []byte, error) {
	if c.api == APIResponses {
		body, err := json.Marshal(responsesRequest{
			Model:           req.Model,
			Input:           convertResponsesInput(req.Messages),
			Tools:           convertResponsesTools(req.Tools),
			MaxOutputTokens: c.maxOutputTokens(req),
			Temperature:     c.params.temperature,
			TopP:            c.params.topP,
			Stream:          boolPtr(stream),
			Thinking:        thinkingConfig(c.params.thinkingType),
			Reasoning:       reasoningConfig(c.params.reasoningEffort),
		})
		return endpointResponses, body, wrapMarshalErr(err)
	}

	body, err := json.Marshal(chatCompletionRequest{
		Model:           req.Model,
		Messages:        convertMessages(req.Messages),
		Tools:           convertTools(req.Tools),
		MaxTokens:       c.maxOutputTokens(req),
		Temperature:     c.params.temperature,
		TopP:            c.params.topP,
		Stream:          boolPtr(stream),
		StreamOptions:   streamOptions(stream),
		Thinking:        thinkingConfig(c.params.thinkingType),
		ReasoningEffort: c.params.reasoningEffort,
	})
	return endpointChatCompletions, body, wrapMarshalErr(err)
}

func (c *Client) maxOutputTokens(req gopact.ModelRequest) *int {
	if req.Budget.MaxOutputTokens > 0 {
		return &req.Budget.MaxOutputTokens
	}
	if c.params.maxOutputTokens > 0 {
		return &c.params.maxOutputTokens
	}
	return nil
}

func boolPtr(v bool) *bool {
	if !v {
		return nil
	}
	return &v
}

func thinkingConfig(thinkingType string) *thinking {
	if thinkingType == "" {
		return nil
	}
	return &thinking{Type: thinkingType}
}

func reasoningConfig(effort string) *reasoning {
	if effort == "" {
		return nil
	}
	return &reasoning{Effort: effort}
}

func streamOptions(stream bool) *chatStreamOptions {
	if !stream {
		return nil
	}
	return &chatStreamOptions{IncludeUsage: true}
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
		if c == nil {
			err := errors.New("openai: client is nil")
			yield(gopact.Event{Type: gopact.EventModelProviderAttemptFailed, IDs: req.IDs, Err: err}, err)
			return
		}
		if err := ctx.Err(); err != nil {
			yield(gopact.Event{Type: gopact.EventModelProviderAttemptFailed, IDs: req.IDs, Err: err}, err)
			return
		}

		path, body, err := c.marshalRequest(req, true)
		if err != nil {
			yield(gopact.Event{Type: gopact.EventModelProviderAttemptFailed, IDs: req.IDs, Err: err}, err)
			return
		}

		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
		if err != nil {
			err = fmt.Errorf("openai: create request: %w", err)
			yield(gopact.Event{Type: gopact.EventModelProviderAttemptFailed, IDs: req.IDs, Err: err}, err)
			return
		}
		httpReq.Header.Set(headerAuthorization, authBearerPrefix+c.apiKey)
		httpReq.Header.Set(headerContentType, contentTypeJSON)
		httpReq.Header.Set(headerAccept, contentTypeEventStream)

		resp, err := c.httpClient.Do(httpReq)
		if err != nil {
			err = provider.NewError(provider.ErrorUnavailable, err, provider.WithErrorProvider(c.provider), provider.WithErrorModel(req.Model))
			yield(gopact.Event{Type: gopact.EventModelProviderAttemptFailed, IDs: req.IDs, Err: err}, err)
			return
		}
		defer func() {
			_ = resp.Body.Close()
		}()
		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes))
			if readErr != nil {
				err = fmt.Errorf("openai: read response: %w", readErr)
			} else {
				err = provider.NewError(classForStatus(resp.StatusCode), errors.New(strings.TrimSpace(string(respBody))), provider.WithErrorProvider(c.provider), provider.WithErrorModel(req.Model))
			}
			yield(gopact.Event{Type: gopact.EventModelProviderAttemptFailed, IDs: req.IDs, Err: err}, err)
			return
		}

		var streamErr error
		if c.api == APIResponses {
			streamErr = c.streamResponses(resp.Body, req, yield)
		} else {
			streamErr = c.streamChatCompletions(resp.Body, req, yield)
		}
		if streamErr != nil {
			yield(gopact.Event{Type: gopact.EventModelProviderAttemptFailed, IDs: req.IDs, Err: streamErr}, streamErr)
		}
	}
}

type chatCompletionRequest struct {
	Model           string             `json:"model"`
	Messages        []chatMessage      `json:"messages"`
	Tools           []chatTool         `json:"tools,omitempty"`
	MaxTokens       *int               `json:"max_tokens,omitempty"`
	Temperature     *float64           `json:"temperature,omitempty"`
	TopP            *float64           `json:"top_p,omitempty"`
	Stream          *bool              `json:"stream,omitempty"`
	StreamOptions   *chatStreamOptions `json:"stream_options,omitempty"`
	Thinking        *thinking          `json:"thinking,omitempty"`
	ReasoningEffort string             `json:"reasoning_effort,omitempty"`
}

type chatMessage struct {
	Role             string         `json:"role"`
	Content          string         `json:"content,omitempty"`
	ReasoningContent string         `json:"reasoning_content,omitempty"`
	Name             string         `json:"name,omitempty"`
	ToolCallID       string         `json:"tool_call_id,omitempty"`
	ToolCalls        []chatToolCall `json:"tool_calls,omitempty"`
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

type chatStreamOptions struct {
	IncludeUsage bool `json:"include_usage,omitempty"`
}

type thinking struct {
	Type string `json:"type"`
}

type reasoning struct {
	Effort string `json:"effort,omitempty"`
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

type chatCompletionStreamResponse struct {
	Choices []struct {
		Delta        chatStreamDelta `json:"delta"`
		FinishReason string          `json:"finish_reason"`
	} `json:"choices"`
	Usage usage `json:"usage"`
}

type chatStreamDelta struct {
	Role             string              `json:"role"`
	Content          string              `json:"content"`
	ReasoningContent string              `json:"reasoning_content"`
	ToolCalls        []chatToolCallDelta `json:"tool_calls"`
}

type chatToolCallDelta struct {
	Index    int                  `json:"index"`
	ID       string               `json:"id"`
	Type     string               `json:"type"`
	Function chatToolCallFunction `json:"function"`
}

type usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	InputTokens      int `json:"input_tokens"`
	OutputTokens     int `json:"output_tokens"`
}

type responsesRequest struct {
	Model           string               `json:"model"`
	Input           []responsesInputItem `json:"input"`
	Tools           []responsesTool      `json:"tools,omitempty"`
	MaxOutputTokens *int                 `json:"max_output_tokens,omitempty"`
	Temperature     *float64             `json:"temperature,omitempty"`
	TopP            *float64             `json:"top_p,omitempty"`
	Stream          *bool                `json:"stream,omitempty"`
	Thinking        *thinking            `json:"thinking,omitempty"`
	Reasoning       *reasoning           `json:"reasoning,omitempty"`
}

type responsesInputItem struct {
	Type      string                  `json:"type"`
	Role      string                  `json:"role,omitempty"`
	Content   []responsesInputContent `json:"content,omitempty"`
	CallID    string                  `json:"call_id,omitempty"`
	ID        string                  `json:"id,omitempty"`
	Name      string                  `json:"name,omitempty"`
	Arguments string                  `json:"arguments,omitempty"`
	Output    string                  `json:"output,omitempty"`
}

type responsesInputContent struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
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
	Summary   []responsesOutputContent `json:"summary,omitempty"`
	CallID    string                   `json:"call_id,omitempty"`
	ID        string                   `json:"id,omitempty"`
	Name      string                   `json:"name,omitempty"`
	Arguments string                   `json:"arguments,omitempty"`
}

type responsesOutputContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type responsesStreamEvent struct {
	Type        string              `json:"type"`
	Delta       string              `json:"delta"`
	OutputIndex int                 `json:"output_index"`
	Item        responsesOutputItem `json:"item"`
	Response    responsesResponse   `json:"response"`
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

	message := messageWithParts(gopact.RoleAssistant, decoded.responsesText(), decoded.responsesReasoning())
	for _, item := range decoded.Output {
		if item.Type != responsesItemFunctionCall {
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
			if content.Type == responsesContentOutputText || content.Type == responsesContentText {
				b.WriteString(content.Text)
			}
		}
	}
	return b.String()
}

func (r responsesResponse) responsesReasoning() string {
	var b strings.Builder
	for _, item := range r.Output {
		if item.Type != responsesItemReasoning {
			continue
		}
		for _, content := range item.Summary {
			if content.Type == responsesContentSummaryText || content.Type == responsesContentText {
				b.WriteString(content.Text)
			}
		}
	}
	return b.String()
}

func messageWithParts(role gopact.Role, text, reasoning string) gopact.Message {
	message := gopact.Message{Role: role, Content: text}
	if reasoning == "" {
		return message
	}
	message.Parts = []gopact.ContentPart{gopact.ReasoningPart(reasoning)}
	if text != "" {
		message.Parts = append(message.Parts, gopact.TextPart(text))
	}
	return message
}

func convertMessages(messages []gopact.Message) []chatMessage {
	converted := make([]chatMessage, 0, len(messages))
	for _, message := range messages {
		converted = append(converted, chatMessage{
			Role:             string(message.Role),
			Content:          message.Text(),
			ReasoningContent: reasoningText(message),
			Name:             message.Name,
			ToolCallID:       message.ToolCallID,
			ToolCalls:        convertToolCalls(message.ToolCalls),
		})
	}
	return converted
}

func reasoningText(message gopact.Message) string {
	var b strings.Builder
	for _, part := range message.Parts {
		if part.Type == gopact.ContentPartReasoning {
			b.WriteString(part.Text)
		}
	}
	return b.String()
}

func convertTools(tools []gopact.ToolSpec) []chatTool {
	converted := make([]chatTool, 0, len(tools))
	for _, tool := range tools {
		converted = append(converted, chatTool{
			Type: toolTypeFunction,
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
			Type:        toolTypeFunction,
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
				Type:   responsesItemFunctionCallOutput,
				CallID: message.ToolCallID,
				Output: message.Text(),
			})
			continue
		}
		if content := convertResponsesContent(message); len(content) > 0 || len(message.ToolCalls) == 0 {
			converted = append(converted, responsesInputItem{
				Type:    responsesItemMessage,
				Role:    string(message.Role),
				Content: content,
			})
		}
		for _, toolCall := range message.ToolCalls {
			converted = append(converted, responsesInputItem{
				Type:      responsesItemFunctionCall,
				CallID:    toolCall.ID,
				ID:        toolCall.ID,
				Name:      toolCall.Name,
				Arguments: string(toolCall.Arguments),
			})
		}
	}
	return converted
}

func convertResponsesContent(message gopact.Message) []responsesInputContent {
	if len(message.Parts) == 0 {
		if message.Content == "" {
			return nil
		}
		return []responsesInputContent{{Type: responsesContentInputText, Text: message.Content}}
	}

	converted := make([]responsesInputContent, 0, len(message.Parts))
	for _, part := range message.Parts {
		switch part.Type {
		case gopact.ContentPartText:
			converted = append(converted, responsesInputContent{Type: responsesContentInputText, Text: part.Text})
		case gopact.ContentPartImage:
			converted = append(converted, responsesInputContent{Type: responsesContentInputImage, ImageURL: part.URI})
		}
	}
	return converted
}

func convertToolCalls(toolCalls []gopact.ToolCall) []chatToolCall {
	converted := make([]chatToolCall, 0, len(toolCalls))
	for _, toolCall := range toolCalls {
		converted = append(converted, chatToolCall{
			ID:   toolCall.ID,
			Type: toolTypeFunction,
			Function: chatToolCallFunction{
				Name:      toolCall.Name,
				Arguments: string(toolCall.Arguments),
			},
		})
	}
	return converted
}

func (m chatMessage) toGopact() gopact.Message {
	message := messageWithParts(gopact.Role(m.Role), m.Content, m.ReasoningContent)
	message.Name = m.Name
	message.ToolCallID = m.ToolCallID
	message.ToolCalls = m.toGopactToolCalls()
	return message
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

func (c *Client) streamChatCompletions(body io.Reader, req gopact.ModelRequest, yield func(gopact.Event, error) bool) error {
	tools := map[int]*toolCallAccumulator{}
	var lastUsage gopact.Usage
	var keepGoing = true

	err := scanSSE(body, func(data []byte) bool {
		var chunk chatCompletionStreamResponse
		if err := json.Unmarshal(data, &chunk); err != nil {
			return yield(gopact.Event{Type: gopact.EventModelProviderAttemptFailed, IDs: req.IDs, Err: err}, err)
		}
		if usage := toUsage(chunk.Usage); usage.TotalTokens > 0 {
			lastUsage = usage
		}
		for _, choice := range chunk.Choices {
			delta := choice.Delta
			if delta.ReasoningContent != "" {
				keepGoing = yieldMessage(req, messageWithParts(gopact.RoleAssistant, "", delta.ReasoningContent), nil, yield)
				if !keepGoing {
					return false
				}
			}
			if delta.Content != "" {
				keepGoing = yieldMessage(req, gopact.Message{Role: gopact.RoleAssistant, Content: delta.Content}, nil, yield)
				if !keepGoing {
					return false
				}
			}
			mergeChatToolDeltas(tools, delta.ToolCalls)
		}
		return true
	})
	if err != nil || !keepGoing {
		return err
	}
	if calls := toolCallsFromAccumulators(tools); len(calls) > 0 {
		message := gopact.Message{Role: gopact.RoleAssistant, ToolCalls: calls}
		if !yieldMessage(req, message, usagePtr(lastUsage), yield) {
			return nil
		}
		return nil
	}
	if usage := usagePtr(lastUsage); usage != nil {
		yield(gopact.Event{Type: gopact.EventModelMessage, IDs: req.IDs, Usage: usage}, nil)
	}
	return nil
}

func (c *Client) streamResponses(body io.Reader, req gopact.ModelRequest, yield func(gopact.Event, error) bool) error {
	tools := map[int]*toolCallAccumulator{}
	var lastUsage gopact.Usage
	var emittedText bool
	var emittedReasoning bool
	var keepGoing = true

	err := scanSSE(body, func(data []byte) bool {
		var event responsesStreamEvent
		if err := json.Unmarshal(data, &event); err != nil {
			return yield(gopact.Event{Type: gopact.EventModelProviderAttemptFailed, IDs: req.IDs, Err: err}, err)
		}
		switch event.Type {
		case responsesEventOutputTextDelta:
			emittedText = true
			keepGoing = yieldMessage(req, gopact.Message{Role: gopact.RoleAssistant, Content: event.Delta}, nil, yield)
		case responsesEventReasoningSummaryTextDelta, responsesEventReasoningTextDelta:
			emittedReasoning = true
			keepGoing = yieldMessage(req, messageWithParts(gopact.RoleAssistant, "", event.Delta), nil, yield)
		case responsesEventFunctionCallArgumentsDelta:
			accumulatorFor(tools, event.OutputIndex).Arguments.WriteString(event.Delta)
		case responsesEventOutputItemAdded, responsesEventOutputItemDone:
			mergeResponseToolItem(tools, event.OutputIndex, event.Item)
		case responsesEventCompleted:
			lastUsage = usageFromResponses(event.Response)
			mergeResponseToolItems(tools, event.Response.Output)
			if reasoning := event.Response.responsesReasoning(); reasoning != "" && !emittedReasoning {
				emittedReasoning = true
				keepGoing = yieldMessage(req, messageWithParts(gopact.RoleAssistant, "", reasoning), nil, yield)
			}
			if text := event.Response.responsesText(); text != "" && !emittedText && keepGoing {
				emittedText = true
				keepGoing = yieldMessage(req, gopact.Message{Role: gopact.RoleAssistant, Content: text}, nil, yield)
			}
		}
		return keepGoing
	})
	if err != nil || !keepGoing {
		return err
	}
	if calls := toolCallsFromAccumulators(tools); len(calls) > 0 {
		message := gopact.Message{Role: gopact.RoleAssistant, ToolCalls: calls}
		if !yieldMessage(req, message, usagePtr(lastUsage), yield) {
			return nil
		}
		return nil
	}
	if usage := usagePtr(lastUsage); usage != nil {
		yield(gopact.Event{Type: gopact.EventModelMessage, IDs: req.IDs, Usage: usage}, nil)
	}
	return nil
}

func scanSSE(body io.Reader, handle func([]byte) bool) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, sseBufferBytes), maxResponseBodyBytes)
	var data strings.Builder
	flush := func() bool {
		if data.Len() == 0 {
			return true
		}
		payload := strings.TrimSpace(data.String())
		data.Reset()
		if payload == "" || payload == sseDonePayload {
			return true
		}
		return handle([]byte(payload))
	}
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" {
			if !flush() {
				return nil
			}
			continue
		}
		if strings.HasPrefix(line, sseDataPrefix) {
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, sseDataPrefix)))
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	flush()
	return nil
}

type toolCallAccumulator struct {
	ID        string
	Name      string
	Arguments strings.Builder
}

func mergeChatToolDeltas(tools map[int]*toolCallAccumulator, deltas []chatToolCallDelta) {
	for _, delta := range deltas {
		tool := accumulatorFor(tools, delta.Index)
		if delta.ID != "" {
			tool.ID = delta.ID
		}
		if delta.Function.Name != "" {
			tool.Name = delta.Function.Name
		}
		if delta.Function.Arguments != "" {
			tool.Arguments.WriteString(delta.Function.Arguments)
		}
	}
}

func mergeResponseToolItems(tools map[int]*toolCallAccumulator, items []responsesOutputItem) {
	for i, item := range items {
		mergeResponseToolItem(tools, i, item)
	}
}

func mergeResponseToolItem(tools map[int]*toolCallAccumulator, index int, item responsesOutputItem) {
	if item.Type != responsesItemFunctionCall {
		return
	}
	tool := accumulatorFor(tools, index)
	if item.CallID != "" {
		tool.ID = item.CallID
	} else if item.ID != "" {
		tool.ID = item.ID
	}
	if item.Name != "" {
		tool.Name = item.Name
	}
	if item.Arguments != "" {
		tool.Arguments.Reset()
		tool.Arguments.WriteString(item.Arguments)
	}
}

func accumulatorFor(tools map[int]*toolCallAccumulator, index int) *toolCallAccumulator {
	tool := tools[index]
	if tool == nil {
		tool = &toolCallAccumulator{}
		tools[index] = tool
	}
	return tool
}

func toolCallsFromAccumulators(tools map[int]*toolCallAccumulator) []gopact.ToolCall {
	if len(tools) == 0 {
		return nil
	}
	indexes := make([]int, 0, len(tools))
	for index := range tools {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)

	calls := make([]gopact.ToolCall, 0, len(tools))
	for _, index := range indexes {
		tool := tools[index]
		if tool == nil || tool.Name == "" {
			continue
		}
		calls = append(calls, gopact.ToolCall{
			ID:        tool.ID,
			Name:      tool.Name,
			Arguments: []byte(tool.Arguments.String()),
		})
	}
	return calls
}

func yieldMessage(req gopact.ModelRequest, message gopact.Message, usage *gopact.Usage, yield func(gopact.Event, error) bool) bool {
	return yield(gopact.Event{Type: gopact.EventModelMessage, IDs: req.IDs, Message: &message, Usage: usage}, nil)
}

func toUsage(in usage) gopact.Usage {
	return gopact.Usage{
		InputTokens:  firstNonZero(in.PromptTokens, in.InputTokens),
		OutputTokens: firstNonZero(in.CompletionTokens, in.OutputTokens),
		TotalTokens:  in.TotalTokens,
	}
}

func usageFromResponses(response responsesResponse) gopact.Usage {
	return gopact.Usage{
		InputTokens:  response.Usage.InputTokens,
		OutputTokens: response.Usage.OutputTokens,
		TotalTokens:  response.Usage.TotalTokens,
	}
}

func usagePtr(usage gopact.Usage) *gopact.Usage {
	if usage.InputTokens == 0 && usage.OutputTokens == 0 && usage.TotalTokens == 0 {
		return nil
	}
	return &usage
}

func firstNonZero(values ...int) int {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
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
