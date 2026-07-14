// Package sqlite provides pure-Go SQLite persistence for workflow checkpoints and history.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/gopact-ai/gopact-ext/stores/dbstore"
	"github.com/gopact-ai/gopact/workflow"
	gormsqlite "github.com/libtnb/sqlite"
)

const (
	checkpointTable          = "gopact_workflow_checkpoints"
	eventTable               = "gopact_runlog"
	runTable                 = "gopact_workflow_runs"
	migrationOpenConnections = 2
)

// Store is a dbstore backed by one SQLite database file.
type Store struct {
	*dbstore.Store
	db *sql.DB
}

var _ workflow.Store = (*Store)(nil)

// PurgeRequest selects terminal workflow runs for deletion.
type PurgeRequest = dbstore.PurgeRequest

// PurgeResult reports rows deleted by PurgeTerminalRuns.
type PurgeResult = dbstore.PurgeResult

// RunLogPurgeRequest selects journal-only events by Store append time.
type RunLogPurgeRequest = dbstore.RunLogPurgeRequest

// RunLogPurgeResult reports rows deleted by PurgeRunLog.
type RunLogPurgeResult = dbstore.RunLogPurgeResult

// ConfirmedRunLogPurgeRequest selects an active Workflow's confirmed RunLog prefix.
type ConfirmedRunLogPurgeRequest = dbstore.ConfirmedRunLogPurgeRequest

// ConfirmedRunLogPurgeResult reports the compacted Workflow RunLog prefix.
type ConfirmedRunLogPurgeResult = dbstore.ConfirmedRunLogPurgeResult

// Migrate creates or upgrades a SQLite Store schema. Existing WAL databases
// are checkpointed and converted before migration.
func Migrate(path string) error {
	return MigrateContext(context.Background(), path)
}

// MigrateContext creates or upgrades a SQLite Store schema using ctx.
func MigrateContext(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if path == "" {
		return errors.New("sqlite: path is required")
	}
	memory := inMemoryDSN(path)
	if mode, unsupported := unsupportedJournalMode(path, memory); unsupported {
		return fmt.Errorf(
			"sqlite: Migrate requires journal_mode=DELETE for file databases; %s is not supported",
			mode,
		)
	}
	db, err := sql.Open(gormsqlite.DriverName, sqliteDSN(path))
	if err != nil {
		return fmt.Errorf("sqlite: open migration connection: %w", err)
	}
	// libtnb's SQLite migrator may temporarily reserve one connection while it
	// introspects a table through another. Permit that only during Migrate; dbstore
	// restores the steady-state single-connection pool after migration.
	db.SetMaxOpenConns(migrationOpenConnections)
	db.SetMaxIdleConns(1)
	if err := prepareRollbackJournal(ctx, db, memory); err != nil {
		_ = db.Close()
		return fmt.Errorf("sqlite: prepare rollback journal: %w", err)
	}
	if err := dbstore.MigrateContext(ctx, gormsqlite.New(gormsqlite.Config{
		DriverName: gormsqlite.DriverName,
		DSN:        path,
		Conn:       db,
	})); err != nil {
		_ = db.Close()
		return fmt.Errorf("sqlite: migrate: %w", err)
	}
	return nil
}

// Open connects to a SQLite database whose Store schema has already been
// migrated. In-memory databases are initialized during Open because their
// schema cannot survive a separate migration connection.
func Open(path string) (*Store, error) {
	return OpenContext(context.Background(), path)
}

// OpenContext connects to a SQLite database using ctx.
func OpenContext(ctx context.Context, path string) (*Store, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if path == "" {
		return nil, errors.New("sqlite: path is required")
	}
	memory := inMemoryDSN(path)
	if mode, unsupported := unsupportedJournalMode(path, memory); unsupported {
		return nil, fmt.Errorf(
			"sqlite: Open requires journal_mode=DELETE for file databases; %s is not supported",
			mode,
		)
	}
	db, err := sql.Open(gormsqlite.DriverName, sqliteDSN(path))
	if err != nil {
		return nil, fmt.Errorf("sqlite: open connection: %w", err)
	}
	var shared *dbstore.Store
	if memory {
		db.SetMaxOpenConns(migrationOpenConnections)
		db.SetMaxIdleConns(1)
		shared, err = dbstore.OpenContext(ctx, gormsqlite.New(gormsqlite.Config{
			DriverName: gormsqlite.DriverName,
			DSN:        path,
			Conn:       db,
		}))
	} else {
		if err = verifyRollbackJournal(ctx, db); err == nil {
			shared, err = dbstore.ConnectContext(ctx, gormsqlite.New(gormsqlite.Config{
				DriverName: gormsqlite.DriverName,
				DSN:        path,
				Conn:       db,
			}))
		}
	}
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlite: open: %w", err)
	}
	sharedDB, err := shared.SQLDB()
	if err != nil {
		_ = shared.Close()
		return nil, fmt.Errorf("sqlite: access connection: %w", err)
	}
	return &Store{Store: shared, db: sharedDB}, nil
}

