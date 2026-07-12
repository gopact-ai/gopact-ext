# 🤖 Parallel Agent

<!-- gopact:doc-language: zh -->

[English](./README.md)

`parallel` 把一个请求 fan-out 到固定且相互独立的 child Agents，再由显式 Reducer 按声明顺序合并结果。

## 常见场景

适用于可以并发执行、最后需要确定性合并的独立 security、quality、policy 或 specialist review。

## 执行模型

每个 branch 接收原始请求的独立副本。Workflow 提供 fan-out、`SettleAll`、child lineage、checkpoint 与多中断 resume；`WithMaxParallelism` 限制活跃 branch 数量。Reducer 仅在全部 branch settled 后运行，并按 child 声明顺序而非完成顺序接收防御性结果副本。真实 branch failure 不会被 sibling cancellation 覆盖。

## 示例

[example_test.go](./example_test.go) 并发执行 security 与 quality review，再按声明顺序归并结果。

```bash
go test -run ExampleNew -count=1 -v
```

示例会挂载自己的本地 event handler。使用 `-v` 时，终端会显示写入 stderr 的有界 Workflow 过程事件和测试 PASS 状态。稳定业务结果写入 stdout，由 Go example harness 捕获并与 `// Output:` 校验，不会直接显示在终端。

## 优点

- 并发有界，归并顺序确定。
- Branch 请求与结果相互隔离，sibling 修改不会影响其他 branch。

## 限制

- Branch 必须彼此独立，不能消费 sibling 的结果。
- 归并策略必须由 Reducer 显式表达。

## 何时选择其他 Agent

当一个 child 必须接收并继续处理前一个 child 的输出时，使用 `sequential`。
