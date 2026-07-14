// Package dbstore persists workflow checkpoints and run history in a relational database.
package dbstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gopact-ai/gopact/workflow"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const (
	checkpointTable     = "gopact_workflow_checkpoints"
	eventTable          = "gopact_runlog"
	eventRetentionTable = "gopact_runlog_retention"
	runTable            = "gopact_workflow_runs"
	registryTable       = "gopact_run_registry"
	maxCheckpointBytes  = 4 << 20
	maxRunLogBytes      = 4 << 20
	maxIndexedIDBytes   = 191
	maxRunStatusBytes   = 32
	maxRegistryTagBytes = 16
	schemaVersion       = 2
	migrationLockName   = "gopact_dbstore_schema_migration"
	migrationLockKey    = int64(0x676f70616374)
	migrationLockWait   = 30 * time.Second
	migrationLockPoll   = 100 * time.Millisecond
)

// Store persists workflow checkpoints and run history in one database.
type Store struct {
	db      *gorm.DB
	dialect string
	now     func() time.Time
}

type checkpointRow struct {
	RunID      string `gorm:"column:run_id;not null;primaryKey;size:191"`
	Version    int64  `gorm:"column:version;not null;primaryKey;autoIncrement:false"`
	RecordJSON []byte `gorm:"column:record_json;not null"`
	Payload    []byte `gorm:"column:payload;not null"`
}

func (checkpointRow) TableName() string { return checkpointTable }

type runLogRow struct {
	Ordinal    int64  `gorm:"column:ordinal;not null;primaryKey;autoIncrement;index:gopact_runlog_session_ordinal,priority:2"`
	SessionID  string `gorm:"column:session_id;not null;size:191;index:gopact_runlog_session_ordinal,priority:1"`
	RunID      string `gorm:"column:run_id;not null;size:191;uniqueIndex:gopact_runlog_run_sequence,priority:1"`
	Sequence   int64  `gorm:"column:sequence;not null;uniqueIndex:gopact_runlog_run_sequence,priority:2"`
	RecordJSON []byte `gorm:"column:record_json;not null"`
}

func (runLogRow) TableName() string { return eventTable }

type runLogRetentionRow struct {
	Ordinal            int64  `gorm:"column:ordinal;not null;primaryKey;autoIncrement:false;index:gopact_runlog_retention_time,priority:2"`
	RunID              string `gorm:"column:run_id;not null;size:191;index:gopact_runlog_retention_run"`
	AppendedAtUnixNano int64  `gorm:"column:appended_at_unix_nano;not null;index:gopact_runlog_retention_time,priority:1"`
}

func (runLogRetentionRow) TableName() string { return eventRetentionTable }

type runRow struct {
	RunID             string `gorm:"column:run_id;not null;primaryKey;size:191;index:gopact_workflow_runs_retention,priority:3"`
	SessionID         string `gorm:"column:session_id;not null;size:191"`
	Status            string `gorm:"column:status;not null;size:32;index:gopact_workflow_runs_retention,priority:1"`
	Version           int64  `gorm:"column:version;not null"`
	CreatedAtUnixNano int64  `gorm:"column:created_at_unix_nano;not null"`
	UpdatedAtUnixNano int64  `gorm:"column:updated_at_unix_nano;not null;index:gopact_workflow_runs_retention,priority:2"`
}

func (runRow) TableName() string { return runTable }

const (
	runKindJournal  = "journal"
	runKindWorkflow = "workflow"
	runStateActive  = "active"
	runStatePurging = "purging"
	runStatePurged  = "purged"
)

// runRegistryRow gives every RunID a stable retention domain. Workflow
// tombstones are intentionally retained after history deletion so a late
// Append cannot resurrect a purged run.
type runRegistryRow struct {
	RunID                    string `gorm:"column:run_id;not null;primaryKey;size:191"`
	Kind                     string `gorm:"column:kind;not null;size:16"`
	State                    string `gorm:"column:state;not null;size:16;index:gopact_run_registry_retention,priority:1"`
	UpdatedAtUnixNano        int64  `gorm:"column:updated_at_unix_nano;not null;index:gopact_run_registry_retention,priority:2"`
	CompactedThroughSequence int64  `gorm:"column:compacted_through_sequence;not null;default:0"`
}

func (runRegistryRow) TableName() string { return registryTable }

type schemaMigrationRow struct {
	Version           int64 `gorm:"column:version;not null;primaryKey;autoIncrement:false"`
	AppliedAtUnixNano int64 `gorm:"column:applied_at_unix_nano;not null"`
}

func (schemaMigrationRow) TableName() string { return "gopact_schema_migrations" }

type schemaColumnKind uint8

const (
	schemaColumnString schemaColumnKind = iota
	schemaColumnInteger
	schemaColumnBytes
)

type schemaColumnRequirement struct {
	name     string
	kind     schemaColumnKind
	length   int64
	binaryID bool
}

type schemaRequirement struct {
	model         any
	table         string
	primaryKey    []string
	autoIncrement string
	columns       []schemaColumnRequirement
}

// Open opens a SQLite Store and initializes its schema. Server databases must
// run Migrate in the deployment stage and use Connect from application instances.
func Open(dialector gorm.Dialector, options ...gorm.Option) (*Store, error) {
	return OpenContext(context.Background(), dialector, options...)
}

