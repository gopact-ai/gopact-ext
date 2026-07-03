# Feature Coverage

<!-- gopact:doc-language: zh -->

[英文文档](./FEATURES.md)

## 中文

这个矩阵是 `gopact-ext` 的可执行能力契约。CI 默认运行 mock 测试，确保 extension 行为可重复、无外部依赖；真实 provider 测试通过 `integration` build tag 在本地手动执行。

| Capability | Path | Mock test | Local integration |
| --- | --- | --- | --- |
| agent as graph node | `agents/agentnode` | `(cd agents/agentnode && go test -count=1 ./...)` | - |
| agent as tool | `agents/agenttool` | `(cd agents/agenttool && go test -count=1 ./...)` | - |
| human review approval gate with checkpoint and step-export resume | `agents/humanreview` | `(cd agents/humanreview && go test -count=1 ./...)` | - |
| Plan-Execute agent template with replan, approval, checkpoint, and cancel | `agents/planexec` | `(cd agents/planexec && go test -count=1 ./...)` | - |
| Plan-Execute golden trajectory | `agents/planexec` | `(cd agents/planexec && go test -count=1 ./...)` | - |
| ReAct agent template | `agents/react` | `(cd agents/react && go test -count=1 ./...)` | - |
| ReAct verification export process records and step-export resume | `agents/react` | `(cd agents/react && go test -count=1 ./...)` | - |
| durable background scheduler with queue transitions, lease renewal, retry, drain, and evidence | `agents/scheduler` | `(cd agents/scheduler && go test -count=1 ./...)` | - |
| Supervisor agent template | `agents/supervisor` | `(cd agents/supervisor && go test -count=1 ./...)` | - |
| ReAct tool loop with model options and runtime IDs | `tests/agents` | `(cd tests/agents && go test -count=1 ./...)` | - |
| ReAct checkpoint resume with tool, memory, and verification | `tests/agents` | `(cd tests/agents && go test -count=1 ./...)` | `(cd tests/agents && go test -tags=integration -count=1 ./...)` |
| Plan-Execute model planner and executor with request options | `tests/agents` | `(cd tests/agents && go test -count=1 ./...)` | `(cd tests/agents && go test -tags=integration -count=1 ./...)` |
| Plan-Execute approval checkpoint resume | `tests/agents` | `(cd tests/agents && go test -count=1 ./...)` | - |
| Agent-as-Tool A2A delegation success and failure evidence | `tests/agents` | `(cd tests/agents && go test -count=1 ./...)` | `(cd tests/agents && go test -tags=integration -count=1 ./...)` |
| Supervisor routing to Plan-Execute child with runtime IDs | `tests/agents` | `(cd tests/agents && go test -count=1 ./...)` | `(cd tests/agents && go test -tags=integration -count=1 ./...)` |
| A2A agent node inside graph workflow | `tests/agents` | `(cd tests/agents && go test -count=1 ./...)` | - |
| Human review gate inside graph workflow | `tests/agents` | `(cd tests/agents && go test -count=1 ./...)` | - |
| file snapshot evidence | `devagent/filesnapshot` | `(cd devagent/filesnapshot && go test -count=1 ./...)` | - |
| git diff evidence | `devagent/gitdiff` | `(cd devagent/gitdiff && go test -count=1 ./...)` | - |
| self-bootstrap Dev Agent workflow with analyze, plan patch proposal policy, write, test, review, failure attribution, and verification report evidence | `devagent/selfbootstrap` | `(cd devagent/selfbootstrap && go test -count=1 ./...)` | - |
| local workspace adapter for self-bootstrap policy-approved plan patch apply, controlled patch apply, diff, file snapshot, command, and CI gate evidence | `devagent/workspace` | `(cd devagent/workspace && go test -count=1 ./...)` | - |
| OpenAI provider | `models/openai` | `(cd models/openai && go test -count=1 ./...)` | `(cd models/openai && GOWORK=off go test -tags=integration -count=1 ./...)` |
| Ark provider | `models/ark` | `(cd models/ark && go test -count=1 ./...)` | `(cd models/ark && GOWORK=off go test -tags=integration -count=1 ./...)` |
| Agnes provider | `models/agnes` | `(cd models/agnes && go test -count=1 ./...)` | `(cd models/agnes && go test -tags=integration -count=1 ./...)` |
| Agnes provider streaming | `models/agnes` | `(cd models/agnes && go test -count=1 ./...)` | `(cd models/agnes && go test -tags=integration -count=1 ./...)` |
| Agnes provider tool calling | `models/agnes` | `(cd models/agnes && go test -count=1 ./...)` | `(cd models/agnes && go test -tags=integration -count=1 ./...)` |
| Agnes provider structured output | `models/agnes` | `(cd models/agnes && go test -count=1 ./...)` | `(cd models/agnes && go test -tags=integration -count=1 ./...)` |
| Agnes provider thinking toggle | `models/agnes` | `(cd models/agnes && go test -count=1 ./...)` | `(cd models/agnes && go test -tags=integration -count=1 ./...)` |
| Agnes provider error classification | `models/agnes` | `(cd models/agnes && go test -count=1 ./...)` | `(cd models/agnes && go test -tags=integration -count=1 ./...)` |
| Agnes provider cancel and timeout | `models/agnes` | `(cd models/agnes && go test -count=1 ./...)` | `(cd models/agnes && go test -tags=integration -count=1 ./...)` |
| Agnes-backed agent templates | `tests/agents` | `(cd tests/agents && go test -count=1 ./...)` | `(cd tests/agents && go test -tags=integration -count=1 ./...)` |
| Agnes-backed ReAct, Plan-Execute, Agent-as-Tool, Supervisor, and AgentNode templates | `tests/agents` | `(cd tests/agents && go test -count=1 ./...)` | `(cd tests/agents && go test -tags=integration -count=1 ./...)` |

能力说明：

- provider adapter 必须覆盖默认 model、per-call model override、参数预算、采样参数、stream、tool calling、structured output、thinking/reasoning 控制、超时/取消和错误分类。
- agent template 必须覆盖成功路径、失败路径、组合路径和可恢复边界；涉及人工审批、checkpoint、memory、verification 的能力必须有具体测试固化。
- dev-agent helper 必须通过显式 host-controlled workspace action 暴露写入能力并采集证据，不替调用方做发布决策。
