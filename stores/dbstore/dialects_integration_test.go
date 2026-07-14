//go:build dbintegration

package dbstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gopact-ai/gopact/runlog"
	"github.com/gopact-ai/gopact/workflow"
	gormmysql "gorm.io/driver/mysql"
	gormpostgres "gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestStoreRealDialects(t *testing.T) {
	tests := []struct {
		name      string
		env       string
		dialector func(string) gorm.Dialector
	}{
		{name: "mysql", env: "GOPACT_TEST_MYSQL_DSN", dialector: gormmysql.Open},
		{name: "mariadb", env: "GOPACT_TEST_MARIADB_DSN", dialector: gormmysql.Open},
		{name: "postgres", env: "GOPACT_TEST_POSTGRES_DSN", dialector: gormpostgres.Open},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dsn := os.Getenv(test.env)
			if dsn == "" {
				t.Skipf("%s is not set", test.env)
			}

			prefix := fmt.Sprintf("dbintegration-%s-%d", test.name, time.Now().UnixNano())
			testIntegrationPopulatedV1Upgrade(t, test.dialector, dsn, prefix+"-upgrade")
			if err := Migrate(test.dialector(dsn)); err != nil {
				t.Fatalf("Migrate(%s) error = %v", test.name, err)
			}
			testConcurrentMigrate(t, test.dialector, dsn)
			store, err := Connect(test.dialector(dsn))
			if err != nil {
				t.Fatalf("Connect(%s) error = %v", test.name, err)
			}
			t.Cleanup(func() {
				if err := store.Close(); err != nil {
					t.Errorf("Close(%s) error = %v", test.name, err)
				}
			})
			if err := clearIntegrationData(store); err != nil {
				t.Fatalf("clear %s data: %v", test.name, err)
			}
			testIntegrationSchemaDriftGate(t, store, test.dialector(dsn), test.name)
			t.Cleanup(func() {
				if err := clearIntegrationData(store); err != nil {
					t.Errorf("cleanup %s data: %v", test.name, err)
				}
			})

			var migrations int64
			if err := store.db.Model(&schemaMigrationRow{}).Count(&migrations).Error; err != nil {
				t.Fatalf("count %s migrations: %v", test.name, err)
			}
			if migrations == 0 {
				t.Fatalf("%s schema migration was removed", test.name)
			}

			testIntegrationMigrationRepair(t, store, test.dialector(dsn), prefix+"-repair")
			if err := clearIntegrationData(store); err != nil {
				t.Fatalf("clear %s repair fixture: %v", test.name, err)
			}
			testIntegrationLifecycle(t, store, prefix+"-lifecycle")
			testIntegrationConfirmedRunLogPurge(t, store, prefix+"-confirmed-prefix")
			testIntegrationRunIDCase(t, store, prefix)
			testIntegrationJournalPurge(t, store, prefix+"-journal")
		})
	}
}

type integrationSchemaDrift struct {
	mutateSQL  string
	restoreSQL string
	want       string
}

func testIntegrationSchemaDriftGate(t *testing.T, store *Store, dialector gorm.Dialector, dialect string) {
	t.Helper()
	switch dialect {
	case "mysql", "mariadb":
		assertIntegrationSchemaDriftRejected(
			t,
			store,
			dialector,
			integrationSchemaDrift{
				mutateSQL: `ALTER TABLE gopact_run_registry MODIFY run_id varchar(191)
					CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci NOT NULL`,
				restoreSQL: `ALTER TABLE gopact_run_registry MODIFY run_id varchar(191)
					CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL`,
				want: "utf8mb4_bin",
			},
		)
		assertIntegrationSchemaDriftRejected(
			t,
			store,
			dialector,
			integrationSchemaDrift{
				mutateSQL:  `ALTER TABLE gopact_run_registry MODIFY kind varchar(15) NOT NULL`,
				restoreSQL: `ALTER TABLE gopact_run_registry MODIFY kind varchar(16) NOT NULL`,
				want:       "length",
			},
		)
	case "postgres":
		assertIntegrationSchemaDriftRejected(
			t,
			store,
			dialector,
			integrationSchemaDrift{
				mutateSQL:  `ALTER TABLE gopact_run_registry ALTER COLUMN kind TYPE varchar(15)`,
				restoreSQL: `ALTER TABLE gopact_run_registry ALTER COLUMN kind TYPE varchar(16)`,
				want:       "length",
			},
		)
		assertIntegrationSchemaDriftRejected(
			t,
			store,
			dialector,
			integrationSchemaDrift{
				mutateSQL:  `ALTER TABLE gopact_run_registry DROP CONSTRAINT gopact_run_registry_pkey`,
				restoreSQL: `ALTER TABLE gopact_run_registry ADD PRIMARY KEY (run_id)`,
				want:       "primary key",
			},
		)
	}
}

