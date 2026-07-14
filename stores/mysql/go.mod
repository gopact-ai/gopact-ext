module github.com/gopact-ai/gopact-ext/stores/mysql

go 1.27

require (
	github.com/gopact-ai/gopact-ext/stores/dbstore v0.0.0
	gorm.io/driver/mysql v1.6.0
)

require (
	filippo.io/edwards25519 v1.2.0 // indirect
	github.com/go-sql-driver/mysql v1.10.0 // indirect
	github.com/gopact-ai/gopact v0.0.0 // indirect
	github.com/jinzhu/inflection v1.0.0 // indirect
	github.com/jinzhu/now v1.1.5 // indirect
	golang.org/x/text v0.29.0 // indirect
	gorm.io/gorm v1.31.2 // indirect
)

replace github.com/gopact-ai/gopact-ext/stores/dbstore => ../dbstore

replace github.com/gopact-ai/gopact => ../../../gopact
