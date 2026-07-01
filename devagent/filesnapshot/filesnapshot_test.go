package filesnapshot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact/gopacttest"
)

func TestScanCapturesFileSnapshot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "go.mod")
	content := []byte("module example.test\n")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	snapshot, err := Scan(context.Background(), path)
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if snapshot.Path != path || snapshot.HashAlgorithm != "sha256" || snapshot.SizeBytes != int64(len(content)) {
		t.Fatalf("snapshot = %+v, want path/hash algorithm/size", snapshot)
	}
	sum := sha256.Sum256(content)
	if snapshot.Hash != hex.EncodeToString(sum[:]) {
		t.Fatalf("hash = %q, want sha256", snapshot.Hash)
	}
	if snapshot.Mode == "" || snapshot.ModifiedAt.IsZero() {
		t.Fatalf("snapshot = %+v, want mode and modified time", snapshot)
	}

	recorder := gopact.NewVerificationRecorder()
	if err := gopacttest.RecordFileSnapshotCheck(recorder, snapshot); err != nil {
		t.Fatalf("RecordFileSnapshotCheck() error = %v", err)
	}
	if got := recorder.Checks()[0].Evidence[0].Metadata["source"]; got != "file" {
		t.Fatalf("metadata source = %v, want file", got)
	}
}

func TestScanReturnsSnapshotOnReadError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.txt")

	snapshot, err := Scan(context.Background(), path)
	if err == nil {
		t.Fatal("Scan(missing) error = nil, want error")
	}
	if snapshot.Path != path || snapshot.Err == nil {
		t.Fatalf("snapshot = %+v, want failed file snapshot", snapshot)
	}
}

func TestScanRejectsEmptyPath(t *testing.T) {
	if _, err := Scan(context.Background(), " "); !errors.Is(err, ErrPathRequired) {
		t.Fatalf("Scan(empty) error = %v, want ErrPathRequired", err)
	}
}
