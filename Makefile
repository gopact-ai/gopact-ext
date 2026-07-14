MODULES := models/openai models/agnes models/fake models/glm agents/internal agents/react agents/sequential agents/parallel agents/loop agents/agenttool agents/router agents/planexec agents/supervisor agents/deep agents/deepresearch stores/dbstore stores/mariadb stores/mysql stores/postgres stores/sqlite

.PHONY: test integration capability fmt-check tidy race vet security benchmark

test:
	GOTOOLCHAIN=local go test ./models/openai/... ./models/agnes/... ./models/fake/... ./models/glm/... ./agents/internal/... ./agents/react/... ./agents/sequential/... ./agents/parallel/... ./agents/loop/... ./agents/agenttool/... ./agents/router/... ./agents/planexec/... ./agents/supervisor/... ./agents/deep/... ./agents/deepresearch/... ./stores/dbstore/... ./stores/mariadb/... ./stores/mysql/... ./stores/postgres/... ./stores/sqlite/... ./tests/workflow/...

integration:
	@set -a; [ ! -f .env ] || . ./.env; set +a; \
	test -n "$$AGNES_API_KEY" || { echo "AGNES_API_KEY missing in .env"; exit 1; }; \
	test -n "$$GLM_API_KEY" || { echo "GLM_API_KEY missing in .env"; exit 1; }; \
	GOTOOLCHAIN=local go test -tags=integration -count=1 -v ./models/agnes/... ./models/glm/... ./tests/workflow/...

capability: test integration race vet security

fmt-check:
	test -z "$$(gofmt -l .)"

tidy:
	@set -e; for dir in $(MODULES) tests/workflow; do \
		(cd $$dir && GOWORK=off GOTOOLCHAIN=local go mod tidy -diff); \
	done

race:
	GOTOOLCHAIN=local go test -race ./models/openai/... ./models/agnes/... ./models/fake/... ./models/glm/... ./agents/internal/... ./agents/react/... ./agents/sequential/... ./agents/parallel/... ./agents/loop/... ./agents/agenttool/... ./agents/router/... ./agents/planexec/... ./agents/supervisor/... ./agents/deep/... ./agents/deepresearch/... ./stores/dbstore/... ./stores/mariadb/... ./stores/mysql/... ./stores/postgres/... ./stores/sqlite/... ./tests/workflow/...

vet:
	GOTOOLCHAIN=local go vet ./models/openai/... ./models/agnes/... ./models/fake/... ./models/glm/... ./agents/internal/... ./agents/react/... ./agents/sequential/... ./agents/parallel/... ./agents/loop/... ./agents/agenttool/... ./agents/router/... ./agents/planexec/... ./agents/supervisor/... ./agents/deep/... ./agents/deepresearch/... ./stores/dbstore/... ./stores/mariadb/... ./stores/mysql/... ./stores/postgres/... ./stores/sqlite/... ./tests/workflow/...

security:
	@command -v govulncheck >/dev/null 2>&1 || { echo "govulncheck not found; install golang.org/x/vuln/cmd/govulncheck@v1.5.0"; exit 1; }
	@set -e; for dir in $(MODULES); do \
		echo "govulncheck $$dir"; \
		(cd $$dir && GOTOOLCHAIN=local govulncheck ./...); \
	done

benchmark:
	GOTOOLCHAIN=local go test -bench=. -run='^$$' ./models/openai/...
