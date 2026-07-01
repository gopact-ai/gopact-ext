// Package gitdiff scans git diffs into gopacttest evidence snapshots.
package gitdiff

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/gopact-ai/gopact/gopacttest"
)

// ScanWorktree captures unstaged working-tree changes from a git repository.
func ScanWorktree(ctx context.Context, dir string) (gopacttest.DiffSnapshot, error) {
	return scan(ctx, dir, false)
}

// ScanStaged captures staged index changes from a git repository.
func ScanStaged(ctx context.Context, dir string) (gopacttest.DiffSnapshot, error) {
	return scan(ctx, dir, true)
}

func scan(ctx context.Context, dir string, staged bool) (gopacttest.DiffSnapshot, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if dir == "" {
		dir = "."
	}

	diff, err := runGit(ctx, dir, diffArgs(staged)...)
	if err != nil {
		snapshot := snapshotFor(staged)
		snapshot.Err = err
		return snapshot, err
	}
	if strings.TrimSpace(diff) == "" {
		snapshot := snapshotFor(staged)
		snapshot.Skipped = true
		snapshot.Summary = "no diff"
		return snapshot, nil
	}

	stats, err := runGit(ctx, dir, numstatArgs(staged)...)
	if err != nil {
		snapshot := snapshotFor(staged)
		snapshot.Diff = diff
		snapshot.Err = err
		return snapshot, err
	}

	snapshot := snapshotFor(staged)
	snapshot.Diff = diff
	snapshot.Files, snapshot.Insertions, snapshot.Deletions = parseNumstat(stats)
	return snapshot, nil
}

func snapshotFor(staged bool) gopacttest.DiffSnapshot {
	if staged {
		return gopacttest.DiffSnapshot{ID: "git-staged", Ref: "git:staged", Metadata: map[string]any{"source": "git", "mode": "staged"}}
	}
	return gopacttest.DiffSnapshot{ID: "git-worktree", Ref: "git:worktree", Metadata: map[string]any{"source": "git", "mode": "worktree"}}
}

func diffArgs(staged bool) []string {
	args := []string{"diff", "--binary"}
	if staged {
		args = append(args, "--cached")
	}
	return args
}

func numstatArgs(staged bool) []string {
	args := []string{"diff", "--numstat"}
	if staged {
		args = append(args, "--cached")
	}
	return args
}

func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return "", fmt.Errorf("gitdiff: git %s: %w: %s", strings.Join(args, " "), err, msg)
		}
		return "", fmt.Errorf("gitdiff: git %s: %w", strings.Join(args, " "), err)
	}
	return string(out), nil
}

func parseNumstat(raw string) ([]string, int, int) {
	var files []string
	var insertions, deletions int
	for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		files = append(files, parts[2])
		insertions += atoiZero(parts[0])
		deletions += atoiZero(parts[1])
	}
	return files, insertions, deletions
}

func atoiZero(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