// OpenContext opens and initializes a SQLite Store using ctx.
func OpenContext(ctx context.Context, dialector gorm.Dialector, options ...gorm.Option) (*Store, error) {
	if dialector != nil && dialector.Name() != "sqlite" {
		return nil, errors.New("dbstore: server databases require Migrate followed by Connect")
	}
	store, sqlDB, err := openConnection(ctx, dialector, options...)
	if err != nil {
		return nil, err
	}
	if err := store.initialize(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	store.configurePool(sqlDB)
	return store, nil
}

// Connect opens a Store only when its schema has already been migrated.
func Connect(dialector gorm.Dialector, options ...gorm.Option) (*Store, error) {
	return ConnectContext(context.Background(), dialector, options...)
}

// ConnectContext opens a Store whose schema has already been migrated using ctx.
func ConnectContext(ctx context.Context, dialector gorm.Dialector, options ...gorm.Option) (*Store, error) {
	store, sqlDB, err := openConnection(ctx, dialector, options...)
	if err != nil {
		return nil, err
	}
	if err := store.verifySchema(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	store.configurePool(sqlDB)
	return store, nil
}

// Migrate applies the forward-only Store schema and closes its migration
// connection. Upgrading an existing schema requires an offline maintenance
// window: stop every application writer before calling Migrate, then start only
// binaries that support the resulting schema version. The database advisory
// lock serializes migrators; it cannot make an old application writer cooperate.
func Migrate(dialector gorm.Dialector, options ...gorm.Option) error {
	return MigrateContext(context.Background(), dialector, options...)
}

// MigrateContext applies the forward-only Store schema using ctx.
func MigrateContext(ctx context.Context, dialector gorm.Dialector, options ...gorm.Option) error {
	store, sqlDB, err := openConnection(ctx, dialector, options...)
	if err != nil {
		return err
	}
	defer sqlDB.Close()
	if store.dialect == "sqlite" {
		return store.initializeWithRepair(ctx, true)
	}
	return store.withMigrationLock(ctx, func(locked *Store) error {
		return locked.initializeWithRepair(ctx, true)
	})
}

func (store *Store) withMigrationLock(ctx context.Context, migrate func(*Store) error) error {
	return store.db.WithContext(ctx).Connection(func(connection *gorm.DB) (returnErr error) {
		sqlConnection, ok := connection.Statement.ConnPool.(*sql.Conn)
		if !ok {
			return errors.New("dbstore: migration did not receive a dedicated SQL connection")
		}
		locked, err := store.acquireMigrationLock(ctx, sqlConnection)
		if err != nil {
			return err
		}
		if !locked {
			return fmt.Errorf("dbstore: timed out after %s waiting for the schema migration lock", migrationLockWait)
		}
		defer func() {
			if err := store.releaseMigrationLock(ctx, sqlConnection); err != nil {
				returnErr = errors.Join(returnErr, err)
			}
		}()
		migrationStore := *store
		// Initialized forces a clean Statement while retaining the dedicated
		// connection's ConnPool and connection-scoped advisory lock.
		migrationStore.db = connection.Session(&gorm.Session{NewDB: true, Initialized: true})
		return migrate(&migrationStore)
	})
}

func (store *Store) acquireMigrationLock(ctx context.Context, connection *sql.Conn) (bool, error) {
	switch store.dialect {
	case "mysql":
		return acquireMySQLMigrationLock(ctx, connection)
	case "postgres":
		return acquirePostgreSQLMigrationLock(ctx, connection)
	default:
		return false, fmt.Errorf("dbstore: migration lock is unsupported for dialect %q", store.dialect)
	}
}

func acquireMySQLMigrationLock(ctx context.Context, connection *sql.Conn) (bool, error) {
	var acquired sql.NullInt64
	if err := connection.QueryRowContext(
		ctx, "SELECT GET_LOCK(?, ?)", migrationLockName, int(migrationLockWait.Seconds()),
	).Scan(&acquired); err != nil {
		return false, fmt.Errorf("dbstore: acquire MySQL migration lock: %w", err)
	}
	return acquired.Valid && acquired.Int64 == 1, nil
}

func acquirePostgreSQLMigrationLock(ctx context.Context, connection *sql.Conn) (bool, error) {
	deadline := time.Now().Add(migrationLockWait)
	for {
		var acquired bool
		if err := connection.QueryRowContext(
			ctx, "SELECT pg_try_advisory_lock($1)", migrationLockKey,
		).Scan(&acquired); err != nil {
			return false, fmt.Errorf("dbstore: acquire PostgreSQL migration lock: %w", err)
		}
		if acquired {
			return true, nil
		}
		if time.Now().After(deadline) {
			return false, nil
		}
		if err := waitForMigrationLockRetry(ctx); err != nil {
			return false, err
		}
	}
}

func waitForMigrationLockRetry(ctx context.Context) error {
	timer := time.NewTimer(migrationLockPoll)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (store *Store) releaseMigrationLock(ctx context.Context, connection *sql.Conn) error {
	switch store.dialect {
	case "mysql":
		return releaseMySQLMigrationLock(ctx, connection)
	case "postgres":
		return releasePostgreSQLMigrationLock(ctx, connection)
	default:
		return nil
	}
}

func releaseMySQLMigrationLock(ctx context.Context, connection *sql.Conn) error {
	var released sql.NullInt64
	if err := connection.QueryRowContext(ctx, "SELECT RELEASE_LOCK(?)", migrationLockName).
		Scan(&released); err != nil {
		return fmt.Errorf("dbstore: release MySQL migration lock: %w", err)
	}
	if !released.Valid || released.Int64 != 1 {
		return errors.New("dbstore: MySQL migration lock was not held while releasing it")
	}
	return nil
}

func releasePostgreSQLMigrationLock(ctx context.Context, connection *sql.Conn) error {
	var released bool
	if err := connection.QueryRowContext(ctx, "SELECT pg_advisory_unlock($1)", migrationLockKey).
		Scan(&released); err != nil {
		return fmt.Errorf("dbstore: release PostgreSQL migration lock: %w", err)
	}
	if !released {
		return errors.New("dbstore: PostgreSQL migration lock was not held while releasing it")
	}
	return nil
}

func openConnection(ctx context.Context, dialector gorm.Dialector, options ...gorm.Option) (*Store, *sql.DB, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if dialector == nil {
		return nil, nil, errors.New("dbstore: dialector is required")
	}
	dialect := dialector.Name()
	if dialect != "sqlite" && dialect != "mysql" && dialect != "postgres" {
		return nil, nil, fmt.Errorf("dbstore: unsupported dialect %q", dialect)
	}
	defaultLogger := logger.New(log.New(io.Discard, "", 0), logger.Config{
		LogLevel:             logger.Silent,
		ParameterizedQueries: true,
	})
	options = append([]gorm.Option{&gorm.Config{
		Logger: defaultLogger, TranslateError: true, DisableAutomaticPing: true,
	}}, options...)
	db, err := gorm.Open(dialector, options...)
	if err != nil {
		return nil, nil, fmt.Errorf("dbstore: open %s: %w", dialect, err)
	}
	store := &Store{db: db, dialect: dialect, now: time.Now}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, nil, fmt.Errorf("dbstore: access %s connection: %w", dialect, err)
	}
	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, nil, fmt.Errorf("dbstore: ping %s: %w", dialect, err)
	}
	if dialect == "sqlite" {
		if err := db.WithContext(ctx).Exec("PRAGMA busy_timeout = 5000").Error; err != nil {
			_ = sqlDB.Close()
			return nil, nil, fmt.Errorf("dbstore: configure sqlite: %w", err)
		}
	}
	return store, sqlDB, nil
}

func (store *Store) nowUTC() time.Time {
	if store.now == nil {
		return time.Now().UTC()
	}
	return store.now().UTC()
}

func (store *Store) configurePool(sqlDB *sql.DB) {
	if store.dialect == "sqlite" {
		sqlDB.SetMaxOpenConns(1)
		sqlDB.SetMaxIdleConns(1)
	}
}

func (store *Store) verifySchema(ctx context.Context) error {
	db := store.db.WithContext(ctx)
	if !db.Migrator().HasTable(&schemaMigrationRow{}) {
		return errors.New("dbstore: schema is not initialized; run Migrate before Connect")
	}
	var migration schemaMigrationRow
	err := db.Order("version DESC").Take(&migration).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return errors.New("dbstore: schema is not initialized; run Migrate before Connect")
	}
	if err != nil {
		return fmt.Errorf("dbstore: inspect schema version: %w", err)
	}
	if migration.Version != schemaVersion {
		return fmt.Errorf(
			"dbstore: schema version %d does not match supported version %d; run Migrate",
			migration.Version,
			schemaVersion,
		)
	}
	requirements := []schemaRequirement{
		{
			model: &checkpointRow{}, table: checkpointTable, primaryKey: []string{"run_id", "version"},
			columns: []schemaColumnRequirement{
				{name: "run_id", kind: schemaColumnString, length: maxIndexedIDBytes, binaryID: true},
				{name: "version", kind: schemaColumnInteger},
				{name: "record_json", kind: schemaColumnBytes},
				{name: "payload", kind: schemaColumnBytes},
			},
		},
		{
			model: &runLogRow{}, table: eventTable, primaryKey: []string{"ordinal"}, autoIncrement: "ordinal",
			columns: []schemaColumnRequirement{
				{name: "ordinal", kind: schemaColumnInteger},
				{name: "session_id", kind: schemaColumnString, length: maxIndexedIDBytes, binaryID: true},
				{name: "run_id", kind: schemaColumnString, length: maxIndexedIDBytes, binaryID: true},
				{name: "sequence", kind: schemaColumnInteger},
				{name: "record_json", kind: schemaColumnBytes},
			},
		},
		{
			model: &runLogRetentionRow{}, table: eventRetentionTable, primaryKey: []string{"ordinal"},
			columns: []schemaColumnRequirement{
				{name: "ordinal", kind: schemaColumnInteger},
				{name: "run_id", kind: schemaColumnString, length: maxIndexedIDBytes, binaryID: true},
				{name: "appended_at_unix_nano", kind: schemaColumnInteger},
			},
		},
		{
			model: &runRow{}, table: runTable, primaryKey: []string{"run_id"},
			columns: []schemaColumnRequirement{
				{name: "run_id", kind: schemaColumnString, length: maxIndexedIDBytes, binaryID: true},
				{name: "session_id", kind: schemaColumnString, length: maxIndexedIDBytes, binaryID: true},
				{name: "status", kind: schemaColumnString, length: maxRunStatusBytes},
				{name: "version", kind: schemaColumnInteger},
				{name: "created_at_unix_nano", kind: schemaColumnInteger},
				{name: "updated_at_unix_nano", kind: schemaColumnInteger},
			},
		},
		{
			model: &runRegistryRow{}, table: registryTable, primaryKey: []string{"run_id"},
			columns: []schemaColumnRequirement{
				{name: "run_id", kind: schemaColumnString, length: maxIndexedIDBytes, binaryID: true},
				{name: "kind", kind: schemaColumnString, length: maxRegistryTagBytes},
				{name: "state", kind: schemaColumnString, length: maxRegistryTagBytes},
				{name: "updated_at_unix_nano", kind: schemaColumnInteger},
				{name: "compacted_through_sequence", kind: schemaColumnInteger},
			},
		},
		{
			model: &schemaMigrationRow{}, table: (schemaMigrationRow{}).TableName(), primaryKey: []string{"version"},
			columns: []schemaColumnRequirement{
				{name: "version", kind: schemaColumnInteger},
				{name: "applied_at_unix_nano", kind: schemaColumnInteger},
			},
		},
	}
	for _, requirement := range requirements {
		if err := store.verifySchemaRequirement(ctx, requirement); err != nil {
			return err
		}
	}
	for _, index := range []struct {
		model any
		name  string
	}{
		{&runLogRow{}, "gopact_runlog_run_sequence"},
		{&runLogRow{}, "gopact_runlog_session_ordinal"},
		{&runLogRetentionRow{}, "gopact_runlog_retention_time"},
		{&runLogRetentionRow{}, "gopact_runlog_retention_run"},
		{&runRow{}, "gopact_workflow_runs_retention"},
		{&runRegistryRow{}, "gopact_run_registry_retention"},
	} {
		if !db.Migrator().HasIndex(index.model, index.name) {
			return fmt.Errorf(
				"dbstore: schema version %d is missing index %s; repair the schema and rerun Migrate",
				schemaVersion,
				index.name,
			)
		}
	}
	if err := store.verifyRunLogIdentityIndex(ctx); err != nil {
		return err
	}
	if err := store.verifyMySQLTableOptions(ctx); err != nil {
		return err
	}
	gap, err := store.v2MetadataGap(ctx)
	if err != nil {
		return err
	}
	if gap != "" {
		return fmt.Errorf("dbstore: schema v2 metadata is incomplete (%s); run Migrate", gap)
	}
	return nil
}

