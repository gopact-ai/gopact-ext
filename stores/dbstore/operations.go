package dbstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/gopact-ai/gopact/runlog"
	"github.com/gopact-ai/gopact/workflow"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	sqlitePrimaryResultCodeMask = 0xff
	sqliteBusyResultCode        = 5
	sqliteLockedResultCode      = 6
)

var errRunHeadConflict = errors.New("dbstore: run head conflict")

// Claim atomically replaces an expired running or interrupted checkpoint.
func (store *Store) Claim(ctx context.Context, candidate workflow.CheckpointRecord, version int64) error {
	if err := store.ready(ctx); err != nil {
		return err
	}
	if version <= 0 || candidate.Version != version || candidate.Status != workflow.CheckpointRunning ||
		candidate.OwnerID == "" || candidate.ClaimSequence <= 0 ||
		(candidate.LeaseExpiresAt.IsZero() && candidate.LeaseDuration <= 0) {
		return fmt.Errorf("%w: invalid checkpoint claim", workflow.ErrInvalidCheckpoint)
	}
	if err := validateCheckpoint(candidate); err != nil {
		return err
	}
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		locked, err := store.lockRunHead(tx, candidate.RunID)
		if err != nil {
			return err
		}
		if !locked {
			return workflow.ErrCheckpointNotFound
		}
		current, err := loadCheckpoint(tx, candidate.RunID)
		if err != nil {
			return err
		}
		now, err := store.databaseNow(tx)
		if err != nil {
			return err
		}
		candidate.LeaseExpiresAt, err = resolveLeaseExpiry(
			now,
			candidate.LeaseExpiresAt,
			candidate.LeaseDuration,
		)
		if err != nil || !candidate.LeaseExpiresAt.After(now) {
			return fmt.Errorf("%w: checkpoint claim lease must be in the future", workflow.ErrInvalidCheckpoint)
		}
		if current.Version != version ||
			(current.Status != workflow.CheckpointRunning && current.Status != workflow.CheckpointInterrupted) {
			return workflow.ErrCheckpointConflict
		}
		if !sameCheckpointIdentity(current, candidate) {
			return workflow.ErrCheckpointMismatch
		}
		if current.LeaseExpiresAt.After(now) || candidate.ClaimSequence != current.ClaimSequence+1 {
			return workflow.ErrCheckpointConflict
		}
		candidate.Version++
		return advanceCheckpoint(tx, candidate, version)
	})
	if errors.Is(err, gorm.ErrDuplicatedKey) || errors.Is(err, errRunHeadConflict) || store.concurrencyError(err) {
		return workflow.ErrCheckpointConflict
	}
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return err
	}
	return nil
}

