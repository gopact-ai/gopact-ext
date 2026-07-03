// Package workspace adapts a local repository workspace into self-bootstrap evidence.
package workspace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/gopact-ai/gopact-ext/devagent/filesnapshot"
	"github.com/gopact-ai/gopact-ext/devagent/gitdiff"
	"github.com/gopact-ai/gopact-ext/devagent/selfbootstrap"
	"github.com/gopact-ai/gopact/gopacttest"
)

var (
	ErrRootRequired        = errors.New("workspace: root is required")
	ErrCommandRequired     = errors.New("workspace: command is required")
	ErrCommandLimitInvalid = errors.New("workspace: command output limit is invalid")
	ErrGateRequired        = errors.New("workspace: gate is required")
	ErrPathOutsideRoot     = errors.New("workspace: path is outside root")
	ErrPatchRequired       = errors.New("workspace: patch diff is required")
	ErrPatchApplyFailed    = errors.New("workspace: patch apply failed")
)

const defaultCommandOutputLimit = 64 * 1024

// Command describes one local command that should be executed and recorded as CI gate evidence.
type Command struct {
	Gate     string
	Name     string
	Args     []string
	Dir      string
	Metadata map[string]any
}

// Patch describes one caller-provided unified diff to apply inside the workspace.
type Patch struct {
	ID       string
	Summary  string
	Diff     string
	Metadata map[string]any
}

// Workspace is a local repository root used by development-agent host adapters.
type Workspace struct {
	root               string
	metadata           map[string]any
	commandOutputLimit int
}

// Option configures a local workspace adapter.
type Option func(*Workspace) error

// WithMetadata adds metadata to every evidence item produced by the workspace.
func WithMetadata(metadata map[string]any) Option {
	return func(w *Workspace) error {
		w.metadata = mergeMetadata(w.metadata, metadata)
		return nil
	}
}

// WithCommandOutputLimit caps captured stdout and stderr bytes for each command.
func WithCommandOutputLimit(limit int) Option {
	return func(w *Workspace) error {
		if limit <= 0 {
			return ErrCommandLimitInvalid
		}
		w.commandOutputLimit = limit
		return nil
	}
}

// New creates a local repository workspace adapter.
func New(root string, opts ...Option) (*Workspace, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, ErrRootRequired
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("workspace: resolve root: %w", err)
	}
	info, err := os.Stat(absRoot)
	if err != nil {
		return nil, fmt.Errorf("workspace: stat root: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("workspace: root is not a directory: %s", root)
	}
	if realRoot, err := filepath.EvalSymlinks(absRoot); err == nil {
		absRoot = realRoot
	}

	w := &Workspace{
		root:               absRoot,
		metadata:           map[string]any{"source": "workspace"},
		commandOutputLimit: defaultCommandOutputLimit,
	}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if err := opt(w); err != nil {
			return nil, err
		}
	}
	return w, nil
}

// Writer returns a self-bootstrap writer that captures worktree diff and file snapshots.
func (w *Workspace) Writer(paths ...string) selfbootstrap.Writer {
	return selfbootstrap.WriterFunc(func(ctx context.Context, request selfbootstrap.WriteRequest) (selfbootstrap.WriteResult, error) {
		return w.CaptureWrite(ctx, request, paths...)
	})
}

// PatchWriter returns a self-bootstrap writer that applies a caller-provided patch and then captures evidence.
func (w *Workspace) PatchWriter(patch Patch, paths ...string) selfbootstrap.Writer {
	return selfbootstrap.WriterFunc(func(ctx context.Context, request selfbootstrap.WriteRequest) (selfbootstrap.WriteResult, error) {
		return w.ApplyPatch(ctx, request, patch, paths...)
	})
}

// Tester returns a self-bootstrap tester that executes commands and maps them to CI gates.
func (w *Workspace) Tester(commands ...Command) selfbootstrap.Tester {
	return selfbootstrap.TesterFunc(func(ctx context.Context, _ selfbootstrap.TestRequest) (selfbootstrap.TestResult, error) {
		return w.RunTests(ctx, commands...)
	})
}

// CaptureWrite captures git worktree diff and repo-relative file snapshots.
func (w *Workspace) CaptureWrite(ctx context.Context, request selfbootstrap.WriteRequest, paths ...string) (selfbootstrap.WriteResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	result := selfbootstrap.WriteResult{
		Summary:  "workspace write evidence captured",
		Metadata: w.requestMetadata(request.Request),
	}
	diff, err := gitdiff.ScanWorktree(ctx, w.root)
	diff.Metadata = mergeMetadata(diff.Metadata, result.Metadata)
	result.Diff = &diff
	if err != nil {
		result.Summary = "workspace diff scan failed"
	}

	for _, path := range paths {
		rel, absPath, err := w.resolvePath(path)
		if err != nil {
			return result, err
		}
		snapshot, scanErr := filesnapshot.Scan(ctx, absPath)
		snapshot.Path = rel
		snapshot.Metadata = mergeMetadata(snapshot.Metadata, result.Metadata)
		snapshot.Metadata["source"] = "workspace"
		result.FileSnapshots = append(result.FileSnapshots, snapshot)
		if scanErr != nil && result.Summary == "workspace write evidence captured" {
			result.Summary = "workspace file snapshot failed"
		}
	}
	return result, nil
}

