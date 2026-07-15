package agnes

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gopact-ai/gopact"
)

func TestModelExposesOnlyDocumentedCapabilities(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"object": "list", "data": []map[string]any{{"id": "agnes-2.0-flash"}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	model, err := New("key", WithBaseURL(server.URL+"/v1"), WithInsecureHTTP())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := any(model).(gopact.Embedder); ok {
		t.Fatal("Agnes unexpectedly advertises undocumented embedding support")
	}
	models, err := model.ListModels(t.Context())
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	if len(models.Models) != 1 || models.Models[0].ID != "agnes-2.0-flash" {
		t.Fatalf("models = %+v", models.Models)
	}
}

func TestNewNormalizesRuntimeBaseURL(t *testing.T) {
	model, err := New("key", WithBaseURL("example.com/v1"))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if model.baseURL != "https://example.com/v1" {
		t.Fatalf("baseURL = %q", model.baseURL)
	}
}
