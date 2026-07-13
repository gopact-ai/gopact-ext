// Package openai provides a minimal OpenAI-compatible chat completions adapter.
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
	"strconv"
	"strings"
	"time"

	"github.com/gopact-ai/gopact"
)

const (
	maxTextBytes          = 4 << 10
	maxResponseBytes      = 4 << 20
	maxStreamFrameBytes   = 1 << 20
	defaultMaxAttempts    = 3
	defaultRequestTimeout = 2 * time.Minute
	defaultInitialBackoff = 100 * time.Millisecond
	defaultMaxBackoff     = 2 * time.Second
	maximumRetryAfter     = 30 * time.Second
	backoffFactor         = 2
)

// RetryPolicy configures bounded retries for transient provider failures.
type RetryPolicy struct {
	MaxAttempts    int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
}

// Model is an OpenAI-compatible chat completions adapter.
type Model struct {
	provider       string
	baseURL        string
	apiKey         string
	httpClient     *http.Client
	defaultRequest gopact.ModelRequest
	retry          RetryPolicy
	timeout        time.Duration
	configErr      error
}

// Option configures a Model.
type Option func(*Model)

// WithHTTPClient sets the HTTP client.
func WithHTTPClient(client *http.Client) Option {
	return func(model *Model) {
		if client == nil {
			model.configErr = errors.Join(model.configErr, errors.New("openai: http client is nil"))
			return
		}
		model.httpClient = client
	}
}

// WithDefaultRequest sets the request template used by NewRequest.
func WithDefaultRequest(req gopact.ModelRequest) Option {
	return func(model *Model) {
		model.defaultRequest = cloneModelRequest(req)
	}
}

// WithMaxAttempts sets the maximum attempts for retryable calls.
func WithMaxAttempts(n int) Option {
	return func(model *Model) {
		model.retry.MaxAttempts = n
	}
}

// WithRetryPolicy sets bounded retry attempts and backoff.
func WithRetryPolicy(policy RetryPolicy) Option {
	return func(model *Model) {
		model.retry = policy
	}
}

// WithTimeout bounds each provider call independently of the HTTP client timeout.
func WithTimeout(timeout time.Duration) Option {
	return func(model *Model) {
		model.timeout = timeout
	}
}

// New creates an OpenAI-compatible model adapter.
func New(provider, baseURL, apiKey, model string, opts ...Option) (*Model, error) {
	if provider == "" {
		return nil, errors.New("openai: provider is required")
	}
	if baseURL == "" {
		return nil, errors.New("openai: base url is required")
	}
	if apiKey == "" {
		return nil, errors.New("openai: api key is required")
	}
	client := &Model{
		provider:       provider,
		baseURL:        strings.TrimRight(baseURL, "/"),
		apiKey:         apiKey,
		httpClient:     http.DefaultClient,
		defaultRequest: gopact.ModelRequest{Model: model},
		retry: RetryPolicy{
			MaxAttempts: defaultMaxAttempts, InitialBackoff: defaultInitialBackoff, MaxBackoff: defaultMaxBackoff,
		},
		timeout: defaultRequestTimeout,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(client)
		}
	}
	if client.defaultRequest.Model == "" {
		client.defaultRequest.Model = model
	}
	if err := client.validate(); err != nil {
		return nil, err
	}
	return client, nil
}

func (c *Model) validate() error {
	if c.configErr != nil {
		return c.configErr
	}
	if c.defaultRequest.Model == "" {
		return errors.New("openai: model is required")
	}
	if c.httpClient == nil {
		return errors.New("openai: http client is nil")
	}
	if c.timeout <= 0 {
		return errors.New("openai: timeout must be positive")
	}
	if err := c.retry.validate(); err != nil {
		return err
	}
	if err := validateBaseURL(c.baseURL); err != nil {
		return err
	}
	return validateModelRequest(c.defaultRequest, false)
}

func (policy RetryPolicy) validate() error {
	if policy.MaxAttempts <= 0 {
		return errors.New("openai: retry max attempts must be positive")
	}
	if policy.InitialBackoff < 0 || policy.MaxBackoff < 0 || policy.InitialBackoff > policy.MaxBackoff {
		return errors.New("openai: retry backoff is invalid")
	}
	return nil
}

func validateBaseURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return errors.New("openai: base url is invalid")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("openai: base url must not contain credentials, query, or fragment")
	}
	return nil
}

func (c *Model) callContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.TODO()
	}
	return context.WithTimeout(ctx, c.timeout)
}

// NewRequest returns a request copied from the model default template.
func (c *Model) NewRequest(messages ...gopact.Message) gopact.ModelRequest {
	if c == nil {
		return gopact.ModelRequest{}
	}
	req := cloneModelRequest(c.defaultRequest)
	req.Messages = cloneMessages(messages)
	return req
}

// Invoke calls the OpenAI-compatible chat completions endpoint.
func (c *Model) Invoke(ctx context.Context, req gopact.ModelRequest, opts ...gopact.ModelCallOption) (gopact.ModelResponse, error) {
	if c == nil {
		return gopact.ModelResponse{}, errors.New("openai: model is nil")
	}
	ctx, cancel := c.callContext(ctx)
	defer cancel()
	cfg := gopact.ResolveModelCallOptions(opts...)
	for key := range cfg.Extensions {
		return gopact.ModelResponse{}, fmt.Errorf("openai: unknown call extension %q", key)
	}
	body, err := c.newChatRequest(req, false)
	if err != nil {
		return gopact.ModelResponse{}, err
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return gopact.ModelResponse{}, fmt.Errorf("openai: encode request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.baseURL+"/chat/completions",
		bytes.NewReader(payload),
	)
	if err != nil {
		return gopact.ModelResponse{}, fmt.Errorf("openai: create request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := c.do(httpReq)
	if err != nil {
		return gopact.ModelResponse{}, fmt.Errorf("openai: invoke: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if !successfulStatus(resp.StatusCode) {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, maxTextBytes))
		return gopact.ModelResponse{}, Error{
			StatusCode: resp.StatusCode,
			Body:       bounded(c.redact(strings.TrimSpace(string(msg)))),
			Retryable:  retryableStatus(resp.StatusCode),
		}
	}
	var out chatResponse
	encoded, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return gopact.ModelResponse{}, fmt.Errorf("openai: decode response: %w", err)
	}
	if len(encoded) > maxResponseBytes {
		return gopact.ModelResponse{}, fmt.Errorf("openai: decode response: body exceeds %d bytes", maxResponseBytes)
	}
	if err := json.Unmarshal(encoded, &out); err != nil {
		return gopact.ModelResponse{}, fmt.Errorf("openai: decode response: %w", err)
	}
	if len(out.Choices) == 0 {
		return gopact.ModelResponse{}, errors.New("openai: response has no choices")
	}
	text := out.Choices[0].Message.Content
	if err := c.emitDelta(ctx, cfg.ModelEventSinks, text); err != nil {
		return gopact.ModelResponse{}, err
	}
	intent, err := c.responseIntent(out.Choices[0].Message)
	if err != nil {
		return gopact.ModelResponse{}, err
	}
	return gopact.ModelResponse{
		Message: gopact.Message{
			Role:  "assistant",
			Parts: []gopact.MessagePart{{Type: "text", Text: text}},
		},
		Intent:       intent,
		Usage:        out.Usage.toGopact(),
		FinishReason: out.Choices[0].FinishReason,
		ProviderMetadata: map[string]any{
			"id":       bounded(out.ID),
			"model":    bounded(out.Model),
			"provider": c.provider,
		},
	}, nil
}

// InvokeStream streams text chunks from the OpenAI-compatible chat completions endpoint.
func (c *Model) InvokeStream(ctx context.Context, req gopact.ModelRequest, opts ...gopact.ModelCallOption) iter.Seq2[gopact.ModelOutputChunk, error] {
	return func(yield func(gopact.ModelOutputChunk, error) bool) {
		if c == nil {
			yield(gopact.ModelOutputChunk{}, errors.New("openai: model is nil"))
			return
		}
		callCtx, cancel := c.callContext(ctx)
		defer cancel()
		cfg := gopact.ResolveModelCallOptions(opts...)
		for key := range cfg.Extensions {
			yield(gopact.ModelOutputChunk{}, fmt.Errorf("openai: unknown call extension %q", key))
			return
		}
		body, err := c.newChatRequest(req, true)
		if err != nil {
			yield(gopact.ModelOutputChunk{}, err)
			return
		}
		payload, err := json.Marshal(body)
		if err != nil {
			yield(gopact.ModelOutputChunk{}, fmt.Errorf("openai: encode request: %w", err))
			return
		}
		httpReq, err := http.NewRequestWithContext(callCtx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(payload))
		if err != nil {
			yield(gopact.ModelOutputChunk{}, fmt.Errorf("openai: create request: %w", err))
			return
		}
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
		httpReq.Header.Set("Content-Type", "application/json")
		resp, err := c.do(httpReq)
		if err != nil {
			yield(gopact.ModelOutputChunk{}, fmt.Errorf("openai: invoke stream: %w", err))
			return
		}
		defer func() { _ = resp.Body.Close() }()
		if !successfulStatus(resp.StatusCode) {
			msg, _ := io.ReadAll(io.LimitReader(resp.Body, maxTextBytes))
			yield(gopact.ModelOutputChunk{}, Error{
				StatusCode: resp.StatusCode,
				Body:       bounded(c.redact(strings.TrimSpace(string(msg)))),
				Retryable:  retryableStatus(resp.StatusCode),
			})
			return
		}
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, maxTextBytes), maxStreamFrameBytes)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, ":") {
				continue
			}
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "[DONE]" {
				return
			}
			var chunk chatStreamChunk
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				yield(gopact.ModelOutputChunk{}, fmt.Errorf("openai: decode stream chunk: %w", err))
				return
			}
			if len(chunk.Choices) == 0 || chunk.Choices[0].Delta.Content == "" {
				continue
			}
			text := chunk.Choices[0].Delta.Content
			if err := c.emitDelta(callCtx, cfg.ModelEventSinks, text); err != nil {
				yield(gopact.ModelOutputChunk{}, err)
				return
			}
			if !yield(gopact.ModelOutputChunk{Text: text}, nil) {
				return
			}
		}
		if err := scanner.Err(); err != nil {
			yield(gopact.ModelOutputChunk{}, fmt.Errorf("openai: read stream: %w", err))
		}
	}
}

