package codex

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

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact-ext/models/openai/codexauth"
)

func TestNewValidatesConfiguration(t *testing.T) {
	validSource := StaticTokenSource(codexauth.Tokens{AccessToken: "access"})
	tests := []struct {
		name string
		new  func() (*Model, error)
	}{
		{name: "model", new: func() (*Model, error) { return New("", validSource) }},
		{name: "token source", new: func() (*Model, error) { return New("gpt-test", nil) }},
		{name: "http client", new: func() (*Model, error) { return New("gpt-test", validSource, WithHTTPClient(nil)) }},
		{name: "http base url", new: func() (*Model, error) { return New("gpt-test", validSource, WithBaseURL("http://example.com")) }},
		{name: "timeout", new: func() (*Model, error) { return New("gpt-test", validSource, WithTimeout(0)) }},
		{name: "attempts", new: func() (*Model, error) { return New("gpt-test", validSource, WithMaxAttempts(0)) }},
		{name: "originator", new: func() (*Model, error) { return New("gpt-test", validSource, WithOriginator("bad\nvalue")) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := test.new(); err == nil {
				t.Fatal("New() error = nil, want validation error")
			}
		})
	}
}

func TestModelInvokeMapsResponsesRequestAndResult(t *testing.T) {
	var got responsesRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Errorf("path = %q, want /responses", r.URL.Path)
		}
		if authorization := r.Header.Get("Authorization"); authorization != "Bearer access-token" {
			t.Errorf("Authorization = %q", authorization)
		}
		if accountID := r.Header.Get("ChatGPT-Account-ID"); accountID != "account-123" {
			t.Errorf("ChatGPT-Account-ID = %q", accountID)
		}
		if accept := r.Header.Get("Accept"); accept != "text/event-stream" {
			t.Errorf("Accept = %q", accept)
		}
		if originator := r.Header.Get("Originator"); originator != defaultOriginator {
			t.Errorf("Originator = %q", originator)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		writeSSE(t, w,
			`{"type":"response.created","response":{"id":"resp-1"}}`,
			`{"type":"response.output_text.delta","delta":"po"}`,
			`{"type":"response.output_text.delta","delta":"ng"}`,
			`{"type":"response.completed","response":{"id":"resp-1","model":"gpt-test-routed","usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5}}}`,
		)
	}))
	defer server.Close()

	model := newTestModel(t, server.URL, StaticTokenSource(codexauth.Tokens{
		AccessToken: "access-token",
		AccountID:   "account-123",
	}))
	req := model.NewRequest(
		gopact.Message{Role: gopact.MessageRoleSystem, Parts: []gopact.MessagePart{{Type: gopact.MessagePartTypeText, Text: "be concise"}}},
		gopact.UserMessage("ping"),
	)
	req.MaxOutputTokens = 128
	req.Reasoning = gopact.ReasoningConfig{Effort: gopact.ReasoningEffortLow}
	req.Tools = []gopact.ToolSpec{{
		Name:        "lookup",
		Description: "look something up",
		Schema:      json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`),
	}}
	req.ToolChoice = gopact.ToolChoice{Mode: gopact.ToolChoiceModeNamed, Name: "lookup"}
	req.ResponseSchema = gopact.SchemaRef{Value: json.RawMessage(`{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"]}`)}
	req.Metadata = map[string]string{"trace": "test"}

	var eventTypes []gopact.ModelEventType
	response, err := model.Invoke(t.Context(), req, gopact.WithModelEventHandler(func(_ context.Context, event gopact.ModelEvent) error {
		eventTypes = append(eventTypes, event.Type)
		return nil
	}))
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if got.Model != "gpt-test" || !got.Stream || got.Store {
		t.Fatalf("request model/stream/store = %q/%v/%v", got.Model, got.Stream, got.Store)
	}
	if got.Instructions != "be concise" || len(got.Input) != 1 || got.Input[0].Type != "message" || got.Input[0].Role != "user" {
		t.Fatalf("request instructions/input = %q/%+v", got.Instructions, got.Input)
	}
	if len(got.Tools) != 1 || got.Tools[0].Name != "lookup" || got.Tools[0].Type != "function" {
		t.Fatalf("request tools = %+v", got.Tools)
	}
	named, ok := got.ToolChoice.(map[string]any)
	if !ok || named["type"] != "function" || named["name"] != "lookup" {
		t.Fatalf("tool choice = %#v", got.ToolChoice)
	}
	if got.Reasoning == nil || got.Reasoning.Effort != "low" || got.MaxOutputTokens != 128 {
		t.Fatalf("reasoning/max tokens = %+v/%d", got.Reasoning, got.MaxOutputTokens)
	}
	if got.Text == nil || got.Text.Format == nil || got.Text.Format.Name != "response" || !got.Text.Format.Strict {
		t.Fatalf("text format = %+v", got.Text)
	}
	if got.Metadata["trace"] != "test" {
		t.Fatalf("metadata = %+v", got.Metadata)
	}
	if text := messageText(t, response.Message); text != "pong" {
		t.Fatalf("response text = %q", text)
	}
	if _, ok := response.Intent.(gopact.FinalIntent); !ok {
		t.Fatalf("response intent = %T, want FinalIntent", response.Intent)
	}
	if response.Usage != (gopact.Usage{InputTokens: 3, OutputTokens: 2, TotalTokens: 5}) {
		t.Fatalf("usage = %+v", response.Usage)
	}
	if response.FinishReason != "stop" || response.ProviderMetadata["id"] != "resp-1" || response.ProviderMetadata["model"] != "gpt-test-routed" {
		t.Fatalf("terminal metadata = %+v/%q", response.ProviderMetadata, response.FinishReason)
	}
	if !slices.Equal(eventTypes, []gopact.ModelEventType{
		gopact.ModelEventMessageDelta,
		gopact.ModelEventMessageDelta,
		gopact.ModelEventUsage,
		gopact.ModelEventFinish,
	}) {
		t.Fatalf("event types = %+v", eventTypes)
	}
}

func TestModelInvokeReplaysReasoningAndToolCalls(t *testing.T) {
	var requestNumber atomic.Int32
	var second responsesRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := requestNumber.Add(1)
		var got responsesRequest
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		if n == 1 {
			writeSSE(t, w,
				`{"type":"response.output_item.done","item":{"id":"rs_1","type":"reasoning","summary":[],"encrypted_content":"opaque"}}`,
				`{"type":"response.output_item.done","item":{"id":"fc_1","type":"function_call","name":"lookup","arguments":"{\"q\":\"gopact\"}","call_id":"call_1"}}`,
				`{"type":"response.completed","response":{"id":"resp-tools","usage":{"input_tokens":4,"output_tokens":3,"total_tokens":7}}}`,
			)
			return
		}
		second = got
		writeSSE(t, w,
			`{"type":"response.output_text.delta","delta":"done"}`,
			`{"type":"response.completed","response":{"id":"resp-final","usage":{"input_tokens":8,"output_tokens":1,"total_tokens":9}}}`,
		)
	}))
	defer server.Close()

	model := newTestModel(t, server.URL, StaticTokenSource(codexauth.Tokens{AccessToken: "access"}))
	firstReq := model.NewRequest(gopact.UserMessage("find it"))
	firstReq.Tools = []gopact.ToolSpec{{Name: "lookup", Schema: json.RawMessage(`{"type":"object"}`)}}
	first, err := model.Invoke(t.Context(), firstReq)
	if err != nil {
		t.Fatalf("first Invoke() error = %v", err)
	}
	intent, ok := first.Intent.(gopact.ToolCallIntent)
	if !ok || len(intent.Calls) != 1 {
		t.Fatalf("first intent = %+v", first.Intent)
	}
	if call := intent.Calls[0]; call.ID != "call_1" || call.Name != "lookup" || string(call.Arguments) != `{"q":"gopact"}` {
		t.Fatalf("tool call = %+v", call)
	}
	if first.FinishReason != "tool_calls" {
		t.Fatalf("finish reason = %q", first.FinishReason)
	}
	if len(first.Message.Parts) != 2 || first.Message.Parts[0].Type != MessagePartTypeResponseItem || first.Message.Parts[1].Type != MessagePartTypeResponseItem {
		t.Fatalf("provider state parts = %+v", first.Message.Parts)
	}

	secondReq := model.NewRequest(
		gopact.UserMessage("find it"),
		first.Message,
		gopact.Message{Role: gopact.MessageRoleTool, Parts: []gopact.MessagePart{{Type: gopact.MessagePartTypeText, Text: "result-42"}}},
	)
	secondReq.Tools = firstReq.Tools
	final, err := model.Invoke(t.Context(), secondReq)
	if err != nil {
		t.Fatalf("second Invoke() error = %v", err)
	}
	if text := messageText(t, final.Message); text != "done" {
		t.Fatalf("final text = %q", text)
	}
	if len(second.Input) != 4 {
		t.Fatalf("second input = %+v", second.Input)
	}
	if types := []string{second.Input[0].Type, second.Input[1].Type, second.Input[2].Type, second.Input[3].Type}; !slices.Equal(types, []string{"message", "reasoning", "function_call", "function_call_output"}) {
		t.Fatalf("second input types = %+v", types)
	}
	if second.Input[3].CallID != "call_1" || second.Input[3].Output != "result-42" {
		t.Fatalf("function output = %+v", second.Input[3])
	}
	if second.Input[1].ID != "" || second.Input[2].ID != "" {
		t.Fatalf("replayed response item ids = %q/%q, want empty", second.Input[1].ID, second.Input[2].ID)
	}
}

