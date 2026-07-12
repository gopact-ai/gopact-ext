package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact/agent"
	"github.com/gopact-ai/gopact/runlog"
	"github.com/gopact-ai/gopact/workflow"
)

type persistence interface {
	workflow.Checkpointer
	workflow.CheckpointHistory
	workflow.CheckpointController
	runlog.Log
}

func TestWorkflowPersistenceConformance(t *testing.T) {
	t.Run("memory", func(t *testing.T) {
		store := workflow.NewMemoryStore()
		requireWorkflowPersistence(t, store, func(*testing.T) persistence { return store })
	})
	t.Run("sqlite", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "workflow.db")
		store, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = store.Close() }()
		requireWorkflowPersistence(t, store, func(t *testing.T) persistence {
			t.Helper()
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			store, err = Open(path)
			if err != nil {
				t.Fatal(err)
			}
			return store
		})
	})
}

func requireWorkflowPersistence(t *testing.T, store persistence, reopen func(*testing.T) persistence) {
	t.Helper()
	bodyCalls := 0
	interrupt := true
	wf := persistenceWorkflow(store, &bodyCalls, &interrupt)
	_, err := wf.Invoke(context.Background(), "input", gopact.WithRunID("store-run"))
	var interrupted workflow.InterruptError
	if !errors.As(err, &interrupted) {
		t.Fatalf("Invoke() error = %v, want interrupt", err)
	}
	store = reopen(t)
	wf = persistenceWorkflow(store, &bodyCalls, &interrupt)
	output, err := wf.Invoke(context.Background(), "", workflow.WithResume(workflow.ResumeRequest{
		RunID: "store-run", CheckpointID: interrupted.CheckpointID,
		Resolutions: []workflow.InterruptResolution{{InterruptID: "approval", PayloadRef: "resolution://approved"}},
	}))
	if err != nil || output != "input-done" || bodyCalls != 1 {
		t.Fatalf("Resume() = %q, %v, calls %d", output, err, bodyCalls)
	}
	snapshot, err := wf.Snapshot(context.Background(), workflow.SnapshotRequest{RunID: "store-run"})
	if err != nil || len(snapshot.Timeline) == 0 || len(snapshot.Checkpoints) == 0 {
		t.Fatalf("Snapshot() = %+v, %v, want durable timeline and checkpoints", snapshot, err)
	}
	empty, err := store.ListCheckpoints(context.Background(), workflow.CheckpointHistoryRequest{
		RunID: "store-run", AfterVersion: 1 << 60,
	})
	if err != nil || len(empty) != 0 {
		t.Fatalf("ListCheckpoints(after end) = %+v, %v, want empty history", empty, err)
	}
	version := nodeVersion(t, snapshot.Timeline, "work")
	retried, err := wf.Retry(context.Background(), workflow.RetryRequest{
		RunID: "store-run", NodeID: "work", NodeExecutionVersion: version,
	})
	if err != nil || retried != "input-done" || bodyCalls != 2 {
		t.Fatalf("Retry() = %q, %v, calls %d", retried, err, bodyCalls)
	}
	requireRunLogSemantics(t, store)
}

func nodeVersion(t *testing.T, records []runlog.Record, nodeID string) int64 {
	t.Helper()
	for _, record := range records {
		if record.NodeID == nodeID && record.EventType == workflow.EventNodeCompleted {
			return record.NodeExecutionVersion
		}
	}
	t.Fatalf("completed node %q not found in timeline", nodeID)
	return 0
}

func persistenceWorkflow(store persistence, bodyCalls *int, interrupt *bool) *workflow.Workflow[string, string] {
	wf := workflow.New[string, string](
		"store-conformance", workflow.WithTopologyVersion("v1"),
		workflow.WithStrictCheckpointer(store), workflow.WithStrictJournal(store),
	)
	work := wf.Node("work", func(_ context.Context, input string) (string, error) {
		*bodyCalls++
		return input + "-done", nil
	})
	work.Guard(workflow.BeforeRun("approval", workflow.GuardFunc[string, string](
		func(context.Context, workflow.GuardContext[string, string]) (workflow.GuardDecision[string, string], error) {
			if !*interrupt {
				return workflow.GuardAllow[string, string]{}, nil
			}
			*interrupt = false
			return workflow.GuardInterrupt[string, string]{Request: workflow.InterruptRequest{ID: "approval"}}, nil
		},
	)))
	wf.Entry(work)
	wf.Exit(work)
	return wf
}