// RenewLease extends the current running ownership claim without creating a checkpoint version.
func (store *Store) RenewLease(ctx context.Context, lease workflow.CheckpointLease) error {
	if err := store.ready(ctx); err != nil {
		return err
	}
	if err := validateIndexedID("run id", lease.RunID); err != nil || lease.OwnerID == "" ||
		lease.ClaimSequence <= 0 || (lease.ExpiresAt.IsZero() && lease.Duration <= 0) {
		return fmt.Errorf("%w: invalid checkpoint lease", workflow.ErrInvalidCheckpoint)
	}
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		locked, err := store.lockRunHead(tx, lease.RunID)
		if err != nil {
			return err
		}
		if !locked {
			return workflow.ErrCheckpointLeaseLost
		}
		current, err := loadCheckpoint(tx, lease.RunID)
		if errors.Is(err, workflow.ErrCheckpointNotFound) {
			return workflow.ErrCheckpointLeaseLost
		}
		if err != nil {
			return err
		}
		now, err := store.databaseNow(tx)
		if err != nil {
			return err
		}
		if current.Status != workflow.CheckpointRunning || current.OwnerID != lease.OwnerID ||
			current.ClaimSequence != lease.ClaimSequence || !current.LeaseExpiresAt.After(now) {
			return workflow.ErrCheckpointLeaseLost
		}
		expiresAt, err := resolveLeaseExpiry(now, lease.ExpiresAt, lease.Duration)
		if err != nil || !expiresAt.After(now) {
			return fmt.Errorf("%w: renewed lease must expire in the future", workflow.ErrInvalidCheckpoint)
		}
		if !expiresAt.After(current.LeaseExpiresAt) {
			return nil
		}
		current.LeaseExpiresAt = expiresAt
		metadata, err := encodeCheckpointMetadata(current)
		if err != nil {
			return err
		}
		result := tx.Model(&checkpointRow{}).
			Where("run_id = ? AND version = ?", lease.RunID, current.Version).
			Update("record_json", metadata)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return workflow.ErrCheckpointLeaseLost
		}
		return nil
	})
	if store.concurrencyError(err) {
		return workflow.ErrCheckpointLeaseLost
	}
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return err
	}
	return nil
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
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		locked, err := store.lockRunHead(tx, record.RunID)
		if err != nil {
			return err
		}
		if !locked {
			return workflow.ErrCheckpointNotFound
		}
		current, err := loadCheckpoint(tx, record.RunID)
		if err != nil {
			return err
		}
		currentTerminal := checkpointTerminal(current.Status)
		if !sameCheckpointIdentity(current, record) {
			return workflow.ErrCheckpointMismatch
		}
		if err := store.prepareCheckpointWriteLease(tx, current, &record, reopen); err != nil {
			return err
		}
		if current.Version != version || (reopen && !currentTerminal) || (!reopen && currentTerminal) {
			return workflow.ErrCheckpointConflict
		}
		if !reopen && record.OwnerID == current.OwnerID && current.LeaseExpiresAt.After(record.LeaseExpiresAt) {
			record.LeaseExpiresAt = current.LeaseExpiresAt
		}
		record.Version++
		return advanceCheckpoint(tx, record, version)
	})
	if errors.Is(err, gorm.ErrDuplicatedKey) || errors.Is(err, errRunHeadConflict) || store.concurrencyError(err) {
		return workflow.ErrCheckpointConflict
	}
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return err
	}
	return nil
}

func (store *Store) prepareCheckpointWriteLease(tx *gorm.DB, current workflow.CheckpointRecord, record *workflow.CheckpointRecord, reopen bool) error {
	if reopen {
		return nil
	}
	claimMismatch := record.ClaimSequence != current.ClaimSequence
	ownerMismatch := record.OwnerID != "" && record.OwnerID != current.OwnerID
	if claimMismatch || ownerMismatch {
		return workflow.ErrCheckpointLeaseLost
	}
	if current.OwnerID == "" {
		return nil
	}
	now, err := store.databaseNow(tx)
	if err != nil {
		return err
	}
	if !current.LeaseExpiresAt.After(now) {
		return workflow.ErrCheckpointLeaseLost
	}
	if record.OwnerID == "" {
		return nil
	}
	record.LeaseExpiresAt, err = resolveLeaseExpiry(now, current.LeaseExpiresAt, record.LeaseDuration)
	if err != nil {
		return fmt.Errorf("%w: invalid checkpoint lease duration", workflow.ErrInvalidCheckpoint)
	}
	return nil
}