func (store *Store) verifySchemaRequirement(ctx context.Context, requirement schemaRequirement) error {
	db := store.db.WithContext(ctx)
	if !db.Migrator().HasTable(requirement.model) {
		return fmt.Errorf("dbstore: schema version %d is incomplete; run Migrate", schemaVersion)
	}
	columnTypes, err := db.Migrator().ColumnTypes(requirement.model)
	if err != nil {
		return fmt.Errorf("dbstore: inspect schema columns for %s: %w", requirement.table, err)
	}
	byName := make(map[string]gorm.ColumnType, len(columnTypes))
	for _, columnType := range columnTypes {
		byName[strings.ToLower(columnType.Name())] = columnType
	}
	for _, column := range requirement.columns {
		columnType := byName[column.name]
		if err := store.verifySchemaColumn(ctx, requirement, column, columnType); err != nil {
			return err
		}
	}
	if err := store.verifyPrimaryKey(ctx, requirement); err != nil {
		return err
	}
	if store.dialect != "sqlite" {
		return nil
	}
	return store.verifySQLiteAutoIncrement(ctx, requirement)
}

func (store *Store) verifySchemaColumn(ctx context.Context, requirement schemaRequirement, column schemaColumnRequirement, columnType gorm.ColumnType) error {
	if columnType == nil {
		return fmt.Errorf(
			"dbstore: schema version %d is missing %s.%s; repair the schema and rerun Migrate",
			schemaVersion,
			requirement.table,
			column.name,
		)
	}
	if !store.validSchemaColumnType(column.kind, columnType.DatabaseTypeName()) {
		return fmt.Errorf(
			"dbstore: schema column %s.%s has type %s; repair the schema and rerun Migrate",
			requirement.table,
			column.name,
			columnType.DatabaseTypeName(),
		)
	}
	if err := store.verifySchemaColumnLength(requirement, column, columnType); err != nil {
		return err
	}
	nullable, ok := columnType.Nullable()
	integerPrimaryKey := store.dialect == "sqlite" && column.name == requirement.autoIncrement
	if !ok || (nullable && !integerPrimaryKey) {
		return fmt.Errorf(
			"dbstore: schema column %s.%s must be NOT NULL; repair the schema and rerun Migrate",
			requirement.table,
			column.name,
		)
	}
	if err := store.verifySchemaAutoIncrement(requirement, column, columnType); err != nil {
		return err
	}
	if store.dialect != "mysql" || !column.binaryID {
		return nil
	}
	return store.verifyMySQLIDColumnCollation(ctx, requirement.table, column.name)
}

