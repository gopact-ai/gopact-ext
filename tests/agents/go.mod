module github.com/gopact-ai/gopact-ext/tests/agents

go 1.25.11

require (
	github.com/gopact-ai/gopact v0.0.38
	github.com/gopact-ai/gopact-ext/agents/agenttool v0.1.17
	github.com/gopact-ai/gopact-ext/agents/planexec v0.2.18
	github.com/gopact-ai/gopact-ext/agents/react v0.2.16
	github.com/gopact-ai/gopact-ext/agents/supervisor v0.1.4
	github.com/gopact-ai/gopact-ext/models/agnes v0.1.19
	golang.org/x/mod v0.37.0
)

require github.com/gopact-ai/gopact-ext/models/openai v0.5.18 // indirect

replace github.com/gopact-ai/gopact-ext/agents/supervisor => ../../agents/supervisor
