//go:build integration

package agnes

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact/gopacttest/providerconformance"
	"github.com/gopact-ai/gopact/provider"
)

func TestAgnesIntegrationFullFeature(t *testing.T) {
	loadDotEnv(t)

	apiKey := firstEnv("GOPACT_AGNES_API_KEY", "GOPACT_AGNES_SK", "GOPACT_LLM_TOKEN")
	if apiKey == "" {
		t.Skip("set GOPACT_AGNES_API_KEY or GOPACT_LLM_TOKEN")
	}
	baseURL, model := agnesEndpointConfig()

	tests := []struct {
		name string
		opt  gopact.ModelRequestOption
	}{
		{name: "thinking disabled", opt: DisableThinking()},
		{name: "thinking enabled", opt: EnableThinking()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := NewClient(
				baseURL,
				apiKey,
				gopact.WithModel(model),
				gopact.WithMaxOutputTokens(512),
				gopact.WithTemperature(0.2),
				gopact.EnableStreaming(),
				tt.opt,
			)
			if err != nil {
				t.Fatal(err)
			}

			requireProviderConformance(t, providerconformance.ProviderConformanceHarness{
				Provider: client,
				Request: gopact.NewModelRequest(
					gopact.WithModel(model),
					gopact.WithMessages(gopact.Message{Role: gopact.RoleUser, Content: "Reply with exactly one short sentence."}),
					gopact.WithMaxOutputTokens(512),
					gopact.WithTemperature(0.2),
					tt.opt,
				),
			})
		})
	}
}