func verifyRollbackJournal(ctx context.Context, db *sql.DB) error {
	var mode string
	if err := db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		return fmt.Errorf("read journal mode: %w", err)
	}
	if !strings.EqualFold(mode, "delete") {
		return fmt.Errorf("journal mode is %q; run sqlite.Migrate while other users are stopped", mode)
	}
	return nil
}

func sqliteDSN(path string) string {
	separator := "?"
	if strings.Contains(path, "?") {
		separator = "&"
	}
	return path + separator +
		"_texttotime=1&_inttotime=1&_time_format=sqlite&_pragma=busy_timeout(5000)"
}

func unsupportedJournalMode(path string, memory bool) (string, bool) {
	queryIndex := strings.IndexByte(path, '?')
	if queryIndex < 0 {
		return "", false
	}
	values, err := url.ParseQuery(path[queryIndex+1:])
	if err != nil {
		return "invalid", true
	}
	for key, pragmas := range values {
		if !strings.EqualFold(key, "_pragma") {
			continue
		}
		if mode, unsupported := unsupportedPragmaMode(pragmas, memory); unsupported {
			return mode, true
		}
	}
	return "", false
}

func unsupportedPragmaMode(pragmas []string, memory bool) (string, bool) {
	for _, pragma := range pragmas {
		mode, journalMode := pragmaJournalMode(pragma)
		if !journalMode {
			continue
		}
		if mode == "delete" || (memory && mode == "memory") {
			continue
		}
		if mode == "" {
			mode = "unknown"
		}
		return mode, true
	}
	return "", false
}

func pragmaJournalMode(pragma string) (string, bool) {
	normalized := strings.ToLower(strings.ReplaceAll(pragma, " ", ""))
	index := strings.Index(normalized, "journal_mode")
	if index < 0 {
		return "", false
	}
	return strings.Trim(normalized[index+len("journal_mode"):], "=()"), true
}

func inMemoryDSN(path string) bool {
	base := path
	if queryIndex := strings.IndexByte(path, '?'); queryIndex >= 0 {
		base = path[:queryIndex]
		values, err := url.ParseQuery(path[queryIndex+1:])
		if err == nil && strings.EqualFold(values.Get("mode"), "memory") {
			return true
		}
	}
	return strings.EqualFold(base, ":memory:") || strings.EqualFold(base, "file::memory:")
}

func prepareRollbackJournal(ctx context.Context, db *sql.DB, memory bool) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "PRAGMA busy_timeout = 5000"); err != nil {
		return fmt.Errorf("set busy timeout: %w", err)
	}
	var mode string
	if err := conn.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		return fmt.Errorf("read journal mode: %w", err)
	}
	if strings.EqualFold(mode, "memory") && memory {
		return nil
	}
	if strings.EqualFold(mode, "wal") {
		var busy, logFrames, checkpointedFrames int
		if err := conn.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").
			Scan(&busy, &logFrames, &checkpointedFrames); err != nil {
			return fmt.Errorf("checkpoint WAL: %w", err)
		}
		if busy != 0 {
			return fmt.Errorf(
				"checkpoint WAL is busy (%d log frames, %d checkpointed); stop other SQLite users and retry",
				logFrames,
				checkpointedFrames,
			)
		}
	}
	if err := conn.QueryRowContext(ctx, "PRAGMA journal_mode=DELETE").Scan(&mode); err != nil {
		return fmt.Errorf("set journal mode DELETE: %w", err)
	}
	if !strings.EqualFold(mode, "delete") {
		return fmt.Errorf("set journal mode DELETE returned %q", mode)
	}
	if err := conn.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		return fmt.Errorf("verify journal mode: %w", err)
	}
	if !strings.EqualFold(mode, "delete") {
		return fmt.Errorf("journal mode is %q after rollback conversion", mode)
	}
	return nil
}
