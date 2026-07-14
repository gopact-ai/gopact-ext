package openai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gopact-ai/gopact"
)

func TestNewRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name string
		new  func() (*Model, error)
	}{
		{name: "provider", new: func() (*Model, error) { return New("", "https://example.com", "key", "model") }},
		{name: "base url", new: func() (*Model, error) { return New("test", "://bad", "key", "model") }},
		{name: "api key", new: func() (*Model, error) { return New("test", "https://example.com", "", "model") }},
		{name: "model", new: func() (*Model, error) { return New("test", "https://example.com", "key", "") }},
		{name: "nil http client", new: func() (*Model, error) { return New("test", "https://example.com", "key", "model", WithHTTPClient(nil)) }},
		{name: "attempts", new: func() (*Model, error) { return New("test", "https://example.com", "key", "model", WithMaxAttempts(0)) }},
		{name: "retry policy", new: func() (*Model, error) {
			return New("test", "https://example.com", "key", "model", WithRetryPolicy(RetryPolicy{MaxAttempts: 2, InitialBackoff: time.Second, MaxBackoff: time.Millisecond}))
		}},
		{name: "timeout", new: func() (*Model, error) { return New("test", "https://example.com", "key", "model", WithTimeout(0)) }},
		{name: "default extension", new: func() (*Model, error) {
			return New("test", "https://example.com", "key", "model", WithDefaultRequest(gopact.ModelRequest{Extensions: map[string]any{"unknown": true}}))
		}},
		{name: "default tool schema", new: func() (*Model, error) {
			return New("test", "https://example.com", "key", "model", WithDefaultRequest(gopact.ModelRequest{Tools: []gopact.ToolSpec{{Name: "bad", Schema: []byte(`{"type":`)}}}))
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := tt.new(); err == nil {
				t.Fatal("New() error = nil, want configuration error")
			}
		})
	}
}

func TestNewDefaultsToHTTPSAndRequiresExplicitHTTPOptIn(t *testing.T) {
	model, err := New("test", "example.com/v1", "key", "model")
	if err != nil {
		t.Fatalf("New(default HTTPS) error = %v", err)
	}
	if model.baseURL != "https://example.com/v1" {
		t.Fatalf("base URL = %q, want HTTPS default", model.baseURL)
	}
	if _, err := New("test", "http://example.com/v1", "key", "model"); err == nil {
		t.Fatal("New(explicit HTTP) error = nil, want insecure transport rejection")
	}
	if _, err := New("test", "http://example.com/v1", "key", "model", WithInsecureHTTP()); err != nil {
		t.Fatalf("New(WithInsecureHTTP) error = %v", err)
	}
}

func TestModelInvoke(t *testing.T) {
	var got chatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("authorization = %q, want bearer token", r.Header.Get("Authorization"))
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_, _ = w.Write([]byte(`{"id":"resp-1","model":"test-model","choices":[{"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`))
	}))
	defer server.Close()

	model, err := New("test", server.URL, "test-key", "test-model", WithInsecureHTTP())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	req := model.NewRequest(gopact.UserMessage("ping"))
	resp, err := model.Invoke(context.Background(), req)
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if got.Model != "test-model" || got.Messages[0].Content != "ping" {
		t.Fatalf("request = %+v, want model and message", got)
	}
	if got.MaxTokens != 0 {
		t.Fatalf("max tokens = %d, want omitted zero value", got.MaxTokens)
	}
	if resp.Message.Parts[0].Text != "pong" || resp.FinishReason != "stop" {
		t.Fatalf("response = %+v, want pong stop", resp)
	}
	if resp.Usage.InputTokens != 1 || resp.Usage.OutputTokens != 2 || resp.Usage.TotalTokens != 3 {
		t.Fatalf("usage = %+v, want mapped usage", resp.Usage)
	}
	if resp.ProviderMetadata["id"] != "resp-1" || resp.ProviderMetadata["model"] != "test-model" {
		t.Fatalf("metadata = %+v, want response id and model", resp.ProviderMetadata)
	}
}

func TestModelInvokeRejectsOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]string{
					"role": "assistant", "content": strings.Repeat("x", maxResponseBytes+1),
				},
				"finish_reason": "stop",
			}},
		})
	}))
	defer server.Close()

	model, err := New("test", server.URL, "test-key", "test-model", WithInsecureHTTP())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = model.Invoke(context.Background(), model.NewRequest(gopact.UserMessage("ping")))
	if err == nil || !strings.Contains(err.Error(), "openai: decode response") {
		t.Fatalf("Invoke() error = %v, want bounded response decode failure", err)
	}
}

