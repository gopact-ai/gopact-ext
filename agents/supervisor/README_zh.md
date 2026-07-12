# 🤖 Supervisor Agent

<!-- gopact:doc-language: zh -->

[English](./README.md)

`supervisor` 迭代地向 child Agents 委派任务，并由 Decider 基于累计 delegation results 生成最终响应。

## 常见场景

当下一步不能在开始时一次选定，且每次决策都可能依赖前序 rounds 收集的结果时使用。

## 执行模型

固定图为 `start → decide → delegate → record → decide | finish`。Typed Workflow context 保存 request、round 与 delegation results。Decider 接收 typed `DecisionInput`；Directory Agents 作为 invokable Workflow nodes 执行。中断的 child 会在同一个 child Run 中恢复，不重复已接受 decision 或已完成 delegation。`WithMaxRounds` 限制成功 delegation 次数，但达到上限后仍允许执行最终 synthesis decision。

## 示例

[example_test.go](./example_test.go) 委派一次 research 任务，然后返回累计结果。

```bash
go test -run ExampleNew -count=1 -v
```

示例会挂载自己的本地 event handler。使用 `-v` 时，终端会显示写入 stderr 的有界 Workflow 过程事件和测试 PASS 状态。稳定业务结果写入 stdout，由 Go example harness 捕获并与 `// Output:` 校验，不会直接显示在终端。

## 优点

- 每次决策都可以使用全部累计 child results。
- 动态 delegation 与最终 synthesis 保持为显式且可恢复的 Workflow 阶段。

## 限制

- Delegation 需要有界 round count 与可靠的 Decider。
- 相比一次性 routing，迭代控制循环会增加成本与变化性。

## 何时选择其他 Agent

一次选择已经足够时使用 `router`；完整 pipeline 在构造期已经固定时使用 `sequential`。
