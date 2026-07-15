package openai

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gopact-ai/gopact"
)

func TestModelEmbed(t *testing.T) {
	var got struct {
		Model      string   `json:"model"`
		Input      []string `json:"input"`
		Dimensions int      `json:"dimensions"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/embeddings" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if authorization := r.Header.Get("Authorization"); authorization != "Bearer test-key" {
			t.Errorf("Authorization = %q", authorization)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "embedding-routed",
			"data": []map[string]any{
				{"index": 1, "embedding": []float32{3, 4}},
				{"index": 0, "embedding": []float32{1, 2}},
			},
			"usage": map[string]int{"prompt_tokens": 7, "total_tokens": 7},
		})
	}))
	defer server.Close()

	model := newCapabilityTestModel(t, server.URL+"/v1")
	response, err := model.Embed(t.Context(), gopact.EmbeddingRequest{
		Model: "embedding-test", Input: []string{"a", "b"}, Dimensions: 2,
	})
	if err != nil {
		t.Fatalf("Embed() error = %v", err)
	}
	if got.Model != "embedding-test" || len(got.Input) != 2 || got.Dimensions != 2 {
		t.Fatalf("request = %+v", got)
	}
	if response.Model != "embedding-routed" || len(response.Embeddings) != 2 {
		t.Fatalf("response = %+v", response)
	}
	if response.Embeddings[0].Index != 0 || response.Embeddings[0].Vector[1] != 2 {
		t.Fatalf("embeddings = %+v, want sorted by index", response.Embeddings)
	}
	if response.Usage != (gopact.Usage{InputTokens: 7, TotalTokens: 7}) {
		t.Fatalf("usage = %+v", response.Usage)
	}
}

func TestModelEmbedValidatesRequest(t *testing.T) {
	model := newCapabilityTestModel(t, "http://example.com/v1")
	for _, request := range []gopact.EmbeddingRequest{
		{},
		{Input: []string{""}},
		{Input: []string{"ok"}, Dimensions: -1},
	} {
		if _, err := model.Embed(t.Context(), request); err == nil {
			t.Fatalf("Embed(%+v) error = nil", request)
		}
	}
}

func TestModelListModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data": []map[string]any{{
				"id": "model-b", "object": "model", "created": 42, "owned_by": "owner-b",
			}, {
				"id": "model-a", "object": "model", "created": 41, "owned_by": "owner-a",
			}},
		})
	}))
	defer server.Close()

	client, err := New("test", server.URL+"/v1", "test-key", "", WithInsecureHTTP())
	if err != nil {
		t.Fatal(err)
	}
	models, err := client.ListModels(t.Context())
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	if len(models.Models) != 2 || models.Models[0].ID != "model-a" || models.Models[1].OwnedBy != "owner-b" {
		t.Fatalf("models = %+v", models.Models)
	}
	if models.ProviderMetadata["object"] != "list" || models.Models[0].ProviderMetadata["created"] != int64(41) {
		t.Fatalf("metadata = %+v / %+v", models.ProviderMetadata, models.Models[0].ProviderMetadata)
	}
}

func TestModelGetModel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.EscapedPath() != "/v1/models/model%2Fone" {
			t.Errorf("request = %s %s", r.Method, r.URL.EscapedPath())
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "model/one", "object": "model", "created": 42, "owned_by": "openai",
		})
	}))
	defer server.Close()

	model, err := newCapabilityTestModel(t, server.URL+"/v1").GetModel(t.Context(), "model/one")
	if err != nil {
		t.Fatalf("GetModel() error = %v", err)
	}
	if model.ID != "model/one" || model.OwnedBy != "openai" || model.ProviderMetadata["created"] != int64(42) {
		t.Fatalf("model = %+v", model)
	}
}

func newCapabilityTestModel(t *testing.T, baseURL string) *Model {
	t.Helper()
	model, err := New("test", baseURL, "test-key", "default-model", WithInsecureHTTP())
	if err != nil {
		t.Fatal(err)
	}
	return model
}
