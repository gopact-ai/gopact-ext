module github.com/gopact-ai/gopact-ext/tests/workflow

go 1.27

require (
	github.com/gopact-ai/gopact v0.0.0
	github.com/gopact-ai/gopact-ext/agents/deep v0.0.0
	github.com/gopact-ai/gopact-ext/agents/deepresearch v0.0.0
	github.com/gopact-ai/gopact-ext/agents/loop v0.0.0
	github.com/gopact-ai/gopact-ext/agents/parallel v0.0.0
	github.com/gopact-ai/gopact-ext/agents/planexec v0.0.0
	github.com/gopact-ai/gopact-ext/agents/react v0.0.0
	github.com/gopact-ai/gopact-ext/agents/router v0.0.0
	github.com/gopact-ai/gopact-ext/agents/sequential v0.0.0
	github.com/gopact-ai/gopact-ext/agents/supervisor v0.0.0
	github.com/gopact-ai/gopact-ext/models/agnes v0.0.0
	github.com/gopact-ai/gopact-ext/models/glm v0.0.0
	github.com/gopact-ai/gopact-ext/stores/sqlite v0.0.0
)

require (
	filippo.io/edwards25519 v1.2.0 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/go-sql-driver/mysql v1.10.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/gopact-ai/gopact-ext/agents/internal v0.0.0 // indirect
	github.com/gopact-ai/gopact-ext/models/openai v0.0.0 // indirect
	github.com/gopact-ai/gopact-ext/stores/dbstore v0.0.0 // indirect
	github.com/jinzhu/inflection v1.0.0 // indirect
	github.com/jinzhu/now v1.1.5 // indirect
	github.com/libtnb/sqlite v1.2.0 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	golang.org/x/sys v0.44.0 // indirect
	golang.org/x/text v0.29.0 // indirect
	gorm.io/gorm v1.31.2 // indirect
	modernc.org/libc v1.73.4 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
	modernc.org/sqlite v1.53.0 // indirect
)

replace github.com/gopact-ai/gopact => ../../../gopact

replace github.com/gopact-ai/gopact-ext/agents/deep => ../../agents/deep

replace github.com/gopact-ai/gopact-ext/agents/deepresearch => ../../agents/deepresearch

replace github.com/gopact-ai/gopact-ext/agents/agenttool => ../../agents/agenttool

replace github.com/gopact-ai/gopact-ext/agents/loop => ../../agents/loop

replace github.com/gopact-ai/gopact-ext/agents/parallel => ../../agents/parallel

replace github.com/gopact-ai/gopact-ext/agents/planexec => ../../agents/planexec

replace github.com/gopact-ai/gopact-ext/agents/react => ../../agents/react

replace github.com/gopact-ai/gopact-ext/agents/router => ../../agents/router

replace github.com/gopact-ai/gopact-ext/agents/sequential => ../../agents/sequential

replace github.com/gopact-ai/gopact-ext/agents/supervisor => ../../agents/supervisor

replace github.com/gopact-ai/gopact-ext/agents/internal => ../../agents/internal

replace github.com/gopact-ai/gopact-ext/models/agnes => ../../models/agnes

replace github.com/gopact-ai/gopact-ext/models/glm => ../../models/glm

replace github.com/gopact-ai/gopact-ext/models/openai => ../../models/openai

replace github.com/gopact-ai/gopact-ext/stores/sqlite => ../../stores/sqlite

replace github.com/gopact-ai/gopact-ext/stores/dbstore => ../../stores/dbstore
