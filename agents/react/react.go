// Package react provides a minimal ReAct-style agent template.
package react

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"strings"
	"time"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact/graph"
	"github.com/gopact-ai/gopact/memory"
	"github.com/gopact-ai/gopact/tools"
)

const (
	nodeCallModel = "call_model"
	nodeCallTool  = "call_tool"
	nodeVerify    = "verify"

	defaultMaxIterations = 8
	defaultMemoryLimit   = 4
	memoryMessageName    = "gopact.memory"

	memoryMetadataExtractMode = "memory_extract_mode"
	memoryMetadataWriteMode   = "memory_write_mode"
	memoryMetadataPending     = "memory_pending"
)

// MemoryExtractMode controls when extracted memories are produced.
type MemoryExtractMode string

const (
	// MemoryExtractSync calls the extractor before the model step completes.
	MemoryExtractSync MemoryExtractMode = "sync"

	// MemoryExtractDeferred records a memory_extract effect for host-managed background extraction.
	MemoryExtractDeferred MemoryExtractMode = "deferred"
)

// MemoryWriteMode controls how extracted memories are committed.
type MemoryWriteMode string

const (
	// MemoryWriteSync writes extracted memories before the model step completes.
	MemoryWriteSync MemoryWriteMode = "sync"

	// MemoryWriteDeferred records replayable memory_put effects without writing the store.
	MemoryWriteDeferred MemoryWriteMode = "deferred"
)

var (
	ErrModelRequired            = errors.New("react: model is required")
	ErrInvalidInput             = errors.New("react: invalid input")
	ErrToolRegistryMissing      = errors.New("react: tool registry is required")
	ErrMaxIterations            = errors.New("react: max iterations exceeded")
	ErrMemoryStoreRequired      = errors.New("react: memory store is required")
	ErrResumeUnsupported        = errors.New("react: resume boundary is unsupported")
	ErrModelMessageMissing      = errors.New("react: model response message is missing")
	ErrCheckpointRequired       = errors.New("react: checkpoint store is required")
	ErrCheckpointThreadID       = errors.New("react: checkpoint loader requires thread id")
	ErrArtifactVerifierRequired = errors.New("react: artifact verifier is required")
	ErrVerifierRequired         = errors.New("react: verifier is required")
	ErrVerificationFailed       = errors.New("react: verification failed")
	ErrMemoryExtractMode        = errors.New("react: memory extract mode is invalid")
	ErrMemoryWriteMode          = errors.New("react: memory write mode is invalid")
)

// State is the portable input state for the ReAct template.
type State struct {
	Messages []gopact.Message
}

// Agent is a minimal ReAct-style runnable.
type Agent struct {
	model             gopact.ChatModel
	registry          *tools.Registry
	maxIterations     int
	routeHint         string
	memoryStore       memory.Store
	memoryQuery       MemoryQueryFunc
	memoryExtract     MemoryExtractFunc
	memoryMerge       MemoryMergeFunc
	memoryExtractMode MemoryExtractMode
	memoryWriteMode   MemoryWriteMode
	memoryLimit       int
	checkpointer      graph.Checkpointer[State]
	checkpointLoader  graph.CheckpointLoader[State]
	artifactVerifier  graph.ArtifactVerifier
	verifier          VerificationFunc
}

// Option configures an Agent.
type Option func(*Agent) error

// MemoryQueryFunc creates a memory search query for one model step.
type MemoryQueryFunc func(ctx context.Context, state State, ids gopact.RuntimeIDs) (memory.Query, bool, error)

// MemoryExtractFunc extracts long-term memories from the latest model state.
type MemoryExtractFunc func(ctx context.Context, state State, ids gopact.RuntimeIDs) ([]memory.Memory, error)

// MemoryMergeRequest is the input for user-defined memory compaction or merge logic.
type MemoryMergeRequest struct {
	State    State
	IDs      gopact.RuntimeIDs
	Memories []memory.Memory
}

// MemoryMergeFunc rewrites extracted memories before they are written or recorded as effects.
type MemoryMergeFunc func(ctx context.Context, request MemoryMergeRequest) ([]memory.Memory, error)

// VerificationFunc verifies a candidate completed run export and records evidence.
type VerificationFunc func(ctx context.Context, export gopact.RunExport, recorder *gopact.VerificationRecorder) error

// MemoryOption configures optional memory recall for an Agent.
type MemoryOption func(*memoryConfig) error

type memoryConfig struct {
	query       MemoryQueryFunc
	extract     MemoryExtractFunc
	merge       MemoryMergeFunc
	extractMode MemoryExtractMode
	writeMode   MemoryWriteMode
	limit       int
}

type resumeAction int

const (
	resumeActionCallTools resumeAction = iota + 1
	resumeActionCallModel
	resumeActionComplete
)

type resumePlan struct {
	state    State
	calls    []gopact.ToolCall
	step     int
	ids      gopact.RuntimeIDs
	snapshot gopact.StepSnapshot
	action   resumeAction
	resume   *gopact.ResumeRequest
}

// New creates a ReAct-style agent template.
func New(model gopact.ChatModel, registry *tools.Registry, opts ...Option) (*Agent, error) {
	if model == nil {
		return nil, ErrModelRequired
	}
	agent := &Agent{
		model:         model,
		registry:      registry,
		maxIterations: defaultMaxIterations,
	}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if err := opt(agent); err != nil {
			return nil, err
		}
	}
	return agent, nil
}

// WithMemory enables memory recall before each model call.
func WithMemory(store memory.Store, opts ...MemoryOption) Option {
	return func(agent *Agent) error {
		if store == nil {
			return ErrMemoryStoreRequired
		}
		cfg := memoryConfig{
			query:       defaultMemoryQuery,
			extractMode: MemoryExtractSync,
			writeMode:   MemoryWriteSync,
			limit:       defaultMemoryLimit,
		}
		for _, opt := range opts {
			if opt == nil {
				continue
			}
			if err := opt(&cfg); err != nil {
				return err
			}
		}
		agent.memoryStore = store
		agent.memoryQuery = cfg.query
		agent.memoryExtract = cfg.extract
		agent.memoryMerge = cfg.merge
		agent.memoryExtractMode = cfg.extractMode
		agent.memoryWriteMode = cfg.writeMode
		agent.memoryLimit = cfg.limit
		return nil
	}
}

// WithMemoryQuery sets the query strategy used by memory recall.
func WithMemoryQuery(query MemoryQueryFunc) MemoryOption {
	return func(cfg *memoryConfig) error {
		if query == nil {
			return errors.New("react: memory query is required")
		}
		cfg.query = query
		return nil
	}
}

// WithMemoryExtractor enables explicit memory write extraction after model calls.
func WithMemoryExtractor(extract MemoryExtractFunc) MemoryOption {
	return func(cfg *memoryConfig) error {
		if extract == nil {
			return errors.New("react: memory extractor is required")
		}
		cfg.extract = extract
		return nil
	}
}

// WithMemoryMerge sets user-defined compaction or merge logic for extracted memories.
func WithMemoryMerge(merge MemoryMergeFunc) MemoryOption {
	return func(cfg *memoryConfig) error {
		if merge == nil {
			return errors.New("react: memory merge is required")
		}
		cfg.merge = merge
		return nil
	}
}

// WithMemoryExtractMode controls whether extraction runs inline or is recorded for a host worker.
func WithMemoryExtractMode(mode MemoryExtractMode) MemoryOption {
	return func(cfg *memoryConfig) error {
		if !mode.valid() {
			return fmt.Errorf("%w: %q", ErrMemoryExtractMode, mode)
		}
		cfg.extractMode = mode
		return nil
	}
}

