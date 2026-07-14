//go:build integration

package workflowtest

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact-ext/agents/deep"
	"github.com/gopact-ai/gopact-ext/agents/deepresearch"
	"github.com/gopact-ai/gopact-ext/agents/loop"
	"github.com/gopact-ai/gopact-ext/agents/parallel"
	"github.com/gopact-ai/gopact-ext/agents/planexec"
	"github.com/gopact-ai/gopact-ext/agents/react"
	"github.com/gopact-ai/gopact-ext/agents/router"
	"github.com/gopact-ai/gopact-ext/agents/sequential"
	"github.com/gopact-ai/gopact-ext/agents/supervisor"
	"github.com/gopact-ai/gopact-ext/models/agnes"
	"github.com/gopact-ai/gopact-ext/models/glm"
	storesqlite "github.com/gopact-ai/gopact-ext/stores/sqlite"
	"github.com/gopact-ai/gopact/agent"
	"github.com/gopact-ai/gopact/runlog"
	"github.com/gopact-ai/gopact/workflow"
)

func TestIntegrationWorkflowInvokesRealProviderNode(t *testing.T) {
	for _, tt := range providerCases() {
		t.Run(tt.name, func(t *testing.T) {
			model := tt.new(t)
			wf := workflow.New[string, string]("model-node")
			ask := wf.Node[string, string]("ask", func(ctx context.Context, task string) (string, error) {
				resp, err := model.Invoke(ctx, model.NewRequest(gopact.UserMessage(task)))
				if err != nil {
					return "", err
				}
				return responseText(resp), nil
			})
			wf.Entry(ask)
			wf.Exit(ask)
			log := runlog.NewMemoryLog()
			var events []string
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			out, err := wf.Invoke(
				ctx,
				"Reply with exactly: workflowpong",
				gopact.WithRunID("workflow-"+tt.name),
				gopact.WithEventSink(runlog.NewSink(log)),
				gopact.WithEventHandler(func(_ context.Context, event gopact.Event) error {
					events = append(events, event.Type)
					return nil
				}),
			)
			if err != nil {
				t.Fatalf("Invoke() error = %v", err)
			}
			if !strings.Contains(out, "workflowpong") {
				t.Fatalf("output = %q, want workflowpong", out)
			}
			if !containsEvent(events, workflow.EventWorkflowStarted) || !containsEvent(events, workflow.EventWorkflowCompleted) {
				t.Fatalf("events = %v, want workflow start and complete", events)
			}
			records, err := log.List(context.Background(), runlog.Query{RunID: "workflow-" + tt.name})
			if err != nil {
				t.Fatalf("RunLog.List() error = %v", err)
			}
			if len(records) == 0 {
				t.Fatal("run log records = 0, want workflow audit records")
			}
		})
	}
}

func TestIntegrationWorkflowSwitchesFromMemoryToSQLiteByConfiguration(t *testing.T) {
	for _, provider := range providerCases() {
		t.Run(provider.name, func(t *testing.T) {
			t.Run("memory", func(t *testing.T) {
				runProviderPersistenceScenario(t, provider.new(t), "memory", nil, nil)
			})
			t.Run("sqlite", func(t *testing.T) {
				path := t.TempDir() + "/workflow.db"
				if err := storesqlite.Migrate(path); err != nil {
					t.Fatal(err)
				}
				store, err := storesqlite.Open(path)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = store.Close() }()
				options := sqliteWorkflowOptions(store)
				reopen := func(t *testing.T) []workflow.BuildOption {
					t.Helper()
					if err := store.Close(); err != nil {
						t.Fatal(err)
					}
					store, err = storesqlite.Open(path)
					if err != nil {
						t.Fatal(err)
					}
					return sqliteWorkflowOptions(store)
				}
				runProviderPersistenceScenario(t, provider.new(t), "sqlite", options, reopen)
			})
		})
	}
}

type providerPersistenceContext struct {
	Prompt string `json:"prompt"`
}

