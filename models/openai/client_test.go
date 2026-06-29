package openai

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

func TestClientGeneratePostsResponses(t *testing.T) {
	var got struct {
		Model string `json:"model"`
		Input []struct {
			Type    string `json:"type"`
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"input"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("path = %q, want /responses", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		_, _ = w.Write([]byte(`{
			"output": [
				{"type": "reasoning", "summary": [{"type": "summary_text", "text": "thought briefly"}]},
				{"type": "message", "role": "assistant", "content": [{"type": "output_text", "text": "hello from responses"}]}
			],
			"usage": {"input_tokens": 1, "output_tokens": 2, "total_tokens": 3}
		}`))
	}))
	defer server.Close()

	client, err := New(Options{
		Provider: "openai",
		BaseURL:  server.URL,
		APIKey:   "token",
		API:      APIResponses,
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
	if got.Model != "test-model" || len(got.Input) != 1 || got.Input[0].Type != "message" || got.Input[0].Role != "user" {
		t.Fatalf("request = %#v, want one user message", got)
	}
	if len(got.Input[0].Content) != 1 || got.Input[0].Content[0].Type != "input_text" || got.Input[0].Content[0].Text != "hi" {
		t.Fatalf("content = %#v, want input_text hi", got.Input[0].Content)
	}
	if response.Message.Text() != "hello from responses" {
		t.Fatalf("Message.Text() = %q, want responses text", response.Message.Text())
	}
	if len(response.Message.Parts) != 2 || response.Message.Parts[0].Type != gopact.ContentPartReasoning || response.Message.Parts[0].Text != "thought briefly" {
		t.Fatalf("Message.Parts = %#v, want reasoning then text", response.Message.Parts)
	}
	if response.Usage.TotalTokens != 3 {
		t.Fatalf("Usage.TotalTokens = %d, want 3", response.Usage.TotalTokens)
	}
}

func TestClientGeneratePostsParameters(t *testing.T) {
	temp := 0.2
	topP := 0.9
	tests := []struct {
		name string
		api  API
		path string
	}{
		{name: "chat completions", api: APIChatCompletions, path: "/chat/completions"},
		{name: "responses", api: APIResponses, path: "/responses"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got struct {
				MaxTokens       *int     `json:"max_tokens"`
				MaxOutputTokens *int     `json:"max_output_tokens"`
				Temperature     *float64 `json:"temperature"`
				TopP            *float64 `json:"top_p"`
				Thinking        *struct {
					Type string `json:"type"`
				} `json:"thinking"`
				ReasoningEffort string `json:"reasoning_effort"`
				Reasoning       *struct {
					Effort string `json:"effort"`
				} `json:"reasoning"`
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != tt.path {
					t.Fatalf("path = %q, want %s", r.URL.Path, tt.path)
				}
				if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
					t.Fatalf("Decode() error = %v", err)
				}
				if tt.api == APIResponses {
					_, _ = w.Write([]byte(`{"output_text": "ok"}`))
					return
				}
				_, _ = w.Write([]byte(`{"choices": [{"message": {"role": "assistant", "content": "ok"}}]}`))
			}))
			defer server.Close()

			client, err := New(Options{
				Provider:        "openai",
				BaseURL:         server.URL,
				APIKey:          "token",
				API:             tt.api,
				MaxOutputTokens: 99,
				Temperature:     &temp,
				TopP:            &topP,
				ThinkingType:    "enabled",
				ReasoningEffort: "high",
			})
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}

			_, err = client.Generate(context.Background(), gopact.ModelRequest{
				Model:    "test-model",
				Messages: []gopact.Message{{Role: gopact.RoleUser, Content: "hi"}},
				Budget:   gopact.Budget{MaxOutputTokens: 7},
			})
			if err != nil {
				t.Fatalf("Generate() error = %v", err)
			}
			if got.Temperature == nil || *got.Temperature != temp || got.TopP == nil || *got.TopP != topP {
				t.Fatalf("sampling params = temperature %#v top_p %#v, want %v/%v", got.Temperature, got.TopP, temp, topP)
			}
			if got.Thinking == nil || got.Thinking.Type != "enabled" {
				t.Fatalf("thinking = %#v, want enabled", got.Thinking)
			}
			if tt.api == APIResponses {
				if got.MaxOutputTokens == nil || *got.MaxOutputTokens != 7 || got.Reasoning == nil || got.Reasoning.Effort != "high" {
					t.Fatalf("responses params = %#v reasoning %#v, want max_output_tokens 7 and high", got.MaxOutputTokens, got.Reasoning)
				}
				return
			}
			if got.MaxTokens == nil || *got.MaxTokens != 7 || got.ReasoningEffort != "high" {
				t.Fatalf("chat params = %#v reasoning_effort %q, want max_tokens 7 and high", got.MaxTokens, got.ReasoningEffort)
			}
		})
	}
}

