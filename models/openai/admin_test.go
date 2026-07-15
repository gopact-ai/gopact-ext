package openai

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAdminClientUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/organization/usage/embeddings" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if authorization := r.Header.Get("Authorization"); authorization != "Bearer admin-key" {
			t.Errorf("Authorization = %q", authorization)
		}
		query := r.URL.Query()
		if query.Get("start_time") != "1751328000" || query.Get("end_time") != "1751414400" {
			t.Errorf("time query = %v", query)
		}
		if query.Get("bucket_width") != "1d" || query.Get("limit") != "7" || query.Get("page") != "next" {
			t.Errorf("paging query = %v", query)
		}
		if got := query["models[]"]; len(got) != 1 || got[0] != "embedding-3" {
			t.Errorf("models = %v", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "page", "has_more": true, "next_page": "next-2",
			"data": []map[string]any{{
				"object": "bucket", "start_time": 1751328000, "end_time": 1751414400,
				"results": []map[string]any{{
					"object":       "organization.usage.embeddings.result",
					"input_tokens": 123, "num_model_requests": 4,
					"model": "embedding-3", "project_id": "project-1",
				}},
			}},
		})
	}))
	defer server.Close()

	client, err := NewAdminClient(
		"admin-key", WithAdminBaseURL(server.URL+"/v1"), WithAdminInsecureHTTP(),
	)
	if err != nil {
		t.Fatal(err)
	}
	page, err := client.Usage(t.Context(), OrganizationUsageEmbeddings, OrganizationUsageQuery{
		StartTime:   time.Unix(1751328000, 0),
		EndTime:     time.Unix(1751414400, 0),
		BucketWidth: "1d",
		Models:      []string{"embedding-3"},
		Limit:       7,
		Page:        "next",
	})
	if err != nil {
		t.Fatalf("Usage() error = %v", err)
	}
	if !page.HasMore || page.NextPage != "next-2" || len(page.Buckets) != 1 {
		t.Fatalf("page = %+v", page)
	}
	result := page.Buckets[0].Results[0]
	if result.InputTokens != 123 || result.ModelRequests != 4 || result.Model != "embedding-3" {
		t.Fatalf("result = %+v", result)
	}
}

func TestAdminClientCosts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/organization/costs" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if got := r.URL.Query()["api_key_ids[]"]; len(got) != 1 || got[0] != "key-1" {
			t.Errorf("api_key_ids = %v", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "page", "has_more": false,
			"data": []map[string]any{{
				"start_time": 1, "end_time": 2,
				"results": []map[string]any{{
					"object":    "organization.costs.result",
					"amount":    map[string]any{"value": 1.25, "currency": "usd"},
					"line_item": "Responses API", "project_id": "project-1",
					"api_key_id": "key-1", "quantity": 2,
				}},
			}},
		})
	}))
	defer server.Close()

	client, err := NewAdminClient("admin-key", WithAdminBaseURL(server.URL+"/v1"), WithAdminInsecureHTTP())
	if err != nil {
		t.Fatal(err)
	}
	page, err := client.Costs(t.Context(), OrganizationCostsQuery{
		StartTime: time.Unix(1, 0), APIKeyIDs: []string{"key-1"},
	})
	if err != nil {
		t.Fatalf("Costs() error = %v", err)
	}
	if len(page.Buckets) != 1 || page.Buckets[0].Results[0].Amount.Value != 1.25 ||
		page.Buckets[0].Results[0].APIKeyID != "key-1" || page.Buckets[0].Results[0].Quantity != 2 {
		t.Fatalf("costs = %+v", page)
	}
}

func TestAdminClientValidatesUsageQuery(t *testing.T) {
	client, err := NewAdminClient("admin-key")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Usage(t.Context(), "unknown", OrganizationUsageQuery{StartTime: time.Now()}); err == nil {
		t.Fatal("Usage(unknown) error = nil")
	}
	if _, err := client.Usage(t.Context(), OrganizationUsageCompletions, OrganizationUsageQuery{}); err == nil {
		t.Fatal("Usage(without start) error = nil")
	}
}
