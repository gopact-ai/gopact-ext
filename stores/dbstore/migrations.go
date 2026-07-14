package dbstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const metadataReconcileAttempts = 2

// ErrIncompatibleLegacyData reports data that cannot preserve identity or
// size guarantees under the shared multi-dialect schema.
var ErrIncompatibleLegacyData = errors.New("dbstore: incompatible legacy data")

func (store *Store) initializeSQLiteCompatibilityTriggers(ctx context.Context) error {
	if store.dialect != "sqlite" {
		return nil
	}
	statements := []string{
		`DROP TRIGGER IF EXISTS gopact_runlog_v2_metadata`,
		`DROP TRIGGER IF EXISTS gopact_runlog_compacted_guard`,
		`CREATE TRIGGER gopact_runlog_compacted_guard
			BEFORE INSERT ON ` + eventTable + `
			WHEN EXISTS (
				SELECT 1 FROM ` + registryTable + `
				WHERE run_id = NEW.run_id AND compacted_through_sequence >= NEW.sequence
			)
			BEGIN
				SELECT RAISE(ABORT, 'gopact run history was compacted');
			END`,
		`CREATE TRIGGER IF NOT EXISTS gopact_runlog_retired_guard
			BEFORE INSERT ON ` + eventTable + `
			WHEN EXISTS (
				SELECT 1 FROM ` + registryTable + `
				WHERE run_id = NEW.run_id AND state <> '` + runStateActive + `'
			)
			BEGIN
				SELECT RAISE(ABORT, 'gopact run is retired');
			END`,
		`CREATE TRIGGER gopact_runlog_v2_metadata
			AFTER INSERT ON ` + eventTable + `
			BEGIN
				INSERT OR IGNORE INTO ` + registryTable + `
					(run_id, kind, state, updated_at_unix_nano)
				VALUES (
					NEW.run_id,
					CASE WHEN EXISTS (
						SELECT 1 FROM ` + runTable + ` WHERE run_id = NEW.run_id
					) THEN '` + runKindWorkflow + `' ELSE '` + runKindJournal + `' END,
					'` + runStateActive + `',
					` + sqliteUnixMicroExpression + ` * 1000
				);
				INSERT OR IGNORE INTO ` + eventRetentionTable + `
					(ordinal, run_id, appended_at_unix_nano)
				VALUES (
					NEW.ordinal, NEW.run_id,
					` + sqliteUnixMicroExpression + ` * 1000
				);
			END`,
		`CREATE TRIGGER IF NOT EXISTS gopact_workflow_run_kind_guard
			BEFORE INSERT ON ` + runTable + `
			WHEN EXISTS (
				SELECT 1 FROM ` + registryTable + `
				WHERE run_id = NEW.run_id AND kind <> '` + runKindWorkflow + `'
			)
			BEGIN
				SELECT RAISE(ABORT, 'gopact run kind conflict');
			END`,
		`CREATE TRIGGER IF NOT EXISTS gopact_workflow_run_state_guard
			BEFORE INSERT ON ` + runTable + `
			WHEN EXISTS (
				SELECT 1 FROM ` + registryTable + `
				WHERE run_id = NEW.run_id AND state <> '` + runStateActive + `'
			)
			BEGIN
				SELECT RAISE(ABORT, 'gopact run is retired');
			END`,
		`CREATE TRIGGER IF NOT EXISTS gopact_workflow_run_v2_registry
			AFTER INSERT ON ` + runTable + `
			BEGIN
				INSERT OR IGNORE INTO ` + registryTable + `
					(run_id, kind, state, updated_at_unix_nano)
				VALUES (
					NEW.run_id, '` + runKindWorkflow + `', '` + runStateActive + `',
					` + sqliteUnixMicroExpression + ` * 1000
				);
			END`,
	}
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, statement := range statements {
			if err := tx.Exec(statement).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("dbstore: initialize SQLite compatibility trigger: %w", err)
	}
	return nil
}

