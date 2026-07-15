package glm

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSearchTool(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tools" || r.Method != http.MethodPost {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if request["model"] != DefaultToolSearchModel || request["recent_days"] != float64(7) {
			t.Errorf("request = %#v", request)
		}
		if stream, _ := request["stream"].(bool); stream {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"id\":\"search-1\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"type\":\"search_result\",\"search_result\":{\"title\":\"Result\",\"link\":\"https://example.com\"}}]}}]}\n\n")
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
			return
		}
		_, _ = io.WriteString(w, `{"id":"search-1","choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","tool_calls":[{"type":"search_result","search_result":{"title":"Result","link":"https://example.com"}}]}}]}`)
	}))
	defer server.Close()

	model := newExtendedRuntimeModel(t, server)
	request := ToolSearchRequest{
		Messages:   []map[string]string{{"role": "user", "content": "latest Go release"}},
		RecentDays: 7,
	}
	response, err := model.SearchTool(t.Context(), request)
	if err != nil || len(response.Choices) != 1 ||
		response.Choices[0].Message.ToolCalls[0].SearchResult.Title != "Result" {
		t.Fatalf("SearchTool() = %+v, %v", response, err)
	}
	var chunks []ToolSearchChunk
	for chunk, err := range model.StreamSearchTool(t.Context(), request) {
		if err != nil {
			t.Fatalf("StreamSearchTool() error = %v", err)
		}
		chunks = append(chunks, chunk)
	}
	if len(chunks) != 1 || chunks[0].Choices[0].Delta.ToolCalls[0].SearchResult.Link != "https://example.com" {
		t.Fatalf("StreamSearchTool() = %+v", chunks)
	}
}

func TestSearchToolValidatesRequest(t *testing.T) {
	model, err := New("key")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := model.SearchTool(t.Context(), ToolSearchRequest{}); err == nil {
		t.Fatal("SearchTool() error = nil")
	}
	if _, err := model.SearchTool(t.Context(), ToolSearchRequest{Messages: "query", RecentDays: 31}); err == nil {
		t.Fatal("SearchTool(recent days) error = nil")
	}
}
