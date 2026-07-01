# Feature Coverage

This matrix is the extension repository contract for expected runnable capabilities. CI uses mock tests; provider-backed checks stay local unless explicitly run with integration tags.

| Capability | Path | Mock test | Local integration |
| --- | --- | --- | --- |
| agent as tool | `agents/agenttool` | `(cd agents/agenttool && go test -count=1 ./...)` | - |
| Plan-Execute agent template with approval, checkpoint, and cancel | `agents/planexec` | `(cd agents/planexec && go test -count=1 ./...)` | - |
| Plan-Execute golden trajectory | `agents/planexec` | `(cd agents/planexec && go test -count=1 ./...)` | - |
| ReAct agent template | `agents/react` | `(cd agents/react && go test -count=1 ./...)` | - |
| file snapshot evidence | `devagent/filesnapshot` | `(cd devagent/filesnapshot && go test -count=1 ./...)` | - |
| git diff evidence | `devagent/gitdiff` | `(cd devagent/gitdiff && go test -count=1 ./...)` | - |
| OpenAI provider | `models/openai` | `(cd models/openai && go test -count=1 ./...)` | `(cd models/openai && GOWORK=off go test -tags=integration -count=1 ./...)` |
| Ark provider | `models/ark` | `(cd models/ark && go test -count=1 ./...)` | `(cd models/ark && GOWORK=off go test -tags=integration -count=1 ./...)` |
| Agnes provider | `models/agnes` | `(cd models/agnes && go test -count=1 ./...)` | `(cd models/agnes && go test -tags=integration -count=1 ./...)` |
| Agnes-backed agent templates | `tests/agents` | `(cd tests/agents && go test -count=1 ./...)` | `(cd tests/agents && go test -tags=integration -count=1 ./...)` |
