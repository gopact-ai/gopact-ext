// Package mariadb opens a dbstore Store backed by MariaDB.
package mariadb

import (
	"context"
	"errors"
	"fmt"

	"github.com/gopact-ai/gopact-ext/stores/dbstore"
	gormmysql "gorm.io/driver/mysql"
)

// Store is the MariaDB facade over the shared relational dbstore.
type Store struct {
	*dbstore.Store
}

// PurgeRequest selects terminal workflow runs for deletion.
type PurgeRequest = dbstore.PurgeRequest

// PurgeResult reports terminal workflow history deletion.
type PurgeResult = dbstore.PurgeResult

// RunLogPurgeRequest selects standalone journal events for deletion.
type RunLogPurgeRequest = dbstore.RunLogPurgeRequest

// RunLogPurgeResult reports standalone journal events deleted.
type RunLogPurgeResult = dbstore.RunLogPurgeResult

// ConfirmedRunLogPurgeRequest selects an active Workflow's confirmed RunLog prefix.
type ConfirmedRunLogPurgeRequest = dbstore.ConfirmedRunLogPurgeRequest

// ConfirmedRunLogPurgeResult reports the compacted Workflow RunLog prefix.
type ConfirmedRunLogPurgeResult = dbstore.ConfirmedRunLogPurgeResult

// Migrate applies or repairs the Store schema. Stop and drain every old writer
// before upgrading an existing database; the advisory lock only serializes migrators.
func Migrate(dsn string) error {
	return MigrateContext(context.Background(), dsn)
}

// MigrateContext applies or repairs the Store schema using ctx.
func MigrateContext(ctx context.Context, dsn string) error {
	if dsn == "" {
		return errors.New("mariadb: dsn is required")
	}
	if err := dbstore.MigrateContext(ctx, gormmysql.Open(dsn)); err != nil {
		return fmt.Errorf("mariadb: migrate: %w", err)
	}
	return nil
}

// Open connects to a MariaDB database whose Store schema has already been migrated.
func Open(dsn string) (*Store, error) {
	return OpenContext(context.Background(), dsn)
}

// OpenContext connects to a migrated MariaDB database using ctx.
func OpenContext(ctx context.Context, dsn string) (*Store, error) {
	if dsn == "" {
		return nil, errors.New("mariadb: dsn is required")
	}
	store, err := dbstore.ConnectContext(ctx, gormmysql.Open(dsn))
	if err != nil {
		return nil, fmt.Errorf("mariadb: open: %w", err)
	}
	return &Store{Store: store}, nil
}
