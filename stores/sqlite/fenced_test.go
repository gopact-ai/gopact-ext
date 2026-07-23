package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact/runlog"
	"github.com/gopact-ai/gopact/workflow"
)

var _ runlog.FencedLog = (*Store)(nil)

func TestStoreAppendFencedRejectsInvalidFence(t *testing.T) {
	tests := []struct {
		name  string
		fence runlog.Fence
	}{
		{name: "missing owner", fence: runlog.Fence{ClaimSequence: 1}},
		{name: "missing claim sequence", fence: runlog.Fence{OwnerID: "owner-1"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := openTestStore(t)
			record := sqliteRunRecord("session-1", "invalid-fence", 1, "audit.custom")
			if err := store.AppendFenced(t.Context(), record, test.fence); !errors.Is(err, workflow.ErrInvalidCheckpoint) {
				t.Fatalf("AppendFenced() error = %v, want ErrInvalidCheckpoint", err)
			}
		})
	}
}

func TestCombinedSQLiteStoreAvoidsObservedEventCheckpointAmplification(t *testing.T) {
	want := runCombinedStoreWorkflow(t, workflow.NewMemoryStore(), "memory-run")
	got := runCombinedStoreWorkflow(t, openTestStore(t), "sqlite-run")
	if got != want {
		t.Fatalf("SQLite checkpoint history = %d, want %d without observed-event amplification", got, want)
	}
}

func runCombinedStoreWorkflow(t *testing.T, store workflow.Store, runID string) int {
	t.Helper()
	wf := workflow.New[string, string](
		"fenced-history",
		workflow.WithStore(store),
	)
	node := wf.Node("work", func(ctx context.Context, input string) (string, error) {
		if err := workflow.Emit(ctx, gopact.Event{Type: "audit.custom"}); err != nil {
			return "", err
		}
		return input, nil
	})
	wf.Entry(node)
	wf.Exit(node)
	if _, err := wf.Invoke(t.Context(), "input", gopact.WithRunID(runID)); err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	history, err := store.ListCheckpoints(t.Context(), workflow.CheckpointHistoryRequest{RunID: runID})
	if err != nil {
		t.Fatalf("ListCheckpoints() error = %v", err)
	}
	return len(history)
}

func TestSQLiteWritesRejectExpiredLease(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name  string
		write func(context.Context, *Store, workflow.CheckpointRecord) error
	}{
		{
			name: "save",
			write: func(ctx context.Context, store *Store, record workflow.CheckpointRecord) error {
				record.LeaseExpiresAt = now.Add(time.Hour)
				return store.Save(ctx, record, record.Version)
			},
		},
		{
			name: "finish",
			write: func(ctx context.Context, store *Store, record workflow.CheckpointRecord) error {
				record.Status = workflow.CheckpointCompleted
				record.OwnerID = ""
				record.LeaseExpiresAt = time.Time{}
				return store.Finish(ctx, record, record.Version)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := openTestStore(t)
			record := leaseCheckpoint("expired-write")
			record.LeaseExpiresAt = now.Add(-time.Hour)
			if err := store.Create(t.Context(), record); err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			if err := test.write(t.Context(), store, record); !errors.Is(err, workflow.ErrCheckpointLeaseLost) {
				t.Fatalf("write error = %v, want ErrCheckpointLeaseLost", err)
			}
			history, err := store.ListCheckpoints(t.Context(), workflow.CheckpointHistoryRequest{RunID: record.RunID})
			if err != nil {
				t.Fatalf("ListCheckpoints() error = %v", err)
			}
			if len(history) != 1 {
				t.Fatalf("checkpoint history = %d, want unchanged version", len(history))
			}
		})
	}
}

func TestSQLiteStaleOwnerWritesAfterClaimReturnLeaseLost(t *testing.T) {
	tests := []struct {
		name  string
		write func(context.Context, *Store, workflow.CheckpointRecord) error
	}{
		{
			name: "save",
			write: func(ctx context.Context, store *Store, record workflow.CheckpointRecord) error {
				record.LeaseExpiresAt = time.Now().Add(time.Hour)
				return store.Save(ctx, record, record.Version)
			},
		},
		{
			name: "finish",
			write: func(ctx context.Context, store *Store, record workflow.CheckpointRecord) error {
				record.Status = workflow.CheckpointCompleted
				record.OwnerID = ""
				record.LeaseExpiresAt = time.Time{}
				return store.Finish(ctx, record, record.Version)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "stale-owner.db")
			oldOwner := openTestStoreAt(t, path)
			newOwner := openTestStoreAt(t, path)
			record := leaseCheckpoint("stale-owner-write")
			record.LeaseExpiresAt = time.Now().Add(-time.Hour)
			if err := oldOwner.Create(t.Context(), record); err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			stale, err := oldOwner.Load(t.Context(), record.RunID)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			claimed := stale
			claimed.OwnerID = "owner-2"
			claimed.ClaimSequence++
			claimed.LeaseExpiresAt = time.Now().Add(time.Hour)
			if err := newOwner.Claim(t.Context(), claimed, claimed.Version); err != nil {
				t.Fatalf("Claim() error = %v", err)
			}
			if err := test.write(t.Context(), oldOwner, stale); !errors.Is(err, workflow.ErrCheckpointLeaseLost) {
				t.Fatalf("stale write error = %v, want ErrCheckpointLeaseLost", err)
			}
			loaded, err := oldOwner.Load(t.Context(), record.RunID)
			if err != nil {
				t.Fatalf("Load() after stale write error = %v", err)
			}
			if loaded.Version != record.Version+1 || loaded.OwnerID != claimed.OwnerID ||
				loaded.ClaimSequence != claimed.ClaimSequence {
				t.Fatalf("Load() = %+v, want claimed head", loaded)
			}
		})
	}
}

