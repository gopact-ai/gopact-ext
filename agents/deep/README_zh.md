# 🤖 Deep Agent

<!-- gopact:doc-language: zh -->

[English](./README.md)

`deep` 是 Workflow-backed Agent，通过具名 child Agents 执行有界且经过校验的长周期 task ledger，并向后传递 artifact context。

## 常见场景

当 Planner 能生成引用已有 child Agents 的有序 task ledger，且后续任务需要使用前序结果的 artifacts 时使用。

## 执行模型

固定图为 `plan → accept-plan → continue → build-context → execute-task → record-task → continue | finish`。Typed Workflow state 保存 plan、cursor、results、progress 和 artifact context；`build-context` 将原始 messages、metadata 与累计的 `ContextRefs` 投影到每个 child request。Plan 会校验 task 上限、唯一 identity、pending status 与 Directory membership。只有 `record-task` 推进完成进度；child 中断后在同一个 child Run 中恢复，不重放已完成任务。

## 示例

[example_test.go](./example_test.go) 校验并执行一个具名 child task，生成一份报告。

```bash
go test -run ExampleNew -count=1 -v
```

示例会挂载自己的本地 event handler。使用 `-v` 时，终端会显示写入 stderr 的有界 Workflow 过程事件和测试 PASS 状态。稳定业务结果写入 stdout，由 Go example harness 捕获并与 `// Output:` 校验，不会直接显示在终端。

## 优点

- Task identity 经过校验，child 进度可以恢复。
- 已完成任务的 artifact context 可显式传递给后续任务。

## 限制

- Planner 只运行一次，任务按 plan 顺序执行。
- 检索、replan、报告策略和 citation 语义不属于这套固定算法。

## 何时选择其他 Agent

需要运行期 replan 与 report 阶段时使用 `planexec`；核心诉求是 evidence 与 citation 完整性时使用 `deepresearch`。