func (store *Store) validateLegacyIndexedIDs(ctx context.Context) error {
	lengthExpression := "OCTET_LENGTH(%s)"
	spaceExpression := "RIGHT(%s, 1) = ' '"
	if store.dialect == "sqlite" {
		lengthExpression = "length(CAST(%s AS BLOB))"
		spaceExpression = "substr(%s, -1) = ' '"
	}
	columns := []struct {
		model      any
		table      string
		column     string
		allowEmpty bool
	}{
		{model: &checkpointRow{}, table: checkpointTable, column: "run_id"},
		{model: &runLogRow{}, table: eventTable, column: "run_id"},
		{model: &runLogRow{}, table: eventTable, column: "session_id", allowEmpty: true},
		{model: &runRow{}, table: runTable, column: "run_id"},
		{model: &runRow{}, table: runTable, column: "session_id"},
		{model: &runRegistryRow{}, table: registryTable, column: "run_id"},
	}
	db := store.db.WithContext(ctx)
	for _, item := range columns {
		if !db.Migrator().HasTable(item.model) || !db.Migrator().HasColumn(item.model, item.column) {
			continue
		}
		predicate := fmt.Sprintf(
			"(%s > ? OR %s)",
			fmt.Sprintf(lengthExpression, item.column),
			fmt.Sprintf(spaceExpression, item.column),
		)
		if !item.allowEmpty {
			predicate = strings.TrimSuffix(predicate, ")") + " OR " + item.column + " = '')"
		}
		var found int
		err := db.Raw(
			"SELECT 1 FROM "+item.table+" WHERE "+predicate+" LIMIT 1",
			maxIndexedIDBytes,
		).Scan(&found).Error
		if err != nil {
			return fmt.Errorf("dbstore: validate legacy %s.%s: %w", item.table, item.column, err)
		}
		if found == 1 {
			return fmt.Errorf(
				"%w: %s.%s contains an empty, overlong, or trailing-space ID",
				ErrIncompatibleLegacyData,
				item.table,
				item.column,
			)
		}
		rows, err := db.Raw("SELECT " + item.column + " FROM " + item.table).Rows()
		if err != nil {
			return fmt.Errorf("dbstore: scan legacy %s.%s: %w", item.table, item.column, err)
		}
		if err := validateLegacyIDRows(rows, item.table, item.column); err != nil {
			return err
		}
	}
	if err := store.validateLegacyJSONTable(ctx, checkpointTable, "run_id, version", maxCheckpointBytes, true); err != nil {
		return err
	}
	if err := store.validateLegacyJSONTable(ctx, eventTable, "ordinal", maxRunLogBytes, false); err != nil {
		return err
	}
	return nil
}

func validateLegacyIDRows(rows *sql.Rows, table, column string) error {
	defer rows.Close()
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return fmt.Errorf("dbstore: read legacy %s.%s: %w", table, column, err)
		}
		if value != "" && (!utf8.ValidString(value) || strings.IndexByte(value, 0) >= 0) {
			return fmt.Errorf(
				"%w: %s.%s contains invalid UTF-8 or NUL",
				ErrIncompatibleLegacyData,
				table,
				column,
			)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("dbstore: scan legacy %s.%s: %w", table, column, err)
	}
	return nil
}

type legacyJSONIdentity struct {
	RunID     string
	SessionID string
}

