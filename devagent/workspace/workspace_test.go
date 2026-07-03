package workspace

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact-ext/devagent/selfbootstrap"
	"github.com/gopact-ai/gopact/gopacttest"
)

func TestWriterCapturesWorktreeDiffAndRepoRelativeFileSnapshots(t *testing.T) {
	root := newGitRepo(t)
	writeFile(t, root, "README.md", "hello\n")
	runGitTest(t, root, "add", "README.md")
	runGitTest(t, root, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-m", "init")
	writeFile(t, root, "README.md", "hello\nworld\n")

	ws, err := New(root, WithMetadata(map[string]any{"suite": "unit"}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := ws.Writer("README.md").Write(context.Background(), selfbootstrap.WriteRequest{
		Request: selfbootstrap.Request{Repository: "demo"},
	})
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	if result.Diff == nil || result.Diff.ID != "git-worktree" || result.Diff.Skipped {
		t.Fatalf("diff = %+v, want captured worktree diff", result.Diff)
	}
	if len(result.Diff.Files) != 1 || result.Diff.Files[0] != "README.md" || result.Diff.Insertions != 1 {
		t.Fatalf("diff stats = %+v, want README insertion", result.Diff)
	}
	if len(result.FileSnapshots) != 1 {
		t.Fatalf("snapshots = %d, want 1", len(result.FileSnapshots))
	}
	snapshot := result.FileSnapshots[0]
	if snapshot.Path != "README.md" || snapshot.Hash == "" || snapshot.HashAlgorithm != "sha256" {
		t.Fatalf("snapshot = %+v, want repo-relative sha256 snapshot", snapshot)
	}
	if snapshot.Metadata["source"] != "workspace" || snapshot.Metadata["repository"] != "demo" ||
		snapshot.Metadata["suite"] != "unit" {
		t.Fatalf("snapshot metadata = %+v, want workspace repository metadata", snapshot.Metadata)
	}
}

func TestPatchWriterAppliesPatchAndCapturesEvidence(t *testing.T) {
	root := newGitRepo(t)
	writeFile(t, root, "hello.txt", "hello\n")
	runGitTest(t, root, "add", "hello.txt")
	runGitTest(t, root, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-m", "init")

	ws, err := New(root, WithMetadata(map[string]any{"suite": "unit"}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := ws.PatchWriter(Patch{
		ID:      "patch-1",
		Summary: "extend greeting",
		Diff: strings.Join([]string{
			"diff --git a/hello.txt b/hello.txt",
			"--- a/hello.txt",
			"+++ b/hello.txt",
			"@@ -1 +1,2 @@",
			" hello",
			"+workspace",
			"",
		}, "\n"),
		Metadata: map[string]any{"source_step": "plan"},
	}, "hello.txt").Write(context.Background(), selfbootstrap.WriteRequest{
		Request: selfbootstrap.Request{Repository: "demo"},
	})
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	content, err := os.ReadFile(filepath.Join(root, "hello.txt"))
	if err != nil {
		t.Fatalf("read patched file: %v", err)
	}
	if string(content) != "hello\nworkspace\n" {
		t.Fatalf("patched file = %q, want applied patch", content)
	}
	if result.Summary != "workspace patch applied and evidence captured" {
		t.Fatalf("summary = %q, want patch applied summary", result.Summary)
	}
	if result.Metadata["patch_id"] != "patch-1" || result.Metadata["patch_applied"] != true ||
		result.Metadata["source_step"] != "plan" {
		t.Fatalf("metadata = %+v, want patch metadata", result.Metadata)
	}
	if result.Diff == nil || len(result.Diff.Files) != 1 || result.Diff.Files[0] != "hello.txt" ||
		result.Diff.Insertions != 1 || result.Diff.Deletions != 0 {
		t.Fatalf("diff = %+v, want applied patch diff", result.Diff)
	}
	if len(result.FileSnapshots) != 1 || result.FileSnapshots[0].Path != "hello.txt" ||
		result.FileSnapshots[0].Metadata["patch_id"] != "patch-1" {
		t.Fatalf("snapshots = %+v, want patched file snapshot with patch metadata", result.FileSnapshots)
	}
}

func TestPlanPatchWriterAppliesApprovedPlanPatch(t *testing.T) {
	root := newGitRepo(t)
	writeFile(t, root, "hello.txt", "hello\n")
	runGitTest(t, root, "add", "hello.txt")
	runGitTest(t, root, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-m", "init")

	ws, err := New(root)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := ws.PlanPatchWriter("hello.txt").Write(context.Background(), selfbootstrap.WriteRequest{
		Request: selfbootstrap.Request{Repository: "demo"},
		Plan: selfbootstrap.Plan{
			Patch: &selfbootstrap.PatchProposal{
				ID:       "plan-patch-1",
				Summary:  "extend greeting",
				Diff:     helloPatchDiff(),
				Metadata: map[string]any{"source_step": "plan"},
			},
		},
		PatchDecision: &gopact.PolicyDecision{
			Action: gopact.PolicyAllow,
			Reason: "small patch",
			Metadata: map[string]any{
				"reviewer": "unit-policy",
			},
		},
	})
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	content, err := os.ReadFile(filepath.Join(root, "hello.txt"))
	if err != nil {
		t.Fatalf("read patched file: %v", err)
	}
	if string(content) != "hello\nworkspace\n" {
		t.Fatalf("patched file = %q, want approved plan patch applied", content)
	}
	if result.Metadata["patch_id"] != "plan-patch-1" ||
		result.Metadata["patch_policy_action"] != string(gopact.PolicyAllow) ||
		result.Metadata["patch_policy_reason"] != "small patch" ||
		result.Metadata["source_step"] != "plan" {
		t.Fatalf("metadata = %+v, want plan patch and policy metadata", result.Metadata)
	}
}

func TestPlanPatchWriterRequiresPolicyAllowBeforeApply(t *testing.T) {
	root := newGitRepo(t)
	writeFile(t, root, "hello.txt", "hello\n")
	runGitTest(t, root, "add", "hello.txt")
	runGitTest(t, root, "-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-m", "init")

	ws, err := New(root)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	request := selfbootstrap.WriteRequest{
		Request: selfbootstrap.Request{
			Objective:  "apply greeting patch",
			Repository: "demo",
		},
		Plan: selfbootstrap.Plan{
			Patch: &selfbootstrap.PatchProposal{
				ID:      "plan-patch-denied",
				Summary: "extend greeting",
				Files: []selfbootstrap.PatchFile{
					{Path: "hello.txt", Intent: "modify"},
				},
				Diff:     helloPatchDiff(),
				Metadata: map[string]any{"source_step": "plan"},
			},
		},
	}

	_, err = ws.PlanPatchWriter("hello.txt").Write(context.Background(), request)
	if !errors.Is(err, ErrPatchDecisionRequired) {
		t.Fatalf("Write(missing decision) error = %v, want ErrPatchDecisionRequired", err)
	}
	request.PatchDecision = &gopact.PolicyDecision{Action: gopact.PolicyDeny, Reason: "blocked"}
	result, err := ws.PlanPatchWriter("hello.txt").Write(context.Background(), request)
	if !errors.Is(err, gopact.ErrPolicyDenied) {
		t.Fatalf("Write(denied decision) error = %v, want gopact.ErrPolicyDenied", err)
	}
	if result.Summary != "workspace plan patch policy blocked" {
		t.Fatalf("denied summary = %q, want policy blocked", result.Summary)
	}
	var denied *gopact.PolicyDeniedError
	if !errors.As(err, &denied) {
		t.Fatalf("Write(denied decision) error = %T, want *gopact.PolicyDeniedError", err)
	}
	if denied.Request.Boundary != gopact.PolicyBoundarySandbox ||
		denied.Request.Action != gopact.PolicyActionWrite ||
		denied.Request.Metadata["stage"] != "write" ||
		denied.Request.Metadata["objective"] != "apply greeting patch" ||
		denied.Request.Metadata["repository"] != "demo" {
		t.Fatalf("denied policy request = %+v, want self-bootstrap patch policy context", denied.Request)
	}
	input, ok := denied.Request.Input.(selfbootstrap.PatchPolicyInput)
	if !ok {
		t.Fatalf("denied policy input type = %T, want selfbootstrap.PatchPolicyInput", denied.Request.Input)
	}
	if input.ID != "plan-patch-denied" ||
		input.Summary != "extend greeting" ||
		!input.HasDiff ||
		input.DiffBytes != len(helloPatchDiff()) ||
		len(input.Files) != 1 ||
		input.Files[0].Path != "hello.txt" ||
		input.Metadata["source_step"] != "plan" {
		t.Fatalf("denied policy input = %+v, want sanitized plan patch summary", input)
	}

	content, err := os.ReadFile(filepath.Join(root, "hello.txt"))
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(content) != "hello\n" {
		t.Fatalf("file = %q, want unchanged without allow decision", content)
	}
}

func TestPatchWriterRejectsUnsafePatchPathsBeforeApply(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("secret\n"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "outside-link")); err != nil {
		t.Fatalf("symlink outside file: %v", err)
	}

	ws, err := New(root)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = ws.PatchWriter(Patch{
		ID: "patch-symlink",
		Diff: strings.Join([]string{
			"diff --git a/outside-link b/outside-link",
			"--- a/outside-link",
			"+++ b/outside-link",
			"@@ -1 +1 @@",
			"-secret",
			"+leaked",
			"",
		}, "\n"),
	}).Write(context.Background(), selfbootstrap.WriteRequest{})
	if !errors.Is(err, ErrPathOutsideRoot) {
		t.Fatalf("Write(symlink patch) error = %v, want ErrPathOutsideRoot", err)
	}
	content, err := os.ReadFile(outside)
	if err != nil {
		t.Fatalf("read outside file: %v", err)
	}
	if string(content) != "secret\n" {
		t.Fatalf("outside file = %q, want unchanged", content)
	}

	_, err = ws.PatchWriter(Patch{
		ID: "patch-parent",
		Diff: strings.Join([]string{
			"diff --git a/../outside.txt b/../outside.txt",
			"--- a/../outside.txt",
			"+++ b/../outside.txt",
			"@@ -1 +1 @@",
			"-secret",
			"+leaked",
			"",
		}, "\n"),
	}).Write(context.Background(), selfbootstrap.WriteRequest{})
	if !errors.Is(err, ErrPathOutsideRoot) {
		t.Fatalf("Write(parent path patch) error = %v, want ErrPathOutsideRoot", err)
	}
}

func TestTesterRunsCommandsAndMapsCIGates(t *testing.T) {
	root := t.TempDir()
	ws, err := New(root)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := ws.Tester(Command{
		Gate: gopacttest.SelfBootstrapCIGateUnit,
		Args: []string{"sh", "-c", "printf ok"},
	}).Test(context.Background(), selfbootstrap.TestRequest{})
	if err != nil {
		t.Fatalf("Test() error = %v", err)
	}

	if len(result.Commands) != 1 || len(result.Gates) != 1 {
		t.Fatalf("result commands/gates = %d/%d, want 1/1", len(result.Commands), len(result.Gates))
	}
	command := result.Commands[0]
	if command.ExitCode != 0 || command.Stdout != "ok" || command.Stderr != "" {
		t.Fatalf("command = %+v, want successful stdout capture", command)
	}
	if command.Dir != "." {
		t.Fatalf("command dir = %q, want repo-relative root", command.Dir)
	}
	if result.Gates[0].Gate != gopacttest.SelfBootstrapCIGateUnit {
		t.Fatalf("gate = %+v, want unit gate", result.Gates[0])
	}
	if len(result.RequiredGates) != 1 || result.RequiredGates[0] != gopacttest.SelfBootstrapCIGateUnit {
		t.Fatalf("required gates = %+v, want unit gate", result.RequiredGates)
	}

	recorder := gopact.NewVerificationRecorder()
	if err := gopacttest.RecordCommandCheck(recorder, command); err != nil {
		t.Fatalf("RecordCommandCheck() error = %v", err)
	}
	if err := gopacttest.RecordCIGateSuiteCheck(recorder, gopacttest.CIGateSuite{
		RequiredGates: result.RequiredGates,
		Results:       result.Gates,
	}); err != nil {
		t.Fatalf("RecordCIGateSuiteCheck() error = %v", err)
	}
}

func TestTesterRecordsFailedCommandWithoutReturningRuntimeError(t *testing.T) {
	ws, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := ws.Tester(Command{
		Gate: gopacttest.SelfBootstrapCIGateUnit,
		Args: []string{"sh", "-c", "printf nope >&2; exit 7"},
	}).Test(context.Background(), selfbootstrap.TestRequest{})
	if err != nil {
		t.Fatalf("Test() error = %v, want nil so selfbootstrap records verification failure", err)
	}

	command := result.Commands[0]
	if command.ExitCode != 7 || !strings.Contains(command.Stderr, "nope") || command.Err == nil {
		t.Fatalf("command = %+v, want failed command evidence", command)
	}
	recorder := gopact.NewVerificationRecorder()
	if err := gopacttest.RecordCommandCheck(recorder, command); !errors.Is(err, gopacttest.ErrCommandFailed) {
		t.Fatalf("RecordCommandCheck() error = %v, want ErrCommandFailed", err)
	}
}

func TestTesterKeepsCommandIDsDistinctWithinOneGate(t *testing.T) {
	ws, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := ws.Tester(
		Command{Gate: gopacttest.SelfBootstrapCIGateUnit, Args: []string{"sh", "-c", "printf one"}},
		Command{Gate: gopacttest.SelfBootstrapCIGateUnit, Args: []string{"sh", "-c", "printf two"}},
	).Test(context.Background(), selfbootstrap.TestRequest{})
	if err != nil {
		t.Fatalf("Test() error = %v", err)
	}

	if len(result.Commands) != 2 {
		t.Fatalf("commands = %d, want 2", len(result.Commands))
	}
	if result.Commands[0].ID == result.Commands[1].ID {
		t.Fatalf("command IDs = %q and %q, want distinct IDs", result.Commands[0].ID, result.Commands[1].ID)
	}
	if len(result.RequiredGates) != 1 {
		t.Fatalf("required gates = %+v, want de-duplicated gate", result.RequiredGates)
	}
}

func TestTesterTruncatesLargeCommandOutput(t *testing.T) {
	ws, err := New(t.TempDir(), WithCommandOutputLimit(4))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result, err := ws.Tester(Command{
		Gate: gopacttest.SelfBootstrapCIGateUnit,
		Args: []string{"sh", "-c", "printf 0123456789; printf abcdef >&2"},
	}).Test(context.Background(), selfbootstrap.TestRequest{})
	if err != nil {
		t.Fatalf("Test() error = %v", err)
	}

	command := result.Commands[0]
	if command.Stdout != "0123" || command.Stderr != "abcd" {
		t.Fatalf("command output = stdout %q stderr %q, want truncated output", command.Stdout, command.Stderr)
	}
	if command.Metadata["stdout_truncated"] != true || command.Metadata["stderr_truncated"] != true ||
		command.Metadata["output_limit_bytes"] != 4 {
		t.Fatalf("command metadata = %+v, want truncation metadata", command.Metadata)
	}
}

func TestWorkspaceValidatesRootFilesAndCommands(t *testing.T) {
	if _, err := New(" "); !errors.Is(err, ErrRootRequired) {
		t.Fatalf("New(empty) error = %v, want ErrRootRequired", err)
	}

	ws, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := ws.Writer("../outside.txt").Write(context.Background(), selfbootstrap.WriteRequest{}); !errors.Is(err, ErrPathOutsideRoot) {
		t.Fatalf("Write(outside) error = %v, want ErrPathOutsideRoot", err)
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	link := filepath.Join(ws.root, "outside-link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatalf("symlink outside file: %v", err)
	}
	if _, err := ws.Writer("outside-link").Write(context.Background(), selfbootstrap.WriteRequest{}); !errors.Is(err, ErrPathOutsideRoot) {
		t.Fatalf("Write(outside symlink) error = %v, want ErrPathOutsideRoot", err)
	}
	if _, err := ws.Tester(Command{Gate: gopacttest.SelfBootstrapCIGateUnit}).Test(context.Background(), selfbootstrap.TestRequest{}); !errors.Is(err, ErrCommandRequired) {
		t.Fatalf("Test(empty command) error = %v, want ErrCommandRequired", err)
	}
	if _, err := ws.Tester(Command{Args: []string{"sh", "-c", "true"}}).Test(context.Background(), selfbootstrap.TestRequest{}); !errors.Is(err, ErrGateRequired) {
		t.Fatalf("Test(empty gate) error = %v, want ErrGateRequired", err)
	}
}

func newGitRepo(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", strings.ReplaceAll(t.Name(), "/", "-")+"-*")
	if err != nil {
		t.Fatalf("create temp git repo: %v", err)
	}
	t.Cleanup(func() {
		var err error
		for range 20 {
			err = os.RemoveAll(dir)
			if err == nil {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("remove temp git repo: %v", err)
	})
	runGitTest(t, dir, "init")
	return dir
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func helloPatchDiff() string {
	return strings.Join([]string{
		"diff --git a/hello.txt b/hello.txt",
		"--- a/hello.txt",
		"+++ b/hello.txt",
		"@@ -1 +1,2 @@",
		" hello",
		"+workspace",
		"",
	}, "\n")
}

func runGitTest(t *testing.T, dir string, args ...string) {
	t.Helper()
	gitArgs := append([]string{"-c", "gc.auto=0", "-c", "maintenance.auto=false"}, args...)
	cmd := exec.Command("git", gitArgs...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}
