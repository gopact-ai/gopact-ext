//go:build integration

package agnes

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact/gopacttest/providerconformance"
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