func TestModelPreservesReasoningBeforeAssistantText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeSSE(t, w,
			`{"type":"response.output_item.done","item":{"id":"rs_1","type":"reasoning","summary":[],"encrypted_content":"opaque"}}`,
			`{"type":"response.output_text.delta","delta":"answer"}`,
			`{"type":"response.completed","response":{"id":"resp-ordered"}}`,
		)
	}))
	defer server.Close()
	model := newTestModel(t, server.URL, StaticTokenSource(codexauth.Tokens{AccessToken: "access"}))

	response, err := model.Invoke(t.Context(), model.NewRequest(gopact.UserMessage("question")))
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if len(response.Message.Parts) != 2 || response.Message.Parts[0].Type != MessagePartTypeResponseItem || response.Message.Parts[1].Type != gopact.MessagePartTypeText {
		t.Fatalf("response parts = %+v", response.Message.Parts)
	}
	_, input, err := encodeMessages([]gopact.Message{response.Message})
	if err != nil {
		t.Fatalf("encodeMessages() error = %v", err)
	}
	if len(input) != 2 || input[0].Type != "reasoning" || input[1].Type != "message" {
		t.Fatalf("replayed input = %+v", input)
	}
}

func TestModelInvokeStreamEmitsVisibleAndReasoningEvents(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeSSE(t, w,
			`{"type":"response.reasoning_summary_text.delta","delta":"thinking"}`,
			`{"type":"response.output_text.delta","delta":"a"}`,
			`{"type":"response.output_text.delta","delta":"b"}`,
			`{"type":"response.completed","response":{"id":"resp-stream","usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}}}`,
		)
	}))
	defer server.Close()
	model := newTestModel(t, server.URL, StaticTokenSource(codexauth.Tokens{AccessToken: "access"}))

	var chunks []string
	var events []gopact.ModelEventType
	for chunk, err := range model.InvokeStream(
		t.Context(),
		model.NewRequest(gopact.UserMessage("stream")),
		gopact.WithModelEventHandler(func(_ context.Context, event gopact.ModelEvent) error {
			events = append(events, event.Type)
			return nil
		}),
	) {
		if err != nil {
			t.Fatalf("InvokeStream() error = %v", err)
		}
		chunks = append(chunks, chunk.Text)
	}
	if !slices.Equal(chunks, []string{"a", "b"}) {
		t.Fatalf("chunks = %+v", chunks)
	}
	if !slices.Equal(events, []gopact.ModelEventType{
		gopact.ModelEventReasoningDelta,
		gopact.ModelEventMessageDelta,
		gopact.ModelEventMessageDelta,
		gopact.ModelEventUsage,
		gopact.ModelEventFinish,
	}) {
		t.Fatalf("events = %+v", events)
	}
}