func (store *Store) validateLegacyJSONTable(ctx context.Context, table, order string, limit int, hasPayload bool) error {
	db := store.db.WithContext(ctx)
	if !db.Migrator().HasTable(table) || !db.Migrator().HasColumn(table, "record_json") {
		return nil
	}
	sizeSQL := store.byteLengthSQL("record_json")
	if hasPayload && db.Migrator().HasColumn(table, "payload") {
		sizeSQL += " + " + store.byteLengthSQL("payload")
	}
	var oversized int
	if err := db.Raw(
		"SELECT 1 FROM "+table+" WHERE "+sizeSQL+" > ? LIMIT 1",
		limit,
	).Scan(&oversized).Error; err != nil {
		return fmt.Errorf("dbstore: inspect legacy %s record size: %w", table, err)
	}
	if oversized == 1 {
		return fmt.Errorf("%w: %s contains a record over %d bytes", ErrIncompatibleLegacyData, table, limit)
	}
	rows, err := db.Raw("SELECT record_json FROM " + table + " ORDER BY " + order).Rows()
	if err != nil {
		return fmt.Errorf("dbstore: scan legacy %s identities: %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var encoded []byte
		if err := rows.Scan(&encoded); err != nil {
			return fmt.Errorf("dbstore: read legacy %s identity: %w", table, err)
		}
		var identity legacyJSONIdentity
		if !utf8.Valid(encoded) {
			return fmt.Errorf("%w: %s contains non-UTF-8 record JSON", ErrIncompatibleLegacyData, table)
		}
		if err := json.Unmarshal(encoded, &identity); err != nil {
			return fmt.Errorf("%w: %s contains invalid record JSON", ErrIncompatibleLegacyData, table)
		}
		if err := validateLegacyJSONID(table, "RunID", identity.RunID, false); err != nil {
			return err
		}
		if err := validateLegacyJSONID(table, "SessionID", identity.SessionID, !hasPayload); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("dbstore: scan legacy %s identities: %w", table, err)
	}
	return nil
}

func validateLegacyJSONID(table, name, value string, allowEmpty bool) error {
	if value == "" && allowEmpty {
		return nil
	}
	invalid := value == "" || len(value) > maxIndexedIDBytes || strings.HasSuffix(value, " ") ||
		!utf8.ValidString(value) || strings.IndexByte(value, 0) >= 0
	if !invalid {
		return nil
	}
	return fmt.Errorf(
		"%w: %s JSON %s exceeds the shared ID contract",
		ErrIncompatibleLegacyData,
		table,
		name,
	)
}

type retentionBackfillCandidate struct {
	Ordinal int64  `gorm:"column:ordinal"`
	RunID   string `gorm:"column:run_id"`
}

// backfillRunLogRetention deliberately never reads or rewrites record_json.
// Existing records start their retention age at migration time; this avoids
// trusting caller-supplied event timestamps and keeps migration I/O bounded to
// the new, narrow metadata table.
func (store *Store) backfillRunLogRetention(ctx context.Context) error {
	afterOrdinal := int64(0)
	appendedAt := store.nowUTC().UnixNano()
	for {
		var candidates []retentionBackfillCandidate
		err := store.db.WithContext(ctx).Raw(
			`SELECT events.ordinal, events.run_id
				FROM `+eventTable+` AS events
				LEFT JOIN `+eventRetentionTable+` AS retention ON retention.ordinal = events.ordinal
				WHERE events.ordinal > ? AND retention.ordinal IS NULL
				ORDER BY events.ordinal LIMIT ?`,
			afterOrdinal,
			backfillBatchSize,
		).Scan(&candidates).Error
		if err != nil {
			return fmt.Errorf("dbstore: select RunLog retention metadata for backfill: %w", err)
		}
		if len(candidates) == 0 {
			return nil
		}
		rows := make([]runLogRetentionRow, 0, len(candidates))
		for _, candidate := range candidates {
			rows = append(rows, runLogRetentionRow{
				Ordinal: candidate.Ordinal, RunID: candidate.RunID, AppendedAtUnixNano: appendedAt,
			})
		}
		if err := store.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&rows).Error; err != nil {
			return fmt.Errorf("dbstore: backfill RunLog retention metadata: %w", err)
		}
		afterOrdinal = candidates[len(candidates)-1].Ordinal
		if len(candidates) < backfillBatchSize {
			return nil
		}
	}
}

func (store *Store) backfillRunRegistry(ctx context.Context) error {
	if err := store.backfillRegistryKind(ctx, runKindWorkflow, store.workflowRunIDBatch); err != nil {
		return err
	}
	if err := store.backfillRegistryKind(ctx, runKindJournal, store.journalRunIDBatch); err != nil {
		return err
	}
	return nil
}

// reconcileV2Metadata is both the final gate before the v2 marker is written
// and the explicit repair path used by Migrate on an existing v2 database.
// Server upgrades must still run after every v1 writer has stopped: the
// advisory migration lock serializes migrators, not old application binaries.
func (store *Store) reconcileV2Metadata(ctx context.Context) error {
	var gap string
	for attempt := 0; attempt < metadataReconcileAttempts; attempt++ {
		if err := store.backfillRunLogRetention(ctx); err != nil {
			return err
		}
		if err := store.backfillRunRegistry(ctx); err != nil {
			return err
		}
		if err := store.promoteWorkflowRegistries(ctx); err != nil {
			return err
		}
		var err error
		gap, err = store.v2MetadataGap(ctx)
		if err != nil {
			return err
		}
		if gap == "" {
			return nil
		}
	}
	return fmt.Errorf(
		"dbstore: schema v2 metadata remains incomplete (%s); stop all v1 writers and rerun Migrate",
		gap,
	)
}

func (store *Store) promoteWorkflowRegistries(ctx context.Context) error {
	result := store.db.WithContext(ctx).Exec(
		`UPDATE `+registryTable+` AS registry
			SET kind = ?, updated_at_unix_nano = ?
			WHERE registry.kind = ? AND registry.state = ?
			AND EXISTS (
				SELECT 1 FROM `+runTable+` AS runs WHERE runs.run_id = registry.run_id
			)`,
		runKindWorkflow,
		store.nowUTC().UnixNano(),
		runKindJournal,
		runStateActive,
	)
	if result.Error != nil {
		return fmt.Errorf("dbstore: repair workflow run registry kind: %w", result.Error)
	}
	return nil
}

