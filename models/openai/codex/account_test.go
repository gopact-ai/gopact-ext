package codex

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gopact-ai/gopact-ext/models/openai/codexauth"
)

func TestModelDiscoveryAndSubscriptionUsage(t *testing.T) {
	routes := map[string]http.HandlerFunc{
		"GET /backend-api/codex/models": func(w http.ResponseWriter, r *http.Request) {
			if version := r.URL.Query().Get("client_version"); version != "1.2.3" {
				t.Errorf("client_version = %q", version)
			}
			w.Header().Set("ETag", `"catalog-1"`)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"models": []map[string]any{{
					"slug": "gpt-codex", "display_name": "GPT Codex", "description": "Coding model",
					"default_reasoning_level":    "medium",
					"supported_reasoning_levels": []map[string]string{{"effort": "low", "description": "Fast"}},
					"visibility":                 "list", "supported_in_api": true, "priority": 1,
					"input_modalities":             []string{"text", "image"},
					"supports_parallel_tool_calls": true, "supports_search_tool": true,
					"context_window": 272000, "future_capability": "preserved",
				}},
			})
		},
		"GET /backend-api/wham/usage": func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"plan_type": "plus",
				"rate_limit": map[string]any{
					"allowed": true, "limit_reached": false,
					"primary_window": map[string]any{
						"used_percent": 25, "limit_window_seconds": 18000,
						"reset_after_seconds": 900, "reset_at": 1750000000,
					},
				},
				"credits": map[string]any{"has_credits": true, "unlimited": false, "balance": "12.5"},
				"additional_rate_limits": []map[string]any{{
					"limit_name": "review", "metered_feature": "code_review",
					"rate_limit": map[string]any{"allowed": true, "limit_reached": false},
				}},
			})
		},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if authorization := r.Header.Get("Authorization"); authorization != "Bearer access" {
			t.Errorf("Authorization = %q", authorization)
		}
		if accountID := r.Header.Get("ChatGPT-Account-ID"); accountID != "account" {
			t.Errorf("ChatGPT-Account-ID = %q", accountID)
		}
		dispatchAccountTestRoute(t, routes, w, r)
	}))
	defer server.Close()

	model, err := New(
		"",
		StaticTokenSource(codexauth.Tokens{AccessToken: "access", AccountID: "account"}),
		WithBaseURL(server.URL+"/backend-api/codex"),
		WithClientVersion("1.2.3"),
		WithInsecureHTTP(),
	)
	if err != nil {
		t.Fatal(err)
	}
	models, err := model.ListModels(t.Context())
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	if len(models.Models) != 1 || models.Models[0].ID != "gpt-codex" || models.Models[0].DisplayName != "GPT Codex" {
		t.Fatalf("models = %+v", models.Models)
	}
	if len(models.Models[0].InputModalities) != 2 || models.ProviderMetadata["etag"] != `"catalog-1"` {
		t.Fatalf("model metadata = %+v / %+v", models.Models[0], models.ProviderMetadata)
	}
	if models.Models[0].ProviderMetadata["future_capability"] != "preserved" {
		t.Fatalf("provider metadata = %+v", models.Models[0].ProviderMetadata)
	}

	usage, err := model.SubscriptionUsage(t.Context())
	if err != nil {
		t.Fatalf("SubscriptionUsage() error = %v", err)
	}
	if usage.Plan != "plus" || usage.RateLimit == nil || usage.RateLimit.Primary == nil || usage.RateLimit.Primary.UsedPercent != 25 {
		t.Fatalf("usage = %+v", usage)
	}
	if usage.Credits == nil || usage.Credits.Balance != "12.5" || len(usage.AdditionalRateLimits) != 1 {
		t.Fatalf("usage credits/limits = %+v", usage)
	}
}

func TestSubscriptionUsageRefreshesAfterUnauthorized(t *testing.T) {
	source := &rotatingTokenSource{tokens: codexauth.Tokens{AccessToken: "stale"}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer stale" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"expired"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"plan_type":"pro"}`))
	}))
	defer server.Close()

	model := newTestModel(t, server.URL+"/backend-api/codex", source)
	usage, err := model.SubscriptionUsage(t.Context())
	if err != nil {
		t.Fatalf("SubscriptionUsage() error = %v", err)
	}
	if usage.Plan != "pro" || source.refreshes.Load() != 1 {
		t.Fatalf("usage/refreshes = %+v/%d", usage, source.refreshes.Load())
	}
}

func TestModelDiscoveryUsesAuditedClientVersionByDefault(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if version := r.URL.Query().Get("client_version"); version != defaultClientVersion {
			t.Errorf("client_version = %q", version)
		}
		_, _ = w.Write([]byte(`{"models":[]}`))
	}))
	defer server.Close()

	model := newTestModel(
		t,
		server.URL+"/backend-api/codex",
		StaticTokenSource(codexauth.Tokens{AccessToken: "access"}),
	)
	if _, err := model.ListModels(t.Context()); err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
}
