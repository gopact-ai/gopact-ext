//go:build integration

package ark

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
	"github.com/gopact-ai/gopact/provider"
)

func TestArkIntegrationFullFeature(t *testing.T) {
	loadDotEnv(t)

	model := firstEnv("GOPACT_ARK_MODEL", "GOPACT_LLM_MODEL")
	if model == "" {
		t.Skip("set GOPACT_ARK_MODEL or GOPACT_LLM_MODEL")
	}
	client, err := New(Options{
		BaseURL:   firstEnv("GOPACT_ARK_BASEURL", "GOPACT_LLM_BASEURL"),
		Region:    envOrDefault("GOPACT_ARK_REGION", DefaultRegion),
		APIKey:    firstEnv("GOPACT_ARK_API_KEY", "GOPACT_LLM_TOKEN"),
		AccessKey: os.Getenv("GOPACT_ARK_ACCESS_KEY"),
		SecretKey: os.Getenv("GOPACT_ARK_SECRET_KEY"),
		Models:    []provider.ModelInfo{{Name: model, Provider: DefaultProvider}},
	})
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
