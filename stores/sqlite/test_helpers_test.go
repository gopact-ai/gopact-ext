package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/gopact-ai/gopact/workflow"
)

type sqlExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func insertCheckpoint(ctx context.Context, executor sqlExecutor, record workflow.CheckpointRecord) error {
	metadata, err := encodeCheckpointMetadata(record)
	if err != nil {
		return err
	}
	_, err = executor.ExecContext(
		ctx,
		`INSERT INTO `+checkpointTable+` (run_id, version, record_json, payload) VALUES (?, ?, ?, ?)`,
		record.RunID,
		record.Version,
		metadata,
		record.Payload,
	)
	return err
}

func encodeCheckpointMetadata(record workflow.CheckpointRecord) ([]byte, error) {
	record.Payload = nil
	return json.Marshal(record)
}

func databaseBusy(err error) bool {
	var result interface{ Code() int }
	if !errors.As(err, &result) {
		return false
	}
	code := result.Code() & 0xff
	return code == 5 || code == 6
}
