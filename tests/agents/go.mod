module github.com/gopact-ai/gopact-ext/tests/agents

go 1.25.11

require (
	github.com/gopact-ai/gopact v0.0.50
	github.com/gopact-ai/gopact-ext/agents/agentnode v0.1.6
	github.com/gopact-ai/gopact-ext/agents/agenttool v0.1.25
	github.com/gopact-ai/gopact-ext/agents/humanreview v0.1.3
	github.com/gopact-ai/gopact-ext/agents/planexec v0.2.26
	github.com/gopact-ai/gopact-ext/agents/react v0.2.24
	github.com/gopact-ai/gopact-ext/agents/supervisor v0.1.12
	github.com/gopact-ai/gopact-ext/models/agnes v0.1.27
	golang.org/x/mod v0.37.0
)

require github.com/gopact-ai/gopact-ext/models/openai v0.5.26 // indirect

replace (
	github.com/gopact-ai/gopact-ext/agents/agentnode => ../../agents/agentnode
	github.com/gopact-ai/gopact-ext/agents/agenttool => ../../agents/agenttool
	github.com/gopact-ai/gopact-ext/agents/humanreview => ../../agents/humanreview
	github.com/gopact-ai/gopact-ext/agents/planexec => ../../agents/planexec
	github.com/gopact-ai/gopact-ext/agents/react => ../../agents/react
	github.com/gopact-ai/gopact-ext/agents/supervisor => ../../agents/supervisor
	github.com/gopact-ai/gopact-ext/models/agnes => ../../models/agnes
	github.com/gopact-ai/gopact-ext/models/openai => ../../models/openai
)
