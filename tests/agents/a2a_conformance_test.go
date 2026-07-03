package agents

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gopact-ai/gopact/a2a"
	"github.com/gopact-ai/gopact/gopacttest/a2aconformance"
)

func TestDownstreamUsesA2ACardRegistrarConformance(t *testing.T) {
	store := a2a.NewRegistry()
	server := httptest.NewServer(a2a.NewHTTPRegistryHandler(store))
	defer server.Close()

	registry, err := a2a.NewHTTPRegistry(server.URL, a2a.WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatalf("NewHTTPRegistry() error = %v", err)
	}

	a2aconformance.RequireCardRegistrarConformance(t, a2aconformance.CardRegistrarConformanceHarness{
		Registrar: registry,
		Card: a2a.AgentCard{
			Name:         "reviewer",
			URL:          "http://127.0.0.1:8080",
			Capabilities: []string{"code.review"},
			Tags:         []string{"code"},
			Metadata:     map[string]any{"region": "local"},
		},
		TTL: time.Minute,
	})
}