// WithMemoryWriteMode controls whether extracted memories are written synchronously or recorded as deferred effects.
func WithMemoryWriteMode(mode MemoryWriteMode) MemoryOption {
	return func(cfg *memoryConfig) error {
		if !mode.valid() {
			return fmt.Errorf("%w: %q", ErrMemoryWriteMode, mode)
		}
		cfg.writeMode = mode
		return nil
	}
}

// WithMemoryLimit sets the default recall limit when the query omits one.
func WithMemoryLimit(limit int) MemoryOption {
	return func(cfg *memoryConfig) error {
		if limit <= 0 {
			return errors.New("react: memory limit must be positive")
		}
		cfg.limit = limit
		return nil
	}
}

func (m MemoryExtractMode) valid() bool {
	switch m {
	case MemoryExtractSync, MemoryExtractDeferred:
		return true
	default:
		return false
	}
}

func (m MemoryWriteMode) valid() bool {
	switch m {
	case MemoryWriteSync, MemoryWriteDeferred:
		return true
	default:
		return false
	}
}

// WithMaxIterations limits model/tool loop iterations before failing the run.
func WithMaxIterations(maxIterations int) Option {
	return func(agent *Agent) error {
		if maxIterations <= 0 {
			return errors.New("react: max iterations must be positive")
		}
		agent.maxIterations = maxIterations
		return nil
	}
}

// WithCheckpointer writes ReAct step checkpoints after stable node boundaries.
func WithCheckpointer(checkpointer graph.Checkpointer[State]) Option {
	return func(agent *Agent) error {
		if checkpointer == nil {
			return ErrCheckpointRequired
		}
		agent.checkpointer = checkpointer
		return nil
	}
}

// WithCheckpointLoader resumes ReAct runs from the latest checkpoint for the run ThreadID.
func WithCheckpointLoader(loader graph.CheckpointLoader[State]) Option {
	return func(agent *Agent) error {
		if loader == nil {
			return ErrCheckpointRequired
		}
		agent.checkpointLoader = loader
		return nil
	}
}

// WithCheckpointStore writes checkpoints and resumes from the latest checkpoint for the run ThreadID.
func WithCheckpointStore(store graph.CheckpointStore[State]) Option {
	return func(agent *Agent) error {
		if store == nil {
			return ErrCheckpointRequired
		}
		agent.checkpointer = store
		agent.checkpointLoader = store
		return nil
	}
}

// WithArtifactVerifier verifies artifact refs before a step export or checkpoint resume is trusted.
func WithArtifactVerifier(verifier graph.ArtifactVerifier) Option {
	return func(agent *Agent) error {
		if verifier == nil {
			return ErrArtifactVerifierRequired
		}
		agent.artifactVerifier = verifier
		return nil
	}
}

// WithVerifier adds a final verification node before a completed ReAct run is committed.
func WithVerifier(verifier VerificationFunc) Option {
	return func(agent *Agent) error {
		if verifier == nil {
			return ErrVerifierRequired
		}
		agent.verifier = verifier
		return nil
	}
}

// WithRouteHint sets the model route hint on each model request.
func WithRouteHint(routeHint string) Option {
	return func(agent *Agent) error {
		agent.routeHint = routeHint
		return nil
	}
}

// Run implements gopact.Runnable.
func (a *Agent) Run(ctx context.Context, input any, opts ...gopact.RunOption) iter.Seq2[gopact.Event, error] {
	return func(yield func(gopact.Event, error) bool) {
		if ctx == nil {
			ctx = context.TODO()
		}
		var events []gopact.Event
		emit := func(event gopact.Event, err error) bool {
			events = append(events, event)
			return yield(event, err)
		}
		cfg := gopact.ResolveRunOptions(opts...)
		ids := cfg.IDs
		state, err := inputState(input)
		if err != nil {
			emit(reactEvent(gopact.EventRunFailed, ids, "", 0, nil, err), err)
			return
		}
		if err := ctx.Err(); err != nil {
			emit(reactEvent(gopact.EventRunCanceled, ids, "", 0, nil, err), err)
			return
		}
		initialInput := copyState(state)

		var resume *resumePlan
		var loadedCheckpoint *gopact.StepSnapshot
		if cfg.StepExport != nil {
			resume, err = a.resumeFromStepExport(*cfg.StepExport, cfg.ResumeRequest)
			if err != nil {
				var interruptErr *gopact.InterruptError
				if errors.As(err, &interruptErr) && cfg.StepExport.Step.Phase == gopact.StepInterrupted {
					snapshot := cfg.StepExport.Step
					eventIDs := ids.WithDefaults(resumeRunIDs(snapshot.IDs))
					emit(reactEvent(gopact.EventRunInterrupted, eventIDs, snapshot.Node, snapshot.Step, &snapshot, interruptErr), interruptErr)
					return
				}
				emit(reactEvent(gopact.EventRunFailed, ids, "", 0, nil, err), err)
				return
			}
			state = resume.state
			ids = ids.WithDefaults(resume.ids)
		} else if a.checkpointLoader != nil {
			if ids.ThreadID == "" {
				emit(reactEvent(gopact.EventRunFailed, ids, "", 0, nil, ErrCheckpointThreadID), ErrCheckpointThreadID)
				return
			}
			checkpoint, ok, err := a.checkpointLoader.Latest(ctx, ids.ThreadID)
			if err != nil {
				wrapped := fmt.Errorf("react: load checkpoint for thread %q: %w", ids.ThreadID, err)
				emit(reactEvent(gopact.EventRunFailed, ids, "", 0, nil, wrapped), wrapped)
				return
			}
			if ok {
				resume, err = a.resumeFromCheckpoint(checkpoint, cfg.ResumeRequest)
				if err != nil {
					var interruptErr *gopact.InterruptError
					if errors.As(err, &interruptErr) && checkpointPhase(checkpoint) == gopact.StepInterrupted {
						snapshot := checkpointStepSnapshot(checkpoint, ids.WithDefaults(checkpointIDs(checkpoint)))
						emit(reactEvent(gopact.EventRunInterrupted, snapshot.IDs, snapshot.Node, snapshot.Step, &snapshot, interruptErr), interruptErr)
						return
					}
					emit(reactEvent(gopact.EventRunFailed, ids, "", 0, nil, err), err)
					return
				}
				state = resume.state
				ids = ids.WithDefaults(resume.ids)
				snapshot := checkpointStepSnapshot(checkpoint, ids)
				loadedCheckpoint = &snapshot
			}
		}

		if !emit(reactEvent(gopact.EventRunStarted, ids, "", 0, nil, nil), nil) {
			return
		}

		step := 0
		nextModelStartEvent := gopact.EventNodeStarted
		if resume != nil {
			step = resume.step
			var imported gopact.StepSnapshot
			if loadedCheckpoint != nil {
				imported = copyStepSnapshot(*loadedCheckpoint)
				if err := verifySnapshotArtifacts(ctx, a.artifactVerifier, imported); err != nil {
					wrapped := fmt.Errorf("react: verify checkpoint artifacts: %w", err)
					emit(reactEvent(gopact.EventRunFailed, ids, imported.Node, imported.Step, nil, wrapped), wrapped)
					return
				}
				checkpointEvent := reactEvent(gopact.EventCheckpointLoaded, ids, imported.Node, imported.Step, &imported, nil)
				checkpointEvent.Metadata = copyAnyMap(imported.Metadata)
				if !emit(checkpointEvent, nil) {
					return
				}
			} else {
				imported = copyStepSnapshot(resume.snapshot)
				if err := verifySnapshotArtifacts(ctx, a.artifactVerifier, imported); err != nil {
					wrapped := fmt.Errorf("react: verify imported step artifacts: %w", err)
					emit(reactEvent(gopact.EventRunFailed, ids, imported.Node, imported.Step, nil, wrapped), wrapped)
					return
				}
				if !emit(reactEvent(gopact.EventStepImported, ids, imported.Node, imported.Step, &imported, nil), nil) {
					return
				}
			}
			if resume.resume != nil {
				resumeEvent := reactEvent(gopact.EventResumeReceived, ids, imported.Node, imported.Step, &imported, nil)
				resumeEvent.Metadata = resumeEventMetadata(*resume.resume)
				if !emit(resumeEvent, nil) {
					return
				}
			}
			switch resume.action {
			case resumeActionCallTools:
				step++
				if ok := a.callTools(ctx, emit, ids, &state, resume.calls, step, gopact.EventNodeResumed, resume.resume); !ok {
					return
				}
			case resumeActionCallModel:
				nextModelStartEvent = gopact.EventNodeResumed
			case resumeActionComplete:
				a.completeRun(ctx, emit, ids, state, step+1, events, initialInput, resume)
				return
			default:
				err := fmt.Errorf("%w: resume action %d", ErrResumeUnsupported, resume.action)
				emit(reactEvent(gopact.EventRunFailed, ids, "", 0, nil, err), err)
				return
			}
		}

		for iteration := 0; iteration < a.maxIterations; iteration++ {
			step++
			startEvent := nextModelStartEvent
			nextModelStartEvent = gopact.EventNodeStarted
			nextState, message, ok := a.callModel(ctx, emit, ids, state, step, startEvent)
			if !ok {
				return
			}
			state = nextState
			if len(message.ToolCalls) == 0 {
				a.completeRun(ctx, emit, ids, state, step+1, events, initialInput, resume)
				return
			}

			step++
			ok = a.callTools(ctx, emit, ids, &state, message.ToolCalls, step, gopact.EventNodeStarted, nil)
			if !ok {
				return
			}
		}

		err = fmt.Errorf("%w: %d", ErrMaxIterations, a.maxIterations)
		emit(reactEvent(gopact.EventRunFailed, ids, "", 0, nil, err), err)
	}
}

