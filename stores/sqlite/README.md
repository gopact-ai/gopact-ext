# 💾 SQLite Workflow Store

`stores/sqlite` is a single-file durable backend built on the shared GORM `dbstore` with the CGO-free `libtnb/sqlite` driver powered by modernc SQLite.

## Use cases

- Persist Workflow checkpoints and execution history in local or single-service deployments.
- Exercise durable resume and run-log behavior in tests without an external database.

The store directly implements `workflow.Store`.

## Example

```go
if err := sqlite.Migrate("gopact.db"); err != nil { // deployment migration stage
	return err
}
store, err := sqlite.Open("gopact.db")
if err != nil {
	return err
}
defer store.Close()

wf := workflow.New[Input, Output](
	"example",
	workflow.WithStore(store),
	workflow.WithCheckpointLease(3*time.Minute, time.Minute),
)
```

The complete runnable version is in [`example_test.go`](./example_test.go).

## Advantages

The same `*sqlite.Store` provides checkpoints, history, journal queries, and fenced appends through one consumer-owned interface. Other persistent backends can implement `workflow.Store` without a repository or service abstraction. Configured persistence is authoritative: a checkpoint, journal, or lease-renewal failure stops the invocation.

Observed and custom events validate the current running owner, claim sequence, and unexpired lease in the same SQLite transaction that appends the RunLog record. This closes the claim-to-append window and avoids extra checkpoint history versions for each observed event.

The store solves durable execution persistence. It does not provide semantic Agent Memory or Session state; those remain separate domain capabilities.

## Trade-offs

SQLite is a single-node store with bounded write concurrency. Atomic claim and renewal coordinate Workflow ownership between processes that safely share the same database file; a stale claim fails if another process renewed or replaced the loaded head. It is not a multi-host distributed coordinator. Multi-host deployments need a distributed database Store with atomic Claim and fencing.

The store serializes writes through one SQLite connection. File databases require `journal_mode=DELETE`, and `Migrate`/`Open` reject DSNs that explicitly select another journal mode; only a true in-memory database may use `MEMORY`. `Migrate` runs in the deployment stage, while application instances call `Open`, which only verifies and connects. When an old database is persistently in WAL mode, `Migrate` first runs a truncating checkpoint, requires it to report no busy readers, switches to `journal_mode=DELETE`, and verifies the result before schema changes. Stop every other SQLite connection for migration; never delete `-wal` or `-shm` files by hand. A heartbeat updates only the current version's lease metadata in place and does not append a history version. A true in-memory database is initialized by `Open` because its schema cannot survive a separate migration connection.

Migration performs a read-only compatibility preflight before changing schema. Run and Session IDs over 191 bytes, IDs ending in a space, invalid UTF-8/NUL, malformed historical JSON, and records above the new 4 MiB limit fail explicitly without writing a migration marker. Older RunLog rows start their retention age at migration time. Defensive SQLite triggers keep v2 metadata synchronized for direct inserts, but they do not make mixed-version writers safe: stop every writer for the upgrade and do not run or downgrade to an old binary afterward. Historical rows without a Session identity retain `session_id=''`; RunID queries still work, while Session queries intentionally omit those rows.

## External side effects

SQLite makes Workflow state durable, but recovered node execution remains at-least-once. Use `RunInfo.RunID + "/" + RunInfo.ActivationID` as a recovery-stable key only with an external API that natively deduplicates it, or write a uniquely constrained dedup/outbox record in the same database transaction as the business data. This Store does not create that business record and cannot atomically combine its checkpoint transaction with an arbitrary remote API.

If an explicit business retry should produce another side effect, use a new operation key rather than the recovery key. See the runnable [`concepts/durable-resume`](https://github.com/gopact-ai/gopact-examples/tree/main/concepts/durable-resume) example.

## Retention and maintenance

Checkpoint and RunLog history remains append-only while a Run can still be resumed. The store does not delete the oldest records by count because doing so would make Resume, Retry, Jump, and Fork incomplete. It also does not start a background cleanup goroutine.

After a Run reaches `completed`, `failed`, `canceled`, or `terminated`, the service may delete it after its own retention period:

```go
result, err := store.PurgeTerminalRuns(ctx, sqlite.PurgeRequest{
	Before:    time.Now().Add(-30 * 24 * time.Hour),
	Limit:     100,
	RowLimit:  1000,
	ByteLimit: 64 << 20,
})
```

`Before` is required. `Limit` bounds selected Runs (default 100, maximum 1,000). `RowLimit` counts logical checkpoint/event history rows and `ByteLimit` counts encoded record/payload bytes deleted by one call (defaults 1,000 rows and 64 MiB; maxima 10,000 rows and 1 GiB). They are work budgets, not hard limits on rollback-journal or filesystem writes; indexes, metadata, and page changes add overhead. One oversized legacy row is processed alone so cleanup can advance. Selection atomically changes the registry to `purging` and removes the executable Run head; later appends and reopen attempts are rejected. History is then deleted in small transactions, and the permanent `purged` tombstone prevents RunID resurrection. `result.Pending` reports Runs still being drained; repeat until it reaches zero and no new eligible Runs are selected. Running and interrupted Runs are never selected.

Standalone RunLog retention is separate and destructive to replay:

```go
result, err := store.PurgeRunLog(ctx, sqlite.RunLogPurgeRequest{
	Before:          time.Now().Add(-30 * 24 * time.Hour),
	Limit:           1000,
	ByteLimit:       64 << 20,
	AllowReplayLoss: true,
})
```

The cutoff is Store append time, not `Record.Timestamp`. Only registry entries classified as journal are selected; Workflow entries in this database are excluded. `AllowReplayLoss` is mandatory because a separate journal database may still be used by Retry, Fork, or audit workflows even though it has no local checkpoint head. Do not call this purge for such a database until the application's replay contract permits deletion. Empty journal registry rows are reclaimed; compact Workflow tombstones remain and grow with unique purged RunIDs.

For a long-running active Workflow, `PurgeConfirmedRunLog` can compact one RunID's contiguous confirmed prefix in bounded batches. It requires `AllowHistoryLoss: true`, refuses a checkpoint with a pending event, and preserves a durable sequence floor that rejects late writes. Current-checkpoint recovery remains valid, but Retry/Fork, timeline, and audit access to the deleted prefix does not; archive first when those contracts apply. Queries below the floor return `runlog.ErrHistoryCompacted`.

Archive terminal Runs first when audit or replay requirements apply. Schedule purge outside the SDK, record counts, pending backlog, errors, duration, and database size, and alert when the backlog stops converging. The SDK starts no cleanup goroutine. A stale Run head with a newer checkpoint is skipped rather than deleted.

SQLite reuses freed pages, so successful purges bound live data without necessarily shrinking the database file. Run `VACUUM` only in a separate maintenance window, after a backup and with sufficient temporary disk space; do not put it on the request or per-batch purge path. Migrating an older database backfills narrow retention metadata and Run registries in bounded, restart-safe batches without rewriting RunLog BLOBs.