// ApplyPatch applies a caller-provided unified diff inside the workspace and captures the resulting write evidence.
func (w *Workspace) ApplyPatch(ctx context.Context, request selfbootstrap.WriteRequest, patch Patch, paths ...string) (selfbootstrap.WriteResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	metadata := w.patchMetadata(request.Request, patch, false)
	result := selfbootstrap.WriteResult{
		Summary:  "workspace patch apply failed",
		Metadata: metadata,
	}
	if strings.TrimSpace(patch.Diff) == "" {
		return result, ErrPatchRequired
	}
	patchPaths, err := w.patchPaths(ctx, patch.Diff)
	if err != nil {
		return result, err
	}
	for _, path := range patchPaths {
		if _, err := w.validatePatchPath(path); err != nil {
			return result, err
		}
	}
	if err := w.checkPatch(ctx, patch.Diff); err != nil {
		return result, err
	}
	if err := w.applyPatch(ctx, patch.Diff); err != nil {
		return result, err
	}

	if len(paths) == 0 {
		paths = patchPaths
	}
	result, err = w.CaptureWrite(ctx, request, paths...)
	result.Summary = "workspace patch applied and evidence captured"
	result.Metadata = w.patchMetadata(request.Request, patch, true)
	if result.Diff != nil {
		result.Diff.Metadata = mergeMetadata(result.Diff.Metadata, result.Metadata)
	}
	for i := range result.FileSnapshots {
		result.FileSnapshots[i].Metadata = mergeMetadata(result.FileSnapshots[i].Metadata, result.Metadata)
	}
	return result, err
}

// RunTests executes local commands and records their observed results without treating non-zero exits as runtime errors.
func (w *Workspace) RunTests(ctx context.Context, commands ...Command) (selfbootstrap.TestResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	result := selfbootstrap.TestResult{
		Summary:  "workspace test evidence captured",
		Metadata: copyMetadata(w.metadata),
	}
	for _, command := range commands {
		if len(command.Args) == 0 {
			return result, ErrCommandRequired
		}
		gate := strings.TrimSpace(command.Gate)
		if gate == "" {
			return result, ErrGateRequired
		}
		dir, displayDir, err := w.commandDir(command.Dir)
		if err != nil {
			return result, err
		}
		observed := runCommand(ctx, dir, displayDir, w.commandOutputLimit, command)
		observed.Metadata = mergeMetadata(observed.Metadata, w.metadata)
		result.Commands = append(result.Commands, observed)
		result.Gates = append(result.Gates, gopacttest.CIGateResult{
			Gate:     gate,
			Result:   observed,
			Metadata: mergeMetadata(command.Metadata, w.metadata),
		})
		if !contains(result.RequiredGates, gate) {
			result.RequiredGates = append(result.RequiredGates, gate)
		}
	}
	return result, nil
}

func (w *Workspace) patchMetadata(request selfbootstrap.Request, patch Patch, applied bool) map[string]any {
	metadata := mergeMetadata(w.requestMetadata(request), patch.Metadata)
	metadata["patch_applied"] = applied
	if patch.ID != "" {
		metadata["patch_id"] = patch.ID
	}
	if patch.Summary != "" {
		metadata["patch_summary"] = patch.Summary
	}
	metadata["source"] = "workspace"
	return metadata
}

func (w *Workspace) requestMetadata(request selfbootstrap.Request) map[string]any {
	metadata := mergeMetadata(w.metadata, request.Metadata)
	if request.Repository != "" {
		metadata["repository"] = request.Repository
	}
	return metadata
}

func (w *Workspace) resolvePath(path string) (string, string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", "", fmt.Errorf("%w: empty path", ErrPathOutsideRoot)
	}
	clean := filepath.Clean(filepath.FromSlash(path))
	if filepath.IsAbs(clean) {
		return "", "", fmt.Errorf("%w: %s", ErrPathOutsideRoot, path)
	}
	absPath := filepath.Join(w.root, clean)
	if !isWithinRoot(w.root, absPath) {
		return "", "", fmt.Errorf("%w: %s", ErrPathOutsideRoot, path)
	}
	if realPath, err := filepath.EvalSymlinks(absPath); err == nil && !isWithinRoot(w.root, realPath) {
		return "", "", fmt.Errorf("%w: %s", ErrPathOutsideRoot, path)
	}
	return filepath.ToSlash(clean), absPath, nil
}

