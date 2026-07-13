package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/gopact-ai/gopact/runlog"
	"github.com/gopact-ai/gopact/workflow"
	_ "modernc.org/sqlite"
)

func TestPurgeTerminalRuns(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	before := time.Date(2026, time.July, 13, 12, 0, 0, 0, time.UTC)
	old := before.Add(-time.Hour)
	fresh := before.Add(time.Hour)

	terminal := map[string]workflow.CheckpointStatus{
		"completed":  workflow.CheckpointCompleted,
		"failed":     workflow.CheckpointFailed,
		"canceled":   workflow.CheckpointCanceled,
		"terminated": workflow.CheckpointTerminated,
	}
	for runID, status := range terminal {
		createTerminalRun(t, store, runID, status, old)
	}
	createTerminalRun(t, store, "fresh-terminal", workflow.CheckpointCompleted, fresh)
	createTerminalRun(t, store, "boundary-terminal", workflow.CheckpointCompleted, before)
	createRunningRun(t, store, "old-running", workflow.CheckpointRunning, old)
	createRunningRun(t, store, "old-interrupted", workflow.CheckpointInterrupted, old)

	result, err := store.PurgeTerminalRuns(ctx, PurgeRequest{Before: before})
	if err != nil {
		t.Fatalf("PurgeTerminalRuns() error = %v", err)
	}
	if result != (PurgeResult{Runs: 4, Checkpoints: 8, Events: 4}) {
		t.Fatalf("PurgeTerminalRuns() = %+v, want 4 runs, 8 checkpoints, and 4 events", result)
	}
	for runID := range terminal {
		assertRunPurged(t, store, runID)
	}
	for _, runID := range []string{"fresh-terminal", "boundary-terminal", "old-running", "old-interrupted"} {
		if _, err := store.Load(ctx, runID); err != nil {
			t.Fatalf("Load(%q) error = %v, want retained run", runID, err)
		}
		records, err := store.List(ctx, runlog.Query{RunID: runID})
		if err != nil || len(records) != 1 {
			t.Fatalf("List(%q) = %+v, %v, want one retained event", runID, records, err)
		}
	}
}

func TestPurgeTerminalRunsHonorsLimitAcrossBatches(t *testing.T) {
	t.Run("explicit limit converges", func(t *testing.T) {
		store := openTestStore(t)
		before := time.Date(2026, time.July, 13, 12, 0, 0, 0, time.UTC)
		for index := range 3 {
			createTerminalRun(
				t,
				store,
				fmt.Sprintf("batch-%d", index),
				workflow.CheckpointCompleted,
				before.Add(-time.Duration(3-index)*time.Minute),
			)
		}

		first, err := store.PurgeTerminalRuns(context.Background(), PurgeRequest{Before: before, Limit: 2})
		if err != nil {
			t.Fatalf("first PurgeTerminalRuns() error = %v", err)
		}
		if first != (PurgeResult{Runs: 2, Checkpoints: 4, Events: 2}) {
			t.Fatalf("first PurgeTerminalRuns() = %+v", first)
		}
		second, err := store.PurgeTerminalRuns(context.Background(), PurgeRequest{Before: before, Limit: 2})
		if err != nil {
			t.Fatalf("second PurgeTerminalRuns() error = %v", err)
		}
		if second != (PurgeResult{Runs: 1, Checkpoints: 2, Events: 1}) {
			t.Fatalf("second PurgeTerminalRuns() = %+v", second)
		}
		third, err := store.PurgeTerminalRuns(context.Background(), PurgeRequest{Before: before, Limit: 2})
		if err != nil {
			t.Fatalf("third PurgeTerminalRuns() error = %v", err)
		}
		if third != (PurgeResult{}) {
			t.Fatalf("third PurgeTerminalRuns() = %+v, want empty result", third)
		}
	})

	t.Run("zero limit defaults to one hundred", func(t *testing.T) {
		store := openTestStore(t)
		before := time.Date(2026, time.July, 13, 12, 0, 0, 0, time.UTC)
		for index := range 101 {
			createTerminalRun(
				t,
				store,
				fmt.Sprintf("default-%03d", index),
				workflow.CheckpointCompleted,
				before.Add(-time.Hour),
			)
		}

		first, err := store.PurgeTerminalRuns(context.Background(), PurgeRequest{Before: before})
		if err != nil {
			t.Fatalf("first PurgeTerminalRuns() error = %v", err)
		}
		if first != (PurgeResult{Runs: 100, Checkpoints: 200, Events: 100}) {
			t.Fatalf("first PurgeTerminalRuns() = %+v, want default batch of 100", first)
		}
		second, err := store.PurgeTerminalRuns(context.Background(), PurgeRequest{Before: before})
		if err != nil {
			t.Fatalf("second PurgeTerminalRuns() error = %v", err)
		}
		if second != (PurgeResult{Runs: 1, Checkpoints: 2, Events: 1}) {
			t.Fatalf("second PurgeTerminalRuns() = %+v", second)
		}
	})
}

