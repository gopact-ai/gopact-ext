// Package codex provides a gopact model adapter for the OpenAI Codex backend
// available to eligible ChatGPT plans.
package codex

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
	"strconv"
	"strings"
	"time"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact-ext/models/openai/codexauth"
)

const (
	// DefaultBaseURL is the ChatGPT Codex backend used by the official Codex
	// client for ChatGPT-authenticated model requests.
	DefaultBaseURL = "https://chatgpt.com/backend-api/codex"

	// MessagePartTypeResponseItem identifies opaque Codex response state that
	// must be preserved when a Message is supplied to a later request. Callers
	// should copy these parts unchanged and must not render them as user text.
	MessagePartTypeResponseItem = "openai.codex.response_item"

	providerName          = "openai.codex"
	defaultOriginator     = "gopact-ext"
	defaultUserAgent      = "gopact-ext/openai-codex"
	defaultRequestTimeout = 5 * time.Minute
	defaultMaxAttempts    = 3
	defaultMaxRedirects   = 10
	defaultRetryDelay     = 200 * time.Millisecond
	maximumRetryAfter     = 30 * time.Second
	maxHTTPErrorBytes     = 64 << 10
	maxRequestBytes       = 16 << 20
	minHeaderByte         = 0x21
	maxHeaderByte         = 0x7e
	retryBackoffFactor    = 2
)

var errStopIteration = errors.New("codex: stream consumer stopped")

// TokenSource returns the current ChatGPT OAuth credentials for a model call.
// Implementations must be safe for concurrent use.
type TokenSource interface {
	Token(context.Context) (codexauth.Tokens, error)
}

// RefreshingTokenSource can rotate credentials after an unauthorized response.
// codexauth.Source implements this interface.
type RefreshingTokenSource interface {
	TokenSource
	Refresh(context.Context) (codexauth.Tokens, error)
}

// TokenSourceFunc adapts a function into a TokenSource.
type TokenSourceFunc func(context.Context) (codexauth.Tokens, error)

// Token implements TokenSource.
func (source TokenSourceFunc) Token(ctx context.Context) (codexauth.Tokens, error) {
	if source == nil {
		return codexauth.Tokens{}, errors.New("codex: token source function is nil")
	}
	return source(ctx)
}

type staticTokenSource struct{ tokens codexauth.Tokens }

func (source staticTokenSource) Token(context.Context) (codexauth.Tokens, error) {
	return source.tokens, nil
}

// StaticTokenSource returns a non-refreshing source. It is suitable for short
// calls and tests; long-running applications should use codexauth.Source or an
// equivalent persisted implementation.
func StaticTokenSource(tokens codexauth.Tokens) TokenSource {
	return staticTokenSource{tokens: tokens}
}

// Model calls the ChatGPT Codex Responses endpoint.
type Model struct {
	baseURL        string
	tokenSource    TokenSource
	httpClient     *http.Client
	defaultRequest gopact.ModelRequest
	timeout        time.Duration
	maxAttempts    int
	originator     string
	allowHTTP      bool
	configErr      error
}

var (
	_ gopact.StreamingModel = (*Model)(nil)
	_ TokenSource           = (*codexauth.Source)(nil)
	_ RefreshingTokenSource = (*codexauth.Source)(nil)
)

// Option configures a Model.
type Option func(*Model)

// WithBaseURL overrides the Codex backend URL. HTTPS is required unless
// WithInsecureHTTP is also supplied.
func WithBaseURL(baseURL string) Option {
	return func(model *Model) {
		model.baseURL = strings.TrimRight(baseURL, "/")
	}
}

// WithHTTPClient sets the HTTP client. The client is copied and restricted to
// same-origin redirects so OAuth credentials cannot be forwarded elsewhere.
func WithHTTPClient(client *http.Client) Option {
	return func(model *Model) {
		if client == nil {
			model.configErr = errors.Join(model.configErr, errors.New("codex: http client is nil"))
			return
		}
		model.httpClient = client
	}
}

