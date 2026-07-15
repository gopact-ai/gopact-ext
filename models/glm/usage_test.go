package glm

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPlanUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/usage/quota/limit" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if authorization := r.Header.Get("Authorization"); authorization != "test-key" {
			t.Errorf("Authorization = %q, want raw token", authorization)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "code": 200, "msg": "ok",
			"data": map[string]any{
				"level": "max",
				"limits": []map[string]any{{
					"type": "TOKENS_LIMIT", "unit": 5, "number": 1,
					"usage": 100, "currentValue": 25, "remaining": 75,
					"percentage": 25, "nextResetTime": int64(1_750_000_000_000),
				}},
			},
		})
	}))
	defer server.Close()

	usage, err := newUsageTestModel(t, server.URL+"/usage").PlanUsage(t.Context())
	if err != nil {
		t.Fatalf("PlanUsage() error = %v", err)
	}
	if usage.Level != "max" || len(usage.Limits) != 1 || usage.Limits[0].Remaining != 75 {
		t.Fatalf("usage = %+v", usage)
	}
	if usage.Limits[0].NextResetTime.UnixMilli() != 1_750_000_000_000 {
		t.Fatalf("reset = %v", usage.Limits[0].NextResetTime)
	}
}

func TestDetailedUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("startTime"); got != "2026-07-01 00:00:00" {
			t.Errorf("startTime = %q", got)
		}
		if got := r.URL.Query().Get("endTime"); got != "2026-07-02 03:04:05" {
			t.Errorf("endTime = %q", got)
		}
		switch r.URL.Path {
		case "/usage/model-usage":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true, "code": 200,
				"data": map[string]any{
					"granularity": "daily", "x_time": []string{"2026-07-01"},
					"modelCallCount": []int64{2}, "tokensUsage": []int64{10},
					"totalUsage": map[string]any{"totalModelCallCount": 2, "totalTokensUsage": 10},
				},
			})
		case "/usage/tool-usage":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true, "code": 200,
				"data": map[string]any{
					"granularity": "daily", "x_time": []string{"2026-07-01"},
					"networkSearchCount": []int64{3},
					"totalUsage":         map[string]any{"totalNetworkSearchCount": 3},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	model := newUsageTestModel(t, server.URL+"/usage")
	period := UsagePeriod{
		Start: time.Date(2026, 7, 1, 0, 0, 0, 0, time.Local),
		End:   time.Date(2026, 7, 2, 3, 4, 5, 0, time.Local),
	}
	models, err := model.ModelUsage(t.Context(), period)
	if err != nil {
		t.Fatalf("ModelUsage() error = %v", err)
	}
	if models.Total.TotalTokens != 10 || len(models.Tokens) != 1 || models.Tokens[0] != 10 {
		t.Fatalf("model usage = %+v", models)
	}
	tools, err := model.ToolUsage(t.Context(), period)
	if err != nil {
		t.Fatalf("ToolUsage() error = %v", err)
	}
	if tools.Total.NetworkSearches != 3 || len(tools.NetworkSearches) != 1 {
		t.Fatalf("tool usage = %+v", tools)
	}
}

func TestDetailedUsageValidatesPeriod(t *testing.T) {
	model := newUsageTestModel(t, "http://example.com/usage")
	for _, period := range []UsagePeriod{
		{},
		{Start: time.Now(), End: time.Now().Add(-time.Hour)},
	} {
		if _, err := model.ModelUsage(t.Context(), period); err == nil {
			t.Fatalf("ModelUsage(%+v) error = nil", period)
		}
	}
}

func newUsageTestModel(t *testing.T, monitorURL string) *Model {
	t.Helper()
	model, err := New(
		"test-key",
		WithChatBaseURL("http://example.com/coding"),
		WithAPIBaseURL("http://example.com/api"),
		WithMonitorBaseURL(monitorURL),
		WithInsecureHTTP(),
	)
	if err != nil {
		t.Fatal(err)
	}
	return model
}