func TestPurgeTerminalRunsSkipsStaleRunHead(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	before := time.Date(2026, time.July, 13, 12, 0, 0, 0, time.UTC)
	createTerminalRun(t, store, "stale-head", workflow.CheckpointCompleted, before.Add(-time.Hour))

	latest, err := store.Load(ctx, "stale-head")
	if err != nil {
		t.Fatal(err)
	}
	latest.Version++
	latest.Status = workflow.CheckpointRunning
	latest.UpdatedAt = before.Add(time.Hour)
	if err := insertCheckpoint(ctx, store.db, latest); err != nil {
		t.Fatalf("insert checkpoint without run-head update: %v", err)
	}

	result, err := store.PurgeTerminalRuns(ctx, PurgeRequest{Before: before})
	if err != nil {
		t.Fatalf("PurgeTerminalRuns() error = %v", err)
	}
	if result != (PurgeResult{}) {
		t.Fatalf("PurgeTerminalRuns() = %+v, want stale head to be skipped", result)
	}
	loaded, err := store.Load(ctx, "stale-head")
	if err != nil || loaded.Status != workflow.CheckpointRunning || loaded.Version != latest.Version {
		t.Fatalf("Load() = %+v, %v, want newer running checkpoint", loaded, err)
	}
	assertRunStorageCounts(t, store, "stale-head", 3, 1, 1)
}

