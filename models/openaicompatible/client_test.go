package openaicompatible

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact/gopacttest/providerconformance"
	"github.com/gopact-ai/gopact/provider"
)

func TestClientGeneratePostsChatCompletion(t *testing.T) {
	var gotAuth string
	var gotModel string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("path = %q, want /chat/completions", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		gotModel = body.Model

		_, _ = w.Write([]byte(`{
			"choices": [{"message": {"role": "assistant", "content": "hello"}}],
			"usage": {"prompt_tokens": 1, "completion_tokens": 2, "total_tokens": 3}
		}`))
	}))
	defer server.Close()

	client, err := New(Options{
		Provider: "openrouter",
		BaseURL:  server.URL,
		APIKey:   "token",
		Models:   []provider.ModelInfo{{Name: "test-model"}},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	response, err := client.Generate(context.Background(), gopact.ModelRequest{
		Model:    "test-model",
		Messages: []gopact.Message{{Role: gopact.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if gotAuth != "Bearer token" {
		t.Fatalf("Authorization = %q, want bearer token", gotAuth)
	}
	if gotModel != "test-model" {
		t.Fatalf("model = %q, want test-model", gotModel)
	}
	if response.Message.Text() != "hello" {
		t.Fatalf("Message.Text() = %q, want hello", response.Message.Text())
	}
	if response.Usage.TotalTokens != 3 {
		t.Fatalf("Usage.TotalTokens = %d, want 3", response.Usage.TotalTokens)
	}
}

func TestClientGenerateRoundTripsToolCalls(t *testing.T) {
	var got struct {
		Messages []struct {
			Role      string `json:"role"`
			ToolCalls []struct {
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"messages"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("Decode() error = %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{
			"choices": [{
				"message": {
					"role": "assistant",
					"tool_calls": [{
						"id": "call_2",
						"type": "function",
						"function": {"name": "search", "arguments": "{\"q\":\"docs\"}"}
					}]
				}
			}]
		}`))
	}))
	defer server.Close()

	client, err := New(Options{
		Provider: "openrouter",
		BaseURL:  server.URL,
		APIKey:   "token",
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	response, err := client.Generate(context.Background(), gopact.ModelRequest{
		Model: "test-model",
		Messages: []gopact.Message{
			{
				Role: gopact.RoleAssistant,
				ToolCalls: []gopact.ToolCall{{
					ID:        "call_1",
					Name:      "search",
					Arguments: []byte(`{"q":"gopact"}`),
				}},
			},
		},
	})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if len(got.Messages) != 1 || len(got.Messages[0].ToolCalls) != 1 {
		t.Fatalf("request tool calls = %#v, want one tool call", got.Messages)
	}
	requestCall := got.Messages[0].ToolCalls[0]
	if requestCall.ID != "call_1" || requestCall.Type != "function" || requestCall.Function.Name != "search" || requestCall.Function.Arguments != `{"q":"gopact"}` {
		t.Fatalf("request tool call = %#v, want search call with raw arguments", requestCall)
	}
	if len(response.Message.ToolCalls) != 1 {
		t.Fatalf("response tool calls = %d, want 1", len(response.Message.ToolCalls))
	}
	responseCall := response.Message.ToolCalls[0]
	if responseCall.ID != "call_2" || responseCall.Name != "search" || string(responseCall.Arguments) != `{"q":"docs"}` {
		t.Fatalf("response tool call = %#v, want search call with raw arguments", responseCall)
	}
}

func TestClientRejectsInvalidOptions(t *testing.T) {
	tests := []struct {
		name string
		opts Options
	}{
		{name: "missing provider", opts: Options{BaseURL: "https://example.com", APIKey: "token"}},
		{name: "missing base url", opts: Options{Provider: "openai", APIKey: "token"}},
		{name: "missing api key", opts: Options{Provider: "openai", BaseURL: "https://example.com"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := New(tt.opts); err == nil {
				t.Fatal("New() error = nil, want validation error")
			}
		})
	}
}

func TestClientGenerateReturnsProviderErrorForNonOKResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	defer server.Close()

	client, err := New(Options{
		Provider: "openrouter",
		BaseURL:  server.URL,
		APIKey:   "token",
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = client.Generate(context.Background(), gopact.ModelRequest{Model: "test-model"})
	if provider.Classify(err) != provider.ErrorRateLimited {
		t.Fatalf("Classify() = %q, want rate_limited; err = %v", provider.Classify(err), err)
	}
}

func TestClientProviderConformance(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
			"choices": [{"message": {"role": "assistant", "content": "hello"}}]
		}`))
	}))
	defer server.Close()

	client, err := New(Options{
		Provider: "openrouter",
		BaseURL:  server.URL,
		APIKey:   "token",
		Models:   []provider.ModelInfo{{Name: "test-model", Provider: "openrouter"}},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	providerconformance.RequireProviderConformance(t, providerconformance.ProviderConformanceHarness{
		Provider: client,
		Request: gopact.ModelRequest{
			Model:    "test-model",
			Messages: []gopact.Message{{Role: gopact.RoleUser, Content: "hi"}},
		},
	})
}
