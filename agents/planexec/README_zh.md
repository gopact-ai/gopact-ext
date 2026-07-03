# planexec

<!-- gopact:doc-language: zh -->

[英文文档](./README.md)

## 中文

`planexec` 是 provider-neutral 的 Plan-Execute agent template。它把任务拆成步骤、逐步执行、汇总结果，并支持 replan、approval、checkpoint resume 和 cancel 传播。

## 安装

```bash
go get github.com/gopact-ai/gopact-ext/agents/planexec@v0.2.27
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
