# planexec

[![CI](https://github.com/gopact-ai/gopact-ext/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/gopact-ai/gopact-ext/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/gopact-ai/gopact-ext/agents/planexec.svg)](https://pkg.go.dev/github.com/gopact-ai/gopact-ext/agents/planexec)
[![License](https://img.shields.io/github/license/gopact-ai/gopact-ext)](../../LICENSE)

<!-- gopact:doc-language: zh,en -->

## 中文

`planexec` 是 provider-neutral 的 Plan-Execute agent template。它把任务拆成步骤、逐步执行、汇总结果，并支持 replan、approval、checkpoint resume 和 cancel 传播。

## 安装

```bash
go get github.com/gopact-ai/gopact-ext/agents/planexec@v0.2.15
```

## 用法

最常见的方式是把任意 `gopact.ResponseModel` 交给 `NewModelAgent`：

```go
agent, err := planexec.NewModelAgent(
	model,
	planexec.WithModelOptions(
		gopact.WithMaxOutputTokens(1024),
		gopact.WithTemperature(0.2),
	),
	planexec.WithApprovalPolicy(policy),
	planexec.WithCheckpointStore(checkpoints),
)
if err != nil {
	return err
}

events, err := gopacttest.CollectEvents(agent.Run(
	ctx,
	"draft and review the release note",
	gopact.WithRuntimeIDs(gopact.RuntimeIDs{ThreadID: "release-thread"}),
))
```

更高级的调用方可以用 `New` 注入自定义 `Planner`、`Executor` 和 `Replanner`，让模板只负责 graph orchestration。

## 能力边界

- planner 和 executor 都使用 core 的 `gopact.ModelRequestOption`，provider 选择和鉴权不在本模块内处理。
- approval 通过 `gopact.Policy` 触发 interrupt/resume，不在模板里实现 UI 或人工审批系统。
- checkpoint 依赖调用方传入的 `graph.CheckpointStore`。
- replan 当前是一次失败后的替换计划，不是无限循环规划器。

## 验证

```bash
(cd agents/planexec && go test -count=1 ./...)
```

## English

`planexec` is a provider-neutral Plan-Execute template. It plans a task into steps, executes them, summarizes the result, and supports replan, approval interrupts, checkpoint resume, and cancellation propagation.

Install it with `go get github.com/gopact-ai/gopact-ext/agents/planexec@v0.2.15`. Use `NewModelAgent` for model-backed planning/execution or `New` when you want to provide custom `Planner`, `Executor`, and `Replanner` implementations.
