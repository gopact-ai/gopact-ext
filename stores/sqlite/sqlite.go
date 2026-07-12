// Package sqlite provides SQLite persistence for Workflow checkpoints and history.
package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/gopact-ai/gopact/runlog"
	"github.com/gopact-ai/gopact/workflow"
	_ "modernc.org/sqlite"
)

const (
	checkpointTable    = `gopact_workflow_checkpoints`
	eventTable         = `gopact_runlog`
	maxCheckpointBytes = 4 << 20
)

// Store persists Workflow checkpoints and run history in one SQLite database.
type Store struct{ db *sql.DB }

var (
	_ workflow.Checkpointer         = (*Store)(nil)
	_ workflow.CheckpointHistory    = (*Store)(nil)
	_ workflow.CheckpointController = (*Store)(nil)
	_ runlog.Log                    = (*Store)(nil)
)

// Open opens or creates a SQLite store at path.
func Open(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("sqlite: path is required")
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("sqlite: open: %w", err)
	}
	// ponytail: one connection keeps SQLite writes ordered; add a measured multi-connection policy only if throughput requires it.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	store := &Store{db: db}
	if err := store.initialize(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

// Close releases the database connection.
func (store *Store) Close() error {
	if store == nil || store.db == nil {
		return nil
	}
	return store.db.Close()
}

func (store *Store) initialize(ctx context.Context) error {
	statements := []string{
		`PRAGMA busy_timeout = 5000`,
		`PRAGMA journal_mode = WAL`,
		`CREATE TABLE IF NOT EXISTS ` + checkpointTable + ` (
			run_id TEXT NOT NULL,
			version INTEGER NOT NULL,
			record_json BLOB NOT NULL,
			payload BLOB NOT NULL,
			PRIMARY KEY (run_id, version)
		)`,
		`CREATE TABLE IF NOT EXISTS ` + eventTable + ` (
			ordinal INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL,
			run_id TEXT NOT NULL,
			sequence INTEGER NOT NULL,
			record_json BLOB NOT NULL,
			UNIQUE (run_id, sequence)
		)`,
	}
	for _, statement := range statements {
		if _, err := store.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("sqlite: initialize: %w", err)
		}
	}
	rows, err := store.db.QueryContext(ctx, `PRAGMA table_info(`+eventTable+`)`)
	if err != nil {
		return fmt.Errorf("sqlite: inspect runlog schema: %w", err)
	}
	hasSessionID := false
	for rows.Next() {
		var columnID, notNull, primaryKey int
		var name, dataType string
		var defaultValue sql.NullString
		if err := rows.Scan(&columnID, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			_ = rows.Close()
			return fmt.Errorf("sqlite: scan runlog schema: %w", err)
		}
		if name == "session_id" {
			hasSessionID = true
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("sqlite: inspect runlog schema rows: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("sqlite: close runlog schema rows: %w", err)
	}
	if !hasSessionID {
		if _, err := store.db.ExecContext(ctx, `ALTER TABLE `+eventTable+` ADD COLUMN session_id TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("sqlite: migrate runlog session id: %w", err)
		}
	}
	for _, statement := range []string{
		`CREATE INDEX IF NOT EXISTS gopact_runlog_session_ordinal ON ` + eventTable + ` (session_id, ordinal)`,
	} {
		if _, err := store.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("sqlite: initialize runlog index: %w", err)
		}
	}
	return nil
}

// Create stores a new running checkpoint.
func (store *Store) Create(ctx context.Context, record workflow.CheckpointRecord) error {
	if err := store.ready(ctx); err != nil {
		return err
	}
	if record.Version != 1 || record.Status != workflow.CheckpointRunning {
		return fmt.Errorf("%w: new checkpoint must be running at version one", workflow.ErrInvalidCheckpoint)
	}
	if err := validateCheckpoint(record); err != nil {
		return err
	}
	if err := insertCheckpoint(ctx, store.db, record); err != nil {
		if uniqueConstraint(err) {
			return workflow.ErrCheckpointExists
		}
		return fmt.Errorf("sqlite: create checkpoint: %w", err)
	}
	return nil
}

// Load returns the latest checkpoint for a Run.
func (store *Store) Load(ctx context.Context, runID string) (workflow.CheckpointRecord, error) {
	if err := store.ready(ctx); err != nil {
		return workflow.CheckpointRecord{}, err
	}
	if runID == "" {
		return workflow.CheckpointRecord{}, fmt.Errorf("%w: run id is required", workflow.ErrInvalidCheckpoint)
	}
	return loadCheckpoint(ctx, store.db, runID)
}

// Save writes a non-terminal checkpoint using version CAS.
func (store *Store) Save(ctx context.Context, record workflow.CheckpointRecord, version int64) error {
	return store.write(ctx, record, version, false, false)
}

// Finish writes a terminal checkpoint using version CAS.
func (store *Store) Finish(ctx context.Context, record workflow.CheckpointRecord, version int64) error {
	return store.write(ctx, record, version, true, false)
}

// Reopen starts an explicit control epoch from a terminal checkpoint.
func (store *Store) Reopen(ctx context.Context, record workflow.CheckpointRecord, version int64) error {
	return store.write(ctx, record, version, false, true)
}

func (store *Store) write(ctx context.Context, record workflow.CheckpointRecord, version int64, terminal, reopen bool) error {
	if err := store.ready(ctx); err != nil {
		return err
	}
	if version <= 0 || record.Version != version {
		return fmt.Errorf("%w: snapshot version %d, expected %d", workflow.ErrCheckpointConflict, record.Version, version)
	}
	if reopen {
		if record.Status != workflow.CheckpointRunning {
			return fmt.Errorf("%w: invalid reopened checkpoint", workflow.ErrInvalidCheckpoint)
		}
	} else if terminal != checkpointTerminal(record.Status) {
		return fmt.Errorf("%w: status %q does not match write operation", workflow.ErrInvalidCheckpoint, record.Status)
	}
	if err := validateCheckpoint(record); err != nil {
		return err
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlite: begin checkpoint write: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	current, err := loadCheckpoint(ctx, tx, record.RunID)
	if err != nil {
		return err
	}
	if current.Version != version || (reopen && !checkpointTerminal(current.Status)) || (!reopen && checkpointTerminal(current.Status)) {
		return workflow.ErrCheckpointConflict
	}
	if !sameCheckpointIdentity(current, record) {
		return workflow.ErrCheckpointMismatch
	}
	record.Version++
	if err := insertCheckpoint(ctx, tx, record); err != nil {
		if uniqueConstraint(err) || databaseBusy(err) {
			return workflow.ErrCheckpointConflict
		}
		return fmt.Errorf("sqlite: write checkpoint: %w", err)
	}
	if err := tx.Commit(); err != nil {
		if databaseBusy(err) {
			return workflow.ErrCheckpointConflict
		}
		return fmt.Errorf("sqlite: commit checkpoint: %w", err)
	}
	return nil
}

// ListCheckpoints returns immutable checkpoint versions in ascending order.
func (store *Store) ListCheckpoints(ctx context.Context, request workflow.CheckpointHistoryRequest) ([]workflow.CheckpointRecord, error) {
	if err := store.ready(ctx); err != nil {
		return nil, err
	}
	if request.RunID == "" || request.AfterVersion < 0 || request.Limit < 0 {
		return nil, fmt.Errorf("%w: invalid checkpoint history request", workflow.ErrInvalidCheckpoint)
	}
	query := `SELECT record_json, payload FROM ` + checkpointTable + ` WHERE run_id = ? AND version > ? ORDER BY version`
	args := []any{request.RunID, request.AfterVersion}
	if request.Limit > 0 {
		query += ` LIMIT ?`
		args = append(args, request.Limit)
	}
	rows, err := store.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list checkpoints: %w", err)
	}
	defer rows.Close()
	records := []workflow.CheckpointRecord{}
	for rows.Next() {
		record, err := scanCheckpoint(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: iterate checkpoints: %w", err)
	}
	if len(records) == 0 {
		var exists int
		err := store.db.QueryRowContext(
			ctx,
			`SELECT 1 FROM `+checkpointTable+` WHERE run_id = ? LIMIT 1`,
			request.RunID,
		).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, workflow.ErrCheckpointNotFound
		}
		if err != nil {
			return nil, fmt.Errorf("sqlite: check checkpoint history: %w", err)
		}
	}
	return records, nil
}

// Append appends a RunLog record idempotently by RunID and sequence.
func (store *Store) Append(ctx context.Context, record runlog.Record) error {
	if err := store.ready(ctx); err != nil {
		return err
	}
	if err := validateRunLogRecord(record); err != nil {
		return err
	}
	payload, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("sqlite: encode runlog record: %w", err)
	}
	_, err = store.db.ExecContext(
		ctx,
		`INSERT INTO `+eventTable+` (session_id, run_id, sequence, record_json) VALUES (?, ?, ?, ?)`,
		record.SessionID,
		record.RunID,
		record.Sequence,
		payload,
	)
	if err == nil {
		return nil
	}
	if !uniqueConstraint(err) {
		return fmt.Errorf("sqlite: append runlog: %w", err)
	}
	var existing []byte
	queryErr := store.db.QueryRowContext(
		ctx,
		`SELECT record_json FROM `+eventTable+` WHERE run_id = ? AND sequence = ?`,
		record.RunID,
		record.Sequence,
	).Scan(&existing)
	if queryErr != nil {
		return fmt.Errorf("sqlite: load conflicting runlog: %w", queryErr)
	}
	if bytes.Equal(existing, payload) {
		return nil
	}
	return fmt.Errorf("%w for %s/%d", runlog.ErrConflict, record.RunID, record.Sequence)
}

// List returns RunLog records in append order.
func (store *Store) List(ctx context.Context, query runlog.Query) ([]runlog.Record, error) {
	if err := store.ready(ctx); err != nil {
		return nil, err
	}
	if query.After < 0 || query.Limit < 0 {
		return nil, fmt.Errorf("%w: after and limit must not be negative", runlog.ErrInvalidQuery)
	}
	if query.SessionID != "" && query.RunID == "" && query.After != 0 {
		return nil, fmt.Errorf("%w: after requires a run id for session queries", runlog.ErrInvalidQuery)
	}
	statement := `SELECT record_json FROM ` + eventTable + ` WHERE 1 = 1`
	args := []any{}
	if query.SessionID != "" {
		statement += ` AND session_id = ?`
		args = append(args, query.SessionID)
	}
	if query.RunID != "" {
		statement += ` AND run_id = ?`
		args = append(args, query.RunID)
	}
	statement += ` AND sequence > ?`
	args = append(args, query.After)
	statement += ` ORDER BY ordinal`
	if query.Limit > 0 {
		statement += ` LIMIT ?`
		args = append(args, query.Limit)
	}
	rows, err := store.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list runlog: %w", err)
	}
	defer rows.Close()
	records := []runlog.Record{}
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, fmt.Errorf("sqlite: scan runlog: %w", err)
		}
		var record runlog.Record
		if err := json.Unmarshal(payload, &record); err != nil {
			return nil, fmt.Errorf("sqlite: decode runlog: %w", err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: iterate runlog: %w", err)
	}
	return records, nil
}

type sqlExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

type sqlQueryRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type scanner interface{ Scan(...any) error }

func insertCheckpoint(ctx context.Context, executor sqlExecutor, record workflow.CheckpointRecord) error {
	metadata := record
	metadata.Payload = nil
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("sqlite: encode checkpoint: %w", err)
	}
	_, err = executor.ExecContext(
		ctx,
		`INSERT INTO `+checkpointTable+` (run_id, version, record_json, payload) VALUES (?, ?, ?, ?)`,
		record.RunID,
		record.Version,
		encoded,
		record.Payload,
	)
	return err
}

func loadCheckpoint(ctx context.Context, queryer sqlQueryRower, runID string) (workflow.CheckpointRecord, error) {
	row := queryer.QueryRowContext(
		ctx,
		`SELECT record_json, payload FROM `+checkpointTable+` WHERE run_id = ? ORDER BY version DESC LIMIT 1`,
		runID,
	)
	record, err := scanCheckpoint(row)
	if errors.Is(err, sql.ErrNoRows) {
		return workflow.CheckpointRecord{}, workflow.ErrCheckpointNotFound
	}
	return record, err
}

func scanCheckpoint(row scanner) (workflow.CheckpointRecord, error) {
	var metadata, payload []byte
	if err := row.Scan(&metadata, &payload); err != nil {
		return workflow.CheckpointRecord{}, err
	}
	var record workflow.CheckpointRecord
	if err := json.Unmarshal(metadata, &record); err != nil {
		return workflow.CheckpointRecord{}, fmt.Errorf("sqlite: decode checkpoint: %w", err)
	}
	record.Payload = append([]byte(nil), payload...)
	return record, nil
}

func (store *Store) ready(ctx context.Context) error {
	if store == nil || store.db == nil {
		return errors.New("sqlite: store is nil")
	}
	return ctx.Err()
}

func validateCheckpoint(record workflow.CheckpointRecord) error {
	if record.ID == "" || record.SessionID == "" || record.RunID == "" || record.WorkflowName == "" || record.TopologyVersion == "" || record.SchemaVersion <= 0 {
		return fmt.Errorf("%w: checkpoint identity is incomplete", workflow.ErrInvalidCheckpoint)
	}
	if record.ConfirmedSequence < 0 || record.PendingSequence < 0 || len(record.Payload) == 0 || len(record.Payload) > maxCheckpointBytes {
		return fmt.Errorf("%w: checkpoint payload or sequence is invalid", workflow.ErrInvalidCheckpoint)
	}
	if record.CreatedAt.IsZero() || record.UpdatedAt.IsZero() {
		return fmt.Errorf("%w: checkpoint timestamps are required", workflow.ErrInvalidCheckpoint)
	}
	switch record.Status {
	case workflow.CheckpointRunning, workflow.CheckpointInterrupted, workflow.CheckpointCompleted,
		workflow.CheckpointFailed, workflow.CheckpointCanceled, workflow.CheckpointTerminated:
	default:
		return fmt.Errorf("%w: checkpoint status %q", workflow.ErrInvalidCheckpoint, record.Status)
	}
	switch record.ReplayStatus {
	case workflow.ReplayUnknown, workflow.ReplaySafe, workflow.ReplayUnsafe:
		return nil
	default:
		return fmt.Errorf("%w: replay status %q", workflow.ErrInvalidCheckpoint, record.ReplayStatus)
	}
}

func validateRunLogRecord(record runlog.Record) error {
	switch {
	case record.SessionID == "":
		return fmt.Errorf("%w: session id is required", runlog.ErrInvalidRecord)
	case record.RunID == "":
		return fmt.Errorf("%w: run id is required", runlog.ErrInvalidRecord)
	case record.Sequence <= 0:
		return fmt.Errorf("%w: sequence must be positive", runlog.ErrInvalidRecord)
	case record.EventType == "":
		return fmt.Errorf("%w: event type is required", runlog.ErrInvalidRecord)
	case record.Source == "":
		return fmt.Errorf("%w: source is required", runlog.ErrInvalidRecord)
	case record.Timestamp.IsZero():
		return fmt.Errorf("%w: timestamp is required", runlog.ErrInvalidRecord)
	case (record.SourceRunID == "") != (record.SourceEventSeq == 0):
		return fmt.Errorf("%w: fork source run and event sequence must be set together", runlog.ErrInvalidRecord)
	case record.SourceEventSeq < 0:
		return fmt.Errorf("%w: source event sequence must not be negative", runlog.ErrInvalidRecord)
	default:
		return nil
	}
}

func sameCheckpointIdentity(left, right workflow.CheckpointRecord) bool {
	return left.ID == right.ID && left.SessionID == right.SessionID && left.RunID == right.RunID && left.WorkflowName == right.WorkflowName &&
		left.TopologyVersion == right.TopologyVersion && left.SchemaVersion == right.SchemaVersion
}

func checkpointTerminal(status workflow.CheckpointStatus) bool {
	return status == workflow.CheckpointCompleted || status == workflow.CheckpointFailed ||
		status == workflow.CheckpointCanceled || status == workflow.CheckpointTerminated
}

func uniqueConstraint(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "unique constraint failed")
}

func databaseBusy(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "database is locked") || strings.Contains(message, "database is busy")
}
