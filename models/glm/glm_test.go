package glm

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gopact-ai/gopact"
)

func TestModelRoutesDiscoveryAndEmbeddingToGeneralAPI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/models":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"object": "list", "data": []map[string]any{{"id": "glm-5"}},
			})
		case "/api/models/glm-5":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "glm-5", "object": "model", "owned_by": "z.ai", "created": 1,
			})
		case "/api/embeddings":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"model": "embedding-3", "data": []map[string]any{{"index": 0, "embedding": []float32{1, 2}}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	model, err := New(
		"key",
		WithChatBaseURL(server.URL+"/coding"),
		WithAPIBaseURL(server.URL+"/api"),
		WithInsecureHTTP(),
	)
	if err != nil {
		t.Fatal(err)
	}
	models, err := model.ListModels(t.Context())
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	if len(models.Models) != 1 || models.Models[0].ID != "glm-5" {
		t.Fatalf("models = %+v", models.Models)
	}
	detail, err := model.GetModel(t.Context(), "glm-5")
	if err != nil || detail.ID != "glm-5" || detail.OwnedBy != "z.ai" {
		t.Fatalf("GetModel() = %+v, %v", detail, err)
	}
	embedding, err := model.Embed(t.Context(), gopact.EmbeddingRequest{Input: []string{"hello"}})
	if err != nil {
		t.Fatalf("Embed() error = %v", err)
	}
	if embedding.Model != DefaultEmbeddingModel || len(embedding.Embeddings) != 1 {
		t.Fatalf("embedding = %+v", embedding)
	}
}

func TestNewNormalizesRuntimeBaseURLs(t *testing.T) {
	model, err := New(
		"key",
		WithAPIBaseURL("example.com/api"),
		WithMonitorBaseURL("monitor.example.com/usage"),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if model.apiBaseURL != "https://example.com/api" {
		t.Fatalf("apiBaseURL = %q", model.apiBaseURL)
	}
	if model.monitorURL != "https://monitor.example.com/usage" {
		t.Fatalf("monitorURL = %q", model.monitorURL)
	}
}
