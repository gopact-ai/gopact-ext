PUBLIC_MODULES := . stores
WORKSPACE_PACKAGES := ./... ./stores/... ./tests/workflow/...

.PHONY: test integration capability fmt-check tidy race vet security dbintegration benchmark

test:
	GOTOOLCHAIN=local go test -count=1 $(WORKSPACE_PACKAGES)

integration:
	@set -a; [ ! -f .env ] || . ./.env; set +a; \
	test -n "$$AGNES_API_KEY" || { echo "AGNES_API_KEY missing in .env"; exit 1; }; \
	test -n "$$GLM_API_KEY" || { echo "GLM_API_KEY missing in .env"; exit 1; }; \
	GOTOOLCHAIN=local go test -tags=integration -count=1 -v ./models/agnes/... ./models/glm/... ./tests/workflow/...

capability: test integration race vet security

fmt-check:
	test -z "$$(gofmt -l .)"

tidy:
	@set -e; for dir in $(PUBLIC_MODULES); do \
		(cd $$dir && GOWORK=off GOTOOLCHAIN=local go mod tidy -diff); \
	done

race:
	GOTOOLCHAIN=local go test -race -count=1 $(WORKSPACE_PACKAGES)

vet:
	GOTOOLCHAIN=local go vet $(WORKSPACE_PACKAGES)

security:
	@command -v govulncheck >/dev/null 2>&1 || { echo "govulncheck not found; install golang.org/x/vuln/cmd/govulncheck@v1.5.0"; exit 1; }
	@set -e; for dir in $(PUBLIC_MODULES); do \
		echo "govulncheck $$dir"; \
		(cd $$dir && GOTOOLCHAIN=local govulncheck ./...); \
	done

dbintegration:
	GOTOOLCHAIN=local go test -tags=dbintegration -count=1 ./stores/dbstore

benchmark:
	GOTOOLCHAIN=local go test -bench=. -run='^$$' ./models/openai/...