func (store *Store) verifySchemaColumnLength(requirement schemaRequirement, column schemaColumnRequirement, columnType gorm.ColumnType) error {
	if column.length == 0 || store.dialect == "sqlite" {
		return nil
	}
	length, ok := columnType.Length()
	if !ok || length != column.length {
		return fmt.Errorf(
			"dbstore: schema column %s.%s has length %d; want %d",
			requirement.table,
			column.name,
			length,
			column.length,
		)
	}
	return nil
}

func (store *Store) verifySchemaAutoIncrement(requirement schemaRequirement, column schemaColumnRequirement, columnType gorm.ColumnType) error {
	if store.dialect == "sqlite" {
		return nil
	}
	autoIncrement, _ := columnType.AutoIncrement()
	if autoIncrement == (column.name == requirement.autoIncrement) {
		return nil
	}
	return fmt.Errorf(
		"dbstore: schema column %s.%s has the wrong auto-increment setting",
		requirement.table,
		column.name,
	)
}

func (store *Store) validSchemaColumnType(kind schemaColumnKind, actual string) bool {
	actual = strings.ToLower(actual)
	switch store.dialect {
	case "sqlite":
		return kind == schemaColumnString && actual == "text" ||
			kind == schemaColumnInteger && actual == "integer" ||
			kind == schemaColumnBytes && actual == "blob"
	case "mysql":
		return kind == schemaColumnString && actual == "varchar" ||
			kind == schemaColumnInteger && actual == "bigint" ||
			kind == schemaColumnBytes && actual == "longblob"
	case "postgres":
		return kind == schemaColumnString && actual == "varchar" ||
			kind == schemaColumnInteger && (actual == "int8" || actual == "bigint") ||
			kind == schemaColumnBytes && actual == "bytea"
	default:
		return false
	}
}