func TestModelInvokeStreamRequiresCompletedEvent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeSSE(t, w, `{"type":"response.output_text.delta","delta":"partial"}`)
	}))
	defer server.Close()
	model := newTestModel(t, server.URL, StaticTokenSource(codexauth.Tokens{AccessToken: "access"}))

	var gotErr error
	for _, err := range model.InvokeStream(t.Context(), model.NewRequest(gopact.UserMessage("stream"))) {
		if err != nil {
			gotErr = err
		}
	}
	if !errors.Is(gotErr, io.ErrUnexpectedEOF) {
		t.Fatalf("InvokeStream() error = %v, want unexpected EOF", gotErr)
	}
}

func TestModelInvokeReturnsFailedEvent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeSSE(t, w, `{"type":"response.failed","response":{"error":{"code":"invalid_prompt","message":"blocked"}}}`)
	}))
	defer server.Close()
	model := newTestModel(t, server.URL, StaticTokenSource(codexauth.Tokens{AccessToken: "access"}))

	_, err := model.Invoke(t.Context(), model.NewRequest(gopact.UserMessage("fail")))
	if err == nil || !strings.Contains(err.Error(), "invalid_prompt") || !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("Invoke() error = %v", err)
	}
}

func TestModelDoesNotDuplicateTerminalStreamContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeSSE(t, w,
			`{"type":"response.refusal.delta","delta":"no"}`,
			`{"type":"response.output_item.done","item":{"id":"msg_1","type":"message","content":[{"type":"refusal","refusal":"no"}]}}`,
			`{"type":"response.completed","response":{"id":"resp-refusal"}}`,
		)
	}))
	defer server.Close()
	model := newTestModel(t, server.URL, StaticTokenSource(codexauth.Tokens{AccessToken: "access"}))

	response, err := model.Invoke(t.Context(), model.NewRequest(gopact.UserMessage("refuse")))
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	intent, ok := response.Intent.(gopact.RefusalIntent)
	if !ok || messageText(t, intent.Refusal.Message) != "no" {
		t.Fatalf("response intent = %+v", response.Intent)
	}
}