func TestPurgeTerminalRunsFailsClosedOnMismatchedRunHead(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	before := time.Date(2026, time.July, 13, 12, 0, 0, 0, time.UTC)
	createTerminalRun(t, store, "mismatched-head", workflow.CheckpointCompleted, before.Add(-time.Hour))

	latest, err := store.Load(ctx, "mismatched-head")
	if err != nil {
		t.Fatal(err)
	}
	latest.Status = workflow.CheckpointRunning
	metadata, err := encodeCheckpointMetadata(latest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(
		ctx,
		`UPDATE gopact_workflow_checkpoints SET record_json = ? WHERE run_id = ? AND version = ?`,
		metadata,
		latest.RunID,
		latest.Version,
	); err != nil {
		t.Fatalf("corrupt latest checkpoint metadata: %v", err)
	}

	if _, err := store.PurgeTerminalRuns(ctx, PurgeRequest{Before: before}); err == nil {
		t.Fatal("PurgeTerminalRuns() error = nil, want mismatched head error")
	}
	assertRunStorageCounts(t, store, "mismatched-head", 2, 1, 1)
}

func TestPurgeTerminalRunsValidatesRequestAndCancellation(t *testing.T) {
	store := openTestStore(t)
	before := time.Now().UTC()

	for _, request := range []PurgeRequest{
		{},
		{Before: before, Limit: -1},
		{Before: before, Limit: 1001},
	} {
		if _, err := store.PurgeTerminalRuns(context.Background(), request); err == nil {
			t.Fatalf("PurgeTerminalRuns(%+v) error = nil, want validation error", request)
		}
	}

	createTerminalRun(t, store, "canceled-purge", workflow.CheckpointCompleted, before.Add(-time.Hour))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.PurgeTerminalRuns(ctx, PurgeRequest{Before: before}); !errors.Is(err, context.Canceled) {
		t.Fatalf("PurgeTerminalRuns() error = %v, want context.Canceled", err)
	}
	if _, err := store.Load(context.Background(), "canceled-purge"); err != nil {
		t.Fatalf("Load() error = %v, want canceled purge to leave the run intact", err)
	}
}

func TestRunHeadTracksCheckpointWritesAndIgnoresLeaseRenewal(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	base := time.Date(2026, time.July, 13, 8, 0, 0, 0, time.UTC)
	record := retentionCheckpoint("head-sync", workflow.CheckpointRunning, base)
	record.LeaseExpiresAt = time.Now().Add(time.Hour)
	if err := store.Create(ctx, record); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	assertRunHead(t, store, record.RunID, workflow.CheckpointRunning, 1, base)

	if err := store.RenewLease(ctx, workflow.CheckpointLease{
		RunID: record.RunID, OwnerID: record.OwnerID, ClaimSequence: record.ClaimSequence, ExpiresAt: time.Now().Add(2 * time.Hour),
	}); err != nil {
		t.Fatalf("RenewLease() error = %v", err)
	}
	assertRunHead(t, store, record.RunID, workflow.CheckpointRunning, 1, base)

	current, err := store.Load(ctx, record.RunID)
	if err != nil {
		t.Fatal(err)
	}
	current.Status = workflow.CheckpointInterrupted
	current.OwnerID = ""
	current.LeaseExpiresAt = time.Time{}
	current.UpdatedAt = base.Add(time.Minute)
	if err := store.Save(ctx, current, current.Version); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	assertRunHead(t, store, record.RunID, workflow.CheckpointInterrupted, 2, base.Add(time.Minute))

	current, err = store.Load(ctx, record.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if current.OwnerID != "" || !current.LeaseExpiresAt.IsZero() {
		t.Fatalf("Load() = owner %q expiry %v, want cleared interrupted lease", current.OwnerID, current.LeaseExpiresAt)
	}
	current.Status = workflow.CheckpointRunning
	current.OwnerID = "owner-2"
	current.ClaimSequence++
	current.LeaseExpiresAt = time.Now().Add(time.Hour)
	current.UpdatedAt = base.Add(2 * time.Minute)
	if err := store.Claim(ctx, current, current.Version); err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	assertRunHead(t, store, record.RunID, workflow.CheckpointRunning, 3, base.Add(2*time.Minute))

	current, err = store.Load(ctx, record.RunID)
	if err != nil {
		t.Fatal(err)
	}
	current.Status = workflow.CheckpointCompleted
	current.OwnerID = ""
	current.LeaseExpiresAt = time.Time{}
	current.UpdatedAt = base.Add(3 * time.Minute)
	if err := store.Finish(ctx, current, current.Version); err != nil {
		t.Fatalf("Finish() error = %v", err)
	}
	assertRunHead(t, store, record.RunID, workflow.CheckpointCompleted, 4, base.Add(3*time.Minute))

	current, err = store.Load(ctx, record.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if current.OwnerID != "" || !current.LeaseExpiresAt.IsZero() {
		t.Fatalf("Load() = owner %q expiry %v, want cleared terminal lease", current.OwnerID, current.LeaseExpiresAt)
	}
	current.Status = workflow.CheckpointRunning
	current.UpdatedAt = base.Add(4 * time.Minute)
	if err := store.Reopen(ctx, current, current.Version); err != nil {
		t.Fatalf("Reopen() error = %v", err)
	}
	assertRunHead(t, store, record.RunID, workflow.CheckpointRunning, 5, base.Add(4*time.Minute))
}

func TestRunHeadUpsertRequiresCurrentCheckpointSource(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	record := retentionCheckpoint("missing-source", workflow.CheckpointCompleted, time.Now().UTC())
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := upsertRunHead(ctx, tx, record)
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("upsertRunHead() error = %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if updated {
		t.Fatal("upsertRunHead() updated a head without its checkpoint source")
	}
	assertRunStorageCounts(t, store, record.RunID, 0, 0, 0)
}

func TestOpenBackfillsRunHeadsInBatches(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-checkpoints.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE gopact_workflow_checkpoints (
		run_id TEXT NOT NULL,
		version INTEGER NOT NULL,
		record_json BLOB NOT NULL,
		payload BLOB NOT NULL,
		PRIMARY KEY (run_id, version)
	)`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	base := time.Date(2026, time.July, 13, 8, 0, 0, 0, time.UTC)
	for index := range 257 {
		record := retentionCheckpoint(fmt.Sprintf("legacy-%03d", index), workflow.CheckpointRunning, base)
		if err := insertCheckpoint(ctx, db, record); err != nil {
			t.Fatalf("insert legacy checkpoint %d: %v", index, err)
		}
		if index == 0 {
			record.Version = 2
			record.Status = workflow.CheckpointCompleted
			record.UpdatedAt = base.Add(time.Hour)
			if err := insertCheckpoint(ctx, db, record); err != nil {
				t.Fatalf("insert latest legacy checkpoint: %v", err)
			}
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	assertRunHead(t, store, "legacy-000", workflow.CheckpointCompleted, 2, base.Add(time.Hour))
	if count := countRows(t, store.db, "gopact_workflow_runs"); count != 257 {
		t.Fatalf("run head count = %d, want 257", count)
	}
	var retentionIndexes int
	if err := store.db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'gopact_workflow_runs_retention'`,
	).Scan(&retentionIndexes); err != nil {
		t.Fatalf("inspect run retention index: %v", err)
	}
	if retentionIndexes != 1 {
		t.Fatal("run retention index was not added")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store, err = Open(path)
	if err != nil {
		t.Fatalf("second Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if count := countRows(t, store.db, "gopact_workflow_runs"); count != 257 {
		t.Fatalf("run head count after repeated migration = %d, want 257", count)
	}
}

func TestOpenDoesNotRewriteCurrentRunHeads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "current-heads.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	record := retentionCheckpoint("current-head", workflow.CheckpointRunning, time.Now().UTC())
	if err := store.Create(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`CREATE TRIGGER reject_current_run_head_rewrite
		BEFORE INSERT ON gopact_workflow_runs
		BEGIN
			SELECT RAISE(ABORT, 'current run head must not be rewritten');
		END`); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store, err = Open(path)
	if err != nil {
		t.Fatalf("Open() rewrote an already-current run head: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	assertRunHead(t, store, record.RunID, record.Status, record.Version, record.UpdatedAt)
}

func TestPurgeTerminalRunsRacingReopenLeavesNoPartialRun(t *testing.T) {
	for iteration := range 20 {
		path := filepath.Join(t.TempDir(), fmt.Sprintf("race-%d.db", iteration))
		purger, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		writer, err := Open(path)
		if err != nil {
			_ = purger.Close()
			t.Fatal(err)
		}
		before := time.Now().UTC()
		createTerminalRun(t, purger, "race", workflow.CheckpointCompleted, before.Add(-time.Hour))
		terminal, err := writer.Load(context.Background(), "race")
		if err != nil {
			t.Fatal(err)
		}
		reopened := terminal
		reopened.Status = workflow.CheckpointRunning
		reopened.UpdatedAt = before.Add(time.Hour)

		start := make(chan struct{})
		var wait sync.WaitGroup
		wait.Add(2)
		var purgeResult PurgeResult
		var purgeErr, reopenErr error
		go func() {
			defer wait.Done()
			<-start
			purgeResult, purgeErr = purger.PurgeTerminalRuns(context.Background(), PurgeRequest{Before: before})
		}()
		go func() {
			defer wait.Done()
			<-start
			reopenErr = writer.Reopen(context.Background(), reopened, terminal.Version)
		}()
		close(start)
		wait.Wait()

		loaded, loadErr := purger.Load(context.Background(), "race")
		switch {
		case loadErr == nil:
			if loaded.Status != workflow.CheckpointRunning || loaded.Version != terminal.Version+1 {
				t.Fatalf("iteration %d: Load() = %+v after race", iteration, loaded)
			}
			if reopenErr != nil {
				t.Fatalf("iteration %d: retained run but Reopen() error = %v", iteration, reopenErr)
			}
			if purgeResult.Runs != 0 {
				t.Fatalf("iteration %d: retained run but purge result = %+v", iteration, purgeResult)
			}
			assertRunStorageCounts(t, purger, "race", 3, 1, 1)
		case errors.Is(loadErr, workflow.ErrCheckpointNotFound):
			if purgeErr != nil || purgeResult != (PurgeResult{Runs: 1, Checkpoints: 2, Events: 1}) {
				t.Fatalf("iteration %d: purged run result = %+v, error = %v", iteration, purgeResult, purgeErr)
			}
			if reopenErr == nil {
				t.Fatalf("iteration %d: Reopen() succeeded after the run was purged", iteration)
			}
			assertRunStorageCounts(t, purger, "race", 0, 0, 0)
		default:
			t.Fatalf("iteration %d: Load() error = %v", iteration, loadErr)
		}
		if purgeErr != nil && !databaseBusy(purgeErr) {
			t.Fatalf("iteration %d: PurgeTerminalRuns() error = %v", iteration, purgeErr)
		}
		_ = writer.Close()
		_ = purger.Close()
	}
}

func TestPurgeTerminalRunsRacingClaimRetainsInterruptedRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claim-race.db")
	purger, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = purger.Close() })
	claimer, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = claimer.Close() })

	before := time.Now().UTC()
	createRunningRun(t, purger, "claim-race", workflow.CheckpointInterrupted, before.Add(-time.Hour))
	candidate, err := claimer.Load(context.Background(), "claim-race")
	if err != nil {
		t.Fatal(err)
	}
	candidate.Status = workflow.CheckpointRunning
	candidate.OwnerID = "owner-2"
	candidate.ClaimSequence++
	candidate.LeaseExpiresAt = before.Add(time.Hour)
	candidate.UpdatedAt = before.Add(time.Minute)

	start := make(chan struct{})
	var wait sync.WaitGroup
	wait.Add(2)
	var purgeResult PurgeResult
	var purgeErr, claimErr error
	go func() {
		defer wait.Done()
		<-start
		purgeResult, purgeErr = purger.PurgeTerminalRuns(context.Background(), PurgeRequest{Before: before})
	}()
	go func() {
		defer wait.Done()
		<-start
		claimErr = claimer.Claim(context.Background(), candidate, candidate.Version)
	}()
	close(start)
	wait.Wait()

	if purgeErr != nil || purgeResult != (PurgeResult{}) {
		t.Fatalf("PurgeTerminalRuns() = %+v, %v, want interrupted run retained", purgeResult, purgeErr)
	}
	if claimErr != nil {
		t.Fatalf("Claim() error = %v", claimErr)
	}
	loaded, err := purger.Load(context.Background(), "claim-race")
	if err != nil || loaded.Status != workflow.CheckpointRunning || loaded.Version != candidate.Version+1 {
		t.Fatalf("Load() = %+v, %v, want claimed running checkpoint", loaded, err)
	}
	assertRunStorageCounts(t, purger, "claim-race", 3, 1, 1)
}