// ListCheckpoints returns immutable checkpoint versions in ascending order.
func (store *Store) ListCheckpoints(ctx context.Context, request workflow.CheckpointHistoryRequest) ([]workflow.CheckpointRecord, error) {
	if err := store.ready(ctx); err != nil {
		return nil, err
	}
	if err := validateIndexedID("run id", request.RunID); err != nil || request.AfterVersion < 0 || request.Limit < 0 {
		return nil, fmt.Errorf("%w: invalid checkpoint history request", workflow.ErrInvalidCheckpoint)
	}
	query := activeCheckpointRows(store.db.WithContext(ctx), request.RunID).
		Where("checkpoints.version > ?", request.AfterVersion).
		Order("checkpoints.version")
	if request.Limit > 0 {
		query = query.Limit(request.Limit)
	}
	var rows []checkpointRow
	if err := query.Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("dbstore: list checkpoints: %w", err)
	}
	if len(rows) == 0 {
		var marker int
		if err := activeCheckpointRows(store.db.WithContext(ctx), request.RunID).
			Select("1").Limit(1).Scan(&marker).Error; err != nil {
			return nil, fmt.Errorf("dbstore: check checkpoint history: %w", err)
		}
		if marker != 1 {
			return nil, workflow.ErrCheckpointNotFound
		}
	}
	records := make([]workflow.CheckpointRecord, 0, len(rows))
	for _, row := range rows {
		record, err := decodeCheckpoint(row.RecordJSON, row.Payload)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}

// Append appends a RunLog record idempotently by RunID and sequence.
func (store *Store) Append(ctx context.Context, record runlog.Record) error {
	if err := store.ready(ctx); err != nil {
		return err
	}
	payload, err := encodeRunLogRecord(record)
	if err != nil {
		return err
	}
	err = store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		registry, err := store.ensureRunRegistry(tx, record.RunID, runKindJournal)
		if err != nil {
			return err
		}
		if record.Sequence <= registry.CompactedThroughSequence {
			return fmt.Errorf("%w at sequence %d", runlog.ErrHistoryCompacted, registry.CompactedThroughSequence)
		}
		return store.appendRunLog(tx, record, payload)
	})
	if errors.Is(err, errRunRetired) || errors.Is(err, errRunKindConflict) {
		return fmt.Errorf("%w for retired run %s", runlog.ErrConflict, record.RunID)
	}
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return err
	}
	return nil
}

// AppendFenced validates the current workflow lease and appends one RunLog
// record in the same transaction.
func (store *Store) AppendFenced(ctx context.Context, record runlog.Record, fence runlog.Fence) error {
	if err := store.ready(ctx); err != nil {
		return err
	}
	if fence.OwnerID == "" || fence.ClaimSequence <= 0 {
		return fmt.Errorf("%w: invalid journal fence", workflow.ErrInvalidCheckpoint)
	}
	payload, err := encodeRunLogRecord(record)
	if err != nil {
		return err
	}
	err = store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		registry, err := store.ensureRunRegistry(tx, record.RunID, runKindWorkflow)
		if err != nil {
			return err
		}
		if record.Sequence <= registry.CompactedThroughSequence {
			return fmt.Errorf("%w at sequence %d", runlog.ErrHistoryCompacted, registry.CompactedThroughSequence)
		}
		locked, err := store.lockRunHead(tx, record.RunID)
		if err != nil {
			return err
		}
		if !locked {
			return workflow.ErrCheckpointLeaseLost
		}
		current, err := loadCheckpoint(tx, record.RunID)
		if errors.Is(err, workflow.ErrCheckpointNotFound) {
			return workflow.ErrCheckpointLeaseLost
		}
		if err != nil {
			return err
		}
		now, err := store.databaseNow(tx)
		if err != nil {
			return err
		}
		if current.Status != workflow.CheckpointRunning || current.OwnerID != fence.OwnerID ||
			current.ClaimSequence != fence.ClaimSequence || !current.LeaseExpiresAt.After(now) {
			return workflow.ErrCheckpointLeaseLost
		}
		return store.appendRunLog(tx, record, payload)
	})
	if errors.Is(err, errRunRetired) || errors.Is(err, errRunKindConflict) || store.concurrencyError(err) {
		return workflow.ErrCheckpointLeaseLost
	}
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return err
	}
	return nil
}