// WithDefaultRequest sets the template copied by NewRequest.
func WithDefaultRequest(request gopact.ModelRequest) Option {
	return func(model *Model) {
		model.defaultRequest = cloneModelRequest(request)
	}
}

// WithTimeout bounds the complete model call, including its response stream.
func WithTimeout(timeout time.Duration) Option {
	return func(model *Model) {
		model.timeout = timeout
	}
}

// WithMaxAttempts sets the maximum number of attempts for transport, 429, and
// server failures that happen before a response stream is consumed.
func WithMaxAttempts(attempts int) Option {
	return func(model *Model) {
		model.maxAttempts = attempts
	}
}

// WithOriginator sets the product identifier sent to the Codex backend.
func WithOriginator(originator string) Option {
	return func(model *Model) {
		model.originator = originator
	}
}

// WithInsecureHTTP permits an HTTP base URL for local tests and development.
func WithInsecureHTTP() Option {
	return func(model *Model) {
		model.allowHTTP = true
	}
}

// New creates a ChatGPT-authenticated Codex model adapter.
func New(modelName string, source TokenSource, opts ...Option) (*Model, error) {
	model := &Model{
		baseURL:        DefaultBaseURL,
		tokenSource:    source,
		httpClient:     http.DefaultClient,
		defaultRequest: gopact.ModelRequest{Model: modelName},
		timeout:        defaultRequestTimeout,
		maxAttempts:    defaultMaxAttempts,
		originator:     defaultOriginator,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(model)
		}
	}
	if model.defaultRequest.Model == "" {
		model.defaultRequest.Model = modelName
	}
	base, err := model.validate()
	if err != nil {
		return nil, err
	}
	model.httpClient = secureHTTPClient(model.httpClient, base)
	return model, nil
}

func (model *Model) validate() (*url.URL, error) {
	if model.configErr != nil {
		return nil, model.configErr
	}
	if model.tokenSource == nil {
		return nil, errors.New("codex: token source is required")
	}
	if model.httpClient == nil {
		return nil, errors.New("codex: http client is nil")
	}
	if model.timeout <= 0 {
		return nil, errors.New("codex: timeout must be positive")
	}
	if model.maxAttempts <= 0 {
		return nil, errors.New("codex: max attempts must be positive")
	}
	if !validHeaderValue(model.originator) {
		return nil, errors.New("codex: originator is invalid")
	}
	base, err := validateBaseURL(model.baseURL, model.allowHTTP)
	if err != nil {
		return nil, err
	}
	if err := validateModelRequest(model.defaultRequest, false); err != nil {
		return nil, err
	}
	return base, nil
}

// NewRequest returns a request copied from the model's default template.
func (model *Model) NewRequest(messages ...gopact.Message) gopact.ModelRequest {
	if model == nil {
		return gopact.ModelRequest{}
	}
	request := cloneModelRequest(model.defaultRequest)
	request.Messages = cloneMessages(messages)
	return request
}

// Invoke runs one Codex Responses call and normalizes its terminal output.
func (model *Model) Invoke(ctx context.Context, request gopact.ModelRequest, opts ...gopact.ModelCallOption) (gopact.ModelResponse, error) {
	if model == nil {
		return gopact.ModelResponse{}, errors.New("codex: model is nil")
	}
	config, err := resolveCallOptions(opts)
	if err != nil {
		return gopact.ModelResponse{}, err
	}
	payload, err := responsesPayload(request)
	if err != nil {
		return gopact.ModelResponse{}, err
	}
	callCtx, cancel := model.callContext(ctx)
	defer cancel()
	result, err := model.execute(callCtx, payload, config, nil)
	if err != nil {
		return gopact.ModelResponse{}, err
	}
	return result.response()
}