func TestModelInvokeMapsRequestFieldsAndEvents(t *testing.T) {
	temp := 0.2
	topP := 0.9
	var got chatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	model, err := New("test", server.URL, "test-key", "test-model", WithInsecureHTTP())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	req := model.NewRequest(
		gopact.Message{Role: "system", Parts: []gopact.MessagePart{{Type: "text", Text: "be terse"}}},
		gopact.UserMessage("ping"),
	)
	req.Temperature = &temp
	req.TopP = &topP
	req.MaxOutputTokens = 128
	req.Stop = []string{"END"}
	var events []gopact.ModelEvent
	_, err = model.Invoke(context.Background(), req, gopact.WithModelEventHandler(func(_ context.Context, ev gopact.ModelEvent) error {
		events = append(events, ev)
		return nil
	}))
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if got.Messages[0].Role != "system" || got.Messages[1].Content != "ping" {
		t.Fatalf("messages = %+v, want mapped roles and content", got.Messages)
	}
	if got.Temperature == nil || *got.Temperature != temp || got.TopP == nil || *got.TopP != topP {
		t.Fatalf("sampling = %+v/%+v, want temp/top_p", got.Temperature, got.TopP)
	}
	if got.MaxTokens != 128 || len(got.Stop) != 1 || got.Stop[0] != "END" {
		t.Fatalf("budget/stop = %+v, want mapped max tokens and stop", got)
	}
	if len(events) != 1 || events[0].Type != gopact.ModelEventMessageDelta || events[0].Source != "test" || events[0].Summary != "pong" {
		t.Fatalf("events = %+v, want one message delta from test", events)
	}
}

func TestModelInvokeValidation(t *testing.T) {
	tests := []struct {
		name string
		req  gopact.ModelRequest
	}{
		{name: "empty model", req: gopact.ModelRequest{Messages: []gopact.Message{gopact.UserMessage("x")}}},
		{name: "empty messages", req: gopact.ModelRequest{Model: "test-model"}},
		{
			name: "unsupported output protocol",
			req: gopact.ModelRequest{
				Model:           "test-model",
				Messages:        []gopact.Message{gopact.UserMessage("x")},
				OutputProtocols: []gopact.OutputProtocol{testProtocol{}},
			},
		},
		{
			name: "unsupported part",
			req: gopact.ModelRequest{
				Model:    "test-model",
				Messages: []gopact.Message{{Role: "user", Parts: []gopact.MessagePart{{Type: "image"}}}},
			},
		},
		{
			name: "unknown request extension",
			req: gopact.ModelRequest{
				Model:      "test-model",
				Messages:   []gopact.Message{gopact.UserMessage("x")},
				Extensions: map[string]any{"other.runtime": true},
			},
		},
	}
	model, err := New("test", "https://example.invalid", "test-key", "test-model")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := model.Invoke(context.Background(), tt.req); err == nil {
				t.Fatal("Invoke() error = nil, want validation error")
			}
		})
	}
}

func TestModelInvokeRejectsUnknownCallExtension(t *testing.T) {
	model, err := New("test", "https://example.invalid", "test-key", "test-model")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = model.Invoke(context.Background(), model.NewRequest(gopact.UserMessage("ping")), testModelCallOptionFunc(func(cfg *gopact.ModelCallConfig) {
		cfg.Extensions = map[string]any{"other.runtime": true}
	}))
	if err == nil || !strings.Contains(err.Error(), "unknown call extension") {
		t.Fatalf("Invoke() error = %v, want unknown call extension", err)
	}
}