func (store *Store) v2MetadataGap(ctx context.Context) (string, error) {
	checks := []struct {
		name  string
		query string
		args  []any
	}{
		{
			name: "RunLog retention metadata",
			query: `SELECT 1 FROM ` + eventTable + ` AS events
				LEFT JOIN ` + eventRetentionTable + ` AS retention ON retention.ordinal = events.ordinal
				WHERE retention.ordinal IS NULL LIMIT 1`,
		},
		{
			name: "workflow run registry",
			query: `SELECT 1 FROM ` + runTable + ` AS runs
				LEFT JOIN ` + registryTable + ` AS registry ON registry.run_id = runs.run_id
				WHERE registry.run_id IS NULL OR registry.kind <> ? OR registry.state <> ? LIMIT 1`,
			args: []any{runKindWorkflow, runStateActive},
		},
		{
			name: "RunLog run registry",
			query: `SELECT 1 FROM ` + eventTable + ` AS events
					LEFT JOIN ` + runTable + ` AS runs ON runs.run_id = events.run_id
					LEFT JOIN ` + registryTable + ` AS registry ON registry.run_id = events.run_id
					WHERE registry.run_id IS NULL
					OR (runs.run_id IS NOT NULL AND (registry.kind <> ? OR registry.state <> ?))
					OR (runs.run_id IS NULL AND registry.state = ? AND registry.kind <> ?)
					OR (runs.run_id IS NULL AND registry.kind = ? AND registry.state <> ?)
					LIMIT 1`,
			args: []any{
				runKindWorkflow, runStateActive,
				runStateActive, runKindJournal,
				runKindJournal, runStateActive,
			},
		},
		{
			name: "RunLog compaction floor",
			query: `SELECT 1 FROM ` + registryTable + ` AS registry
				WHERE registry.compacted_through_sequence < 0
				OR (registry.kind = ? AND registry.compacted_through_sequence <> 0)
				OR EXISTS (
					SELECT 1 FROM ` + eventTable + ` AS events
					WHERE events.run_id = registry.run_id
					AND events.sequence <= registry.compacted_through_sequence
				)
				LIMIT 1`,
			args: []any{runKindJournal},
		},
	}
	for _, check := range checks {
		var found int
		if err := store.db.WithContext(ctx).Raw(check.query, check.args...).Scan(&found).Error; err != nil {
			return "", fmt.Errorf("dbstore: verify schema v2 %s: %w", check.name, err)
		}
		if found == 1 {
			return check.name, nil
		}
	}
	return "", nil
}

type runIDBatchLoader func(context.Context, string) ([]string, error)

func (store *Store) backfillRegistryKind(ctx context.Context, kind string, load runIDBatchLoader) error {
	afterRunID := ""
	for {
		runIDs, err := load(ctx, afterRunID)
		if err != nil {
			return err
		}
		if len(runIDs) == 0 {
			return nil
		}
		rows := make([]runRegistryRow, 0, len(runIDs))
		for _, runID := range runIDs {
			rows = append(rows, runRegistryRow{
				RunID: runID, Kind: kind, State: runStateActive,
			})
		}
		if err := store.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&rows).Error; err != nil {
			return fmt.Errorf("dbstore: backfill %s run registry: %w", kind, err)
		}
		afterRunID = runIDs[len(runIDs)-1]
		if len(runIDs) < backfillBatchSize {
			return nil
		}
	}
}

func (store *Store) workflowRunIDBatch(ctx context.Context, afterRunID string) ([]string, error) {
	var runIDs []string
	err := store.db.WithContext(ctx).Model(&runRow{}).
		Where("run_id > ?", afterRunID).Order("run_id").Limit(backfillBatchSize).
		Pluck("run_id", &runIDs).Error
	if err != nil {
		return nil, fmt.Errorf("dbstore: select workflow registry backfill: %w", err)
	}
	return runIDs, nil
}

func (store *Store) journalRunIDBatch(ctx context.Context, afterRunID string) ([]string, error) {
	var runIDs []string
	err := store.db.WithContext(ctx).Model(&runLogRow{}).
		Distinct("run_id").Where("run_id > ?", afterRunID).Order("run_id").Limit(backfillBatchSize).
		Pluck("run_id", &runIDs).Error
	if err != nil {
		return nil, fmt.Errorf("dbstore: select journal registry backfill: %w", err)
	}
	return runIDs, nil
}
