package dbstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/gopact-ai/gopact/workflow"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	backfillBatchSize     = 256
	defaultPurgeLimit     = 100
	maxPurgeLimit         = 1000
	defaultPurgeRowLimit  = 1000
	maxPurgeRowLimit      = 10000
	defaultPurgeByteLimit = int64(64 << 20)
	maxPurgeByteLimit     = int64(1 << 30)
)

// PurgeRequest selects terminal workflow runs older than Before for deletion.
// RowLimit counts logical checkpoint/event history rows and ByteLimit counts
// encoded record and payload bytes as a hard upper bound; neither is a physical WAL/redo limit.
type PurgeRequest struct {
	Before    time.Time
	Limit     int
	RowLimit  int
	ByteLimit int64
}

// PurgeResult reports rows deleted by PurgeTerminalRuns.
type PurgeResult struct {
	Runs        int
	Checkpoints int64
	Events      int64
	Pending     int64
}

type purgeBudget struct {
	rows  int
	bytes int64
}

type historyDeletion struct {
	events      int64
	checkpoints int64
	bytes       int64
	complete    bool
}

type historyPart struct {
	rows  int64
	bytes int64
}

// PurgeTerminalRuns logically retires terminal workflow runs and deletes their
// history in transactions bounded by RowLimit. A permanent tombstone rejects
// late appends after a run has been selected.
func (store *Store) PurgeTerminalRuns(ctx context.Context, request PurgeRequest) (PurgeResult, error) {
	if err := store.ready(ctx); err != nil {
		return PurgeResult{}, err
	}
	if request.Before.IsZero() {
		return PurgeResult{}, errors.New("dbstore: purge before time is required")
	}
	if request.Limit < 0 || request.Limit > maxPurgeLimit {
		return PurgeResult{}, fmt.Errorf("dbstore: purge limit must be between 0 and %d", maxPurgeLimit)
	}
	if request.RowLimit < 0 || request.RowLimit > maxPurgeRowLimit {
		return PurgeResult{}, fmt.Errorf("dbstore: purge row limit must be between 0 and %d", maxPurgeRowLimit)
	}
	if request.ByteLimit < 0 || request.ByteLimit > maxPurgeByteLimit {
		return PurgeResult{}, fmt.Errorf("dbstore: purge byte limit must be between 0 and %d", maxPurgeByteLimit)
	}
	limit := request.Limit
	if limit == 0 {
		limit = defaultPurgeLimit
	}
	rowLimit := request.RowLimit
	if rowLimit == 0 {
		rowLimit = defaultPurgeRowLimit
	}
	byteLimit := request.ByteLimit
	if byteLimit == 0 {
		byteLimit = defaultPurgeByteLimit
	}
	result := PurgeResult{}
	pending, err := store.purgingRunIDs(ctx, limit)
	if err != nil {
		return result, err
	}
	if capacity := limit - len(pending); capacity > 0 {
		candidates, err := store.terminalRunIDs(ctx, request.Before, capacity)
		if err != nil {
			return result, err
		}
		if err := store.enrollTerminalRuns(
			ctx, candidates, request.Before, purgeBudget{rows: rowLimit, bytes: byteLimit},
		); err != nil {
			return result, err
		}
	}
	pending, err = store.purgingRunIDs(ctx, limit)
	if err != nil {
		return result, err
	}
	remaining := rowLimit
	remainingBytes := byteLimit
	for _, runID := range pending {
		deleted, err := store.deletePurgingHistory(ctx, runID, purgeBudget{rows: remaining, bytes: remainingBytes})
		result.Events += deleted.events
		result.Checkpoints += deleted.checkpoints
		remaining -= int(deleted.events + deleted.checkpoints)
		remainingBytes -= deleted.bytes
		if deleted.complete {
			result.Runs++
		}
		if err != nil {
			return result, contextOrError(ctx, err)
		}
		if remaining <= 0 || remainingBytes <= 0 {
			break
		}
	}
	if err := store.db.WithContext(ctx).Model(&runRegistryRow{}).
		Where("kind = ? AND state = ?", runKindWorkflow, runStatePurging).
		Count(&result.Pending).Error; err != nil {
		return result, fmt.Errorf("dbstore: count pending purges: %w", err)
	}
	return result, nil
}

