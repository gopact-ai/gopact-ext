# Loop Agent

<!-- gopact:doc-language: zh -->

[English](./README.md)

`loop` 重复调用一个固定 child Agent，直到 typed code condition 停止 Workflow，或达到硬性 iteration 上限。

## 常见场景

适用于确定性的改进、轮询或校验循环：每一轮都由同一个 child 执行，并由代码控制停止规则。

## 执行模型

Workflow 在 `child` 与 `condition` 节点之间交替。每次响应后，typed `Condition` 返回 `DecisionContinue` 或 `DecisionStop`；`WithMaxIterations` 设置正整数硬上限。重复执行会在同一 parent Run 中产生新的节点执行版本。Checkpoint、child continuation 与 resume 由 Workflow 负责管理，因此已完成轮次不会重放。

## 示例

[example_test.go](./example_test.go) 连续改进一份 draft 三次，然后由 typed condition 停止循环。

```bash
go test -run ExampleNew -count=1 -v
```

示例会挂载自己的本地 event handler。使用 `-v` 时，终端会显示写入 stderr 的有界 Workflow 过程事件和测试 PASS 状态。稳定业务结果写入 stdout，由 Go example harness 捕获并与 `// Output:` 校验，不会直接显示在终端。

## 优点

- 停止规则显式、typed、确定且有界。
- Resume 会保留已完成轮次，不会从头启动循环。

## 限制

- 每一轮都使用同一个固定 child。
- 模型不能选择 tool 或控制循环。

## 何时选择其他 Agent

模型需要控制 tool 循环时使用 `react`；循环中需要动态委派多个 child 时使用其他多 child Agent。
