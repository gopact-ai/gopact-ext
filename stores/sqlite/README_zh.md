# 💾 SQLite Workflow Store

`stores/sqlite` 基于共享的 GORM `dbstore` 与无需 CGO 的 `libtnb/sqlite` driver（底层为 modernc SQLite）构建单文件持久化后端。

## 适用场景

- 在本地或单服务部署中持久化 Workflow checkpoint 与执行历史；
- 无需外部数据库即可测试持久化恢复和 runlog 行为。

Store 直接实现 `workflow.Checkpointer`、`workflow.CheckpointHistory`、`workflow.CheckpointController`、`runlog.Log` 与 `runlog.FencedLog`。

## 示例

```go
if err := sqlite.Migrate("gopact.db"); err != nil { // 部署迁移阶段
	return err
}
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

完整的可运行版本见 [`example_test.go`](./example_test.go)。

## 优点

同一个 `*sqlite.Store` 直接传给各个 consumer-owned interface，不需要 repository 或 service 抽象。其他持久化后端也可以实现同一组 consumer-owned interface。配置后的持久化是权威数据源：checkpoint、journal 或租约续期失败都会终止本次调用。

同一个 Store 同时传给 `WithCheckpointer` 与 `WithJournal` 时，observed/custom event 会在追加 RunLog 记录的同一个 SQLite 事务中校验当前 head 是否仍为 running、owner 与 claim sequence 是否匹配，以及租约是否尚未过期。这样可以关闭 claim→append 窗口，也不会再为每个 observed event 额外产生两份 checkpoint history version。

该 Store 解决的是执行数据持久化，不提供语义层的 Agent Memory 或 Session state；这些仍是独立的领域能力。

## 取舍

SQLite 是单节点存储，写入并发能力有限。原子续租可以协调安全共享同一数据库文件的多个进程，但它不是跨主机的分布式协调器。多主机部署必须改用支持原子 Claim 与 fencing 的分布式数据库 Store。

Store 使用一个 SQLite connection 串行写入。文件库强制使用 `journal_mode=DELETE`，`Migrate`/`Open` 都会拒绝显式指定其他 journal mode 的 DSN；只有真正的内存数据库可以使用 `MEMORY`。`Migrate` 在部署迁移阶段运行，应用实例只调用负责校验和连接的 `Open`。旧数据库若持久化在 WAL 模式，`Migrate` 会在 schema 变更前先执行 truncating checkpoint，要求没有 busy reader，再切换到 `journal_mode=DELETE` 并复查结果。迁移时必须先停止所有其他 SQLite 连接；不要手工删除 `-wal` 或 `-shm` 文件。Heartbeat 只会原地更新当前 version 的租约元数据，不会新增历史 version。真正的内存数据库无法跨迁移连接保留 schema，因此由 `Open` 直接初始化。

迁移会先做只读兼容性检查，再修改结构。Run/Session ID 超过 191 字节、尾随空格、非法 UTF-8/NUL、损坏的历史 JSON，或超过新 4 MiB 上限的记录都会明确失败，且不会写 migration marker。旧 RunLog 的 retention 年龄从迁移时刻开始计算。防御性 SQLite trigger 会为直接插入同步 v2 元数据，但它不能让新旧 writer 混跑变得安全：升级期间必须停止全部 writer，完成后不得继续运行旧二进制或直接降级。缺少 Session identity 的旧记录仍保留 `session_id=''`：RunID 查询可用，Session 查询会主动忽略这些记录。

## 外部副作用

SQLite 能持久化 Workflow 状态，但恢复后的节点执行仍是 at-least-once。`RunInfo.RunID + "/" + RunInfo.ActivationID` 只能作为跨恢复稳定的 key：要么交给原生支持按 key 去重的外部 API，要么由业务在修改业务数据的同一数据库事务中，写入带唯一约束的 dedup/outbox 记录。该 Store 不会代替业务创建这条记录，也无法把自身 checkpoint 事务与任意远程 API 合并成一个原子事务。

如果显式业务重试需要再次产生副作用，应使用新的 operation key，而不是恢复幂等键。可运行示例见 [`concepts/durable-resume`](https://github.com/gopact-ai/gopact-examples/tree/main/concepts/durable-resume)。

## 数据保留与维护

Run 仍可恢复时，checkpoint 与 RunLog 历史始终保持 append-only。Store 不会按条数删除最旧记录，因为那会让 Resume、Retry、Jump 和 Fork 读到不完整历史；SDK 内部也不会启动后台清理 goroutine。

Run 进入 `completed`、`failed`、`canceled` 或 `terminated` 后，服务可以在自己的保留期结束后显式清理：

```go
result, err := store.PurgeTerminalRuns(ctx, sqlite.PurgeRequest{
	Before:    time.Now().Add(-30 * 24 * time.Hour),
	Limit:     100,
	RowLimit:  1000,
	ByteLimit: 64 << 20,
})
```

`Before` 必填。`Limit` 限制选中的 Run 数（默认 100，最大 1,000）；`RowLimit` 统计逻辑 checkpoint/event 历史行，`ByteLimit` 统计被删记录的编码内容与 payload 字节（默认 1,000 行与 64 MiB，最大 10,000 行与 1 GiB）。它们是清理工作量预算，不是 rollback journal 或文件系统写入量的硬上限；索引、元数据与页面变更都会带来额外开销。若旧库存在单条超大记录，会单独用一个事务处理，保证清理可推进。选中 Run 时会原子地把 registry 切为 `purging` 并删除可执行 Run-head，后续 Append 与 Reopen 都会被拒绝；历史随后由小事务分段删除，最终保留永久 `purged` tombstone，防止 RunID 被复活。`result.Pending` 表示尚未排空的 Run，积压处理应重复调用，直到它归零且没有新的合格 Run。`running` 与 `interrupted` 永远不会被选择。

独立 RunLog 的保留策略是另一条显式、会损失重放能力的路径：

```go
result, err := store.PurgeRunLog(ctx, sqlite.RunLogPurgeRequest{
	Before:          time.Now().Add(-30 * 24 * time.Hour),
	Limit:           1000,
	ByteLimit:       64 << 20,
	AllowReplayLoss: true,
})
```

截止时间采用 Store 写入时间，而不是调用方可伪造的 `Record.Timestamp`。它只选择 registry 中明确为 journal 的条目，不会选中本数据库的 Workflow 条目。`AllowReplayLoss` 必须显式为 true，因为 checkpointer/runlog 分库时，即使 journal 库没有本地 head，Retry、Fork 或审计仍可能依赖这些事件；在应用重放契约允许删除前不要调用。清空后的 journal registry 会回收；Workflow 的紧凑 tombstone 会保留，并随已清理的唯一 RunID 数增长。

对于长期运行的 active Workflow，可用 `PurgeConfirmedRunLog` 按 RunID、按有界批次压缩连续的已确认前缀。它要求显式设置 `AllowHistoryLoss: true`，checkpoint 仍有 pending event 时会拒绝，并持久化 sequence floor 来阻止迟到写入复活旧序列。当前 checkpoint 的恢复仍然可用，但被删前缀上的 Retry/Fork、timeline 与审计能力会丢失；这些契约有要求时必须先归档。查询游标低于 floor 时会返回 `runlog.ErrHistoryCompacted`，不会静默给出不完整历史。

如果有审计或重放要求，应先按页归档终态 Run。清理由服务在 SDK 外部调度，并记录返回数量、Pending 积压、错误、耗时与数据库体积；SDK 不启动后台清理 goroutine。若积压不再收敛，应触发告警。head 若落后于更新的 checkpoint，清理会跳过该 Run，而不会冒险删除。

SQLite 会复用已释放页，因此清理能限制有效数据量，但数据库文件不一定立即变小。若需要收缩物理文件，只能在独立维护窗口中、完成备份并确保临时磁盘空间充足后运行 `VACUUM`；不要把它放进请求路径或每批清理路径。旧库迁移会以有界、可重试批次回填窄 retention 元数据和 Run registry，不会重写 RunLog 大 BLOB。