// InvokeStream streams visible assistant text from one Codex Responses call.
// Reasoning and tool-call deltas are delivered only through ModelEvent sinks.
func (model *Model) InvokeStream(ctx context.Context, request gopact.ModelRequest, opts ...gopact.ModelCallOption) iter.Seq2[gopact.ModelOutputChunk, error] {
	return func(yield func(gopact.ModelOutputChunk, error) bool) {
		if model == nil {
			yield(gopact.ModelOutputChunk{}, errors.New("codex: model is nil"))
			return
		}
		config, err := resolveCallOptions(opts)
		if err != nil {
			yield(gopact.ModelOutputChunk{}, err)
			return
		}
		payload, err := responsesPayload(request)
		if err != nil {
			yield(gopact.ModelOutputChunk{}, err)
			return
		}
		callCtx, cancel := model.callContext(ctx)
		defer cancel()
		_, err = model.execute(callCtx, payload, config, func(text string) error {
			if !yield(gopact.ModelOutputChunk{Text: text}, nil) {
				return errStopIteration
			}
			return nil
		})
		if err != nil && !errors.Is(err, errStopIteration) {
			yield(gopact.ModelOutputChunk{}, err)
		}
	}
}

func responsesPayload(request gopact.ModelRequest) ([]byte, error) {
	body, err := newResponsesRequest(request)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("codex: encode request: %w", err)
	}
	if len(payload) > maxRequestBytes {
		return nil, fmt.Errorf("codex: request exceeds %d bytes", maxRequestBytes)
	}
	return payload, nil
}

func resolveCallOptions(opts []gopact.ModelCallOption) (gopact.ModelCallConfig, error) {
	config := gopact.ResolveModelCallOptions(opts...)
	for key := range config.Extensions {
		return gopact.ModelCallConfig{}, fmt.Errorf("codex: unknown call extension %q", key)
	}
	return config, nil
}

func (model *Model) callContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.TODO()
	}
	return context.WithTimeout(ctx, model.timeout)
}

func (model *Model) openStream(ctx context.Context, payload []byte) (*http.Response, codexauth.Tokens, error) {
	refreshed := false
	for attempt := 1; attempt <= model.maxAttempts; attempt++ {
		current, err := model.requestAttempt(ctx, payload)
		if err != nil {
			return nil, codexauth.Tokens{}, err
		}
		current, refreshed, err = model.refreshIfNeeded(ctx, payload, current, refreshed)
		if err != nil {
			return nil, codexauth.Tokens{}, err
		}
		if current.successful() {
			return current.response, current.tokens, nil
		}
		if !current.retryable() || attempt == model.maxAttempts || ctx.Err() != nil {
			return nil, codexauth.Tokens{}, model.attemptError(current)
		}
		delay := retryDelay(current.response, attempt)
		closeResponse(current.response)
		if err := waitRetry(ctx, delay); err != nil {
			return nil, codexauth.Tokens{}, err
		}
	}
	return nil, codexauth.Tokens{}, errors.New("codex: request attempts exhausted")
}

func (model *Model) refreshIfNeeded(ctx context.Context, payload []byte, prior requestAttempt, refreshed bool) (requestAttempt, bool, error) {
	if !prior.needsRefresh(refreshed) {
		return prior, refreshed, nil
	}
	current, didRefresh, err := model.refreshAttempt(ctx, payload, prior)
	return current, refreshed || didRefresh, err
}

type requestAttempt struct {
	response *http.Response
	tokens   codexauth.Tokens
	err      error
}

func (model *Model) requestAttempt(ctx context.Context, payload []byte) (requestAttempt, error) {
	tokens, err := model.tokenSource.Token(ctx)
	if err != nil {
		return requestAttempt{}, fmt.Errorf("codex: resolve tokens: %w", err)
	}
	if err := validateTokens(tokens); err != nil {
		return requestAttempt{}, err
	}
	resp, sendErr := model.send(ctx, payload, tokens)
	return requestAttempt{response: resp, tokens: tokens, err: sendErr}, nil
}