func (store *Store) verifyPrimaryKey(ctx context.Context, requirement schemaRequirement) error {
	var columns []string
	var result *gorm.DB
	switch store.dialect {
	case "sqlite":
		result = store.db.WithContext(ctx).Raw(
			`SELECT name FROM pragma_table_info(?) WHERE pk > 0 ORDER BY pk`,
			requirement.table,
		).Scan(&columns)
	case "mysql":
		result = store.db.WithContext(ctx).Raw(
			`SELECT column_name FROM information_schema.statistics
				WHERE table_schema = DATABASE() AND table_name = ? AND index_name = 'PRIMARY'
				ORDER BY seq_in_index`,
			requirement.table,
		).Scan(&columns)
	case "postgres":
		result = store.db.WithContext(ctx).Raw(
			`SELECT keys.column_name
				FROM information_schema.table_constraints AS constraints
				JOIN information_schema.key_column_usage AS keys
					ON keys.constraint_catalog = constraints.constraint_catalog
					AND keys.constraint_schema = constraints.constraint_schema
					AND keys.constraint_name = constraints.constraint_name
				WHERE constraints.table_schema = current_schema()
				AND constraints.table_name = ? AND constraints.constraint_type = 'PRIMARY KEY'
				ORDER BY keys.ordinal_position`,
			requirement.table,
		).Scan(&columns)
	}
	if result == nil || result.Error != nil {
		if result == nil {
			return fmt.Errorf("dbstore: verify primary key for unsupported dialect %q", store.dialect)
		}
		return fmt.Errorf("dbstore: verify primary key for %s: %w", requirement.table, result.Error)
	}
	if strings.Join(columns, ",") != strings.Join(requirement.primaryKey, ",") {
		return fmt.Errorf(
			"dbstore: schema table %s primary key is (%s); want (%s)",
			requirement.table,
			strings.Join(columns, ","),
			strings.Join(requirement.primaryKey, ","),
		)
	}
	return nil
}

func (store *Store) verifySQLiteAutoIncrement(ctx context.Context, requirement schemaRequirement) error {
	var ddl string
	if err := store.db.WithContext(ctx).Raw(
		`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?`,
		requirement.table,
	).Row().Scan(&ddl); err != nil {
		return fmt.Errorf("dbstore: inspect SQLite table %s: %w", requirement.table, err)
	}
	hasAutoIncrement := strings.Contains(strings.ToUpper(ddl), "AUTOINCREMENT")
	if hasAutoIncrement != (requirement.autoIncrement != "") {
		return fmt.Errorf(
			"dbstore: schema table %s has the wrong auto-increment setting",
			requirement.table,
		)
	}
	return nil
}

func (store *Store) verifyMySQLIDColumnCollation(ctx context.Context, table, column string) error {
	var collation sql.NullString
	err := store.db.WithContext(ctx).Raw(
		`SELECT collation_name FROM information_schema.columns
			WHERE table_schema = DATABASE() AND table_name = ? AND column_name = ?`,
		table,
		column,
	).Row().Scan(&collation)
	if err != nil {
		return fmt.Errorf("dbstore: inspect MySQL collation for %s.%s: %w", table, column, err)
	}
	if !collation.Valid || !strings.EqualFold(collation.String, "utf8mb4_bin") {
		return fmt.Errorf(
			"dbstore: schema column %s.%s must use utf8mb4_bin collation",
			table,
			column,
		)
	}
	return nil
}

