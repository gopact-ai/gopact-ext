module github.com/gopact-ai/gopact-ext/agents/react

go 1.27

require (
	github.com/gopact-ai/gopact v0.0.0
	github.com/gopact-ai/gopact-ext/agents/agenttool v0.0.0
	github.com/gopact-ai/gopact-ext/agents/internal v0.0.0
)

replace github.com/gopact-ai/gopact => ../../../gopact

replace github.com/gopact-ai/gopact-ext/agents/agenttool => ../agenttool

replace github.com/gopact-ai/gopact-ext/agents/internal => ../internal
