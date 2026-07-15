package fake

import (
	"context"
	"testing"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact/gopacttest"
)

func TestModelConformance(t *testing.T) {
	gopacttest.RequireModelConformance(t, New())
}

func TestEmbeddingAndDiscovery(t *testing.T) {
	model := New(WithDefaultRequest(gopact.ModelRequest{Model: "fake-test"}))
	result, err := model.Embed(t.Context(), gopact.EmbeddingRequest{Input: []string{"go"}, Dimensions: 2})
	if err != nil {
		t.Fatalf("Embed() error = %v", err)
	}
	if result.Model != "fake-test" || len(result.Embeddings) != 1 || len(result.Embeddings[0].Vector) != 2 {
		t.Fatalf("embedding = %+v", result)
	}
	models, err := model.ListModels(t.Context())
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	if len(models.Models) != 1 || models.Models[0].ID != "fake-test" {
		t.Fatalf("models = %+v", models.Models)
	}
}

func TestInvokeEmitsModelEvent(t *testing.T) {
	var got gopact.ModelEvent
	_, err := New(WithResponse("hello")).Invoke(
		context.Background(),
		gopact.ModelRequest{Messages: []gopact.Message{gopact.UserMessage("task")}},
		gopact.WithModelEventHandler(func(_ context.Context, event gopact.ModelEvent) error {
			got = event
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if got.Type != gopact.ModelEventMessageDelta || got.Summary != "hello" {
		t.Fatalf("event = %+v, want fake message delta", got)
	}
}

func TestInvokeRejectsUnknownCallExtension(t *testing.T) {
	_, err := New().Invoke(
		context.Background(),
		gopact.ModelRequest{Messages: []gopact.Message{gopact.UserMessage("task")}},
		testModelCallOptionFunc(func(cfg *gopact.ModelCallConfig) {
			cfg.Extensions = map[string]any{"other.provider": true}
		}),
	)
	if err == nil {
		t.Fatal("Invoke() error = nil, want unknown call extension")
	}
}

func TestInvokeRejectsUnknownRequestExtension(t *testing.T) {
	_, err := New().Invoke(
		context.Background(),
		gopact.ModelRequest{
			Messages:   []gopact.Message{gopact.UserMessage("task")},
			Extensions: map[string]any{"other.provider": true},
		},
	)
	if err == nil {
		t.Fatal("Invoke() error = nil, want unknown request extension")
	}
}

type testModelCallOptionFunc func(*gopact.ModelCallConfig)

func (f testModelCallOptionFunc) ApplyModelCallOption(cfg *gopact.ModelCallConfig) {
	f(cfg)
}