func (store *Store) enrollTerminalRuns(ctx context.Context, runIDs []string, before time.Time, budget purgeBudget) error {
	for _, runID := range runIDs {
		fits, err := store.firstHistoryRowFits(ctx, runID, budget)
		if err != nil {
			return err
		}
		if !fits {
			break
		}
		if _, err := store.enrollTerminalRun(ctx, runID, before); err != nil {
			return err
		}
	}
	return nil
}

func (store *Store) firstHistoryRowFits(ctx context.Context, runID string, budget purgeBudget) (bool, error) {
	if budget.rows <= 0 || budget.bytes <= 0 {
		return false, nil
	}
	db := store.db.WithContext(ctx)
	candidates, err := store.runLogDeletionCandidates(db, runID, 1)
	if err != nil {
		return false, fmt.Errorf("dbstore: inspect terminal RunLog byte size: %w", err)
	}
	if len(candidates) == 0 {
		candidates, err = store.checkpointDeletionCandidates(db, runID, 1)
		if err != nil {
			return false, fmt.Errorf("dbstore: inspect terminal checkpoint byte size: %w", err)
		}
	}
	return len(candidates) == 0 || candidates[0].ByteSize <= budget.bytes, nil
}

func (store *Store) purgingRunIDs(ctx context.Context, limit int) ([]string, error) {
	var runIDs []string
	err := store.db.WithContext(ctx).Model(&runRegistryRow{}).
		Where("kind = ? AND state = ?", runKindWorkflow, runStatePurging).
		Order("updated_at_unix_nano, run_id").Limit(limit).Pluck("run_id", &runIDs).Error
	if err != nil {
		return nil, fmt.Errorf("dbstore: select pending purges: %w", err)
	}
	return runIDs, nil
}

func (store *Store) terminalRunIDs(ctx context.Context, before time.Time, limit int) ([]string, error) {
	var runIDs []string
	err := store.db.WithContext(ctx).Raw(
		`SELECT runs.run_id
			FROM `+runTable+` AS runs
			JOIN `+registryTable+` AS registry ON registry.run_id = runs.run_id
			WHERE registry.kind = ? AND registry.state = ?
			AND runs.status IN (?, ?, ?, ?) AND runs.updated_at_unix_nano < ?
			AND NOT EXISTS (
				SELECT 1 FROM `+checkpointTable+` AS newer
				WHERE newer.run_id = runs.run_id AND newer.version > runs.version
			)
			ORDER BY runs.updated_at_unix_nano, runs.run_id LIMIT ?`,
		runKindWorkflow,
		runStateActive,
		workflow.CheckpointCompleted,
		workflow.CheckpointFailed,
		workflow.CheckpointCanceled,
		workflow.CheckpointTerminated,
		before.UnixNano(),
		limit,
	).Scan(&runIDs).Error
	if err != nil {
		return nil, fmt.Errorf("dbstore: select terminal runs for purge: %w", err)
	}
	return runIDs, nil
}

func (store *Store) enrollTerminalRun(ctx context.Context, runID string, before time.Time) (bool, error) {
	enrolled := false
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		registry, found, err := store.lockRunRegistry(tx, runID)
		if err != nil {
			return err
		}
		if !found || registry.Kind != runKindWorkflow {
			return fmt.Errorf("dbstore: workflow run %q has no workflow registry", runID)
		}
		if registry.State != runStateActive {
			return nil
		}
		locked, err := store.lockRunHead(tx, runID)
		if err != nil || !locked {
			return err
		}
		var head runRow
		if err := tx.Where("run_id = ?", runID).Take(&head).Error; err != nil {
			return err
		}
		checkpoint, err := loadCheckpointMetadata(tx, runID)
		if err != nil {
			return err
		}
		if head.Version != checkpoint.Version {
			return nil
		}
		if !runHeadMatchesCheckpoint(head, checkpoint) {
			return fmt.Errorf("dbstore: terminal run head %q does not match its latest checkpoint", runID)
		}
		if !checkpointTerminal(checkpoint.Status) || head.UpdatedAtUnixNano >= before.UnixNano() {
			return nil
		}
		result := tx.Model(&runRegistryRow{}).
			Where("run_id = ? AND state = ?", runID, runStateActive).
			Updates(map[string]any{"state": runStatePurging, "updated_at_unix_nano": store.nowUTC().UnixNano()})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return nil
		}
		result = tx.Where("run_id = ? AND version = ?", runID, head.Version).Delete(&runRow{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("dbstore: terminal run head changed while enrolling purge")
		}
		enrolled = true
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("dbstore: enroll terminal run %q for purge: %w", runID, err)
	}
	return enrolled, nil
}