func assertIntegrationSchemaDriftRejected(t *testing.T, store *Store, dialector gorm.Dialector, drift integrationSchemaDrift) {
	t.Helper()
	if err := store.db.Exec(drift.mutateSQL).Error; err != nil {
		t.Fatalf("mutate schema: %v", err)
	}
	connected, connectErr := Connect(dialector)
	if connected != nil {
		_ = connected.Close()
	}
	restoreErr := store.db.Exec(drift.restoreSQL).Error
	if restoreErr != nil {
		t.Fatalf("restore schema after Connect() error %v: %v", connectErr, restoreErr)
	}
	if connectErr == nil || !strings.Contains(connectErr.Error(), drift.want) {
		t.Fatalf("Connect() error = %v, want schema rejection containing %q", connectErr, drift.want)
	}
}

type integrationV1Fixture struct {
	checkpoint    workflow.CheckpointRecord
	workflowEvent runlog.Record
	journalEvent  runlog.Record
}

func testIntegrationPopulatedV1Upgrade(t *testing.T, dialector func(string) gorm.Dialector, dsn, prefix string) {
	t.Helper()
	fixture := prepareIntegrationV1(t, dialector(dsn), prefix)
	if err := Migrate(dialector(dsn)); err != nil {
		t.Fatalf("Migrate(populated v1) error = %v", err)
	}
	store, err := Connect(dialector(dsn))
	if err != nil {
		t.Fatalf("Connect(upgraded v1) error = %v", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close(upgraded v1) error = %v", err)
		}
	}()
	verifyIntegrationV1Upgrade(t, store, fixture)
}

func prepareIntegrationV1(t *testing.T, dialector gorm.Dialector, prefix string) integrationV1Fixture {
	t.Helper()
	store, sqlDB, err := openConnection(dialector)
	if err != nil {
		t.Fatalf("open v1 fixture connection: %v", err)
	}
	defer func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close v1 fixture connection: %v", err)
		}
	}()
	resetIntegrationStore(t, store)
	fixture := newIntegrationV1Fixture(prefix)
	installIntegrationV1(t, store, fixture)
	if store.db.Migrator().HasTable(&runLogRetentionRow{}) || store.db.Migrator().HasTable(&runRegistryRow{}) {
		t.Fatal("v1 fixture unexpectedly contains v2 metadata tables")
	}
	return fixture
}

func resetIntegrationStore(t *testing.T, store *Store) {
	t.Helper()
	models := []any{
		&runLogRetentionRow{},
		&runRegistryRow{},
		&runLogRow{},
		&checkpointRow{},
		&runRow{},
		&schemaMigrationRow{},
	}
	for _, model := range models {
		if err := store.db.Migrator().DropTable(model); err != nil {
			t.Fatalf("drop integration Store table: %v", err)
		}
	}
}

func newIntegrationV1Fixture(prefix string) integrationV1Fixture {
	checkpoint := testCheckpoint(prefix + "-workflow")
	return integrationV1Fixture{
		checkpoint: checkpoint,
		workflowEvent: runlog.Record{
			SessionID: checkpoint.SessionID, RunID: checkpoint.RunID, Sequence: 1,
			EventType: "upgrade.workflow", Source: "v1-writer", Timestamp: time.Now().UTC(),
		},
		journalEvent: runlog.Record{
			SessionID: "upgrade-journal", RunID: prefix + "-journal", Sequence: 1,
			EventType: "upgrade.journal", Source: "v1-writer", Timestamp: time.Now().UTC(),
		},
	}
}

