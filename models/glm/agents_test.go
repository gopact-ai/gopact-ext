package glm

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStreamAgent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/agents" {
			t.Errorf("path = %q", r.URL.Path)
		}
		var request AgentRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if !request.Stream || request.UserID != "user-1" || request.SensitiveWordCheck == nil {
			t.Errorf("request = %+v", request)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"id\":\"agent-1\",\"agent_id\":\"general_translation\"}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
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
	var events []AgentEvent
	for event, err := range model.StreamAgent(t.Context(), AgentRequest{
		AgentID: "general_translation", UserID: "user-1",
		Messages:           []AgentMessage{{Role: "user", Content: []AgentContent{{Type: "text", Text: "hello"}}}},
		SensitiveWordCheck: &SensitiveWordCheck{Type: "ALL", Status: "DISABLE"},
	}) {
		if err != nil {
			t.Fatalf("StreamAgent() error = %v", err)
		}
		events = append(events, event)
	}
	if len(events) != 1 || events[0].Response.ID != "agent-1" {
		t.Fatalf("events = %+v", events)
	}
}