func TestAgnesIntegrationStructuredOutput(t *testing.T) {
	loadDotEnv(t)

	apiKey := firstEnv("GOPACT_AGNES_API_KEY", "GOPACT_AGNES_SK", "GOPACT_LLM_TOKEN")
	if apiKey == "" {
		t.Skip("set GOPACT_AGNES_API_KEY or GOPACT_LLM_TOKEN")
	}
	baseURL, model := agnesEndpointConfig()
	client, err := NewClient(
		baseURL,
		apiKey,
		gopact.WithModel(model),
		gopact.WithMaxOutputTokens(512),
		gopact.WithTemperature(0.1),
		DisableThinking(),
		gopact.EnableStructuredOutput(),
	)
	if err != nil {
		t.Fatal(err)
	}

	schema := gopact.JSONSchema{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"status", "summary"},
		"properties": map[string]any{
			"status":  map[string]any{"type": "string", "const": "ok"},
			"summary": map[string]any{"type": "string", "minLength": 1},
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	response, err := client.Generate(ctx, gopact.NewModelRequest(
		gopact.WithMessages(gopact.UserMessage("Return a JSON object with status ok and a short summary about gopact structured output.")),
		gopact.WithResponseSchema(schema),
		gopact.WithMaxOutputTokens(512),
		gopact.WithTemperature(0.1),
		DisableThinking(),
		gopact.EnableStructuredOutput(),
	))
	if err != nil {
		t.Fatal(err)
	}

	var payload any
	if err := json.Unmarshal([]byte(strings.TrimSpace(response.Message.Text())), &payload); err != nil {
		t.Fatalf("structured output is not JSON: %v", err)
	}
	if err := gopact.ValidateJSONSchemaValue(schema, payload); err != nil {
		t.Fatalf("structured output schema validation failed: %v", err)
	}
}

func TestAgnesIntegrationToolCall(t *testing.T) {
	loadDotEnv(t)

	apiKey := firstEnv("GOPACT_AGNES_API_KEY", "GOPACT_AGNES_SK", "GOPACT_LLM_TOKEN")
	if apiKey == "" {
		t.Skip("set GOPACT_AGNES_API_KEY or GOPACT_LLM_TOKEN")
	}
	baseURL, model := agnesEndpointConfig()
	recorder := &recordingRoundTripper{base: http.DefaultTransport}
	client, err := NewClient(
		baseURL,
		apiKey,
		gopact.WithModel(model),
		gopact.WithMaxOutputTokens(512),
		gopact.WithTemperature(0.1),
		DisableThinking(),
		WithHTTPClient(&http.Client{Transport: recorder, Timeout: 2 * time.Minute}),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	response, err := client.Generate(ctx, gopact.NewModelRequest(
		gopact.WithMessages(
			gopact.SystemMessage("Use the requested tool. Do not answer directly."),
			gopact.UserMessage("Call lookup_status with item set to gopact."),
		),
		gopact.WithTools(gopact.ObjectToolSpec("lookup_status", "Lookup status for an item.", gopact.RequiredStringField("item", "Item name."))),
		gopact.WithAutoToolChoice(),
		gopact.WithMaxOutputTokens(512),
		gopact.WithTemperature(0.1),
		DisableThinking(),
	))
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Message.ToolCalls) != 1 {
		t.Fatalf("tool calls = %+v text=%q parts=%+v raw=%s, want one lookup_status call", response.Message.ToolCalls, response.Message.Text(), response.Message.Parts, summarizeRawResponse(recorder.last))
	}
	call := response.Message.ToolCalls[0]
	if call.Name != "lookup_status" || call.ID == "" {
		t.Fatalf("tool call = %+v, want named lookup_status with id", call)
	}
	var args struct {
		Item string `json:"item"`
	}
	if err := json.Unmarshal(call.Arguments, &args); err != nil {
		t.Fatalf("tool arguments are not JSON: %v", err)
	}
	if strings.TrimSpace(args.Item) == "" {
		t.Fatalf("tool arguments = %s, want non-empty item", string(call.Arguments))
	}
}

func TestAgnesIntegrationCancelAndTimeout(t *testing.T) {
	loadDotEnv(t)

	apiKey := firstEnv("GOPACT_AGNES_API_KEY", "GOPACT_AGNES_SK", "GOPACT_LLM_TOKEN")
	if apiKey == "" {
		t.Skip("set GOPACT_AGNES_API_KEY or GOPACT_LLM_TOKEN")
	}
	baseURL, model := agnesEndpointConfig()
	client, err := NewClient(
		baseURL,
		apiKey,
		gopact.WithModel(model),
		gopact.WithMaxOutputTokens(512),
		gopact.WithTemperature(0.2),
		DisableThinking(),
	)
	if err != nil {
		t.Fatal(err)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = client.Generate(canceled, gopact.NewModelRequest(
		gopact.WithMessages(gopact.UserMessage("This request should be canceled before network I/O.")),
	))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Generate() error = %v, want context.Canceled", err)
	}

	timedOut, timeoutCancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer timeoutCancel()
	_, err = client.Generate(timedOut, gopact.NewModelRequest(
		gopact.WithMessages(gopact.UserMessage("This request should time out before network I/O.")),
	))
	if provider.Classify(err) != provider.ErrorTimeout {
		t.Fatalf("timeout Generate() class = %q, want timeout; err = %v", provider.Classify(err), err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout Generate() error = %v, want context deadline cause", err)
	}
}

type recordingRoundTripper struct {
	base http.RoundTripper
	last []byte
}

func (t *recordingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	resp, err := base.RoundTrip(req)
	if err != nil || resp == nil || resp.Body == nil {
		return resp, err
	}
	raw, readErr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	t.last = append(t.last[:0], raw...)
	resp.Body = io.NopCloser(bytes.NewReader(raw))
	if readErr != nil {
		return resp, readErr
	}
	return resp, nil
}

func summarizeRawResponse(raw []byte) string {
	if len(raw) == 0 {
		return "<empty>"
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "<non-json>"
	}
	for _, key := range []string{"id", "model", "created", "system_fingerprint"} {
		delete(payload, key)
	}
	summary, err := json.Marshal(payload)
	if err != nil {
		return "<invalid-json>"
	}
	return string(summary)
}

func requireProviderConformance(t *testing.T, harness providerconformance.ProviderConformanceHarness) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	for _, result := range providerconformance.CheckProviderConformance(ctx, harness) {
		if !result.Passed {
			t.Fatalf("provider conformance case %q failed: %v", result.Case, result.Err)
		}
	}
}

func agnesEndpointConfig() (string, string) {
	if firstEnv("GOPACT_AGNES_API_KEY", "GOPACT_AGNES_SK") != "" {
		return envOrDefault("GOPACT_AGNES_BASEURL", DefaultBaseURL), envOrDefault("GOPACT_AGNES_MODEL", DefaultModel)
	}
	return envOrDefault("GOPACT_LLM_BASEURL", DefaultBaseURL), envOrDefault("GOPACT_LLM_MODEL", DefaultModel)
}

func loadDotEnv(t *testing.T) {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		path := filepath.Join(dir, ".env")
		file, err := os.Open(path)
		if err == nil {
			defer func() { _ = file.Close() }()
			scanner := bufio.NewScanner(file)
			for scanner.Scan() {
				setDotEnvLine(scanner.Text())
			}
			if err := scanner.Err(); err != nil {
				t.Fatal(err)
			}
			return
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return
		}
		dir = parent
	}
}

func setDotEnvLine(line string) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return
	}
	key, value, ok := strings.Cut(line, "=")
	if !ok {
		return
	}
	key = strings.TrimSpace(strings.TrimPrefix(key, "export "))
	if key == "" || os.Getenv(key) != "" {
		return
	}
	value = strings.TrimSpace(value)
	value = strings.Trim(value, `"'`)
	_ = os.Setenv(key, value)
}

func firstEnv(keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}

func envOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