func installIntegrationV1(t *testing.T, store *Store, fixture integrationV1Fixture) {
	t.Helper()
	ctx := t.Context()
	if err := store.migrationDB(ctx).AutoMigrate(
		&schemaMigrationRow{},
		&checkpointRow{},
		&runLogRow{},
		&runRow{},
	); err != nil {
		t.Fatalf("install v1 schema: %v", err)
	}
	metadata, err := encodeCheckpointMetadata(fixture.checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	events := []runlog.Record{fixture.workflowEvent, fixture.journalEvent}
	eventRows := make([]runLogRow, 0, len(events))
	for _, event := range events {
		encoded, err := encodeRunLogRecord(event)
		if err != nil {
			t.Fatal(err)
		}
		eventRows = append(eventRows, runLogRow{
			SessionID: event.SessionID, RunID: event.RunID, Sequence: event.Sequence, RecordJSON: encoded,
		})
	}
	err = store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&schemaMigrationRow{
			Version: 1, AppliedAtUnixNano: time.Now().UTC().UnixNano(),
		}).Error; err != nil {
			return err
		}
		if err := tx.Create(&checkpointRow{
			RunID:      fixture.checkpoint.RunID,
			Version:    fixture.checkpoint.Version,
			RecordJSON: metadata,
			Payload:    fixture.checkpoint.Payload,
		}).Error; err != nil {
			return err
		}
		if err := tx.Create(runHead(fixture.checkpoint)).Error; err != nil {
			return err
		}
		return tx.Create(&eventRows).Error
	})
	if err != nil {
		t.Fatalf("install populated v1 fixture: %v", err)
	}
}

func verifyIntegrationV1Upgrade(t *testing.T, store *Store, fixture integrationV1Fixture) {
	t.Helper()
	loaded := loadIntegrationCheckpoint(t, store, fixture.checkpoint.RunID)
	if loaded.RunID != fixture.checkpoint.RunID || loaded.Version != fixture.checkpoint.Version ||
		string(loaded.Payload) != string(fixture.checkpoint.Payload) {
		t.Fatalf("Load(upgraded v1) = %+v, want checkpoint %+v", loaded, fixture.checkpoint)
	}
	verifyIntegrationRunLog(t, store, fixture.workflowEvent)
	verifyIntegrationRunLog(t, store, fixture.journalEvent)

	events := []runlog.Record{fixture.workflowEvent, fixture.journalEvent}
	var retentionRows int64
	if err := store.db.Model(&runLogRetentionRow{}).Where("appended_at_unix_nano > 0").Count(&retentionRows).Error; err != nil {
		t.Fatalf("count upgraded retention rows: %v", err)
	}
	if retentionRows != int64(len(events)) {
		t.Fatalf("upgraded retention rows = %d, want %d", retentionRows, len(events))
	}
	var missingRetention int64
	if err := store.db.Table(eventTable + " AS events").
		Joins("LEFT JOIN " + eventRetentionTable + " AS retention ON retention.ordinal = events.ordinal").
		Where("retention.ordinal IS NULL").Count(&missingRetention).Error; err != nil {
		t.Fatalf("count RunLog rows missing retention metadata: %v", err)
	}
	if missingRetention != 0 {
		t.Fatalf("RunLog rows missing retention metadata = %d", missingRetention)
	}

	expectedRegistry := []struct {
		runID string
		kind  string
	}{
		{runID: fixture.workflowEvent.RunID, kind: runKindWorkflow},
		{runID: fixture.journalEvent.RunID, kind: runKindJournal},
	}
	var registryRows int64
	if err := store.db.Model(&runRegistryRow{}).Count(&registryRows).Error; err != nil {
		t.Fatalf("count upgraded registry rows: %v", err)
	}
	if registryRows != int64(len(expectedRegistry)) {
		t.Fatalf("upgraded registry rows = %d, want %d", registryRows, len(expectedRegistry))
	}
	for _, expected := range expectedRegistry {
		var row runRegistryRow
		if err := store.db.Where("run_id = ?", expected.runID).Take(&row).Error; err != nil {
			t.Fatalf("load upgraded registry %q: %v", expected.runID, err)
		}
		if row.Kind != expected.kind || row.State != runStateActive {
			t.Fatalf("upgraded registry %q = %+v, want kind %q and active", expected.runID, row, expected.kind)
		}
	}
}

