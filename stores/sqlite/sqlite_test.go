package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact/agent"
	"github.com/gopact-ai/gopact/runlog"
	"github.com/gopact-ai/gopact/workflow"
	sqlite3 "modernc.org/sqlite/lib"
)

type persistence interface {
	workflow.Checkpointer
	workflow.CheckpointHistory
	workflow.CheckpointController
	runlog.Log
}

func TestMigrateThenOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "explicit-migration.db")
	if store, err := Open(path); err == nil {
		_ = store.Close()
		t.Fatal("Open() succeeded before Migrate")
	}
	if err := Migrate(path); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open() after Migrate error = %v", err)
	}
	if store == nil || store.Store == nil {
		t.Fatal("Open() returned a nil Store")
	}
	_ = store.Close()
}

func TestContextEntryPointsRejectCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	path := filepath.Join(t.TempDir(), "canceled.db")

	if err := MigrateContext(ctx, path); !errors.Is(err, context.Canceled) {
		t.Fatalf("MigrateContext(canceled) error = %v, want context.Canceled", err)
	}
	if store, err := OpenContext(ctx, path); store != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("OpenContext(canceled) = %v, %v, want nil, context.Canceled", store, err)
	}
}

func TestWorkflowPersistenceConformance(t *testing.T) {
	t.Run("memory", func(t *testing.T) {
		store := workflow.NewMemoryStore()
		requireWorkflowPersistence(t, store, func(*testing.T) persistence { return store })
	})
	t.Run("sqlite", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "workflow.db")
		if err := Migrate(path); err != nil {
			t.Fatal(err)
		}
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

func TestStoreRenewLease(t *testing.T) {
	t.Run("updates latest record without a new version", func(t *testing.T) {
		store := openTestStore(t)
		record := leaseCheckpoint("renew-success")
		if err := store.Create(context.Background(), record); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		expiresAt := record.LeaseExpiresAt.Add(time.Minute)
		if err := store.RenewLease(context.Background(), workflow.CheckpointLease{
			RunID: record.RunID, OwnerID: record.OwnerID, ClaimSequence: record.ClaimSequence, ExpiresAt: expiresAt,
		}); err != nil {
			t.Fatalf("RenewLease() error = %v", err)
		}
		loaded, err := store.Load(context.Background(), record.RunID)
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if loaded.Version != record.Version || !loaded.LeaseExpiresAt.Equal(expiresAt) {
			t.Fatalf("Load() = %+v, want version %d and expiry %v", loaded, record.Version, expiresAt)
		}
		history, err := store.ListCheckpoints(context.Background(), workflow.CheckpointHistoryRequest{RunID: record.RunID})
		if err != nil || len(history) != 1 {
			t.Fatalf("ListCheckpoints() = %+v, %v, want one version", history, err)
		}
	})

	tests := []struct {
		name   string
		mutate func(*testing.T, *Store, *workflow.CheckpointRecord, *workflow.CheckpointLease)
	}{
		{
			name: "wrong owner",
			mutate: func(_ *testing.T, _ *Store, _ *workflow.CheckpointRecord, lease *workflow.CheckpointLease) {
				lease.OwnerID = "other-owner"
			},
		},
		{
			name: "wrong claim sequence",
			mutate: func(_ *testing.T, _ *Store, _ *workflow.CheckpointRecord, lease *workflow.CheckpointLease) {
				lease.ClaimSequence++
			},
		},
		{
			name: "terminal checkpoint",
			mutate: func(t *testing.T, store *Store, record *workflow.CheckpointRecord, _ *workflow.CheckpointLease) {
				t.Helper()
				record.Status = workflow.CheckpointCompleted
				if err := store.Finish(context.Background(), *record, record.Version); err != nil {
					t.Fatalf("Finish() error = %v", err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := openTestStore(t)
			record := leaseCheckpoint("renew-" + test.name)
			if err := store.Create(context.Background(), record); err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			lease := workflow.CheckpointLease{
				RunID: record.RunID, OwnerID: record.OwnerID, ClaimSequence: record.ClaimSequence,
				ExpiresAt: record.LeaseExpiresAt.Add(time.Minute),
			}
			test.mutate(t, store, &record, &lease)
			if err := store.RenewLease(context.Background(), lease); !errors.Is(err, workflow.ErrCheckpointLeaseLost) {
				t.Fatalf("RenewLease() error = %v, want ErrCheckpointLeaseLost", err)
			}
		})
	}

	t.Run("canceled context", func(t *testing.T) {
		store := openTestStore(t)
		record := leaseCheckpoint("renew-canceled")
		if err := store.Create(context.Background(), record); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := store.RenewLease(ctx, workflow.CheckpointLease{
			RunID: record.RunID, OwnerID: record.OwnerID, ClaimSequence: record.ClaimSequence,
			ExpiresAt: record.LeaseExpiresAt.Add(time.Minute),
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("RenewLease() error = %v, want context.Canceled", err)
		}
	})
}

func TestStoreRenewLeaseRejectsExpiredCurrentLease(t *testing.T) {
	store := openTestStore(t)
	record := leaseCheckpoint("renew-expired")
	record.LeaseExpiresAt = time.Now().Add(-time.Hour)
	if err := store.Create(t.Context(), record); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	err := store.RenewLease(t.Context(), workflow.CheckpointLease{
		RunID: record.RunID, OwnerID: record.OwnerID, ClaimSequence: record.ClaimSequence,
		ExpiresAt: time.Now().Add(time.Hour),
	})
	if !errors.Is(err, workflow.ErrCheckpointLeaseLost) {
		t.Fatalf("RenewLease() error = %v, want ErrCheckpointLeaseLost", err)
	}
	loaded, err := store.Load(t.Context(), record.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !loaded.LeaseExpiresAt.Equal(record.LeaseExpiresAt) {
		t.Fatalf("lease expiry = %v, want unchanged %v", loaded.LeaseExpiresAt, record.LeaseExpiresAt)
	}
}

func TestStoreRenewLeaseRejectsPastExpiry(t *testing.T) {
	store := openTestStore(t)
	record := leaseCheckpoint("renew-past")
	record.LeaseExpiresAt = time.Now().Add(time.Hour)
	if err := store.Create(t.Context(), record); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	err := store.RenewLease(t.Context(), workflow.CheckpointLease{
		RunID: record.RunID, OwnerID: record.OwnerID, ClaimSequence: record.ClaimSequence,
		ExpiresAt: time.Now().Add(-time.Minute),
	})
	if !errors.Is(err, workflow.ErrInvalidCheckpoint) {
		t.Fatalf("RenewLease() error = %v, want ErrInvalidCheckpoint", err)
	}
	loaded, err := store.Load(t.Context(), record.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !loaded.LeaseExpiresAt.Equal(record.LeaseExpiresAt) {
		t.Fatalf("lease expiry = %v, want unchanged %v", loaded.LeaseExpiresAt, record.LeaseExpiresAt)
	}
}

func TestStoreRenewLeaseDoesNotShortenExpiry(t *testing.T) {
	store := openTestStore(t)
	record := leaseCheckpoint("renew-shorter")
	record.LeaseExpiresAt = time.Now().Add(2 * time.Hour)
	if err := store.Create(t.Context(), record); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := store.RenewLease(t.Context(), workflow.CheckpointLease{
		RunID: record.RunID, OwnerID: record.OwnerID, ClaimSequence: record.ClaimSequence,
		ExpiresAt: record.LeaseExpiresAt.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("RenewLease() error = %v", err)
	}
	loaded, err := store.Load(t.Context(), record.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !loaded.LeaseExpiresAt.Equal(record.LeaseExpiresAt) {
		t.Fatalf("lease expiry = %v, want monotonic %v", loaded.LeaseExpiresAt, record.LeaseExpiresAt)
	}
}

func TestSQLiteExpiredRenewCannotBeatPendingClaim(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "renew-claim.db")
	renewer := openTestStoreAt(t, path)
	claimant := openTestStoreAt(t, path)
	record := leaseCheckpoint("renew-claim")
	record.LeaseExpiresAt = time.Now().Add(-time.Hour)
	if err := renewer.Create(ctx, record); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	blocker, err := claimant.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin claim blocker: %v", err)
	}
	defer func() { _ = blocker.Rollback() }()
	claimed := record
	claimed.OwnerID = "owner-2"
	claimed.ClaimSequence++
	claimed.LeaseExpiresAt = time.Now().Add(time.Hour)
	claimStarted := make(chan struct{})
	claimResult := make(chan error, 1)
	go func() {
		close(claimStarted)
		claimResult <- claimant.Claim(ctx, claimed, claimed.Version)
	}()
	<-claimStarted
	renewErr := renewer.RenewLease(ctx, workflow.CheckpointLease{
		RunID: record.RunID, OwnerID: record.OwnerID, ClaimSequence: record.ClaimSequence,
		ExpiresAt: time.Now().Add(2 * time.Hour),
	})
	if err := blocker.Rollback(); err != nil {
		t.Fatalf("rollback claim blocker: %v", err)
	}
	claimErr := <-claimResult
	if !errors.Is(renewErr, workflow.ErrCheckpointLeaseLost) {
		t.Fatalf("RenewLease() error = %v, want ErrCheckpointLeaseLost", renewErr)
	}
	if claimErr != nil {
		t.Fatalf("Claim() error = %v", claimErr)
	}
	loaded, err := renewer.Load(ctx, record.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.OwnerID != claimed.OwnerID || loaded.ClaimSequence != claimed.ClaimSequence ||
		!loaded.LeaseExpiresAt.Equal(claimed.LeaseExpiresAt) {
		t.Fatalf("Load() = %+v, want claimed lease %+v", loaded, claimed)
	}
}

func TestSQLiteSavePreservesLeaseRenewedAfterLoad(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	originalExpiry := now.Add(time.Minute)
	renewedExpiry := now.Add(2 * time.Minute)
	path := filepath.Join(t.TempDir(), "save.db")
	storeA := openTestStoreAt(t, path)
	storeB := openTestStoreAt(t, path)
	record := leaseCheckpoint("stale-save")
	record.LeaseExpiresAt = originalExpiry
	if err := storeA.Create(ctx, record); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	stale, err := storeB.Load(ctx, record.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if err := storeA.RenewLease(ctx, workflow.CheckpointLease{
		RunID: record.RunID, OwnerID: record.OwnerID,
		ClaimSequence: record.ClaimSequence, ExpiresAt: renewedExpiry,
	}); err != nil {
		t.Fatalf("RenewLease() error = %v", err)
	}
	stale.ConfirmedSequence = 1
	stale.ReplayStatus = workflow.ReplaySafe
	stale.UpdatedAt = now.Add(time.Second)
	if err := storeB.Save(ctx, stale, stale.Version); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	loaded, err := storeA.Load(ctx, record.RunID)
	if err != nil {
		t.Fatalf("Load() after Save error = %v", err)
	}
	if loaded.Version != 2 || loaded.ConfirmedSequence != 1 ||
		loaded.OwnerID != record.OwnerID || loaded.ClaimSequence != record.ClaimSequence ||
		!loaded.LeaseExpiresAt.Equal(renewedExpiry) {
		t.Fatalf("Load() = %+v, want saved state with renewed expiry %v", loaded, renewedExpiry)
	}
}

func TestSQLiteSaveRejectsChangedLeaseIdentity(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*workflow.CheckpointRecord)
	}{
		{
			name: "owner",
			mutate: func(record *workflow.CheckpointRecord) {
				record.OwnerID = "owner-2"
			},
		},
		{
			name: "claim sequence",
			mutate: func(record *workflow.CheckpointRecord) {
				record.ClaimSequence++
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "save.db")
			storeA := openTestStoreAt(t, path)
			storeB := openTestStoreAt(t, path)
			record := leaseCheckpoint("changed-lease")
			if err := storeA.Create(t.Context(), record); err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			candidate, err := storeB.Load(t.Context(), record.RunID)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			test.mutate(&candidate)
			if err := storeB.Save(t.Context(), candidate, candidate.Version); !errors.Is(err, workflow.ErrCheckpointLeaseLost) {
				t.Fatalf("Save() error = %v, want ErrCheckpointLeaseLost", err)
			}
			loaded, err := storeA.Load(t.Context(), record.RunID)
			if err != nil {
				t.Fatalf("Load() after Save error = %v", err)
			}
			if loaded.Version != record.Version || loaded.OwnerID != record.OwnerID ||
				loaded.ClaimSequence != record.ClaimSequence {
				t.Fatalf("Load() = %+v, want original lease identity", loaded)
			}
		})
	}
}

func TestSQLiteExpiredRenewalDoesNotBlockClaim(t *testing.T) {
	ctx := context.Background()
	past := time.Date(2000, time.January, 1, 0, 0, 0, 0, time.UTC)
	future := time.Date(2100, time.January, 1, 0, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "claim.db")
	storeA := openTestStoreAt(t, path)
	storeB := openTestStoreAt(t, path)
	record := leaseCheckpoint("stale-claim")
	record.OwnerID = "owner-a"
	record.LeaseExpiresAt = past
	if err := storeA.Create(ctx, record); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	stale, err := storeB.Load(ctx, record.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if err := storeA.RenewLease(ctx, workflow.CheckpointLease{
		RunID: record.RunID, OwnerID: record.OwnerID, ClaimSequence: record.ClaimSequence, ExpiresAt: future,
	}); !errors.Is(err, workflow.ErrCheckpointLeaseLost) {
		t.Fatalf("RenewLease() error = %v, want ErrCheckpointLeaseLost", err)
	}
	stale.OwnerID = "owner-b"
	stale.ClaimSequence++
	stale.LeaseExpiresAt = future
	if err := storeB.Claim(ctx, stale, stale.Version); err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	loaded, err := storeA.Load(ctx, record.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.OwnerID != stale.OwnerID || loaded.ClaimSequence != stale.ClaimSequence ||
		loaded.Version != record.Version+1 || !loaded.LeaseExpiresAt.Equal(future) {
		t.Fatalf("Load() = %+v, want claimed owner %q expiry %v claim sequence %d version %d",
			loaded, stale.OwnerID, future, stale.ClaimSequence, record.Version+1)
	}
}

func TestSQLiteClaimAllowsOneConcurrentClaimant(t *testing.T) {
	ctx := context.Background()
	past := time.Date(2000, time.January, 1, 0, 0, 0, 0, time.UTC)
	future := time.Date(2100, time.January, 1, 0, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "claim.db")
	storeA := openTestStoreAt(t, path)
	storeB := openTestStoreAt(t, path)
	record := leaseCheckpoint("concurrent-claim")
	record.OwnerID = "expired-owner"
	record.LeaseExpiresAt = past
	if err := storeA.Create(ctx, record); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	head, err := storeA.Load(ctx, record.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	ready := make(chan struct{}, 2)
	start := make(chan struct{})
	results := make(chan error, 2)
	claim := func(store *Store, ownerID string) {
		candidate := head
		candidate.OwnerID = ownerID
		candidate.ClaimSequence++
		candidate.LeaseExpiresAt = future
		ready <- struct{}{}
		<-start
		results <- store.Claim(ctx, candidate, head.Version)
	}
	go claim(storeA, "owner-a")
	go claim(storeB, "owner-b")
	<-ready
	<-ready
	close(start)

	succeeded, conflicted := 0, 0
	for range 2 {
		switch err := <-results; {
		case err == nil:
			succeeded++
		case errors.Is(err, workflow.ErrCheckpointConflict):
			conflicted++
		default:
			t.Fatalf("Claim() error = %v", err)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("Claim() results = %d succeeded, %d conflicted; want 1 and 1", succeeded, conflicted)
	}
	loaded, err := storeA.Load(ctx, record.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.Version != head.Version+1 || loaded.ClaimSequence != head.ClaimSequence+1 {
		t.Fatalf("Load() = version %d claim sequence %d, want %d and %d",
			loaded.Version, loaded.ClaimSequence, head.Version+1, head.ClaimSequence+1)
	}
}

func TestSQLiteClaimErrors(t *testing.T) {
	past := time.Date(2000, time.January, 1, 0, 0, 0, 0, time.UTC)
	future := time.Date(2100, time.January, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name          string
		terminal      bool
		mutate        func(*workflow.CheckpointRecord, *int64)
		cancelContext bool
		want          error
	}{
		{name: "terminal head", terminal: true, want: workflow.ErrCheckpointConflict},
		{
			name: "identity mismatch",
			mutate: func(candidate *workflow.CheckpointRecord, _ *int64) {
				candidate.SessionID = "other-session"
			},
			want: workflow.ErrCheckpointMismatch,
		},
		{
			name: "wrong version",
			mutate: func(candidate *workflow.CheckpointRecord, version *int64) {
				candidate.Version++
				*version = candidate.Version
			},
			want: workflow.ErrCheckpointConflict,
		},
		{name: "canceled context", cancelContext: true, want: context.Canceled},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := openTestStore(t)
			record := leaseCheckpoint("claim-" + test.name)
			if test.terminal {
				record.OwnerID = ""
				record.ClaimSequence = 0
				record.LeaseExpiresAt = time.Time{}
			} else {
				record.LeaseExpiresAt = past
			}
			if err := store.Create(context.Background(), record); err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			head, err := store.Load(context.Background(), record.RunID)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if test.terminal {
				terminal := head
				terminal.Status = workflow.CheckpointCompleted
				if err := store.Finish(context.Background(), terminal, head.Version); err != nil {
					t.Fatalf("Finish() error = %v", err)
				}
				head, err = store.Load(context.Background(), record.RunID)
				if err != nil {
					t.Fatalf("Load() error = %v", err)
				}
			}
			candidate := head
			candidate.Status = workflow.CheckpointRunning
			candidate.OwnerID = "owner-b"
			candidate.ClaimSequence++
			candidate.LeaseExpiresAt = future
			version := candidate.Version
			if test.mutate != nil {
				test.mutate(&candidate, &version)
			}
			ctx := context.Background()
			if test.cancelContext {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if err := store.Claim(ctx, candidate, version); !errors.Is(err, test.want) {
				t.Fatalf("Claim() error = %v, want %v", err, test.want)
			}
		})
	}
}

type sqliteResultError struct {
	code    int
	message string
}

func (err sqliteResultError) Error() string { return err.message }
func (err sqliteResultError) Code() int     { return err.code }

func TestDatabaseBusyUsesSQLiteResultCode(t *testing.T) {
	tests := []struct {
		name string
		code int
		text string
		want bool
	}{
		{name: "busy", code: sqlite3.SQLITE_BUSY, text: "opaque sqlite error", want: true},
		{name: "busy snapshot", code: sqlite3.SQLITE_BUSY | (2 << 8), text: "opaque sqlite error", want: true},
		{name: "locked", code: sqlite3.SQLITE_LOCKED, text: "opaque sqlite error", want: true},
		{name: "locked shared cache", code: sqlite3.SQLITE_LOCKED | (1 << 8), text: "opaque sqlite error", want: true},
		{name: "unrelated code", code: sqlite3.SQLITE_CONSTRAINT, text: "database is locked", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := errors.Join(errors.New("wrapped sqlite operation"), sqliteResultError{code: test.code, message: test.text})
			if got := databaseBusy(err); got != test.want {
				t.Fatalf("databaseBusy() = %t, want %t for result code %d", got, test.want, test.code)
			}
		})
	}
}

func TestSQLiteHeartbeatKeepsLongNodeLeasedAcrossStores(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lease.db")
	if err := Migrate(path); err != nil {
		t.Fatal(err)
	}
	firstStore, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = firstStore.Close() }()
	secondStore, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = secondStore.Close() }()

	const (
		leaseDuration = 300 * time.Millisecond
		renewEvery    = 50 * time.Millisecond
	)
	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseNode := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseNode()
	first := workflow.New[string, string](
		"sqlite-heartbeat",
		workflow.WithCheckpointer(firstStore),
		workflow.WithCheckpointLease(leaseDuration, renewEvery),
	)
	firstNode := first.Node("node", func(ctx context.Context, input string) (string, error) {
		close(started)
		select {
		case <-release:
			return input, nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	})
	first.Entry(firstNode)
	first.Exit(firstNode)

	secondRan := make(chan struct{}, 1)
	second := workflow.New[string, string](
		"sqlite-heartbeat",
		workflow.WithCheckpointer(secondStore),
		workflow.WithCheckpointLease(leaseDuration, renewEvery),
	)
	secondNode := second.Node("node", func(_ context.Context, input string) (string, error) {
		secondRan <- struct{}{}
		return input, nil
	})
	second.Entry(secondNode)
	second.Exit(secondNode)

	firstDone := make(chan error, 1)
	go func() {
		_, err := first.Invoke(context.Background(), "input", gopact.WithRunID("shared-run"))
		firstDone <- err
	}()
	<-started
	initial, err := secondStore.Load(context.Background(), "shared-run")
	if err != nil {
		t.Fatalf("initial Load() error = %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	renewedPastInitialLease := false
	for time.Now().Before(deadline) {
		current, loadErr := secondStore.Load(context.Background(), "shared-run")
		if loadErr != nil {
			t.Fatalf("Load() error = %v", loadErr)
		}
		if time.Now().After(initial.LeaseExpiresAt) && current.LeaseExpiresAt.After(time.Now()) {
			renewedPastInitialLease = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !renewedPastInitialLease {
		t.Fatal("lease was not renewed past its initial expiry")
	}
	_, err = second.Invoke(context.Background(), "", workflow.WithResume(workflow.ResumeRequest{RunID: "shared-run"}))
	if !errors.Is(err, workflow.ErrCheckpointConflict) {
		t.Fatalf("second Invoke() error = %v, want ErrCheckpointConflict", err)
	}
	select {
	case <-secondRan:
		t.Fatal("second instance executed the leased node")
	default:
	}
	releaseNode()
	if err := <-firstDone; err != nil {
		t.Fatalf("first Invoke() error = %v", err)
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	return openTestStoreAt(t, ":memory:")
}

func openTestStoreAt(t *testing.T, path string) *Store {
	t.Helper()
	migrateTestStore(t, path)
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	return store
}

func migrateTestStore(t *testing.T, path string) {
	t.Helper()
	if inMemoryDSN(path) {
		return
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		return
	}
	if err := Migrate(path); err != nil {
		t.Fatal(err)
	}
}

func TestOpenDoesNotEnableWAL(t *testing.T) {
	store := openTestStoreAt(t, filepath.Join(t.TempDir(), "rollback-journal.db"))
	var mode string
	if err := store.db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatalf("read journal mode: %v", err)
	}
	if mode != "delete" {
		t.Fatalf("journal mode = %q, want delete", mode)
	}
}

func TestMigrateConvertsPersistentWAL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-wal.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	var mode string
	if err := db.QueryRow(`PRAGMA journal_mode=WAL`).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Fatalf("legacy journal mode = %q, want wal", mode)
	}
	if _, err := db.Exec(`CREATE TABLE legacy_data (value TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO legacy_data (value) VALUES ('preserved')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	if err := Migrate(path); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open() after Migrate error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "delete" {
		t.Fatalf("journal mode after upgrade = %q, want delete", mode)
	}
	var value string
	if err := store.db.QueryRow(`SELECT value FROM legacy_data`).Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != "preserved" {
		t.Fatalf("legacy value = %q", value)
	}
}

func TestOpenRejectsExplicitWALDSN(t *testing.T) {
	path := filepath.Join(t.TempDir(), "explicit-wal.db") + "?_pragma=journal_mode(WAL)"
	if _, err := Open(path); err == nil {
		t.Fatal("Open() error = nil, want explicit WAL rejection")
	}
}

func TestOpenRejectsFileBackedMemoryJournal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory-journal.db") + "?_pragma=journal_mode(MEMORY)"
	if _, err := Open(path); err == nil {
		t.Fatal("Open() error = nil, want file-backed MEMORY journal rejection")
	}
}

func leaseCheckpoint(runID string) workflow.CheckpointRecord {
	now := time.Now().UTC()
	return workflow.CheckpointRecord{
		ID: "checkpoint:" + runID, SessionID: "session-1", RunID: runID, WorkflowName: "workflow",
		TopologyVersion: "v1", SchemaVersion: 2, Version: 1, Status: workflow.CheckpointRunning,
		Payload: []byte(`{"state":"running"}`), ReplayStatus: workflow.ReplayUnknown,
		OwnerID: "owner-1", ClaimSequence: 1, LeaseExpiresAt: now.Add(time.Minute),
		CreatedAt: now, UpdatedAt: now,
	}
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
	if err := Migrate(path); err != nil {
		t.Fatal(err)
	}
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

func TestMigrateUpgradesLegacyRunLogTable(t *testing.T) {
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

	if err := Migrate(path); err != nil {
		t.Fatalf("Migrate() error = %v", err)
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