func createTerminalRun(t *testing.T, store *Store, runID string, status workflow.CheckpointStatus, updatedAt time.Time) {
	t.Helper()
	record := retentionCheckpoint(runID, workflow.CheckpointRunning, updatedAt.Add(-time.Minute))
	record.OwnerID = ""
	record.ClaimSequence = 0
	record.LeaseExpiresAt = time.Time{}
	if err := store.Create(context.Background(), record); err != nil {
		t.Fatalf("Create(%q) error = %v", runID, err)
	}
	appendRetentionEvent(t, store, record)
	record.Status = status
	record.UpdatedAt = updatedAt
	if err := store.Finish(context.Background(), record, record.Version); err != nil {
		t.Fatalf("Finish(%q) error = %v", runID, err)
	}
}

func createRunningRun(t *testing.T, store *Store, runID string, status workflow.CheckpointStatus, updatedAt time.Time) {
	t.Helper()
	record := retentionCheckpoint(runID, workflow.CheckpointRunning, updatedAt)
	record.OwnerID = ""
	record.ClaimSequence = 0
	record.LeaseExpiresAt = time.Time{}
	if err := store.Create(context.Background(), record); err != nil {
		t.Fatalf("Create(%q) error = %v", runID, err)
	}
	appendRetentionEvent(t, store, record)
	if status == workflow.CheckpointInterrupted {
		record.Status = status
		if err := store.Save(context.Background(), record, record.Version); err != nil {
			t.Fatalf("Save(%q) error = %v", runID, err)
		}
	}
}

