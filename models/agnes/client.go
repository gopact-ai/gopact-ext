// Package agnes adapts Agnes AI's OpenAI-compatible text model API.
package agnes

import (
	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact-ext/models/openai"
)

const (
	DefaultProvider = "agnes"
	DefaultBaseURL  = "https://apihub.agnes-ai.com/v1"
	DefaultModel    = "agnes-2.0-flash"
)

func New(apiKey string, opts ...gopact.ModelRequestOption) (*openai.Client, error) {
	return NewClient(DefaultBaseURL, apiKey, opts...)
}

func NewClient(baseURL, apiKey string, opts ...gopact.ModelRequestOption) (*openai.Client, error) {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	defaults := []gopact.ModelRequestOption{
		openai.WithChatCompletionsAPI(),
		gopact.WithModel(DefaultModel),
	}
	return openai.NewClient(DefaultProvider, baseURL, apiKey, append(defaults, opts...)...)
}

func EnableThinking() gopact.ModelRequestOption {
	return openai.WithChatTemplateThinking(true)
}

func DisableThinking() gopact.ModelRequestOption {
	return openai.WithChatTemplateThinking(false)
}
