package openai

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

func TestResponsesRuntime(t *testing.T) {
	var calls []string
	routes := map[string]http.HandlerFunc{
		"POST /v1/responses": func(w http.ResponseWriter, r *http.Request) {
			var request ResponseRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode response request: %v", err)
			}
			if request.Model != "default-model" || request.Input != "hello" {
				t.Errorf("response request = %+v", request)
			}
			_, _ = io.WriteString(w, `{"id":"resp_1","object":"response","status":"completed","model":"default-model","output":[{"type":"message"}],"usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5}}`)
		},
		"GET /v1/responses/resp_1": func(w http.ResponseWriter, r *http.Request) {
			if got := r.URL.Query()["include[]"]; !slices.Equal(got, []string{"reasoning.encrypted_content"}) {
				t.Errorf("include = %v", got)
			}
			_, _ = io.WriteString(w, `{"id":"resp_1","status":"completed"}`)
		},
		"POST /v1/responses/resp_1/cancel": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"id":"resp_1","status":"cancelled"}`)
		},
		"GET /v1/responses/resp_1/input_items": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("limit") != "10" || r.URL.Query().Get("order") != "asc" {
				t.Errorf("input item query = %q", r.URL.RawQuery)
			}
			_, _ = io.WriteString(w, `{"object":"list","data":[{"type":"message"}],"first_id":"item_1","last_id":"item_1","has_more":false}`)
		},
		"POST /v1/responses/input_tokens": func(w http.ResponseWriter, r *http.Request) {
			var request ResponseInputTokenRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode input-token request: %v", err)
			}
			if request.Personality != "friendly" {
				t.Errorf("personality = %q", request.Personality)
			}
			_, _ = io.WriteString(w, `{"object":"response.input_tokens","input_tokens":17}`)
		},
		"POST /v1/responses/compact": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"id":"cmp_1","object":"response.compaction","output":[{"type":"compaction"}],"usage":{"input_tokens":10,"output_tokens":1,"total_tokens":11}}`)
		},
		"DELETE /v1/responses/resp_1": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.RequestURI())
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q", got)
		}
		dispatchRuntimeTestRoute(t, routes, w, r)
	}))
	defer server.Close()

	model := newCapabilityTestModel(t, server.URL+"/v1")
	created, err := model.CreateResponse(t.Context(), ResponseRequest{Input: "hello"})
	if err != nil {
		t.Fatalf("CreateResponse() error = %v", err)
	}
	if created.ID != "resp_1" || created.Usage.TotalTokens != 5 || len(created.Output) != 1 {
		t.Fatalf("response = %+v", created)
	}
	got, err := model.GetResponse(t.Context(), "resp_1", ResponseQuery{Include: []string{"reasoning.encrypted_content"}})
	if err != nil || got.ID != "resp_1" {
		t.Fatalf("GetResponse() = %+v, %v", got, err)
	}
	cancelled, err := model.CancelResponse(t.Context(), "resp_1")
	if err != nil || cancelled.Status != "cancelled" {
		t.Fatalf("CancelResponse() = %+v, %v", cancelled, err)
	}
	items, err := model.ResponseInputItems(t.Context(), "resp_1", ResponseInputItemQuery{Limit: 10, Order: "asc"})
	if err != nil || len(items.Data) != 1 || items.FirstID != "item_1" {
		t.Fatalf("ResponseInputItems() = %+v, %v", items, err)
	}
	count, err := model.CountResponseInputTokens(t.Context(), ResponseInputTokenRequest{
		Input: "hello", Personality: "friendly",
	})
	if err != nil || count.InputTokens != 17 {
		t.Fatalf("CountResponseInputTokens() = %+v, %v", count, err)
	}
	compacted, err := model.CompactResponse(t.Context(), ResponseCompactRequest{Input: "hello"})
	if err != nil || compacted.ID != "cmp_1" || compacted.Usage.TotalTokens != 11 {
		t.Fatalf("CompactResponse() = %+v, %v", compacted, err)
	}
	if err := model.DeleteResponse(t.Context(), "resp_1"); err != nil {
		t.Fatalf("DeleteResponse() error = %v", err)
	}
	if len(calls) != 7 {
		t.Fatalf("calls = %v", calls)
	}
}

func TestStreamResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if request["stream"] != true {
			t.Errorf("stream = %#v", request["stream"])
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	var events []ResponseEvent
	for event, err := range newCapabilityTestModel(t, server.URL).StreamResponse(t.Context(), ResponseRequest{Input: "hello"}) {
		if err != nil {
			t.Fatalf("StreamResponse() error = %v", err)
		}
		events = append(events, event)
	}
	if len(events) != 1 || events[0].Type != "response.output_text.delta" {
		t.Fatalf("events = %+v", events)
	}
}

func TestStreamStoredResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/responses/resp_1" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.URL.Query().Get("starting_after") != "7" {
			t.Errorf("query = %q", r.URL.RawQuery)
		}
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if request["stream"] != true {
			t.Errorf("stream = %#v", request["stream"])
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: response.completed\ndata: {\"type\":\"response.completed\"}\n\n")
	}))
	defer server.Close()

	var events []ResponseEvent
	stream := newCapabilityTestModel(t, server.URL).StreamStoredResponse(
		t.Context(),
		"resp_1",
		ResponseQuery{StartingAfter: 7},
	)
	for event, err := range stream {
		if err != nil {
			t.Fatalf("StreamStoredResponse() error = %v", err)
		}
		events = append(events, event)
	}
	if len(events) != 1 || events[0].Type != "response.completed" {
		t.Fatalf("events = %+v", events)
	}
}

func TestModerate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/moderations" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"id":"modr_1","model":"omni-moderation-latest","results":[{"flagged":true,"categories":{"violence":true},"category_scores":{"violence":0.99},"category_applied_input_types":{"violence":["text"]}}]}`)
	}))
	defer server.Close()

	response, err := newCapabilityTestModel(t, server.URL).Moderate(t.Context(), ModerationRequest{Input: "bad"})
	if err != nil {
		t.Fatalf("Moderate() error = %v", err)
	}
	if response.ID != "modr_1" || !response.Results[0].Flagged || !response.Results[0].Categories["violence"] {
		t.Fatalf("response = %+v", response)
	}
}

func TestResponsesValidateIdentifiers(t *testing.T) {
	model := newCapabilityTestModel(t, "http://example.com")
	if _, err := model.GetResponse(t.Context(), "", ResponseQuery{}); err == nil {
		t.Fatal("GetResponse(empty) error = nil")
	}
	if _, err := model.Moderate(t.Context(), ModerationRequest{}); err == nil {
		t.Fatal("Moderate(empty) error = nil")
	}
}