func verifyIntegrationRunLog(t *testing.T, store *Store, expected runlog.Record) {
	t.Helper()
	records, err := store.List(t.Context(), runlog.Query{RunID: expected.RunID})
	if err != nil {
		t.Fatalf("List(upgraded v1 %q) error = %v", expected.RunID, err)
	}
	if len(records) != 1 {
		t.Fatalf("List(upgraded v1 %q) = %+v, want one record", expected.RunID, records)
	}
	actual := records[0]
	if actual.SessionID != expected.SessionID || actual.RunID != expected.RunID ||
		actual.Sequence != expected.Sequence || actual.EventType != expected.EventType {
		t.Fatalf("List(upgraded v1 %q) = %+v, want %+v", expected.RunID, actual, expected)
	}
}

func testConcurrentMigrate(t *testing.T, dialector func(string) gorm.Dialector, dsn string) {
	t.Helper()
	start := make(chan struct{})
	results := make(chan error, 2)
	var group sync.WaitGroup
	for range 2 {
		group.Go(func() {
			<-start
			results <- Migrate(dialector(dsn))
		})
	}
	close(start)
	group.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatalf("concurrent Migrate() error = %v", err)
		}
	}
}

func testIntegrationMigrationRepair(t *testing.T, store *Store, dialector gorm.Dialector, runID string) {
	t.Helper()
	record := runlog.Record{
		SessionID: "dbintegration-repair", RunID: runID, Sequence: 1,
		EventType: "repair.event", Source: "legacy-writer", Timestamp: time.Now().UTC(),
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.db.Exec(
		`INSERT INTO `+eventTable+` (session_id, run_id, sequence, record_json) VALUES (?, ?, ?, ?)`,
		record.SessionID,
		record.RunID,
		record.Sequence,
		encoded,
	).Error; err != nil {
		t.Fatalf("insert simulated legacy event: %v", err)
	}
	if records, err := store.List(t.Context(), runlog.Query{RunID: runID}); err != nil || len(records) != 0 {
		t.Fatalf("List() before metadata repair = %+v, %v; want hidden legacy event", records, err)
	}
	if err := Migrate(dialector); err != nil {
		t.Fatalf("Migrate(repair) error = %v", err)
	}
	records, err := store.List(t.Context(), runlog.Query{RunID: runID})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Sequence != record.Sequence {
		t.Fatalf("List() after metadata repair = %+v, want the simulated legacy event", records)
	}
}

func testIntegrationJournalPurge(t *testing.T, store *Store, runID string) {
	t.Helper()
	for sequence := int64(1); sequence <= 2; sequence++ {
		if err := store.Append(t.Context(), runlog.Record{
			SessionID: "dbintegration-journal", RunID: runID, Sequence: sequence,
			EventType: "journal.event", Source: "dbintegration", Timestamp: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("Append(journal) error = %v", err)
		}
	}
	first, err := store.PurgeRunLog(t.Context(), RunLogPurgeRequest{
		Before: time.Now().Add(time.Minute), Limit: 1, AllowReplayLoss: true,
	})
	if err != nil || first.Events != 1 {
		t.Fatalf("first PurgeRunLog() = %+v, %v", first, err)
	}
	remaining, err := store.List(t.Context(), runlog.Query{RunID: runID})
	if err != nil || len(remaining) != 1 || remaining[0].Sequence != 2 {
		t.Fatalf("List(journal) = %+v, %v", remaining, err)
	}
	second, err := store.PurgeRunLog(t.Context(), RunLogPurgeRequest{
		Before: time.Now().Add(time.Minute), Limit: 10, AllowReplayLoss: true,
	})
	if err != nil || second.Events != 1 {
		t.Fatalf("second PurgeRunLog() = %+v, %v", second, err)
	}
	var registryRows int64
	if err := store.db.Model(&runRegistryRow{}).Where("run_id = ?", runID).Count(&registryRows).Error; err != nil {
		t.Fatal(err)
	}
	if registryRows != 0 {
		t.Fatalf("journal registry rows = %d, want 0 after all events were purged", registryRows)
	}
}

func testIntegrationLifecycle(t *testing.T, store *Store, runID string) {
	t.Helper()
	record := testCheckpoint(runID)
	if err := store.Create(t.Context(), record); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	renewed := record.LeaseExpiresAt.Add(time.Minute)
	if err := store.RenewLease(t.Context(), workflow.CheckpointLease{
		RunID: record.RunID, OwnerID: record.OwnerID,
		ClaimSequence: record.ClaimSequence, ExpiresAt: renewed,
	}); err != nil {
		t.Fatalf("RenewLease() error = %v", err)
	}

	current := loadIntegrationCheckpoint(t, store, runID)
	current.Payload = []byte(`{"state":"saved"}`)
	if err := store.Save(t.Context(), current, current.Version); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	current = loadIntegrationCheckpoint(t, store, runID)
	if !current.LeaseExpiresAt.Equal(renewed) {
		t.Fatalf("Save() lease = %v, want %v", current.LeaseExpiresAt, renewed)
	}

	event := runlog.Record{
		SessionID: current.SessionID, RunID: current.RunID, Sequence: 1,
		EventType: "test.event", Source: "dbintegration", Timestamp: time.Now().UTC(),
	}
	fence := runlog.Fence{OwnerID: current.OwnerID, ClaimSequence: current.ClaimSequence}
	if err := store.AppendFenced(t.Context(), event, fence); err != nil {
		t.Fatalf("AppendFenced() error = %v", err)
	}
	if err := store.AppendFenced(t.Context(), event, fence); err != nil {
		t.Fatalf("idempotent AppendFenced() error = %v", err)
	}
	conflict := event
	conflict.Summary = "different"
	if err := store.AppendFenced(t.Context(), conflict, fence); !errors.Is(err, runlog.ErrConflict) {
		t.Fatalf("conflicting AppendFenced() error = %v, want ErrConflict", err)
	}

	current.Status = workflow.CheckpointCompleted
	current.OwnerID = ""
	current.LeaseExpiresAt = time.Time{}
	if err := store.Finish(t.Context(), current, current.Version); err != nil {
		t.Fatalf("final Finish() error = %v", err)
	}
	result, err := store.PurgeTerminalRuns(t.Context(), PurgeRequest{
		Before: time.Now().Add(time.Minute), Limit: 1,
	})
	if err != nil {
		t.Fatalf("PurgeTerminalRuns() error = %v", err)
	}
	if result.Runs != 1 || result.Checkpoints != 3 || result.Events != 1 {
		t.Fatalf("PurgeTerminalRuns() = %+v, want 1 run, 3 checkpoints, 1 event", result)
	}
	if _, err := store.Load(t.Context(), runID); !errors.Is(err, workflow.ErrCheckpointNotFound) {
		t.Fatalf("Load(purged run) error = %v, want ErrCheckpointNotFound", err)
	}
}

func testIntegrationConfirmedRunLogPurge(t *testing.T, store *Store, runID string) {
	t.Helper()
	record := testCheckpoint(runID)
	record.LeaseExpiresAt = time.Unix(1, 0)
	record.LeaseDuration = time.Minute
	if err := store.Create(t.Context(), record); err != nil {
		t.Fatalf("Create(database TTL) error = %v", err)
	}
	current := loadIntegrationCheckpoint(t, store, runID)
	if !current.LeaseExpiresAt.After(time.Now()) {
		t.Fatalf("database TTL lease expires at %v, want future", current.LeaseExpiresAt)
	}
	fence := runlog.Fence{OwnerID: current.OwnerID, ClaimSequence: current.ClaimSequence}
	for sequence := int64(1); sequence <= 3; sequence++ {
		if err := store.AppendFenced(t.Context(), runlog.Record{
			SessionID: current.SessionID, RunID: current.RunID, Sequence: sequence,
			EventType: "confirmed.event", Source: "dbintegration", Timestamp: time.Now().UTC(),
		}, fence); err != nil {
			t.Fatalf("AppendFenced(confirmed %d) error = %v", sequence, err)
		}
	}
	current.ConfirmedSequence = 2
	current.Payload = []byte(`{"state":"confirmed-two"}`)
	current.LeaseDuration = time.Minute
	if err := store.Save(t.Context(), current, current.Version); err != nil {
		t.Fatalf("Save(confirmed prefix) error = %v", err)
	}
	first, err := store.PurgeConfirmedRunLog(t.Context(), ConfirmedRunLogPurgeRequest{
		RunID: runID, Before: time.Now().Add(time.Minute), Limit: 1, AllowHistoryLoss: true,
	})
	if err != nil || first.Events != 1 || first.CompactedThroughSequence != 1 {
		t.Fatalf("first PurgeConfirmedRunLog() = %+v, %v", first, err)
	}
	second, err := store.PurgeConfirmedRunLog(t.Context(), ConfirmedRunLogPurgeRequest{
		RunID: runID, Before: time.Now().Add(time.Minute), Limit: 10, AllowHistoryLoss: true,
	})
	if err != nil || second.Events != 1 || second.CompactedThroughSequence != 2 {
		t.Fatalf("second PurgeConfirmedRunLog() = %+v, %v", second, err)
	}
	if _, err := store.List(t.Context(), runlog.Query{RunID: runID}); !errors.Is(err, runlog.ErrHistoryCompacted) {
		t.Fatalf("List(compacted prefix) error = %v, want ErrHistoryCompacted", err)
	}
	remaining, err := store.List(t.Context(), runlog.Query{RunID: runID, After: 2})
	if err != nil || len(remaining) != 1 || remaining[0].Sequence != 3 {
		t.Fatalf("List(compacted suffix) = %+v, %v", remaining, err)
	}
}

func claimIntegrationCheckpoint(t *testing.T, store *Store, current workflow.CheckpointRecord) {
	t.Helper()
	ctx := t.Context()
	start := make(chan struct{})
	results := make(chan error, 2)
	var group sync.WaitGroup
	for _, owner := range []string{"claim-owner-a", "claim-owner-b"} {
		candidate := current
		candidate.OwnerID = owner
		candidate.ClaimSequence++
		candidate.LeaseExpiresAt = time.Now().Add(time.Minute)
		group.Go(func() {
			<-start
			results <- store.Claim(ctx, candidate, current.Version)
		})
	}
	close(start)
	group.Wait()
	close(results)

	succeeded, conflicted := 0, 0
	for err := range results {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, workflow.ErrCheckpointConflict):
			conflicted++
		default:
			t.Fatalf("concurrent Claim() error = %v", err)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("concurrent Claim() = %d success, %d conflict; want 1 and 1", succeeded, conflicted)
	}
}

func testIntegrationRunIDCase(t *testing.T, store *Store, prefix string) {
	t.Helper()
	upper := testCheckpoint(prefix + "-Case")
	lower := testCheckpoint(prefix + "-case")
	if err := store.Create(t.Context(), upper); err != nil {
		t.Fatalf("Create(%q) error = %v", upper.RunID, err)
	}
	if err := store.Create(t.Context(), lower); err != nil {
		t.Fatalf("Create(%q) error = %v", lower.RunID, err)
	}
	if loaded := loadIntegrationCheckpoint(t, store, upper.RunID); loaded.RunID != upper.RunID {
		t.Fatalf("Load(%q).RunID = %q", upper.RunID, loaded.RunID)
	}
	if loaded := loadIntegrationCheckpoint(t, store, lower.RunID); loaded.RunID != lower.RunID {
		t.Fatalf("Load(%q).RunID = %q", lower.RunID, loaded.RunID)
	}
}

func loadIntegrationCheckpoint(t *testing.T, store *Store, runID string) workflow.CheckpointRecord {
	t.Helper()
	record, err := store.Load(t.Context(), runID)
	if err != nil {
		t.Fatalf("Load(%q) error = %v", runID, err)
	}
	return record
}

func clearIntegrationData(store *Store) error {
	for _, table := range []string{
		eventRetentionTable,
		eventTable,
		checkpointTable,
		runTable,
		registryTable,
	} {
		if err := store.db.Exec("DELETE FROM " + table).Error; err != nil {
			return fmt.Errorf("delete %s: %w", table, err)
		}
	}
	return nil
}