func (c *Model) emitDelta(ctx context.Context, sinks []gopact.ModelEventSink, text string) error {
	for _, sink := range sinks {
		if err := sink.EmitModelEvent(ctx, gopact.ModelEvent{Type: gopact.ModelEventMessageDelta, Source: c.provider, Summary: bounded(text)}); err != nil {
			return err
		}
	}
	return nil
}

func (c *Model) redact(s string) string {
	if c.apiKey == "" {
		return s
	}
	return strings.ReplaceAll(s, c.apiKey, "[redacted]")
}

func (c *Model) do(req *http.Request) (*http.Response, error) {
	var lastErr error
	for attempt := 1; attempt <= c.retry.MaxAttempts; attempt++ {
		resp, err := c.doAttempt(req, attempt)
		if !c.shouldRetry(req.Context(), resp, err, attempt) {
			return resp, c.safeError(err)
		}
		lastErr = err
		delay := c.retryDelay(resp, attempt)
		closeRetryResponse(resp)
		if err := waitRetry(req.Context(), delay); err != nil {
			return nil, err
		}
	}
	return nil, c.safeError(lastErr)
}

func (c *Model) doAttempt(req *http.Request, attempt int) (*http.Response, error) {
	if err := resetRequestBody(req, attempt); err != nil {
		return nil, err
	}
	return c.httpClient.Do(req)
}

func (c *Model) shouldRetry(ctx context.Context, resp *http.Response, err error, attempt int) bool {
	if attempt >= c.retry.MaxAttempts || ctx.Err() != nil {
		return false
	}
	if err != nil {
		return true
	}
	return resp != nil && retryableStatus(resp.StatusCode)
}

