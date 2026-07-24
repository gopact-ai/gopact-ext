package fornax

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"net/http"
	"net/http/httptest"
	"slices"
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

const metadataStressTagCount = sdktrace.DefaultAttributeCountLimit * 2

func TestNewRequiresAKSK(t *testing.T) {
	if _, err := New(t.Context(), Config{}); err == nil || err.Error() != "fornax: AK is required" {
		t.Fatalf("New() error = %v, want AK required", err)
	}
	if _, err := New(t.Context(), Config{AK: "ak"}); err == nil || err.Error() != "fornax: SK is required" {
		t.Fatalf("New() error = %v, want SK required", err)
	}
}

func TestAuthenticateUsesAKSKAndResolvedSpaceID(t *testing.T) {
	type authRequestBody struct {
		PSM   string `json:"psm"`
		IsTCE bool   `json:"isTCE"`
	}
	authHeaders := make(chan http.Header, 2)
	authBodies := make(chan authRequestBody, 2)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, incoming *http.Request) {
		if incoming.URL.Path != authPath {
			t.Errorf("auth path = %q", incoming.URL.Path)
		}
		authHeaders <- incoming.Header.Clone()
		var body authRequestBody
		if err := json.NewDecoder(incoming.Body).Decode(&body); err != nil {
			t.Errorf("decode auth body: %v", err)
		}
		authBodies <- body
		_, _ = response.Write([]byte(fmt.Sprintf(`{"jwtToken":%q}`, fakeJWT(67890))))
	}))
	defer server.Close()

	auth, err := authenticateWithHost(t.Context(), Config{
		AK:             "ak-value",
		SK:             "sk-value",
		SpaceID:        "67890",
		Endpoint:       server.URL + defaultTracePath,
		PSM:            "demo.psm",
		CaptureContent: true,
	}, server.URL)
	if err != nil {
		t.Fatalf("authenticate() error = %v", err)
	}
	if auth.spaceID != "67890" {
		t.Fatalf("spaceID = %q, want 67890", auth.spaceID)
	}
	if auth.authorization != fakeJWT(67890) {
		t.Fatalf("authorization token mismatch")
	}
	if !auth.captureContent {
		t.Fatal("capture content = false, want explicit opt-in preserved")
	}

	headers := <-authHeaders
	if got := headers.Get("Agw-Js-Conv"); got != "str" {
		t.Fatalf("Agw-Js-Conv = %q, want str", got)
	}
	if got := headers.Get("Fornax-Auth"); !strings.HasPrefix(got, "auth-v1/ak-value/") {
		t.Fatalf("Fornax-Auth = %q, want auth-v1 prefix with AK", got)
	}
	body := <-authBodies
	if !body.IsTCE {
		t.Fatalf("auth body isTCE = false, want true")
	}
	if body.PSM != "demo.psm" {
		t.Fatalf("auth body psm = %q, want demo.psm", body.PSM)
	}

	_, err = authenticateWithHost(t.Context(), Config{
		AK:       "ak-value",
		SK:       "sk-value",
		SpaceID:  "1",
		Endpoint: server.URL + defaultTracePath,
	}, server.URL)
	if err == nil || err.Error() != "fornax: space ID mismatch: configured 1, authenticated 67890" {
		t.Fatalf("space ID mismatch error = %v", err)
	}
}