func loadCheckpointMetadata(tx *gorm.DB, runID string) (workflow.CheckpointRecord, error) {
	var row checkpointRow
	err := tx.Select("record_json").Where("run_id = ?", runID).Order("version DESC").Take(&row).Error
	if err != nil {
		return workflow.CheckpointRecord{}, err
	}
	var record workflow.CheckpointRecord
	if err := json.Unmarshal(row.RecordJSON, &record); err != nil {
		return workflow.CheckpointRecord{}, fmt.Errorf("decode terminal checkpoint: %w", err)
	}
	return record, nil
}

func runHeadMatchesCheckpoint(head runRow, checkpoint workflow.CheckpointRecord) bool {
	return head.RunID == checkpoint.RunID && head.SessionID == checkpoint.SessionID &&
		head.Status == string(checkpoint.Status) && head.Version == checkpoint.Version &&
		head.CreatedAtUnixNano == checkpoint.CreatedAt.UnixNano() &&
		head.UpdatedAtUnixNano == checkpoint.UpdatedAt.UnixNano()
}

func (store *Store) deletePurgingHistory(ctx context.Context, runID string, budget purgeBudget) (historyDeletion, error) {
	deleted := historyDeletion{}
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		registry, found, err := store.lockRunRegistry(tx, runID)
		if err != nil {
			return err
		}
		if !found || registry.Kind != runKindWorkflow || registry.State != runStatePurging {
			return nil
		}
		events, err := store.deleteRunLogs(tx, runID, budget)
		if err != nil {
			return err
		}
		deleted.events = events.rows
		deleted.bytes = events.bytes
		budget.rows -= int(events.rows)
		budget.bytes -= events.bytes
		checkpoints, err := store.deleteCheckpoints(tx, runID, budget)
		if err != nil {
			return err
		}
		deleted.checkpoints = checkpoints.rows
		deleted.bytes += checkpoints.bytes
		historyExists, err := hasRunRows(tx, &runLogRow{}, runID)
		if err != nil {
			return err
		}
		if !historyExists {
			historyExists, err = hasRunRows(tx, &checkpointRow{}, runID)
			if err != nil {
				return err
			}
		}
		if historyExists {
			return nil
		}
		result := tx.Model(&runRegistryRow{}).Where("run_id = ? AND state = ?", runID, runStatePurging).
			Updates(map[string]any{"state": runStatePurged, "updated_at_unix_nano": store.nowUTC().UnixNano()})
		if result.Error != nil {
			return result.Error
		}
		deleted.complete = result.RowsAffected == 1
		return nil
	})
	if err != nil {
		return historyDeletion{}, fmt.Errorf("dbstore: delete purging run %q: %w", runID, err)
	}
	return deleted, nil
}

