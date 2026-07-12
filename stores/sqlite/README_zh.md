# SQLite Workflow Store

`stores/sqlite` 适合用作本地或单服务场景下的单文件持久化后端，保存 Workflow 执行历史和 checkpoint，也适合测试。它直接实现 `workflow.Checkpointer`、`workflow.CheckpointHistory`、`workflow.CheckpointController` 与 `runlog.Log`，底层只使用 `database/sql` 和无需 CGO 的 `modernc.org/sqlite` driver。

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

同一个 `*sqlite.Store` 直接传给各个 consumer-owned interface，不需要 repository 或 service 抽象。其他持久化后端也可以实现同一组 consumer-owned interface。`WithCheckpointer`/`WithJournal` 仍是 best-effort；只有持久化失败必须终止调用时才使用 strict 版本。

该 Store 解决的是执行数据持久化，不提供语义层的 Agent Memory 或 Session state；这些仍是独立的领域能力。

## 取舍

- 优点：直接实现消费方接口、无需 CGO、单文件部署简单，也便于构造持久化测试环境。
- 缺点：SQLite 是单节点存储，写入并发能力有限，也不提供分布式协调。

Store 使用一个 SQLite connection 串行写入，checkpoint version 保持 append-only。打开旧数据库时，如果 `gopact_runlog` 表尚无 session 索引，`Open` 会新增默认值为空字符串的 `session_id` 列，并创建 session/ordinal 索引。旧记录会保留 `session_id=''`：Session 查询不会返回这些记录，RunID 查询仍按原有 JSON 解码。迁移不会猜测或回填历史 Session identity。