func retentionCheckpoint(runID string, status workflow.CheckpointStatus, updatedAt time.Time) workflow.CheckpointRecord {
	return workflow.CheckpointRecord{
		ID: "checkpoint:" + runID, SessionID: "session-retention", RunID: runID, WorkflowName: "retention",
		TopologyVersion: "v1", SchemaVersion: 2, Version: 1, Status: status,
		Payload: []byte(`{"state":"retention"}`), ReplayStatus: workflow.ReplayUnknown,
		OwnerID: "owner-1", ClaimSequence: 1, LeaseExpiresAt: updatedAt.Add(time.Minute),
		CreatedAt: updatedAt.Add(-time.Hour), UpdatedAt: updatedAt,
	}
}

func appendRetentionEvent(t *testing.T, store *Store, checkpoint workflow.CheckpointRecord) {
	t.Helper()
	record := runlog.Record{
		SessionID: checkpoint.SessionID,
		RunID:     checkpoint.RunID,
		Sequence:  1,
		EventType: workflow.EventWorkflowStarted,
		Source:    "retention-test",
		Timestamp: checkpoint.CreatedAt,
	}
	if err := store.Append(context.Background(), record); err != nil {
		t.Fatalf("Append(%q) error = %v", checkpoint.RunID, err)
	}
}

