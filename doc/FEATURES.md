# Feature Coverage

<!-- gopact:doc-language: en -->

Chinese documentation: [FEATURES_zh.md](FEATURES_zh.md)

This matrix is the executable capability contract for `gopact-ext`. CI runs mock tests only so the repository stays deterministic and independent of provider credentials. Provider-backed checks are local opt-in tests behind the `integration` build tag.

| Capability | Module | Mock command | Integration command |
| --- | --- | --- | --- |
| agent as graph node | `agents/agentnode` | `(cd agents/agentnode && go test -count=1 ./...)` | Not required |
| agent as tool | `agents/agenttool` | `(cd agents/agenttool && go test -count=1 ./...)` | Not required |
| human review approval gate with checkpoint and step-export resume | `agents/humanreview` | `(cd agents/humanreview && go test -count=1 ./...)` | Not required |
| Plan-Execute agent template with replan, approval, checkpoint, and cancel | `agents/planexec` | `(cd agents/planexec && go test -count=1 ./...)` | Not required |
| Plan-Execute golden trajectory | `agents/planexec` | `(cd agents/planexec && go test -count=1 ./...)` | Not required |
| ReAct agent template | `agents/react` | `(cd agents/react && go test -count=1 ./...)` | Not required |
| ReAct verification export process records and step-export resume | `agents/react` | `(cd agents/react && go test -count=1 ./...)` | Not required |
| Supervisor agent template | `agents/supervisor` | `(cd agents/supervisor && go test -count=1 ./...)` | Not required |
| ReAct tool loop with model options and runtime IDs | `tests/agents` | `(cd tests/agents && go test -count=1 ./...)` | Not required |
| ReAct checkpoint resume with tool, memory, and verification | `tests/agents` | `(cd tests/agents && go test -count=1 ./...)` | `(cd tests/agents && go test -tags=integration -count=1 ./...)` |
| Plan-Execute model planner and executor with request options | `tests/agents` | `(cd tests/agents && go test -count=1 ./...)` | `(cd tests/agents && go test -tags=integration -count=1 ./...)` |
| Plan-Execute approval checkpoint resume | `tests/agents` | `(cd tests/agents && go test -count=1 ./...)` | Not required |
| Agent-as-Tool A2A delegation success and failure evidence | `tests/agents` | `(cd tests/agents && go test -count=1 ./...)` | `(cd tests/agents && go test -tags=integration -count=1 ./...)` |
| Supervisor routing to Plan-Execute child with runtime IDs | `tests/agents` | `(cd tests/agents && go test -count=1 ./...)` | `(cd tests/agents && go test -tags=integration -count=1 ./...)` |
| A2A agent node inside graph workflow | `tests/agents` | `(cd tests/agents && go test -count=1 ./...)` | Not required |
| Human review gate inside graph workflow | `tests/agents` | `(cd tests/agents && go test -count=1 ./...)` | Not required |
| file snapshot evidence | `devagent/filesnapshot` | `(cd devagent/filesnapshot && go test -count=1 ./...)` | Not required |
| git diff evidence | `devagent/gitdiff` | `(cd devagent/gitdiff && go test -count=1 ./...)` | Not required |
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

Provider adapters must cover default and per-call model selection, request budgets, sampling controls, streaming, tool calling, structured output, thinking or reasoning controls, timeout and cancel behavior, and error classification. Agent templates must cover success paths, failure paths, composition paths, and resumable boundaries. Development-agent helpers collect evidence only; release decisions remain with the caller.
