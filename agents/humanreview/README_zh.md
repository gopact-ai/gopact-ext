# humanreview

[![CI](https://github.com/gopact-ai/gopact-ext/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/gopact-ai/gopact-ext/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/gopact-ai/gopact-ext/agents/humanreview.svg)](https://pkg.go.dev/github.com/gopact-ai/gopact-ext/agents/humanreview)
[![License](https://img.shields.io/github/license/gopact-ai/gopact-ext)](../../LICENSE)

<!-- gopact:doc-language: zh -->

[英文文档](README.md)

`humanreview` 提供 provider-neutral 的人工审批 graph node。审批节点把 typed graph state 映射成 `gopact.InterruptRecord`，通过 `gopact.ErrInterrupted` 暂停执行，并允许 graph 通过 step-export 或 checkpoint resume 从已审批边界继续向下执行。

安装：

```bash
go get github.com/gopact-ai/gopact-ext/agents/humanreview@v0.1.9
```

用法：

```go
gate, err := humanreview.New(func(_ context.Context, state ReleaseState) (humanreview.Request, error) {
	return humanreview.Request{
		ID:         "release:" + state.ID,
		Reason:     "release approval required",
		RequiredBy: "release-manager",
		Prompt:     gopact.UserMessage("Approve release " + state.ID + "?"),
	}, nil
})
if err != nil {
	return err
}

g := graph.New[ReleaseState]()
g.AddNode("review", gate)
g.AddNode("publish", publishRelease)
g.AddEdge(graph.Start, "review")
g.AddEdge("review", "publish")
g.AddEdge("publish", graph.End)
```

resume 时，core graph 会从 interrupted step 的 output 和后续队列继续执行。审批节点不会再次运行，resume payload 也不会自动写回 state。如果业务需要持久化审批结论，应放在调用方的审批通道、event store 或下游节点契约里。

验证：

```bash
(cd agents/humanreview && go test -count=1 ./...)
```
