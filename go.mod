module github.com/gopact-ai/gopact-ext

go 1.27

require (
	github.com/gopact-ai/gopact v0.0.0-20260716101400-d2f2a74a3ced // rewritten equivalent of the removed v0.1.0-rc.3 tag
	github.com/gopact-ai/gopact-ext/models/openai v0.6.0-rc.2
)

exclude (
	github.com/gopact-ai/gopact v0.1.0-rc.2
	github.com/gopact-ai/gopact v0.1.0-rc.3
)
