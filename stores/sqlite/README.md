# 💾 SQLite Workflow Store

`stores/sqlite` is a single-file durable backend built with `database/sql` and the CGO-free `modernc.org/sqlite` driver.

## Use cases

- Persist Workflow checkpoints and execution history in local or single-service deployments.
- Exercise durable resume and run-log behavior in tests without an external database.

The store directly implements `workflow.Checkpointer`, `workflow.CheckpointHistory`, `workflow.CheckpointController`, and `runlog.Log`.

## Example

```go
store, err := sqlite.Open("gopact.db")
if err != nil {
	return err
}
defer store.Close()

wf := workflow.New[Input, Output](
	"example",
	workflow.WithCheckpointer(store),
	workflow.WithJournal(store),
	workflow.WithCheckpointLease(3*time.Minute, time.Minute),
)
```

The complete runnable version is in [`example_test.go`](./example_test.go).

## Advantages

The same `*sqlite.Store` is passed directly to each consumer-owned interface. Other persistent backends can implement those same interfaces without a repository or service abstraction. Configured persistence is authoritative: a checkpoint, journal, or lease-renewal failure stops the invocation.

The store solves durable execution persistence. It does not provide semantic Agent Memory or Session state; those remain separate domain capabilities.

## Trade-offs

SQLite is a single-node store with bounded write concurrency. Atomic lease renewal coordinates Workflow ownership between processes that safely share the same database file; it is not a multi-host distributed coordinator.

The store serializes writes through one SQLite connection and keeps logical checkpoint versions append-only. A heartbeat updates only the current version's lease metadata in place and does not append a history version. When opening an existing database whose `gopact_runlog` table predates session indexing, `Open` adds `session_id` with an empty default and creates the session/ordinal index. Existing rows remain `session_id=''`: session queries intentionally do not return them, while RunID queries still decode their stored JSON. The migration does not guess or backfill historical session identity.