func (store *Store) deleteRunLogs(tx *gorm.DB, runID string, budget purgeBudget) (historyPart, error) {
	if budget.rows <= 0 || budget.bytes <= 0 {
		return historyPart{}, nil
	}
	candidates, err := store.runLogDeletionCandidates(tx, runID, budget.rows)
	if err != nil {
		return historyPart{}, err
	}
	ordinals, selectedBytes := deletionIDsWithinBudget(candidates, budget.bytes)
	if len(ordinals) == 0 {
		return historyPart{}, nil
	}
	if err := tx.Where("ordinal IN ?", ordinals).Delete(&runLogRetentionRow{}).Error; err != nil {
		return historyPart{}, err
	}
	result := tx.Where("run_id = ? AND ordinal IN ?", runID, ordinals).Delete(&runLogRow{})
	if result.Error != nil {
		return historyPart{}, result.Error
	}
	return historyPart{rows: result.RowsAffected, bytes: selectedBytes}, nil
}

func (store *Store) deleteCheckpoints(tx *gorm.DB, runID string, budget purgeBudget) (historyPart, error) {
	if budget.rows <= 0 || budget.bytes <= 0 {
		return historyPart{}, nil
	}
	candidates, err := store.checkpointDeletionCandidates(tx, runID, budget.rows)
	if err != nil {
		return historyPart{}, err
	}
	versions, selectedBytes := deletionIDsWithinBudget(candidates, budget.bytes)
	if len(versions) == 0 {
		return historyPart{}, nil
	}
	result := tx.Where("run_id = ? AND version IN ?", runID, versions).Delete(&checkpointRow{})
	if result.Error != nil {
		return historyPart{}, result.Error
	}
	return historyPart{rows: result.RowsAffected, bytes: selectedBytes}, nil
}

type deletionCandidate struct {
	ID       int64 `gorm:"column:id"`
	ByteSize int64 `gorm:"column:byte_size"`
}

func (store *Store) runLogDeletionCandidates(tx *gorm.DB, runID string, limit int) ([]deletionCandidate, error) {
	var candidates []deletionCandidate
	err := tx.Raw(
		`SELECT ordinal AS id, `+store.byteLengthSQL("record_json")+` AS byte_size
			FROM `+eventTable+` WHERE run_id = ? ORDER BY sequence LIMIT ?`,
		runID,
		limit,
	).Scan(&candidates).Error
	return candidates, err
}

func (store *Store) checkpointDeletionCandidates(tx *gorm.DB, runID string, limit int) ([]deletionCandidate, error) {
	var candidates []deletionCandidate
	err := tx.Raw(
		`SELECT version AS id, (`+store.byteLengthSQL("record_json")+` + `+store.byteLengthSQL("payload")+`) AS byte_size
			FROM `+checkpointTable+` WHERE run_id = ? ORDER BY version LIMIT ?`,
		runID,
		limit,
	).Scan(&candidates).Error
	return candidates, err
}

func (store *Store) byteLengthSQL(column string) string {
	if store.dialect == "postgres" {
		return "OCTET_LENGTH(" + column + ")"
	}
	return "LENGTH(" + column + ")"
}

func hasRunRows(tx *gorm.DB, model any, runID string) (bool, error) {
	var marker int
	err := tx.Model(model).Select("1").Where("run_id = ?", runID).Limit(1).Scan(&marker).Error
	return marker == 1, err
}

func deletionIDsWithinBudget(candidates []deletionCandidate, byteLimit int64) ([]int64, int64) {
	ids := make([]int64, 0, len(candidates))
	var bytes int64
	for _, candidate := range candidates {
		if bytes+candidate.ByteSize > byteLimit {
			break
		}
		ids = append(ids, candidate.ID)
		bytes += candidate.ByteSize
		if bytes >= byteLimit {
			break
		}
	}
	return ids, bytes
}

// RunLogPurgeRequest selects journal-only events by Store append time.
// Limit counts logical events and ByteLimit is a hard upper bound on encoded
// record bytes; neither is a physical WAL/redo limit.
type RunLogPurgeRequest struct {
	Before          time.Time
	Limit           int
	ByteLimit       int64
	AllowReplayLoss bool
}

// RunLogPurgeResult reports journal-only events deleted by PurgeRunLog.
type RunLogPurgeResult struct {
	Events int64
}