func (a *Agent) completeRun(
	ctx context.Context,
	yield func(gopact.Event, error) bool,
	ids gopact.RuntimeIDs,
	state State,
	verificationStep int,
	events []gopact.Event,
	initialInput State,
	resume *resumePlan,
) bool {
	terminal := reactEvent(gopact.EventRunCompleted, ids, "", 0, nil, nil)
	if a.verifier == nil {
		return yield(terminal, nil)
	}

	startedAt := time.Now()
	input := copyState(state)
	started := reactStepSnapshot(verificationStep, nodeVerify, ids, gopact.StepRunning, input, nil, "", startedAt, time.Time{})
	if !yield(reactEvent(gopact.EventNodeStarted, ids, nodeVerify, verificationStep, &started, nil), nil) {
		return false
	}

	export, err := verificationRunExport(events, terminal, initialInput, state, ids, resume)
	if err != nil {
		wrapped := fmt.Errorf("react: build verification export: %w", err)
		return failVerificationNode(yield, ids, verificationStep, input, gopact.VerificationReport{}, wrapped, startedAt)
	}

	recorder := gopact.NewVerificationRecorder()
	verifyErr := a.verifier(ctx, export, recorder)
	report, reportErr := recorder.Report(export)
	if reportErr != nil {
		err = fmt.Errorf("%w: build verification report: %w", ErrVerificationFailed, reportErr)
		if verifyErr != nil {
			err = errors.Join(verificationFailedError(report, verifyErr), err)
		}
		return failVerificationNode(yield, ids, verificationStep, input, report, err, startedAt)
	}
	if verifyErr != nil {
		return failVerificationNode(yield, ids, verificationStep, input, report, verificationFailedError(report, verifyErr), startedAt)
	}
	if report.Status == gopact.VerificationStatusFailed {
		return failVerificationNode(yield, ids, verificationStep, input, report, verificationFailedError(report, nil), startedAt)
	}

	completed := reactStepSnapshot(verificationStep, nodeVerify, ids, gopact.StepCompleted, input, report, "", startedAt, time.Now())
	event := reactEvent(gopact.EventNodeCompleted, ids, nodeVerify, verificationStep, &completed, nil)
	event.Metadata = verificationReportMetadata(report)
	if !yield(event, nil) {
		return false
	}
	return yield(terminal, nil)
}

func verificationRunExport(events []gopact.Event, terminal gopact.Event, initialInput State, finalState State, ids gopact.RuntimeIDs, resume *resumePlan) (gopact.RunExport, error) {
	recorder := gopact.NewRunRecorder()
	for _, event := range events {
		if err := recorder.Record(event); err != nil {
			return gopact.RunExport{}, err
		}
	}
	if err := recorder.Record(terminal); err != nil {
		return gopact.RunExport{}, err
	}
	if err := recordProcessRecords(recorder, terminal, initialInput, finalState, ids, resume); err != nil {
		return gopact.RunExport{}, err
	}
	return recorder.Export()
}

func recordProcessRecords(recorder *gopact.RunRecorder, terminal gopact.Event, initialInput State, finalState State, ids gopact.RuntimeIDs, resume *resumePlan) error {
	status := taskStatusForTerminal(terminal.Type)
	if err := recorder.RecordTask(gopact.TaskRecord{
		ID:          processRecordID(ids, "task"),
		Name:        "react",
		Status:      status,
		IDs:         ids,
		Input:       copyState(initialInput),
		Output:      copyState(finalState),
		CreatedAt:   processRecordTime(terminal),
		StartedAt:   processRecordTime(terminal),
		CompletedAt: processRecordTime(terminal),
		Metadata: map[string]any{
			"template": "react",
		},
	}); err != nil {
		return err
	}

	if len(initialInput.Messages) > 0 {
		if err := recorder.RecordInput(gopact.InputRecord{
			ID:        processRecordID(ids, "input"),
			Kind:      gopact.InputUser,
			IDs:       ids,
			Source:    "react.run",
			Value:     copyState(initialInput),
			CreatedAt: processRecordTime(terminal),
			Metadata: map[string]any{
				"template": "react",
			},
		}); err != nil {
			return err
		}
	}

	if resume == nil || resume.resume == nil {
		return nil
	}
	resumeCopy := copyResumeRequest(*resume.resume)
	if err := recorder.RecordInput(gopact.InputRecord{
		ID:        processRecordID(ids, "resume:"+resumeCopy.InterruptID),
		Kind:      gopact.InputResume,
		IDs:       ids,
		Source:    "react.resume",
		Value:     resumeCopy.Payload,
		Resume:    &resumeCopy,
		CreatedAt: processRecordTime(terminal),
		Metadata: map[string]any{
			"template": "react",
		},
	}); err != nil {
		return err
	}

	if resume.snapshot.Pending == nil {
		return nil
	}
	request := *resume.snapshot.Pending
	return recorder.RecordIntervention(gopact.InterventionRecord{
		ID:         request.ID,
		Type:       request.Type,
		Status:     gopact.InterventionResolved,
		IDs:        ids,
		Request:    &request,
		Resume:     &resumeCopy,
		CreatedAt:  request.CreatedAt,
		ResolvedAt: processRecordTime(terminal),
		Metadata: map[string]any{
			"template": "react",
		},
	})
}

func taskStatusForTerminal(eventType gopact.EventType) gopact.TaskStatus {
	switch eventType {
	case gopact.EventRunCompleted:
		return gopact.TaskCompleted
	case gopact.EventRunFailed:
		return gopact.TaskFailed
	case gopact.EventRunCanceled:
		return gopact.TaskCanceled
	case gopact.EventRunInterrupted:
		return gopact.TaskInterrupted
	default:
		return gopact.TaskRunning
	}
}

func processRecordID(ids gopact.RuntimeIDs, suffix string) string {
	if ids.RunID == "" {
		return "react:" + suffix
	}
	return ids.RunID + ":" + suffix
}

