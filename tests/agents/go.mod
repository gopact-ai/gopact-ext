module github.com/gopact-ai/gopact-ext/tests/agents

go 1.25.11

require (
	github.com/gopact-ai/gopact v0.0.39
	github.com/gopact-ai/gopact-ext/agents/agenttool v0.1.18
	github.com/gopact-ai/gopact-ext/agents/planexec v0.2.19
	github.com/gopact-ai/gopact-ext/agents/react v0.2.17
	github.com/gopact-ai/gopact-ext/agents/supervisor v0.1.5
	github.com/gopact-ai/gopact-ext/models/agnes v0.1.20
	golang.org/x/mod v0.37.0
)

require github.com/gopact-ai/gopact-ext/models/openai v0.5.19 // indirect

replace github.com/gopact-ai/gopact-ext/agents/supervisor => ../../agents/supervisor
