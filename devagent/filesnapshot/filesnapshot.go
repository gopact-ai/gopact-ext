// Package filesnapshot scans files into gopacttest evidence snapshots.
package filesnapshot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/gopact-ai/gopact/gopacttest"
)

var ErrPathRequired = errors.New("filesnapshot: path is required")

// Scan captures file hash and stat metadata for verification evidence.
func Scan(ctx context.Context, path string) (gopacttest.FileSnapshot, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return gopacttest.FileSnapshot{}, ErrPathRequired
	}

	snapshot := gopacttest.FileSnapshot{
		Path:          path,
		HashAlgorithm: "sha256",
		Metadata:      map[string]any{"source": "file"},
	}
	if err := ctx.Err(); err != nil {
		snapshot.Err = err
		return snapshot, err
	}

	info, err := os.Stat(path)
	if err != nil {
		snapshot.Err = err
		return snapshot, fmt.Errorf("filesnapshot: stat %s: %w", path, err)
	}
	if info.IsDir() {
		err := fmt.Errorf("filesnapshot: %s is a directory", path)
		snapshot.Err = err
		return snapshot, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		snapshot.Err = err
		return snapshot, fmt.Errorf("filesnapshot: read %s: %w", path, err)
	}
	if err := ctx.Err(); err != nil {
		snapshot.Err = err
		return snapshot, err
	}

	sum := sha256.Sum256(data)
	snapshot.Hash = hex.EncodeToString(sum[:])
	snapshot.SizeBytes = info.Size()
	snapshot.Mode = info.Mode().String()
	snapshot.ModifiedAt = info.ModTime()
	return snapshot, nil
}