func (model *Model) refreshAttempt(ctx context.Context, payload []byte, prior requestAttempt) (requestAttempt, bool, error) {
	source, ok := model.tokenSource.(RefreshingTokenSource)
	if !ok {
		return prior, false, nil
	}
	closeResponse(prior.response)
	tokens, err := source.Refresh(ctx)
	if err != nil {
		return requestAttempt{}, false, fmt.Errorf("codex: refresh after unauthorized: %w", err)
	}
	if err := validateTokens(tokens); err != nil {
		return requestAttempt{}, false, err
	}
	resp, sendErr := model.send(ctx, payload, tokens)
	return requestAttempt{response: resp, tokens: tokens, err: sendErr}, true, nil
}

func (attempt requestAttempt) needsRefresh(refreshed bool) bool {
	return attempt.err == nil && attempt.response != nil &&
		attempt.response.StatusCode == http.StatusUnauthorized && !refreshed
}

func (attempt requestAttempt) successful() bool {
	return attempt.err == nil && attempt.response != nil && successfulStatus(attempt.response.StatusCode)
}

func (attempt requestAttempt) retryable() bool {
	return attempt.err != nil || attempt.response != nil && retryableStatus(attempt.response.StatusCode)
}

func (model *Model) attemptError(attempt requestAttempt) error {
	if attempt.err != nil {
		closeResponse(attempt.response)
		return fmt.Errorf("codex: send request: %w", attempt.err)
	}
	if attempt.response == nil {
		return errors.New("codex: send request returned no response")
	}
	return model.httpError(attempt.response, attempt.tokens)
}

func (model *Model) send(ctx context.Context, payload []byte, tokens codexauth.Tokens) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, model.baseURL+"/responses", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+tokens.AccessToken)
	if tokens.AccountID != "" {
		request.Header.Set("ChatGPT-Account-ID", tokens.AccountID)
	}
	request.Header.Set("Accept", "text/event-stream")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Originator", model.originator)
	request.Header.Set("User-Agent", defaultUserAgent)
	return model.httpClient.Do(request)
}

// HTTPError reports a non-successful Codex backend response without retaining
// credential-bearing response bodies.
type HTTPError struct {
	StatusCode int
	RequestID  string
	Code       string
	Message    string
	Retryable  bool
}

// Error implements error.
func (err *HTTPError) Error() string {
	if err == nil {
		return "codex: http error"
	}
	detail := err.Message
	if err.Code != "" {
		if detail == "" {
			detail = err.Code
		} else {
			detail = err.Code + ": " + detail
		}
	}
	if detail == "" {
		detail = http.StatusText(err.StatusCode)
	}
	return fmt.Sprintf("codex: status %d: %s", err.StatusCode, detail)
}

func (model *Model) httpError(resp *http.Response, tokens codexauth.Tokens) error {
	defer closeResponse(resp)
	encoded, readErr := io.ReadAll(io.LimitReader(resp.Body, maxHTTPErrorBytes+1))
	if readErr != nil {
		return &HTTPError{
			StatusCode: resp.StatusCode,
			RequestID:  resp.Header.Get("x-request-id"),
			Message:    "failed to read error response",
			Retryable:  retryableStatus(resp.StatusCode),
		}
	}
	if len(encoded) > maxHTTPErrorBytes {
		encoded = encoded[:maxHTTPErrorBytes]
	}
	code, message := parseErrorBody(encoded)
	message = redactTokens(message, tokens)
	return &HTTPError{
		StatusCode: resp.StatusCode,
		RequestID:  resp.Header.Get("x-request-id"),
		Code:       code,
		Message:    message,
		Retryable:  retryableStatus(resp.StatusCode),
	}
}

