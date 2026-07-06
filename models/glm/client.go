// Package glm adapts GLM/Zhipu AI OpenAI-compatible chat completion APIs.
package glm

import (
	"net/http"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact-ext/models/openai"
)

const (
	DefaultProvider             = "glm"
	DefaultBaseURL              = "https://open.bigmodel.cn/api/paas/v4"
	DefaultInternationalBaseURL = "https://api.z.ai/api/coding/paas/v4"
	DefaultModel                = "glm-default"
)

func New(apiKey string, opts ...gopact.ModelRequestOption) (*openai.Client, error) {
	return NewClient(DefaultBaseURL, apiKey, opts...)
}

func NewInternational(apiKey string, opts ...gopact.ModelRequestOption) (*openai.Client, error) {
	return NewInternationalClient(DefaultInternationalBaseURL, apiKey, opts...)
}

func NewClient(baseURL, apiKey string, opts ...gopact.ModelRequestOption) (*openai.Client, error) {
	return newClient(baseURL, apiKey, DefaultBaseURL, opts...)
}

func NewInternationalClient(baseURL, apiKey string, opts ...gopact.ModelRequestOption) (*openai.Client, error) {
	return newClient(baseURL, apiKey, DefaultInternationalBaseURL, opts...)
}

func newClient(baseURL, apiKey, fallbackBaseURL string, opts ...gopact.ModelRequestOption) (*openai.Client, error) {
	if baseURL == "" {
		baseURL = fallbackBaseURL
	}
	defaults := []gopact.ModelRequestOption{
		openai.WithChatCompletionsAPI(),
		gopact.WithModel(DefaultModel),
	}
	return openai.NewClient(DefaultProvider, baseURL, apiKey, append(defaults, opts...)...)
}

func WithHTTPClient(client *http.Client) gopact.ModelRequestOption {
	return openai.WithHTTPClient(client)
}

func EnableThinking() gopact.ModelRequestOption {
	return openai.WithChatTemplateThinking(true)
}

func DisableThinking() gopact.ModelRequestOption {
	return openai.WithChatTemplateThinking(false)
}