// ConfirmedRunLogPurgeRequest selects one active Workflow's confirmed RunLog
// prefix for destructive compaction. This preserves current-checkpoint resume,
// but removes history that Retry, Fork, projections, or audits may require.
type ConfirmedRunLogPurgeRequest struct {
	RunID            string
	Before           time.Time
	Limit            int
	ByteLimit        int64
	AllowHistoryLoss bool
}

// ConfirmedRunLogPurgeResult reports the compacted prefix for one Workflow Run.
type ConfirmedRunLogPurgeResult struct {
	Events                   int64
	CompactedThroughSequence int64
}

type journalPurgeCandidate struct {
	Ordinal  int64  `gorm:"column:ordinal"`
	RunID    string `gorm:"column:run_id"`
	ByteSize int64  `gorm:"column:byte_size"`
}

// PurgeRunLog deletes a bounded number of journal-only events. Workflow logs
// have a different registry kind and are never selected by this operation.
func (store *Store) PurgeRunLog(ctx context.Context, request RunLogPurgeRequest) (RunLogPurgeResult, error) {
	if err := store.ready(ctx); err != nil {
		return RunLogPurgeResult{}, err
	}
	if request.Before.IsZero() {
		return RunLogPurgeResult{}, errors.New("dbstore: RunLog purge before time is required")
	}
	if !request.AllowReplayLoss {
		return RunLogPurgeResult{}, errors.New(
			"dbstore: RunLog purge requires AllowReplayLoss because deleted events cannot be replayed",
		)
	}
	if request.Limit < 0 || request.Limit > maxPurgeRowLimit {
		return RunLogPurgeResult{}, fmt.Errorf("dbstore: RunLog purge limit must be between 0 and %d", maxPurgeRowLimit)
	}
	if request.ByteLimit < 0 || request.ByteLimit > maxPurgeByteLimit {
		return RunLogPurgeResult{}, fmt.Errorf("dbstore: RunLog purge byte limit must be between 0 and %d", maxPurgeByteLimit)
	}
	limit := request.Limit
	if limit == 0 {
		limit = defaultPurgeRowLimit
	}
	byteLimit := request.ByteLimit
	if byteLimit == 0 {
		byteLimit = defaultPurgeByteLimit
	}
	var candidates []journalPurgeCandidate
	err := store.db.WithContext(ctx).Raw(
		`SELECT retention.ordinal, retention.run_id, `+store.byteLengthSQL("events.record_json")+` AS byte_size
			FROM `+eventRetentionTable+` AS retention
			JOIN `+registryTable+` AS registry ON registry.run_id = retention.run_id
			JOIN `+eventTable+` AS events ON events.ordinal = retention.ordinal
			WHERE registry.kind = ? AND registry.state = ?
			AND retention.appended_at_unix_nano < ?
			ORDER BY retention.appended_at_unix_nano, retention.ordinal LIMIT ?`,
		runKindJournal,
		runStateActive,
		request.Before.UnixNano(),
		limit,
	).Scan(&candidates).Error
	if err != nil {
		return RunLogPurgeResult{}, fmt.Errorf("dbstore: select journal RunLog for purge: %w", err)
	}
	bounded := candidates[:0]
	var selectedBytes int64
	for _, candidate := range candidates {
		if selectedBytes+candidate.ByteSize > byteLimit {
			break
		}
		bounded = append(bounded, candidate)
		selectedBytes += candidate.ByteSize
		if selectedBytes >= byteLimit {
			break
		}
	}
	candidates = bounded
	byRun := make(map[string][]int64)
	order := make([]string, 0)
	for _, candidate := range candidates {
		if _, exists := byRun[candidate.RunID]; !exists {
			order = append(order, candidate.RunID)
		}
		byRun[candidate.RunID] = append(byRun[candidate.RunID], candidate.Ordinal)
	}
	result := RunLogPurgeResult{}
	for _, runID := range order {
		ordinals := byRun[runID]
		var deletedEvents int64
		err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			registry, found, err := store.lockRunRegistry(tx, runID)
			if err != nil {
				return err
			}
			if !found || registry.Kind != runKindJournal || registry.State != runStateActive {
				return nil
			}
			var eligible []int64
			if err := tx.Model(&runLogRetentionRow{}).
				Where("run_id = ? AND ordinal IN ? AND appended_at_unix_nano < ?", runID, ordinals, request.Before.UnixNano()).
				Pluck("ordinal", &eligible).Error; err != nil {
				return err
			}
			if len(eligible) == 0 {
				return nil
			}
			if err := tx.Where("ordinal IN ?", eligible).Delete(&runLogRetentionRow{}).Error; err != nil {
				return err
			}
			deleted := tx.Where("run_id = ? AND ordinal IN ?", runID, eligible).Delete(&runLogRow{})
			if deleted.Error != nil {
				return deleted.Error
			}
			deletedEvents = deleted.RowsAffected
			remainingEvents, err := hasRunRows(tx, &runLogRow{}, runID)
			if err != nil {
				return err
			}
			if !remainingEvents {
				result := tx.Where(
					"run_id = ? AND kind = ? AND state = ?",
					runID,
					runKindJournal,
					runStateActive,
				).Delete(&runRegistryRow{})
				if result.Error != nil {
					return result.Error
				}
			}
			return nil
		})
		if err != nil {
			return result, runLogPurgeError(ctx, runID, err)
		}
		result.Events += deletedEvents
	}
	return result, nil
}

