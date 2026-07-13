# 💾 SQLite Workflow Store

`stores/sqlite` 是基于 `database/sql` 和无需 CGO 的 `modernc.org/sqlite` driver 构建的单文件持久化后端。

## 适用场景

- 在本地或单服务部署中持久化 Workflow checkpoint 与执行历史；
- 无需外部数据库即可测试持久化恢复和 runlog 行为。

Store 直接实现 `workflow.Checkpointer`、`workflow.CheckpointHistory`、`workflow.CheckpointController` 与 `runlog.Log`。

## 示例

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

完整的可运行版本见 [`example_test.go`](./example_test.go)。

## 优点

同一个 `*sqlite.Store` 直接传给各个 consumer-owned interface，不需要 repository 或 service 抽象。其他持久化后端也可以实现同一组 consumer-owned interface。配置后的持久化是权威数据源：checkpoint、journal 或租约续期失败都会终止本次调用。

该 Store 解决的是执行数据持久化，不提供语义层的 Agent Memory 或 Session state；这些仍是独立的领域能力。

## 取舍

SQLite 是单节点存储，写入并发能力有限。原子续租可以协调安全共享同一数据库文件的多个进程，但它不是跨主机的分布式协调器。

Store 使用一个 SQLite connection 串行写入，逻辑 checkpoint version 保持 append-only。Heartbeat 只会原地更新当前 version 的租约元数据，不会新增历史 version。打开旧数据库时，如果 `gopact_runlog` 表尚无 session 索引，`Open` 会新增默认值为空字符串的 `session_id` 列，并创建 session/ordinal 索引。旧记录会保留 `session_id=''`：Session 查询不会返回这些记录，RunID 查询仍按原有 JSON 解码。迁移不会猜测或回填历史 Session identity。

## 数据保留与维护

Run 仍可恢复时，checkpoint 与 RunLog 历史始终保持 append-only。Store 不会按条数删除最旧记录，因为那会让 Resume、Retry、Jump 和 Fork 读到不完整历史；SDK 内部也不会启动后台清理 goroutine。

Run 进入 `completed`、`failed`、`canceled` 或 `terminated` 后，服务可以在自己的保留期结束后显式清理：

```go
result, err := store.PurgeTerminalRuns(ctx, sqlite.PurgeRequest{
	Before: time.Now().Add(-30 * 24 * time.Hour),
	Limit:  100,
})
```

`Before` 必填。`Limit == 0` 默认清理 100 个 Run，超过 1,000 会直接拒绝。每次调用只处理一个有界批次，并在同一个事务中删除符合条件的 Run-head、全部 checkpoint version 和对应 RunLog event。`running` 与 `interrupted` 永远不会被选择。积压处理应重复调用，直到 `result.Runs == 0`。

如果有审计或重放要求，应先按页归档终态 Run。清理由服务在 SDK 外部调度，并记录返回数量、错误、耗时、数据库体积与 WAL 体积；如果积压不再收敛，应触发告警。滚动升级时，必须先把所有数据库写入进程升级到这个版本，再启用清理；如果 head 落后于一个更新的 checkpoint，清理会跳过该 Run，而不会冒险删除。

SQLite 会复用已释放页，因此清理能限制有效数据量，但数据库文件不一定立即变小。若需要收缩物理文件，只能在独立维护窗口中、完成备份并确保临时磁盘空间充足后运行 `VACUUM`；不要把它放进请求路径或每批清理路径。打开旧数据库时，Store 会以每批 256 个 Run 回填 Run-head 索引，重复执行是安全的。
