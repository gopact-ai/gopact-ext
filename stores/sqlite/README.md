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

SQLite is a single-node store with bounded write concurrency. Atomic lease renewal coordinates Workflow ownership between processes that safely share the same database file; it is not a multi-host distributed coordinator. Multi-host deployments need a distributed database Store with atomic Claim and fencing.

The store serializes writes through one SQLite connection and keeps logical checkpoint versions append-only. A heartbeat updates only the current version's lease metadata in place and does not append a history version. When opening an existing database whose `gopact_runlog` table predates session indexing, `Open` adds `session_id` with an empty default and creates the session/ordinal index. Existing rows remain `session_id=''`: session queries intentionally do not return them, while RunID queries still decode their stored JSON. The migration does not guess or backfill historical session identity.

## External side effects

SQLite makes Workflow state durable, but recovered node execution remains at-least-once. Use `RunInfo.RunID + "/" + RunInfo.ActivationID` as a recovery-stable key only with an external API that natively deduplicates it, or write a uniquely constrained dedup/outbox record in the same database transaction as the business data. This Store does not create that business record and cannot atomically combine its checkpoint transaction with an arbitrary remote API.

If an explicit business retry should produce another side effect, use a new operation key rather than the recovery key. See the runnable [`concepts/durable-resume`](https://github.com/gopact-ai/gopact-examples/tree/main/concepts/durable-resume) example.

## Retention and maintenance

Checkpoint and RunLog history remains append-only while a Run can still be resumed. The store does not delete the oldest records by count because doing so would make Resume, Retry, Jump, and Fork incomplete. It also does not start a background cleanup goroutine.

After a Run reaches `completed`, `failed`, `canceled`, or `terminated`, the service may delete it after its own retention period:

```go
result, err := store.PurgeTerminalRuns(ctx, sqlite.PurgeRequest{
	Before: time.Now().Add(-30 * 24 * time.Hour),
	Limit:  100,
})
```

`Before` is required. A zero `Limit` defaults to 100 and values above 1,000 are rejected. One call deletes a bounded batch of eligible Run heads, every checkpoint version, and all matching RunLog events in one transaction. Running and interrupted Runs are never selected. Repeat calls until `result.Runs == 0` to drain a backlog.

Archive terminal Runs first when audit or replay requirements apply. Schedule purge outside the SDK, record the returned counts, errors, duration, database size, and WAL size, and alert when the backlog stops converging. Deploy this version to every process that writes the database before enabling purge during a rolling upgrade; a stale head with a newer checkpoint is skipped rather than deleted.

SQLite reuses freed pages, so successful purges bound live data without necessarily shrinking the database file. Run `VACUUM` only in a separate maintenance window, after a backup and with sufficient temporary disk space; do not put it on the request or per-batch purge path. Opening an older database backfills the Run-head index in batches of 256 Runs and is safe to repeat.
