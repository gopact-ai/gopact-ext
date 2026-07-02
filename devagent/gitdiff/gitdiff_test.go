package gitdiff

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact/gopacttest"
)

func TestScanWorktreeCapturesDiffSnapshot(t *testing.T) {
	dir := newGitRepo(t)
	writeFile(t, dir, "README.md", "hello\n")
	runGitTest(t, dir, "add", "README.md")
	runGitTest(t, dir, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-m", "init")
	writeFile(t, dir, "README.md", "hello\nworld\n")

	snapshot, err := ScanWorktree(context.Background(), dir)
	if err != nil {
		t.Fatalf("ScanWorktree() error = %v", err)
	}
	if snapshot.ID != "git-worktree" || snapshot.Ref != "git:worktree" || snapshot.Skipped {
		t.Fatalf("snapshot = %+v, want captured worktree diff", snapshot)
	}
	if !strings.Contains(snapshot.Diff, "diff --git a/README.md b/README.md") {
		t.Fatalf("diff = %q, want README diff", snapshot.Diff)
	}
	if len(snapshot.Files) != 1 || snapshot.Files[0] != "README.md" || snapshot.Insertions != 1 || snapshot.Deletions != 0 {
		t.Fatalf("stats = files:%v +%d -%d, want README +1 -0", snapshot.Files, snapshot.Insertions, snapshot.Deletions)
	}

	recorder := gopact.NewVerificationRecorder()
	if err := gopacttest.RecordDiffCheck(recorder, snapshot); err != nil {
		t.Fatalf("RecordDiffCheck() error = %v", err)
	}
	if got := recorder.Checks()[0].Metadata["mode"]; got != "worktree" {
		t.Fatalf("metadata mode = %v, want worktree", got)
	}
}

func TestScanStagedCapturesIndexDiff(t *testing.T) {
	dir := newGitRepo(t)
	writeFile(t, dir, "main.go", "package main\n")
	runGitTest(t, dir, "add", "main.go")

	snapshot, err := ScanStaged(context.Background(), dir)
	if err != nil {
		t.Fatalf("ScanStaged() error = %v", err)
	}
	if snapshot.ID != "git-staged" || len(snapshot.Files) != 1 || snapshot.Files[0] != "main.go" {
		t.Fatalf("snapshot = %+v, want staged main.go diff", snapshot)
	}
}

func TestScanWorktreeSkipsCleanRepo(t *testing.T) {
	snapshot, err := ScanWorktree(context.Background(), newGitRepo(t))
	if err != nil {
		t.Fatalf("ScanWorktree() error = %v", err)
	}
	if !snapshot.Skipped || snapshot.Summary != "no diff" {
		t.Fatalf("snapshot = %+v, want skipped clean diff", snapshot)
	}
}

func TestScanWorktreeReturnsSnapshotOnGitError(t *testing.T) {
	snapshot, err := ScanWorktree(context.Background(), t.TempDir())
	if err == nil {
		t.Fatal("ScanWorktree(non-git-dir) error = nil, want error")
	}
	if snapshot.Err == nil || snapshot.ID != "git-worktree" {
		t.Fatalf("snapshot = %+v, want failed worktree snapshot", snapshot)
	}
}

func newGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGitTest(t, dir, "init")
	return dir
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func runGitTest(t *testing.T, dir string, args ...string) {
	t.Helper()
	// ponytail: disable background git maintenance; TempDir cleanup can race it under -race.
	gitArgs := append([]string{"-c", "gc.auto=0", "-c", "maintenance.auto=false"}, args...)
	cmd := exec.Command("git", gitArgs...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}