// PurgeConfirmedRunLog deletes a contiguous, durably checkpointed prefix from
// one active Workflow Run. The persisted compaction floor prevents a late
// append from recreating deleted sequence numbers.
func (store *Store) PurgeConfirmedRunLog(ctx context.Context, request ConfirmedRunLogPurgeRequest) (ConfirmedRunLogPurgeResult, error) {
	if err := store.ready(ctx); err != nil {
		return ConfirmedRunLogPurgeResult{}, err
	}
	if err := validateIndexedID("run id", request.RunID); err != nil {
		return ConfirmedRunLogPurgeResult{}, fmt.Errorf("dbstore: invalid confirmed RunLog purge: %w", err)
	}
	if request.Before.IsZero() {
		return ConfirmedRunLogPurgeResult{}, errors.New("dbstore: confirmed RunLog purge before time is required")
	}
	if !request.AllowHistoryLoss {
		return ConfirmedRunLogPurgeResult{}, errors.New(
			"dbstore: confirmed RunLog purge requires AllowHistoryLoss because Retry, Fork, and audit history may be lost",
		)
	}
	if request.Limit < 0 || request.Limit > maxPurgeRowLimit {
		return ConfirmedRunLogPurgeResult{}, fmt.Errorf(
			"dbstore: confirmed RunLog purge limit must be between 0 and %d",
			maxPurgeRowLimit,
		)
	}
	if request.ByteLimit < 0 || request.ByteLimit > maxPurgeByteLimit {
		return ConfirmedRunLogPurgeResult{}, fmt.Errorf(
			"dbstore: confirmed RunLog purge byte limit must be between 0 and %d",
			maxPurgeByteLimit,
		)
	}
	limit := request.Limit
	if limit == 0 {
		limit = defaultPurgeRowLimit
	}
	byteLimit := request.ByteLimit
	if byteLimit == 0 {
		byteLimit = defaultPurgeByteLimit
	}
	result := ConfirmedRunLogPurgeResult{}
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		registry, found, err := store.lockRunRegistry(tx, request.RunID)
		if err != nil {
			return err
		}
		if !found || registry.Kind != runKindWorkflow || registry.State != runStateActive {
			return fmt.Errorf("dbstore: run %q is not an active workflow", request.RunID)
		}
		result.CompactedThroughSequence = registry.CompactedThroughSequence
		locked, err := store.lockRunHead(tx, request.RunID)
		if err != nil {
			return err
		}
		if !locked {
			return fmt.Errorf("dbstore: active workflow run %q has no run head", request.RunID)
		}
		var head runRow
		if err := tx.Where("run_id = ?", request.RunID).Take(&head).Error; err != nil {
			return err
		}
		checkpoint, err := loadCheckpointMetadata(tx, request.RunID)
		if err != nil {
			return err
		}
		if !runHeadMatchesCheckpoint(head, checkpoint) {
			return fmt.Errorf("dbstore: workflow run head %q does not match its latest checkpoint", request.RunID)
		}
		if checkpoint.Status != workflow.CheckpointRunning && checkpoint.Status != workflow.CheckpointInterrupted {
			return fmt.Errorf("dbstore: workflow run %q is terminal and cannot be prefix-compacted", request.RunID)
		}
		if checkpoint.PendingSequence != 0 {
			return fmt.Errorf("dbstore: workflow run %q has a pending event and cannot be compacted", request.RunID)
		}
		if checkpoint.ConfirmedSequence <= registry.CompactedThroughSequence {
			return nil
		}
		selection := confirmedRunLogSelection{
			runID: request.RunID, floor: registry.CompactedThroughSequence,
			confirmed: checkpoint.ConfirmedSequence, limit: limit,
		}
		candidates, err := store.confirmedRunLogCandidates(tx, selection)
		if err != nil {
			return err
		}
		if len(candidates) == 0 {
			return fmt.Errorf(
				"dbstore: confirmed RunLog is missing sequence %d; repair or restore history before compaction",
				registry.CompactedThroughSequence+1,
			)
		}
		selected, err := contiguousConfirmedCandidates(
			candidates,
			registry.CompactedThroughSequence,
			request.Before.UnixNano(),
			byteLimit,
		)
		if err != nil {
			return err
		}
		if len(selected) == 0 {
			return nil
		}
		ordinals := make([]int64, 0, len(selected))
		for _, candidate := range selected {
			ordinals = append(ordinals, candidate.Ordinal)
		}
		if deleted := tx.Where("run_id = ? AND ordinal IN ?", request.RunID, ordinals).
			Delete(&runLogRetentionRow{}); deleted.Error != nil {
			return deleted.Error
		} else if deleted.RowsAffected != int64(len(ordinals)) {
			return fmt.Errorf("dbstore: confirmed RunLog retention changed while compacting %q", request.RunID)
		}
		deleted := tx.Where("run_id = ? AND ordinal IN ?", request.RunID, ordinals).Delete(&runLogRow{})
		if deleted.Error != nil {
			return deleted.Error
		}
		if deleted.RowsAffected != int64(len(ordinals)) {
			return fmt.Errorf("dbstore: confirmed RunLog changed while compacting %q", request.RunID)
		}
		floor := selected[len(selected)-1].Sequence
		updated := tx.Model(&runRegistryRow{}).Where(
			"run_id = ? AND kind = ? AND state = ? AND compacted_through_sequence = ?",
			request.RunID,
			runKindWorkflow,
			runStateActive,
			registry.CompactedThroughSequence,
		).Update("compacted_through_sequence", floor)
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return fmt.Errorf("dbstore: confirmed RunLog floor changed while compacting %q", request.RunID)
		}
		result.Events = deleted.RowsAffected
		result.CompactedThroughSequence = floor
		return nil
	})
	if err != nil {
		return ConfirmedRunLogPurgeResult{}, contextOrError(ctx, err)
	}
	return result, nil
}

