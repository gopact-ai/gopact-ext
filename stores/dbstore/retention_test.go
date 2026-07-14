package dbstore

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gopact-ai/gopact/runlog"
	"github.com/gopact-ai/gopact/workflow"
	"github.com/libtnb/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestPurgeTerminalRuns(t *testing.T) {
	store, err := Open(sqlite.Open(filepath.Join(t.TempDir(), "retention.db")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	terminal := testCheckpoint("terminal-run")
	if err := store.Create(t.Context(), terminal); err != nil {
		t.Fatal(err)
	}
	event := runlog.Record{
		SessionID: terminal.SessionID, RunID: terminal.RunID, Sequence: 1,
		EventType: "test.event", Source: "test", Timestamp: time.Now().UTC(),
	}
	if err := store.Append(t.Context(), event); err != nil {
		t.Fatal(err)
	}
	terminal.Status = workflow.CheckpointCompleted
	terminal.OwnerID = ""
	terminal.LeaseExpiresAt = time.Time{}
	if err := store.Finish(t.Context(), terminal, terminal.Version); err != nil {
		t.Fatal(err)
	}
	running := testCheckpoint("running-run")
	if err := store.Create(t.Context(), running); err != nil {
		t.Fatal(err)
	}

	result, err := store.PurgeTerminalRuns(t.Context(), PurgeRequest{Before: time.Now().Add(time.Minute)})
	if err != nil {
		t.Fatalf("PurgeTerminalRuns() error = %v", err)
	}
	if result.Runs != 1 || result.Checkpoints != 2 || result.Events != 1 {
		t.Fatalf("PurgeTerminalRuns() = %+v, want 1 run, 2 checkpoints, 1 event", result)
	}
	if _, err := store.Load(t.Context(), terminal.RunID); !errors.Is(err, workflow.ErrCheckpointNotFound) {
		t.Fatalf("Load(terminal) error = %v, want ErrCheckpointNotFound", err)
	}
	if _, err := store.Load(t.Context(), running.RunID); err != nil {
		t.Fatalf("Load(running) error = %v", err)
	}
}

func TestPurgeTerminalRunsBatchesOneLargeRunAndRejectsLateAppend(t *testing.T) {
	store, err := Open(sqlite.Open(filepath.Join(t.TempDir(), "batched-retention.db")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	record := testCheckpoint("large-terminal")
	if err := store.Create(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	for sequence := int64(1); sequence <= 5; sequence++ {
		if err := store.Append(t.Context(), runlog.Record{
			SessionID: record.SessionID, RunID: record.RunID, Sequence: sequence,
			EventType: "test.event", Source: "test", Timestamp: time.Now().UTC(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	record.Status = workflow.CheckpointCompleted
	record.OwnerID = ""
	record.LeaseExpiresAt = time.Time{}
	if err := store.Finish(t.Context(), record, record.Version); err != nil {
		t.Fatal(err)
	}

	total := PurgeResult{}
	for iteration := 0; iteration < 10; iteration++ {
		result, err := store.PurgeTerminalRuns(t.Context(), PurgeRequest{
			Before: time.Now().Add(time.Minute), Limit: 1, RowLimit: 2,
		})
		if err != nil {
			t.Fatal(err)
		}
		if result.Checkpoints+result.Events > 2 {
			t.Fatalf("deleted %d history rows with RowLimit 2", result.Checkpoints+result.Events)
		}
		total.Runs += result.Runs
		total.Checkpoints += result.Checkpoints
		total.Events += result.Events
		if result.Pending == 0 {
			break
		}
	}
	if total.Runs != 1 || total.Checkpoints != 2 || total.Events != 5 {
		t.Fatalf("batched purge total = %+v", total)
	}
	late := runlog.Record{
		SessionID: record.SessionID, RunID: record.RunID, Sequence: 6,
		EventType: "late.event", Source: "test", Timestamp: time.Now().UTC(),
	}
	if err := store.Append(t.Context(), late); !errors.Is(err, runlog.ErrConflict) {
		t.Fatalf("late Append() error = %v, want ErrConflict", err)
	}
	legacy := testCheckpoint(record.RunID)
	metadata, err := encodeCheckpointMetadata(legacy)
	if err != nil {
		t.Fatal(err)
	}
	tx := store.db.Begin()
	if err := tx.Create(&checkpointRow{
		RunID: legacy.RunID, Version: legacy.Version, RecordJSON: metadata, Payload: legacy.Payload,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := tx.Create(runHead(legacy)).Error; err == nil {
		_ = tx.Rollback().Error
		t.Fatal("legacy run-head insert succeeded for a purged RunID")
	}
	if err := tx.Rollback().Error; err != nil {
		t.Fatal(err)
	}
	var recreated int64
	if err := store.db.Model(&checkpointRow{}).Where("run_id = ?", legacy.RunID).Count(&recreated).Error; err != nil {
		t.Fatal(err)
	}
	if recreated != 0 {
		t.Fatalf("legacy recreate left %d checkpoint rows after rollback", recreated)
	}
}

func TestPurgeRunLogBoundsPlainJournalHistory(t *testing.T) {
	store, err := Open(sqlite.Open(filepath.Join(t.TempDir(), "journal-retention.db")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	before := time.Now().UTC()
	clock := before.Add(-time.Hour)
	store.now = func() time.Time { return clock }
	for sequence, timestamp := range []time.Time{before.Add(-time.Hour), before.Add(time.Hour)} {
		clock = timestamp
		if err := store.Append(t.Context(), runlog.Record{
			SessionID: "session-1", RunID: "journal-only", Sequence: int64(sequence + 1),
			EventType: "test.event", Source: "test", Timestamp: timestamp,
		}); err != nil {
			t.Fatal(err)
		}
	}
	var retentionRows []runLogRetentionRow
	if err := store.db.Where("run_id = ?", "journal-only").Order("ordinal").Find(&retentionRows).Error; err != nil {
		t.Fatal(err)
	}
	for index, appendedAt := range []time.Time{before.Add(-time.Hour), before.Add(time.Hour)} {
		if err := store.db.Model(&runLogRetentionRow{}).
			Where("ordinal = ?", retentionRows[index].Ordinal).
			Update("appended_at_unix_nano", appendedAt.UnixNano()).Error; err != nil {
			t.Fatal(err)
		}
	}
	result, err := store.PurgeRunLog(t.Context(), RunLogPurgeRequest{
		Before: before, Limit: 1, AllowReplayLoss: true,
	})
	if err != nil {
		t.Fatalf("PurgeRunLog() error = %v", err)
	}
	if result.Events != 1 {
		t.Fatalf("PurgeRunLog() = %+v, want one event", result)
	}
	rows, err := store.List(t.Context(), runlog.Query{RunID: "journal-only"})
	if err != nil || len(rows) != 1 || rows[0].Sequence != 2 {
		t.Fatalf("List() = %+v, %v, want only sequence 2", rows, err)
	}
}

func TestPurgeConfirmedRunLogCompactsOnlyContinuousCheckpointedPrefix(t *testing.T) {
	store, err := Open(sqlite.Open(filepath.Join(t.TempDir(), "confirmed-prefix.db")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	record := testCheckpoint("confirmed-prefix")
	if err := store.Create(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	fence := runlog.Fence{OwnerID: record.OwnerID, ClaimSequence: record.ClaimSequence}
	for sequence := int64(1); sequence <= 5; sequence++ {
		if err := store.AppendFenced(t.Context(), runlog.Record{
			SessionID: record.SessionID, RunID: record.RunID, Sequence: sequence,
			EventType: "test.event", Source: "test", Timestamp: time.Now().UTC(),
		}, fence); err != nil {
			t.Fatal(err)
		}
	}
	record.ConfirmedSequence = 3
	record.Payload = []byte(`{"state":"confirmed-three"}`)
	if err := store.Save(t.Context(), record, record.Version); err != nil {
		t.Fatal(err)
	}
	bounded, err := store.PurgeConfirmedRunLog(t.Context(), ConfirmedRunLogPurgeRequest{
		RunID: record.RunID, Before: time.Now().Add(time.Minute), Limit: 2, ByteLimit: 1, AllowHistoryLoss: true,
	})
	if err != nil {
		t.Fatalf("byte-bounded PurgeConfirmedRunLog() error = %v", err)
	}
	if bounded.Events != 0 || bounded.CompactedThroughSequence != 0 {
		t.Fatalf("byte-bounded PurgeConfirmedRunLog() = %+v, want no compaction", bounded)
	}

	first, err := store.PurgeConfirmedRunLog(t.Context(), ConfirmedRunLogPurgeRequest{
		RunID: record.RunID, Before: time.Now().Add(time.Minute), Limit: 2, AllowHistoryLoss: true,
	})
	if err != nil {
		t.Fatalf("first PurgeConfirmedRunLog() error = %v", err)
	}
	if first.Events != 2 || first.CompactedThroughSequence != 2 {
		t.Fatalf("first PurgeConfirmedRunLog() = %+v, want two events through sequence 2", first)
	}
	if _, err := store.List(t.Context(), runlog.Query{RunID: record.RunID}); !errors.Is(err, runlog.ErrHistoryCompacted) {
		t.Fatalf("List(before floor) error = %v, want ErrHistoryCompacted", err)
	}
	remaining, err := store.List(t.Context(), runlog.Query{RunID: record.RunID, After: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 3 || remaining[0].Sequence != 3 || remaining[2].Sequence != 5 {
		t.Fatalf("List(after floor) = %+v, want sequences 3..5", remaining)
	}
	if err := store.AppendFenced(t.Context(), runlog.Record{
		SessionID: record.SessionID, RunID: record.RunID, Sequence: 1,
		EventType: "late.event", Source: "test", Timestamp: time.Now().UTC(),
	}, fence); !errors.Is(err, runlog.ErrHistoryCompacted) {
		t.Fatalf("late AppendFenced() error = %v, want ErrHistoryCompacted", err)
	}
	second, err := store.PurgeConfirmedRunLog(t.Context(), ConfirmedRunLogPurgeRequest{
		RunID: record.RunID, Before: time.Now().Add(time.Minute), Limit: 10, AllowHistoryLoss: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.Events != 1 || second.CompactedThroughSequence != 3 {
		t.Fatalf("second PurgeConfirmedRunLog() = %+v, want one event through sequence 3", second)
	}
	remaining, err = store.List(t.Context(), runlog.Query{RunID: record.RunID, After: 3})
	if err != nil || len(remaining) != 2 || remaining[0].Sequence != 4 {
		t.Fatalf("List(final suffix) = %+v, %v, want sequences 4..5", remaining, err)
	}
	if _, err := store.Load(t.Context(), record.RunID); err != nil {
		t.Fatalf("Load() after prefix compaction error = %v", err)
	}
}

func TestPurgeConfirmedRunLogFailsClosedForUnsafeRunStates(t *testing.T) {
	store, err := Open(sqlite.Open(filepath.Join(t.TempDir(), "confirmed-guards.db")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	record := testCheckpoint("pending-prefix")
	if err := store.Create(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendFenced(t.Context(), runlog.Record{
		SessionID: record.SessionID, RunID: record.RunID, Sequence: 1,
		EventType: "pending.event", Source: "test", Timestamp: time.Now().UTC(),
	}, runlog.Fence{OwnerID: record.OwnerID, ClaimSequence: record.ClaimSequence}); err != nil {
		t.Fatal(err)
	}
	record.ConfirmedSequence = 1
	record.PendingSequence = 2
	if err := store.Save(t.Context(), record, record.Version); err != nil {
		t.Fatal(err)
	}
	request := ConfirmedRunLogPurgeRequest{
		RunID: record.RunID, Before: time.Now().Add(time.Minute), AllowHistoryLoss: true,
	}
	if _, err := store.PurgeConfirmedRunLog(t.Context(), request); err == nil ||
		!strings.Contains(err.Error(), "pending event") {
		t.Fatalf("PurgeConfirmedRunLog(pending) error = %v", err)
	}

	if err := store.Append(t.Context(), runlog.Record{
		SessionID: "session-1", RunID: "journal-prefix", Sequence: 1,
		EventType: "journal.event", Source: "test", Timestamp: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	request.RunID = "journal-prefix"
	if _, err := store.PurgeConfirmedRunLog(t.Context(), request); err == nil ||
		!strings.Contains(err.Error(), "not an active workflow") {
		t.Fatalf("PurgeConfirmedRunLog(journal) error = %v", err)
	}

	current, err := store.Load(t.Context(), record.RunID)
	if err != nil {
		t.Fatal(err)
	}
	current.PendingSequence = 0
	current.Status = workflow.CheckpointCompleted
	current.OwnerID = ""
	current.LeaseExpiresAt = time.Time{}
	if err := store.Finish(t.Context(), current, current.Version); err != nil {
		t.Fatal(err)
	}
	request.RunID = record.RunID
	if _, err := store.PurgeConfirmedRunLog(t.Context(), request); err == nil ||
		!strings.Contains(err.Error(), "terminal") {
		t.Fatalf("PurgeConfirmedRunLog(terminal) error = %v", err)
	}

	request.AllowHistoryLoss = false
	if _, err := store.PurgeConfirmedRunLog(t.Context(), request); err == nil ||
		!strings.Contains(err.Error(), "AllowHistoryLoss") {
		t.Fatalf("PurgeConfirmedRunLog(without acknowledgement) error = %v", err)
	}
}

func TestPurgeTerminalRunsDoesNotExceedByteLimit(t *testing.T) {
	store, err := Open(sqlite.Open(filepath.Join(t.TempDir(), "byte-bounded-retention.db")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	record := testCheckpoint("byte-bounded-terminal")
	if err := store.Create(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	for sequence := int64(1); sequence <= 2; sequence++ {
		if err := store.Append(t.Context(), runlog.Record{
			SessionID: record.SessionID, RunID: record.RunID, Sequence: sequence,
			EventType: "test.event", Source: "test", Timestamp: time.Now().UTC(),
			Payload: []byte(`{"payload":"large-enough-to-exceed-one-byte"}`),
		}); err != nil {
			t.Fatal(err)
		}
	}
	record.Status = workflow.CheckpointCompleted
	record.OwnerID = ""
	record.LeaseExpiresAt = time.Time{}
	if err := store.Finish(t.Context(), record, record.Version); err != nil {
		t.Fatal(err)
	}

	result, err := store.PurgeTerminalRuns(t.Context(), PurgeRequest{
		Before: time.Now().Add(time.Minute), Limit: 1, RowLimit: 100, ByteLimit: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result != (PurgeResult{}) {
		t.Fatalf("PurgeTerminalRuns() = %+v, want no deletion over byte limit", result)
	}
}

func TestPurgeRunLogDoesNotExceedByteLimit(t *testing.T) {
	store, err := Open(sqlite.Open(filepath.Join(t.TempDir(), "journal-byte-limit.db")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if err := store.Append(t.Context(), runlog.Record{
		SessionID: "journal-byte-limit", RunID: "journal-byte-limit", Sequence: 1, EventType: "test.event", Source: "test",
		Timestamp: time.Now().UTC(), Payload: []byte(`{"payload":"larger-than-one-byte"}`),
	}); err != nil {
		t.Fatal(err)
	}
	result, err := store.PurgeRunLog(t.Context(), RunLogPurgeRequest{
		Before: time.Now().Add(time.Minute), Limit: 10, ByteLimit: 1, AllowReplayLoss: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Events != 0 {
		t.Fatalf("PurgeRunLog() = %+v, want no deletion over byte limit", result)
	}
}

func TestSQLiteRunLogRetentionUsesSubsecondDatabaseClock(t *testing.T) {
	store, err := Open(sqlite.Open(filepath.Join(t.TempDir(), "retention-clock.db")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	for {
		now, err := store.databaseNow(store.db.WithContext(t.Context()))
		if err != nil {
			t.Fatal(err)
		}
		if now.Nanosecond() >= int(100*time.Millisecond) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := store.Append(t.Context(), runlog.Record{
		SessionID: "retention-clock", RunID: "retention-clock", Sequence: 1, EventType: "test.event", Source: "test", Timestamp: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	var appendedAt int64
	if err := store.db.Model(&runLogRetentionRow{}).Select("appended_at_unix_nano").Take(&appendedAt).Error; err != nil {
		t.Fatal(err)
	}
	if appendedAt%int64(time.Second) == 0 {
		t.Fatalf("appended_at_unix_nano = %d, want subsecond precision", appendedAt)
	}
}

func TestPurgeTerminalRunsChecksRemainingHistoryWithExistenceQueries(t *testing.T) {
	store, err := Open(sqlite.Open(filepath.Join(t.TempDir(), "terminal-existence.db")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	record := testCheckpoint("terminal-existence")
	if err := store.Create(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(t.Context(), runlog.Record{
		SessionID: record.SessionID, RunID: record.RunID, Sequence: 1,
		EventType: "test.event", Source: "test", Timestamp: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	record.Status = workflow.CheckpointCompleted
	record.OwnerID = ""
	record.LeaseExpiresAt = time.Time{}
	if err := store.Finish(t.Context(), record, record.Version); err != nil {
		t.Fatal(err)
	}

	recorder := &sqlRecorder{}
	store.db = store.db.Session(&gorm.Session{Logger: recorder})
	result, err := store.PurgeTerminalRuns(t.Context(), PurgeRequest{
		Before: time.Now().Add(time.Minute), Limit: 1, RowLimit: 1,
	})
	if err != nil {
		t.Fatalf("PurgeTerminalRuns() error = %v", err)
	}
	if result.Events != 1 || result.Checkpoints != 0 || result.Pending != 1 {
		t.Fatalf("PurgeTerminalRuns() = %+v, want one event and one pending run", result)
	}
	assertExistenceQueryWithoutCount(t, recorder.statements, eventTable)
	assertExistenceQueryWithoutCount(t, recorder.statements, checkpointTable)
}

func TestPurgeRunLogChecksRemainingEventsWithExistenceQuery(t *testing.T) {
	store, err := Open(sqlite.Open(filepath.Join(t.TempDir(), "journal-existence.db")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	for sequence := int64(1); sequence <= 2; sequence++ {
		if err := store.Append(t.Context(), runlog.Record{
			SessionID: "session-1", RunID: "journal-existence", Sequence: sequence,
			EventType: "test.event", Source: "test", Timestamp: time.Now().UTC(),
		}); err != nil {
			t.Fatal(err)
		}
	}

	recorder := &sqlRecorder{}
	store.db = store.db.Session(&gorm.Session{Logger: recorder})
	result, err := store.PurgeRunLog(t.Context(), RunLogPurgeRequest{
		Before: time.Now().Add(time.Minute), Limit: 1, AllowReplayLoss: true,
	})
	if err != nil {
		t.Fatalf("PurgeRunLog() error = %v", err)
	}
	if result.Events != 1 {
		t.Fatalf("PurgeRunLog() = %+v, want one event", result)
	}
	assertExistenceQueryWithoutCount(t, recorder.statements, eventTable)
}

type sqlRecorder struct {
	statements []string
}

func (recorder *sqlRecorder) LogMode(logger.LogLevel) logger.Interface { return recorder }
func (*sqlRecorder) Info(context.Context, string, ...any)              {}
func (*sqlRecorder) Warn(context.Context, string, ...any)              {}
func (*sqlRecorder) Error(context.Context, string, ...any)             {}

func (recorder *sqlRecorder) Trace(_ context.Context, _ time.Time, query func() (string, int64), _ error) {
	statement, _ := query()
	recorder.statements = append(recorder.statements, statement)
}

func assertExistenceQueryWithoutCount(t *testing.T, statements []string, table string) {
	t.Helper()
	wantExistence := "select 1 from " + table
	wantCount := "select count(*) from " + table
	foundExistence := false
	for _, statement := range statements {
		normalized := strings.NewReplacer("`", "", `"`, "").Replace(strings.ToLower(statement))
		normalized = strings.Join(strings.Fields(normalized), " ")
		if strings.Contains(normalized, wantCount) {
			t.Fatalf("purge issued a full count against %s: %s", table, statement)
		}
		if strings.Contains(normalized, wantExistence) && strings.Contains(normalized, "limit 1") {
			foundExistence = true
		}
	}
	if !foundExistence {
		t.Fatalf("purge did not issue SELECT 1 ... LIMIT 1 against %s; SQL = %q", table, statements)
	}
}