func TestNewClientAppliesFeatureOptions(t *testing.T) {
	var got struct {
		MaxOutputTokens *int     `json:"max_output_tokens"`
		Temperature     *float64 `json:"temperature"`
		TopP            *float64 `json:"top_p"`
		Thinking        *struct {
			Type string `json:"type"`
		} `json:"thinking"`
		Reasoning *struct {
			Effort string `json:"effort"`
		} `json:"reasoning"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("path = %q, want /responses", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		_, _ = w.Write([]byte(`{"output_text": "ok"}`))
	}))
	defer server.Close()

	client, err := NewClient(
		ProviderArk,
		server.URL,
		"token",
		WithResponsesAPI(),
		WithMaxOutputTokens(9),
		WithTemperature(0.2),
		WithTopP(0.8),
		WithThinkingType("enabled"),
		WithReasoningEffort("high"),
		WithModels(ProviderModel(ProviderArk, "ep-test", CapabilityToolCalling, CapabilityReasoning)),
	)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	_, err = client.Generate(context.Background(), gopact.ModelRequest{
		Model:    "ep-test",
		Messages: []gopact.Message{{Role: gopact.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if got.MaxOutputTokens == nil || *got.MaxOutputTokens != 9 {
		t.Fatalf("MaxOutputTokens = %#v, want 9", got.MaxOutputTokens)
	}
	if got.Temperature == nil || *got.Temperature != 0.2 || got.TopP == nil || *got.TopP != 0.8 {
		t.Fatalf("sampling params = %#v/%#v, want 0.2/0.8", got.Temperature, got.TopP)
	}
	if got.Thinking == nil || got.Thinking.Type != "enabled" {
		t.Fatalf("thinking = %#v, want enabled", got.Thinking)
	}
	if got.Reasoning == nil || got.Reasoning.Effort != "high" {
		t.Fatalf("reasoning = %#v, want high", got.Reasoning)
	}
	models, err := client.Models(context.Background())
	if err != nil {
		t.Fatalf("Models() error = %v", err)
	}
	if len(models) != 1 || models[0].Provider != ProviderArk || len(models[0].Capabilities) != 2 {
		t.Fatalf("models = %#v, want ark model with capabilities", models)
	}
}

func TestClientGenerateResponsesPostsImagePart(t *testing.T) {
	var got struct {
		Input []struct {
			Content []struct {
				Type     string `json:"type"`
				Text     string `json:"text"`
				ImageURL string `json:"image_url"`
			} `json:"content"`
		} `json:"input"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		_, _ = w.Write([]byte(`{"output_text": "saw it"}`))
	}))
	defer server.Close()

	client, err := New(Options{
		Provider: "openai",
		BaseURL:  server.URL,
		APIKey:   "token",
		API:      APIResponses,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = client.Generate(context.Background(), gopact.ModelRequest{
		Model: "test-model",
		Messages: []gopact.Message{{
			Role: gopact.RoleUser,
			Parts: []gopact.ContentPart{
				gopact.ImagePart("https://example.test/image.png", "image/png"),
				gopact.TextPart("what is this?"),
			},
		}},
	})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if len(got.Input) != 1 || len(got.Input[0].Content) != 2 {
		t.Fatalf("input = %#v, want two content parts", got.Input)
	}
	if got.Input[0].Content[0].Type != "input_image" || got.Input[0].Content[0].ImageURL != "https://example.test/image.png" {
		t.Fatalf("image part = %#v, want input_image", got.Input[0].Content[0])
	}
	if got.Input[0].Content[1].Type != "input_text" || got.Input[0].Content[1].Text != "what is this?" {
		t.Fatalf("text part = %#v, want input_text", got.Input[0].Content[1])
	}
}

func TestClientStreamChatCompletions(t *testing.T) {
	var got struct {
		Stream        bool `json:"stream"`
		StreamOptions struct {
			IncludeUsage bool `json:"include_usage"`
		} `json:"stream_options"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("path = %q, want /chat/completions", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"hel\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"lo\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"search\",\"arguments\":\"{\\\"q\\\":\\\"\"}}]}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"docs\\\"}\"}}]}}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":2,\"total_tokens\":3}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	client, err := New(Options{Provider: "openai", BaseURL: server.URL, APIKey: "token"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	events := collectStream(t, client, gopact.ModelRequest{Model: "test-model"})
	if !got.Stream || !got.StreamOptions.IncludeUsage {
		t.Fatalf("stream request = %#v, want stream with usage", got)
	}
	if len(events) != 3 {
		t.Fatalf("events = %d, want 3", len(events))
	}
	if events[0].Message.Text() != "hel" || events[1].Message.Text() != "lo" {
		t.Fatalf("stream text = %q %q, want hel lo", events[0].Message.Text(), events[1].Message.Text())
	}
	if len(events[2].Message.ToolCalls) != 1 || string(events[2].Message.ToolCalls[0].Arguments) != `{"q":"docs"}` {
		t.Fatalf("tool event = %#v, want search call", events[2].Message)
	}
	if events[2].Usage == nil || events[2].Usage.TotalTokens != 3 {
		t.Fatalf("usage = %#v, want total 3", events[2].Usage)
	}
}

func TestClientStreamResponses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("path = %q, want /responses", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"think\"}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_item.added\",\"output_index\":1,\"item\":{\"type\":\"function_call\",\"call_id\":\"call_1\",\"name\":\"search\"}}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.function_call_arguments.delta\",\"output_index\":1,\"delta\":\"{\\\"q\\\":\\\"docs\\\"}\"}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":1,\"output_tokens\":2,\"total_tokens\":3}}}\n\n"))
	}))
	defer server.Close()

	client, err := New(Options{Provider: "openai", BaseURL: server.URL, APIKey: "token", API: APIResponses})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	events := collectStream(t, client, gopact.ModelRequest{Model: "test-model"})
	if len(events) != 3 {
		t.Fatalf("events = %d, want 3", len(events))
	}
	if len(events[0].Message.Parts) != 1 || events[0].Message.Parts[0].Type != gopact.ContentPartReasoning || events[0].Message.Parts[0].Text != "think" {
		t.Fatalf("reasoning event = %#v, want reasoning delta", events[0].Message)
	}
	if events[1].Message.Text() != "hi" {
		t.Fatalf("text event = %q, want hi", events[1].Message.Text())
	}
	if len(events[2].Message.ToolCalls) != 1 || events[2].Message.ToolCalls[0].ID != "call_1" || string(events[2].Message.ToolCalls[0].Arguments) != `{"q":"docs"}` {
		t.Fatalf("tool event = %#v, want search call", events[2].Message)
	}
	if events[2].Usage == nil || events[2].Usage.TotalTokens != 3 {
		t.Fatalf("usage = %#v, want total 3", events[2].Usage)
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

func collectStream(t *testing.T, client *Client, req gopact.ModelRequest) []gopact.Event {
	t.Helper()
	var events []gopact.Event
	for event, err := range client.Stream(context.Background(), req) {
		if err != nil {
			t.Fatalf("Stream() error = %v", err)
		}
		events = append(events, event)
	}
	return events
}

func TestClientGenerateResponsesRoundTripsToolCalls(t *testing.T) {
	var got struct {
		Input []struct {
			Type      string `json:"type"`
			CallID    string `json:"call_id"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
			Output    string `json:"output"`
		} `json:"input"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("Decode() error = %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{
			"output": [{
				"type": "function_call",
				"call_id": "call_2",
				"name": "search",
				"arguments": "{\"q\":\"docs\"}"
			}]
		}`))
	}))
	defer server.Close()

	client, err := New(Options{
		Provider: "openai",
		BaseURL:  server.URL,
		APIKey:   "token",
		API:      APIResponses,
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
			{Role: gopact.RoleTool, ToolCallID: "call_1", Content: "GOPACT"},
		},
	})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if len(got.Input) != 2 || got.Input[0].Type != "function_call" || got.Input[1].Type != "function_call_output" {
		t.Fatalf("input = %#v, want function call and output", got.Input)
	}
	if got.Input[0].CallID != "call_1" || got.Input[0].Name != "search" || got.Input[0].Arguments != `{"q":"gopact"}` {
		t.Fatalf("request function call = %#v, want search call", got.Input[0])
	}
	if got.Input[1].CallID != "call_1" || got.Input[1].Output != "GOPACT" {
		t.Fatalf("request function output = %#v, want GOPACT output", got.Input[1])
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
		{name: "unsupported api", opts: Options{Provider: "openai", BaseURL: "https://example.com", APIKey: "token", API: "legacy_completions"}},
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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
