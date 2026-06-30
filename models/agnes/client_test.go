package agnes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gopact-ai/gopact"
)

func TestNewClientUsesAgnesDefaults(t *testing.T) {
	var got struct {
		Model        string         `json:"model"`
		ChatTemplate map[string]any `json:"chat_template_kwargs"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("path = %q, want /chat/completions", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		_, _ = w.Write([]byte(`{"choices": [{"message": {"role": "assistant", "content": "hello"}}]}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "token", EnableThinking())
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	response, err := client.Generate(context.Background(), gopact.NewModelRequest(
		gopact.WithMessages(gopact.Message{Role: gopact.RoleUser, Content: "hi"}),
	))
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if response.Message.Text() != "hello" {
		t.Fatalf("Message.Text() = %q, want hello", response.Message.Text())
	}
	if got.Model != DefaultModel {
		t.Fatalf("model = %q, want %q", got.Model, DefaultModel)
	}
	if got.ChatTemplate["enable_thinking"] != true {
		t.Fatalf("chat_template_kwargs = %#v, want enable_thinking true", got.ChatTemplate)
	}

	models, err := client.Models(context.Background())
	if err != nil {
		t.Fatalf("Models() error = %v", err)
	}
	if len(models) != 1 || models[0].Provider != DefaultProvider || models[0].Name != DefaultModel {
		t.Fatalf("models = %#v, want Agnes default model", models)
	}
}

func TestNewClientAllowsModelOverride(t *testing.T) {
	var got struct {
		Model string `json:"model"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		_, _ = w.Write([]byte(`{"choices": [{"message": {"role": "assistant", "content": "ok"}}]}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "token", gopact.WithModel("agnes-custom"))
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if _, err := client.Generate(context.Background(), gopact.NewModelRequest(
		gopact.WithMessages(gopact.Message{Role: gopact.RoleUser, Content: "hi"}),
	)); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if got.Model != "agnes-custom" {
		t.Fatalf("model = %q, want override", got.Model)
	}
}
