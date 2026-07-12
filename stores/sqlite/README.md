# SQLite Workflow Store

`stores/sqlite` is a single-file durable backend for local or single-service Workflow execution history and checkpoints. It is also convenient for tests. The store directly implements `workflow.Checkpointer`, `workflow.CheckpointHistory`, `workflow.CheckpointController`, and `runlog.Log` with `database/sql` and the CGO-free `modernc.org/sqlite` driver.

```go
store, err := sqlite.Open("gopact.db")
if err != nil {
	return err
}
defer store.Close()

wf := workflow.New[Input, Output](
	"example",
	workflow.WithStrictCheckpointer(store),
	workflow.WithStrictJournal(store),
)
```

The same `*sqlite.Store` is passed directly to each consumer-owned interface. Other persistent backends can implement those same interfaces without a repository or service abstraction. `WithCheckpointer`/`WithJournal` remain best-effort; use the strict variants when a persistence failure must fail the invocation.

The store solves durable execution persistence. It does not provide semantic Agent Memory or Session state; those remain separate domain capabilities.

## Trade-offs

- Pros: direct consumer-owned interfaces, CGO-free driver, one-file deployment, and durable local test fixtures.
- Cons: SQLite is a single-node store with bounded write concurrency. It does not provide distributed coordination.

The store serializes writes through one SQLite connection and keeps checkpoint versions append-only. When opening an existing database whose `gopact_runlog` table predates session indexing, `Open` adds `session_id` with an empty default and creates the session/ordinal index. Existing rows remain `session_id=''`: session queries intentionally do not return them, while RunID queries still decode their stored JSON. The migration does not guess or backfill historical session identity.
