// Package agnes adapts Agnes AI's OpenAI-compatible model API.
package agnes

import (
	"net/http"
	"time"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact-ext/models/openai"
)

const (
	DefaultProvider = "agnes"
	DefaultBaseURL  = "https://apihub.agnes-ai.com/v1"
	DefaultModel    = "agnes-2.0-flash"
)

// Option configures a Model.
type Option = openai.Option

// Model is an Agnes model adapter.
type Model = openai.Model

// RetryPolicy configures bounded provider retries.
type RetryPolicy = openai.RetryPolicy

// New creates an Agnes model adapter.
func New(apiKey string, opts ...Option) (*Model, error) {
	return openai.New(DefaultProvider, DefaultBaseURL, apiKey, DefaultModel, opts...)
}

// WithHTTPClient sets the HTTP client.
func WithHTTPClient(client *http.Client) Option {
	return openai.WithHTTPClient(client)
}

// WithDefaultRequest sets the request template used by NewRequest.
func WithDefaultRequest(req gopact.ModelRequest) Option {
	return openai.WithDefaultRequest(req)
}

// WithMaxAttempts sets the maximum attempts for retryable calls.
func WithMaxAttempts(n int) Option {
	return openai.WithMaxAttempts(n)
}

// WithRetryPolicy sets bounded retry attempts and backoff.
func WithRetryPolicy(policy RetryPolicy) Option {
	return openai.WithRetryPolicy(policy)
}

// WithTimeout bounds each provider call.
func WithTimeout(timeout time.Duration) Option {
	return openai.WithTimeout(timeout)
}
