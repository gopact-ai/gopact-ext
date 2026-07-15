package openai

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLegacyCompletionsRuntime(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if request["model"] != "default-model" || request["prompt"] != "Once" {
			t.Errorf("request = %#v", request)
		}
		if request["stream"] == true {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"id\":\"cmpl_1\",\"choices\":[{\"text\":\" upon\",\"index\":0}]}\n\n")
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
			return
		}
		_, _ = io.WriteString(w, `{"id":"cmpl_1","object":"text_completion","model":"default-model","choices":[{"text":" upon a time","index":0,"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":3,"total_tokens":4}}`)
	}))
	defer server.Close()

	model := newCapabilityTestModel(t, server.URL)
	completion, err := model.Complete(t.Context(), CompletionRequest{Prompt: "Once"})
	if err != nil || completion.Choices[0].Text != " upon a time" || completion.Usage.TotalTokens != 4 {
		t.Fatalf("Complete() = %+v, %v", completion, err)
	}
	var events []CompletionEvent
	for event, err := range model.StreamCompletion(t.Context(), CompletionRequest{Prompt: "Once"}) {
		if err != nil {
			t.Fatalf("StreamCompletion() error = %v", err)
		}
		events = append(events, event)
	}
	if len(events) != 1 || events[0].Completion.Choices[0].Text != " upon" {
		t.Fatalf("events = %+v", events)
	}
}

func TestLegacyCompletionValidatesRequest(t *testing.T) {
	model := newCapabilityTestModel(t, "http://example.com")
	if _, err := model.Complete(t.Context(), CompletionRequest{}); err == nil {
		t.Fatal("Complete(empty) error = nil")
	}
}