func (store *Store) verifyRunLogIdentityIndex(ctx context.Context) error {
	var valid int
	var err error
	switch store.dialect {
	case "sqlite":
		err = store.db.WithContext(ctx).Raw(
			`SELECT 1 FROM pragma_index_list('`+eventTable+`') AS indexes
				WHERE indexes.name = ? AND indexes."unique" = 1
				AND (
					SELECT group_concat(name, ',') FROM (
						SELECT name FROM pragma_index_info('gopact_runlog_run_sequence') ORDER BY seqno
					)
				) = 'run_id,sequence' LIMIT 1`,
			"gopact_runlog_run_sequence",
		).Scan(&valid).Error
	case "mysql":
		err = store.db.WithContext(ctx).Raw(
			`SELECT 1 FROM (
				SELECT non_unique, GROUP_CONCAT(column_name ORDER BY seq_in_index SEPARATOR ',') AS columns_csv
				FROM information_schema.statistics
				WHERE table_schema = DATABASE() AND table_name = ? AND index_name = ?
				GROUP BY non_unique
			) AS identity_index
			WHERE non_unique = 0 AND columns_csv = 'run_id,sequence' LIMIT 1`,
			eventTable,
			"gopact_runlog_run_sequence",
		).Scan(&valid).Error
	case "postgres":
		err = store.db.WithContext(ctx).Raw(
			`SELECT 1
				FROM pg_class AS indexes
				JOIN pg_index AS definition ON definition.indexrelid = indexes.oid
				JOIN pg_class AS tables ON tables.oid = definition.indrelid
				JOIN pg_namespace AS namespaces ON namespaces.oid = tables.relnamespace
				WHERE namespaces.nspname = current_schema()
				AND tables.relname = ? AND indexes.relname = ?
				AND definition.indisunique AND definition.indisvalid
				AND (
					SELECT array_agg(attributes.attname ORDER BY keys.ordinality)
					FROM unnest(definition.indkey) WITH ORDINALITY AS keys(attnum, ordinality)
					JOIN pg_attribute AS attributes
						ON attributes.attrelid = tables.oid AND attributes.attnum = keys.attnum
				) = ARRAY['run_id', 'sequence']::name[]
				LIMIT 1`,
			eventTable,
			"gopact_runlog_run_sequence",
		).Scan(&valid).Error
	}
	if err != nil {
		return fmt.Errorf("dbstore: verify RunLog identity index: %w", err)
	}
	if valid != 1 {
		return errors.New(
			"dbstore: RunLog identity index must uniquely cover (run_id, sequence); repair the schema and rerun Migrate",
		)
	}
	return nil
}

func (store *Store) verifyMySQLTableOptions(ctx context.Context) error {
	if store.dialect != "mysql" {
		return nil
	}
	tables := []string{
		checkpointTable,
		eventTable,
		eventRetentionTable,
		runTable,
		registryTable,
		(schemaMigrationRow{}).TableName(),
	}
	var valid int64
	err := store.db.WithContext(ctx).Raw(
		`SELECT COUNT(*) FROM information_schema.tables
			WHERE table_schema = DATABASE() AND table_name IN ?
			AND UPPER(engine) = 'INNODB' AND LOWER(table_collation) = 'utf8mb4_bin'`,
		tables,
	).Scan(&valid).Error
	if err != nil {
		return fmt.Errorf("dbstore: verify MySQL table options: %w", err)
	}
	if valid != int64(len(tables)) {
		return errors.New(
			"dbstore: every Store table must use InnoDB and utf8mb4_bin; repair the schema and rerun Migrate",
		)
	}
	return nil
}

// Close releases the database connection.
func (store *Store) Close() error {
	if store == nil || store.db == nil {
		return nil
	}
	db, err := store.db.DB()
	if err != nil {
		return err
	}
	return db.Close()
}

// SQLDB returns the underlying connection pool for production pool tuning and health checks.
func (store *Store) SQLDB() (*sql.DB, error) {
	if store == nil || store.db == nil {
		return nil, errors.New("dbstore: store is nil")
	}
	return store.db.DB()
}

// GORMDB returns the underlying GORM handle for application-specific queries
// and transactions. Callers must not close it, mutate Store-owned tables or
// schema, or assume Store methods join a transaction started on this handle.
func (store *Store) GORMDB() (*gorm.DB, error) {
	if store == nil || store.db == nil {
		return nil, errors.New("dbstore: store is nil")
	}
	return store.db, nil
}

func (store *Store) initialize(ctx context.Context) error {
	return store.initializeWithRepair(ctx, true)
}