func parseErrorBody(encoded []byte) (string, string) {
	var body struct {
		Detail string `json:"detail"`
		Error  struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(encoded, &body) == nil {
		if body.Error.Code != "" || body.Error.Message != "" {
			return body.Error.Code, body.Error.Message
		}
		if body.Detail != "" {
			return "", body.Detail
		}
	}
	return "", strings.TrimSpace(string(encoded))
}

func validateTokens(tokens codexauth.Tokens) error {
	if tokens.AccessToken == "" {
		return errors.New("codex: access token is empty")
	}
	if !validHeaderValue(tokens.AccessToken) {
		return errors.New("codex: access token is invalid")
	}
	if tokens.AccountID != "" && !validHeaderValue(tokens.AccountID) {
		return errors.New("codex: account id is invalid")
	}
	return nil
}

func redactTokens(value string, tokens codexauth.Tokens) string {
	for _, secret := range []string{tokens.AccessToken, tokens.IDToken, tokens.RefreshToken} {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, "[redacted]")
		}
	}
	return value
}

func validateBaseURL(value string, allowHTTP bool) (*url.URL, error) {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, errors.New("codex: base url is invalid")
	}
	if parsed.Scheme == "http" && !allowHTTP {
		return nil, errors.New("codex: HTTP base url requires WithInsecureHTTP")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Opaque != "" {
		return nil, errors.New("codex: base url must not contain credentials, query, or fragment")
	}
	return parsed, nil
}

func secureHTTPClient(base *http.Client, origin *url.URL) *http.Client {
	secured := *base
	callerPolicy := secured.CheckRedirect
	secured.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) >= defaultMaxRedirects {
			return errors.New("codex: stopped after too many redirects")
		}
		if !sameOrigin(origin, request.URL) {
			return errors.New("codex: refusing cross-origin redirect")
		}
		if callerPolicy != nil {
			return callerPolicy(request, via)
		}
		return nil
	}
	return &secured
}

func sameOrigin(left, right *url.URL) bool {
	return strings.EqualFold(left.Scheme, right.Scheme) &&
		strings.EqualFold(left.Hostname(), right.Hostname()) &&
		effectivePort(left) == effectivePort(right)
}

func effectivePort(value *url.URL) string {
	if port := value.Port(); port != "" {
		return port
	}
	if value.Scheme == "https" {
		return "443"
	}
	if value.Scheme == "http" {
		return "80"
	}
	return ""
}

func validHeaderValue(value string) bool {
	if value == "" || strings.TrimSpace(value) != value {
		return false
	}
	for index := range len(value) {
		if value[index] < minHeaderByte || value[index] > maxHeaderByte {
			return false
		}
	}
	return true
}

func successfulStatus(status int) bool {
	return status >= http.StatusOK && status < http.StatusMultipleChoices
}

func retryableStatus(status int) bool {
	return status == http.StatusTooManyRequests || status >= http.StatusInternalServerError
}

func retryDelay(resp *http.Response, attempt int) time.Duration {
	if resp != nil {
		if delay, ok := parseRetryAfter(resp.Header.Get("Retry-After"), time.Now()); ok {
			return min(delay, maximumRetryAfter)
		}
	}
	delay := defaultRetryDelay
	for step := 1; step < attempt; step++ {
		delay *= retryBackoffFactor
		if delay >= maximumRetryAfter {
			return maximumRetryAfter
		}
	}
	return delay
}

func parseRetryAfter(value string, now time.Time) (time.Duration, bool) {
	if value == "" {
		return 0, false
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		if seconds > int(maximumRetryAfter/time.Second) {
			return maximumRetryAfter, true
		}
		return time.Duration(seconds) * time.Second, true
	}
	when, err := http.ParseTime(value)
	if err != nil || when.Before(now) {
		return 0, false
	}
	return when.Sub(now), true
}

func closeResponse(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxHTTPErrorBytes))
	_ = resp.Body.Close()
}

func waitRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-timer.C:
		return nil
	}
}