func processRecordTime(event gopact.Event) time.Time {
	if !event.CreatedAt.IsZero() {
		return event.CreatedAt
	}
	return time.Now()
}

func failVerificationNode(
	yield func(gopact.Event, error) bool,
	ids gopact.RuntimeIDs,
	step int,
	input State,
	report gopact.VerificationReport,
	err error,
	startedAt time.Time,
) bool {
	failed := reactStepSnapshot(step, nodeVerify, ids, gopact.StepFailed, input, report, err.Error(), startedAt, time.Now())
	event := reactEvent(gopact.EventNodeFailed, ids, nodeVerify, step, &failed, err)
	if report.Status != "" {
		event.Metadata = verificationReportMetadata(report)
	}
	if !yield(event, nil) {
		return false
	}
	yield(reactEvent(gopact.EventRunFailed, ids, nodeVerify, step, nil, err), err)
	return false
}

func verificationFailedError(report gopact.VerificationReport, err error) error {
	if err != nil {
		return fmt.Errorf("%w: %w", ErrVerificationFailed, err)
	}
	return fmt.Errorf("%w: status %q", ErrVerificationFailed, report.Status)
}

func verificationReportMetadata(report gopact.VerificationReport) map[string]any {
	return map[string]any{
		gopact.EventMetadataVerificationReport: report,
		"verification_status":                  string(report.Status),
		"verification_passed_count":            report.PassedCount,
		"verification_failed_count":            report.FailedCount,
		"verification_skipped_count":           report.SkippedCount,
	}
}

func (a *Agent) resumeFromStepExport(export gopact.StepExport, resume *gopact.ResumeRequest) (*resumePlan, error) {
	if err := export.Validate(); err != nil {
		return nil, fmt.Errorf("react: resume step export: %w", err)
	}
	switch export.Step.Phase {
	case gopact.StepInterrupted:
		if export.Step.Node != nodeCallTool {
			return nil, fmt.Errorf("%w: phase %q node %q", ErrResumeUnsupported, export.Step.Phase, export.Step.Node)
		}
		if resume == nil {
			return nil, &gopact.InterruptError{Record: *export.Step.Pending}
		}
		if err := resume.Validate(); err != nil {
			return nil, fmt.Errorf("react: resume request: %w", err)
		}
		if resume.InterruptID != export.Step.Pending.ID {
			return nil, fmt.Errorf("react: resume interrupt id %q does not match pending interrupt %q", resume.InterruptID, export.Step.Pending.ID)
		}
		if resume.StepID != "" && resume.StepID != export.Step.ID {
			return nil, fmt.Errorf("react: resume step id %q does not match export step %q", resume.StepID, export.Step.ID)
		}
		state, ok := export.Step.Output.(State)
		if !ok {
			return nil, fmt.Errorf("react: resume state type mismatch: got %T", export.Step.Output)
		}
		calls := pendingToolCalls(state)
		if len(calls) == 0 {
			return nil, errors.New("react: resume tool calls are missing")
		}
		resumeCopy := copyResumeRequest(*resume)
		return &resumePlan{
			state:    copyState(state),
			calls:    calls,
			step:     export.Step.Step,
			ids:      resumeRunIDs(export.Step.IDs),
			snapshot: copyStepSnapshot(export.Step),
			action:   resumeActionCallTools,
			resume:   &resumeCopy,
		}, nil
	case gopact.StepCompleted:
		state, ok := export.Step.Output.(State)
		if !ok {
			return nil, fmt.Errorf("react: resume state type mismatch: got %T", export.Step.Output)
		}
		plan := &resumePlan{
			state:    copyState(state),
			step:     export.Step.Step,
			ids:      resumeRunIDs(export.Step.IDs),
			snapshot: copyStepSnapshot(export.Step),
		}
		switch export.Step.Node {
		case nodeCallModel:
			plan.calls = pendingToolCalls(state)
			if len(plan.calls) == 0 {
				plan.action = resumeActionComplete
			} else {
				plan.action = resumeActionCallTools
			}
		case nodeCallTool:
			plan.action = resumeActionCallModel
		default:
			return nil, fmt.Errorf("%w: phase %q node %q", ErrResumeUnsupported, export.Step.Phase, export.Step.Node)
		}
		return plan, nil
	default:
		return nil, fmt.Errorf("%w: phase %q node %q", ErrResumeUnsupported, export.Step.Phase, export.Step.Node)
	}
}

func (a *Agent) resumeFromCheckpoint(checkpoint graph.Checkpoint[State], resume *gopact.ResumeRequest) (*resumePlan, error) {
	if checkpoint.Step < 0 {
		return nil, fmt.Errorf("react: checkpoint step must be non-negative, got %d", checkpoint.Step)
	}
	if checkpoint.Node == "" {
		return nil, errors.New("react: checkpoint node is required")
	}
	phase := checkpointPhase(checkpoint)
	if phase == gopact.StepInterrupted && resume != nil && resume.CheckpointID != "" && checkpoint.ID != "" && resume.CheckpointID != checkpoint.ID {
		return nil, fmt.Errorf("react: resume checkpoint id %q does not match checkpoint %q", resume.CheckpointID, checkpoint.ID)
	}

	ids := checkpointIDs(checkpoint)
	snapshot := checkpointStepSnapshot(checkpoint, ids)
	resumeForStep := resume
	if resume != nil && resume.StepID != "" && resume.StepID == checkpoint.ID && checkpoint.ID != snapshot.ID {
		copied := copyResumeRequest(*resume)
		copied.StepID = snapshot.ID
		resumeForStep = &copied
	}
	return a.resumeFromStepExport(gopact.StepExport{
		Version: gopact.RunExportVersion,
		Step:    snapshot,
	}, resumeForStep)
}

func (a *Agent) callModel(ctx context.Context, yield func(gopact.Event, error) bool, ids gopact.RuntimeIDs, state State, step int, startEvent gopact.EventType) (State, gopact.Message, bool) {
	startedAt := time.Now()
	input := copyState(state)
	started := reactStepSnapshot(step, nodeCallModel, ids, gopact.StepRunning, input, nil, "", startedAt, time.Time{})
	if startEvent == "" {
		startEvent = gopact.EventNodeStarted
	}
	if !yield(reactEvent(startEvent, ids, nodeCallModel, step, &started, nil), nil) {
		return State{}, gopact.Message{}, false
	}

	modelMessages, ok, err := a.modelMessages(ctx, yield, ids, state, step)
	if !ok {
		if err == nil {
			return State{}, gopact.Message{}, false
		}
		failNode(yield, ids, nodeCallModel, step, input, copyState(state), err, startedAt)
		return State{}, gopact.Message{}, false
	}
	toolSpecs, err := a.visibleToolSpecs(ctx, ids)
	if err != nil {
		failNode(yield, ids, nodeCallModel, step, input, copyState(state), err, startedAt)
		return State{}, gopact.Message{}, false
	}
	request := gopact.ModelRequest{
		Messages:  modelMessages,
		Tools:     toolSpecs,
		RouteHint: a.routeHint,
		IDs:       ids,
	}
	var message gopact.Message
	if streamer, ok := a.model.(gopact.StreamingModel); ok {
		response, ok := streamModelResponse(ctx, yield, streamer, request, ids, step, input, copyState(state), startedAt)
		if !ok {
			return State{}, gopact.Message{}, false
		}
		message = response.Message
	} else {
		var err error
		message, err = a.model.Generate(ctx, request)
		if err != nil {
			failNode(yield, ids, nodeCallModel, step, input, copyState(state), err, startedAt)
			return State{}, gopact.Message{}, false
		}
		if !yield(reactEvent(gopact.EventModelMessage, ids, nodeCallModel, step, nil, nil, withMessage(message)), nil) {
			return State{}, gopact.Message{}, false
		}
	}
	if message.Role == "" {
		failNode(yield, ids, nodeCallModel, step, input, copyState(state), ErrModelMessageMissing, startedAt)
		return State{}, gopact.Message{}, false
	}
	nextState := copyState(state)
	nextState.Messages = append(nextState.Messages, copyMessage(message))
	memoryEffects, ok, err := a.writeMemories(ctx, yield, ids, nextState, step)
	if !ok {
		return State{}, gopact.Message{}, false
	}
	if err != nil {
		failNode(yield, ids, nodeCallModel, step, input, copyState(nextState), err, startedAt)
		return State{}, gopact.Message{}, false
	}
	completed := reactStepSnapshot(step, nodeCallModel, ids, gopact.StepCompleted, input, copyState(nextState), "", startedAt, time.Now())
	completed.Effects = copyEffectRecords(memoryEffects)
	if !yield(reactEvent(gopact.EventNodeCompleted, ids, nodeCallModel, step, &completed, nil), nil) {
		return State{}, gopact.Message{}, false
	}
	if err := a.writeCheckpoint(ctx, ids, completed, nextState, modelCheckpointQueue(message)); err != nil {
		wrapped := fmt.Errorf("react: checkpoint node %q: %w", nodeCallModel, err)
		yield(reactEvent(gopact.EventRunFailed, ids, nodeCallModel, step, nil, wrapped), wrapped)
		return State{}, gopact.Message{}, false
	}
	return nextState, message, true
}