type confirmedRunLogCandidate struct {
	Ordinal            int64 `gorm:"column:ordinal"`
	Sequence           int64 `gorm:"column:sequence"`
	ByteSize           int64 `gorm:"column:byte_size"`
	AppendedAtUnixNano int64 `gorm:"column:appended_at_unix_nano"`
}

type confirmedRunLogSelection struct {
	runID     string
	floor     int64
	confirmed int64
	limit     int
}

func (store *Store) confirmedRunLogCandidates(tx *gorm.DB, selection confirmedRunLogSelection) ([]confirmedRunLogCandidate, error) {
	var candidates []confirmedRunLogCandidate
	err := tx.Raw(
		`SELECT events.ordinal, events.sequence, `+store.byteLengthSQL("events.record_json")+` AS byte_size,
			retention.appended_at_unix_nano
			FROM `+eventTable+` AS events
			JOIN `+eventRetentionTable+` AS retention ON retention.ordinal = events.ordinal
			WHERE events.run_id = ? AND retention.run_id = events.run_id
			AND events.sequence > ? AND events.sequence <= ?
			ORDER BY events.sequence LIMIT ?`,
		selection.runID,
		selection.floor,
		selection.confirmed,
		selection.limit,
	).Scan(&candidates).Error
	if err != nil {
		return nil, fmt.Errorf("dbstore: select confirmed RunLog prefix: %w", err)
	}
	return candidates, nil
}

