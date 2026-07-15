// Package glm adapts GLM/Z.AI model APIs.
package glm

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
	DefaultProvider       = "glm"
	DefaultBaseURL        = "https://api.z.ai/api/coding/paas/v4"
	DefaultAPIBaseURL     = "https://api.z.ai/api/paas/v4"
	DefaultMonitorBaseURL = "https://api.z.ai/api/monitor/usage"
	DefaultModel          = "glm-5-turbo"
	DefaultEmbeddingModel = "embedding-3"
)

type config struct {
	chatBaseURL  string
	apiBaseURL   string
	monitorURL   string
	common       []openai.Option
	chatSpecific []openai.Option
	httpClient   *http.Client
	timeout      time.Duration
	allowHTTP    bool
}

// Option configures a Model.
type Option func(*config)

// Model is a GLM model adapter.
type Model struct {
	chat       *openai.Model
	api        *openai.Model
	apiKey     string
	apiBaseURL string
	httpClient *http.Client
	monitorURL string
	timeout    time.Duration
	allowHTTP  bool
}

var (
	_ gopact.StreamingModel = (*Model)(nil)
	_ gopact.Embedder       = (*Model)(nil)
	_ gopact.ModelCatalog   = (*Model)(nil)
)

// RetryPolicy configures bounded provider retries.
type RetryPolicy = openai.RetryPolicy

// ModerationRequest classifies Z.AI text or multimodal input.
type ModerationRequest struct {
	Model string `json:"model"`
	Input any    `json:"input"`
}

// ModerationResponse contains the input classification returned by Z.AI.
type ModerationResponse struct {
	Model string `json:"model"`
	Input any    `json:"input"`
}

// New creates a GLM model adapter.
func New(apiKey string, opts ...Option) (*Model, error) {
	cfg := config{
		chatBaseURL: DefaultBaseURL,
		apiBaseURL:  DefaultAPIBaseURL,
		monitorURL:  DefaultMonitorBaseURL,
		httpClient:  http.DefaultClient,
		timeout:     2 * time.Minute,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	chat, err := openai.New(
		DefaultProvider,
		cfg.chatBaseURL,
		apiKey,
		DefaultModel,
		append(append([]openai.Option(nil), cfg.common...), cfg.chatSpecific...)...,
	)
	if err != nil {
		return nil, err
	}
	api, err := openai.New(DefaultProvider, cfg.apiBaseURL, apiKey, DefaultEmbeddingModel, cfg.common...)
	if err != nil {
		return nil, err
	}
	return &Model{
		chat: chat, api: api, apiKey: apiKey, apiBaseURL: normalizedBaseURL(cfg.apiBaseURL), httpClient: cfg.httpClient,
		monitorURL: normalizedBaseURL(cfg.monitorURL), timeout: cfg.timeout, allowHTTP: cfg.allowHTTP,
	}, nil
}

// NewRequest creates a request from messages and configured defaults.
func (model *Model) NewRequest(messages ...gopact.Message) gopact.ModelRequest {
	if model == nil || model.chat == nil {
		return gopact.ModelRequest{}
	}
	return model.chat.NewRequest(messages...)
}

// Invoke calls GLM Coding Plan chat completions.
func (model *Model) Invoke(ctx context.Context, request gopact.ModelRequest, opts ...gopact.ModelCallOption) (gopact.ModelResponse, error) {
	if model == nil || model.chat == nil {
		return gopact.ModelResponse{}, errors.New("glm: model is nil")
	}
	return model.chat.Invoke(ctx, request, opts...)
}

// InvokeStream calls streaming GLM Coding Plan chat completions.
func (model *Model) InvokeStream(ctx context.Context, request gopact.ModelRequest, opts ...gopact.ModelCallOption) iter.Seq2[gopact.ModelOutputChunk, error] {
	if model == nil || model.chat == nil {
		return func(yield func(gopact.ModelOutputChunk, error) bool) {
			yield(gopact.ModelOutputChunk{}, errors.New("glm: model is nil"))
		}
	}
	return model.chat.InvokeStream(ctx, request, opts...)
}

// Embed creates embeddings through the GLM general API.
func (model *Model) Embed(ctx context.Context, request gopact.EmbeddingRequest) (gopact.EmbeddingResponse, error) {
	if model == nil || model.api == nil {
		return gopact.EmbeddingResponse{}, errors.New("glm: model is nil")
	}
	return model.api.Embed(ctx, request)
}

// ListModels returns models available through the GLM general API.
func (model *Model) ListModels(ctx context.Context) (gopact.ModelList, error) {
	if model == nil || model.api == nil {
		return gopact.ModelList{}, errors.New("glm: model is nil")
	}
	return model.api.ListModels(ctx)
}

// GetModel returns one model from the GLM general API catalog.
func (model *Model) GetModel(ctx context.Context, modelID string) (gopact.ModelInfo, error) {
	if model == nil || model.api == nil {
		return gopact.ModelInfo{}, errors.New("glm: model is nil")
	}
	return model.api.GetModel(ctx, modelID)
}

// Moderate classifies potentially harmful text or multimodal input.
func (model *Model) Moderate(ctx context.Context, request ModerationRequest) (ModerationResponse, error) {
	if request.Input == nil {
		return ModerationResponse{}, errors.New("glm: moderation input is required")
	}
	var response ModerationResponse
	err := model.runtimeJSON(ctx, http.MethodPost, "/moderations", request, &response)
	return response, err
}

// WithChatBaseURL overrides the Coding Plan chat API base URL.
func WithChatBaseURL(baseURL string) Option {
	return func(cfg *config) { cfg.chatBaseURL = baseURL }
}

// WithAPIBaseURL overrides the general GLM API base URL.
func WithAPIBaseURL(baseURL string) Option {
	return func(cfg *config) { cfg.apiBaseURL = baseURL }
}

// WithMonitorBaseURL overrides the Coding Plan usage API base URL.
func WithMonitorBaseURL(baseURL string) Option {
	return func(cfg *config) { cfg.monitorURL = baseURL }
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
		cfg.allowHTTP = true
		cfg.common = append(cfg.common, openai.WithInsecureHTTP())
	}
}

func normalizedBaseURL(baseURL string) string {
	if !strings.Contains(baseURL, "://") {
		baseURL = "https://" + baseURL
	}
	return strings.TrimRight(baseURL, "/")
}