func streamModelResponse(
	ctx context.Context,
	yield func(gopact.Event, error) bool,
	streamer gopact.StreamingModel,
	request gopact.ModelRequest,
	ids gopact.RuntimeIDs,
	step int,
	input State,
	output State,
	startedAt time.Time,
) (gopact.ModelResponse, bool) {
	var response gopact.ModelResponse
	if streamer == nil {
		return response, false
	}
	for event, err := range streamer.Stream(ctx, request) {
		event = event.WithRuntimeDefaults(ids)
		if event.Node == "" {
			event.Node = nodeCallModel
		}
		if event.Step == 0 {
			event.Step = step
		}
		if event.Type == gopact.EventModelMessage && event.Message != nil {
			response.Message = copyMessage(*event.Message)
			if event.ModelRoute != nil {
				response.Route = *event.ModelRoute
			}
			if event.Usage != nil {
				response.Usage = *event.Usage
			}
		}
		if !yield(event, nil) {
			return response, false
		}
		if err != nil {
			failNode(yield, ids, nodeCallModel, step, input, output, err, startedAt)
			return gopact.ModelResponse{}, false
		}
	}
	if response.Message.Role == "" {
		failNode(yield, ids, nodeCallModel, step, input, output, ErrModelMessageMissing, startedAt)
		return gopact.ModelResponse{}, false
	}
	return response, true
}

func (a *Agent) writeMemories(ctx context.Context, yield func(gopact.Event, error) bool, ids gopact.RuntimeIDs, state State, step int) ([]gopact.EffectRecord, bool, error) {
	if a.memoryStore == nil || a.memoryExtract == nil {
		return nil, true, nil
	}
	extractMode := a.memoryExtractMode
	if extractMode == "" {
		extractMode = MemoryExtractSync
	}
	if extractMode == MemoryExtractDeferred {
		return []gopact.EffectRecord{memoryExtractEffect(ids, step, state)}, true, nil
	}

	memories, err := a.memoryExtract(ctx, copyState(state), ids)
	if err != nil {
		return nil, true, fmt.Errorf("react: extract memories: %w", err)
	}
	if a.memoryMerge != nil {
		memories, err = a.memoryMerge(ctx, MemoryMergeRequest{
			State:    copyState(state),
			IDs:      ids,
			Memories: copyMemories(memories),
		})
		if err != nil {
			return nil, true, fmt.Errorf("react: merge memories: %w", err)
		}
		memories = copyMemories(memories)
	}
	mode := a.memoryWriteMode
	if mode == "" {
		mode = MemoryWriteSync
	}
	effects := make([]gopact.EffectRecord, 0, len(memories))
	for i, item := range memories {
		item = memoryWithRuntimeScope(item, ids)
		if mode == MemoryWriteDeferred {
			if item.ID == "" {
				item.ID = deferredMemoryID(ids, step, i)
			}
			event := reactEvent(gopact.EventMemoryPut, ids, nodeCallModel, step, nil, nil)
			event.Metadata = memoryPutMetadata(item)
			event.Metadata[memoryMetadataWriteMode] = string(MemoryWriteDeferred)
			event.Metadata[memoryMetadataPending] = true
			if !yield(event, nil) {
				return effects, false, nil
			}
			effects = append(effects, deferredMemoryPutEffect(ids, step, i, item))
			continue
		}

		explicitID := item.ID != ""
		id, err := a.memoryStore.Put(ctx, item)
		if err != nil {
			return effects, true, fmt.Errorf("react: put memory: %w", err)
		}
		item.ID = id
		event := reactEvent(gopact.EventMemoryPut, ids, nodeCallModel, step, nil, nil)
		event.Metadata = memoryPutMetadata(item)
		if !yield(event, nil) {
			return effects, false, nil
		}
		effects = append(effects, memoryPutEffect(ids, step, i, item, explicitID))
	}
	return effects, true, nil
}

func (a *Agent) modelMessages(ctx context.Context, yield func(gopact.Event, error) bool, ids gopact.RuntimeIDs, state State, step int) ([]gopact.Message, bool, error) {
	messages := copyMessages(state.Messages)
	if a.memoryStore == nil {
		return messages, true, nil
	}
	queryFn := a.memoryQuery
	if queryFn == nil {
		queryFn = defaultMemoryQuery
	}
	query, search, err := queryFn(ctx, copyState(state), ids)
	if err != nil {
		return nil, false, err
	}
	if !search {
		return messages, true, nil
	}
	if query.Limit == 0 {
		query.Limit = a.memoryLimit
		if query.Limit == 0 {
			query.Limit = defaultMemoryLimit
		}
	}
	result, err := a.memoryStore.Search(ctx, query)
	if err != nil {
		return nil, false, err
	}
	memoryEvent := reactEvent(gopact.EventMemorySearched, ids, nodeCallModel, step, nil, nil)
	memoryEvent.Metadata = memorySearchMetadata(query, result)
	if !yield(memoryEvent, nil) {
		return nil, false, nil
	}
	if len(result.Memories) == 0 {
		return messages, true, nil
	}
	injected := make([]gopact.Message, 0, len(messages)+1)
	injected = append(injected, memoryContextMessage(result))
	injected = append(injected, messages...)
	return injected, true, nil
}

