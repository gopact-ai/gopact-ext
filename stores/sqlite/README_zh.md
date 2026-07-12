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
	workflow.WithStrictCheckpointer(store),
	workflow.WithStrictJournal(store),
)
```

完整的可运行版本见 [`example_test.go`](./example_test.go)。

## 优点

同一个 `*sqlite.Store` 直接传给各个 consumer-owned interface，不需要 repository 或 service 抽象。其他持久化后端也可以实现同一组 consumer-owned interface。`WithCheckpointer`/`WithJournal` 仍是 best-effort；只有持久化失败必须终止调用时才使用 strict 版本。

该 Store 解决的是执行数据持久化，不提供语义层的 Agent Memory 或 Session state；这些仍是独立的领域能力。

## 取舍

SQLite 是单节点存储，写入并发能力有限，也不提供分布式协调。

Store 使用一个 SQLite connection 串行写入，checkpoint version 保持 append-only。打开旧数据库时，如果 `gopact_runlog` 表尚无 session 索引，`Open` 会新增默认值为空字符串的 `session_id` 列，并创建 session/ordinal 索引。旧记录会保留 `session_id=''`：Session 查询不会返回这些记录，RunID 查询仍按原有 JSON 解码。迁移不会猜测或回填历史 Session identity。
