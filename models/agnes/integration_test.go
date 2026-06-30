//go:build integration

package agnes

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

func TestAgnesIntegrationFullFeature(t *testing.T) {
	loadDotEnv(t)

	apiKey := firstEnv("GOPACT_AGNES_API_KEY", "GOPACT_AGNES_SK")
	if apiKey == "" {
		t.Skip("set GOPACT_AGNES_API_KEY")
	}
	baseURL := envOrDefault("GOPACT_AGNES_BASEURL", DefaultBaseURL)
	model := envOrDefault("GOPACT_AGNES_MODEL", DefaultModel)

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