func (a *Agent) callTools(ctx context.Context, yield func(gopact.Event, error) bool, ids gopact.RuntimeIDs, state *State, calls []gopact.ToolCall, step int, startEvent gopact.EventType, resume *gopact.ResumeRequest) bool {
	startedAt := time.Now()
	input := copyState(*state)
	started := reactStepSnapshot(step, nodeCallTool, ids, gopact.StepRunning, input, nil, "", startedAt, time.Time{})
	if startEvent == "" {
		startEvent = gopact.EventNodeStarted
	}
	if !yield(reactEvent(startEvent, ids, nodeCallTool, step, &started, nil), nil) {
		return false
	}
	if a.registry == nil {
		return failNode(yield, ids, nodeCallTool, step, input, copyState(*state), ErrToolRegistryMissing, startedAt)
	}

	var artifacts []gopact.ArtifactRef
	var effects []gopact.EffectRecord
	for _, call := range calls {
		callIDs := toolCallIDs(ids, call.ID)
		if !yield(reactEvent(gopact.EventToolCall, callIDs, nodeCallTool, step, nil, nil, withToolCall(call)), nil) {
			return false
		}
		scope := tools.Scope{IDs: callIDs}
		if resume != nil {
			scope.Metadata = resumeMetadata(*resume)
		}
		result, err := a.registry.InvokeVisible(ctx, call.Name, json.RawMessage(call.Arguments), tools.Scope{
			IDs:      scope.IDs,
			Metadata: scope.Metadata,
		})
		if !yieldToolEvents(yield, result.Events, callIDs, nodeCallTool, step) {
			return false
		}
		if err != nil {
			var interruptErr *gopact.InterruptError
			if errors.As(err, &interruptErr) {
				return a.interruptToolNode(ctx, yield, ids, callIDs, step, input, copyState(*state), interruptErr, artifacts, effects, startedAt)
			}
			return failNode(yield, ids, nodeCallTool, step, input, copyState(*state), err, startedAt)
		}
		artifacts = append(artifacts, copyArtifactRefs(result.Artifacts)...)
		effects = append(effects, copyEffectRecords(result.Effects)...)
		if !yield(reactEvent(gopact.EventToolResult, callIDs, nodeCallTool, step, nil, nil, withToolResult(result)), nil) {
			return false
		}
		state.Messages = append(state.Messages, gopact.Message{
			Role:       gopact.RoleTool,
			Name:       call.Name,
			ToolCallID: call.ID,
			Content:    result.Content,
		})
	}
	completed := reactStepSnapshot(step, nodeCallTool, ids, gopact.StepCompleted, input, copyState(*state), "", startedAt, time.Now())
	completed.Artifacts = copyArtifactRefs(artifacts)
	completed.Effects = copyEffectRecords(effects)
	if !yield(reactEvent(gopact.EventNodeCompleted, ids, nodeCallTool, step, &completed, nil), nil) {
		return false
	}
	if err := a.writeCheckpoint(ctx, ids, completed, *state, []string{nodeCallModel}); err != nil {
		wrapped := fmt.Errorf("react: checkpoint node %q: %w", nodeCallTool, err)
		yield(reactEvent(gopact.EventRunFailed, ids, nodeCallTool, step, nil, wrapped), wrapped)
		return false
	}
	return true
}

func (a *Agent) interruptToolNode(
	ctx context.Context,
	yield func(gopact.Event, error) bool,
	runIDs gopact.RuntimeIDs,
	interruptIDs gopact.RuntimeIDs,
	step int,
	input State,
	output State,
	interruptErr *gopact.InterruptError,
	artifacts []gopact.ArtifactRef,
	effects []gopact.EffectRecord,
	startedAt time.Time,
) bool {
	record := interruptErr.Record
	interrupted := reactStepSnapshot(step, nodeCallTool, interruptIDs, gopact.StepInterrupted, input, output, "", startedAt, time.Now())
	interrupted.Pending = &record
	interrupted.Queue = []string{nodeCallTool}
	interrupted.Artifacts = copyArtifactRefs(artifacts)
	interrupted.Effects = copyEffectRecords(effects)
	if err := a.writeCheckpoint(ctx, runIDs, interrupted, output, interrupted.Queue); err != nil {
		wrapped := fmt.Errorf("react: checkpoint interrupted node %q: %w", nodeCallTool, err)
		yield(reactEvent(gopact.EventRunFailed, runIDs, nodeCallTool, step, nil, wrapped), wrapped)
		return false
	}
	if !yield(reactEvent(gopact.EventInterrupted, interruptIDs, nodeCallTool, step, &interrupted, interruptErr), nil) {
		return false
	}
	yield(reactEvent(gopact.EventRunInterrupted, runIDs, nodeCallTool, step, nil, interruptErr), interruptErr)
	return false
}

func (a *Agent) writeCheckpoint(ctx context.Context, ids gopact.RuntimeIDs, snapshot gopact.StepSnapshot, state State, queue []string) error {
	if a.checkpointer == nil {
		return nil
	}
	return a.checkpointer.Put(ctx, graph.Checkpoint[State]{
		ID:        snapshot.ID,
		IDs:       resumeRunIDs(ids),
		ThreadID:  ids.ThreadID,
		Step:      snapshot.Step,
		Node:      snapshot.Node,
		Phase:     snapshot.Phase,
		State:     copyState(state),
		Queue:     append([]string(nil), queue...),
		Pending:   copyPending(snapshot.Pending),
		Effects:   copyEffectRecords(snapshot.Effects),
		CreatedAt: time.Now(),
		Metadata:  copyAnyMap(snapshot.Metadata),
	})
}

func modelCheckpointQueue(message gopact.Message) []string {
	if len(message.ToolCalls) == 0 {
		return nil
	}
	return []string{nodeCallTool}
}

func yieldToolEvents(yield func(gopact.Event, error) bool, events []gopact.Event, ids gopact.RuntimeIDs, node string, step int) bool {
	for _, event := range events {
		event = event.WithRuntimeDefaults(ids)
		if event.Node == "" {
			event.Node = node
		}
		if event.Step == 0 {
			event.Step = step
		}
		if !yield(event, nil) {
			return false
		}
	}
	return true
}

func (a *Agent) visibleToolSpecs(ctx context.Context, ids gopact.RuntimeIDs) ([]gopact.ToolSpec, error) {
	if a.registry == nil {
		return nil, nil
	}
	infos, err := a.registry.Visible(ctx, tools.Scope{IDs: ids})
	if err != nil {
		return nil, err
	}
	specs := make([]gopact.ToolSpec, 0, len(infos))
	for _, info := range infos {
		specs = append(specs, gopact.ToolSpec{
			Name:        info.Name,
			Description: info.Description,
			InputSchema: info.Schema,
		})
	}
	return specs, nil
}

func inputState(input any) (State, error) {
	switch value := input.(type) {
	case State:
		value.Messages = append([]gopact.Message(nil), value.Messages...)
		return value, nil
	case []gopact.Message:
		return State{Messages: append([]gopact.Message(nil), value...)}, nil
	case gopact.Message:
		return State{Messages: []gopact.Message{value}}, nil
	default:
		return State{}, fmt.Errorf("%w: got %T", ErrInvalidInput, input)
	}
}

type eventOption func(*gopact.Event)

func withMessage(message gopact.Message) eventOption {
	return func(event *gopact.Event) {
		copied := copyMessage(message)
		event.Message = &copied
	}
}

func withToolCall(call gopact.ToolCall) eventOption {
	return func(event *gopact.Event) {
		copied := copyToolCall(call)
		event.ToolCall = &copied
	}
}

func withToolResult(result gopact.ToolResult) eventOption {
	return func(event *gopact.Event) {
		copied := result
		copied.Artifacts = copyArtifactRefs(result.Artifacts)
		copied.Effects = copyEffectRecords(result.Effects)
		copied.Metadata = copyAnyMap(result.Metadata)
		event.Result = &copied
		event.Artifacts = copyArtifactRefs(result.Artifacts)
	}
}

func reactEvent(eventType gopact.EventType, ids gopact.RuntimeIDs, node string, step int, snapshot *gopact.StepSnapshot, err error, opts ...eventOption) gopact.Event {
	event := gopact.Event{
		Type:         eventType,
		IDs:          ids,
		RunID:        ids.RunID,
		ThreadID:     ids.ThreadID,
		Node:         node,
		Step:         step,
		StepSnapshot: snapshot,
		Err:          err,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&event)
		}
	}
	return event
}

func failNode(yield func(gopact.Event, error) bool, ids gopact.RuntimeIDs, node string, step int, input State, output State, err error, startedAt time.Time) bool {
	failed := reactStepSnapshot(step, node, ids, gopact.StepFailed, input, output, err.Error(), startedAt, time.Now())
	if !yield(reactEvent(gopact.EventNodeFailed, ids, node, step, &failed, err), nil) {
		return false
	}
	yield(reactEvent(gopact.EventRunFailed, ids, node, step, nil, err), err)
	return false
}

