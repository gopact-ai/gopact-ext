package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/gopact-ai/gopact/workflow"
)

const (
	runTable          = `gopact_workflow_runs`
	backfillBatchSize = 256
	defaultPurgeLimit = 100
	maxPurgeLimit     = 1000
)

var errRunHeadConflict = errors.New("sqlite: run head conflict")

// PurgeRequest selects terminal workflow runs older than Before for deletion.
type PurgeRequest struct {
	Before time.Time
	Limit  int
}

// PurgeResult reports rows deleted by PurgeTerminalRuns.
type PurgeResult struct {
	Runs        int
	Checkpoints int64
	Events      int64
}

// PurgeTerminalRuns deletes a bounded batch of terminal workflow runs and all
// of their checkpoint and RunLog history in one transaction.
func (store *Store) PurgeTerminalRuns(ctx context.Context, request PurgeRequest) (PurgeResult, error) {
	if err := store.ready(ctx); err != nil {
		return PurgeResult{}, err
	}
	if request.Before.IsZero() {
		return PurgeResult{}, errors.New("sqlite: purge before time is required")
	}
	if request.Limit < 0 || request.Limit > maxPurgeLimit {
		return PurgeResult{}, fmt.Errorf("sqlite: purge limit must be between 0 and %d", maxPurgeLimit)
	}
	limit := request.Limit
	if limit == 0 {
		limit = defaultPurgeLimit
	}

	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return PurgeResult{}, ctxErr
		}
		return PurgeResult{}, fmt.Errorf("sqlite: begin terminal run purge: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	runIDs, err := terminalRunIDs(ctx, tx, request.Before, limit)
	if err != nil {
		return PurgeResult{}, err
	}
	result := PurgeResult{}
	for _, runID := range runIDs {
		deleted, err := deleteTerminalRun(ctx, tx, runID, request.Before)
		if err != nil {
			return PurgeResult{}, err
		}
		if !deleted {
			continue
		}
		result.Runs++

		result.Events, err = deleteRows(ctx, tx, eventTable, runID, result.Events)
		if err != nil {
			return PurgeResult{}, err
		}
		result.Checkpoints, err = deleteRows(ctx, tx, checkpointTable, runID, result.Checkpoints)
		if err != nil {
			return PurgeResult{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return PurgeResult{}, err
	}
	if err := tx.Commit(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return PurgeResult{}, ctxErr
		}
		return PurgeResult{}, fmt.Errorf("sqlite: commit terminal run purge: %w", err)
	}
	return result, nil
}

func terminalRunIDs(ctx context.Context, tx *sql.Tx, before time.Time, limit int) ([]string, error) {
	rows, err := tx.QueryContext(
		ctx,
		`SELECT
			runs.run_id,
			runs.session_id,
			runs.status,
			runs.version,
			runs.created_at_unix_nano,
			runs.updated_at_unix_nano,
			checkpoints.record_json
		FROM `+runTable+` AS runs
			JOIN `+checkpointTable+` AS checkpoints
				ON checkpoints.run_id = runs.run_id AND checkpoints.version = runs.version
			WHERE runs.status IN (?, ?, ?, ?) AND runs.updated_at_unix_nano < ?
			AND NOT EXISTS (
				SELECT 1 FROM `+checkpointTable+` AS newer
				WHERE newer.run_id = runs.run_id AND newer.version > runs.version
			)
			ORDER BY runs.updated_at_unix_nano, runs.run_id LIMIT ?`,
		workflow.CheckpointCompleted,
		workflow.CheckpointFailed,
		workflow.CheckpointCanceled,
		workflow.CheckpointTerminated,
		before.UnixNano(),
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("sqlite: select terminal runs for purge: %w", err)
	}
	runIDs := make([]string, 0, limit)
	for rows.Next() {
		var runID, sessionID, status string
		var version, createdAt, updatedAt int64
		var encoded []byte
		if err := rows.Scan(&runID, &sessionID, &status, &version, &createdAt, &updatedAt, &encoded); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("sqlite: scan terminal run for purge: %w", err)
		}
		var checkpoint workflow.CheckpointRecord
		if err := json.Unmarshal(encoded, &checkpoint); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("sqlite: decode terminal run %q for purge: %w", runID, err)
		}
		if checkpoint.RunID != runID || checkpoint.SessionID != sessionID || string(checkpoint.Status) != status ||
			checkpoint.Version != version || checkpoint.CreatedAt.UnixNano() != createdAt || checkpoint.UpdatedAt.UnixNano() != updatedAt {
			_ = rows.Close()
			return nil, fmt.Errorf("sqlite: terminal run head %q does not match its latest checkpoint", runID)
		}
		runIDs = append(runIDs, runID)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("sqlite: iterate terminal runs for purge: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("sqlite: close terminal runs for purge: %w", err)
	}
	return runIDs, nil
}

