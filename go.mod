module github.com/gopact-ai/gopact-ext

go 1.27

require (
	github.com/gopact-ai/gopact v0.0.0-20260715175854-19c7fe707d87 // rewritten equivalent of the removed v0.1.0-rc.3 tag
	github.com/gopact-ai/gopact-ext/models/openai v0.6.0-rc.2
)

exclude (
	github.com/gopact-ai/gopact v0.1.0-rc.2
	github.com/gopact-ai/gopact v0.1.0-rc.3
)