func reactStepSnapshot(step int, node string, ids gopact.RuntimeIDs, phase gopact.StepPhase, input State, output any, errText string, startedAt time.Time, completedAt time.Time) gopact.StepSnapshot {
	return gopact.StepSnapshot{
		ID:          reactStepID(ids, step),
		Step:        step,
		Node:        node,
		Phase:       phase,
		IDs:         ids,
		Input:       copyState(input),
		Output:      output,
		Error:       errText,
		StartedAt:   startedAt,
		CompletedAt: completedAt,
	}
}

func reactStepID(ids gopact.RuntimeIDs, step int) string {
	if ids.RunID == "" {
		return fmt.Sprintf("step:%d", step)
	}
	return fmt.Sprintf("%s:%d", ids.RunID, step)
}

func toolCallIDs(ids gopact.RuntimeIDs, callID string) gopact.RuntimeIDs {
	callIDs := ids
	if callIDs.ParentCallID == "" {
		callIDs.ParentCallID = ids.CallID
	}
	callIDs.CallID = callID
	return callIDs
}

func resumeRunIDs(ids gopact.RuntimeIDs) gopact.RuntimeIDs {
	ids.CallID = ""
	ids.ParentCallID = ""
	return ids
}

func checkpointPhase(checkpoint graph.Checkpoint[State]) gopact.StepPhase {
	if checkpoint.Phase == "" {
		return gopact.StepCompleted
	}
	return checkpoint.Phase
}

func checkpointIDs(checkpoint graph.Checkpoint[State]) gopact.RuntimeIDs {
	ids := checkpoint.IDs
	if ids.ThreadID == "" {
		ids.ThreadID = checkpoint.ThreadID
	}
	return resumeRunIDs(ids)
}

func checkpointStepSnapshot(checkpoint graph.Checkpoint[State], ids gopact.RuntimeIDs) gopact.StepSnapshot {
	metadata := copyAnyMap(checkpoint.Metadata)
	if checkpoint.ID != "" {
		if metadata == nil {
			metadata = make(map[string]any)
		}
		metadata["checkpoint_id"] = checkpoint.ID
	}
	if checkpoint.ConfigVersion != "" {
		if metadata == nil {
			metadata = make(map[string]any)
		}
		metadata["config_version"] = checkpoint.ConfigVersion
	}
	return gopact.StepSnapshot{
		ID:          reactStepID(ids, checkpoint.Step),
		Step:        checkpoint.Step,
		Node:        checkpoint.Node,
		Phase:       checkpointPhase(checkpoint),
		IDs:         ids,
		Output:      copyState(checkpoint.State),
		Queue:       append([]string(nil), checkpoint.Queue...),
		Pending:     copyPending(checkpoint.Pending),
		Effects:     copyEffectRecords(checkpoint.Effects),
		StartedAt:   checkpoint.CreatedAt,
		CompletedAt: checkpoint.CreatedAt,
		Metadata:    metadata,
	}
}

func verifySnapshotArtifacts(ctx context.Context, verifier graph.ArtifactVerifier, snapshot gopact.StepSnapshot) error {
	if verifier == nil {
		return nil
	}
	refs := snapshotArtifactRefs(snapshot)
	if len(refs) == 0 {
		return nil
	}
	return verifier.VerifyRefs(ctx, refs)
}

func snapshotArtifactRefs(snapshot gopact.StepSnapshot) []gopact.ArtifactRef {
	refs := copyArtifactRefs(snapshot.Artifacts)
	for _, effect := range snapshot.Effects {
		refs = append(refs, copyArtifactRefs(effect.Artifacts)...)
	}
	return refs
}

func pendingToolCalls(state State) []gopact.ToolCall {
	for i := len(state.Messages) - 1; i >= 0; i-- {
		message := state.Messages[i]
		if message.Role == gopact.RoleAssistant && len(message.ToolCalls) > 0 {
			completed := completedToolCallIDs(state.Messages[i+1:])
			calls := make([]gopact.ToolCall, 0, len(message.ToolCalls))
			for _, call := range message.ToolCalls {
				if _, ok := completed[call.ID]; ok {
					continue
				}
				calls = append(calls, copyToolCall(call))
			}
			return calls
		}
	}
	return nil
}

func completedToolCallIDs(messages []gopact.Message) map[string]struct{} {
	completed := make(map[string]struct{})
	for _, message := range messages {
		if message.Role != gopact.RoleTool || message.ToolCallID == "" {
			continue
		}
		completed[message.ToolCallID] = struct{}{}
	}
	return completed
}

func resumeMetadata(resume gopact.ResumeRequest) map[string]any {
	metadata := copyAnyMap(resume.Metadata)
	if metadata == nil {
		metadata = make(map[string]any)
	}
	copied := copyResumeRequest(resume)
	metadata[gopact.MetadataResumeRequest] = copied
	metadata[gopact.MetadataResumePayload] = resume.Payload
	metadata["interrupt_id"] = resume.InterruptID
	if resume.StepID != "" {
		metadata["step_id"] = resume.StepID
	}
	if resume.CheckpointID != "" {
		metadata["checkpoint_id"] = resume.CheckpointID
	}
	return metadata
}

func resumeEventMetadata(resume gopact.ResumeRequest) map[string]any {
	metadata := resumeMetadata(resume)
	delete(metadata, gopact.MetadataResumeRequest)
	return metadata
}

func defaultMemoryQuery(ctx context.Context, state State, ids gopact.RuntimeIDs) (memory.Query, bool, error) {
	if err := ctx.Err(); err != nil {
		return memory.Query{}, false, err
	}
	text := lastUserText(state)
	if text == "" {
		return memory.Query{}, false, nil
	}
	return memory.Query{
		Scope: memory.Scope{
			UserID:    ids.UserID,
			SessionID: ids.SessionID,
			ThreadID:  ids.ThreadID,
			AgentID:   ids.AgentID,
			AppID:     ids.AppID,
		},
		Text: text,
	}, true, nil
}

func lastUserText(state State) string {
	for i := len(state.Messages) - 1; i >= 0; i-- {
		if state.Messages[i].Role != gopact.RoleUser {
			continue
		}
		if text := strings.TrimSpace(state.Messages[i].Text()); text != "" {
			return text
		}
	}
	return ""
}

func memoryContextMessage(result memory.SearchResult) gopact.Message {
	text := memoryContextText(result)
	part := gopact.TextPart(text)
	part.Metadata = map[string]any{
		"source":     "memory_recall",
		"memory_ids": memoryIDs(result),
	}
	return gopact.Message{
		Role:    gopact.RoleSystem,
		Name:    memoryMessageName,
		Content: text,
		Parts:   []gopact.ContentPart{part},
	}
}

func memoryContextText(result memory.SearchResult) string {
	var b strings.Builder
	b.WriteString("Relevant memory:\n")
	for _, memory := range result.Memories {
		fmt.Fprintf(&b, "- [%s:%s] %s\n", memory.Type, memory.ID, memory.Content)
	}
	return strings.TrimRight(b.String(), "\n")
}

func memorySearchMetadata(query memory.Query, result memory.SearchResult) map[string]any {
	return map[string]any{
		"memory_query":  query,
		"memory_count":  len(result.Memories),
		"memory_ids":    memoryIDs(result),
		"memory_result": copyMemorySearchResult(result),
	}
}

func memoryPutMetadata(item memory.Memory) map[string]any {
	return map[string]any{
		memory.EffectReplayMetadataMemoryID: item.ID,
		memory.EffectMetadataMemory:         copyMemory(item),
	}
}

