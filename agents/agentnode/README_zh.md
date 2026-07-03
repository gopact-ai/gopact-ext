# agentnode

<!-- gopact:doc-language: zh -->

[英文文档](./README.md)

`agentnode` 将 A2A agent 适配成 typed `graph.NodeFunc`。当 workflow graph 的某一步需要委派给垂域 agent，同时还要在父 graph event stream 中保留 A2A 子事件证据时，使用这个模块。

安装：

```bash
go get github.com/gopact-ai/gopact-ext/agents/agentnode@v0.1.7
```

最小用法：

```go
node, err := agentnode.New(
	childAgent,
	func(ctx context.Context, state State) (a2a.Task, error) {
		ids, _ := gopact.RuntimeIDsFromContext(ctx)
		return a2a.Task{ID: "plan-task", IDs: ids, Input: state.Input}, nil
	},
	func(ctx context.Context, state State, result a2a.Result) (State, error) {
		state.Plan = result.Output
		return state, nil
	},
)
```

如果 child agent 支持 `a2a.StreamingAgent`，adapter 会优先使用 stream。A2A message、artifact、status、completion 和 failure 事件会通过 `graph.EmitNodeEvent` 写入父 graph event stream，并带上 `graph_parent_node`、`graph_parent_step` 元数据。

验证：

```bash
(cd agents/agentnode && go test -count=1 ./...)
```
