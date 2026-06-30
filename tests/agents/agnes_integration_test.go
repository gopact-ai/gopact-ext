//go:build integration

package agents

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact-ext/agents/planexec"
	"github.com/gopact-ai/gopact-ext/agents/react"
	"github.com/gopact-ai/gopact-ext/models/agnes"
	"github.com/gopact-ai/gopact/gopacttest"
)

func TestAgnesIntegrationReActTemplate(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	client := newAgnesIntegrationModel(t)
	agent, err := react.New(gopact.AdaptStreamingModel(client), nil, react.WithMaxIterations(2))
	if err != nil {
		t.Fatalf("react.New() error = %v", err)
	}

	events, err := gopacttest.CollectEvents(agent.Run(ctx, react.State{Messages: []gopact.Message{
		gopact.SystemMessage("You are concise. Reply with one short sentence."),
		gopact.UserMessage("Confirm that the ReAct template works with Agnes."),
	}}))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !hasAgnesIntegrationEvent(events, gopact.EventModelMessage) {
		t.Fatalf("events = %v, want model message event", gopacttest.EventTypes(events))
	}
	if !hasAgnesIntegrationEvent(events, gopact.EventRunCompleted) {
		t.Fatalf("events = %v, want run completed", gopacttest.EventTypes(events))
	}
}

func TestAgnesIntegrationPlanExecuteTemplate(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	model := newAgnesIntegrationModel(t)
	agent, err := planexec.New(
		planexec.PlannerFunc(func(ctx context.Context, request planexec.PlanRequest) ([]planexec.Step, error) {
			text, err := agnesIntegrationText(ctx, model, "Return exactly one short execution step for this task: "+request.Task)
			if err != nil {
				return nil, err
			}
			instruction := firstAgnesIntegrationLine(text)
			if instruction == "" {
				return nil, errors.New("agnes returned empty plan")
			}
			return []planexec.Step{{ID: "step-1", Instruction: instruction}}, nil
		}),
		planexec.ExecutorFunc(func(ctx context.Context, step planexec.Step) (planexec.StepResult, error) {
			text, err := agnesIntegrationText(ctx, model, "Complete this step in one short sentence: "+step.Instruction)
			if err != nil {
				return planexec.StepResult{}, err
			}
			return planexec.StepResult{StepID: step.ID, Output: firstAgnesIntegrationLine(text)}, nil
		}),
	)
	if err != nil {
		t.Fatalf("planexec.New() error = %v", err)
	}

	events, err := gopacttest.CollectEvents(agent.Run(ctx, planexec.State{Task: "validate the plan-execute template with Agnes"}))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	gopacttest.RequireEventTypes(t, events,
		gopact.EventRunStarted,
		gopact.EventNodeStarted,
		gopact.EventNodeCompleted,
		gopact.EventNodeStarted,
		gopact.EventNodeCompleted,
		gopact.EventNodeStarted,
		gopact.EventNodeCompleted,
		gopact.EventRunCompleted,
	)
	output, ok := events[6].StepSnapshot.Output.(planexec.State)
	if !ok {
		t.Fatalf("summary output type = %T, want planexec.State", events[6].StepSnapshot.Output)
	}
	if len(output.Results) != 1 || strings.TrimSpace(output.Results[0].Output) == "" {
		t.Fatalf("results = %+v, want one non-empty Agnes-backed result", output.Results)
	}
}

func newAgnesIntegrationModel(t *testing.T) gopact.StreamingResponseModel {
	t.Helper()
	loadAgnesIntegrationDotEnv(t)
	apiKey := firstAgnesIntegrationEnv("GOPACT_AGNES_API_KEY", "GOPACT_AGNES_SK")
	if apiKey == "" {
		t.Skip("set GOPACT_AGNES_API_KEY")
	}
	client, err := agnes.NewClient(
		envOrAgnesIntegrationDefault("GOPACT_AGNES_BASEURL", agnes.DefaultBaseURL),
		apiKey,
		gopact.WithModel(envOrAgnesIntegrationDefault("GOPACT_AGNES_MODEL", agnes.DefaultModel)),
		gopact.WithMaxOutputTokens(512),
		gopact.WithTemperature(0.2),
		gopact.EnableStreaming(),
		agnes.DisableThinking(),
	)
	if err != nil {
		t.Fatalf("agnes.NewClient() error = %v", err)
	}
	return client
}

func agnesIntegrationText(ctx context.Context, model gopact.ResponseModel, prompt string) (string, error) {
	response, err := model.Generate(ctx, gopact.NewModelRequest(
		gopact.WithMessages(
			gopact.SystemMessage("You are concise. Return plain text only."),
			gopact.UserMessage(prompt),
		),
		gopact.WithMaxOutputTokens(512),
		gopact.WithTemperature(0.2),
		agnes.DisableThinking(),
	))
	if err != nil {
		return "", err
	}
	text := strings.TrimSpace(response.Message.Text())
	if text == "" {
		return "", errors.New("agnes returned empty text")
	}
	return text, nil
}

func firstAgnesIntegrationLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "-*0123456789.) \t"))
		if line != "" {
			return line
		}
	}
	return ""
}

func hasAgnesIntegrationEvent(events []gopact.Event, eventType gopact.EventType) bool {
	for _, event := range events {
		if event.Type == eventType {
			return true
		}
	}
	return false
}

func loadAgnesIntegrationDotEnv(t *testing.T) {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		data, err := os.ReadFile(filepath.Join(dir, ".env"))
		if err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				setAgnesIntegrationEnvLine(line)
			}
			return
		}
		if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return
		}
		dir = parent
	}
}

func setAgnesIntegrationEnvLine(line string) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return
	}
	key, value, ok := strings.Cut(line, "=")
	if !ok {
		return
	}
	key = strings.TrimSpace(strings.TrimPrefix(key, "export "))
	if key == "" || os.Getenv(key) != "" {
		return
	}
	_ = os.Setenv(key, strings.Trim(strings.TrimSpace(value), `"'`))
}

func firstAgnesIntegrationEnv(keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}

func envOrAgnesIntegrationDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
