package glm

import (
	"context"
	"encoding/json"
	"iter"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact/gopacttest/providerconformance"
)

func TestNewClientUsesGLMChinaDefaults(t *testing.T) {
	var got struct {
		Model        string         `json:"model"`
		ChatTemplate map[string]any `json:"chat_template_kwargs"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("path = %q, want /chat/completions", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer token" {
			t.Fatalf("Authorization = %q, want bearer token", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		_, _ = w.Write([]byte(`{"choices": [{"message": {"role": "assistant", "content": "hello"}}]}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "token", EnableThinking())
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	response, err := client.Generate(context.Background(), gopact.NewModelRequest(
		gopact.WithMessages(gopact.UserMessage("hi")),
	))
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if response.Message.Text() != "hello" {
		t.Fatalf("Message.Text() = %q, want hello", response.Message.Text())
	}
	if got.Model != DefaultModel {
		t.Fatalf("model = %q, want %q", got.Model, DefaultModel)
	}
	if got.ChatTemplate["enable_thinking"] != true {
		t.Fatalf("chat_template_kwargs = %#v, want enable_thinking true", got.ChatTemplate)
	}

	models, err := client.Models(context.Background())
	if err != nil {
		t.Fatalf("Models() error = %v", err)
	}
	if len(models) != 1 || models[0].Provider != DefaultProvider || models[0].Name != DefaultModel {
		t.Fatalf("models = %#v, want GLM default model", models)
	}
}

func TestNewClientDefaultsToChinaBaseURL(t *testing.T) {
	client, err := NewClient("", "token")
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	models, err := client.Models(context.Background())
	if err != nil {
		t.Fatalf("Models() error = %v", err)
	}
	if len(models) != 1 || models[0].Provider != DefaultProvider || models[0].Name != DefaultModel {
		t.Fatalf("models = %#v, want GLM defaults", models)
	}
}

func TestNewInternationalClientUsesZAIBaseURL(t *testing.T) {
	var got struct {
		Model string `json:"model"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("path = %q, want /chat/completions", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		_, _ = w.Write([]byte(`{"choices": [{"message": {"role": "assistant", "content": "ok"}}]}`))
	}))
	defer server.Close()

	client, err := NewInternationalClient(server.URL, "token", gopact.WithModel("glm-custom"))
	if err != nil {
		t.Fatalf("NewInternationalClient() error = %v", err)
	}
	if _, err := client.Generate(context.Background(), gopact.NewModelRequest(
		gopact.WithMessages(gopact.UserMessage("hi")),
	)); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if got.Model != "glm-custom" {
		t.Fatalf("model = %q, want override", got.Model)
	}
}

func TestNewClientSupportsFullFeatureMock(t *testing.T) {
	var generateRequest struct {
		Model        string         `json:"model"`
		ChatTemplate map[string]any `json:"chat_template_kwargs"`
		Tools        []struct {
			Type     string `json:"type"`
			Function struct {
				Name       string         `json:"name"`
				Parameters map[string]any `json:"parameters"`
			} `json:"function"`
		} `json:"tools"`
	}
	var streamRequest struct {
		Model        string         `json:"model"`
		Stream       bool           `json:"stream"`
		ChatTemplate map[string]any `json:"chat_template_kwargs"`
	}
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("path = %q, want /chat/completions", r.URL.Path)
		}
		calls++
		switch calls {
		case 1:
			if err := json.NewDecoder(r.Body).Decode(&generateRequest); err != nil {
				t.Fatalf("Decode(generate) error = %v", err)
			}
			_, _ = w.Write([]byte(`{
				"choices": [{
					"message": {
						"role": "assistant",
						"tool_calls": [{
							"id": "call_lookup",
							"type": "function",
							"function": {"name": "lookup", "arguments": "{\"q\":\"gopact\"}"}
						}]
					}
				}]
			}`))
		case 2:
			if err := json.NewDecoder(r.Body).Decode(&streamRequest); err != nil {
				t.Fatalf("Decode(stream) error = %v", err)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"ok\"}}]}\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
		default:
			t.Fatalf("unexpected request %d", calls)
		}
	}))
	defer server.Close()

	client, err := NewClient(
		server.URL,
		"token",
		gopact.WithModel("glm-mock"),
		gopact.EnableStreaming(),
		DisableThinking(),
	)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	response, err := client.Generate(context.Background(), gopact.NewModelRequest(
		gopact.WithMessages(gopact.UserMessage("use lookup")),
		gopact.WithTools(gopact.ObjectToolSpec("lookup", "Lookup docs.", gopact.RequiredStringField("q", "Query."))),
		gopact.WithAutoToolChoice(),
	))
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if len(response.Message.ToolCalls) != 1 || response.Message.ToolCalls[0].Name != "lookup" {
		t.Fatalf("tool calls = %#v, want lookup call", response.Message.ToolCalls)
	}
	if generateRequest.Model != "glm-mock" {
		t.Fatalf("generate model = %q, want glm-mock", generateRequest.Model)
	}
	if generateRequest.ChatTemplate["enable_thinking"] != false {
		t.Fatalf("generate chat_template_kwargs = %#v, want thinking disabled", generateRequest.ChatTemplate)
	}
	if len(generateRequest.Tools) != 1 ||
		generateRequest.Tools[0].Function.Name != "lookup" ||
		generateRequest.Tools[0].Function.Parameters["type"] != "object" {
		t.Fatalf("generate tools = %#v, want lookup object tool", generateRequest.Tools)
	}

	events := collectStream(t, client.Stream(context.Background(), gopact.NewModelRequest(
		gopact.WithMessages(gopact.UserMessage("stream ok")),
		EnableThinking(),
	)))
	if len(events) != 1 || events[0].Message.Text() != "ok" {
		t.Fatalf("stream events = %#v, want ok", events)
	}
	if streamRequest.Model != "glm-mock" || !streamRequest.Stream {
		t.Fatalf("stream request = %#v, want glm-mock streaming", streamRequest)
	}
	if streamRequest.ChatTemplate["enable_thinking"] != true {
		t.Fatalf("stream chat_template_kwargs = %#v, want thinking enabled", streamRequest.ChatTemplate)
	}
}

func TestNewClientProviderConformance(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("path = %q, want /chat/completions", r.URL.Path)
		}
		var body struct {
			Stream bool `json:"stream"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("Decode() error = %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if body.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"hello\"}}]}\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
			return
		}
		_, _ = w.Write([]byte(`{
			"choices": [{"message": {"role": "assistant", "content": "hello"}}]
		}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "token", gopact.WithModel("glm-mock"))
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	providerconformance.RequireProviderConformance(t, providerconformance.ProviderConformanceHarness{
		Provider: client,
		Request: gopact.ModelRequest{
			Model:    "glm-mock",
			Messages: []gopact.Message{gopact.UserMessage("hi")},
		},
	})
}

func collectStream(t *testing.T, stream iter.Seq2[gopact.Event, error]) []gopact.Event {
	t.Helper()
	var events []gopact.Event
	for event, err := range stream {
		if err != nil {
			t.Fatalf("Stream() error = %v", err)
		}
		events = append(events, event)
	}
	return events
}