func TestModelDoesNotRepeatCompletedToolArgumentsAsDelta(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeSSE(t, w,
			`{"type":"response.function_call_arguments.delta","item_id":"fc_1","delta":"{\"q\":\"x\"}"}`,
			`{"type":"response.output_item.done","item":{"id":"fc_1","type":"function_call","name":"lookup","arguments":"{\"q\":\"x\"}","call_id":"call_1"}}`,
			`{"type":"response.completed","response":{"id":"resp-tool"}}`,
		)
	}))
	defer server.Close()
	model := newTestModel(t, server.URL, StaticTokenSource(codexauth.Tokens{AccessToken: "access"}))
	request := model.NewRequest(gopact.UserMessage("lookup"))
	request.Tools = []gopact.ToolSpec{{Name: "lookup", Schema: json.RawMessage(`{"type":"object"}`)}}

	var toolEvents int
	_, err := model.Invoke(t.Context(), request, gopact.WithModelEventHandler(func(_ context.Context, event gopact.ModelEvent) error {
		if event.Type == gopact.ModelEventToolCallDelta {
			toolEvents++
		}
		return nil
	}))
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if toolEvents != 1 {
		t.Fatalf("tool delta events = %d, want one", toolEvents)
	}
}

func TestModelInvokeRefreshesOnceAfterUnauthorized(t *testing.T) {
	source := &rotatingTokenSource{tokens: codexauth.Tokens{AccessToken: "stale", AccountID: "account"}}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Authorization") == "Bearer stale" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":{"message":"expired stale"}}`)
			return
		}
		writeSSE(t, w,
			`{"type":"response.output_text.delta","delta":"ok"}`,
			`{"type":"response.completed","response":{"id":"resp-ok"}}`,
		)
	}))
	defer server.Close()
	model := newTestModel(t, server.URL, source)

	response, err := model.Invoke(t.Context(), model.NewRequest(gopact.UserMessage("retry")))
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if text := messageText(t, response.Message); text != "ok" {
		t.Fatalf("response text = %q", text)
	}
	if requests.Load() != 2 || source.refreshes.Load() != 1 {
		t.Fatalf("requests/refreshes = %d/%d", requests.Load(), source.refreshes.Load())
	}
}

func TestModelInvokeRedactsTokensFromHTTPError(t *testing.T) {
	tokens := codexauth.Tokens{
		AccessToken:  "access-secret",
		IDToken:      "id-secret",
		RefreshToken: "refresh-secret",
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"access-secret id-secret refresh-secret"}}`)
	}))
	defer server.Close()
	model := newTestModel(t, server.URL, StaticTokenSource(tokens))

	_, err := model.Invoke(t.Context(), model.NewRequest(gopact.UserMessage("error")))
	if err == nil {
		t.Fatal("Invoke() error = nil")
	}
	for _, secret := range []string{tokens.AccessToken, tokens.IDToken, tokens.RefreshToken} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error contains token %q: %v", secret, err)
		}
	}
}