func TestModelInvokeMapsToolsSchemaReasoningAndToolIntent(t *testing.T) {
	var got chatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call-1","type":"function","function":{"name":"search","arguments":"{\"q\":\"gopact\"}"}}]},"finish_reason":"tool_calls"}]}`))
	}))
	defer server.Close()

	model, err := New("test", server.URL, "test-key", "test-model", WithInsecureHTTP())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	req := model.NewRequest(gopact.UserMessage("find docs"))
	req.Tools = []gopact.ToolSpec{{
		Name:        "search",
		Description: "search docs",
		Schema:      json.RawMessage(`{"type":"object"}`),
	}}
	req.ToolChoice = gopact.ToolChoice{Mode: "named", Name: "search"}
	req.ResponseSchema = gopact.SchemaRef{Value: json.RawMessage(`{"type":"object","properties":{"answer":{"type":"string"}}}`)}
	req.Reasoning = gopact.ReasoningConfig{Effort: "low"}
	resp, err := model.Invoke(context.Background(), req)
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if len(got.Tools) != 1 || got.Tools[0].Function.Name != "search" {
		t.Fatalf("tools = %+v, want search tool", got.Tools)
	}
	if got.ToolChoice == nil || got.ResponseFormat == nil || got.ReasoningEffort != "low" {
		t.Fatalf("advanced request = %+v, want tool choice, response format, reasoning", got)
	}
	intent, ok := resp.Intent.(gopact.ToolCallIntent)
	if !ok || len(intent.Calls) != 1 || intent.Calls[0].Name != "search" {
		t.Fatalf("intent = %+v, want search tool call", resp.Intent)
	}
	if string(intent.Calls[0].Arguments) != `{"q":"gopact"}` {
		t.Fatalf("arguments = %s, want tool arguments", intent.Calls[0].Arguments)
	}
}

func TestModelInvokeStream(t *testing.T) {
	var got chatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"po\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ng\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	model, err := New("test", server.URL, "test-key", "test-model", WithInsecureHTTP())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	var chunks []string
	var events []string
	for chunk, err := range model.InvokeStream(
		context.Background(),
		model.NewRequest(gopact.UserMessage("ping")),
		gopact.WithModelEventHandler(func(_ context.Context, event gopact.ModelEvent) error {
			events = append(events, event.Summary)
			return nil
		}),
	) {
		if err != nil {
			t.Fatalf("InvokeStream() error = %v", err)
		}
		chunks = append(chunks, chunk.Text)
	}
	if !got.Stream {
		t.Fatal("stream = false, want true")
	}
	if !slices.Equal(chunks, []string{"po", "ng"}) || !slices.Equal(events, chunks) {
		t.Fatalf("chunks/events = %+v/%+v, want streamed chunks", chunks, events)
	}
}

func TestModelInvokeStreamRequiresTerminalMarker(t *testing.T) {
	tests := []struct {
		name              string
		body              string
		wantUnexpectedEOF bool
	}{
		{name: "clean EOF after partial chunk", body: "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n", wantUnexpectedEOF: true},
		{name: "finish reason", body: "data: {\"choices\":[{\"delta\":{\"content\":\"complete\"},\"finish_reason\":\"stop\"}]}\n\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, test.body)
			}))
			defer server.Close()
			model, err := New("test", server.URL, "test-key", "test-model", WithInsecureHTTP())
			if err != nil {
				t.Fatal(err)
			}
			var gotErr error
			for _, err := range model.InvokeStream(t.Context(), model.NewRequest(gopact.UserMessage("ping"))) {
				if err != nil {
					gotErr = err
				}
			}
			if test.wantUnexpectedEOF != errors.Is(gotErr, io.ErrUnexpectedEOF) {
				t.Fatalf("InvokeStream() error = %v, want unexpected EOF = %v", gotErr, test.wantUnexpectedEOF)
			}
		})
	}
}

func TestModelInvokeConcurrent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var got chatRequest
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	model, err := New("test", server.URL, "test-key", "test-model", WithInsecureHTTP())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := model.Invoke(context.Background(), model.NewRequest(gopact.UserMessage("ping")))
			if err != nil {
				t.Errorf("Invoke() error = %v", err)
				return
			}
			if resp.Message.Parts[0].Text != "pong" {
				t.Errorf("text = %q, want pong", resp.Message.Parts[0].Text)
			}
		}()
	}
	wg.Wait()
}

func TestModelInvokeBoundsModelEventSummary(t *testing.T) {
	longText := strings.Repeat("x", 5000)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := map[string]any{
			"choices": []map[string]any{{
				"message":       map[string]string{"role": "assistant", "content": longText},
				"finish_reason": "stop",
			}},
		}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))
	defer server.Close()

	model, err := New("test", server.URL, "test-key", "test-model", WithInsecureHTTP())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	var summaries []string
	_, err = model.Invoke(context.Background(), model.NewRequest(gopact.UserMessage("ping")), gopact.WithModelEventHandler(func(_ context.Context, event gopact.ModelEvent) error {
		summaries = append(summaries, event.Summary)
		return nil
	}))
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if len(summaries) != 1 || len(summaries[0]) != 4096 {
		t.Fatalf("summaries = %d/%d, want one bounded summary", len(summaries), len(summaries[0]))
	}
}

func TestModelInvokeHTTPErrorIsBoundedAndRetryable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte("secret test-key " + strings.Repeat("x", 5000)))
	}))
	defer server.Close()

	model, err := New("test", server.URL, "test-key", "test-model", WithInsecureHTTP())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = model.Invoke(context.Background(), model.NewRequest(gopact.UserMessage("ping")))
	var httpErr Error
	if !errors.As(err, &httpErr) {
		t.Fatalf("Invoke() error = %T, want openai.Error", err)
	}
	if !httpErr.Retryable || httpErr.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("http error = %+v, want retryable 429", httpErr)
	}
	if strings.Contains(err.Error(), "test-key") || len(httpErr.Body) > 4096 {
		t.Fatalf("error leaked key or exceeded bound: len=%d err=%v", len(httpErr.Body), err)
	}
}

func TestModelInvokeRetriesRetryableStatus(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var got chatRequest
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if got.Messages[0].Content != "ping" {
			t.Fatalf("request content = %q, want ping", got.Messages[0].Content)
		}
		if attempts.Add(1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	model, err := New("test", server.URL, "test-key", "test-model", WithInsecureHTTP(), WithMaxAttempts(2))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := model.Invoke(context.Background(), model.NewRequest(gopact.UserMessage("ping"))); err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if attempts.Load() != 2 {
		t.Fatalf("attempts = %d, want 2", attempts.Load())
	}
}

func TestModelInvokeRetriesTransportFailure(t *testing.T) {
	var attempts atomic.Int32
	transportErr := errors.New("temporary transport failure")
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		if attempts.Add(1) == 1 {
			return nil, transportErr
		}
		body := `{"choices":[{"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}]}`
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	model, err := New("test", "https://example.com", "test-key", "test-model", WithHTTPClient(client), WithRetryPolicy(RetryPolicy{MaxAttempts: 2}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := model.Invoke(context.Background(), model.NewRequest(gopact.UserMessage("ping"))); err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if attempts.Load() != 2 {
		t.Fatalf("attempts = %d, want 2", attempts.Load())
	}
}

func TestModelInvokeCancelsDuringRetryBackoff(t *testing.T) {
	var attempts atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		attempts.Add(1)
		return &http.Response{StatusCode: http.StatusServiceUnavailable, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("retry"))}, nil
	})}
	policy := RetryPolicy{MaxAttempts: 3, InitialBackoff: time.Hour, MaxBackoff: time.Hour}
	model, err := New("test", "https://example.com", "test-key", "test-model", WithHTTPClient(client), WithRetryPolicy(policy))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err = model.Invoke(ctx, model.NewRequest(gopact.UserMessage("ping")))
	if !errors.Is(err, context.DeadlineExceeded) || attempts.Load() != 1 {
		t.Fatalf("Invoke() error/attempts = %v/%d, want deadline/1", err, attempts.Load())
	}
}

