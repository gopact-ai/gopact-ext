//go:build integration

package openai

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact/gopacttest/providerconformance"
)

func TestOpenAIIntegrationFullFeature(t *testing.T) {
	loadDotEnv(t)

	apiKey := firstEnv("GOPACT_OPENAI_API_KEY", "OPENAI_API_KEY")
	if apiKey == "" {
		t.Skip("set GOPACT_OPENAI_API_KEY")
	}
	model := firstEnv("GOPACT_OPENAI_MODEL", "OPENAI_MODEL")
	if model == "" {
		t.Skip("set GOPACT_OPENAI_MODEL")
	}
	baseURL := envOrDefault("GOPACT_OPENAI_BASEURL", "https://api.openai.com/v1")

	tests := []struct {
		name string
		api  Option
	}{
		{name: "chat completions", api: WithChatCompletionsAPI()},
		{name: "responses", api: WithResponsesAPI()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := NewClient(
				ProviderOpenAI,
				baseURL,
				apiKey,
				tt.api,
				gopact.WithModel(model),
				gopact.WithMaxOutputTokens(128),
				gopact.WithTemperature(0.2),
				gopact.EnableStreaming(),
			)
			if err != nil {
				t.Fatal(err)
			}

			requireProviderConformance(t, providerconformance.ProviderConformanceHarness{
				Provider: client,
				Request: gopact.NewModelRequest(
					gopact.WithModel(model),
					gopact.WithMessages(gopact.Message{Role: gopact.RoleUser, Content: "Reply with exactly one short sentence."}),
					gopact.WithMaxOutputTokens(128),
					gopact.WithTemperature(0.2),
				),
			})
		})
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
			defer file.Close()
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