// List returns RunLog records in append order.
func (store *Store) List(ctx context.Context, query runlog.Query) ([]runlog.Record, error) {
	if err := store.ready(ctx); err != nil {
		return nil, err
	}
	if query.After < 0 || query.Limit < 0 {
		return nil, fmt.Errorf("%w: after and limit must not be negative", runlog.ErrInvalidQuery)
	}
	if query.SessionID != "" {
		if err := validateIndexedID("session id", query.SessionID); err != nil {
			return nil, fmt.Errorf("%w: %v", runlog.ErrInvalidQuery, err)
		}
	}
	if query.RunID != "" {
		if err := validateIndexedID("run id", query.RunID); err != nil {
			return nil, fmt.Errorf("%w: %v", runlog.ErrInvalidQuery, err)
		}
	}
	if query.SessionID != "" && query.RunID == "" && query.After != 0 {
		return nil, fmt.Errorf("%w: after requires a run id for session queries", runlog.ErrInvalidQuery)
	}
	var rows []runLogRow
	if query.RunID != "" {
		err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			registry, found, err := store.lockRunRegistry(tx, query.RunID)
			if err != nil {
				return fmt.Errorf("dbstore: lock RunLog compaction floor: %w", err)
			}
			if found && registry.Kind == runKindWorkflow && query.After < registry.CompactedThroughSequence {
				return fmt.Errorf(
					"%w: run %s was compacted through sequence %d",
					runlog.ErrHistoryCompacted,
					query.RunID,
					registry.CompactedThroughSequence,
				)
			}
			return store.findRunLogRows(tx, query, &rows)
		})
		if err != nil {
			return nil, contextOrError(ctx, err)
		}
	} else if err := store.findRunLogRows(store.db.WithContext(ctx), query, &rows); err != nil {
		return nil, err
	}
	records := make([]runlog.Record, 0, len(rows))
	for _, row := range rows {
		var record runlog.Record
		if err := json.Unmarshal(row.RecordJSON, &record); err != nil {
			return nil, fmt.Errorf("dbstore: decode runlog: %w", err)
		}
		records = append(records, record)
	}
	return records, nil
}

func (store *Store) findRunLogRows(db *gorm.DB, query runlog.Query, rows *[]runLogRow) error {
	db = db.Table(eventTable+" AS events").Select("events.record_json").
		Joins("JOIN "+registryTable+" AS registry ON registry.run_id = events.run_id").
		Where("registry.state = ?", runStateActive)
	if query.SessionID != "" {
		db = db.Where("events.session_id = ?", query.SessionID)
	}
	if query.RunID != "" {
		db = db.Where("events.run_id = ?", query.RunID)
	}
	db = db.Where("events.sequence > ?", query.After).Order("events.ordinal")
	if query.Limit > 0 {
		db = db.Limit(query.Limit)
	}
	if err := db.Find(rows).Error; err != nil {
		return fmt.Errorf("dbstore: list runlog: %w", err)
	}
	return nil
}

func (store *Store) lockRunHead(tx *gorm.DB, runID string) (bool, error) {
	if store.dialect == "sqlite" {
		result := tx.Exec(`UPDATE `+runTable+` SET version = version WHERE run_id = ?`, runID)
		return result.RowsAffected == 1, result.Error
	}
	var row runRow
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("run_id").Where("run_id = ?", runID).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	return err == nil, err
}

func loadCheckpoint(db *gorm.DB, runID string) (workflow.CheckpointRecord, error) {
	var row checkpointRow
	err := db.Where("run_id = ?", runID).Order("version DESC").Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return workflow.CheckpointRecord{}, workflow.ErrCheckpointNotFound
	}
	if err != nil {
		return workflow.CheckpointRecord{}, fmt.Errorf("dbstore: load checkpoint: %w", err)
	}
	return decodeCheckpoint(row.RecordJSON, row.Payload)
}

