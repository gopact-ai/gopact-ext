package postgres

import (
	"context"
	"errors"
	"testing"
)

var _ interface{ Close() error } = (*Store)(nil)

func TestOpen_EmptyDSN(t *testing.T) {
	t.Parallel()

	store, err := Open("")
	if store != nil {
		t.Fatal("Open(\"\") returned a store")
	}
	if err == nil || err.Error() != "postgres: dsn is required" {
		t.Fatalf("Open(\"\") error = %v, want %q", err, "postgres: dsn is required")
	}
}

func TestMigrate_EmptyDSN(t *testing.T) {
	if err := Migrate(""); err == nil || err.Error() != "postgres: dsn is required" {
		t.Fatalf("Migrate(\"\") error = %v", err)
	}
}

func TestContextEntryPointsRejectCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if store, err := OpenContext(ctx, "unused"); store != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("OpenContext(canceled) = %v, %v", store, err)
	}
	if err := MigrateContext(ctx, "unused"); !errors.Is(err, context.Canceled) {
		t.Fatalf("MigrateContext(canceled) error = %v", err)
	}
}
