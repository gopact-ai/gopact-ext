package ark

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact/gopacttest/providerconformance"
	"github.com/gopact-ai/gopact/provider"
)

func TestClientGeneratePostsArkChatCompletion(t *testing.T) {
	var gotAuth string
	var got struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		Tools []struct {
			Type     string `json:"type"`
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/chat/completions" {
			t.Fatalf("path = %q, want /api/v3/chat/completions", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		_, _ = w.Write([]byte(`{
			"choices": [{"message": {"role": "assistant", "content": "hello from ark"}}],
			"usage": {"prompt_tokens": 1, "completion_tokens": 2, "total_tokens": 3}
		}`))
	}))
	defer server.Close()

	client, err := New(Options{
		BaseURL: server.URL,
		APIKey:  "token",
		Models:  []provider.ModelInfo{{Name: "ep-test"}},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	response, err := client.Generate(context.Background(), gopact.ModelRequest{
		Model:    "ep-test",
		Messages: []gopact.Message{{Role: gopact.RoleUser, Content: "hi"}},
		Tools:    []gopact.ToolSpec{{Name: "search"}},
	})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if gotAuth != "Bearer token" {
		t.Fatalf("Authorization = %q, want bearer token", gotAuth)
	}
	if got.Model != "ep-test" || len(got.Messages) != 1 || got.Messages[0].Content != "hi" {
		t.Fatalf("request = %#v, want user message", got)
	}
	if len(got.Tools) != 1 || got.Tools[0].Function.Name != "search" {
		t.Fatalf("tools = %#v, want search tool", got.Tools)
	}
	if response.Message.Text() != "hello from ark" {
		t.Fatalf("Message.Text() = %q, want ark response", response.Message.Text())
	}
	if response.Usage.TotalTokens != 3 {
		t.Fatalf("Usage.TotalTokens = %d, want 3", response.Usage.TotalTokens)
	}
}

func TestClientRoundTripsToolCalls(t *testing.T) {
	var got struct {
		Messages []struct {
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

	client, err := New(Options{BaseURL: server.URL, APIKey: "token"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	response, err := client.Generate(context.Background(), gopact.ModelRequest{
		Model: "ep-test",
		Messages: []gopact.Message{{
			Role: gopact.RoleAssistant,
			ToolCalls: []gopact.ToolCall{{
				ID:        "call_1",
				Name:      "search",
				Arguments: []byte(`{"q":"gopact"}`),
			}},
		}},
	})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if len(got.Messages) != 1 || len(got.Messages[0].ToolCalls) != 1 {
		t.Fatalf("request tool calls = %#v, want one", got.Messages)
	}
	requestCall := got.Messages[0].ToolCalls[0]
	if requestCall.ID != "call_1" || requestCall.Type != "function" || requestCall.Function.Name != "search" || requestCall.Function.Arguments != `{"q":"gopact"}` {
		t.Fatalf("request tool call = %#v, want search call", requestCall)
	}
	if len(response.Message.ToolCalls) != 1 {
		t.Fatalf("response tool calls = %d, want 1", len(response.Message.ToolCalls))
	}
	responseCall := response.Message.ToolCalls[0]
	if responseCall.ID != "call_2" || responseCall.Name != "search" || string(responseCall.Arguments) != `{"q":"docs"}` {
		t.Fatalf("response tool call = %#v, want search call", responseCall)
	}
}

func TestClientGeneratePostsModelRequestOptions(t *testing.T) {
	var got struct {
		MaxTokens   *int     `json:"max_tokens"`
		Temperature *float32 `json:"temperature"`
		TopP        *float32 `json:"top_p"`
		Thinking    *struct {
			Type string `json:"type"`
		} `json:"thinking"`
		ReasoningEffort string `json:"reasoning_effort"`
		ResponseFormat  *struct {
			Type       string `json:"type"`
			JSONSchema *struct {
				Name   string         `json:"name"`
				Schema map[string]any `json:"schema"`
				Strict bool           `json:"strict"`
			} `json:"json_schema"`
		} `json:"response_format"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("Decode() error = %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{
			"choices": [{"message": {"role": "assistant", "content": "{\"answer\":\"ok\"}"}}]
		}`))
	}))
	defer server.Close()

	client, err := New(Options{BaseURL: server.URL, APIKey: "token"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	schema := gopact.JSONSchema{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"answer"},
		"properties": map[string]any{
			"answer": map[string]any{"type": "string"},
		},
	}

	_, err = client.Generate(context.Background(), gopact.NewModelRequest(
		gopact.WithModel("ep-test"),
		gopact.WithMessages(gopact.UserMessage("answer as json")),
		gopact.WithMaxOutputTokens(99),
		gopact.WithTemperature(0.2),
		gopact.WithTopP(0.9),
		gopact.WithThinkingType("disabled"),
		gopact.WithReasoningEffort("high"),
		gopact.WithResponseSchema(schema),
	))
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if got.MaxTokens == nil || *got.MaxTokens != 99 {
		t.Fatalf("max_tokens = %#v, want 99", got.MaxTokens)
	}
	if got.Temperature == nil || *got.Temperature != 0.2 || got.TopP == nil || *got.TopP != 0.9 {
		t.Fatalf("sampling params = temperature %#v top_p %#v, want 0.2/0.9", got.Temperature, got.TopP)
	}
	if got.Thinking == nil || got.Thinking.Type != "disabled" || got.ReasoningEffort != "high" {
		t.Fatalf("thinking/reasoning = %#v/%q, want disabled/high", got.Thinking, got.ReasoningEffort)
	}
	if got.ResponseFormat == nil || got.ResponseFormat.JSONSchema == nil {
		t.Fatalf("response_format = %#v, want json schema", got.ResponseFormat)
	}
	if got.ResponseFormat.Type != "json_schema" || got.ResponseFormat.JSONSchema.Name != "gopact_response" ||
		!got.ResponseFormat.JSONSchema.Strict || got.ResponseFormat.JSONSchema.Schema["type"] != "object" {
		t.Fatalf("response_format = %#v, want strict object json_schema", got.ResponseFormat)
	}
}

func TestClientStreamUsesArkStreamingAPI(t *testing.T) {
	var got struct {
		Stream *bool `json:"stream"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("Decode() error = %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"hel\"}}]}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"lo\"}}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":2,\"total_tokens\":3}}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	client, err := New(Options{BaseURL: server.URL, APIKey: "token"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	events, err := collectEvents(client.Stream(context.Background(), gopact.ModelRequest{
		IDs:      gopact.RuntimeIDs{RunID: "run-stream"},
		Model:    "ep-test",
		Messages: []gopact.Message{gopact.UserMessage("hi")},
	}))
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if got.Stream == nil || !*got.Stream {
		t.Fatalf("stream request = %#v, want stream true", got.Stream)
	}
	if len(events) != 1 || events[0].Type != gopact.EventModelMessage || events[0].Message == nil {
		t.Fatalf("events = %+v, want one model message", events)
	}
	if events[0].IDs.RunID != "run-stream" || events[0].Message.Text() != "hello" {
		t.Fatalf("event = %+v, want run id and hello", events[0])
	}
	if events[0].Usage == nil || events[0].Usage.TotalTokens != 3 {
		t.Fatalf("usage = %+v, want total tokens", events[0].Usage)
	}
}

func TestClientStreamAggregatesToolCallDeltas(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, `data: {"choices":[{"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"search","arguments":"{\"q\":\""}}]}}]}`+"\n\n")
		_, _ = fmt.Fprint(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"docs\"}"}}]}}]}`+"\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	client, err := New(Options{BaseURL: server.URL, APIKey: "token"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	events, err := collectEvents(client.Stream(context.Background(), gopact.ModelRequest{
		Model:    "ep-test",
		Messages: []gopact.Message{gopact.UserMessage("use search")},
	}))
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}
	if len(events) != 1 || events[0].Message == nil || len(events[0].Message.ToolCalls) != 1 {
		t.Fatalf("events = %+v, want one tool call message", events)
	}
	call := events[0].Message.ToolCalls[0]
	if call.ID != "call_1" || call.Name != "search" || string(call.Arguments) != `{"q":"docs"}` {
		t.Fatalf("tool call = %+v args %q, want aggregated search call", call, call.Arguments)
	}
}

