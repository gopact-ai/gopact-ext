//go:build integration

package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact-ext/agents/planexec"
	"github.com/gopact-ai/gopact-ext/agents/react"
	"github.com/gopact-ai/gopact-ext/models/agnes"
	"github.com/gopact-ai/gopact/checkpoint"
	"github.com/gopact-ai/gopact/gopacttest"
	"github.com/gopact-ai/gopact/memory"
)

func TestAgnesIntegrationReActTemplate(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	client := newAgnesIntegrationModel(t)
	agent, err := react.NewModelAgent(client, react.WithMaxIterations(2))
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

func TestAgnesIntegrationReActTemplateCapabilities(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	agnesModel := newAgnesIntegrationModel(t)
	checkpoints := checkpoint.NewMemory[react.State]()
	memories := memory.New()
	if _, err := memories.Put(ctx, memory.Memory{
		Scope:   memory.Scope{UserID: "user-1"},
		Type:    memory.TypeProfile,
		Content: "prefers concise status updates",
	}); err != nil {
		t.Fatalf("Put(memory) error = %v", err)
	}

	toolInvoked := 0
	uppercase := gopact.ToolFunc{
		SpecValue: gopact.ObjectToolSpec("uppercase", "Uppercase text.", gopact.RequiredStringField("text", "Text to uppercase.")),
		InvokeFunc: func(_ context.Context, args json.RawMessage) (gopact.ToolResult, error) {
			toolInvoked++
			var input struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(args, &input); err != nil {
				return gopact.ToolResult{}, err
			}
			return gopact.ToolResult{Content: strings.ToUpper(input.Text)}, nil
		},
	}

	initialAgent, err := react.New(scriptedToolCallModel{}, nil, react.WithTools(ctx, uppercase), react.WithCheckpointStore(checkpoints))
	if err != nil {
		t.Fatalf("react.New(initial) error = %v", err)
	}
	for event, err := range initialAgent.Run(ctx, react.State{Messages: []gopact.Message{
		gopact.UserMessage("Use the uppercase tool on gopact, then answer briefly."),
	}}, gopact.WithRuntimeIDs(gopact.RuntimeIDs{RunID: "run-initial", ThreadID: "thread-1", UserID: "user-1"})) {
		if err != nil {
			t.Fatalf("initial Run() error = %v", err)
		}
		if event.Type == gopact.EventNodeStarted && event.Node == "call_tool" {
			break
		}
	}
	if toolInvoked != 0 {
		t.Fatalf("tool invoked before resume = %d, want 0", toolInvoked)
	}

	finalModel := &agnesFinalModel{provider: agnesModel}
	extractCalls := 0
	verified := false
	resumeAgent, err := react.New(
		finalModel,
		nil,
		react.WithTools(ctx, uppercase),
		react.WithCheckpointStore(checkpoints),
		react.WithMemory(
			memories,
			react.WithMemoryQuery(func(_ context.Context, _ react.State, ids gopact.RuntimeIDs) (memory.Query, bool, error) {
				return memory.Query{Scope: memory.Scope{UserID: ids.UserID}, Text: "concise", Limit: 2}, true, nil
			}),
			react.WithMemoryExtractor(func(_ context.Context, state react.State, _ gopact.RuntimeIDs) ([]memory.Memory, error) {
				if len(state.Messages) == 0 || strings.TrimSpace(state.Messages[len(state.Messages)-1].Text()) == "" {
					return nil, nil
				}
				extractCalls++
				return []memory.Memory{{Type: memory.TypeSemantic, Content: "agnes finalized react tool run"}}, nil
			}),
		),
		react.WithVerifier(func(_ context.Context, export gopact.RunExport, recorder *gopact.VerificationRecorder) error {
			verified = true
			if export.IDs.RunID != "run-resume" {
				return fmt.Errorf("verification export run id = %q, want run-resume", export.IDs.RunID)
			}
			return recorder.Record(gopact.VerificationCheck{
				ID:     "agnes-react-capabilities",
				Status: gopact.VerificationStatusPassed,
				Evidence: []gopact.VerificationEvidence{{
					Type:    "integration",
					Ref:     "agnes-react",
					Summary: "Agnes completed resumed ReAct run with tool, memory, checkpoint, and verifier.",
				}},
			})
		}),
	)
	if err != nil {
		t.Fatalf("react.New(resume) error = %v", err)
	}

	events, err := gopacttest.CollectEvents(resumeAgent.Run(ctx, react.State{}, gopact.WithRuntimeIDs(gopact.RuntimeIDs{
		RunID:    "run-resume",
		ThreadID: "thread-1",
		UserID:   "user-1",
	})))
	if err != nil {
		t.Fatalf("resume Run() error = %v", err)
	}
	gopacttest.RequireEventTypes(t, events,
		gopact.EventRunStarted,
		gopact.EventCheckpointLoaded,
		gopact.EventNodeResumed,
		gopact.EventToolCall,
		gopact.EventToolResult,
		gopact.EventNodeCompleted,
		gopact.EventNodeStarted,
		gopact.EventMemorySearched,
		gopact.EventModelMessage,
		gopact.EventMemoryPut,
		gopact.EventNodeCompleted,
		gopact.EventNodeStarted,
		gopact.EventNodeCompleted,
		gopact.EventRunCompleted,
	)
	if toolInvoked != 1 {
		t.Fatalf("tool invocations = %d, want 1", toolInvoked)
	}
	if finalModel.calls != 1 {
		t.Fatalf("Agnes final model calls = %d, want 1", finalModel.calls)
	}
	if extractCalls != 1 {
		t.Fatalf("memory extract calls = %d, want 1", extractCalls)
	}
	if !verified {
		t.Fatal("verifier was not called")
	}
	stored, err := memories.Search(ctx, memory.Query{Scope: memory.Scope{UserID: "user-1"}, Text: "finalized"})
	if err != nil {
		t.Fatalf("Search(memory) error = %v", err)
	}
	if len(stored.Memories) != 1 {
		t.Fatalf("stored memories = %+v, want one extracted memory", stored.Memories)
	}
	report, ok := events[12].StepSnapshot.Output.(gopact.VerificationReport)
	if !ok || report.Status != gopact.VerificationStatusPassed || report.PassedCount != 1 {
		t.Fatalf("verification output = %#v, want one passed check", events[12].StepSnapshot.Output)
	}
}

func TestAgnesIntegrationPlanExecuteTemplate(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	model := newAgnesIntegrationModel(t)
	agent, err := planexec.NewModelAgent(
		model,
		gopact.WithMaxOutputTokens(512),
		gopact.WithTemperature(0.2),
		agnes.DisableThinking(),
	)
	if err != nil {
		t.Fatalf("planexec.New() error = %v", err)
	}

	events, err := gopacttest.CollectEvents(agent.Run(ctx, "Create exactly two short steps to validate the plan-execute template with Agnes."))
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
	if len(output.Steps) < 2 || len(output.Results) != len(output.Steps) {
		t.Fatalf("steps/results = %+v/%+v, want multiple Agnes-backed steps and matching results", output.Steps, output.Results)
	}
	for _, result := range output.Results {
		if strings.TrimSpace(result.Output) == "" {
			t.Fatalf("results = %+v, want non-empty Agnes-backed outputs", output.Results)
		}
	}
}

type scriptedToolCallModel struct{}

func (scriptedToolCallModel) Generate(_ context.Context, request gopact.ModelRequest) (gopact.Message, error) {
	if len(request.Tools) != 1 || request.Tools[0].Name != "local.uppercase" {
		return gopact.Message{}, fmt.Errorf("tools = %+v, want local.uppercase", request.Tools)
	}
	return gopact.Message{
		Role: gopact.RoleAssistant,
		ToolCalls: []gopact.ToolCall{{
			ID:        "call-uppercase",
			Name:      "local.uppercase",
			Arguments: []byte(`{"text":"gopact"}`),
		}},
	}, nil
}

type agnesFinalModel struct {
	provider gopact.ResponseModel
	calls    int
}

func (m *agnesFinalModel) Generate(ctx context.Context, request gopact.ModelRequest) (gopact.Message, error) {
	m.calls++
	if !hasMessageContaining(request.Messages, "prefers concise status updates") {
		return gopact.Message{}, errors.New("missing recalled memory in model request")
	}
	if len(request.Messages) == 0 {
		return gopact.Message{}, errors.New("missing tool result message")
	}
	last := request.Messages[len(request.Messages)-1]
	if last.Role != gopact.RoleTool || last.ToolCallID != "call-uppercase" || last.Content != "GOPACT" {
		return gopact.Message{}, fmt.Errorf("last message = %+v, want uppercase tool result", last)
	}
	request.Tools = nil
	response, err := m.provider.Generate(ctx, gopact.ApplyModelRequestOptions(
		request,
		gopact.WithMaxOutputTokens(512),
		gopact.WithTemperature(0.2),
		agnes.DisableThinking(),
	))
	if err != nil {
		return gopact.Message{}, err
	}
	if strings.TrimSpace(response.Message.Text()) == "" {
		return gopact.Message{}, errors.New("agnes returned empty final message")
	}
	return response.Message, nil
}

func (m *agnesFinalModel) Stream(ctx context.Context, request gopact.ModelRequest) iter.Seq2[gopact.Event, error] {
	return func(yield func(gopact.Event, error) bool) {
		message, err := m.Generate(ctx, request)
		if err != nil {
			yield(gopact.Event{Type: gopact.EventModelProviderAttemptFailed, Err: err}, err)
			return
		}
		yield(gopact.Event{Type: gopact.EventModelMessage, Message: &message}, nil)
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

func hasMessageContaining(messages []gopact.Message, text string) bool {
	for _, message := range messages {
		if strings.Contains(message.Text(), text) {
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
