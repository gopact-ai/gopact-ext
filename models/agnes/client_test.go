package agnes

import (
	"context"
	"encoding/json"
	"iter"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact/provider"
)

func TestNewClientUsesAgnesDefaults(t *testing.T) {
	var got struct {
		Model        string         `json:"model"`
		ChatTemplate map[string]any `json:"chat_template_kwargs"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("path = %q, want /chat/completions", r.URL.Path)
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
		gopact.WithMessages(gopact.Message{Role: gopact.RoleUser, Content: "hi"}),
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
		t.Fatalf("models = %#v, want Agnes default model", models)
	}
}

func TestNewClientAllowsModelOverride(t *testing.T) {
	var got struct {
		Model string `json:"model"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		_, _ = w.Write([]byte(`{"choices": [{"message": {"role": "assistant", "content": "ok"}}]}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "token", gopact.WithModel("agnes-custom"))
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if _, err := client.Generate(context.Background(), gopact.NewModelRequest(
		gopact.WithMessages(gopact.Message{Role: gopact.RoleUser, Content: "hi"}),
	)); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if got.Model != "agnes-custom" {
		t.Fatalf("model = %q, want override", got.Model)
	}
}

func TestNewClientSupportsFullFeatureMock(t *testing.T) {
	schema := gopact.JSONSchema{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"answer"},
		"properties": map[string]any{
			"answer": map[string]any{"type": "string"},
		},
	}
	var generateRequest struct {
		Model          string         `json:"model"`
		MaxTokens      *int           `json:"max_tokens"`
		Temperature    *float64       `json:"temperature"`
		ChatTemplate   map[string]any `json:"chat_template_kwargs"`
		ResponseFormat *struct {
			Type       string `json:"type"`
			JSONSchema *struct {
				Name   string         `json:"name"`
				Schema map[string]any `json:"schema"`
				Strict bool           `json:"strict"`
			} `json:"json_schema"`
		} `json:"response_format"`
		Tools []struct {
			Type     string `json:"type"`
			Function struct {
				Name        string         `json:"name"`
				Description string         `json:"description"`
				Parameters  map[string]any `json:"parameters"`
			} `json:"function"`
		} `json:"tools"`
	}
	var streamRequest struct {
		Model        string         `json:"model"`
		MaxTokens    *int           `json:"max_tokens"`
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
		gopact.WithModel("agnes-mock"),
		gopact.WithMaxOutputTokens(1024),
		gopact.WithTemperature(0.2),
		DisableThinking(),
		gopact.EnableStreaming(),
	)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	response, err := client.Generate(context.Background(), gopact.NewModelRequest(
		gopact.WithMessages(gopact.UserMessage("use lookup")),
		gopact.WithTools(gopact.ObjectToolSpec("lookup", "Lookup docs.", gopact.RequiredStringField("q", "Query."))),
		gopact.WithResponseSchema(schema),
		gopact.WithMaxOutputTokens(2048),
		gopact.EnableStructuredOutput(),
	))
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if len(response.Message.ToolCalls) != 1 || response.Message.ToolCalls[0].Name != "lookup" {
		t.Fatalf("tool calls = %#v, want lookup call", response.Message.ToolCalls)
	}
	if generateRequest.Model != "agnes-mock" {
		t.Fatalf("generate model = %q, want agnes-mock", generateRequest.Model)
	}
	if generateRequest.MaxTokens == nil || *generateRequest.MaxTokens != 2048 {
		t.Fatalf("generate max_tokens = %#v, want 2048", generateRequest.MaxTokens)
	}
	if generateRequest.Temperature == nil || *generateRequest.Temperature != 0.2 {
		t.Fatalf("generate temperature = %#v, want 0.2", generateRequest.Temperature)
	}
	if generateRequest.ChatTemplate["enable_thinking"] != false {
		t.Fatalf("generate chat_template_kwargs = %#v, want thinking disabled", generateRequest.ChatTemplate)
	}
	if generateRequest.ResponseFormat == nil || generateRequest.ResponseFormat.JSONSchema == nil {
		t.Fatalf("generate response_format = %#v, want json schema", generateRequest.ResponseFormat)
	}
	if generateRequest.ResponseFormat.Type != "json_schema" ||
		generateRequest.ResponseFormat.JSONSchema.Schema["type"] != "object" ||
		!generateRequest.ResponseFormat.JSONSchema.Strict {
		t.Fatalf("generate response_format = %#v, want strict object schema", generateRequest.ResponseFormat)
	}
	if len(generateRequest.Tools) != 1 ||
		generateRequest.Tools[0].Function.Name != "lookup" ||
		generateRequest.Tools[0].Function.Parameters["type"] != "object" {
		t.Fatalf("generate tools = %#v, want lookup object tool", generateRequest.Tools)
	}

	events := collectStream(t, client.Stream(context.Background(), gopact.NewModelRequest(
		gopact.WithMessages(gopact.UserMessage("stream ok")),
		gopact.WithMaxOutputTokens(512),
		EnableThinking(),
	)))
	if len(events) != 1 || events[0].Message.Text() != "ok" {
		t.Fatalf("stream events = %#v, want ok", events)
	}
	if streamRequest.Model != "agnes-mock" || !streamRequest.Stream {
		t.Fatalf("stream request = %#v, want agnes-mock streaming", streamRequest)
	}
	if streamRequest.MaxTokens == nil || *streamRequest.MaxTokens != 512 {
		t.Fatalf("stream max_tokens = %#v, want 512", streamRequest.MaxTokens)
	}
	if streamRequest.ChatTemplate["enable_thinking"] != true {
		t.Fatalf("stream chat_template_kwargs = %#v, want thinking enabled", streamRequest.ChatTemplate)
	}
}

func TestNewClientAcceptsHTTPClientOption(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		time.Sleep(100 * time.Millisecond)
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "token", WithHTTPClient(&http.Client{Timeout: time.Nanosecond}))
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	_, err = client.Generate(context.Background(), gopact.NewModelRequest(
		gopact.WithMessages(gopact.UserMessage("hi")),
	))
	if provider.Classify(err) != provider.ErrorTimeout {
		t.Fatalf("Classify() = %q, want timeout; err = %v", provider.Classify(err), err)
	}
}

func TestNewClientClassifiesStatusErrors(t *testing.T) {
	tests := []struct {
		status int
		want   provider.ErrorClass
	}{
		{status: http.StatusUnauthorized, want: provider.ErrorUnauthorized},
		{status: http.StatusTooManyRequests, want: provider.ErrorRateLimited},
	}
	for _, tt := range tests {
		t.Run(string(tt.want), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, http.StatusText(tt.status), tt.status)
			}))
			defer server.Close()

			client, err := NewClient(server.URL, "token")
			if err != nil {
				t.Fatalf("NewClient() error = %v", err)
			}

			_, err = client.Generate(context.Background(), gopact.NewModelRequest(
				gopact.WithMessages(gopact.UserMessage("hi")),
			))
			if provider.Classify(err) != tt.want {
				t.Fatalf("Generate Classify() = %q, want %q; err = %v", provider.Classify(err), tt.want, err)
			}

			err = streamErr(client.Stream(context.Background(), gopact.NewModelRequest(
				gopact.WithMessages(gopact.UserMessage("hi")),
			)))
			if provider.Classify(err) != tt.want {
				t.Fatalf("Stream Classify() = %q, want %q; err = %v", provider.Classify(err), tt.want, err)
			}
		})
	}
}

func streamErr(stream iter.Seq2[gopact.Event, error]) error {
	for _, err := range stream {
		if err != nil {
			return err
		}
	}
	return nil
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