func advanceCheckpoint(tx *gorm.DB, record workflow.CheckpointRecord, priorVersion int64) error {
	metadata, err := encodeCheckpointMetadata(record)
	if err != nil {
		return err
	}
	if err := tx.Create(&checkpointRow{
		RunID: record.RunID, Version: record.Version, RecordJSON: metadata, Payload: record.Payload,
	}).Error; err != nil {
		return err
	}
	head := runHead(record)
	result := tx.Model(&runRow{}).Where("run_id = ? AND version = ?", record.RunID, priorVersion).Updates(map[string]any{
		"session_id": head.SessionID, "status": head.Status, "version": head.Version,
		"created_at_unix_nano": head.CreatedAtUnixNano, "updated_at_unix_nano": head.UpdatedAtUnixNano,
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errRunHeadConflict
	}
	return nil
}

func (store *Store) appendRunLog(db *gorm.DB, record runlog.Record, payload []byte) error {
	row := runLogRow{
		SessionID: record.SessionID, RunID: record.RunID, Sequence: record.Sequence, RecordJSON: payload,
	}
	if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
		return fmt.Errorf("dbstore: append runlog: %w", err)
	}
	var existing runLogRow
	if err := db.Select("ordinal", "record_json").Where("run_id = ? AND sequence = ?", record.RunID, record.Sequence).
		Take(&existing).Error; err != nil {
		return fmt.Errorf("dbstore: load conflicting runlog: %w", err)
	}
	if bytes.Equal(existing.RecordJSON, payload) {
		now, err := store.databaseNow(db)
		if err != nil {
			return err
		}
		retention := runLogRetentionRow{
			Ordinal: existing.Ordinal, RunID: record.RunID, AppendedAtUnixNano: now.UnixNano(),
		}
		if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&retention).Error; err != nil {
			return fmt.Errorf("dbstore: append RunLog retention metadata: %w", err)
		}
		return nil
	}
	return fmt.Errorf("%w for %s/%d", runlog.ErrConflict, record.RunID, record.Sequence)
}

func encodeRunLogRecord(record runlog.Record) ([]byte, error) {
	if err := validateRunLogRecord(record); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(record)
	if err != nil {
		return nil, fmt.Errorf("dbstore: encode runlog record: %w", err)
	}
	if len(payload) > maxRunLogBytes {
		return nil, fmt.Errorf("%w: encoded record exceeds %d bytes", runlog.ErrInvalidRecord, maxRunLogBytes)
	}
	return payload, nil
}

func validateRunLogRecord(record runlog.Record) error {
	for _, field := range []struct{ name, value string }{
		{name: "session id", value: record.SessionID},
		{name: "run id", value: record.RunID},
	} {
		if err := validateIndexedID(field.name, field.value); err != nil {
			return fmt.Errorf("%w: %v", runlog.ErrInvalidRecord, err)
		}
	}
	switch {
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
	return left.ID == right.ID && left.SessionID == right.SessionID && left.RunID == right.RunID &&
		left.WorkflowName == right.WorkflowName && left.TopologyVersion == right.TopologyVersion &&
		left.SchemaVersion == right.SchemaVersion
}

func checkpointTerminal(status workflow.CheckpointStatus) bool {
	return status == workflow.CheckpointCompleted || status == workflow.CheckpointFailed ||
		status == workflow.CheckpointCanceled || status == workflow.CheckpointTerminated
}

func (store *Store) concurrencyError(err error) bool {
	if err == nil {
		return false
	}
	if store.dialect == "sqlite" {
		var result interface{ Code() int }
		if errors.As(err, &result) {
			code := result.Code() & sqlitePrimaryResultCodeMask
			return code == sqliteBusyResultCode || code == sqliteLockedResultCode
		}
		message := strings.ToLower(err.Error())
		return strings.Contains(message, "database is locked") || strings.Contains(message, "database table is locked")
	}
	var sqlState interface{ SQLState() string }
	if errors.As(err, &sqlState) {
		state := sqlState.SQLState()
		return state == "40001" || state == "40P01" || state == "55P03"
	}
	return mysqlConcurrencyError(err)
}
