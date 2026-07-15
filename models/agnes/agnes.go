// Package agnes adapts Agnes AI's OpenAI-compatible model API.
package agnes

import (
	"context"
	"errors"
	"iter"
	"net/http"
	"strings"
	"time"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact-ext/models/openai"
)

const (
	DefaultProvider   = "agnes"
	DefaultBaseURL    = "https://apihub.agnes-ai.com/v1"
	DefaultModel      = "agnes-2.0-flash"
	DefaultImageModel = "agnes-image-2.1-flash"
	DefaultVideoModel = "agnes-video-v2.0"
)

type config struct {
	baseURL      string
	common       []openai.Option
	chatSpecific []openai.Option
	httpClient   *http.Client
	timeout      time.Duration
}

// Option configures a Model.
type Option func(*config)

// Model is an Agnes model adapter.
type Model struct {
	chat       *openai.Model
	baseURL    string
	apiKey     string
	httpClient *http.Client
	timeout    time.Duration
}

var (
	_ gopact.StreamingModel = (*Model)(nil)
	_ gopact.ModelCatalog   = (*Model)(nil)
)

// RetryPolicy configures bounded provider retries.
type RetryPolicy = openai.RetryPolicy

// New creates an Agnes model adapter.
func New(apiKey string, opts ...Option) (*Model, error) {
	cfg := config{baseURL: DefaultBaseURL, httpClient: http.DefaultClient, timeout: 2 * time.Minute}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	chat, err := openai.New(
		DefaultProvider,
		cfg.baseURL,
		apiKey,
		DefaultModel,
		append(append([]openai.Option(nil), cfg.common...), cfg.chatSpecific...)...,
	)
	if err != nil {
		return nil, err
	}
	return &Model{
		chat: chat, baseURL: normalizedBaseURL(cfg.baseURL), apiKey: apiKey,
		httpClient: cfg.httpClient, timeout: cfg.timeout,
	}, nil
}

// NewRequest creates a request from messages and configured defaults.
func (model *Model) NewRequest(messages ...gopact.Message) gopact.ModelRequest {
	if model == nil || model.chat == nil {
		return gopact.ModelRequest{}
	}
	return model.chat.NewRequest(messages...)
}

// Invoke calls Agnes chat completions.
func (model *Model) Invoke(ctx context.Context, request gopact.ModelRequest, opts ...gopact.ModelCallOption) (gopact.ModelResponse, error) {
	if model == nil || model.chat == nil {
		return gopact.ModelResponse{}, errors.New("agnes: model is nil")
	}
	return model.chat.Invoke(ctx, request, opts...)
}

// InvokeStream calls streaming Agnes chat completions.
func (model *Model) InvokeStream(ctx context.Context, request gopact.ModelRequest, opts ...gopact.ModelCallOption) iter.Seq2[gopact.ModelOutputChunk, error] {
	if model == nil || model.chat == nil {
		return func(yield func(gopact.ModelOutputChunk, error) bool) {
			yield(gopact.ModelOutputChunk{}, errors.New("agnes: model is nil"))
		}
	}
	return model.chat.InvokeStream(ctx, request, opts...)
}

// ListModels returns models available to the configured Agnes API key.
func (model *Model) ListModels(ctx context.Context) (gopact.ModelList, error) {
	if model == nil || model.chat == nil {
		return gopact.ModelList{}, errors.New("agnes: model is nil")
	}
	return model.chat.ListModels(ctx)
}

// WithBaseURL overrides the Agnes API base URL.
func WithBaseURL(baseURL string) Option {
	return func(cfg *config) { cfg.baseURL = baseURL }
}

// WithHTTPClient sets the HTTP client.
func WithHTTPClient(client *http.Client) Option {
	return func(cfg *config) {
		cfg.httpClient = client
		cfg.common = append(cfg.common, openai.WithHTTPClient(client))
	}
}

// WithDefaultRequest sets the request template used by NewRequest.
func WithDefaultRequest(request gopact.ModelRequest) Option {
	return func(cfg *config) { cfg.chatSpecific = append(cfg.chatSpecific, openai.WithDefaultRequest(request)) }
}

// WithMaxAttempts sets the maximum attempts for retryable calls.
func WithMaxAttempts(attempts int) Option {
	return func(cfg *config) { cfg.common = append(cfg.common, openai.WithMaxAttempts(attempts)) }
}

// WithRetryPolicy sets bounded retry attempts and backoff.
func WithRetryPolicy(policy RetryPolicy) Option {
	return func(cfg *config) { cfg.common = append(cfg.common, openai.WithRetryPolicy(policy)) }
}

// WithTimeout bounds each provider call.
func WithTimeout(timeout time.Duration) Option {
	return func(cfg *config) {
		cfg.timeout = timeout
		cfg.common = append(cfg.common, openai.WithTimeout(timeout))
	}
}

// WithInsecureHTTP permits HTTP endpoints for local development and tests.
func WithInsecureHTTP() Option {
	return func(cfg *config) {
		cfg.common = append(cfg.common, openai.WithInsecureHTTP())
	}
}

func normalizedBaseURL(baseURL string) string {
	if !strings.Contains(baseURL, "://") {
		baseURL = "https://" + baseURL
	}
	return strings.TrimRight(baseURL, "/")
}