func contiguousConfirmedCandidates(candidates []confirmedRunLogCandidate, floor, beforeNano, byteLimit int64) ([]confirmedRunLogCandidate, error) {
	selected := make([]confirmedRunLogCandidate, 0, len(candidates))
	var selectedBytes int64
	expected := floor + 1
	for _, candidate := range candidates {
		if candidate.Sequence != expected {
			return nil, fmt.Errorf(
				"dbstore: confirmed RunLog has a sequence gap after %d; repair or restore history before compaction",
				floor,
			)
		}
		if candidate.AppendedAtUnixNano >= beforeNano {
			break
		}
		if selectedBytes+candidate.ByteSize > byteLimit {
			break
		}
		selected = append(selected, candidate)
		selectedBytes += candidate.ByteSize
		expected++
		if selectedBytes >= byteLimit {
			break
		}
	}
	return selected, nil
}

func contextOrError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return err
}

func runLogPurgeError(ctx context.Context, runID string, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return fmt.Errorf("dbstore: purge journal RunLog %q: %w", runID, err)
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
		err = store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			for _, record := range records {
				if err := backfillRunHead(tx, record); err != nil {
					return fmt.Errorf("dbstore: backfill run head %q: %w", record.RunID, err)
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
		afterRunID = records[len(records)-1].RunID
		if len(records) < backfillBatchSize {
			return nil
		}
	}
}

type backfillCandidate struct {
	RunID      string `gorm:"column:run_id"`
	Version    int64  `gorm:"column:version"`
	RecordJSON []byte `gorm:"column:record_json"`
}

func (store *Store) loadRunHeadBackfillBatch(ctx context.Context, afterRunID string) ([]workflow.CheckpointRecord, error) {
	var candidates []backfillCandidate
	err := store.db.WithContext(ctx).Raw(
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
	).Scan(&candidates).Error
	if err != nil {
		return nil, fmt.Errorf("dbstore: select run heads for backfill: %w", err)
	}
	records := make([]workflow.CheckpointRecord, 0, len(candidates))
	for _, candidate := range candidates {
		var record workflow.CheckpointRecord
		if err := json.Unmarshal(candidate.RecordJSON, &record); err != nil {
			return nil, fmt.Errorf("dbstore: decode run head for backfill: %w", err)
		}
		if record.RunID != candidate.RunID || record.Version != candidate.Version {
			return nil, fmt.Errorf(
				"dbstore: checkpoint metadata does not match run head source %q version %d",
				candidate.RunID,
				candidate.Version,
			)
		}
		records = append(records, record)
	}
	return records, nil
}

func backfillRunHead(tx *gorm.DB, record workflow.CheckpointRecord) error {
	var exists int
	err := tx.Raw(
		`SELECT 1 FROM `+checkpointTable+` AS source
			WHERE source.run_id = ? AND source.version = ?
			AND NOT EXISTS (
				SELECT 1 FROM `+checkpointTable+` AS newer
				WHERE newer.run_id = source.run_id AND newer.version > source.version
			) LIMIT 1`,
		record.RunID,
		record.Version,
	).Row().Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	head := runHead(record)
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(head).Error; err != nil {
		return err
	}
	return tx.Model(&runRow{}).Where("run_id = ? AND version < ?", record.RunID, record.Version).Updates(map[string]any{
		"session_id": head.SessionID, "status": head.Status, "version": head.Version,
		"created_at_unix_nano": head.CreatedAtUnixNano, "updated_at_unix_nano": head.UpdatedAtUnixNano,
	}).Error
}
