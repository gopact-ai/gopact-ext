//go:build integration

package glm

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gopact-ai/gopact"
)

func TestIntegrationInvoke(t *testing.T) {
	model := newIntegrationModel(t)
	req := newIntegrationRequest(model, "Reply with exactly: pong")
	resp, err := model.Invoke(context.Background(), req)
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if text := responseText(resp); text == "" {
		t.Fatalf("response = %+v, want non-empty text", resp)
	}
}

func TestIntegrationCapabilities(t *testing.T) {
	model := newIntegrationModel(t)
	tests := []struct {
		name  string
		setup func() gopact.ModelRequest
		check func(*testing.T, gopact.ModelResponse)
	}{
		{
			name: "non-ascii",
			setup: func() gopact.ModelRequest {
				return newIntegrationRequest(model, "只回复: 你好")
			},
			check: func(t *testing.T, resp gopact.ModelResponse) {
				if !strings.Contains(responseText(resp), "你好") {
					t.Fatalf("text = %q, want Chinese marker", responseText(resp))
				}
			},
		},
		{
			name: "multi-turn",
			setup: func() gopact.ModelRequest {
				req := model.NewRequest(
					gopact.UserMessage("Remember this marker: memorytoken."),
					gopact.UserMessage("Reply with only the marker."),
				)
				applyIntegrationDefaults(&req)
				return req
			},
			check: func(t *testing.T, resp gopact.ModelResponse) {
				if !strings.Contains(responseText(resp), "memorytoken") {
					t.Fatalf("text = %q, want remembered marker", responseText(resp))
				}
			},
		},
		{
			name: "stop",
			setup: func() gopact.ModelRequest {
				req := newIntegrationRequest(model, "Reply exactly: alpha STOP beta")
				req.Stop = []string{"STOP"}
				return req
			},
			check: func(t *testing.T, resp gopact.ModelResponse) {
				if strings.Contains(responseText(resp), "STOP") {
					t.Fatalf("text = %q, want stop sequence removed", responseText(resp))
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := model.Invoke(context.Background(), tt.setup())
			if err != nil {
				t.Fatalf("Invoke() error = %v", err)
			}
			tt.check(t, resp)
		})
	}
}

func TestIntegrationStream(t *testing.T) {
	model := newIntegrationModel(t)
	var last string
	for attempt := 1; attempt <= 3; attempt++ {
		last = integrationStreamText(t, model)
		if strings.Contains(last, "streampong") {
			return
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("stream text = %q, want streampong", last)
}

func integrationStreamText(t *testing.T, model *Model) string {
	t.Helper()
	var text strings.Builder
	for chunk, err := range model.InvokeStream(context.Background(), newIntegrationRequest(model, "Reply with exactly: streampong")) {
		if err != nil {
			t.Fatalf("InvokeStream() error = %v", err)
		}
		text.WriteString(chunk.Text)
	}
	return text.String()
}

func newIntegrationModel(t *testing.T) *Model {
	t.Helper()
	key := os.Getenv("GLM_API_KEY")
	if key == "" {
		t.Skip("GLM_API_KEY is not set")
	}
	model, err := New(key)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return model
}

func newIntegrationRequest(model *Model, prompt string) gopact.ModelRequest {
	req := model.NewRequest(gopact.UserMessage(prompt))
	applyIntegrationDefaults(&req)
	return req
}

func applyIntegrationDefaults(req *gopact.ModelRequest) {
	if modelName := os.Getenv("GLM_MODEL"); modelName != "" {
		req.Model = modelName
	}
	temperature := 0.0
	req.Temperature = &temperature
	req.MaxOutputTokens = 1024
}

func responseText(resp gopact.ModelResponse) string {
	if len(resp.Message.Parts) == 0 {
		return ""
	}
	return resp.Message.Parts[0].Text
}