func TestNewSupportsAkSkAndDefaults(t *testing.T) {
	if _, err := New(Options{AccessKey: "ak", SecretKey: "sk"}); err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if got, err := normalizeBaseURL("https://ark.cn-beijing.volces.com"); err != nil || got != DefaultBaseURL {
		t.Fatalf("normalizeBaseURL() = %q, %v; want %q", got, err, DefaultBaseURL)
	}
}

func TestNewRejectsMissingCredentials(t *testing.T) {
	if _, err := New(Options{}); err == nil {
		t.Fatal("New() error = nil, want credentials error")
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
		BaseURL: server.URL,
		APIKey:  "token",
		Models:  []provider.ModelInfo{{Name: "ep-test", Provider: DefaultProvider}},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	providerconformance.RequireProviderConformance(t, providerconformance.ProviderConformanceHarness{
		Provider: client,
		Request: gopact.ModelRequest{
			Model:    "ep-test",
			Messages: []gopact.Message{{Role: gopact.RoleUser, Content: "hi"}},
		},
	})
}

func collectEvents(seq iter.Seq2[gopact.Event, error]) ([]gopact.Event, error) {
	var events []gopact.Event
	for event, err := range seq {
		if err != nil {
			return events, err
		}
		events = append(events, event)
	}
	return events, nil
}