func (c *Model) retryDelay(resp *http.Response, attempt int) time.Duration {
	if resp != nil {
		if delay, ok := parseRetryAfter(resp.Header.Get("Retry-After"), time.Now()); ok {
			return min(delay, maximumRetryAfter)
		}
	}
	return c.retry.backoff(attempt)
}

func (policy RetryPolicy) backoff(attempt int) time.Duration {
	delay := policy.InitialBackoff
	for step := 1; step < attempt; step++ {
		if delay >= policy.MaxBackoff/time.Duration(backoffFactor) {
			return policy.MaxBackoff
		}
		delay *= time.Duration(backoffFactor)
	}
	return min(delay, policy.MaxBackoff)
}

func parseRetryAfter(value string, now time.Time) (time.Duration, bool) {
	if value == "" {
		return 0, false
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second, true
	}
	when, err := http.ParseTime(value)
	if err != nil || when.Before(now) {
		return 0, false
	}
	return when.Sub(now), true
}

func closeRetryResponse(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxTextBytes))
	_ = resp.Body.Close()
}

func waitRetry(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type redactedError struct {
	message string
	cause   error
}

func (err redactedError) Error() string { return err.message }
func (err redactedError) Unwrap() error { return err.cause }

func (c *Model) safeError(err error) error {
	if err == nil {
		return nil
	}
	return redactedError{message: c.redact(err.Error()), cause: err}
}

func resetRequestBody(req *http.Request, attempt int) error {
	if attempt <= 1 || req.GetBody == nil {
		return nil
	}
	body, err := req.GetBody()
	if err != nil {
		return err
	}
	req.Body = body
	return nil
}

func (c *Model) newChatRequest(req gopact.ModelRequest, stream bool) (chatRequest, error) {
	if err := validateModelRequest(req, true); err != nil {
		return chatRequest{}, err
	}
	messages := make([]chatMessage, 0, len(req.Messages))
	for _, msg := range req.Messages {
		content, err := messageText(msg)
		if err != nil {
			return chatRequest{}, err
		}
		messages = append(messages, chatMessage{Role: msg.Role, Content: content})
	}
	body := chatRequest{
		Model:       req.Model,
		Messages:    messages,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		MaxTokens:   req.MaxOutputTokens,
		Stop:        req.Stop,
		Seed:        req.Seed,
		Stream:      stream,
	}
	if req.Reasoning.Effort != "" {
		body.ReasoningEffort = req.Reasoning.Effort
	}
	if len(req.Tools) > 0 {
		body.Tools = make([]chatTool, 0, len(req.Tools))
		for _, tool := range req.Tools {
			body.Tools = append(body.Tools, chatTool{
				Type: "function",
				Function: chatToolFunction{
					Name:        tool.Name,
					Description: tool.Description,
					Parameters:  tool.Schema,
				},
			})
		}
		body.ToolChoice = encodeToolChoice(req.ToolChoice)
	}
	if len(req.ResponseSchema.Value) > 0 {
		body.ResponseFormat = chatResponseFormat{
			Type: "json_schema",
			JSONSchema: chatJSONSchema{
				Name:   "response",
				Schema: req.ResponseSchema.Value,
			},
		}
	}
	return body, nil
}

func validateModelRequest(req gopact.ModelRequest, required bool) error {
	if req.Model == "" {
		return errors.New("openai: request model is required")
	}
	if required && len(req.Messages) == 0 {
		return errors.New("openai: request has no messages")
	}
	if req.MaxOutputTokens < 0 {
		return errors.New("openai: max output tokens must not be negative")
	}
	if len(req.OutputProtocols) != 0 {
		return errors.New("openai: output protocols are not implemented")
	}
	if len(req.Modalities) != 0 {
		return errors.New("openai: modalities are not implemented")
	}
	if req.ResponseSchema.URI != "" {
		return errors.New("openai: response schema URI is not implemented")
	}
	if len(req.ResponseSchema.Value) != 0 && !json.Valid(req.ResponseSchema.Value) {
		return errors.New("openai: response schema is invalid JSON")
	}
	if err := validateToolChoice(req.ToolChoice); err != nil {
		return err
	}
	if err := validateMessages(req.Messages); err != nil {
		return err
	}
	if err := validateTools(req.Tools); err != nil {
		return err
	}
	for key := range req.Extensions {
		return fmt.Errorf("openai: unknown request extension %q", key)
	}
	return nil
}

func validateMessages(messages []gopact.Message) error {
	for _, message := range messages {
		if message.Role == "" {
			return errors.New("openai: message role is required")
		}
		if _, err := messageText(message); err != nil {
			return err
		}
	}
	return nil
}

func validateTools(tools []gopact.ToolSpec) error {
	seen := make(map[string]struct{}, len(tools))
	for _, tool := range tools {
		if tool.Name == "" || len(tool.Schema) == 0 || !json.Valid(tool.Schema) {
			return fmt.Errorf("openai: tool %q has an invalid schema", tool.Name)
		}
		if _, exists := seen[tool.Name]; exists {
			return fmt.Errorf("openai: duplicate tool %q", tool.Name)
		}
		seen[tool.Name] = struct{}{}
	}
	return nil
}

func validateToolChoice(choice gopact.ToolChoice) error {
	switch choice.Mode {
	case "", "auto", "none", "required":
		return nil
	case "named":
		if choice.Name == "" {
			return errors.New("openai: named tool choice requires a name")
		}
		return nil
	default:
		return fmt.Errorf("openai: unknown tool choice mode %q", choice.Mode)
	}
}

func encodeToolChoice(choice gopact.ToolChoice) any {
	switch choice.Mode {
	case "":
		return nil
	case "auto", "none", "required":
		return choice.Mode
	default:
		return namedToolChoice(choice.Name)
	}
}

func namedToolChoice(name string) any {
	if name == "" {
		return nil
	}
	return chatNamedToolChoice{
		Type: "function",
		Function: struct {
			Name string `json:"name"`
		}{Name: name},
	}
}

func messageText(msg gopact.Message) (string, error) {
	var b strings.Builder
	for _, part := range msg.Parts {
		if part.Type != "text" || part.Ref != nil {
			return "", fmt.Errorf("openai: unsupported message part %q", part.Type)
		}
		b.WriteString(part.Text)
	}
	return b.String(), nil
}

func (c *Model) responseIntent(message chatMessage) (gopact.ModelIntent, error) {
	if len(message.ToolCalls) == 0 {
		return gopact.FinalIntent{}, nil
	}
	calls := make([]gopact.ToolCall, 0, len(message.ToolCalls))
	seen := make(map[string]struct{}, len(message.ToolCalls))
	for _, call := range message.ToolCalls {
		if call.ID == "" || call.Function.Name == "" {
			return nil, errors.New("openai: tool call id and name are required")
		}
		if _, exists := seen[call.ID]; exists {
			return nil, fmt.Errorf("openai: duplicate tool call id %q", call.ID)
		}
		if !json.Valid([]byte(call.Function.Arguments)) {
			return nil, fmt.Errorf("openai: tool arguments for %q are invalid JSON", call.Function.Name)
		}
		seen[call.ID] = struct{}{}
		calls = append(calls, gopact.ToolCall{
			ID: call.ID, Name: call.Function.Name, Arguments: json.RawMessage(call.Function.Arguments), SourceRef: c.provider,
		})
	}
	return gopact.ToolCallIntent{Calls: calls}, nil
}

func cloneModelRequest(req gopact.ModelRequest) gopact.ModelRequest {
	req.Messages = cloneMessages(req.Messages)
	req.Tools = cloneToolSpecs(req.Tools)
	req.Modalities = append([]gopact.Modality(nil), req.Modalities...)
	req.Stop = append([]string(nil), req.Stop...)
	req.OutputProtocols = append([]gopact.OutputProtocol(nil), req.OutputProtocols...)
	req.ResponseSchema.Value = append(json.RawMessage(nil), req.ResponseSchema.Value...)
	req.Temperature = cloneFloat(req.Temperature)
	req.TopP = cloneFloat(req.TopP)
	req.Seed = cloneInt64(req.Seed)
	req.Metadata = cloneStringMap(req.Metadata)
	req.Extensions = cloneAnyMap(req.Extensions)
	return req
}

func cloneMessages(messages []gopact.Message) []gopact.Message {
	cloned := make([]gopact.Message, len(messages))
	for index, message := range messages {
		message.Parts = cloneMessageParts(message.Parts)
		cloned[index] = message
	}
	return cloned
}

func cloneMessageParts(parts []gopact.MessagePart) []gopact.MessagePart {
	cloned := append([]gopact.MessagePart(nil), parts...)
	for index := range cloned {
		if cloned[index].Ref != nil {
			ref := *cloned[index].Ref
			cloned[index].Ref = &ref
		}
	}
	return cloned
}

func cloneToolSpecs(tools []gopact.ToolSpec) []gopact.ToolSpec {
	cloned := make([]gopact.ToolSpec, len(tools))
	for index, tool := range tools {
		tool.Schema = append(json.RawMessage(nil), tool.Schema...)
		tool.Metadata = cloneStringMap(tool.Metadata)
		cloned[index] = tool
	}
	return cloned
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func cloneAnyMap(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	cloned := make(map[string]any, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func cloneFloat(value *float64) *float64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

type chatRequest struct {
	Model           string        `json:"model"`
	Messages        []chatMessage `json:"messages"`
	Temperature     *float64      `json:"temperature,omitempty"`
	TopP            *float64      `json:"top_p,omitempty"`
	MaxTokens       int           `json:"max_tokens,omitempty"`
	Seed            *int64        `json:"seed,omitempty"`
	Stop            []string      `json:"stop,omitempty"`
	Tools           []chatTool    `json:"tools,omitempty"`
	ToolChoice      any           `json:"tool_choice,omitempty"`
	ResponseFormat  any           `json:"response_format,omitempty"`
	ReasoningEffort string        `json:"reasoning_effort,omitempty"`
	Stream          bool          `json:"stream,omitempty"`
}

type chatMessage struct {
	Role      string         `json:"role"`
	Content   string         `json:"content,omitempty"`
	ToolCalls []chatToolCall `json:"tool_calls,omitempty"`
}

type chatResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Message      chatMessage `json:"message"`
		FinishReason string      `json:"finish_reason"`
	} `json:"choices"`
	Usage chatUsage `json:"usage"`
}

type chatTool struct {
	Type     string           `json:"type"`
	Function chatToolFunction `json:"function"`
}

type chatToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
	Arguments   string          `json:"arguments,omitempty"`
}

type chatToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function chatToolFunction `json:"function"`
}

