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