func (store *Store) initializeWithRepair(ctx context.Context, repair bool) error {
	version, err := store.inspectSchemaVersion(ctx)
	if err != nil {
		return err
	}
	if version > schemaVersion {
		return fmt.Errorf(
			"dbstore: schema version %d is newer than supported version %d",
			version,
			schemaVersion,
		)
	}
	if version == schemaVersion {
		return store.initializeCurrentSchema(ctx, repair)
	}
	if err := store.validateLegacyIndexedIDs(ctx); err != nil {
		return err
	}
	if err := store.migrationDB(ctx).AutoMigrate(&schemaMigrationRow{}); err != nil {
		return fmt.Errorf("dbstore: initialize migration schema: %w", err)
	}
	if version < 1 {
		if err := store.migrateV1Schema(ctx); err != nil {
			return fmt.Errorf("dbstore: initialize %s schema version 1: %w", store.dialect, err)
		}
		if err := store.backfillRunHeads(ctx); err != nil {
			return err
		}
		if err := store.recordSchemaVersion(ctx, 1); err != nil {
			return err
		}
		version = 1
	}
	if version < schemaVersion {
		if err := store.migrationDB(ctx).AutoMigrate(&runLogRetentionRow{}, &runRegistryRow{}); err != nil {
			return fmt.Errorf(
				"dbstore: initialize %s schema version 2: %w",
				store.dialect,
				err,
			)
		}
		if err := store.initializeSQLiteCompatibilityTriggers(ctx); err != nil {
			return err
		}
		if err := store.reconcileV2Metadata(ctx); err != nil {
			return err
		}
		if err := store.recordSchemaVersion(ctx, schemaVersion); err != nil {
			return err
		}
	}
	return store.verifySchema(ctx)
}

func (store *Store) initializeCurrentSchema(ctx context.Context, repair bool) error {
	if repair {
		return store.repairCurrentSchema(ctx)
	}
	if err := store.initializeSQLiteCompatibilityTriggers(ctx); err != nil {
		return err
	}
	return store.verifySchema(ctx)
}

func (store *Store) repairCurrentSchema(ctx context.Context) error {
	if err := store.migrationDB(ctx).AutoMigrate(&runLogRetentionRow{}, &runRegistryRow{}); err != nil {
		return fmt.Errorf("dbstore: repair %s schema version 2: %w", store.dialect, err)
	}
	if err := store.initializeSQLiteCompatibilityTriggers(ctx); err != nil {
		return err
	}
	if err := store.reconcileV2Metadata(ctx); err != nil {
		return err
	}
	return store.verifySchema(ctx)
}

func (store *Store) inspectSchemaVersion(ctx context.Context) (int64, error) {
	db := store.db.WithContext(ctx)
	if !db.Migrator().HasTable(&schemaMigrationRow{}) {
		return 0, nil
	}
	var migration schemaMigrationRow
	err := db.Order("version DESC").Take(&migration).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("dbstore: inspect schema version: %w", err)
	}
	return migration.Version, nil
}

func (store *Store) recordSchemaVersion(ctx context.Context, version int64) error {
	err := store.db.WithContext(ctx).Create(&schemaMigrationRow{
		Version: version, AppliedAtUnixNano: time.Now().UTC().UnixNano(),
	}).Error
	if err != nil && !errors.Is(err, gorm.ErrDuplicatedKey) {
		return fmt.Errorf("dbstore: record schema version %d: %w", version, err)
	}
	return nil
}

func (store *Store) migrationDB(ctx context.Context) *gorm.DB {
	db := store.db.WithContext(ctx).Session(&gorm.Session{NewDB: true})
	if store.dialect == "mysql" {
		db = db.Set("gorm:table_options", "ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin")
	}
	return db
}

