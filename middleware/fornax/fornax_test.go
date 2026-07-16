package fornax

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"iter"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact/agent"
	"github.com/gopact-ai/gopact/workflow"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestNewUsesExplicitFornaxConfiguration(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "http://127.0.0.1:1/ambient")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_HEADERS", "Authorization=ambient,cozeloop-workspace-id=999")

	type request struct {
		authorization string
		spaceID       string
		path          string
		body          []byte
	}
	requests := make(chan request, 1)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, incoming *http.Request) {
		body, err := io.ReadAll(incoming.Body)
		if err != nil {
			t.Errorf("read trace request: %v", err)
		}
		requests <- request{
			authorization: incoming.Header.Get("Authorization"),
			spaceID:       incoming.Header.Get("cozeloop-workspace-id"),
			path:          incoming.URL.Path,
			body:          body,
		}
		response.Header().Set("Content-Type", "application/x-protobuf")
		response.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	middleware, err := New(t.Context(), Config{
		SpaceID:       "12345",
		Endpoint:      server.URL + "/open-api/observability/opentelemetry/v1/traces",
		Authorization: "Bearer explicit",
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := middleware.Use(testAgent{}).Invoke(t.Context(), agent.Request{}); err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if err := middleware.Close(t.Context()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	var got request
	select {
	case got = <-requests:
	case <-time.After(time.Second):
		t.Fatal("trace request was not received")
	}
	if got.authorization != "Bearer explicit" {
		t.Fatalf("Authorization = %q, want explicit value", got.authorization)
	}
	if got.spaceID != "12345" {
		t.Fatalf("workspace ID = %q, want 12345", got.spaceID)
	}
	if got.path != "/open-api/observability/opentelemetry/v1/traces" {
		t.Fatalf("request path = %q", got.path)
	}
	if len(got.body) == 0 {
		t.Fatal("trace request body is empty")
	}
}

func TestMiddlewareReportsAgentAndWorkflowSpans(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	middleware := newMiddleware(provider)
	t.Cleanup(func() { _ = middleware.Close(t.Context()) })

	var observed atomic.Int64
	target := middleware.Use(testAgent{})
	request := agent.Request{Messages: []gopact.Message{gopact.UserMessage("hello")}}
	response, err := target.Invoke(t.Context(), request, gopact.WithEventHandler(func(context.Context, gopact.Event) error {
		observed.Add(1)
		return nil
	}))
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if got := response.Message.Parts[0].Text; got != "world" {
		t.Fatalf("Invoke() text = %q, want world", got)
	}
	if got := observed.Load(); got != 8 {
		t.Fatalf("existing sink received %d events, want 8", got)
	}

	spans := exporter.GetSpans()
	if len(spans) != 4 {
		t.Fatalf("reported %d spans, want 4", len(spans))
	}
	root := spanNamed(t, spans, "react")
	model := spanNamed(t, spans, "model")
	child := spanNamed(t, spans, "child")
	tool := spanNamed(t, spans, "tool")
	if model.Parent.SpanID() != root.SpanContext.SpanID() {
		t.Fatal("model span is not a child of the agent span")
	}
	if child.Parent.SpanID() != root.SpanContext.SpanID() {
		t.Fatal("nested Agent span is not a child of the root Agent span")
	}
	if tool.Parent.SpanID() != child.SpanContext.SpanID() {
		t.Fatal("tool span is not a child of the nested Agent span")
	}
	if got := stringAttribute(root.Attributes, "cozeloop.span_type"); got != "fornax_query" {
		t.Fatalf("root span type = %q, want fornax_query", got)
	}
	if got := stringAttribute(root.Attributes, "messaging.message.id"); got != "run-1" {
		t.Fatalf("message_id = %q, want run-1", got)
	}
	if got := stringAttribute(root.Attributes, "session.id"); got != "session-1" {
		t.Fatalf("thread_id = %q, want session-1", got)
	}
	if got := stringAttribute(model.Attributes, "cozeloop.span_type"); got != "model" {
		t.Fatalf("model span type = %q, want model", got)
	}
	if got := stringAttribute(child.Attributes, "cozeloop.span_type"); got != "agent" {
		t.Fatalf("nested span type = %q, want agent", got)
	}
	if got := stringAttribute(tool.Attributes, "cozeloop.span_type"); got != "tool" {
		t.Fatalf("tool span type = %q, want tool", got)
	}
	if got := stringAttribute(root.Attributes, "cozeloop.input"); got == "" {
		t.Fatal("root input is empty")
	}
	if got := stringAttribute(root.Attributes, "cozeloop.output"); got == "" {
		t.Fatal("root output is empty")
	}
	if got, ok := int64Attribute(root.Attributes, "cozeloop.status_code"); !ok || got != 0 {
		t.Fatalf("root status code = %d, want 0", got)
	}
}

func TestMiddlewareEnrichesNodeSpansWithComponentEvents(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	middleware := newMiddleware(provider)
	t.Cleanup(func() { _ = middleware.Close(t.Context()) })

	if _, err := middleware.Use(componentEventAgent{}).Invoke(t.Context(), agent.Request{
		Messages: []gopact.Message{gopact.UserMessage("hello")},
	}); err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}

	spans := exporter.GetSpans()
	model := spanNamed(t, spans, "model")
	tool := spanNamed(t, spans, "tool")
	if got := stringAttribute(model.Attributes, spanTypeAttribute); got != "model" {
		t.Fatalf("model span type = %q, want model", got)
	}
	if got := stringAttribute(model.Attributes, modelNameAttribute); got != "demo-model" {
		t.Fatalf("model_name = %q, want demo-model", got)
	}
	if got, ok := int64Attribute(model.Attributes, totalTokensAttribute); !ok || got != 3 {
		t.Fatalf("tokens = %d, want 3", got)
	}
	if got := stringAttribute(model.Attributes, inputAttribute); !strings.Contains(got, "demo-model") {
		t.Fatalf("model input = %q, want request payload", got)
	}
	if got := stringAttribute(model.Attributes, outputAttribute); !strings.Contains(got, "assistant") {
		t.Fatalf("model output = %q, want response payload", got)
	}
	if got := stringAttribute(tool.Attributes, spanTypeAttribute); got != "tool" {
		t.Fatalf("tool span type = %q, want tool", got)
	}
	if got := stringAttribute(tool.Attributes, toolCallIDAttribute); got != "call-1" {
		t.Fatalf("tool_call_id = %q, want call-1", got)
	}
	if got := stringAttribute(tool.Attributes, outputAttribute); !strings.Contains(got, "tool-result") {
		t.Fatalf("tool output = %q, want outcome payload", got)
	}
}

func TestUseStreamingReportsOutput(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	middleware := newMiddleware(provider)
	t.Cleanup(func() { _ = middleware.Close(t.Context()) })

	target, ok := middleware.Use(streamingTestAgent{}).(agent.StreamingAgent)
	if !ok {
		t.Fatal("Use() removed StreamingAgent support")
	}
	var chunks []agent.Chunk
	for chunk, err := range target.InvokeStream(t.Context(), agent.Request{}) {
		if err != nil {
			t.Fatalf("InvokeStream() error = %v", err)
		}
		chunks = append(chunks, chunk)
	}
	if len(chunks) != 2 || chunks[0].Text != "first" || chunks[1].Text != "second" {
		t.Fatalf("InvokeStream() chunks = %+v", chunks)
	}
	root := spanNamed(t, exporter.GetSpans(), "react")
	output := stringAttribute(root.Attributes, "cozeloop.output")
	if !strings.Contains(output, "first") || !strings.Contains(output, "second") {
		t.Fatalf("stream output = %q", output)
	}
}

func TestStreamingConsumerStopEndsTrace(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	middleware := newMiddleware(provider)
	t.Cleanup(func() { _ = middleware.Close(t.Context()) })

	for range middleware.UseStreaming(streamingTestAgent{}).InvokeStream(t.Context(), agent.Request{}) {
		break
	}
	root := spanNamed(t, exporter.GetSpans(), "react")
	if root.Status.Code != codes.Error {
		t.Fatalf("root status = %v, want error", root.Status.Code)
	}
	if got, ok := int64Attribute(root.Attributes, "cozeloop.status_code"); !ok || got != failedStatusCode {
		t.Fatalf("root status code = %d, want %d", got, failedStatusCode)
	}
}

func TestStreamingTraceOutputIsBounded(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	middleware := newMiddleware(provider)
	t.Cleanup(func() { _ = middleware.Close(t.Context()) })

	var chunks []agent.Chunk
	for chunk, err := range middleware.UseStreaming(largeStreamingTestAgent{}).InvokeStream(t.Context(), agent.Request{}) {
		if err != nil {
			t.Fatalf("InvokeStream() error = %v", err)
		}
		chunks = append(chunks, chunk)
	}
	if len(chunks) != 2 {
		t.Fatalf("InvokeStream() chunks = %d, want 2", len(chunks))
	}
	if len(chunks[0].Text) != maxTraceFieldBytes || chunks[1].Text != "tail" {
		t.Fatalf("InvokeStream() did not forward all chunks: first bytes = %d, second = %q", len(chunks[0].Text), chunks[1].Text)
	}
	root := spanNamed(t, exporter.GetSpans(), "react")
	output := stringAttribute(root.Attributes, outputAttribute)
	if len(output) > maxTraceFieldBytes {
		t.Fatalf("stream trace output bytes = %d, limit = %d", len(output), maxTraceFieldBytes)
	}
	if !json.Valid([]byte(output)) {
		t.Fatalf("stream trace output is invalid JSON: %q", output)
	}
	if got := stringAttribute(root.Attributes, cutOffAttribute); got != `["output"]` {
		t.Fatalf("cut_off = %q, want [\"output\"]", got)
	}
}

func TestUnaryTracePayloadsAreBounded(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	middleware := newMiddleware(provider)
	t.Cleanup(func() { _ = middleware.Close(t.Context()) })

	largeText := strings.Repeat("x", maxTraceFieldBytes)
	request := agent.Request{Messages: []gopact.Message{{
		Role:  gopact.MessageRoleUser,
		Parts: []gopact.MessagePart{{Type: gopact.MessagePartTypeText, Text: largeText}},
	}}}
	if _, err := middleware.Use(largeTestAgent{output: largeText}).Invoke(t.Context(), request); err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	root := spanNamed(t, exporter.GetSpans(), "react")
	if got := stringAttribute(root.Attributes, inputAttribute); got != "" {
		t.Fatalf("oversized input was reported with %d bytes", len(got))
	}
	if got := stringAttribute(root.Attributes, outputAttribute); got != "" {
		t.Fatalf("oversized output was reported with %d bytes", len(got))
	}
	if got := stringAttribute(root.Attributes, cutOffAttribute); got != `["input","output"]` {
		t.Fatalf("cut_off = %q, want [\"input\",\"output\"]", got)
	}
}

func TestMiddlewareReportsErrors(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	middleware := newMiddleware(provider)
	t.Cleanup(func() { _ = middleware.Close(t.Context()) })

	if _, err := middleware.Use(failingTestAgent{}).Invoke(t.Context(), agent.Request{}); !errors.Is(err, errTestAgent) {
		t.Fatalf("Invoke() error = %v, want test error", err)
	}
	spans := exporter.GetSpans()
	if len(spans) != 2 {
		t.Fatalf("reported %d spans, want 2", len(spans))
	}
	for _, name := range []string{"failing", "model"} {
		span := spanNamed(t, spans, name)
		if span.Status.Code != codes.Error {
			t.Fatalf("%s status = %v, want error", name, span.Status.Code)
		}
		if got, ok := int64Attribute(span.Attributes, "cozeloop.status_code"); !ok || got != failedStatusCode {
			t.Fatalf("%s status code = %d, want %d", name, got, failedStatusCode)
		}
		if got := stringAttribute(span.Attributes, "error"); got == "" {
			t.Fatalf("%s error attribute is empty", name)
		}
	}
}

type testAgent struct{}

func (testAgent) Identity() agent.Identity {
	return agent.Identity{Name: "react", Version: "test"}
}

func (testAgent) Invoke(ctx context.Context, _ agent.Request, options ...gopact.RunOption) (agent.Response, error) {
	start := time.Now().UTC()
	config := gopact.ResolveRunOptions(options...)
	events := []gopact.Event{
		{DefinitionID: "react", SessionID: "session-1", RunID: "run-1", Type: workflow.EventWorkflowStarted, Timestamp: start},
		{DefinitionID: "react", SessionID: "session-1", RunID: "run-1", NodeID: "model", ActivationID: "act-1", AttemptID: "act-1/attempt-1", Type: workflow.EventNodeStarted, Timestamp: start.Add(time.Millisecond)},
		{DefinitionID: "child", SessionID: "session-1", RunID: "run-2", ParentRunID: "run-1", Type: workflow.EventWorkflowRetryStarted, Timestamp: start.Add(2 * time.Millisecond)},
		{DefinitionID: "child", SessionID: "session-1", RunID: "run-2", ParentRunID: "run-1", NodeID: "tool", ActivationID: "act-1", AttemptID: "act-1/attempt-1", Type: workflow.EventNodeStarted, Timestamp: start.Add(3 * time.Millisecond)},
		{DefinitionID: "child", SessionID: "session-1", RunID: "run-2", ParentRunID: "run-1", NodeID: "tool", ActivationID: "act-1", AttemptID: "act-1/attempt-1", Type: workflow.EventNodeCompleted, Timestamp: start.Add(4 * time.Millisecond)},
		{DefinitionID: "child", SessionID: "session-1", RunID: "run-2", ParentRunID: "run-1", Type: workflow.EventWorkflowCompleted, Timestamp: start.Add(5 * time.Millisecond)},
		{DefinitionID: "react", SessionID: "session-1", RunID: "run-1", NodeID: "model", ActivationID: "act-1", AttemptID: "act-1/attempt-1", Type: workflow.EventNodeCompleted, Timestamp: start.Add(6 * time.Millisecond)},
		{DefinitionID: "react", SessionID: "session-1", RunID: "run-1", Type: workflow.EventWorkflowCompleted, Timestamp: start.Add(7 * time.Millisecond)},
	}
	for _, event := range events {
		if err := emitEvent(ctx, config.EventSinks, event); err != nil {
			return agent.Response{}, err
		}
	}
	return agent.Response{Message: gopact.Message{
		Role:  gopact.MessageRoleAssistant,
		Parts: []gopact.MessagePart{{Type: gopact.MessagePartTypeText, Text: "world"}},
	}}, nil
}

type streamingTestAgent struct{ testAgent }

func (streamingTestAgent) InvokeStream(ctx context.Context, request agent.Request, options ...gopact.RunOption) iter.Seq2[agent.Chunk, error] {
	return func(yield func(agent.Chunk, error) bool) {
		if _, err := (testAgent{}).Invoke(ctx, request, options...); err != nil {
			yield(agent.Chunk{}, err)
			return
		}
		if !yield(agent.Chunk{Text: "first"}, nil) {
			return
		}
		yield(agent.Chunk{Text: "second"}, nil)
	}
}

type largeStreamingTestAgent struct{ testAgent }

func (largeStreamingTestAgent) InvokeStream(ctx context.Context, request agent.Request, options ...gopact.RunOption) iter.Seq2[agent.Chunk, error] {
	return func(yield func(agent.Chunk, error) bool) {
		if _, err := (testAgent{}).Invoke(ctx, request, options...); err != nil {
			yield(agent.Chunk{}, err)
			return
		}
		if !yield(agent.Chunk{Text: strings.Repeat("x", maxTraceFieldBytes)}, nil) {
			return
		}
		yield(agent.Chunk{Text: "tail"}, nil)
	}
}

type largeTestAgent struct {
	testAgent
	output string
}

func (a largeTestAgent) Invoke(ctx context.Context, request agent.Request, options ...gopact.RunOption) (agent.Response, error) {
	if _, err := (testAgent{}).Invoke(ctx, request, options...); err != nil {
		return agent.Response{}, err
	}
	return agent.Response{Message: gopact.Message{
		Role:  gopact.MessageRoleAssistant,
		Parts: []gopact.MessagePart{{Type: gopact.MessagePartTypeText, Text: a.output}},
	}}, nil
}

var errTestAgent = errors.New("test agent failed")

type failingTestAgent struct{}

func (failingTestAgent) Identity() agent.Identity {
	return agent.Identity{Name: "failing", Version: "test"}
}

func (failingTestAgent) Invoke(ctx context.Context, _ agent.Request, options ...gopact.RunOption) (agent.Response, error) {
	config := gopact.ResolveRunOptions(options...)
	start := time.Now().UTC()
	events := []gopact.Event{
		{DefinitionID: "failing", SessionID: "session-1", RunID: "run-1", Type: workflow.EventWorkflowStarted, Timestamp: start},
		{DefinitionID: "failing", SessionID: "session-1", RunID: "run-1", NodeID: "model", ActivationID: "act-1", AttemptID: "act-1/attempt-1", Type: workflow.EventNodeStarted, Timestamp: start.Add(time.Millisecond)},
	}
	for _, event := range events {
		if err := emitEvent(ctx, config.EventSinks, event); err != nil {
			return agent.Response{}, err
		}
	}
	return agent.Response{}, errTestAgent
}

func emitEvent(ctx context.Context, sinks []gopact.EventSink, event gopact.Event) error {
	for _, sink := range sinks {
		if err := sink.Emit(ctx, event); err != nil {
			return err
		}
	}
	return nil
}

type componentEventAgent struct{}

func (componentEventAgent) Identity() agent.Identity {
	return agent.Identity{Name: "component", Description: "component events", Version: "test"}
}

func (componentEventAgent) Invoke(ctx context.Context, request agent.Request, options ...gopact.RunOption) (agent.Response, error) {
	wf := workflow.New[agent.Request, agent.Response]("component")
	modelNode := wf.Node("model", func(ctx context.Context, request agent.Request) (agent.Request, error) {
		modelRequest := gopact.ModelRequest{Model: "demo-model", Messages: request.Messages}
		if err := workflow.EmitModelEvent(ctx, gopact.ModelEvent{
			Type: gopact.ModelEventCallStarted, Request: &modelRequest,
		}); err != nil {
			return agent.Request{}, err
		}
		modelResponse := gopact.ModelResponse{
			Message:      gopact.Message{Role: "assistant", Parts: []gopact.MessagePart{{Type: "text", Text: "use tool"}}},
			Usage:        gopact.Usage{InputTokens: 1, OutputTokens: 2, TotalTokens: 3},
			FinishReason: "tool_calls",
		}
		if err := workflow.EmitModelEvent(ctx, gopact.ModelEvent{
			Type: gopact.ModelEventCallFinished, Request: &modelRequest, Response: &modelResponse,
		}); err != nil {
			return agent.Request{}, err
		}
		return request, nil
	})
	toolNode := wf.Node("tool", func(ctx context.Context, _ agent.Request) (agent.Response, error) {
		call := gopact.ToolCall{ID: "call-1", Name: "lookup", Arguments: json.RawMessage(`{"query":"hello"}`)}
		if err := workflow.EmitToolEvent(ctx, gopact.ToolEvent{Type: gopact.ToolEventCallStarted, Call: call}); err != nil {
			return agent.Response{}, err
		}
		outcome := gopact.ToolResultOutcome{
			CallID: call.ID, Name: call.Name, Result: gopact.ToolResult{Preview: "tool-result"},
		}
		if err := workflow.EmitToolEvent(ctx, gopact.ToolEvent{
			Type: gopact.ToolEventCallFinished, Call: call, Outcome: outcome,
		}); err != nil {
			return agent.Response{}, err
		}
		return agent.Response{Message: gopact.Message{
			Role: "assistant", Parts: []gopact.MessagePart{{Type: "text", Text: "done"}},
		}}, nil
	})
	wf.Entry(modelNode)
	wf.Edge(modelNode, toolNode)
	wf.Exit(toolNode)
	target, err := agent.NewWorkflowAgent(agent.Identity{Name: "component", Description: "component events", Version: "test"}, wf)
	if err != nil {
		return agent.Response{}, err
	}
	return target.Invoke(ctx, request, options...)
}

func spanNamed(t *testing.T, spans tracetest.SpanStubs, name string) tracetest.SpanStub {
	t.Helper()
	for _, span := range spans {
		if span.Name == name {
			return span
		}
	}
	t.Fatalf("span %q not found", name)
	return tracetest.SpanStub{}
}

func stringAttribute(attributes []attribute.KeyValue, key string) string {
	for _, item := range attributes {
		if string(item.Key) == key {
			return item.Value.AsString()
		}
	}
	return ""
}

func int64Attribute(attributes []attribute.KeyValue, key string) (int64, bool) {
	for _, item := range attributes {
		if string(item.Key) == key {
			return item.Value.AsInt64(), true
		}
	}
	return 0, false
}