func (w *Workspace) validatePatchPath(path string) (string, error) {
	if path == "" || path == "/dev/null" {
		return "", nil
	}
	clean := filepath.Clean(filepath.FromSlash(path))
	if filepath.IsAbs(clean) {
		return "", fmt.Errorf("%w: %s", ErrPathOutsideRoot, path)
	}
	absPath := filepath.Join(w.root, clean)
	if !isWithinRoot(w.root, absPath) {
		return "", fmt.Errorf("%w: %s", ErrPathOutsideRoot, path)
	}
	if realPath, err := filepath.EvalSymlinks(absPath); err == nil && !isWithinRoot(w.root, realPath) {
		return "", fmt.Errorf("%w: %s", ErrPathOutsideRoot, path)
	}
	parent := filepath.Dir(absPath)
	for {
		if realParent, err := filepath.EvalSymlinks(parent); err == nil {
			if !isWithinRoot(w.root, realParent) {
				return "", fmt.Errorf("%w: %s", ErrPathOutsideRoot, path)
			}
			break
		}
		next := filepath.Dir(parent)
		if next == parent || !isWithinRoot(w.root, next) {
			break
		}
		parent = next
	}
	return filepath.ToSlash(clean), nil
}

func (w *Workspace) commandDir(dir string) (string, string, error) {
	if strings.TrimSpace(dir) == "" {
		return w.root, ".", nil
	}
	rel, abs, err := w.resolvePath(dir)
	if err != nil {
		return "", "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", "", fmt.Errorf("workspace: stat command dir: %w", err)
	}
	if !info.IsDir() {
		return "", "", fmt.Errorf("workspace: command dir is not a directory: %s", rel)
	}
	return abs, rel, nil
}

func (w *Workspace) patchPaths(ctx context.Context, diff string) ([]string, error) {
	out, err := w.gitPatchOutput(ctx, []string{"apply", "--numstat", "-z", "--"}, diff)
	if err != nil {
		return nil, err
	}
	var paths []string
	seen := map[string]bool{}
	for _, record := range strings.Split(out, "\x00") {
		if record == "" {
			continue
		}
		parts := strings.SplitN(record, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		path := parts[2]
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		paths = append(paths, path)
	}
	return paths, nil
}

func (w *Workspace) checkPatch(ctx context.Context, diff string) error {
	_, err := w.gitPatchOutput(ctx, []string{"apply", "--check", "--"}, diff)
	return err
}

func (w *Workspace) applyPatch(ctx context.Context, diff string) error {
	_, err := w.gitPatchOutput(ctx, []string{"apply", "--whitespace=nowarn", "--"}, diff)
	return err
}

func (w *Workspace) gitPatchOutput(ctx context.Context, args []string, diff string) (string, error) {
	gitArgs := append([]string{"-c", "gc.auto=0", "-c", "maintenance.auto=false"}, args...)
	cmd := exec.CommandContext(ctx, "git", gitArgs...)
	cmd.Dir = w.root
	cmd.Stdin = strings.NewReader(diff)
	stdout := &boundedBuffer{limit: w.commandOutputLimit}
	stderr := &boundedBuffer{limit: w.commandOutputLimit}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return stdout.String(), fmt.Errorf("%w: %s", ErrPatchApplyFailed, message)
	}
	return stdout.String(), nil
}

func runCommand(ctx context.Context, dir, displayDir string, outputLimit int, command Command) gopacttest.CommandResult {
	start := time.Now()
	cmd := exec.CommandContext(ctx, command.Args[0], command.Args[1:]...)
	cmd.Dir = dir
	stdout := &boundedBuffer{limit: outputLimit}
	stderr := &boundedBuffer{limit: outputLimit}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()

	exitCode := 0
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	} else if err != nil {
		exitCode = -1
	}
	name := command.Name
	if name == "" {
		name = command.Gate
	}
	metadata := copyMetadata(command.Metadata)
	if stdout.truncated {
		metadata["stdout_truncated"] = true
	}
	if stderr.truncated {
		metadata["stderr_truncated"] = true
	}
	if stdout.truncated || stderr.truncated {
		metadata["output_limit_bytes"] = outputLimit
	}
	return gopacttest.CommandResult{
		ID:       commandID(command.Args),
		Name:     name,
		Command:  append([]string(nil), command.Args...),
		Dir:      displayDir,
		ExitCode: exitCode,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Err:      err,
		Duration: time.Since(start),
		Metadata: metadata,
	}
}

type boundedBuffer struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if b.limit <= 0 {
		return len(p), nil
	}
	remaining := b.limit - b.buf.Len()
	if remaining <= 0 {
		b.truncated = b.truncated || len(p) > 0
		return len(p), nil
	}
	if len(p) > remaining {
		b.truncated = true
		_, _ = b.buf.Write(p[:remaining])
		return len(p), nil
	}
	_, _ = b.buf.Write(p)
	return len(p), nil
}

func (b *boundedBuffer) String() string {
	return b.buf.String()
}

func commandID(args []string) string {
	sum := sha256.Sum256([]byte(strings.Join(args, "\x00")))
	return "command:" + hex.EncodeToString(sum[:])[:16]
}

func isWithinRoot(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != "..")
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func mergeMetadata(base, extra map[string]any) map[string]any {
	out := copyMetadata(base)
	for key, value := range extra {
		out[key] = value
	}
	return out
}

func copyMetadata(in map[string]any) map[string]any {
	if len(in) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
