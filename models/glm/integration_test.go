//go:build integration

package glm

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact-ext/models/openai"
	"github.com/gopact-ai/gopact/gopacttest/providerconformance"
)

func TestGLMIntegrationChinaConformance(t *testing.T) {
	loadDotEnv(t)

	apiKey := firstEnv("GOPACT_GLM_API_KEY", "GLM_API_KEY", "GOPACT_LLM_TOKEN")
	model := firstEnv("GOPACT_GLM_MODEL", "GOPACT_LLM_MODEL")
	if apiKey == "" || model == "" {
		t.Skip("set GOPACT_GLM_API_KEY, GLM_API_KEY, or GOPACT_LLM_TOKEN; and set GOPACT_GLM_MODEL or GOPACT_LLM_MODEL")
	}

	client, err := NewClient(
		envOrDefault("GOPACT_GLM_BASEURL", DefaultBaseURL),
		apiKey,
		gopact.WithModel(model),
		gopact.WithMaxOutputTokens(512),
		gopact.WithTemperature(0.2),
		gopact.EnableStreaming(),
		DisableThinking(),
	)
	if err != nil {
		t.Fatal(err)
	}

	requireProviderConformance(t, client, model)
}

func TestGLMIntegrationInternationalConformance(t *testing.T) {
	loadDotEnv(t)

	apiKey := firstEnv("GOPACT_GLM_INTERNATIONAL_API_KEY", "GLM_API_KEY", "GOPACT_GLM_API_KEY", "GOPACT_LLM_TOKEN")
	model := firstEnv("GOPACT_GLM_MODEL", "GOPACT_LLM_MODEL")
	if apiKey == "" || model == "" {
		t.Skip("set GOPACT_GLM_INTERNATIONAL_API_KEY, GLM_API_KEY, GOPACT_GLM_API_KEY, or GOPACT_LLM_TOKEN; and set GOPACT_GLM_MODEL or GOPACT_LLM_MODEL")
	}

	client, err := NewInternationalClient(
		envOrDefault("GOPACT_GLM_INTERNATIONAL_BASEURL", DefaultInternationalBaseURL),
		apiKey,
		gopact.WithModel(model),
		gopact.WithMaxOutputTokens(512),
		gopact.WithTemperature(0.2),
		gopact.EnableStreaming(),
		DisableThinking(),
	)
	if err != nil {
		t.Fatal(err)
	}

	requireProviderConformance(t, client, model)
}

func requireProviderConformance(t *testing.T, provider *openai.Client, model string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	for _, result := range providerconformance.CheckProviderConformance(ctx, providerconformance.ProviderConformanceHarness{
		Provider: provider,
		Request: gopact.NewModelRequest(
			gopact.WithModel(model),
			gopact.WithMessages(gopact.UserMessage("Reply with exactly one short sentence.")),
			gopact.WithMaxOutputTokens(512),
			gopact.WithTemperature(0.2),
			DisableThinking(),
		),
	}) {
		if result.Err != nil {
			t.Fatalf("provider conformance %s failed: %v", result.Case, result.Err)
		}
	}
}

func loadDotEnv(t *testing.T) {
	t.Helper()
	path := filepath.Join("..", "..", ".env")
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		t.Fatalf("open .env: %v", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if key != "" && os.Getenv(key) == "" {
			t.Setenv(key, value)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("read .env: %v", err)
	}
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
