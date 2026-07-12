# Sequential Agent

<!-- gopact:doc-language: zh -->

[English](./README.md)

`sequential` 通过一个 Workflow，按照构造期声明的固定顺序执行 child Agents。

## 常见场景

适用于固定的 writer-reviewer、transform-validate 或其他每个 child 都基于前序结果继续处理的有序 pipeline。

## 执行模型

第一个 child 接收调用方请求；后续 child 接收前一个 response 的 message、artifacts 与 metadata。任一 child 失败会立即终止序列。每个 child 都是 typed Workflow invokable node；lineage、节点事实、checkpoint、中断与 resume 由 Workflow 负责管理。中断的 child 在同一个 child Run 中恢复，已完成 child 不重放。

## 示例

[example_test.go](./example_test.go) 将 release notes 依次交给 writer 和 reviewer。

```bash
go test -run ExampleNew -count=1 -v
```

示例会挂载自己的本地 event handler。使用 `-v` 时，终端会显示写入 stderr 的有界 Workflow 过程事件和测试 PASS 状态。稳定业务结果写入 stdout，由 Go example harness 捕获并与 `// Output:` 校验，不会直接显示在终端。

## 优点

- Handoff 与执行顺序确定。
- 同一时刻只有一个 child 活跃，因此 failure 与 resume 语义简单。

## 限制

- 不提供运行期 planning 或动态 routing。
- 不会并发执行相互独立的工作。

## 何时选择其他 Agent

Steps 需要在运行期创建时使用 `planexec`；独立 branches 应并发 fan-out 时使用 `parallel`。