func assertRunPurged(t *testing.T, store *Store, runID string) {
	t.Helper()
	if _, err := store.Load(context.Background(), runID); !errors.Is(err, workflow.ErrCheckpointNotFound) {
		t.Fatalf("Load(%q) error = %v, want ErrCheckpointNotFound", runID, err)
	}
	records, err := store.List(context.Background(), runlog.Query{RunID: runID})
	if err != nil || len(records) != 0 {
		t.Fatalf("List(%q) = %+v, %v, want no events", runID, records, err)
	}
	assertRunStorageCounts(t, store, runID, 0, 0, 0)
}

func assertRunHead(
	t *testing.T,
	store *Store,
	runID string,
	status workflow.CheckpointStatus,
	version int64,
	updatedAt time.Time,
) {
	t.Helper()
	var gotStatus string
	var gotVersion, gotUpdatedAt int64
	err := store.db.QueryRow(
		`SELECT status, version, updated_at_unix_nano FROM gopact_workflow_runs WHERE run_id = ?`,
		runID,
	).Scan(&gotStatus, &gotVersion, &gotUpdatedAt)
	if err != nil {
		t.Fatalf("load run head %q: %v", runID, err)
	}
	if gotStatus != string(status) || gotVersion != version || gotUpdatedAt != updatedAt.UnixNano() {
		t.Fatalf(
			"run head %q = status %q, version %d, updated %d; want %q, %d, %d",
			runID, gotStatus, gotVersion, gotUpdatedAt, status, version, updatedAt.UnixNano(),
		)
	}
}

func assertRunStorageCounts(t *testing.T, store *Store, runID string, checkpoints, events, heads int) {
	t.Helper()
	queries := []struct {
		name  string
		query string
		want  int
	}{
		{"checkpoints", `SELECT COUNT(*) FROM gopact_workflow_checkpoints WHERE run_id = ?`, checkpoints},
		{"events", `SELECT COUNT(*) FROM gopact_runlog WHERE run_id = ?`, events},
		{"heads", `SELECT COUNT(*) FROM gopact_workflow_runs WHERE run_id = ?`, heads},
	}
	for _, query := range queries {
		var got int
		if err := store.db.QueryRow(query.query, runID).Scan(&got); err != nil {
			t.Fatalf("count %s for %q: %v", query.name, runID, err)
		}
		if got != query.want {
			t.Fatalf("%s count for %q = %d, want %d", query.name, runID, got, query.want)
		}
	}
}

func countRows(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}