func memoryPutEffect(ids gopact.RuntimeIDs, step int, index int, item memory.Memory, explicitID bool) gopact.EffectRecord {
	policy := gopact.EffectReplayRecordOnly
	idempotencyKey := ""
	if explicitID {
		policy = gopact.EffectReplayIdempotent
		idempotencyKey = "memory:" + string(item.ID)
	}
	return gopact.EffectRecord{
		ID:             fmt.Sprintf("%s:memory_put:%d", reactStepID(ids, step), index+1),
		Type:           memory.EffectTypeMemoryPut,
		Target:         "memory://" + string(item.ID),
		Applied:        true,
		ReplayPolicy:   policy,
		IdempotencyKey: idempotencyKey,
		Metadata: map[string]any{
			memory.EffectMetadataMemory:         copyMemory(item),
			memory.EffectReplayMetadataMemoryID: item.ID,
		},
	}
}

func deferredMemoryPutEffect(ids gopact.RuntimeIDs, step int, index int, item memory.Memory) gopact.EffectRecord {
	return gopact.EffectRecord{
		ID:             fmt.Sprintf("%s:memory_put:%d", reactStepID(ids, step), index+1),
		Type:           memory.EffectTypeMemoryPut,
		Target:         "memory://" + string(item.ID),
		Applied:        false,
		ReplayPolicy:   gopact.EffectReplayIdempotent,
		IdempotencyKey: "memory:" + string(item.ID),
		Metadata: map[string]any{
			memory.EffectMetadataMemory:         copyMemory(item),
			memory.EffectReplayMetadataMemoryID: item.ID,
			memoryMetadataWriteMode:             string(MemoryWriteDeferred),
			memoryMetadataPending:               true,
		},
	}
}

func memoryExtractEffect(ids gopact.RuntimeIDs, step int, state State) gopact.EffectRecord {
	effectID := fmt.Sprintf("%s:memory_extract", reactStepID(ids, step))
	return gopact.EffectRecord{
		ID:             effectID,
		Type:           memory.EffectTypeMemoryExtract,
		Target:         "memory://extract/" + effectID,
		Applied:        false,
		ReplayPolicy:   gopact.EffectReplayIdempotent,
		IdempotencyKey: "memory_extract:" + effectID,
		Metadata: map[string]any{
			memory.EffectMetadataMemoryExtractState: copyState(state),
			memory.EffectMetadataMemoryExtractIDs:   ids,
			memoryMetadataExtractMode:               string(MemoryExtractDeferred),
			memoryMetadataPending:                   true,
		},
	}
}

func deferredMemoryID(ids gopact.RuntimeIDs, step int, index int) memory.ID {
	return memory.ID(fmt.Sprintf("%s:memory:%d", reactStepID(ids, step), index+1))
}

func memoryWithRuntimeScope(item memory.Memory, ids gopact.RuntimeIDs) memory.Memory {
	out := copyMemory(item)
	if out.Scope.UserID == "" {
		out.Scope.UserID = ids.UserID
	}
	if out.Scope.SessionID == "" {
		out.Scope.SessionID = ids.SessionID
	}
	if out.Scope.ThreadID == "" {
		out.Scope.ThreadID = ids.ThreadID
	}
	if out.Scope.AgentID == "" {
		out.Scope.AgentID = ids.AgentID
	}
	if out.Scope.AppID == "" {
		out.Scope.AppID = ids.AppID
	}
	return out
}

func memoryIDs(result memory.SearchResult) []string {
	if len(result.Memories) == 0 {
		return nil
	}
	ids := make([]string, 0, len(result.Memories))
	for _, memory := range result.Memories {
		ids = append(ids, string(memory.ID))
	}
	return ids
}

func copyMemory(in memory.Memory) memory.Memory {
	out := in
	out.Metadata = copyAnyMap(in.Metadata)
	return out
}

func copyMemories(in []memory.Memory) []memory.Memory {
	if len(in) == 0 {
		return nil
	}
	out := make([]memory.Memory, len(in))
	for i, item := range in {
		out[i] = copyMemory(item)
	}
	return out
}

func copyMemorySearchResult(in memory.SearchResult) memory.SearchResult {
	if len(in.Memories) == 0 {
		return memory.SearchResult{}
	}
	out := memory.SearchResult{Memories: make([]memory.Memory, len(in.Memories))}
	for i, memory := range in.Memories {
		out.Memories[i] = copyMemory(memory)
	}
	return out
}

func copyState(in State) State {
	return State{Messages: copyMessages(in.Messages)}
}

func copyMessages(in []gopact.Message) []gopact.Message {
	if len(in) == 0 {
		return nil
	}
	out := make([]gopact.Message, len(in))
	for i, message := range in {
		out[i] = copyMessage(message)
	}
	return out
}

func copyMessage(in gopact.Message) gopact.Message {
	out := in
	out.Parts = copyContentParts(in.Parts)
	out.ToolCalls = copyToolCalls(in.ToolCalls)
	return out
}

func copyContentParts(in []gopact.ContentPart) []gopact.ContentPart {
	if len(in) == 0 {
		return nil
	}
	out := make([]gopact.ContentPart, len(in))
	for i, part := range in {
		out[i] = part
		out[i].Metadata = copyAnyMap(part.Metadata)
	}
	return out
}

func copyToolCalls(in []gopact.ToolCall) []gopact.ToolCall {
	if len(in) == 0 {
		return nil
	}
	out := make([]gopact.ToolCall, len(in))
	for i, call := range in {
		out[i] = copyToolCall(call)
	}
	return out
}

func copyToolCall(in gopact.ToolCall) gopact.ToolCall {
	out := in
	out.Arguments = append([]byte(nil), in.Arguments...)
	return out
}

func copyArtifactRefs(in []gopact.ArtifactRef) []gopact.ArtifactRef {
	if len(in) == 0 {
		return nil
	}
	out := make([]gopact.ArtifactRef, len(in))
	for i, ref := range in {
		out[i] = ref
		out[i].Metadata = copyAnyMap(ref.Metadata)
	}
	return out
}

func copyEffectRecords(in []gopact.EffectRecord) []gopact.EffectRecord {
	if len(in) == 0 {
		return nil
	}
	out := make([]gopact.EffectRecord, len(in))
	for i, effect := range in {
		out[i] = effect
		out[i].DependsOn = append([]string(nil), effect.DependsOn...)
		out[i].Artifacts = copyArtifactRefs(effect.Artifacts)
		if effect.Sandbox != nil {
			sandbox := *effect.Sandbox
			sandbox.Command = append([]string(nil), effect.Sandbox.Command...)
			sandbox.Metadata = copyAnyMap(effect.Sandbox.Metadata)
			out[i].Sandbox = &sandbox
		}
		out[i].Metadata = copyAnyMap(effect.Metadata)
	}
	return out
}

func copyStepSnapshot(in gopact.StepSnapshot) gopact.StepSnapshot {
	out := in
	out.Input = copySnapshotValue(in.Input)
	out.Output = copySnapshotValue(in.Output)
	out.Queue = append([]string(nil), in.Queue...)
	out.Pending = copyPending(in.Pending)
	out.Effects = copyEffectRecords(in.Effects)
	out.Artifacts = copyArtifactRefs(in.Artifacts)
	out.Metadata = copyAnyMap(in.Metadata)
	return out
}

func copyPending(in *gopact.InterruptRecord) *gopact.InterruptRecord {
	if in == nil {
		return nil
	}
	out := *in
	out.Metadata = copyAnyMap(in.Metadata)
	return &out
}

func copySnapshotValue(in any) any {
	switch value := in.(type) {
	case State:
		return copyState(value)
	default:
		return value
	}
}

func copyResumeRequest(in gopact.ResumeRequest) gopact.ResumeRequest {
	out := in
	out.IDs = in.IDs
	out.Metadata = copyAnyMap(in.Metadata)
	return out
}

func copyAnyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