func TestSQLiteStaleOwnerWriteAfterNewOwnerFinishesReturnsLeaseLost(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stale-owner-terminal.db")
	oldOwner := openTestStoreAt(t, path)
	newOwner := openTestStoreAt(t, path)
	record := leaseCheckpoint("stale-owner-terminal")
	record.LeaseExpiresAt = time.Now().Add(-time.Hour)
	if err := oldOwner.Create(t.Context(), record); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	stale, err := oldOwner.Load(t.Context(), record.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	claimed := stale
	claimed.OwnerID = "owner-2"
	claimed.ClaimSequence++
	claimed.LeaseExpiresAt = time.Now().Add(time.Hour)
	if err := newOwner.Claim(t.Context(), claimed, claimed.Version); err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	claimed.Version++
	claimed.Status = workflow.CheckpointCompleted
	claimed.OwnerID = ""
	claimed.LeaseExpiresAt = time.Time{}
	if err := newOwner.Finish(t.Context(), claimed, claimed.Version); err != nil {
		t.Fatalf("Finish() error = %v", err)
	}
	stale.LeaseExpiresAt = time.Now().Add(time.Hour)
	if err := oldOwner.Save(t.Context(), stale, stale.Version); !errors.Is(err, workflow.ErrCheckpointLeaseLost) {
		t.Fatalf("stale Save() error = %v, want ErrCheckpointLeaseLost", err)
	}
}

func TestSQLiteSameClaimVersionRaceRemainsConflict(t *testing.T) {
	tests := []struct {
		name  string
		write func(context.Context, *Store, workflow.CheckpointRecord) error
	}{
		{
			name: "save",
			write: func(ctx context.Context, store *Store, record workflow.CheckpointRecord) error {
				record.ConfirmedSequence = 2
				return store.Save(ctx, record, record.Version)
			},
		},
		{
			name: "finish",
			write: func(ctx context.Context, store *Store, record workflow.CheckpointRecord) error {
				record.Status = workflow.CheckpointCompleted
				record.OwnerID = ""
				record.LeaseExpiresAt = time.Time{}
				return store.Finish(ctx, record, record.Version)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "same-claim.db")
			first := openTestStoreAt(t, path)
			second := openTestStoreAt(t, path)
			record := leaseCheckpoint("same-claim-write")
			record.LeaseExpiresAt = time.Now().Add(time.Hour)
			if err := first.Create(t.Context(), record); err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			stale, err := first.Load(t.Context(), record.RunID)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			winner := stale
			winner.ConfirmedSequence = 1
			if err := second.Save(t.Context(), winner, winner.Version); err != nil {
				t.Fatalf("winning Save() error = %v", err)
			}
			if err := test.write(t.Context(), first, stale); !errors.Is(err, workflow.ErrCheckpointConflict) {
				t.Fatalf("stale write error = %v, want ErrCheckpointConflict", err)
			}
		})
	}
}

func TestSQLiteOwnerlessLegacyWritesRemainSupported(t *testing.T) {
	tests := []struct {
		name  string
		write func(context.Context, *Store, workflow.CheckpointRecord) error
	}{
		{
			name: "save",
			write: func(ctx context.Context, store *Store, record workflow.CheckpointRecord) error {
				record.ConfirmedSequence = 1
				return store.Save(ctx, record, record.Version)
			},
		},
		{
			name: "finish",
			write: func(ctx context.Context, store *Store, record workflow.CheckpointRecord) error {
				record.Status = workflow.CheckpointCompleted
				return store.Finish(ctx, record, record.Version)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := openTestStore(t)
			record := leaseCheckpoint("ownerless-legacy")
			record.OwnerID = ""
			record.ClaimSequence = 0
			record.LeaseExpiresAt = time.Time{}
			if err := store.Create(t.Context(), record); err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			if err := test.write(t.Context(), store, record); err != nil {
				t.Fatalf("write error = %v", err)
			}
			loaded, err := store.Load(t.Context(), record.RunID)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if loaded.Version != record.Version+1 || loaded.OwnerID != "" || loaded.ClaimSequence != 0 {
				t.Fatalf("Load() = %+v, want ownerless legacy version %d", loaded, record.Version+1)
			}
		})
	}
}
