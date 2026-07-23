# Every extension domain is independently versioned. TIDY_MODULES grows only
# after a module's same-repository dependencies are available from the proxy.
WORKSPACE_MODULES := \
	agents/agenttool \
	agents/internal \
	agents/loop \
	agents/parallel \
	agents/planexec \
	agents/react \
	agents/router \
	agents/sequential \
	agents/supervisor \
	middleware/byted/fornax \
	models/agnes \
	models/fake \
	models/glm \
	models/openai \
	stores \
	tests/workflow
TIDY_MODULES := \
	. \
	agents/internal \
	agents/agenttool \
	agents/loop \
	agents/parallel \
	agents/planexec \
	agents/react \
	agents/router \
	agents/sequential \
	agents/supervisor \
	middleware/byted/fornax \
	models/openai \
	stores
SECURITY_MODULES := . $(filter-out tests/workflow,$(WORKSPACE_MODULES))
WORKSPACE_PACKAGES := ./... $(addprefix ./,$(addsuffix /...,$(WORKSPACE_MODULES)))
# Advance only after the next manifest version is available from the public proxy.
PUBLISHED_PREFIX := 13

.PHONY: test integration capability fmt-check print-workspace-modules module-contract published tidy standalone race vet security dbintegration benchmark

test:
	GOTOOLCHAIN=local go test -count=1 $(WORKSPACE_PACKAGES)
	./scripts/clean-consumer_test.sh

integration:
	@set -a; [ ! -f .env ] || . ./.env; set +a; \
	test -n "$$AGNES_API_KEY" || { echo "AGNES_API_KEY missing in .env"; exit 1; }; \
	test -n "$$GLM_API_KEY" || { echo "GLM_API_KEY missing in .env"; exit 1; }; \
	GOTOOLCHAIN=local go test -tags=integration -count=1 -v ./models/agnes/... ./models/glm/... ./tests/workflow/...

capability: test integration race vet security

fmt-check:
	@set -e; files="$$(gofmt -l .)"; \
		test -z "$$files" || { \
			printf 'files need gofmt:\n%s\n' "$$files"; \
			exit 1; \
		}

print-workspace-modules:
	@printf '%s\n' $(WORKSPACE_MODULES)

module-contract:
	./scripts/module-contract_test.sh

published:
	GOENV=off GOPROXY=https://proxy.golang.org GOSUMDB=sum.golang.org \
		GOPRIVATE=none GONOPROXY=none GONOSUMDB=none \
		GO111MODULE=on GOFLAGS= GOWORK=off GOTOOLCHAIN=local \
		./scripts/clean-consumer.sh --prefix-count $(PUBLISHED_PREFIX) \
		./scripts/release-versions.txt

tidy:
	@set -e; for dir in $(TIDY_MODULES); do \
		(cd $$dir && GOWORK=off GOTOOLCHAIN=local go mod tidy -diff); \
	done

standalone:
	@set -e; for dir in $(TIDY_MODULES); do \
		echo "standalone $$dir"; \
		(cd $$dir && GOWORK=off GOTOOLCHAIN=local go test -race -count=1 ./...); \
		(cd $$dir && GOWORK=off GOTOOLCHAIN=local go vet ./...); \
	done

race:
	GOTOOLCHAIN=local go test -race -count=1 $(WORKSPACE_PACKAGES)

vet:
	GOTOOLCHAIN=local go vet $(WORKSPACE_PACKAGES)

security:
	@command -v govulncheck >/dev/null 2>&1 || { echo "govulncheck not found; install golang.org/x/vuln/cmd/govulncheck@v1.5.0"; exit 1; }
	@set -e; for dir in $(SECURITY_MODULES); do \
		echo "govulncheck $$dir"; \
		(cd $$dir && GOTOOLCHAIN=local govulncheck ./...); \
	done

dbintegration:
	GOTOOLCHAIN=local go test -tags=dbintegration -count=1 ./stores/dbstore

benchmark:
	GOTOOLCHAIN=local go test -bench=. -run='^$$' ./models/openai/...