func deleteTerminalRun(ctx context.Context, tx *sql.Tx, runID string, before time.Time) (bool, error) {
	result, err := tx.ExecContext(
		ctx,
		`DELETE FROM `+runTable+`
			WHERE run_id = ? AND status IN (?, ?, ?, ?) AND updated_at_unix_nano < ?
			AND EXISTS (
				SELECT 1 FROM `+checkpointTable+` AS current
				WHERE current.run_id = `+runTable+`.run_id AND current.version = `+runTable+`.version
			)
			AND NOT EXISTS (
				SELECT 1 FROM `+checkpointTable+` AS newer
				WHERE newer.run_id = `+runTable+`.run_id AND newer.version > `+runTable+`.version
			)`,
		runID,
		workflow.CheckpointCompleted,
		workflow.CheckpointFailed,
		workflow.CheckpointCanceled,
		workflow.CheckpointTerminated,
		before.UnixNano(),
	)
	if err != nil {
		return false, fmt.Errorf("sqlite: delete terminal run head: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("sqlite: count deleted terminal run heads: %w", err)
	}
	return affected == 1, nil
}

func deleteRows(ctx context.Context, tx *sql.Tx, table, runID string, total int64) (int64, error) {
	result, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE run_id = ?`, runID)
	if err != nil {
		return 0, fmt.Errorf("sqlite: delete %s rows: %w", table, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("sqlite: count deleted %s rows: %w", table, err)
	}
	return total + affected, nil
}

func (store *Store) backfillRunHeads(ctx context.Context) error {
	afterRunID := ""
	for {
		records, err := store.loadRunHeadBackfillBatch(ctx, afterRunID)
		if err != nil {
			return err
		}
		if len(records) == 0 {
			return nil
		}

		tx, err := store.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("sqlite: begin run head backfill: %w", err)
		}
		for _, record := range records {
			if _, err := upsertRunHead(ctx, tx, record); err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("sqlite: backfill run head %q: %w", record.RunID, err)
			}
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("sqlite: commit run head backfill: %w", err)
		}
		afterRunID = records[len(records)-1].RunID
		if len(records) < backfillBatchSize {
			return nil
		}
	}
}

func (store *Store) loadRunHeadBackfillBatch(ctx context.Context, afterRunID string) ([]workflow.CheckpointRecord, error) {
	rows, err := store.db.QueryContext(
		ctx,
		`SELECT checkpoints.run_id, checkpoints.version, checkpoints.record_json
			FROM `+checkpointTable+` AS checkpoints
			LEFT JOIN `+runTable+` AS runs ON runs.run_id = checkpoints.run_id
			WHERE checkpoints.run_id > ?
			AND checkpoints.version = (
				SELECT MAX(latest.version)
				FROM `+checkpointTable+` AS latest
				WHERE latest.run_id = checkpoints.run_id
			)
			AND (runs.run_id IS NULL OR runs.version < checkpoints.version)
			ORDER BY checkpoints.run_id
			LIMIT ?`,
		afterRunID,
		backfillBatchSize,
	)
	if err != nil {
		return nil, fmt.Errorf("sqlite: select run heads for backfill: %w", err)
	}
	records := make([]workflow.CheckpointRecord, 0, backfillBatchSize)
	for rows.Next() {
		var runID string
		var version int64
		var encoded []byte
		if err := rows.Scan(&runID, &version, &encoded); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("sqlite: scan run head for backfill: %w", err)
		}
		var record workflow.CheckpointRecord
		if err := json.Unmarshal(encoded, &record); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("sqlite: decode run head for backfill: %w", err)
		}
		if record.RunID != runID || record.Version != version {
			_ = rows.Close()
			return nil, fmt.Errorf("sqlite: checkpoint metadata does not match run head source %q version %d", runID, version)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("sqlite: iterate run heads for backfill: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("sqlite: close run heads for backfill: %w", err)
	}
	return records, nil
}

func insertCheckpointAndHead(ctx context.Context, tx *sql.Tx, record workflow.CheckpointRecord) error {
	if err := insertCheckpoint(ctx, tx, record); err != nil {
		return err
	}
	updated, err := upsertRunHead(ctx, tx, record)
	if err != nil {
		return err
	}
	if !updated {
		return errRunHeadConflict
	}
	return nil
}

func upsertRunHead(ctx context.Context, executor sqlExecutor, record workflow.CheckpointRecord) (bool, error) {
	result, err := executor.ExecContext(
		ctx,
		`INSERT INTO `+runTable+` (
			run_id, session_id, status, version, created_at_unix_nano, updated_at_unix_nano
		) SELECT ?, ?, ?, ?, ?, ?
		WHERE EXISTS (
			SELECT 1 FROM `+checkpointTable+` AS source
			WHERE source.run_id = ? AND source.version = ?
			AND NOT EXISTS (
				SELECT 1 FROM `+checkpointTable+` AS newer
				WHERE newer.run_id = source.run_id AND newer.version > source.version
			)
		)
		ON CONFLICT(run_id) DO UPDATE SET
			session_id = excluded.session_id,
			status = excluded.status,
			version = excluded.version,
			created_at_unix_nano = excluded.created_at_unix_nano,
			updated_at_unix_nano = excluded.updated_at_unix_nano
		WHERE excluded.version > `+runTable+`.version`,
		record.RunID,
		record.SessionID,
		record.Status,
		record.Version,
		record.CreatedAt.UnixNano(),
		record.UpdatedAt.UnixNano(),
		record.RunID,
		record.Version,
	)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected == 1, nil
}