func TestExporterRefreshesTokenAndRetriesUnauthorizedRequest(t *testing.T) {
	initialToken := fakeJWT(67890)
	refreshedToken := initialToken + ".refreshed"
	var authCalls atomic.Int64
	var traceCalls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, incoming *http.Request) {
		if incoming.URL.Path == authPath {
			token := initialToken
			if authCalls.Add(1) > 1 {
				token = refreshedToken
			}
			_, _ = response.Write([]byte(fmt.Sprintf(`{"jwtToken":%q}`, token)))
			return
		}
		if incoming.URL.Path != defaultTracePath {
			t.Errorf("request path = %q", incoming.URL.Path)
			response.WriteHeader(http.StatusNotFound)
			return
		}
		traceCalls.Add(1)
		authorization := incoming.Header.Get("Authorization")
		if authorization == initialToken {
			response.WriteHeader(http.StatusUnauthorized)
			return
		}
		if authorization != refreshedToken {
			t.Errorf("Authorization = %q, want initial or refreshed token", authorization)
			response.WriteHeader(http.StatusUnauthorized)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"code":0}`))
	}))
	defer server.Close()

	auth, err := authenticateWithHost(t.Context(), Config{
		AK:       "ak-value",
		SK:       "sk-value",
		Endpoint: server.URL + defaultTracePath,
	}, server.URL)
	if err != nil {
		t.Fatalf("authenticate() error = %v", err)
	}
	if auth.captureContent {
		t.Fatal("capture content = true, want zero-value configuration disabled")
	}
	middleware, err := newWithAuth(t.Context(), auth)
	if err != nil {
		t.Fatalf("newWithAuth() error = %v", err)
	}
	if _, err := middleware.Use(testAgent{}).Invoke(t.Context(), agent.Request{}); err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if err := middleware.Close(t.Context()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if got := authCalls.Load(); got != 2 {
		t.Fatalf("authentication requests = %d, want 2", got)
	}
	if got := traceCalls.Load(); got != 2 {
		t.Fatalf("trace requests = %d, want 2", got)
	}
}

func TestNewWithAuthUsesExplicitFornaxConfiguration(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "http://127.0.0.1:1/ambient")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_HEADERS", "Authorization=ambient,cozeloop-workspace-id=999")

	type request struct {
		authorization string
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
			path:          incoming.URL.Path,
			body:          body,
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"code":0}`))
	}))
	defer server.Close()

	middleware, err := newWithAuth(t.Context(), authConfig{
		spaceID:        "12345",
		endpoint:       server.URL + defaultTracePath,
		authorization:  "explicit-token",
		psm:            "demo.psm",
		userID:         "default-user",
		deviceID:       "default-device",
		captureContent: true,
		metadata: map[string]string{
			"tenant": "tenant-1",
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx := WithMetadata(WithDeviceID(WithUserID(t.Context(), "user-1"), "device-1"), map[string]string{
		"request_id":      "request-1",
		spanTypeAttribute: "bad",
	})
	if _, err := middleware.Use(testAgent{}).Invoke(ctx, agent.Request{}); err != nil {
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
	if got.authorization != "explicit-token" {
		t.Fatalf("Authorization = %q, want explicit value", got.authorization)
	}
	if got.path != defaultTracePath {
		t.Fatalf("request path = %q", got.path)
	}
	if len(got.body) == 0 {
		t.Fatal("trace request body is empty")
	}
	var payload traceIngestRequest
	if err := json.Unmarshal(got.body, &payload); err != nil {
		t.Fatalf("trace request body is not Fornax JSON: %v", err)
	}
	if len(payload.Spans) == 0 {
		t.Fatal("trace request spans are empty")
	}
	if payload.Spans[0].WorkspaceID != "12345" {
		t.Fatalf("workspace ID = %q, want 12345", payload.Spans[0].WorkspaceID)
	}
	rootFound := false
	for _, span := range payload.Spans {
		if span.ServiceName != "demo.psm" {
			t.Fatalf("%s service_name = %q, want demo.psm", span.SpanName, span.ServiceName)
		}
		if span.SystemTagsString[languageSystemTag] != "go" {
			t.Fatalf("%s language system tag = %q, want go", span.SpanName, span.SystemTagsString[languageSystemTag])
		}
		if span.TagsString[spaceIDTag] != "12345" {
			t.Fatalf("%s fornax_space_id tag = %q, want 12345", span.SpanName, span.TagsString[spaceIDTag])
		}
		if span.TagsLong[durationTag] < 0 {
			t.Fatalf("%s duration tag = %d, want non-negative", span.SpanName, span.TagsLong[durationTag])
		}
		if span.TagsString[psmAttribute] != "demo.psm" {
			t.Fatalf("%s psm tag = %q, want demo.psm", span.SpanName, span.TagsString[psmAttribute])
		}
		if span.TagsString[userIDAttribute] != "user-1" {
			t.Fatalf("%s user_id tag = %q, want user-1", span.SpanName, span.TagsString[userIDAttribute])
		}
		if span.TagsString[deviceIDAttribute] != "device-1" {
			t.Fatalf("%s device_id tag = %q, want device-1", span.SpanName, span.TagsString[deviceIDAttribute])
		}
		if span.TagsString["tenant"] != "tenant-1" {
			t.Fatalf("%s tenant tag = %q, want tenant-1", span.SpanName, span.TagsString["tenant"])
		}
		if span.TagsString["request_id"] != "request-1" {
			t.Fatalf("%s request_id tag = %q, want request-1", span.SpanName, span.TagsString["request_id"])
		}
		if rootUploadSpan(t, span) {
			rootFound = true
		}
	}
	if !rootFound {
		t.Fatal("root span was not exported")
	}
}

func TestMetadataCannotOverrideTraceProtocolAttributes(t *testing.T) {
	reserved := []string{
		agentNameAttribute, cutOffAttribute, deviceIDAttribute, durationTag, errorAttribute,
		finishReasonAttribute, inputTokensAttribute, languageSystemTag, messageIDAttribute,
		modelNameAttribute, outputTokensAttribute, psmAttribute, threadIDAttribute,
		totalTokensAttribute, toolCallIDAttribute, toolNameAttribute, userIDAttribute,
		spanTypeAttribute, inputAttribute, outputAttribute, statusAttribute,
		runIDAttribute, parentRunIDAttribute, nodeIDAttribute, activationIDAttribute,
		attemptIDAttribute, spaceIDTag, psmFirstSpanTag,
		"cozeloop.future", "gopact.future", "fornax_future",
	}
	for _, key := range reserved {
		if !reservedMetadataKey(key) {
			t.Fatalf("reservedMetadataKey(%q) = false", key)
		}
	}
	for _, key := range []string{
		"tenant", "app.run_id", "cozeloopx.input", "gopactx.run_id", "fornaxx_space_id",
	} {
		if reservedMetadataKey(key) {
			t.Fatalf("reservedMetadataKey(%q) = true", key)
		}
	}

	configMetadata := map[string]string{
		"config_tag": "config-value",
		"shared":     "config-value",
	}
	requestMetadata := map[string]string{
		"request_tag": "request-value",
		"shared":      "request-value",
	}
	contextMetadata := map[string]string{
		"context_tag": "context-value",
		"shared":      "context-value",
	}
	for _, key := range reserved {
		configMetadata[key] = "config-override"
		requestMetadata[key] = "request-override"
		contextMetadata[key] = "context-override"
	}
	exporter := tracetest.NewInMemoryExporter()
	provider := newTestTracerProvider(exporter)
	middleware := newMiddleware(provider, traceTags(authConfig{
		psm:      "demo.psm",
		userID:   "default-user",
		deviceID: "default-device",
		metadata: configMetadata,
	}), false)
	t.Cleanup(func() { _ = middleware.Close(t.Context()) })

	ctx := WithMetadata(t.Context(), contextMetadata)
	ctx = WithUserID(ctx, "context-user")
	ctx = WithDeviceID(ctx, "context-device")
	if _, err := middleware.Use(testAgent{}).Invoke(
		ctx,
		agent.Request{Metadata: requestMetadata},
		gopact.WithRunID("option-run"),
		gopact.WithSessionID("option-session"),
	); err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}

	spans := exporter.GetSpans()
	for _, span := range spans {
		if got := stringAttribute(span.Attributes, psmAttribute); got != "demo.psm" {
			t.Fatalf("%s psm = %q, want demo.psm", span.Name, got)
		}
		if got := stringAttribute(span.Attributes, userIDAttribute); got != "context-user" {
			t.Fatalf("%s user_id = %q, want context-user", span.Name, got)
		}
		if got := stringAttribute(span.Attributes, deviceIDAttribute); got != "context-device" {
			t.Fatalf("%s device_id = %q, want context-device", span.Name, got)
		}
		assertStringAttributes(t, span, map[string]string{
			"config_tag":  "config-value",
			"request_tag": "request-value",
			"context_tag": "context-value",
			"shared":      "context-value",
		})
		if got := stringAttribute(span.Attributes, errorAttribute); got != "" {
			t.Fatalf("%s error = %q, want empty", span.Name, got)
		}
	}

	root := spanNamedType(t, spans, "react", rootSpanType)
	if got := stringAttribute(root.Attributes, runIDAttribute); got != "run-1" {
		t.Fatalf("root run_id = %q, want run-1", got)
	}
	if got := stringAttribute(root.Attributes, messageIDAttribute); got != "run-1" {
		t.Fatalf("root message_id = %q, want run-1", got)
	}
	if got := stringAttribute(root.Attributes, threadIDAttribute); got != "session-1" {
		t.Fatalf("root thread_id = %q, want session-1", got)
	}
	if got := stringAttribute(root.Attributes, agentNameAttribute); got != "react" {
		t.Fatalf("root agent_name = %q, want react", got)
	}

	child := spanNamed(t, spans, "child")
	if got := stringAttribute(child.Attributes, runIDAttribute); got != "run-2" {
		t.Fatalf("child run_id = %q, want run-2", got)
	}
	if got := stringAttribute(child.Attributes, parentRunIDAttribute); got != "run-1" {
		t.Fatalf("child parent_run_id = %q, want run-1", got)
	}

	model := spanNamed(t, spans, "model")
	if got := stringAttribute(model.Attributes, nodeIDAttribute); got != "model" {
		t.Fatalf("model node_id = %q, want model", got)
	}
	if got := stringAttribute(model.Attributes, activationIDAttribute); got != "act-1" {
		t.Fatalf("model activation_id = %q, want act-1", got)
	}
	if got := stringAttribute(model.Attributes, attemptIDAttribute); got != "act-1/attempt-1" {
		t.Fatalf("model attempt_id = %q, want act-1/attempt-1", got)
	}

	tool := spanNamed(t, spans, "tool")
	if got := stringAttribute(tool.Attributes, toolNameAttribute); got != "tool" {
		t.Fatalf("tool name = %q, want tool", got)
	}
	if got := stringAttribute(tool.Attributes, toolCallIDAttribute); got != "" {
		t.Fatalf("tool call ID = %q, want empty", got)
	}
}

func TestMetadataBudgetPreservesLateProtocolAttributes(t *testing.T) {
	requestMetadata := make(map[string]string, metadataStressTagCount)
	for index := range metadataStressTagCount {
		requestMetadata[fmt.Sprintf("custom.%03d", index)] = "request-value"
	}

	exporter := tracetest.NewInMemoryExporter()
	provider := newTestTracerProvider(exporter)
	middleware := newMiddleware(provider, traceTags(authConfig{
		psm:      "demo.psm",
		metadata: map[string]string{"config.low-priority": "config-value"},
	}), true)
	t.Cleanup(func() { _ = middleware.Close(t.Context()) })

	ctx := WithMetadata(t.Context(), map[string]string{"context.high-priority": "context-value"})
	if _, err := middleware.Use(componentEventAgent{}).Invoke(ctx, agent.Request{
		Messages: []gopact.Message{gopact.UserMessage("hello")},
		Metadata: requestMetadata,
	}); err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}

	spans := exporter.GetSpans()
	for _, span := range spans {
		if span.DroppedAttributes != 0 {
			t.Fatalf("%s dropped %d protocol or bounded metadata attributes", span.Name, span.DroppedAttributes)
		}
		if got := stringAttribute(span.Attributes, "context.high-priority"); got != "context-value" {
			t.Fatalf("%s context metadata = %q, want context-value", span.Name, got)
		}
		if got := stringAttribute(span.Attributes, "custom.062"); got != "request-value" {
			t.Fatalf("%s final selected request metadata = %q, want request-value", span.Name, got)
		}
		if got := stringAttribute(span.Attributes, "custom.063"); got != "" {
			t.Fatalf("%s metadata beyond budget = %q, want empty", span.Name, got)
		}
		if got := stringAttribute(span.Attributes, "config.low-priority"); got != "" {
			t.Fatalf("%s lower-priority metadata = %q, want empty", span.Name, got)
		}
	}

	model := spanNamed(t, spans, "model")
	if got := stringAttribute(model.Attributes, modelNameAttribute); got != "demo-model" {
		t.Fatalf("model_name = %q, want demo-model", got)
	}
	if got, ok := int64Attribute(model.Attributes, totalTokensAttribute); !ok || got != 3 {
		t.Fatalf("tokens = %d, want 3", got)
	}
	if got := stringAttribute(model.Attributes, finishReasonAttribute); got != "tool_calls" {
		t.Fatalf("finish_reason = %q, want tool_calls", got)
	}
	tool := spanNamed(t, spans, "tool")
	if got := stringAttribute(tool.Attributes, toolNameAttribute); got != "lookup" {
		t.Fatalf("tool_name = %q, want lookup", got)
	}
	if got := stringAttribute(tool.Attributes, toolCallIDAttribute); got != "call-1" {
		t.Fatalf("tool_call_id = %q, want call-1", got)
	}

	if _, err := middleware.Use(failingTestAgent{}).Invoke(ctx, agent.Request{
		Metadata: requestMetadata,
	}); !errors.Is(err, errTestAgent) {
		t.Fatalf("Invoke() error = %v, want test error", err)
	}
	failingRoot := spanNamedType(t, exporter.GetSpans(), "failing", rootSpanType)
	if got := stringAttribute(failingRoot.Attributes, errorAttribute); got != errTestAgent.Error() {
		t.Fatalf("error = %q, want %q", got, errTestAgent)
	}
}

func rootUploadSpan(t *testing.T, span uploadSpan) bool {
	t.Helper()
	if span.SpanType == rootSpanType {
		assertRootUploadSpan(t, span)
		return true
	}
	assertNotRootUploadSpan(t, span)
	return false
}

func assertRootUploadSpan(t *testing.T, span uploadSpan) {
	t.Helper()
	if span.ParentID != "0" {
		t.Fatalf("root parent_id = %q, want 0", span.ParentID)
	}
	if !span.TagsBool[psmFirstSpanTag] {
		t.Fatal("root fornax_psm_first_span tag = false, want true")
	}
	if span.SpanType != rootSpanType {
		t.Fatalf("reserved metadata overwrote span type: %q", span.SpanType)
	}
	if span.Input == "" || span.Output == "" {
		t.Fatal("explicit content capture did not export root input/output")
	}
}

func assertNotRootUploadSpan(t *testing.T, span uploadSpan) {
	t.Helper()
	if span.TagsBool[psmFirstSpanTag] {
		t.Fatalf("%s unexpectedly has fornax_psm_first_span", span.SpanName)
	}
}

func TestMiddlewareReportsAgentAndWorkflowSpans(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := newTestTracerProvider(exporter)
	middleware := newMiddleware(provider, nil, true)
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
	if len(spans) != 5 {
		t.Fatalf("reported %d spans, want 5", len(spans))
	}
	root := spanNamedType(t, spans, "react", rootSpanType)
	agentSpan := spanNamedType(t, spans, "react", agentSpanType)
	model := spanNamed(t, spans, "model")
	child := spanNamed(t, spans, "child")
	tool := spanNamed(t, spans, "tool")
	for _, span := range spans {
		if got := stringAttribute(span.Attributes, threadIDAttribute); got != "session-1" {
			t.Fatalf("%s thread_id = %q, want session-1", span.Name, got)
		}
	}
	if agentSpan.Parent.SpanID() != root.SpanContext.SpanID() {
		t.Fatal("agent span is not a child of the root query span")
	}
	if model.Parent.SpanID() != agentSpan.SpanContext.SpanID() {
		t.Fatal("model span is not a child of the agent span")
	}
	if child.Parent.SpanID() != agentSpan.SpanContext.SpanID() {
		t.Fatal("nested Agent span is not a child of the root Agent span")
	}
	if tool.Parent.SpanID() != child.SpanContext.SpanID() {
		t.Fatal("tool span is not a child of the nested Agent span")
	}
	if got := stringAttribute(root.Attributes, "cozeloop.span_type"); got != "fornax_query" {
		t.Fatalf("root span type = %q, want fornax_query", got)
	}
	if got := stringAttribute(agentSpan.Attributes, "cozeloop.span_type"); got != "agent" {
		t.Fatalf("agent span type = %q, want agent", got)
	}
	if got := stringAttribute(root.Attributes, "message_id"); got != "run-1" {
		t.Fatalf("message_id = %q, want run-1", got)
	}
	if got := stringAttribute(root.Attributes, "thread_id"); got != "session-1" {
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

func TestMiddlewareDefaultsToMetadataOnly(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := newTestTracerProvider(exporter)
	middleware := newMiddleware(provider, nil, false)
	t.Cleanup(func() { _ = middleware.Close(t.Context()) })

	if _, err := middleware.Use(componentEventAgent{}).Invoke(t.Context(), agent.Request{
		Messages: []gopact.Message{gopact.UserMessage("private-message")},
		Metadata: map[string]string{
			modelNameAttribute:    "metadata-model",
			totalTokensAttribute:  "999",
			finishReasonAttribute: "metadata-finish",
			toolNameAttribute:     "metadata-tool",
			toolCallIDAttribute:   "metadata-call",
		},
	}); err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}

	spans := exporter.GetSpans()
	model := spanNamed(t, spans, "model")
	tool := spanNamed(t, spans, "tool")
	if got := stringAttribute(model.Attributes, modelNameAttribute); got != "demo-model" {
		t.Fatalf("model_name = %q, want demo-model", got)
	}
	if got, ok := int64Attribute(model.Attributes, totalTokensAttribute); !ok || got != 3 {
		t.Fatalf("tokens = %d, want 3", got)
	}
	if got := stringAttribute(model.Attributes, finishReasonAttribute); got != "tool_calls" {
		t.Fatalf("finish_reason = %q, want tool_calls", got)
	}
	if got := stringAttribute(tool.Attributes, toolNameAttribute); got != "lookup" {
		t.Fatalf("tool_name = %q, want lookup", got)
	}
	if got := stringAttribute(tool.Attributes, toolCallIDAttribute); got != "call-1" {
		t.Fatalf("tool_call_id = %q, want call-1", got)
	}
	for _, span := range spans {
		assertMetadataOnly(t, span)
	}
}

func TestMiddlewareEnrichesNodeSpansWithComponentEvents(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := newTestTracerProvider(exporter)
	middleware := newMiddleware(provider, nil, true)
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
	if got := stringAttribute(model.Attributes, inputAttribute); !strings.Contains(got, `"messages"`) || !strings.Contains(got, `"hello"`) {
		t.Fatalf("model input = %q, want Fornax model input payload", got)
	}
	if got := stringAttribute(model.Attributes, outputAttribute); !strings.Contains(got, "assistant") {
		t.Fatalf("model output = %q, want response payload", got)
	}
	var output modelOutputPayload
	if err := json.Unmarshal([]byte(stringAttribute(model.Attributes, outputAttribute)), &output); err != nil {
		t.Fatalf("model output is not JSON: %v", err)
	}
	if len(output.Choices) != 1 || output.Choices[0].Message.Content != "use tool" {
		t.Fatalf("model output content = %+v, want content field", output)
	}
	if got := stringAttribute(tool.Attributes, spanTypeAttribute); got != "tool" {
		t.Fatalf("tool span type = %q, want tool", got)
	}
	if got := stringAttribute(tool.Attributes, toolCallIDAttribute); got != "call-1" {
		t.Fatalf("tool_call_id = %q, want call-1", got)
	}
	if got := stringAttribute(tool.Attributes, inputAttribute); !strings.Contains(got, `"query":"hello"`) {
		t.Fatalf("tool input = %q, want arguments payload", got)
	}
	if got := stringAttribute(tool.Attributes, outputAttribute); !strings.Contains(got, "tool-result") {
		t.Fatalf("tool output = %q, want outcome payload", got)
	}
}

func TestMiddlewareReportsDirectModelSpan(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := newTestTracerProvider(exporter)
	middleware := newMiddleware(provider, nil, true)
	t.Cleanup(func() { _ = middleware.Close(t.Context()) })

	response, err := middleware.Use(directModelEventAgent{}).Invoke(
		t.Context(),
		agent.Request{Messages: []gopact.Message{gopact.UserMessage("hello")}},
		gopact.WithSessionID("thread-1"),
		gopact.WithRunID("message-1"),
	)
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if got := messageText(response.Message); got != "world" {
		t.Fatalf("Invoke() text = %q, want world", got)
	}

	spans := exporter.GetSpans()
	root := spanNamedType(t, spans, "direct", rootSpanType)
	model := spanNamedType(t, spans, "model", modelSpanType)
	agentSpan := spanNamedType(t, spans, "direct", agentSpanType)
	if got := stringAttribute(root.Attributes, messageIDAttribute); got != "message-1" {
		t.Fatalf("root message_id = %q, want message-1", got)
	}
	if got := stringAttribute(root.Attributes, threadIDAttribute); got != "thread-1" {
		t.Fatalf("root thread_id = %q, want thread-1", got)
	}
	if model.Parent.SpanID() != agentSpan.SpanContext.SpanID() {
		t.Fatal("model span is not a child of the direct Agent span")
	}
	if got := stringAttribute(model.Attributes, threadIDAttribute); got != "thread-1" {
		t.Fatalf("model thread_id = %q, want thread-1", got)
	}
	if got := stringAttribute(model.Attributes, modelNameAttribute); got != "demo-model" {
		t.Fatalf("model_name = %q, want demo-model", got)
	}
	if got := stringAttribute(model.Attributes, inputAttribute); !strings.Contains(got, "hello") {
		t.Fatalf("model input = %q, want hello", got)
	}
	var output modelOutputPayload
	if err := json.Unmarshal([]byte(stringAttribute(model.Attributes, outputAttribute)), &output); err != nil {
		t.Fatalf("model output is not JSON: %v", err)
	}
	if len(output.Choices) != 1 || output.Choices[0].Message.Content != "world" {
		t.Fatalf("model output = %+v, want world", output)
	}
	if got, ok := int64Attribute(model.Attributes, totalTokensAttribute); !ok || got != 3 {
		t.Fatalf("tokens = %d, want 3", got)
	}
	if got := stringAttribute(model.Attributes, finishReasonAttribute); got != "stop" {
		t.Fatalf("finish_reason = %q, want stop", got)
	}
}

func TestActiveNodeKeysMatchWorkflowEventIDs(t *testing.T) {
	const attempt = 2
	tests := []struct {
		name string
		info workflow.RunInfo
		want []string
	}{
		{
			name: "runtime run info",
			info: workflow.RunInfo{RunID: "run-1", ActivationID: "act-1"},
			want: []string{"run-1\x00act-1", "run-1\x00run-1/act-1"},
		},
		{
			name: "retry attempt",
			info: workflow.RunInfo{RunID: "run-1", ActivationID: "act-1", Attempt: attempt},
			want: []string{"run-1\x00act-1/attempt-2", "run-1\x00run-1/act-1/attempt-2"},
		},
		{
			name: "prefixed activation",
			info: workflow.RunInfo{RunID: "run-1", ActivationID: "run-1/act-1"},
			want: []string{"run-1\x00run-1/act-1"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := activeNodeKeys(tt.info); !slices.Equal(got, tt.want) {
				t.Fatalf("activeNodeKeys() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestUseStreamingReportsOutput(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := newTestTracerProvider(exporter)
	middleware := newMiddleware(provider, nil, true)
	t.Cleanup(func() { _ = middleware.Close(t.Context()) })

	target, ok := middleware.Use(streamingTestAgent{}).(agent.StreamingAgent)
	if !ok {
		t.Fatal("Use() removed StreamingAgent support")
	}
	var chunks []agent.Chunk
	request := agent.Request{Metadata: map[string]string{
		"chat_id": "chat-1",
	}}
	ctx := WithUserID(t.Context(), "user-1")
	for chunk, err := range target.InvokeStream(
		ctx,
		request,
		gopact.WithSessionID("thread-1"),
		gopact.WithRunID("message-1"),
	) {
		if err != nil {
			t.Fatalf("InvokeStream() error = %v", err)
		}
		chunks = append(chunks, chunk)
	}
	if len(chunks) != 2 || chunks[0].Text != "first" || chunks[1].Text != "second" {
		t.Fatalf("InvokeStream() chunks = %+v", chunks)
	}
	spans := exporter.GetSpans()
	root := spanNamedType(t, spans, "react", rootSpanType)
	agentSpan := spanNamedType(t, spans, "react", agentSpanType)
	for _, span := range []tracetest.SpanStub{root, agentSpan} {
		if got := stringAttribute(span.Attributes, threadIDAttribute); got != "thread-1" {
			t.Fatalf("%s thread_id = %q, want thread-1", span.Name, got)
		}
		if got := stringAttribute(span.Attributes, userIDAttribute); got != "user-1" {
			t.Fatalf("%s user_id = %q, want user-1", span.Name, got)
		}
		if got := stringAttribute(span.Attributes, "chat_id"); got != "chat-1" {
			t.Fatalf("%s chat_id = %q, want chat-1", span.Name, got)
		}
	}
	if got := stringAttribute(root.Attributes, messageIDAttribute); got != "message-1" {
		t.Fatalf("message_id = %q, want message-1", got)
	}
	var output queryPayload
	if err := json.Unmarshal([]byte(stringAttribute(root.Attributes, outputAttribute)), &output); err != nil {
		t.Fatalf("stream output is not Fornax query JSON: %v", err)
	}
	if len(output.Contents) != 1 || output.Contents[0].Text != "firstsecond" {
		t.Fatalf("stream output = %+v, want one firstsecond text content", output)
	}
	var agentText string
	if err := json.Unmarshal([]byte(stringAttribute(agentSpan.Attributes, outputAttribute)), &agentText); err != nil {
		t.Fatalf("agent output is not a JSON string: %v", err)
	}
	if agentText != "firstsecond" {
		t.Fatalf("agent output = %q, want firstsecond", agentText)
	}
}

func TestStreamingDefaultsToMetadataOnly(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := newTestTracerProvider(exporter)
	middleware := newMiddleware(provider, nil, false)
	t.Cleanup(func() { _ = middleware.Close(t.Context()) })

	var text strings.Builder
	for chunk, err := range middleware.UseStreaming(streamingTestAgent{}).InvokeStream(t.Context(), agent.Request{
		Messages: []gopact.Message{gopact.UserMessage("private-stream-input")},
	}) {
		if err != nil {
			t.Fatalf("InvokeStream() error = %v", err)
		}
		text.WriteString(chunk.Text)
	}
	if got := text.String(); got != "firstsecond" {
		t.Fatalf("stream output = %q, want firstsecond", got)
	}
	for _, span := range exporter.GetSpans() {
		assertMetadataOnly(t, span)
	}
}

func TestStreamingConsumerStopEndsTrace(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := newTestTracerProvider(exporter)
	middleware := newMiddleware(provider, nil, false)
	t.Cleanup(func() { _ = middleware.Close(t.Context()) })

	for range middleware.UseStreaming(streamingTestAgent{}).InvokeStream(t.Context(), agent.Request{}) {
		break
	}
	root := spanNamedType(t, exporter.GetSpans(), "react", rootSpanType)
	if root.Status.Code != codes.Error {
		t.Fatalf("root status = %v, want error", root.Status.Code)
	}
	if got, ok := int64Attribute(root.Attributes, "cozeloop.status_code"); !ok || got != failedStatusCode {
		t.Fatalf("root status code = %d, want %d", got, failedStatusCode)
	}
}

func TestStreamingTraceOutputIsBounded(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := newTestTracerProvider(exporter)
	middleware := newMiddleware(provider, nil, true)
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
	root := spanNamedType(t, exporter.GetSpans(), "react", rootSpanType)
	output := stringAttribute(root.Attributes, outputAttribute)
	if len(output) > maxTraceFieldBytes {
		t.Fatalf("stream trace output bytes = %d, limit = %d", len(output), maxTraceFieldBytes)
	}
	if output != "" && !json.Valid([]byte(output)) {
		t.Fatalf("stream trace output is invalid JSON: %q", output)
	}
	if got := stringAttribute(root.Attributes, cutOffAttribute); got != `["output"]` {
		t.Fatalf("cut_off = %q, want [\"output\"]", got)
	}
}

func TestUnaryTracePayloadsAreBounded(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := newTestTracerProvider(exporter)
	middleware := newMiddleware(provider, nil, true)
	t.Cleanup(func() { _ = middleware.Close(t.Context()) })

	largeText := strings.Repeat("x", maxTraceFieldBytes)
	request := agent.Request{Messages: []gopact.Message{{
		Role:  gopact.MessageRoleUser,
		Parts: []gopact.MessagePart{{Type: gopact.MessagePartTypeText, Text: largeText}},
	}}, Metadata: make(map[string]string, metadataStressTagCount)}
	for index := range metadataStressTagCount {
		request.Metadata[fmt.Sprintf("custom.%03d", index)] = "value"
	}
	if _, err := middleware.Use(largeTestAgent{output: largeText}).Invoke(t.Context(), request); err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	root := spanNamedType(t, exporter.GetSpans(), "react", rootSpanType)
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
	tests := []struct {
		name       string
		capture    bool
		wantDetail bool
	}{
		{name: "metadata-only", capture: false, wantDetail: false},
		{name: "content-capture", capture: true, wantDetail: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			exporter := tracetest.NewInMemoryExporter()
			provider := newTestTracerProvider(exporter)
			middleware := newMiddleware(provider, nil, test.capture)
			t.Cleanup(func() { _ = middleware.Close(t.Context()) })

			request := agent.Request{Metadata: map[string]string{errorAttribute: "metadata-override"}}
			if _, err := middleware.Use(failingTestAgent{}).Invoke(t.Context(), request); !errors.Is(err, errTestAgent) {
				t.Fatalf("Invoke() error = %v, want test error", err)
			}
			spans := exporter.GetSpans()
			if len(spans) != 3 {
				t.Fatalf("reported %d spans, want 3", len(spans))
			}
			for _, span := range []tracetest.SpanStub{
				spanNamedType(t, spans, "failing", rootSpanType),
				spanNamedType(t, spans, "failing", agentSpanType),
				spanNamed(t, spans, "model"),
			} {
				assertErrorMetadata(t, span)
				assertErrorDetail(t, span, test.wantDetail)
			}
		})
	}
}

func assertErrorMetadata(t *testing.T, span tracetest.SpanStub) {
	t.Helper()
	if span.Status.Code != codes.Error {
		t.Fatalf("%s status = %v, want error", span.Name, span.Status.Code)
	}
	if got, ok := int64Attribute(span.Attributes, statusAttribute); !ok || got != failedStatusCode {
		t.Fatalf("%s status code = %d, want %d", span.Name, got, failedStatusCode)
	}
}

func assertErrorDetail(t *testing.T, span tracetest.SpanStub, want bool) {
	t.Helper()
	detail := stringAttribute(span.Attributes, "error")
	uploaded := uploadSpanFrom(span.Snapshot(), "space", "service")
	wireDetail := uploaded.TagsString["error"]
	if want {
		if detail == "" || wireDetail == "" {
			t.Fatalf("%s error detail is incomplete: attribute=%q wire=%q", span.Name, detail, wireDetail)
		}
		return
	}
	if detail != "" || wireDetail != "" {
		t.Fatalf("%s error detail leaked: attribute=%q wire=%q", span.Name, detail, wireDetail)
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

type directModelEventAgent struct{}

func (directModelEventAgent) Identity() agent.Identity {
	return agent.Identity{Name: "direct", Description: "direct model events", Version: "test"}
}

func (directModelEventAgent) Invoke(ctx context.Context, request agent.Request, options ...gopact.RunOption) (agent.Response, error) {
	modelRequest := gopact.ModelRequest{Model: "demo-model", Messages: request.Messages}
	modelResponse := gopact.ModelResponse{Message: gopact.Message{
		Role: gopact.MessageRoleAssistant,
		Parts: []gopact.MessagePart{{
			Type: gopact.MessagePartTypeText,
			Text: "world",
		}},
	}}
	events := []gopact.ModelEvent{
		{Type: gopact.ModelEventCallStarted, Request: &modelRequest},
		{Type: gopact.ModelEventUsage, Payload: json.RawMessage(`{"input_tokens":1,"output_tokens":2,"total_tokens":3}`)},
		{Type: gopact.ModelEventFinish, Summary: "stop"},
		{Type: gopact.ModelEventCallFinished, Request: &modelRequest, Response: &modelResponse},
	}
	for _, sink := range gopact.ResolveRunOptions(options...).EventSinks {
		modelSink, ok := sink.(gopact.ModelEventSink)
		if !ok {
			continue
		}
		if err := emitDirectModelEvents(ctx, modelSink, events); err != nil {
			return agent.Response{}, err
		}
	}
	return agent.Response{Message: modelResponse.Message}, nil
}

func emitDirectModelEvents(ctx context.Context, sink gopact.ModelEventSink, events []gopact.ModelEvent) error {
	for _, event := range events {
		if err := sink.EmitModelEvent(ctx, event); err != nil {
			return err
		}
	}
	return nil
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

func spanNamedType(t *testing.T, spans tracetest.SpanStubs, name, spanType string) tracetest.SpanStub {
	t.Helper()
	for _, span := range spans {
		if span.Name == name && stringAttribute(span.Attributes, spanTypeAttribute) == spanType {
			return span
		}
	}
	t.Fatalf("span %q with span_type %q not found", name, spanType)
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

func assertStringAttributes(t *testing.T, span tracetest.SpanStub, values map[string]string) {
	t.Helper()
	for key, want := range values {
		if got := stringAttribute(span.Attributes, key); got != want {
			t.Fatalf("%s %s = %q, want %q", span.Name, key, got, want)
		}
	}
}

func newTestTracerProvider(exporter sdktrace.SpanExporter) *sdktrace.TracerProvider {
	limits := sdktrace.NewSpanLimits()
	limits.AttributeCountLimit = sdktrace.DefaultAttributeCountLimit
	return sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exporter),
		sdktrace.WithRawSpanLimits(limits),
	)
}

func int64Attribute(attributes []attribute.KeyValue, key string) (int64, bool) {
	for _, item := range attributes {
		if string(item.Key) == key {
			return item.Value.AsInt64(), true
		}
	}
	return 0, false
}

func assertMetadataOnly(t *testing.T, span tracetest.SpanStub) {
	t.Helper()
	for _, attr := range span.Attributes {
		if attr.Key == inputAttribute || attr.Key == outputAttribute || attr.Key == cutOffAttribute {
			t.Fatalf("%s reported content attribute %q by default", span.Name, attr.Key)
		}
		if attr.Value.Type() != attribute.STRING {
			continue
		}
		if content := reportedTraceContent(attr.Value.AsString()); content != "" {
			t.Fatalf("%s reported content %q in attribute %q by default", span.Name, content, attr.Key)
		}
	}
}

func reportedTraceContent(value string) string {
	for _, content := range []string{
		"private-message", "private-stream-input", `"query":"hello"`, "tool-result", "use tool", "done", "firstsecond",
	} {
		if strings.Contains(value, content) {
			return content
		}
	}
	return ""
}

func fakeJWT(spaceID int64) string {
	payload := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"space_id":%d}`, spaceID)))
	return "header." + payload + ".signature"
}