func TestModelRejectsCrossOriginRedirect(t *testing.T) {
	var downstreamCalls atomic.Int32
	downstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		downstreamCalls.Add(1)
	}))
	defer downstream.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", downstream.URL)
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer redirector.Close()
	model := newTestModel(t, redirector.URL, StaticTokenSource(codexauth.Tokens{AccessToken: "access"}))

	_, err := model.Invoke(t.Context(), model.NewRequest(gopact.UserMessage("redirect")))
	if err == nil || !strings.Contains(err.Error(), "cross-origin redirect") {
		t.Fatalf("Invoke() error = %v, want cross-origin redirect error", err)
	}
	if downstreamCalls.Load() != 0 {
		t.Fatalf("downstream calls = %d, want zero", downstreamCalls.Load())
	}
}

func TestModelRetriesServerFailureBeforeStreaming(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		writeSSE(t, w,
			`{"type":"response.output_text.delta","delta":"recovered"}`,
			`{"type":"response.completed","response":{"id":"resp-retry"}}`,
		)
	}))
	defer server.Close()
	model, err := New(
		"gpt-test",
		StaticTokenSource(codexauth.Tokens{AccessToken: "access"}),
		WithBaseURL(server.URL),
		WithInsecureHTTP(),
		WithMaxAttempts(2),
	)
	if err != nil {
		t.Fatal(err)
	}

	response, err := model.Invoke(t.Context(), model.NewRequest(gopact.UserMessage("retry")))
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if text := messageText(t, response.Message); text != "recovered" || requests.Load() != 2 {
		t.Fatalf("response/requests = %q/%d", text, requests.Load())
	}
}

