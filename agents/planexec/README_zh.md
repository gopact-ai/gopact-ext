# Plan Execute Agent

<!-- gopact:doc-language: zh -->

[English](./README.md)

`planexec` 是固定 Workflow：在运行期创建 plan、执行具名 child steps、可选地替换 plan，并生成 report。

## 常见场景

当构造时还不知道有序 steps，且执行过程需要显式 replan 与 report 阶段时使用。

## 执行模型

初始路径为 `plan → accept-plan → continue → dispatch-step → execute-step → record-step → replan`。从 `replan` 开始，done 分支经 `continue → report`；replacement plan 分支经 `accept-replan → continue → dispatch-step` 继续执行 steps。Typed Workflow context 保存 plan、cursor、results 与 transition count。Plan 会校验 child membership、step identity 与 status，以及 replacement version 是否递增。`WithDirectory`、`WithPlanner`、`WithReplanner`、`WithReporter` 是必需配置；`WithMaxTransitions` 可收紧默认 32 次 replan transition 上限。只有 `record-step` 写入完成状态；中断的 child 会在原 child Run 中恢复。

## 示例

[example_test.go](./example_test.go) 创建一个单步骤 release plan，通过 child Agent 执行并生成报告。

```bash
go test -run ExampleNew -count=1 -v
```

示例会挂载自己的本地 event handler。使用 `-v` 时，终端会显示写入 stderr 的有界 Workflow 过程事件和测试 PASS 状态。稳定业务结果写入 stdout，由 Go example harness 捕获并与 `// Output:` 校验，不会直接显示在终端。

## 优点

- Plan transition 与 replacement version 显式且经过校验。
- Replan 与 report 阶段属于可恢复的 Workflow。

## 限制

- 相比固定 pipeline，需要更多接口与 lifecycle stages。
- 算法固定了 planning、execution、replanning 与 reporting 的位置。

## 何时选择其他 Agent

当全部 steps 及其顺序在构造期已经确定时，使用 `sequential`。