func requireRunLogSemantics(t *testing.T, log runlog.Log) {
	t.Helper()
	record := runlog.Record{
		SessionID: "session-1", RunID: "log-run", Sequence: 1, EventType: "test", Source: "test", Timestamp: time.Now().UTC(),
	}
	if err := log.Append(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	if err := log.Append(context.Background(), record); err != nil {
		t.Fatalf("idempotent Append() error = %v", err)
	}
	record.Summary = "conflict"
	if err := log.Append(context.Background(), record); !errors.Is(err, runlog.ErrConflict) {
		t.Fatalf("conflicting Append() error = %v, want ErrConflict", err)
	}
	records, err := log.List(context.Background(), runlog.Query{RunID: "log-run"})
	if err != nil || len(records) != 1 {
		t.Fatalf("List() = %+v, %v, want one record", records, err)
	}
}

func TestSessionRunLogPersistsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	records := []runlog.Record{
		sqliteRunRecord("session-1", "run-a", 1, workflow.EventWorkflowStarted),
		sqliteRunRecord("session-2", "run-x", 1, workflow.EventWorkflowStarted),
		sqliteRunRecord("session-1", "run-b", 1, workflow.EventWorkflowStarted),
		sqliteRunRecord("session-1", "run-a", 2, workflow.EventWorkflowCompleted),
	}
	for _, record := range records {
		if err := store.Append(context.Background(), record); err != nil {
			t.Fatalf("Append() error = %v", err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	}()

	session, err := store.List(context.Background(), runlog.Query{SessionID: "session-1"})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(session) != 3 || session[0].RunID != "run-a" || session[0].Sequence != 1 ||
		session[1].RunID != "run-b" || session[1].Sequence != 1 ||
		session[2].RunID != "run-a" || session[2].Sequence != 2 {
		t.Fatalf("session records = %+v, want append-ordered per-run sequences", session)
	}
	run, err := store.List(context.Background(), runlog.Query{SessionID: "session-1", RunID: "run-a", After: 1})
	if err != nil || len(run) != 1 || run[0].Sequence != 2 {
		t.Fatalf("session/run records = %+v, %v, want run-a sequence 2", run, err)
	}
	if _, err := store.List(context.Background(), runlog.Query{SessionID: "session-1", After: 1}); !errors.Is(err, runlog.ErrInvalidQuery) {
		t.Fatalf("session-only List() error = %v, want ErrInvalidQuery", err)
	}
	summaries, err := workflow.ListSessionRuns(context.Background(), store, workflow.SessionRunsRequest{SessionID: "session-1"})
	if err != nil {
		t.Fatalf("ListSessionRuns() error = %v", err)
	}
	if len(summaries) != 2 || summaries[0].RunID != "run-a" || summaries[0].Status != workflow.CheckpointCompleted ||
		summaries[1].RunID != "run-b" || summaries[1].Status != workflow.CheckpointRunning {
		t.Fatalf("summaries = %+v, want run-a and run-b", summaries)
	}
}

func TestWorkflowAgentPersistsSessionRun(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	}()
	identity := agent.Identity{Name: "sqlite-agent", Description: "test", Version: "v1"}
	wf := workflow.New[agent.Request, agent.Response](
		identity.Name,
		workflow.WithTopologyVersion(identity.Version),
		workflow.WithStrictCheckpointer(store),
		workflow.WithStrictJournal(store),
	)
	respond := wf.Node("respond", func(_ context.Context, request agent.Request) (agent.Response, error) {
		return agent.Response{Message: request.Messages[0]}, nil
	})
	wf.Entry(respond)
	wf.Exit(respond)
	target, err := agent.NewWorkflowAgent(identity, wf)
	if err != nil {
		t.Fatal(err)
	}
	_, err = target.Invoke(
		context.Background(),
		agent.Request{Messages: []gopact.Message{gopact.UserMessage("hello")}},
		gopact.WithSessionID("session-1"),
		gopact.WithRunID("agent-run"),
	)
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	summaries, err := workflow.ListSessionRuns(context.Background(), store, workflow.SessionRunsRequest{SessionID: "session-1"})
	if err != nil || len(summaries) != 1 || summaries[0].RunID != "agent-run" || summaries[0].Status != workflow.CheckpointCompleted {
		t.Fatalf("ListSessionRuns() = %+v, %v, want completed agent-run", summaries, err)
	}
}

func TestSQLiteResumeRestoresAndValidatesSessionID(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	}()
	bodyCalls := 0
	interrupt := true
	wf := persistenceWorkflow(store, &bodyCalls, &interrupt)
	_, err = wf.Invoke(context.Background(), "input", gopact.WithRunID("resume-run"), gopact.WithSessionID("session-1"))
	var interrupted workflow.InterruptError
	if !errors.As(err, &interrupted) {
		t.Fatalf("Invoke() error = %v, want interrupt", err)
	}
	resume := workflow.WithResume(workflow.ResumeRequest{
		RunID: "resume-run", CheckpointID: interrupted.CheckpointID,
		Resolutions: []workflow.InterruptResolution{{InterruptID: "approval", PayloadRef: "resolution://approved"}},
	})
	if _, err := wf.Invoke(context.Background(), "", resume, gopact.WithSessionID("session-2")); !errors.Is(err, workflow.ErrCheckpointMismatch) {
		t.Fatalf("mismatched resume error = %v, want ErrCheckpointMismatch", err)
	}
	output, err := wf.Invoke(context.Background(), "", resume)
	if err != nil || output != "input-done" {
		t.Fatalf("resume Invoke() = %q, %v", output, err)
	}
	checkpoint, err := store.Load(context.Background(), "resume-run")
	if err != nil || checkpoint.SessionID != "session-1" {
		t.Fatalf("Load() = %+v, %v, want session-1", checkpoint, err)
	}
}

