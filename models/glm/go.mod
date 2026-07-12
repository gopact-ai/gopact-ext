module github.com/gopact-ai/gopact-ext/models/glm

go 1.27

require (
	github.com/gopact-ai/gopact v0.0.0
	github.com/gopact-ai/gopact-ext/models/openai v0.0.0
)

replace github.com/gopact-ai/gopact => ../../../gopact

replace github.com/gopact-ai/gopact-ext/models/openai => ../openai