func runProviderPersistenceScenario(t *testing.T, model gopact.Model, backend string, options []workflow.BuildOption, reopen func(*testing.T) []workflow.BuildOption) {
	t.Helper()
	marker := "gopact-store-" + backend
	prompt := "Reply with exactly: " + marker
	interrupt := true
	wf := providerPersistenceWorkflow(model, &interrupt, options...)
	runID := "provider-store-" + backend
	_, err := wf.Invoke(context.Background(), prompt, gopact.WithRunID(runID))
	var interrupted workflow.InterruptError
	if !errors.As(err, &interrupted) {
		t.Fatalf("Invoke() error = %v, want interrupt", err)
	}
	if reopen != nil {
		wf = providerPersistenceWorkflow(model, &interrupt, reopen(t)...)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	output, err := wf.Invoke(ctx, "", workflow.WithResume(workflow.ResumeRequest{
		RunID: runID, CheckpointID: interrupted.CheckpointID,
		Resolutions: []workflow.InterruptResolution{{InterruptID: "provider-approval", PayloadRef: "resolution://approved"}},
	}))
	if err != nil || !strings.Contains(output, marker) {
		t.Fatalf("Resume() = %q, %v, want marker %q", output, err, marker)
	}
	snapshot, err := wf.Snapshot(context.Background(), workflow.SnapshotRequest{RunID: runID})
	if err != nil || len(snapshot.Timeline) == 0 || len(snapshot.Checkpoints) == 0 {
		t.Fatalf("Snapshot() = %+v, %v, want history", snapshot, err)
	}
	assertProviderPersistenceFacts(t, snapshot.Timeline, prompt, marker)
}

func providerPersistenceWorkflow(model gopact.Model, interrupt *bool, options ...workflow.BuildOption) *workflow.Workflow[string, string] {
	wf := workflow.New[string, string]("provider-persistence", options...)
	state := wf.Context(func(prompt string) providerPersistenceContext {
		return providerPersistenceContext{Prompt: prompt}
	})
	approval := wf.Node("approval", func(_ context.Context, prompt string) (string, error) { return prompt, nil })
	approval.Guard(workflow.BeforeRun("approval", workflow.GuardFunc[string, string](
		func(context.Context, workflow.GuardContext[string, string]) (workflow.GuardDecision[string, string], error) {
			if !*interrupt {
				return workflow.GuardAllow[string, string]{}, nil
			}
			*interrupt = false
			return workflow.GuardInterrupt[string, string]{Request: workflow.InterruptRequest{ID: "provider-approval"}}, nil
		},
	)))
	modelNode := wf.Node("model", func(ctx context.Context, prompt string) (string, error) {
		current, err := state.Get(ctx)
		if err != nil {
			return "", err
		}
		if current.Prompt != prompt {
			return "", errors.New("provider node received context/input mismatch")
		}
		response, err := model.Invoke(ctx, model.NewRequest(gopact.UserMessage(prompt)))
		if err != nil {
			return "", err
		}
		return responseText(response), nil
	})
	wf.Entry(approval)
	wf.Edge(approval, modelNode)
	wf.Exit(modelNode)
	return wf
}

func sqliteWorkflowOptions(store *storesqlite.Store) []workflow.BuildOption {
	return []workflow.BuildOption{workflow.WithStore(store)}
}

func assertProviderPersistenceFacts(t *testing.T, records []runlog.Record, prompt, marker string) {
	t.Helper()
	for _, record := range records {
		if record.NodeID != "model" || record.EventType != workflow.EventNodeCompleted {
			continue
		}
		var facts workflow.NodeEventPayload
		if err := json.Unmarshal(record.Payload, &facts); err != nil {
			t.Fatal(err)
		}
		var current providerPersistenceContext
		if err := json.Unmarshal(facts.WorkflowContext.JSON, &current); err != nil {
			t.Fatal(err)
		}
		if facts.EffectiveInput == nil || string(facts.EffectiveInput.JSON) != `"`+prompt+`"` ||
			facts.Output == nil || !strings.Contains(string(facts.Output.JSON), marker) || current.Prompt != prompt {
			t.Fatalf("provider persistence facts = %+v/%+v, want input/output/full context", facts, current)
		}
		return
	}
	t.Fatal("provider completed facts not found")
}

func TestIntegrationWorkflowInvokesReactAgentNode(t *testing.T) {
	for _, tt := range providerCases() {
		t.Run(tt.name, func(t *testing.T) {
			marker := "gopact-react-tool-" + tt.name
			tool := &integrationMarkerTool{name: "lookup_marker", marker: marker}
			runner, err := react.New(agent.Identity{
				Name:        "provider-agent",
				Description: "answers through a real provider",
				Version:     "v1",
			}, tt.new(t), react.WithInstruction(
				"You must call lookup_marker exactly once. After the tool result, reply with exactly that marker and nothing else.",
			), react.WithTools(tool))
			if err != nil {
				t.Fatalf("react.New() error = %v", err)
			}
			wf := workflow.New[agent.Request, agent.Response]("agent-node")
			agentNode := wf.AddInvokable("agent", runner)
			wf.Entry(agentNode)
			wf.Exit(agentNode)
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			runID := "react-agent-node-" + tt.name
			var events []gopact.Event
			out, err := wf.Invoke(ctx, agent.Request{
				Messages: []gopact.Message{gopact.UserMessage("Use the required tool and return its marker.")},
			}, gopact.WithRunID(runID), gopact.WithStrictEventHandler(func(_ context.Context, event gopact.Event) error {
				events = append(events, event)
				return nil
			}))
			if err != nil {
				t.Fatalf("Invoke() error = %v", err)
			}
			if len(out.Message.Parts) == 0 || !strings.Contains(out.Message.Parts[0].Text, marker) || tool.calls != 1 {
				t.Fatalf("Message/tool calls = %+v/%d, want marker %q from one real tool turn", out.Message, tool.calls, marker)
			}
			assertReactToolFacts(t, events, runID)
		})
	}
}

type integrationMarkerTool struct {
	name   string
	marker string
	calls  int
}

func (tool *integrationMarkerTool) Spec() gopact.ToolSpec {
	return gopact.ToolSpec{
		Name: tool.name, Description: "Returns the required test marker.", Schema: json.RawMessage(`{"type":"object","properties":{}}`),
	}
}

func (tool *integrationMarkerTool) ExecuteTool(_ context.Context, call gopact.ToolCall) (gopact.ToolOutcome, error) {
	tool.calls++
	return gopact.ToolResultOutcome{
		CallID: call.ID, Name: call.Name, Result: gopact.ToolResult{Preview: tool.marker},
	}, nil
}

func assertReactToolFacts(t *testing.T, events []gopact.Event, parentRunID string) {
	t.Helper()
	var childRunID string
	for _, event := range events {
		if event.ParentRunID == parentRunID && event.Type == workflow.EventWorkflowStarted {
			childRunID = event.RunID
			break
		}
	}
	if childRunID == "" {
		t.Fatal("React child Run is missing")
	}
	var nodes []string
	for _, event := range events {
		if node, ok := reactCompletedNode(t, event, childRunID); ok {
			nodes = append(nodes, node)
		}
	}
	want := []string{
		"prepare", "model", "continue", "dispatch-tools", "tool", "observe-tools", "continue", "prepare", "model", "finish",
	}
	if !reflect.DeepEqual(nodes, want) {
		t.Fatalf("React child nodes = %v, want real model-tool trajectory %v", nodes, want)
	}
}

func reactCompletedNode(t *testing.T, event gopact.Event, runID string) (string, bool) {
	t.Helper()
	if event.RunID != runID || event.Type != workflow.EventNodeCompleted {
		return "", false
	}
	var facts workflow.NodeEventPayload
	if err := json.Unmarshal(event.Payload, &facts); err != nil {
		t.Fatalf("decode React %s facts: %v", event.NodeID, err)
	}
	if facts.EffectiveInput == nil || facts.Output == nil || len(facts.WorkflowContext.JSON) == 0 {
		t.Fatalf("React %s facts = %+v, want input/output/full context", event.NodeID, facts)
	}
	return event.NodeID, true
}

func TestIntegrationSequentialAgentUsesWorkflowChildRuns(t *testing.T) {
	for _, tt := range providerCases() {
		t.Run(tt.name, func(t *testing.T) {
			marker := "gopact-sequential-" + tt.name
			providerIdentity := agent.Identity{Name: "provider-" + tt.name, Description: "calls the real provider", Version: "v1"}
			providerAgent := newProviderWorkflowAgent(t, providerIdentity, tt.new(t))

			auditIdentity := agent.Identity{Name: "audit-" + tt.name, Description: "checks the provider response", Version: "v1"}
			auditWorkflow := workflow.New[agent.Request, agent.Response](auditIdentity.Name, workflow.WithTopologyVersion(auditIdentity.Version))
			audit := auditWorkflow.Node("audit", func(_ context.Context, request agent.Request) (agent.Response, error) {
				if len(request.Messages) != 1 || !strings.Contains(request.Messages[0].Parts[0].Text, marker) {
					return agent.Response{}, errors.New("audit child did not receive provider output")
				}
				return agent.Response{Message: request.Messages[0]}, nil
			})
			auditWorkflow.Entry(audit)
			auditWorkflow.Exit(audit)
			auditAgent, err := agent.NewWorkflowAgent(auditIdentity, auditWorkflow)
			if err != nil {
				t.Fatal(err)
			}

			catalog := agent.NewCatalog()
			if err := catalog.Add(providerAgent); err != nil {
				t.Fatal(err)
			}
			if err := catalog.Add(auditAgent); err != nil {
				t.Fatal(err)
			}
			directory, err := catalog.Compile()
			if err != nil {
				t.Fatal(err)
			}
			target, err := sequential.New(
				agent.Identity{Name: "sequential-" + tt.name, Description: "provider then audit", Version: "v1"},
				directory,
				[]string{providerIdentity.Name, auditIdentity.Name},
			)
			if err != nil {
				t.Fatal(err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			runID := "sequential-" + tt.name + "-run"
			var events []gopact.Event
			result, err := target.Invoke(
				ctx,
				agent.Request{Messages: []gopact.Message{gopact.UserMessage("Reply with exactly: " + marker)}},
				gopact.WithRunID(runID),
				gopact.WithStrictEventHandler(func(_ context.Context, event gopact.Event) error {
					events = append(events, event)
					return nil
				}),
			)
			if err != nil {
				t.Fatalf("Invoke() error = %v", err)
			}
			if len(result.Message.Parts) != 1 || !strings.Contains(result.Message.Parts[0].Text, marker) {
				t.Fatalf("response = %+v, want marker %q", result, marker)
			}
			assertSequentialFacts(t, events, runID, marker, []string{providerIdentity.Name, auditIdentity.Name})
		})
	}
}

func TestIntegrationPlanExecAgentUsesWorkflowPlanAndChildRun(t *testing.T) {
	for _, tt := range providerCases() {
		t.Run(tt.name, func(t *testing.T) {
			marker := "gopact-planexec-" + tt.name
			providerIdentity := agent.Identity{Name: "plan-worker-" + tt.name, Description: "executes a real provider step", Version: "v1"}
			providerAgent := newProviderWorkflowAgent(t, providerIdentity, tt.new(t))
			catalog := agent.NewCatalog()
			if err := catalog.Add(providerAgent); err != nil {
				t.Fatal(err)
			}
			directory, err := catalog.Compile()
			if err != nil {
				t.Fatal(err)
			}
			target, err := planexec.New(
				agent.Identity{Name: "planexec-" + tt.name, Description: "plans one real provider step", Version: "v1"},
				planexec.WithDirectory(directory),
				planexec.WithPlanner(planexec.PlannerFunc(func(context.Context, planexec.PlanInput) (planexec.Plan, error) {
					return planexec.Plan{ID: "provider-plan", Version: 1, Steps: []planexec.Step{{
						ID: "answer", Description: "answer with the provider", AgentName: providerIdentity.Name,
					}}}, nil
				})),
				planexec.WithReplanner(planexec.ReplannerFunc(func(_ context.Context, input planexec.ReplanInput) (planexec.ReplanDecision, error) {
					return planexec.ReplanDecision{Done: len(input.Results) == 1}, nil
				})),
				planexec.WithReporter(planexec.ReporterFunc(func(_ context.Context, input planexec.ReportInput) (agent.Response, error) {
					if len(input.Results) != 1 || !strings.Contains(input.Results[0].Response.Message.Parts[0].Text, marker) {
						return agent.Response{}, errors.New("planexec reporter did not receive provider result")
					}
					return input.Results[0].Response, nil
				})),
			)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			runID := "planexec-" + tt.name + "-run"
			var events []gopact.Event
			response, err := target.Invoke(ctx, agent.Request{
				Messages: []gopact.Message{gopact.UserMessage("Reply with exactly: " + marker)},
			}, gopact.WithRunID(runID), gopact.WithStrictEventHandler(func(_ context.Context, event gopact.Event) error {
				events = append(events, event)
				return nil
			}))
			if err != nil {
				t.Fatalf("Invoke() error = %v", err)
			}
			if len(response.Message.Parts) != 1 || !strings.Contains(response.Message.Parts[0].Text, marker) {
				t.Fatalf("response = %+v, want marker %q", response, marker)
			}
			assertPlanExecFacts(t, events, runID)
		})
	}
}

func assertPlanExecFacts(t *testing.T, events []gopact.Event, runID string) {
	t.Helper()
	var nodes []string
	childRuns := map[string]struct{}{}
	for _, event := range events {
		if event.ParentRunID == runID && event.Type == workflow.EventWorkflowStarted {
			childRuns[event.RunID] = struct{}{}
		}
		if event.RunID != runID || event.Type != workflow.EventNodeCompleted {
			continue
		}
		nodes = append(nodes, event.NodeID)
		var facts workflow.NodeEventPayload
		if err := json.Unmarshal(event.Payload, &facts); err != nil {
			t.Fatal(err)
		}
		if facts.EffectiveInput == nil || facts.Output == nil || len(facts.WorkflowContext.JSON) == 0 {
			t.Fatalf("PlanExec %s facts = %+v, want input/output/full context", event.NodeID, facts)
		}
	}
	want := []string{"plan", "accept-plan", "continue", "dispatch-step", "execute-step", "record-step", "replan", "continue", "report"}
	if !reflect.DeepEqual(nodes, want) || len(childRuns) != 1 {
		t.Fatalf("PlanExec nodes/child runs = %v/%v, want %v and one child Run", nodes, childRuns, want)
	}
}

func TestIntegrationSupervisorAgentUsesWorkflowDelegationLoop(t *testing.T) {
	for _, tt := range providerCases() {
		t.Run(tt.name, func(t *testing.T) {
			marker := "gopact-supervisor-" + tt.name
			providerIdentity := agent.Identity{Name: "supervised-worker-" + tt.name, Description: "handles delegated work", Version: "v1"}
			providerAgent := newProviderWorkflowAgent(t, providerIdentity, tt.new(t))
			catalog := agent.NewCatalog()
			if err := catalog.Add(providerAgent); err != nil {
				t.Fatal(err)
			}
			directory, err := catalog.Compile()
			if err != nil {
				t.Fatal(err)
			}
			target, err := supervisor.New(
				agent.Identity{Name: "supervisor-" + tt.name, Description: "delegates one real provider task", Version: "v1"},
				directory,
				supervisor.DeciderFunc(func(_ context.Context, input supervisor.DecisionInput) (supervisor.Decision, error) {
					return integrationSupervisorDecision(input, providerIdentity.Name, marker)
				}),
			)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			runID := "supervisor-" + tt.name + "-run"
			var events []gopact.Event
			response, err := target.Invoke(
				ctx,
				agent.Request{Messages: []gopact.Message{gopact.UserMessage("delegate this task")}},
				gopact.WithRunID(runID),
				gopact.WithStrictEventHandler(func(_ context.Context, event gopact.Event) error {
					events = append(events, event)
					return nil
				}),
			)
			if err != nil {
				t.Fatalf("Invoke() error = %v", err)
			}
			if len(response.Message.Parts) != 1 || !strings.Contains(response.Message.Parts[0].Text, marker) {
				t.Fatalf("response = %+v, want marker %q", response, marker)
			}
			assertSupervisorFacts(t, events, runID)
		})
	}
}

func integrationSupervisorDecision(input supervisor.DecisionInput, child, marker string) (supervisor.Decision, error) {
	if len(input.Results) == 0 {
		return supervisor.Decision{
			Kind: supervisor.DecisionDelegate, Child: child,
			Request: agent.Request{Messages: []gopact.Message{gopact.UserMessage("Reply with exactly: " + marker)}},
		}, nil
	}
	if len(input.Results) != 1 {
		return supervisor.Decision{}, errors.New("supervisor received unexpected delegation results")
	}
	if !strings.Contains(input.Results[0].Response.Message.Parts[0].Text, marker) {
		return supervisor.Decision{}, errors.New("supervisor did not receive provider result")
	}
	response := input.Results[0].Response
	return supervisor.Decision{Kind: supervisor.DecisionFinal, Response: &response}, nil
}

func assertSupervisorFacts(t *testing.T, events []gopact.Event, runID string) {
	t.Helper()
	var nodes []string
	childRuns := map[string]struct{}{}
	for _, event := range events {
		if event.ParentRunID == runID && event.Type == workflow.EventWorkflowStarted {
			childRuns[event.RunID] = struct{}{}
		}
		if event.RunID != runID || event.Type != workflow.EventNodeCompleted {
			continue
		}
		nodes = append(nodes, event.NodeID)
		var facts workflow.NodeEventPayload
		if err := json.Unmarshal(event.Payload, &facts); err != nil {
			t.Fatal(err)
		}
		if facts.EffectiveInput == nil || facts.Output == nil || len(facts.WorkflowContext.JSON) == 0 {
			t.Fatalf("Supervisor %s facts = %+v, want input/output/full context", event.NodeID, facts)
		}
	}
	want := []string{"start", "decide", "delegate", "record", "decide", "finish"}
	if !reflect.DeepEqual(nodes, want) || len(childRuns) != 1 {
		t.Fatalf("Supervisor nodes/child runs = %v/%v, want %v and one child Run", nodes, childRuns, want)
	}
}

func TestIntegrationDeepAgentCarriesProviderArtifactAcrossTasks(t *testing.T) {
	for _, tt := range providerCases() {
		t.Run(tt.name, func(t *testing.T) {
			marker := "gopact-deep-" + tt.name
			artifactURI := "artifact://" + marker
			providerIdentity := agent.Identity{Name: "deep-provider-" + tt.name, Description: "collects evidence", Version: "v1"}
			providerWorkflow := workflow.New[agent.Request, agent.Response](providerIdentity.Name, workflow.WithTopologyVersion(providerIdentity.Version))
			model := tt.new(t)
			modelNode := providerWorkflow.Node("model", func(ctx context.Context, request agent.Request) (agent.Response, error) {
				response, err := model.Invoke(ctx, model.NewRequest(request.Messages...))
				if err != nil {
					return agent.Response{}, err
				}
				return agent.Response{
					Message:   gopact.Message{Role: "assistant", Parts: []gopact.MessagePart{{Type: "text", Text: responseText(response)}}},
					Artifacts: []gopact.ArtifactRef{{URI: artifactURI}},
				}, nil
			})
			providerWorkflow.Entry(modelNode)
			providerWorkflow.Exit(modelNode)
			providerAgent, err := agent.NewWorkflowAgent(providerIdentity, providerWorkflow)
			if err != nil {
				t.Fatal(err)
			}
			auditIdentity := agent.Identity{Name: "deep-audit-" + tt.name, Description: "checks carried context", Version: "v1"}
			auditWorkflow := workflow.New[agent.Request, agent.Response](auditIdentity.Name, workflow.WithTopologyVersion(auditIdentity.Version))
			auditNode := auditWorkflow.Node("audit", func(_ context.Context, request agent.Request) (agent.Response, error) {
				if len(request.Artifacts) != 1 || request.Artifacts[0].URI != artifactURI {
					return agent.Response{}, errors.New("deep audit did not receive provider artifact")
				}
				return agent.Response{Message: gopact.UserMessage(marker + "-audited")}, nil
			})
			auditWorkflow.Entry(auditNode)
			auditWorkflow.Exit(auditNode)
			auditAgent, err := agent.NewWorkflowAgent(auditIdentity, auditWorkflow)
			if err != nil {
				t.Fatal(err)
			}
			catalog := agent.NewCatalog()
			if err := catalog.Add(providerAgent); err != nil {
				t.Fatal(err)
			}
			if err := catalog.Add(auditAgent); err != nil {
				t.Fatal(err)
			}
			directory, err := catalog.Compile()
			if err != nil {
				t.Fatal(err)
			}
			target, err := deep.New(
				agent.Identity{Name: "deep-" + tt.name, Description: "runs evidence and audit tasks", Version: "v1"},
				directory,
				deep.PlannerFunc(func(context.Context, deep.PlanInput) ([]deep.Task, error) {
					return []deep.Task{
						{ID: "collect", Description: "collect evidence", AgentName: providerIdentity.Name},
						{ID: "audit", Description: "audit evidence", AgentName: auditIdentity.Name},
					}, nil
				}),
			)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			runID := "deep-" + tt.name + "-run"
			var events []gopact.Event
			response, err := target.Invoke(
				ctx,
				agent.Request{Messages: []gopact.Message{gopact.UserMessage("Reply with exactly: " + marker)}},
				gopact.WithRunID(runID),
				gopact.WithStrictEventHandler(func(_ context.Context, event gopact.Event) error {
					events = append(events, event)
					return nil
				}),
			)
			if err != nil {
				t.Fatalf("Invoke() error = %v", err)
			}
			if response.Message.Parts[0].Text != marker+"-audited" {
				t.Fatalf("response = %+v, want audited marker", response)
			}
			assertDeepFacts(t, events, runID)
		})
	}
}

func assertDeepFacts(t *testing.T, events []gopact.Event, runID string) {
	t.Helper()
	var nodes []string
	childRuns := map[string]struct{}{}
	for _, event := range events {
		if event.ParentRunID == runID && event.Type == workflow.EventWorkflowStarted {
			childRuns[event.RunID] = struct{}{}
		}
		if event.RunID != runID || event.Type != workflow.EventNodeCompleted {
			continue
		}
		nodes = append(nodes, event.NodeID)
		var facts workflow.NodeEventPayload
		if err := json.Unmarshal(event.Payload, &facts); err != nil {
			t.Fatal(err)
		}
		if facts.EffectiveInput == nil || facts.Output == nil || len(facts.WorkflowContext.JSON) == 0 {
			t.Fatalf("Deep %s facts = %+v, want input/output/full context", event.NodeID, facts)
		}
	}
	want := []string{
		"plan", "accept-plan", "continue", "build-context", "execute-task", "record-task",
		"continue", "build-context", "execute-task", "record-task", "continue", "finish",
	}
	if !reflect.DeepEqual(nodes, want) || len(childRuns) != 2 {
		t.Fatalf("Deep nodes/child runs = %v/%v, want %v and two child Runs", nodes, childRuns, want)
	}
}

func TestIntegrationDeepResearchAgentSynthesizesVerifiedEvidence(t *testing.T) {
	for _, tt := range providerCases() {
		t.Run(tt.name, func(t *testing.T) {
			marker := "gopact-deepresearch-" + tt.name
			model := tt.new(t)
			target, err := deepresearch.New(
				agent.Identity{Name: "deepresearch-" + tt.name, Description: "researches and cites evidence", Version: "v1"},
				deepresearch.WithPlanner(deepresearch.PlannerFunc(func(context.Context, deepresearch.PlanInput) ([]deepresearch.Query, error) {
					return []deepresearch.Query{{ID: "q1", Text: "primary"}, {ID: "q2", Text: "cross-check"}}, nil
				})),
				deepresearch.WithDiscoverer(deepresearch.DiscovererFunc(func(_ context.Context, query deepresearch.Query) ([]deepresearch.Source, error) {
					return []deepresearch.Source{{
						ID: "source", QueryID: query.ID, URI: "https://example.test/evidence", Title: "evidence",
					}}, nil
				})),
				deepresearch.WithFetcher(deepresearch.FetcherFunc(func(_ context.Context, source deepresearch.Source) (deepresearch.Source, error) {
					source.Content = "Verified marker: " + marker
					return source, nil
				})),
				deepresearch.WithEvidenceExtractor(deepresearch.EvidenceExtractorFunc(func(_ context.Context, source deepresearch.Source) ([]deepresearch.Evidence, error) {
					return []deepresearch.Evidence{{ID: "e1", SourceID: source.ID, Claim: marker, Quote: source.Content}}, nil
				})),
				deepresearch.WithCitationVerifier(deepresearch.CitationVerifierFunc(func(_ context.Context, input deepresearch.SynthesisInput) error {
					if len(input.Citations) != 1 || input.Citations[0].EvidenceID != "e1" || input.Citations[0].SourceID != "source" {
						return errors.New("deepresearch verifier received invalid citation ledger")
					}
					return nil
				})),
				deepresearch.WithSynthesizer(deepresearch.SynthesizerFunc(func(ctx context.Context, input deepresearch.SynthesisInput) (agent.Response, error) {
					if len(input.Sources) != 1 || len(input.Evidence) != 1 || len(input.Sources[0].QueryIDs) != 2 {
						return agent.Response{}, errors.New("deepresearch synthesizer received incomplete evidence")
					}
					response, err := model.Invoke(ctx, model.NewRequest(gopact.UserMessage("Reply with exactly: "+marker)))
					if err != nil {
						return agent.Response{}, err
					}
					return agent.Response{Message: gopact.Message{
						Role: "assistant", Parts: []gopact.MessagePart{{Type: "text", Text: responseText(response)}},
					}}, nil
				})),
			)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			runID := "deepresearch-" + tt.name + "-run"
			var events []gopact.Event
			response, err := target.Invoke(
				ctx,
				agent.Request{Messages: []gopact.Message{gopact.UserMessage("research the marker")}},
				gopact.WithRunID(runID),
				gopact.WithStrictEventHandler(func(_ context.Context, event gopact.Event) error {
					events = append(events, event)
					return nil
				}),
			)
			if err != nil {
				t.Fatalf("Invoke() error = %v", err)
			}
			if len(response.Message.Parts) != 1 || !strings.Contains(response.Message.Parts[0].Text, marker) {
				t.Fatalf("response = %+v, want marker %q", response, marker)
			}
			assertDeepResearchFacts(t, events, runID)
		})
	}
}

func assertDeepResearchFacts(t *testing.T, events []gopact.Event, runID string) {
	t.Helper()
	var nodes []string
	for _, event := range events {
		if event.RunID != runID || event.Type != workflow.EventNodeCompleted {
			continue
		}
		nodes = append(nodes, event.NodeID)
		var facts workflow.NodeEventPayload
		if err := json.Unmarshal(event.Payload, &facts); err != nil {
			t.Fatal(err)
		}
		if facts.EffectiveInput == nil || facts.Output == nil || len(facts.WorkflowContext.JSON) == 0 {
			t.Fatalf("DeepResearch %s facts = %+v, want input/output/full context", event.NodeID, facts)
		}
	}
	want := []string{
		"plan", "accept-plan", "discover", "discover", "collect-sources",
		"continue-fetch", "fetch-source", "record-source", "continue-fetch",
		"continue-evidence", "extract-evidence", "record-evidence", "continue-evidence",
		"verify-citations", "synthesize",
	}
	if !reflect.DeepEqual(nodes, want) {
		t.Fatalf("DeepResearch nodes = %v, want %v", nodes, want)
	}
}

func TestIntegrationParallelAgentUsesWorkflowFanoutAndMerge(t *testing.T) {
	for _, tt := range providerCases() {
		t.Run(tt.name, func(t *testing.T) {
			marker := "gopact-parallel-" + tt.name
			providerStarted := make(chan struct{})
			providerIdentity := agent.Identity{Name: "parallel-provider-" + tt.name, Description: "calls the real provider", Version: "v1"}
			providerWorkflow := workflow.New[agent.Request, agent.Response](providerIdentity.Name, workflow.WithTopologyVersion(providerIdentity.Version))
			model := tt.new(t)
			providerNode := providerWorkflow.Node("model", func(ctx context.Context, request agent.Request) (agent.Response, error) {
				close(providerStarted)
				response, err := model.Invoke(ctx, model.NewRequest(request.Messages...))
				if err != nil {
					return agent.Response{}, err
				}
				return agent.Response{Message: gopact.Message{
					Role: "assistant", Parts: []gopact.MessagePart{{Type: "text", Text: responseText(response)}},
				}}, nil
			})
			providerWorkflow.Entry(providerNode)
			providerWorkflow.Exit(providerNode)
			providerAgent, err := agent.NewWorkflowAgent(providerIdentity, providerWorkflow)
			if err != nil {
				t.Fatal(err)
			}

			auditIdentity := agent.Identity{Name: "parallel-audit-" + tt.name, Description: "checks the shared input", Version: "v1"}
			auditWorkflow := workflow.New[agent.Request, agent.Response](auditIdentity.Name, workflow.WithTopologyVersion(auditIdentity.Version))
			auditNode := auditWorkflow.Node("audit", func(ctx context.Context, request agent.Request) (agent.Response, error) {
				select {
				case <-providerStarted:
				case <-ctx.Done():
					return agent.Response{}, ctx.Err()
				}
				if len(request.Messages) != 1 || !strings.Contains(request.Messages[0].Parts[0].Text, marker) {
					return agent.Response{}, errors.New("parallel audit did not receive the shared input")
				}
				return agent.Response{Message: gopact.UserMessage("audit-ok")}, nil
			})
			auditWorkflow.Entry(auditNode)
			auditWorkflow.Exit(auditNode)
			auditAgent, err := agent.NewWorkflowAgent(auditIdentity, auditWorkflow)
			if err != nil {
				t.Fatal(err)
			}

			catalog := agent.NewCatalog()
			if err := catalog.Add(providerAgent); err != nil {
				t.Fatal(err)
			}
			if err := catalog.Add(auditAgent); err != nil {
				t.Fatal(err)
			}
			directory, err := catalog.Compile()
			if err != nil {
				t.Fatal(err)
			}
			target, err := parallel.New(
				agent.Identity{Name: "parallel-" + tt.name, Description: "provider and audit panel", Version: "v1"},
				directory,
				[]string{providerIdentity.Name, auditIdentity.Name},
				parallel.ReducerFunc(func(_ context.Context, results []parallel.BranchResult) (agent.Response, error) {
					providerText := results[0].Response.Message.Parts[0].Text
					if !strings.Contains(providerText, marker) || results[1].Response.Message.Parts[0].Text != "audit-ok" {
						return agent.Response{}, errors.New("parallel reducer received incomplete branch results")
					}
					return agent.Response{Message: results[0].Response.Message}, nil
				}),
				parallel.WithMaxParallelism(2),
			)
			if err != nil {
				t.Fatal(err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			runID := "parallel-" + tt.name + "-run"
			var events []gopact.Event
			response, err := target.Invoke(ctx, agent.Request{
				Messages: []gopact.Message{gopact.UserMessage("Reply with exactly: " + marker)},
			}, gopact.WithRunID(runID), gopact.WithStrictEventHandler(func(_ context.Context, event gopact.Event) error {
				events = append(events, event)
				return nil
			}))
			if err != nil {
				t.Fatalf("Invoke() error = %v", err)
			}
			if len(response.Message.Parts) != 1 || !strings.Contains(response.Message.Parts[0].Text, marker) {
				t.Fatalf("response = %+v, want marker %q", response, marker)
			}
			assertParallelFacts(t, events, runID, marker, []string{providerIdentity.Name, auditIdentity.Name})
		})
	}
}

func TestIntegrationRouterAgentUsesWorkflowRouteAndChildRun(t *testing.T) {
	for _, tt := range providerCases() {
		t.Run(tt.name, func(t *testing.T) {
			marker := "gopact-router-" + tt.name
			providerIdentity := agent.Identity{Name: "router-provider-" + tt.name, Description: "calls the real provider", Version: "v1"}
			providerAgent := newProviderWorkflowAgent(t, providerIdentity, tt.new(t))
			fallbackIdentity := agent.Identity{Name: "fallback-" + tt.name, Description: "must not run", Version: "v1"}
			fallbackWorkflow := workflow.New[agent.Request, agent.Response](fallbackIdentity.Name, workflow.WithTopologyVersion(fallbackIdentity.Version))
			fallback := fallbackWorkflow.Node("fallback", func(context.Context, agent.Request) (agent.Response, error) {
				return agent.Response{}, errors.New("fallback child was selected")
			})
			fallbackWorkflow.Entry(fallback)
			fallbackWorkflow.Exit(fallback)
			fallbackAgent, err := agent.NewWorkflowAgent(fallbackIdentity, fallbackWorkflow)
			if err != nil {
				t.Fatal(err)
			}
			catalog := agent.NewCatalog()
			if err := catalog.Add(providerAgent); err != nil {
				t.Fatal(err)
			}
			if err := catalog.Add(fallbackAgent); err != nil {
				t.Fatal(err)
			}
			directory, err := catalog.Compile()
			if err != nil {
				t.Fatal(err)
			}
			target, err := router.New(
				agent.Identity{Name: "router-" + tt.name, Description: "selects a provider child", Version: "v1"},
				directory,
				router.SelectorFunc(func(_ context.Context, _ agent.Request, candidates []agent.Identity) (router.Selection, error) {
					if len(candidates) != 2 {
						return router.Selection{}, errors.New("router candidates are incomplete")
					}
					return router.Selection{Child: providerIdentity.Name, Reason: "real provider"}, nil
				}),
			)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			runID := "router-" + tt.name + "-run"
			var events []gopact.Event
			response, err := target.Invoke(ctx, agent.Request{
				Messages: []gopact.Message{gopact.UserMessage("Reply with exactly: " + marker)},
			}, gopact.WithRunID(runID), gopact.WithStrictEventHandler(func(_ context.Context, event gopact.Event) error {
				events = append(events, event)
				return nil
			}))
			if err != nil {
				t.Fatalf("Invoke() error = %v", err)
			}
			if len(response.Message.Parts) != 1 || !strings.Contains(response.Message.Parts[0].Text, marker) {
				t.Fatalf("response = %+v, want marker %q", response, marker)
			}
			assertRouterFacts(t, events, runID, marker, providerIdentity.Name)
		})
	}
}

func TestIntegrationLoopAgentUsesWorkflowIterations(t *testing.T) {
	for _, tt := range providerCases() {
		t.Run(tt.name, func(t *testing.T) {
			marker := "gopact-loop-" + tt.name
			childIdentity := agent.Identity{Name: "loop-provider-" + tt.name, Description: "calls provider once", Version: "v1"}
			childWorkflow := workflow.New[agent.Request, agent.Response](childIdentity.Name, workflow.WithTopologyVersion(childIdentity.Version))
			model := tt.new(t)
			modelNode := childWorkflow.Node("model", func(ctx context.Context, request agent.Request) (agent.Response, error) {
				if request.Metadata["provider_done"] == "true" {
					return agent.Response{Message: request.Messages[0], Metadata: request.Metadata}, nil
				}
				response, err := model.Invoke(ctx, model.NewRequest(request.Messages...))
				if err != nil {
					return agent.Response{}, err
				}
				return agent.Response{
					Message:  gopact.Message{Role: "assistant", Parts: []gopact.MessagePart{{Type: "text", Text: responseText(response)}}},
					Metadata: map[string]string{"provider_done": "true"},
				}, nil
			})
			childWorkflow.Entry(modelNode)
			childWorkflow.Exit(modelNode)
			child, err := agent.NewWorkflowAgent(childIdentity, childWorkflow)
			if err != nil {
				t.Fatal(err)
			}
			target, err := loop.New(
				agent.Identity{Name: "loop-" + tt.name, Description: "two iteration provider flow", Version: "v1"},
				child,
				loop.ConditionFunc(func(_ context.Context, iteration loop.Iteration) (loop.Decision, error) {
					if iteration.Number == 1 {
						return loop.DecisionContinue, nil
					}
					return loop.DecisionStop, nil
				}),
				loop.WithMaxIterations(2),
			)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			runID := "loop-" + tt.name + "-run"
			var events []gopact.Event
			response, err := target.Invoke(ctx, agent.Request{
				Messages: []gopact.Message{gopact.UserMessage("Reply with exactly: " + marker)},
			}, gopact.WithRunID(runID), gopact.WithStrictEventHandler(func(_ context.Context, event gopact.Event) error {
				events = append(events, event)
				return nil
			}))
			if err != nil {
				t.Fatalf("Invoke() error = %v", err)
			}
			if len(response.Message.Parts) != 1 || !strings.Contains(response.Message.Parts[0].Text, marker) {
				t.Fatalf("response = %+v, want marker %q", response, marker)
			}
			assertLoopFacts(t, events, runID, childIdentity.Name)
		})
	}
}

func assertLoopFacts(t *testing.T, events []gopact.Event, runID, childName string) {
	t.Helper()
	var nodes []string
	var versions []int64
	childRuns := map[string]struct{}{}
	for _, event := range events {
		if event.ParentRunID == runID && event.Type == workflow.EventWorkflowStarted {
			childRuns[event.RunID] = struct{}{}
		}
		if event.RunID != runID || event.Type != workflow.EventNodeStarted {
			continue
		}
		nodes = append(nodes, event.NodeID)
		versions = append(versions, event.NodeExecutionVersion)
	}
	wantNodes := []string{"child." + childName, "condition", "child." + childName, "condition", "finish"}
	wantVersions := []int64{1, 1, 2, 2, 1}
	if !reflect.DeepEqual(nodes, wantNodes) || !reflect.DeepEqual(versions, wantVersions) || len(childRuns) != 2 {
		t.Fatalf("nodes/versions/child runs = %v/%v/%v, want %v/%v/two", nodes, versions, childRuns, wantNodes, wantVersions)
	}
}

func assertParallelFacts(t *testing.T, events []gopact.Event, runID, marker string, childNames []string) {
	t.Helper()
	var completed []string
	childRuns := map[string]struct{}{}
	var sessionID string
	for _, event := range events {
		if event.RunID == runID {
			sessionID = event.SessionID
			break
		}
	}
	if sessionID == "" {
		t.Fatalf("events = %+v, want root session", events)
	}
	for _, event := range events {
		recordParallelChildRun(t, childRuns, event, runID, sessionID)
		if event.RunID != runID || event.Type != workflow.EventNodeCompleted {
			continue
		}
		completed = append(completed, event.NodeID)
		var facts workflow.NodeEventPayload
		if err := json.Unmarshal(event.Payload, &facts); err != nil {
			t.Fatalf("decode %s facts: %v", event.NodeID, err)
		}
		if strings.HasPrefix(event.NodeID, "child.") {
			assertSequentialRequestFact(t, event.NodeID, "effective input", marker, facts.EffectiveInput)
		}
		if (event.NodeID == "plan" || event.NodeID == "merge") && facts.Output == nil {
			t.Fatalf("%s output is missing", event.NodeID)
		}
	}
	want := []string{"plan", "child." + childNames[0], "child." + childNames[1], "merge"}
	if !reflect.DeepEqual(completed, want) || len(childRuns) != len(childNames) {
		t.Fatalf("completed/child runs = %v/%v, want %v and one child Run each", completed, childRuns, want)
	}
}

func recordParallelChildRun(t *testing.T, runs map[string]struct{}, event gopact.Event, parentRunID, sessionID string) {
	t.Helper()
	if event.ParentRunID != parentRunID || event.Type != workflow.EventWorkflowStarted {
		return
	}
	if event.SessionID != sessionID {
		t.Fatalf("child event = %+v, want inherited session %q", event, sessionID)
	}
	runs[event.RunID] = struct{}{}
}

func newProviderWorkflowAgent(t *testing.T, identity agent.Identity, model gopact.Model) *agent.WorkflowAgent {
	t.Helper()
	wf := workflow.New[agent.Request, agent.Response](identity.Name, workflow.WithTopologyVersion(identity.Version))
	ask := wf.Node("model", func(ctx context.Context, request agent.Request) (agent.Response, error) {
		response, err := model.Invoke(ctx, model.NewRequest(request.Messages...))
		if err != nil {
			return agent.Response{}, err
		}
		return agent.Response{Message: gopact.Message{
			Role: "assistant", Parts: []gopact.MessagePart{{Type: "text", Text: responseText(response)}},
		}}, nil
	})
	wf.Entry(ask)
	wf.Exit(ask)
	target, err := agent.NewWorkflowAgent(identity, wf)
	if err != nil {
		t.Fatal(err)
	}
	return target
}

func assertRouterFacts(t *testing.T, events []gopact.Event, runID, marker, selected string) {
	t.Helper()
	var nodes []string
	childRuns := map[string]struct{}{}
	for _, event := range events {
		if event.ParentRunID == runID && event.Type == workflow.EventWorkflowStarted {
			childRuns[event.RunID] = struct{}{}
		}
		if event.RunID != runID || event.Type != workflow.EventNodeCompleted {
			continue
		}
		nodes = append(nodes, event.NodeID)
		assertRouterNodeFact(t, event, marker, selected)
	}
	if !reflect.DeepEqual(nodes, []string{"select", "child." + selected}) || len(childRuns) != 1 {
		t.Fatalf("nodes/child runs = %v/%v, want selected route and one child Run", nodes, childRuns)
	}
}

func assertRouterNodeFact(t *testing.T, event gopact.Event, marker, selected string) {
	t.Helper()
	if event.NodeID == "select" {
		if !strings.Contains(string(event.Payload), selected) {
			t.Fatalf("selector facts = %s, want selected child %q", event.Payload, selected)
		}
		return
	}
	var facts workflow.NodeEventPayload
	if err := json.Unmarshal(event.Payload, &facts); err != nil {
		t.Fatal(err)
	}
	assertSequentialRequestFact(t, event.NodeID, "effective input", marker, facts.EffectiveInput)
}

func assertSequentialFacts(t *testing.T, events []gopact.Event, runID, marker string, nodeNames []string) {
	t.Helper()
	var completed []string
	childRuns := map[string]struct{}{}
	var sessionID string
	for _, event := range events {
		if event.RunID == runID {
			sessionID = event.SessionID
			break
		}
	}
	if sessionID == "" {
		t.Fatalf("events = %+v, want root session", events)
	}
	for _, event := range events {
		if event.ParentRunID != runID || event.Type != workflow.EventWorkflowStarted {
			continue
		}
		if event.SessionID != sessionID {
			t.Fatalf("child event = %+v, want inherited session %q", event, sessionID)
		}
		childRuns[event.RunID] = struct{}{}
	}
	for _, event := range events {
		if event.RunID != runID || event.Type != workflow.EventNodeCompleted {
			continue
		}
		completed = append(completed, event.NodeID)
		var facts workflow.NodeEventPayload
		if err := json.Unmarshal(event.Payload, &facts); err != nil {
			t.Fatalf("decode %s facts: %v", event.NodeID, err)
		}
		for name, value := range map[string]*workflow.NodeValue{
			"input": &facts.Input, "effective input": facts.EffectiveInput,
		} {
			assertSequentialRequestFact(t, event.NodeID, name, marker, value)
		}
		if facts.Output == nil {
			t.Fatalf("%s output is missing", event.NodeID)
		}
		var response agent.Response
		if err := json.Unmarshal(facts.Output.JSON, &response); err != nil || len(response.Message.Parts) != 1 ||
			!strings.Contains(response.Message.Parts[0].Text, marker) {
			t.Fatalf("%s output = %+v, %v, want marker %q", event.NodeID, response, err, marker)
		}
	}
	if !reflect.DeepEqual(completed, nodeNames) || len(childRuns) != len(nodeNames) {
		t.Fatalf("completed/child runs = %v/%v, want nodes %v and one child Run each", completed, childRuns, nodeNames)
	}
}

func assertSequentialRequestFact(t *testing.T, nodeID, name, marker string, value *workflow.NodeValue) {
	t.Helper()
	if value == nil {
		t.Fatalf("%s %s is missing", nodeID, name)
	}
	var request agent.Request
	if err := json.Unmarshal(value.JSON, &request); err != nil || len(request.Messages) != 1 ||
		!strings.Contains(request.Messages[0].Parts[0].Text, marker) {
		t.Fatalf("%s %s = %+v, %v, want marker %q", nodeID, name, request, err, marker)
	}
}

type whiteboxInput struct {
	Prompt string `json:"prompt"`
}

type whiteboxOutput struct {
	Text string `json:"text"`
}

type whiteboxResult struct {
	Text    string
	Context whiteboxContext
}

type whiteboxContext struct {
	InitialPrompt string   `json:"initial_prompt"`
	BodyInput     string   `json:"body_input"`
	BodyOutput    string   `json:"body_output"`
	Stages        []string `json:"stages"`
}

type whiteboxProbe struct {
	workflowHookInput  whiteboxInput
	workflowHookOutput whiteboxInput
	nodeHookInput      whiteboxInput
	nodeHookOutput     whiteboxInput
	middlewareInput    whiteboxInput
	bodyInput          whiteboxInput
	middlewareOutput   whiteboxOutput
	nodeHookOutputSeen whiteboxOutput
	auditContext       whiteboxContext
}

type whiteboxPlugin struct {
	state *workflow.Context[whiteboxContext]
	probe *whiteboxProbe
}

func (whiteboxPlugin) Name() string { return "whitebox-middleware" }

func (plugin whiteboxPlugin) Setup(_ context.Context, registry *workflow.Registry) error {
	return registry.RegisterNodeMiddleware("whitebox-model-input", func(ctx *workflow.NodeContext[whiteboxInput, whiteboxOutput], next workflow.NodeNext[whiteboxInput, whiteboxOutput]) error {
		plugin.probe.middlewareInput = ctx.Input
		state, err := plugin.state.Get(ctx.Context())
		if err != nil {
			return err
		}
		state.Stages = append(state.Stages, "middleware.before")
		if err := plugin.state.Set(ctx.Context(), state); err != nil {
			return err
		}
		ctx.Input.Prompt = strings.TrimPrefix(ctx.Input.Prompt, "before:")
		ctx.Input.Prompt = "Reply with exactly: " + ctx.Input.Prompt
		if err := next(); err != nil {
			return err
		}
		plugin.probe.middlewareOutput = ctx.Output
		state, err = plugin.state.Get(ctx.Context())
		if err != nil {
			return err
		}
		state.Stages = append(state.Stages, "middleware.after")
		return plugin.state.Set(ctx.Context(), state)
	})
}

func TestIntegrationWorkflowHooksMiddlewareContextAndFacts(t *testing.T) {
	for _, tt := range providerCases() {
		t.Run(tt.name, func(t *testing.T) {
			marker := "gopact-whitebox-" + tt.name
			probe := &whiteboxProbe{}
			plugin := &whiteboxPlugin{probe: probe}
			wf := workflow.New[whiteboxInput, whiteboxResult](
				"whitebox-"+tt.name,
				workflow.WithPlugins(plugin),
			)
			state := wf.Context(func(input whiteboxInput) whiteboxContext {
				return whiteboxContext{InitialPrompt: input.Prompt, Stages: []string{"context.init"}}
			})
			plugin.state = state
			wf.BeforeWorkflow(workflow.Hook("normalize-workflow-input", func(ctx *workflow.WorkflowContext[whiteboxInput, whiteboxResult]) error {
				probe.workflowHookInput = ctx.Input
				ctx.Input.Prompt = "hook:" + ctx.Input.Prompt
				probe.workflowHookOutput = ctx.Input
				return nil
			}))
			model := tt.new(t)
			task := wf.Node("ask-model", func(ctx context.Context, input whiteboxInput) (whiteboxOutput, error) {
				probe.bodyInput = input
				current, err := state.Get(ctx)
				if err != nil {
					return whiteboxOutput{}, err
				}
				current.BodyInput = input.Prompt
				current.Stages = append(current.Stages, "node.body")
				response, err := model.Invoke(ctx, model.NewRequest(gopact.UserMessage(input.Prompt)))
				if err != nil {
					return whiteboxOutput{}, err
				}
				output := whiteboxOutput{Text: responseText(response)}
				current.BodyOutput = output.Text
				if err := state.Set(ctx, current); err != nil {
					return whiteboxOutput{}, err
				}
				return output, nil
			})
			task.Before(workflow.Hook("rewrite-node-input", func(ctx *workflow.NodeContext[whiteboxInput, whiteboxOutput]) error {
				probe.nodeHookInput = ctx.Input
				ctx.Input.Prompt = "before:" + strings.TrimPrefix(ctx.Input.Prompt, "hook:")
				probe.nodeHookOutput = ctx.Input
				current, err := state.Get(ctx.Context())
				if err != nil {
					return err
				}
				current.Stages = append(current.Stages, "node.before")
				return state.Set(ctx.Context(), current)
			}))
			task.After(workflow.Hook("observe-node-output", func(ctx *workflow.NodeContext[whiteboxInput, whiteboxOutput]) error {
				probe.nodeHookOutputSeen = ctx.Output
				current, err := state.Get(ctx.Context())
				if err != nil {
					return err
				}
				current.Stages = append(current.Stages, "node.after")
				return state.Set(ctx.Context(), current)
			}))
			audit := wf.Node("audit-context", func(ctx context.Context, output whiteboxOutput) (whiteboxResult, error) {
				current, err := state.Get(ctx)
				if err != nil {
					return whiteboxResult{}, err
				}
				probe.auditContext = current
				return whiteboxResult{Text: output.Text, Context: current}, nil
			})
			wf.Entry(task)
			wf.Edge(task, audit)
			wf.Exit(audit)

			var nodeEvents []gopact.Event
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			runID := "whitebox-" + tt.name + "-run"
			result, err := wf.Invoke(ctx, whiteboxInput{Prompt: marker}, gopact.WithRunID(runID), gopact.WithStrictEventHandler(func(_ context.Context, event gopact.Event) error {
				if event.Type == workflow.EventNodeStarted || event.Type == workflow.EventNodeCompleted {
					nodeEvents = append(nodeEvents, event)
				}
				return nil
			}))
			if err != nil {
				t.Fatalf("Invoke() error = %v", err)
			}
			if !strings.Contains(result.Text, marker) {
				t.Fatalf("output = %q, want marker %q", result.Text, marker)
			}
			wantStages := []string{"context.init", "node.before", "middleware.before", "node.body", "middleware.after", "node.after"}
			if !reflect.DeepEqual(result.Context.Stages, wantStages) || !reflect.DeepEqual(probe.auditContext, result.Context) {
				t.Fatalf("context = %+v, audit = %+v, want stages %v", result.Context, probe.auditContext, wantStages)
			}
			if probe.workflowHookInput.Prompt != marker || probe.workflowHookOutput.Prompt != "hook:"+marker ||
				probe.nodeHookInput.Prompt != "hook:"+marker || probe.nodeHookOutput.Prompt != "before:"+marker ||
				probe.middlewareInput.Prompt != "before:"+marker || probe.bodyInput.Prompt != "Reply with exactly: "+marker {
				t.Fatalf("input flow = %+v, want workflow hook -> node hook -> middleware -> body", probe)
			}
			if probe.middlewareOutput.Text == "" || probe.nodeHookOutputSeen.Text != probe.middlewareOutput.Text || result.Context.BodyOutput != result.Text {
				t.Fatalf("output flow = %+v, result = %+v", probe, result)
			}
			assertWhiteboxFacts(t, nodeEvents, marker, result)
			snapshot, err := wf.Snapshot(context.Background(), workflow.SnapshotRequest{RunID: runID})
			if err != nil || len(snapshot.Timeline) == 0 {
				t.Fatalf("Snapshot() = %+v, %v, want persisted timeline", snapshot, err)
			}
			retried, err := wf.Retry(ctx, workflow.RetryRequest{
				RunID: runID, NodeID: "ask-model", NodeExecutionVersion: 1,
			})
			if err != nil || !strings.Contains(retried.Text, marker) {
				t.Fatalf("Retry() = %+v, %v, want provider marker %q", retried, err, marker)
			}
			retriedSnapshot, err := wf.Snapshot(context.Background(), workflow.SnapshotRequest{RunID: runID})
			if err != nil {
				t.Fatalf("retried Snapshot() error = %v", err)
			}
			assertRetryTimeline(t, retriedSnapshot.Timeline, runID)
			sourceRevision := nodeRevision(t, retriedSnapshot.Timeline, "ask-model", workflow.EventNodeCompleted, 1)
			patched := whiteboxContext{
				InitialPrompt: "manual", BodyInput: "manual", BodyOutput: "manual-output",
				Stages: []string{"manual.patch"},
			}
			jumped, err := wf.JumpTo(ctx, audit, workflow.JumpRequest{
				RunID: runID, FromRevisionID: sourceRevision, ContextPatch: patched,
			}, whiteboxOutput{Text: "manual-output"})
			if err != nil || jumped.Text != "manual-output" || !reflect.DeepEqual(jumped.Context, patched) {
				t.Fatalf("JumpTo() = %+v, %v, want patched audit result", jumped, err)
			}
			jumpedSnapshot, err := wf.Snapshot(context.Background(), workflow.SnapshotRequest{RunID: runID})
			if err != nil {
				t.Fatalf("jumped Snapshot() error = %v", err)
			}
			assertJumpTimeline(t, jumpedSnapshot.Timeline, sourceRevision)
		})
	}
}

func nodeRevision(t *testing.T, timeline []runlog.Record, nodeID, eventType string, version int64) string {
	t.Helper()
	for _, record := range timeline {
		if record.NodeID == nodeID && record.EventType == eventType && record.NodeExecutionVersion == version {
			return record.RevisionID
		}
	}
	t.Fatalf("revision for %s/%s/v%d not found", nodeID, eventType, version)
	return ""
}

func assertJumpTimeline(t *testing.T, timeline []runlog.Record, sourceRev string) {
	t.Helper()
	var auditStarts []runlog.Record
	for _, record := range timeline {
		if record.NodeID == "audit-context" && record.EventType == workflow.EventNodeStarted {
			auditStarts = append(auditStarts, record)
		}
	}
	if len(auditStarts) != 3 {
		t.Fatalf("audit starts = %+v, want 3", auditStarts)
	}
	last := auditStarts[len(auditStarts)-1]
	if last.NodeExecutionVersion != 3 || last.ExecutionEpoch != 3 ||
		last.SourceRevisionID != sourceRev || last.Origin != "external_jump" ||
		last.ActivationID == auditStarts[len(auditStarts)-2].ActivationID {
		t.Fatalf("audit starts = %+v, want new activation in jump epoch 3", auditStarts)
	}
}

func TestIntegrationWorkflowTerminateAfterRealProvider(t *testing.T) {
	for _, tt := range providerCases() {
		t.Run(tt.name, func(t *testing.T) {
			marker := "gopact-cancel-" + tt.name
			model := tt.new(t)
			wf := workflow.New[string, string]("cancel-" + tt.name)
			ask := wf.Node("ask-model", func(ctx context.Context, prompt string) (string, error) {
				response, err := model.Invoke(ctx, model.NewRequest(gopact.UserMessage("Reply with exactly: "+prompt)))
				return responseText(response), err
			})
			wait := wf.Node("wait-for-control", func(ctx context.Context, input string) (string, error) {
				<-ctx.Done()
				return "", ctx.Err()
			})
			wf.Entry(ask)
			wf.Edge(ask, wait)
			wf.Exit(wait)

			runID := "cancel-" + tt.name + "-run"
			started := make(chan struct{})
			done := make(chan error, 1)
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			go func() {
				_, err := wf.Invoke(ctx, marker, gopact.WithRunID(runID), gopact.WithEventHandler(func(_ context.Context, event gopact.Event) error {
					if event.Type == workflow.EventNodeStarted && event.NodeID == "wait-for-control" {
						close(started)
					}
					return nil
				}))
				done <- err
			}()
			select {
			case <-started:
			case <-ctx.Done():
				t.Fatalf("wait node did not start: %v", ctx.Err())
			}
			if err := wf.Terminate(runID); err != nil {
				t.Fatalf("Terminate() error = %v", err)
			}
			err := <-done
			if !errors.Is(err, context.Canceled) || !errors.Is(err, workflow.ErrRunTerminated) {
				t.Fatalf("Invoke() error = %v, want external termination", err)
			}
			snapshot, err := wf.Snapshot(context.Background(), workflow.SnapshotRequest{RunID: runID})
			if err != nil {
				t.Fatalf("Snapshot() error = %v", err)
			}
			assertTerminatedTimeline(t, snapshot.Timeline, marker)
		})
	}
}

func assertTerminatedTimeline(t *testing.T, timeline []runlog.Record, marker string) {
	t.Helper()
	providerCompleted := false
	terminated := false
	for _, record := range timeline {
		if record.NodeID == "ask-model" && record.EventType == workflow.EventNodeCompleted &&
			strings.Contains(string(record.Payload), marker) {
			providerCompleted = true
		}
		if record.EventType == workflow.EventWorkflowTerminated && record.Origin == "external_terminate" {
			terminated = true
		}
	}
	if !providerCompleted || !terminated {
		t.Fatalf("timeline missing provider completion or external terminate: %+v", timeline)
	}
}

func assertRetryTimeline(t *testing.T, timeline []runlog.Record, runID string) {
	t.Helper()
	var askStarts []runlog.Record
	for _, record := range timeline {
		if record.RunID == runID && record.NodeID == "ask-model" && record.EventType == workflow.EventNodeStarted {
			askStarts = append(askStarts, record)
		}
	}
	if len(askStarts) != 2 || askStarts[0].NodeExecutionVersion != 1 || askStarts[1].NodeExecutionVersion != 2 ||
		askStarts[0].ActivationID != askStarts[1].ActivationID || askStarts[0].AttemptID == askStarts[1].AttemptID ||
		askStarts[1].ExecutionEpoch != 2 || askStarts[1].SourceRevisionID == "" || askStarts[1].Origin != "external_retry" {
		t.Fatalf("ask starts = %+v, want same-run retry attempt in epoch 2", askStarts)
	}
}

func assertWhiteboxFacts(t *testing.T, events []gopact.Event, marker string, result whiteboxResult) {
	t.Helper()
	if len(events) != 4 {
		t.Fatalf("node events = %d, want 4", len(events))
	}
	var askStarted, askCompleted, auditStarted workflow.NodeEventPayload
	for _, event := range events {
		var payload workflow.NodeEventPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatalf("decode %s payload: %v", event.Type, err)
		}
		if payload.NodeName == "ask-model" && event.Type == workflow.EventNodeStarted {
			askStarted = payload
		}
		if payload.NodeName == "ask-model" && event.Type == workflow.EventNodeCompleted {
			askCompleted = payload
		}
		if payload.NodeName == "audit-context" && event.Type == workflow.EventNodeStarted {
			auditStarted = payload
		}
	}
	if askStarted.ContextRevision != 1 || string(askStarted.Input.JSON) != `{"prompt":"hook:`+marker+`"}` {
		t.Fatalf("ask started = %+v, want revision 1 and workflow-hook input", askStarted)
	}
	if askCompleted.EffectiveInput == nil ||
		string(askCompleted.EffectiveInput.JSON) != `{"prompt":"Reply with exactly: `+marker+`"}` ||
		askCompleted.Output == nil || !strings.Contains(string(askCompleted.Output.JSON), marker) {
		t.Fatalf("ask completed = %+v, want actual body input and provider output", askCompleted)
	}
	var auditContext whiteboxContext
	if err := json.Unmarshal(auditStarted.WorkflowContext.JSON, &auditContext); err != nil {
		t.Fatalf("decode audit context: %v", err)
	}
	if auditStarted.ContextRevision != 2 || !reflect.DeepEqual(auditContext, result.Context) {
		t.Fatalf("audit started = %+v, want committed provider output context", auditStarted)
	}
}

type providerCase struct {
	name string
	new  func(*testing.T) gopact.Model
}

func providerCases() []providerCase {
	return []providerCase{
		{name: "agnes", new: newAgnesModel},
		{name: "glm", new: newGLMModel},
	}
}

func newAgnesModel(t *testing.T) gopact.Model {
	t.Helper()
	key := os.Getenv("AGNES_API_KEY")
	if key == "" {
		t.Skip("AGNES_API_KEY is not set")
	}
	model, err := agnes.New(key, agnes.WithDefaultRequest(gopact.ModelRequest{
		Model:           firstNonEmpty(os.Getenv("AGNES_MODEL"), agnes.DefaultModel),
		Temperature:     zeroTemperature(),
		MaxOutputTokens: 128,
	}))
	if err != nil {
		t.Fatalf("agnes.New() error = %v", err)
	}
	return model
}

func newGLMModel(t *testing.T) gopact.Model {
	t.Helper()
	key := os.Getenv("GLM_API_KEY")
	if key == "" {
		t.Skip("GLM_API_KEY is not set")
	}
	model, err := glm.New(key, glm.WithDefaultRequest(gopact.ModelRequest{
		Model:           firstNonEmpty(os.Getenv("GLM_MODEL"), glm.DefaultModel),
		Temperature:     zeroTemperature(),
		MaxOutputTokens: 1024,
	}))
	if err != nil {
		t.Fatalf("glm.New() error = %v", err)
	}
	return model
}

func responseText(resp gopact.ModelResponse) string {
	if len(resp.Message.Parts) == 0 {
		return ""
	}
	return resp.Message.Parts[0].Text
}

func containsEvent(events []string, want string) bool {
	for _, event := range events {
		if event == want {
			return true
		}
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func zeroTemperature() *float64 {
	value := 0.0
	return &value
}
