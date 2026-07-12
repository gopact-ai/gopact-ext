# ReAct Agent

<!-- gopact:doc-language: zh -->

[English](./README.md)

`react` 是 Workflow-backed Agent：一个模型在有界循环中选择 tools、消费 observations、修复响应并结束。

## 常见场景

当模型必须拥有下一步动作决策权，且已完成的 model turns 或 tool batches 在中断后不能重放时使用。

## 执行模型

可观测执行图从 `prepare → model` 开始。Final 分支为 `model → finish`；repair 分支为 `model → continue → prepare`；tools 分支为 `model → continue → dispatch-tools → tool → observe-tools → continue`。之后如果仍有下一批调用，`continue` 会回到 `dispatch-tools`，否则回到 `prepare` 开始下一次 model turn。Typed Workflow state 保存 turn、messages、pending observations、tool calls、artifacts 和 metadata。`prepare` 只消费一次 pending observations，并使用已配置的 tool specs 构造模型实际可见的请求。同一 batch 的 direct tools 并发执行，但 observations 保持模型调用顺序；invokable tools 是串行屏障。`WithLimits` 限制 turns、tool call 总数和并行 direct tools 数量。

## 示例

[example_test.go](./example_test.go) 执行一次模型请求的 lookup，把 observation 返回给模型，再生成最终答案。

```bash
go test -run ExampleNew -count=1 -v
```

示例会挂载自己的本地 event handler。使用 `-v` 时，终端会显示写入 stderr 的有界 Workflow 过程事件和测试 PASS 状态。稳定业务结果写入 stdout，由 Go example harness 捕获并与 `// Output:` 校验，不会直接显示在终端。

## 优点

- 模型与 tool 可以自然反馈，tool outcome 支持恢复。
- Tool batch 有界，observations 保持模型调用顺序。

## 限制

- Context projection 与 model-tool loop 算法固定。
- 决策由模型控制，因此确定性低于代码控制的编排。

## 何时选择其他 Agent

更适合确定性编排时，使用 `loop`、`router`、`sequential` 或其他代码控制的 Workflow。