func (store *Store) migrateV1Schema(ctx context.Context) error {
	db := store.migrationDB(ctx)
	if store.dialect != "sqlite" || !db.Migrator().HasTable(&runLogRow{}) {
		return db.AutoMigrate(&checkpointRow{}, &runLogRow{}, &runRow{})
	}
	// Preserve existing RunLog rows in place. SQLite can add the only historical
	// missing column and both indexes without copying record_json.
	if err := db.AutoMigrate(&checkpointRow{}, &runRow{}); err != nil {
		return err
	}
	if !db.Migrator().HasColumn(&runLogRow{}, "session_id") {
		if err := db.Exec(
			`ALTER TABLE ` + eventTable + ` ADD COLUMN session_id TEXT NOT NULL DEFAULT ''`,
		).Error; err != nil {
			return err
		}
	}
	for _, statement := range []string{
		`CREATE UNIQUE INDEX IF NOT EXISTS gopact_runlog_run_sequence ON ` + eventTable + ` (run_id, sequence)`,
		`CREATE INDEX IF NOT EXISTS gopact_runlog_session_ordinal ON ` + eventTable + ` (session_id, ordinal)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			return err
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
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := store.ensureRunRegistry(tx, record.RunID, runKindWorkflow); err != nil {
			return err
		}
		if record.LeaseDuration != 0 {
			now, err := store.databaseNow(tx)
			if err != nil {
				return err
			}
			record.LeaseExpiresAt, err = resolveLeaseExpiry(now, record.LeaseExpiresAt, record.LeaseDuration)
			if err != nil || !record.LeaseExpiresAt.After(now) {
				return fmt.Errorf("%w: new checkpoint lease must be in the future", workflow.ErrInvalidCheckpoint)
			}
		}
		encoded, err := encodeCheckpointMetadata(record)
		if err != nil {
			return err
		}
		if err := tx.Create(&checkpointRow{
			RunID: record.RunID, Version: record.Version, RecordJSON: encoded, Payload: record.Payload,
		}).Error; err != nil {
			return err
		}
		return tx.Create(runHead(record)).Error
	})
	if errors.Is(err, gorm.ErrDuplicatedKey) || errors.Is(err, errRunKindConflict) || errors.Is(err, errRunRetired) {
		return workflow.ErrCheckpointExists
	}
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return fmt.Errorf("dbstore: create checkpoint: %w", err)
	}
	return nil
}

// Load returns the latest checkpoint for a run.
func (store *Store) Load(ctx context.Context, runID string) (workflow.CheckpointRecord, error) {
	if err := store.ready(ctx); err != nil {
		return workflow.CheckpointRecord{}, err
	}
	if err := validateIndexedID("run id", runID); err != nil {
		return workflow.CheckpointRecord{}, fmt.Errorf("%w: %v", workflow.ErrInvalidCheckpoint, err)
	}
	var row checkpointRow
	err := activeCheckpointRows(store.db.WithContext(ctx), runID).
		Order("checkpoints.version DESC").Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return workflow.CheckpointRecord{}, workflow.ErrCheckpointNotFound
	}
	if err != nil {
		return workflow.CheckpointRecord{}, fmt.Errorf("dbstore: load checkpoint: %w", err)
	}
	return decodeCheckpoint(row.RecordJSON, row.Payload)
}

func activeCheckpointRows(db *gorm.DB, runID string) *gorm.DB {
	return db.Table(checkpointTable+" AS checkpoints").Select("checkpoints.*").
		Joins("JOIN "+registryTable+" AS registry ON registry.run_id = checkpoints.run_id").
		Where(
			"checkpoints.run_id = ? AND registry.kind = ? AND registry.state = ?",
			runID,
			runKindWorkflow,
			runStateActive,
		)
}

func runHead(record workflow.CheckpointRecord) *runRow {
	return &runRow{
		RunID: record.RunID, SessionID: record.SessionID, Status: string(record.Status), Version: record.Version,
		CreatedAtUnixNano: record.CreatedAt.UnixNano(), UpdatedAtUnixNano: record.UpdatedAt.UnixNano(),
	}
}

func encodeCheckpointMetadata(record workflow.CheckpointRecord) ([]byte, error) {
	metadata := record
	metadata.Payload = nil
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return nil, fmt.Errorf("dbstore: encode checkpoint: %w", err)
	}
	if len(encoded)+len(record.Payload) > maxCheckpointBytes {
		return nil, fmt.Errorf("%w: encoded checkpoint exceeds %d bytes", workflow.ErrInvalidCheckpoint, maxCheckpointBytes)
	}
	return encoded, nil
}

func decodeCheckpoint(metadata, payload []byte) (workflow.CheckpointRecord, error) {
	var record workflow.CheckpointRecord
	if err := json.Unmarshal(metadata, &record); err != nil {
		return workflow.CheckpointRecord{}, fmt.Errorf("dbstore: decode checkpoint: %w", err)
	}
	record.Payload = append([]byte(nil), payload...)
	return record, nil
}

func (store *Store) ready(ctx context.Context) error {
	if store == nil || store.db == nil {
		return errors.New("dbstore: store is nil")
	}
	return ctx.Err()
}

func validateCheckpoint(record workflow.CheckpointRecord) error {
	for _, field := range []struct{ name, value string }{
		{name: "checkpoint id", value: record.ID},
		{name: "session id", value: record.SessionID},
		{name: "run id", value: record.RunID},
		{name: "workflow name", value: record.WorkflowName},
		{name: "topology", value: record.TopologyVersion},
	} {
		if field.value == "" {
			return fmt.Errorf("%w: %s is required", workflow.ErrInvalidCheckpoint, field.name)
		}
	}
	for _, field := range []struct{ name, value string }{
		{name: "session id", value: record.SessionID},
		{name: "run id", value: record.RunID},
	} {
		if err := validateIndexedID(field.name, field.value); err != nil {
			return fmt.Errorf("%w: %v", workflow.ErrInvalidCheckpoint, err)
		}
	}
	if record.SchemaVersion <= 0 {
		return fmt.Errorf("%w: checkpoint schema version is required", workflow.ErrInvalidCheckpoint)
	}
	if record.LeaseDuration < 0 {
		return fmt.Errorf("%w: checkpoint lease duration must not be negative", workflow.ErrInvalidCheckpoint)
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

func validateIndexedID(name, value string) error {
	if value == "" {
		return fmt.Errorf("%s is required", name)
	}
	if len(value) > maxIndexedIDBytes {
		return fmt.Errorf("%s exceeds %d bytes", name, maxIndexedIDBytes)
	}
	if !utf8.ValidString(value) {
		return fmt.Errorf("%s must be valid UTF-8", name)
	}
	if strings.IndexByte(value, 0) >= 0 {
		return fmt.Errorf("%s must not contain NUL", name)
	}
	if strings.HasSuffix(value, " ") {
		return fmt.Errorf("%s must not end with a space", name)
	}
	return nil
}
