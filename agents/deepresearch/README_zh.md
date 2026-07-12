# 🤖 Deep Research Agent

<!-- gopact:doc-language: zh -->

[English](./README.md)

`deepresearch` 是用于 source discovery、evidence 提取、citation 校验和带引用 synthesis 的固定 Workflow。

## 常见场景

当报告必须保留明确的 source、evidence 与 citation 关系，而不是只生成无法追溯的答案时使用。

## 执行模型

Workflow 依次完成 query 规划、discovery fan-out 与 merge、逐 source fetch、evidence 提取、citation 结构校验和 synthesis。Queries、去重 sources、evidence 与 source cursors 位于 typed Workflow context。Discovery 默认并发上限为 8，可通过 `WithMaxParallelism` 调整。Planner、Discoverer、Fetcher、EvidenceExtractor 与 Synthesizer 是必需组件，CitationVerifier 可选。Identity、唯一性、coverage 与引用完整性会在可选业务 citation policy 和 synthesis 之前校验。

## 示例

[example_test.go](./example_test.go) 规划一个 query、获取一个 source、提取 evidence、校验 citation 并生成报告。

```bash
go test -run ExampleNew -count=1 -v
```

示例会挂载自己的本地 event handler。使用 `-v` 时，终端会显示写入 stderr 的有界 Workflow 过程事件和测试 PASS 状态。稳定业务结果写入 stdout，由 Go example harness 捕获并与 `// Output:` 校验，不会直接显示在终端。

## 优点

- 在 synthesis 前强制保证 source、evidence 与 citation 的结构完整性。
- Research 各阶段与中间 identity 都保持为显式 Workflow 事实。

## 限制

- Research pipeline 固定，组件配置成本高于通用任务 Agent。
- 业务 citation policy 可以追加校验，但不能替代结构校验。

## 何时选择其他 Agent

当工作只是通用 child-task ledger，不需要 evidence 与 citation ledger 时使用 `deep`。
