# workspace

[![CI](https://github.com/gopact-ai/gopact-ext/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/gopact-ai/gopact-ext/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/gopact-ai/gopact-ext/devagent/workspace.svg)](https://pkg.go.dev/github.com/gopact-ai/gopact-ext/devagent/workspace)
[![License](https://img.shields.io/github/license/gopact-ai/gopact-ext)](../../LICENSE)

<!-- gopact:doc-language: en -->

Chinese documentation: [README_zh.md](README_zh.md)

`workspace` adapts a local repository root into self-bootstrap evidence. It combines controlled patch apply, git diff scanning, file snapshots, local command execution, and CI gate mapping into `selfbootstrap.Writer` and `selfbootstrap.Tester` implementations.

Install it with `go get github.com/gopact-ai/gopact-ext/devagent/workspace@v0.1.11`.

```go
ws, err := workspace.New("/path/to/repo")
if err != nil {
	return err
}

workflow, err := selfbootstrap.New(
	selfbootstrap.WithAnalyzer(analyzer),
	selfbootstrap.WithPlanner(planner),
	selfbootstrap.WithPatchPolicy(policy),
	selfbootstrap.WithWriter(ws.PlanPatchWriter("go.mod", "README.md")),
	selfbootstrap.WithTester(ws.Tester(workspace.Command{
		Gate: gopacttest.SelfBootstrapCIGateUnit,
		Args: []string{"go", "test", "-count=1", "./..."},
	})),
	selfbootstrap.WithReviewer(reviewer),
)
```

Use `Writer(paths...)` when another host component has already made the change and the workflow only needs evidence. Use `PatchWriter(patch, paths...)` when the host wants this adapter to apply a caller-provided unified diff before evidence capture. Use `PlanPatchWriter(paths...)` when the self-bootstrap planner produced a `PatchProposal`; it requires an allow `PatchDecision` from `WithPatchPolicy` before applying the patch.

The adapter validates patch paths before applying them, rejects paths outside the repository root and symlink escapes, and records repo-relative paths and command evidence. It does not decide whether a release is acceptable; release gates remain with the caller.

Run `(cd devagent/workspace && go test -count=1 ./...)` before changing behavior.
