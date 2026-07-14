package dbstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gopact-ai/gopact/runlog"
	"github.com/gopact-ai/gopact/workflow"
	"github.com/libtnb/sqlite"
	gormmysql "gorm.io/driver/mysql"
)

func TestContextEntryPointsRejectCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if store, err := ConnectContext(ctx, sqlite.Open(filepath.Join(t.TempDir(), "connect.db"))); store != nil ||
		!errors.Is(err, context.Canceled) {
		t.Fatalf("ConnectContext(canceled) = %v, %v, want nil, context.Canceled", store, err)
	}
	if err := MigrateContext(ctx, sqlite.Open(filepath.Join(t.TempDir(), "migrate.db"))); !errors.Is(err, context.Canceled) {
		t.Fatalf("MigrateContext(canceled) error = %v, want context.Canceled", err)
	}
}

func TestOpenSQLiteInitializesStore(t *testing.T) {
	store, err := Open(sqlite.Open(filepath.Join(t.TempDir(), "workflow.db")))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	now := time.Now().UTC()
	record := workflow.CheckpointRecord{
		ID: "checkpoint:run-1", SessionID: "session-1", RunID: "run-1", WorkflowName: "workflow",
		TopologyVersion: "v1", SchemaVersion: 2, Version: 1, Status: workflow.CheckpointRunning,
		Payload: []byte(`{"state":"running"}`), ReplayStatus: workflow.ReplayUnknown,
		OwnerID: "owner-1", ClaimSequence: 1, LeaseExpiresAt: now.Add(time.Minute),
		CreatedAt: now, UpdatedAt: now,
	}
	if err := store.Create(t.Context(), record); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := store.Create(t.Context(), record); !errors.Is(err, workflow.ErrCheckpointExists) {
		t.Fatalf("duplicate Create() error = %v, want ErrCheckpointExists", err)
	}
	loaded, err := store.Load(t.Context(), record.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.RunID != record.RunID || loaded.Version != record.Version || string(loaded.Payload) != string(record.Payload) {
		t.Fatalf("Load() = %+v, want run %q version %d", loaded, record.RunID, record.Version)
	}
}

func TestStoreExposesGORMHandle(t *testing.T) {
	store, err := Open(sqlite.Open(filepath.Join(t.TempDir(), "gorm.db")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	db, err := store.GORMDB()
	if err != nil {
		t.Fatalf("GORMDB() error = %v", err)
	}
	if db == nil || db != store.db {
		t.Fatalf("GORMDB() = %p, want underlying handle %p", db, store.db)
	}
}

func TestOpenRejectsServerDialectMigration(t *testing.T) {
	store, err := Open(gormmysql.Open("unused"))
	if store != nil {
		_ = store.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "require Migrate followed by Connect") {
		t.Fatalf("Open(MySQL) error = %v, want explicit migration requirement", err)
	}
}

func TestLegacyIdentityPreflightFailsBeforeSchemaMutation(t *testing.T) {
	tests := []struct {
		name      string
		runID     string
		sessionID string
	}{
		{name: "physical ASCII run ID", runID: strings.Repeat("r", 192), sessionID: "session-1"},
		{name: "physical multibyte run ID", runID: strings.Repeat("界", 64), sessionID: "session-1"},
		{name: "physical trailing space", runID: "run-1 ", sessionID: "session-1"},
		{name: "empty run ID", runID: "", sessionID: "session-1"},
		{name: "JSON-only session ID", runID: "run-1", sessionID: strings.Repeat("s", 192)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "legacy.db")
			db, err := sql.Open(sqlite.DriverName, path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`CREATE TABLE gopact_runlog (
				ordinal INTEGER PRIMARY KEY AUTOINCREMENT,
				run_id TEXT NOT NULL,
				sequence INTEGER NOT NULL,
				record_json BLOB NOT NULL,
				UNIQUE (run_id, sequence)
			)`); err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(map[string]any{
				"RunID": test.runID, "SessionID": test.sessionID,
				"Sequence": 1, "EventType": "legacy.event", "Source": "test",
				"Timestamp": time.Now().UTC(),
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(
				`INSERT INTO gopact_runlog (run_id, sequence, record_json) VALUES (?, 1, ?)`,
				test.runID,
				encoded,
			); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}

			store, err := Open(sqlite.Open(path))
			if store != nil {
				_ = store.Close()
			}
			if !errors.Is(err, ErrIncompatibleLegacyData) {
				t.Fatalf("Open() error = %v, want ErrIncompatibleLegacyData", err)
			}
			check, err := sql.Open(sqlite.DriverName, path)
			if err != nil {
				t.Fatal(err)
			}
			defer check.Close()
			var migrationTables int
			if err := check.QueryRow(
				`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'gopact_schema_migrations'`,
			).Scan(&migrationTables); err != nil {
				t.Fatal(err)
			}
			if migrationTables != 0 {
				t.Fatal("preflight failure created migration schema")
			}
			var sessionColumns int
			if err := check.QueryRow(
				`SELECT COUNT(*) FROM pragma_table_info('gopact_runlog') WHERE name = 'session_id'`,
			).Scan(&sessionColumns); err != nil {
				t.Fatal(err)
			}
			if sessionColumns != 0 {
				t.Fatal("preflight failure mutated legacy RunLog schema")
			}
		})
	}
}

func TestConnectRequiresExplicitMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "explicit-migration.db")
	if store, err := Connect(sqlite.Open(path)); err == nil {
		_ = store.Close()
		t.Fatal("Connect() error = nil before Migrate()")
	}
	if err := Migrate(sqlite.Open(path)); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	store, err := Connect(sqlite.Open(path))
	if err != nil {
		t.Fatalf("Connect() error = %v after Migrate()", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestMigrateRepairsIncompleteV2Metadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "repair-v2.db")
	store, err := Open(sqlite.Open(path))
	if err != nil {
		t.Fatal(err)
	}
	record := runlog.Record{
		SessionID: "repair-session", RunID: "repair-journal", Sequence: 1,
		EventType: "repair.event", Source: "test", Timestamp: time.Now().UTC(),
	}
	if err := store.Append(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	raw, err := sql.Open(sqlite.DriverName, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`DELETE FROM gopact_runlog_retention`); err != nil {
		raw.Close()
		t.Fatal(err)
	}
	if _, err := raw.Exec(`DELETE FROM gopact_run_registry`); err != nil {
		raw.Close()
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	if err := Migrate(sqlite.Open(path)); err != nil {
		t.Fatalf("Migrate(repair) error = %v", err)
	}
	repaired, err := Connect(sqlite.Open(path))
	if err != nil {
		t.Fatal(err)
	}
	defer repaired.Close()
	records, err := repaired.List(t.Context(), runlog.Query{RunID: record.RunID})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Sequence != record.Sequence {
		t.Fatalf("List() after repair = %+v, want the original record", records)
	}
	if gap, err := repaired.v2MetadataGap(t.Context()); err != nil || gap != "" {
		t.Fatalf("v2MetadataGap() = %q, %v; want no gap", gap, err)
	}
}

func TestConnectRejectsNonUniqueRunLogIdentityIndex(t *testing.T) {
	path := filepath.Join(t.TempDir(), "schema-drift.db")
	if err := Migrate(sqlite.Open(path)); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open(sqlite.DriverName, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`DROP INDEX gopact_runlog_run_sequence`); err != nil {
		raw.Close()
		t.Fatal(err)
	}
	if _, err := raw.Exec(
		`CREATE INDEX gopact_runlog_run_sequence ON gopact_runlog (run_id, sequence)`,
	); err != nil {
		raw.Close()
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := Connect(sqlite.Open(path))
	if store != nil {
		_ = store.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "must uniquely cover") {
		t.Fatalf("Connect() error = %v, want non-unique identity-index rejection", err)
	}
}

func TestSQLiteLegacyRunLogMigrationDoesNotRebuildTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-no-default.db")
	db, err := sql.Open(sqlite.DriverName, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE gopact_runlog (
		ordinal INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id TEXT NOT NULL,
		run_id TEXT NOT NULL,
		sequence INTEGER NOT NULL,
		record_json BLOB NOT NULL,
		UNIQUE (run_id, sequence)
	)`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	// libtnb's SQLite AlterColumn rebuild path reserves this exact table name.
	// Keeping it occupied makes any accidental full RunLog rewrite fail loudly.
	if _, err := db.Exec(`CREATE TABLE gopact_runlog__temp (sentinel INTEGER)`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	record := runlog.Record{
		SessionID: "legacy-session", RunID: "legacy-run", Sequence: 1,
		EventType: "legacy.event", Source: "test", Timestamp: time.Now().UTC(),
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`INSERT INTO gopact_runlog (session_id, run_id, sequence, record_json) VALUES (?, ?, ?, ?)`,
		record.SessionID,
		record.RunID,
		record.Sequence,
		encoded,
	); err != nil {
		db.Close()
		t.Fatal(err)
	}
	var beforeRootPage int
	if err := db.QueryRow(
		`SELECT rootpage FROM sqlite_master WHERE type = 'table' AND name = 'gopact_runlog'`,
	).Scan(&beforeRootPage); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := Open(sqlite.Open(path))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()
	var afterRootPage int
	if err := store.db.Raw(
		`SELECT rootpage FROM sqlite_master WHERE type = 'table' AND name = 'gopact_runlog'`,
	).Scan(&afterRootPage).Error; err != nil {
		t.Fatal(err)
	}
	if afterRootPage != beforeRootPage {
		t.Fatalf("RunLog root page changed from %d to %d; migration rebuilt the BLOB table", beforeRootPage, afterRootPage)
	}
	var defaultValue sql.NullString
	if err := store.db.Raw(
		`SELECT dflt_value FROM pragma_table_info('gopact_runlog') WHERE name = 'session_id'`,
	).Scan(&defaultValue).Error; err != nil {
		t.Fatal(err)
	}
	if defaultValue.Valid {
		t.Fatalf("session_id default changed to %q; migration rewrote the legacy column", defaultValue.String)
	}
}

func TestConnectRejectsIncompleteV2Metadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metadata-gap.db")
	store, err := Open(sqlite.Open(path))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Append(t.Context(), runlog.Record{
		SessionID: "session-1", RunID: "run-1", Sequence: 1,
		EventType: "event", Source: "test", Timestamp: time.Now().UTC(),
	}); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open(sqlite.DriverName, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`DELETE FROM gopact_run_registry`); err != nil {
		raw.Close()
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	connected, err := Connect(sqlite.Open(path))
	if connected != nil {
		_ = connected.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "metadata") {
		t.Fatalf("Connect() error = %v, want v2 metadata-gap rejection", err)
	}
}

func TestConnectRejectsRunLogBelowCompactionFloor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "compaction-floor-gap.db")
	store, err := Open(sqlite.Open(path))
	if err != nil {
		t.Fatal(err)
	}
	record := testCheckpoint("compaction-floor-gap")
	if err := store.Create(t.Context(), record); err != nil {
		store.Close()
		t.Fatal(err)
	}
	event := runlog.Record{
		SessionID: record.SessionID, RunID: record.RunID, Sequence: 1,
		EventType: "event", Source: "test", Timestamp: time.Now().UTC(),
	}
	if err := store.AppendFenced(
		t.Context(),
		event,
		runlog.Fence{OwnerID: record.OwnerID, ClaimSequence: record.ClaimSequence},
	); err != nil {
		store.Close()
		t.Fatal(err)
	}
	record.ConfirmedSequence = 1
	if err := store.Save(t.Context(), record, record.Version); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if _, err := store.PurgeConfirmedRunLog(t.Context(), ConfirmedRunLogPurgeRequest{
		RunID: record.RunID, Before: time.Now().Add(time.Minute), AllowHistoryLoss: true,
	}); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err := store.db.Exec(`DROP TRIGGER gopact_runlog_compacted_guard`).Error; err != nil {
		store.Close()
		t.Fatal(err)
	}
	encoded, err := encodeRunLogRecord(event)
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	reintroduced := runLogRow{
		SessionID: event.SessionID, RunID: event.RunID, Sequence: event.Sequence, RecordJSON: encoded,
	}
	if err := store.db.Create(&reintroduced).Error; err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	connected, err := Connect(sqlite.Open(path))
	if connected != nil {
		_ = connected.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "compaction floor") {
		t.Fatalf("Connect() error = %v, want compaction-floor rejection", err)
	}
}

func TestConnectRejectsCriticalSQLiteSchemaDrift(t *testing.T) {
	tests := []struct {
		name      string
		mutateSQL []string
		want      string
	}{
		{
			name: "missing registry primary key",
			mutateSQL: []string{
				`DROP TABLE gopact_run_registry`,
				`CREATE TABLE gopact_run_registry (
					run_id TEXT NOT NULL, kind TEXT NOT NULL, state TEXT NOT NULL,
					updated_at_unix_nano INTEGER NOT NULL,
					compacted_through_sequence INTEGER NOT NULL DEFAULT 0
				)`,
				`CREATE INDEX gopact_run_registry_retention
					ON gopact_run_registry (state, updated_at_unix_nano)`,
			},
			want: "primary key",
		},
		{
			name: "nullable registry state",
			mutateSQL: []string{
				`DROP TABLE gopact_run_registry`,
				`CREATE TABLE gopact_run_registry (
					run_id TEXT NOT NULL PRIMARY KEY, kind TEXT NOT NULL, state TEXT,
					updated_at_unix_nano INTEGER NOT NULL,
					compacted_through_sequence INTEGER NOT NULL DEFAULT 0
				)`,
				`CREATE INDEX gopact_run_registry_retention
					ON gopact_run_registry (state, updated_at_unix_nano)`,
			},
			want: "NOT NULL",
		},
		{
			name: "wrong registry timestamp type",
			mutateSQL: []string{
				`DROP TABLE gopact_run_registry`,
				`CREATE TABLE gopact_run_registry (
					run_id TEXT NOT NULL PRIMARY KEY, kind TEXT NOT NULL, state TEXT NOT NULL,
					updated_at_unix_nano TEXT NOT NULL,
					compacted_through_sequence INTEGER NOT NULL DEFAULT 0
				)`,
				`CREATE INDEX gopact_run_registry_retention
					ON gopact_run_registry (state, updated_at_unix_nano)`,
			},
			want: "type",
		},
		{
			name: "RunLog ordinal is not auto incrementing",
			mutateSQL: []string{
				`DROP TABLE gopact_runlog`,
				`CREATE TABLE gopact_runlog (
					ordinal INTEGER PRIMARY KEY, session_id TEXT NOT NULL DEFAULT '',
					run_id TEXT NOT NULL, sequence INTEGER NOT NULL, record_json BLOB NOT NULL
				)`,
				`CREATE UNIQUE INDEX gopact_runlog_run_sequence ON gopact_runlog (run_id, sequence)`,
				`CREATE INDEX gopact_runlog_session_ordinal ON gopact_runlog (session_id, ordinal)`,
			},
			want: "auto-increment",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "drift.db")
			if err := Migrate(sqlite.Open(path)); err != nil {
				t.Fatal(err)
			}
			raw, err := sql.Open(sqlite.DriverName, path)
			if err != nil {
				t.Fatal(err)
			}
			for _, statement := range test.mutateSQL {
				if _, err := raw.Exec(statement); err != nil {
					raw.Close()
					t.Fatal(err)
				}
			}
			if err := raw.Close(); err != nil {
				t.Fatal(err)
			}
			store, err := Connect(sqlite.Open(path))
			if store != nil {
				_ = store.Close()
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Connect() error = %v, want %q schema rejection", err, test.want)
			}
		})
	}
}
