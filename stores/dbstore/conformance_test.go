package dbstore

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gopact-ai/gopact/runlog"
	"github.com/gopact-ai/gopact/workflow"
	"github.com/libtnb/sqlite"
)

var (
	_ workflow.Store = (*Store)(nil)
)

func TestStoreLifecycleAndFencedRunLog(t *testing.T) {
	store, err := Open(sqlite.Open(filepath.Join(t.TempDir(), "lifecycle.db")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	record := testCheckpoint("run-1")
	if err := store.Create(t.Context(), record); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	renewed := record.LeaseExpiresAt.Add(time.Minute)
	if err := store.RenewLease(t.Context(), workflow.CheckpointLease{
		RunID: record.RunID, OwnerID: record.OwnerID, ClaimSequence: record.ClaimSequence, ExpiresAt: renewed,
	}); err != nil {
		t.Fatalf("RenewLease() error = %v", err)
	}

	current, err := store.Load(t.Context(), record.RunID)
	if err != nil {
		t.Fatal(err)
	}
	current.Payload = []byte(`{"state":"saved"}`)
	if err := store.Save(t.Context(), current, current.Version); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	current, _ = store.Load(t.Context(), record.RunID)
	if !current.LeaseExpiresAt.Equal(renewed) {
		t.Fatalf("Save() shortened renewed lease to %v", current.LeaseExpiresAt)
	}
	event := runlog.Record{
		SessionID: current.SessionID, RunID: current.RunID, Sequence: 1,
		EventType: "test.event", Source: "test", Timestamp: time.Now().UTC(),
	}
	fence := runlog.Fence{OwnerID: current.OwnerID, ClaimSequence: current.ClaimSequence}
	if err := store.AppendFenced(t.Context(), event, fence); err != nil {
		t.Fatalf("AppendFenced() error = %v", err)
	}
	if err := store.AppendFenced(t.Context(), event, fence); err != nil {
		t.Fatalf("idempotent AppendFenced() error = %v", err)
	}
	conflict := event
	conflict.Summary = "different"
	if err := store.AppendFenced(t.Context(), conflict, fence); !errors.Is(err, runlog.ErrConflict) {
		t.Fatalf("conflicting AppendFenced() error = %v, want ErrConflict", err)
	}
	events, err := store.List(t.Context(), runlog.Query{RunID: current.RunID})
	if err != nil || len(events) != 1 {
		t.Fatalf("List() = %+v, %v, want one event", events, err)
	}
	current.Status = workflow.CheckpointCompleted
	current.OwnerID = ""
	current.LeaseExpiresAt = time.Time{}
	if err := store.Finish(t.Context(), current, current.Version); err != nil {
		t.Fatalf("Finish() error = %v", err)
	}
	history, err := store.ListCheckpoints(t.Context(), workflow.CheckpointHistoryRequest{RunID: current.RunID})
	if err != nil || len(history) != 3 {
		t.Fatalf("ListCheckpoints() = %d records, %v, want 3", len(history), err)
	}
}

func TestStoreRejectsUnboundedRowsAndNonPortableIDs(t *testing.T) {
	store, err := Open(sqlite.Open(filepath.Join(t.TempDir(), "limits.db")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	tooLong := testCheckpoint(strings.Repeat("r", maxIndexedIDBytes+1))
	if err := store.Create(t.Context(), tooLong); !errors.Is(err, workflow.ErrInvalidCheckpoint) {
		t.Fatalf("Create(long run id) error = %v, want ErrInvalidCheckpoint", err)
	}
	trailingSpace := testCheckpoint("run-with-space ")
	if err := store.Create(t.Context(), trailingSpace); !errors.Is(err, workflow.ErrInvalidCheckpoint) {
		t.Fatalf("Create(trailing space) error = %v, want ErrInvalidCheckpoint", err)
	}
	for _, runID := range []string{"run-with-nul\x00", "\xff"} {
		invalid := testCheckpoint(runID)
		if err := store.Create(t.Context(), invalid); !errors.Is(err, workflow.ErrInvalidCheckpoint) {
			t.Fatalf("Create(non-portable run id) error = %v, want ErrInvalidCheckpoint", err)
		}
	}
	event := runlog.Record{
		SessionID: "session-1", RunID: "large-event", Sequence: 1, EventType: "test.event", Source: "test",
		Timestamp: time.Now().UTC(), Payload: []byte(`"` + strings.Repeat("x", maxRunLogBytes) + `"`),
	}
	if err := store.Append(t.Context(), event); !errors.Is(err, runlog.ErrInvalidRecord) {
		t.Fatalf("Append(large event) error = %v, want ErrInvalidRecord", err)
	}
}

func TestStoreBuildsLeaseExpiryFromDatabaseTime(t *testing.T) {
	store, err := Open(sqlite.Open(filepath.Join(t.TempDir(), "database-clock.db")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	record := testCheckpoint("database-clock")
	record.LeaseExpiresAt = time.Unix(1, 0)
	record.LeaseDuration = 2 * time.Minute
	beforeCreate, err := store.databaseNow(store.db)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Create(t.Context(), record); err != nil {
		t.Fatalf("Create() with TTL error = %v", err)
	}
	afterCreate, err := store.databaseNow(store.db)
	if err != nil {
		t.Fatal(err)
	}
	current, err := store.Load(t.Context(), record.RunID)
	if err != nil {
		t.Fatal(err)
	}
	assertLeaseBetween(t, current.LeaseExpiresAt, beforeCreate.Add(2*time.Minute), afterCreate.Add(2*time.Minute))

	beforeRenew, err := store.databaseNow(store.db)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RenewLease(t.Context(), workflow.CheckpointLease{
		RunID: current.RunID, OwnerID: current.OwnerID, ClaimSequence: current.ClaimSequence,
		ExpiresAt: time.Unix(1, 0), Duration: 3 * time.Minute,
	}); err != nil {
		t.Fatalf("RenewLease() with TTL error = %v", err)
	}
	afterRenew, err := store.databaseNow(store.db)
	if err != nil {
		t.Fatal(err)
	}
	current, err = store.Load(t.Context(), record.RunID)
	if err != nil {
		t.Fatal(err)
	}
	assertLeaseBetween(t, current.LeaseExpiresAt, beforeRenew.Add(3*time.Minute), afterRenew.Add(3*time.Minute))
	renewedExpiry := current.LeaseExpiresAt

	current.Payload = []byte(`{"state":"save-must-not-trust-client-expiry"}`)
	current.LeaseExpiresAt = time.Now().Add(24 * time.Hour)
	current.LeaseDuration = 0
	if err := store.Save(t.Context(), current, current.Version); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	current, err = store.Load(t.Context(), record.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if !current.LeaseExpiresAt.Equal(renewedExpiry) {
		t.Fatalf("Save() changed authoritative lease from %v to %v", renewedExpiry, current.LeaseExpiresAt)
	}
}

func assertLeaseBetween(t *testing.T, actual, earliest, latest time.Time) {
	t.Helper()
	tolerance := 10 * time.Millisecond
	if actual.Before(earliest.Add(-tolerance)) || actual.After(latest.Add(tolerance)) {
		t.Fatalf("lease expiry %v is outside database-time interval [%v, %v]", actual, earliest, latest)
	}
}

type sqliteCodeError int

func (err sqliteCodeError) Error() string { return "opaque sqlite error" }
func (err sqliteCodeError) Code() int     { return int(err) }

func TestSQLiteConcurrencyErrorUsesResultCode(t *testing.T) {
	store := &Store{dialect: "sqlite"}
	for _, code := range []int{5, 5 | 2<<8, 6, 6 | 1<<8} {
		if !store.concurrencyError(fmt.Errorf("wrapped: %w", sqliteCodeError(code))) {
			t.Fatalf("concurrencyError(code %d) = false", code)
		}
	}
	if store.concurrencyError(sqliteCodeError(19)) {
		t.Fatal("constraint error classified as concurrency error")
	}
}

func testCheckpoint(runID string) workflow.CheckpointRecord {
	now := time.Now().UTC()
	return workflow.CheckpointRecord{
		ID: "checkpoint:" + runID, SessionID: "session-1", RunID: runID, WorkflowName: "workflow",
		TopologyVersion: "v1", SchemaVersion: 2, Version: 1, Status: workflow.CheckpointRunning,
		Payload: []byte(`{"state":"running"}`), ReplayStatus: workflow.ReplayUnknown,
		OwnerID: "owner-1", ClaimSequence: 1, LeaseExpiresAt: now.Add(time.Minute),
		CreatedAt: now, UpdatedAt: now,
	}
}