func TestModelInvokeAppliesPerCallTimeout(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		select {
		case <-request.Context().Done():
		case <-release:
		}
	}))
	defer func() {
		close(release)
		server.Close()
	}()
	model, err := New("test", server.URL, "test-key", "test-model", WithInsecureHTTP(), WithTimeout(20*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	_, err = model.Invoke(context.Background(), model.NewRequest(gopact.UserMessage("ping")))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Invoke() error = %v, want deadline exceeded", err)
	}
}

func TestModelInvokeRedactsTransportError(t *testing.T) {
	transportErr := errors.New("transport rejected test-key")
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, transportErr })}
	model, err := New("test", "https://example.com", "test-key", "test-model", WithHTTPClient(client), WithMaxAttempts(1))
	if err != nil {
		t.Fatal(err)
	}
	_, err = model.Invoke(context.Background(), model.NewRequest(gopact.UserMessage("ping")))
	if err == nil || strings.Contains(err.Error(), "test-key") || !errors.Is(err, transportErr) {
		t.Fatalf("Invoke() error = %v, want redacted wrapped transport error", err)
	}
}

func TestModelInvokeRejectsInvalidToolArguments(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"tool_calls":[{"id":"call-1","type":"function","function":{"name":"search","arguments":"{"}}]}}]}`))
	}))
	defer server.Close()
	model, err := New("test", server.URL, "test-key", "test-model", WithInsecureHTTP())
	if err != nil {
		t.Fatal(err)
	}
	_, err = model.Invoke(context.Background(), model.NewRequest(gopact.UserMessage("ping")))
	if err == nil || !strings.Contains(err.Error(), "tool arguments") {
		t.Fatalf("Invoke() error = %v, want invalid tool arguments", err)
	}
}

func TestModelInvokeHonorsContextCancel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("request should not be sent after context cancellation")
	}))
	defer server.Close()

	model, err := New("test", server.URL, "test-key", "test-model", WithInsecureHTTP())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = model.Invoke(ctx, model.NewRequest(gopact.UserMessage("ping")))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Invoke() error = %v, want context canceled", err)
	}
}

type testProtocol struct{}

func (testProtocol) Name() string { return "test" }

func (testProtocol) Claims() []gopact.OutputClaim { return nil }

func (testProtocol) NewDecoder(gopact.ProtocolSink) gopact.OutputDecoder { return nil }

type testModelCallOptionFunc func(*gopact.ModelCallConfig)

func (f testModelCallOptionFunc) ApplyModelCallOption(cfg *gopact.ModelCallConfig) {
	f(cfg)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func BenchmarkModelInvoke(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	model, err := New("test", server.URL, "test-key", "test-model", WithInsecureHTTP())
	if err != nil {
		b.Fatalf("New() error = %v", err)
	}
	req := model.NewRequest(gopact.UserMessage("ping"))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := model.Invoke(context.Background(), req); err != nil {
			b.Fatalf("Invoke() error = %v", err)
		}
	}
}