type chatNamedToolChoice struct {
	Type     string `json:"type"`
	Function struct {
		Name string `json:"name"`
	} `json:"function"`
}

type chatResponseFormat struct {
	Type       string         `json:"type"`
	JSONSchema chatJSONSchema `json:"json_schema"`
}

type chatJSONSchema struct {
	Name   string          `json:"name"`
	Schema json.RawMessage `json:"schema"`
}

type chatStreamChunk struct {
	Choices []struct {
		Delta chatMessage `json:"delta"`
	} `json:"choices"`
}

type chatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

func (u chatUsage) toGopact() gopact.Usage {
	return gopact.Usage{
		InputTokens:  u.PromptTokens,
		OutputTokens: u.CompletionTokens,
		TotalTokens:  u.TotalTokens,
	}
}

// Error reports an OpenAI-compatible HTTP error.
type Error struct {
	StatusCode int
	Body       string
	Retryable  bool
}

func (e Error) Error() string {
	return fmt.Sprintf("openai: status %d: %s", e.StatusCode, e.Body)
}

func retryableStatus(status int) bool {
	return status == http.StatusTooManyRequests || status >= http.StatusInternalServerError
}

func successfulStatus(status int) bool {
	return status >= http.StatusOK && status < http.StatusMultipleChoices
}

func bounded(s string) string {
	if len(s) <= maxTextBytes {
		return s
	}
	return s[:maxTextBytes]
}
