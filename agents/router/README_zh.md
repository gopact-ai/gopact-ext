# 🤖 Router Agent

<!-- gopact:doc-language: zh -->

[English](./README.md)

`router` 只做一次 typed route 决策，并从 immutable `agent.Directory` 调用一个 child。

## 常见场景

适用于 support 分类、specialist 选择或其他只应由一个 child 处理请求的一次性 dispatch。

## 执行模型

Selection 与 selected child 都是 Workflow 节点。Selector 接收请求和可用 child identities，再指定一个 child。执行事实、lineage、checkpoint、中断与 resume 由 Workflow 负责管理。Router 不会在选中后监督 child 或重新规划。

## 示例

[example_test.go](./example_test.go) 对 timeout 请求进行分类，并将其派发给 technical support。

```bash
go test -run ExampleNew -count=1 -v
```

示例会挂载自己的本地 event handler。使用 `-v` 时，终端会显示写入 stderr 的有界 Workflow 过程事件和测试 PASS 状态。稳定业务结果写入 stdout，由 Go example harness 捕获并与 `// Output:` 校验，不会直接显示在终端。

## 优点

- 最小的动态 dispatch 表面：一次选择与一次 child 调用。
- Directory membership 与 child identity 保持显式。

## 限制

- Selection 只执行一次。
- 选中后不提供监督、迭代委派或重新规划。

## 何时选择其他 Agent

当决策需要使用累计的 child results 并反复委派时，使用 `supervisor`。