func TestModelRejectsUnsupportedRequests(t *testing.T) {
	model, err := New("gpt-test", StaticTokenSource(codexauth.Tokens{AccessToken: "access"}))
	if err != nil {
		t.Fatal(err)
	}
	base := model.NewRequest(gopact.UserMessage("test"))
	tests := []struct {
		name   string
		mutate func(*gopact.ModelRequest)
	}{
		{name: "temperature", mutate: func(req *gopact.ModelRequest) { req.Temperature = new(float64) }},
		{name: "top p", mutate: func(req *gopact.ModelRequest) { req.TopP = new(float64) }},
		{name: "stop", mutate: func(req *gopact.ModelRequest) { req.Stop = []string{"stop"} }},
		{name: "seed", mutate: func(req *gopact.ModelRequest) { req.Seed = new(int64) }},
		{name: "modality", mutate: func(req *gopact.ModelRequest) { req.Modalities = []gopact.Modality{gopact.ModalityImage} }},
		{name: "schema uri", mutate: func(req *gopact.ModelRequest) { req.ResponseSchema.URI = "schema://test" }},
		{name: "extension", mutate: func(req *gopact.ModelRequest) { req.Extensions = map[string]any{"unknown": true} }},
		{name: "orphan tool output", mutate: func(req *gopact.ModelRequest) {
			req.Messages = []gopact.Message{{Role: gopact.MessageRoleTool, Parts: []gopact.MessagePart{{Type: gopact.MessagePartTypeText, Text: "orphan"}}}}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := cloneModelRequest(base)
			test.mutate(&req)
			if _, err := model.Invoke(t.Context(), req); err == nil {
				t.Fatal("Invoke() error = nil, want validation error")
			}
		})
	}
}

func TestResponsesPayloadIsBounded(t *testing.T) {
	request := gopact.ModelRequest{
		Model:    "gpt-test",
		Messages: []gopact.Message{gopact.UserMessage(strings.Repeat("x", maxRequestBytes))},
	}
	if _, err := responsesPayload(request); err == nil || !strings.Contains(err.Error(), "request exceeds") {
		t.Fatalf("responsesPayload() error = %v", err)
	}
}

func TestModelInvokeIsConcurrent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeSSE(t, w,
			`{"type":"response.output_text.delta","delta":"ok"}`,
			`{"type":"response.completed","response":{"id":"resp"}}`,
		)
	}))
	defer server.Close()
	model := newTestModel(t, server.URL, StaticTokenSource(codexauth.Tokens{AccessToken: "access"}))

	var wait sync.WaitGroup
	for range 32 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			response, err := model.Invoke(t.Context(), model.NewRequest(gopact.UserMessage("concurrent")))
			if err != nil {
				t.Errorf("Invoke() error = %v", err)
				return
			}
			if text := messageText(t, response.Message); text != "ok" {
				t.Errorf("response text = %q", text)
			}
		}()
	}
	wait.Wait()
}

type rotatingTokenSource struct {
	mu        sync.Mutex
	tokens    codexauth.Tokens
	refreshes atomic.Int32
}

func (source *rotatingTokenSource) Token(context.Context) (codexauth.Tokens, error) {
	source.mu.Lock()
	defer source.mu.Unlock()
	return source.tokens, nil
}

func (source *rotatingTokenSource) Refresh(context.Context) (codexauth.Tokens, error) {
	source.mu.Lock()
	defer source.mu.Unlock()
	source.refreshes.Add(1)
	source.tokens.AccessToken = "fresh"
	return source.tokens, nil
}

func newTestModel(t *testing.T, baseURL string, source TokenSource) *Model {
	t.Helper()
	model, err := New(
		"gpt-test",
		source,
		WithBaseURL(baseURL),
		WithInsecureHTTP(),
		WithMaxAttempts(1),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return model
}

func writeSSE(t *testing.T, w http.ResponseWriter, events ...string) {
	t.Helper()
	w.Header().Set("Content-Type", "text/event-stream")
	for _, event := range events {
		if _, err := io.WriteString(w, "data: "+event+"\n\n"); err != nil {
			t.Errorf("write SSE: %v", err)
			return
		}
	}
}

func messageText(t *testing.T, message gopact.Message) string {
	t.Helper()
	var text strings.Builder
	for _, part := range message.Parts {
		if part.Type == gopact.MessagePartTypeText {
			text.WriteString(part.Text)
		}
	}
	return text.String()
}