func TestCheckpointSessionIdentityIsImmutable(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	}()
	now := time.Now().UTC()
	record := workflow.CheckpointRecord{
		ID: "checkpoint:run-1", SessionID: "session-1", RunID: "run-1", WorkflowName: "workflow",
		TopologyVersion: "v1", SchemaVersion: 2, Version: 1, Status: workflow.CheckpointRunning,
		Payload: []byte(`{"state":"running"}`), ReplayStatus: workflow.ReplayUnknown, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.Create(context.Background(), record); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	loaded, err := store.Load(context.Background(), "run-1")
	if err != nil || loaded.SessionID != "session-1" {
		t.Fatalf("Load() = %+v, %v, want session-1", loaded, err)
	}
	missing := loaded
	missing.SessionID = ""
	if err := store.Save(context.Background(), missing, loaded.Version); !errors.Is(err, workflow.ErrInvalidCheckpoint) {
		t.Fatalf("Save() missing session error = %v, want ErrInvalidCheckpoint", err)
	}
	changed := loaded
	changed.SessionID = "session-2"
	if err := store.Save(context.Background(), changed, loaded.Version); !errors.Is(err, workflow.ErrCheckpointMismatch) {
		t.Fatalf("Save() changed session error = %v, want ErrCheckpointMismatch", err)
	}
	completed := loaded
	completed.Status = workflow.CheckpointCompleted
	if err := store.Finish(context.Background(), completed, loaded.Version); err != nil {
		t.Fatalf("Finish() error = %v", err)
	}
	terminal, err := store.Load(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	reopened := terminal
	reopened.Status = workflow.CheckpointRunning
	reopened.SessionID = "session-2"
	if err := store.Reopen(context.Background(), reopened, terminal.Version); !errors.Is(err, workflow.ErrCheckpointMismatch) {
		t.Fatalf("Reopen() changed session error = %v, want ErrCheckpointMismatch", err)
	}
	history, err := store.ListCheckpoints(context.Background(), workflow.CheckpointHistoryRequest{RunID: "run-1"})
	if err != nil || len(history) != 2 || history[0].SessionID != "session-1" || history[1].SessionID != "session-1" {
		t.Fatalf("ListCheckpoints() = %+v, %v, want immutable session history", history, err)
	}
}

func TestOpenMigratesLegacyRunLogTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE gopact_runlog (
		ordinal INTEGER PRIMARY KEY AUTOINCREMENT,
		run_id TEXT NOT NULL,
		sequence INTEGER NOT NULL,
		record_json BLOB NOT NULL,
		UNIQUE (run_id, sequence)
	)`)
	if err != nil {
		t.Fatal(err)
	}
	legacy := sqliteRunRecord("", "legacy-run", 1, "legacy.event")
	payload, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO gopact_runlog (run_id, sequence, record_json) VALUES (?, ?, ?)`, legacy.RunID, legacy.Sequence, payload); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	}()
	if !hasSQLiteColumn(t, store.db, eventTable, "session_id") {
		t.Fatal("session_id column was not added")
	}
	if !hasSQLiteIndex(t, store.db, "gopact_runlog_session_ordinal") {
		t.Fatal("session ordinal index was not added")
	}
	legacyByRun, err := store.List(context.Background(), runlog.Query{RunID: "legacy-run"})
	if err != nil || len(legacyByRun) != 1 || legacyByRun[0].SessionID != "" {
		t.Fatalf("legacy run query = %+v, %v, want unchanged empty session", legacyByRun, err)
	}
	legacyBySession, err := store.List(context.Background(), runlog.Query{SessionID: "legacy-session"})
	if err != nil || len(legacyBySession) != 0 {
		t.Fatalf("legacy session query = %+v, %v, want no guessed backfill", legacyBySession, err)
	}
	newRecord := sqliteRunRecord("session-1", "new-run", 1, workflow.EventWorkflowStarted)
	if err := store.Append(context.Background(), newRecord); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	newSession, err := store.List(context.Background(), runlog.Query{SessionID: "session-1"})
	if err != nil || len(newSession) != 1 || newSession[0].RunID != "new-run" {
		t.Fatalf("new session query = %+v, %v", newSession, err)
	}
}

func hasSQLiteColumn(t *testing.T, db *sql.DB, table, column string) bool {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), `PRAGMA table_info(`+table+`)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, dataType string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatal(err)
		}
		if name == column {
			return true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return false
}

func hasSQLiteIndex(t *testing.T, db *sql.DB, index string) bool {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), `PRAGMA index_list(`+eventTable+`)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var sequence, unique, partial int
		var name, origin string
		if err := rows.Scan(&sequence, &name, &unique, &origin, &partial); err != nil {
			t.Fatal(err)
		}
		if name == index {
			return true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return false
}

func sqliteRunRecord(sessionID, runID string, sequence int64, eventType string) runlog.Record {
	return runlog.Record{
		SessionID: sessionID, RunID: runID, Sequence: sequence, EventType: eventType, Source: "test",
		DefinitionID: "definition", DefinitionVersion: "v1", Timestamp: time.Now().UTC(),
	}
}
