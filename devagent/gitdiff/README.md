# gitdiff

Git diff scanner for Dev Agent evidence collection.

## Install

```bash
go get github.com/gopact-ai/gopact-ext/devagent/gitdiff@v0.1.10
```

## Usage

```go
snapshot, err := gitdiff.ScanWorktree(ctx, ".")
if err != nil {
	return err
}
if snapshot.Skipped {
	return nil
}
return gopacttest.RecordDiffCheck(recorder, snapshot)
```

`ScanWorktree` reads unstaged changes. `ScanStaged` reads staged changes. Both return `gopacttest.DiffSnapshot` and leave verification, release decisions, and patch application to the caller.
