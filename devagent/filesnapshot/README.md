# filesnapshot

File snapshot scanner for Dev Agent evidence collection.

## Install

```bash
go get github.com/gopact-ai/gopact-ext/devagent/filesnapshot@v0.1.11
```

## Usage

```go
snapshot, err := filesnapshot.Scan(ctx, "go.mod")
if err != nil {
	return err
}
return gopacttest.RecordFileSnapshotCheck(recorder, snapshot)
```

`Scan` records a SHA-256 hash, size, mode, and modified time. Verification and release decisions stay with the caller.
