package agents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepositoryCIMockGateIsDocumented(t *testing.T) {
	workflow := readRepoText(t, "../../.github/workflows/ci.yml")
	readme := readRepoText(t, "../../README.md")

	for _, command := range []string{
		"git diff --check",
		"go mod tidy",
		"git diff --exit-code",
		"go test -count=1 ./...",
		"go test -race -count=1 ./...",
		"go vet ./...",
		"golangci-lint run ./...",
		"go test -coverprofile=coverage.out ./...",
		"govulncheck ./...",
	} {
		if !strings.Contains(workflow, command) {
			t.Fatalf("workflow missing mock CI command %q", command)
		}
		if !strings.Contains(readme, command) {
			t.Fatalf("README missing mock CI command %q", command)
		}
	}

	for _, forbidden := range []string{"-tags=integration", ".env"} {
		if strings.Contains(workflow, forbidden) {
			t.Fatalf("workflow contains %q; ext CI must stay mock-only", forbidden)
		}
	}
}

func TestRepositoryIntegrationCommandsRunInsideModules(t *testing.T) {
	readme := readRepoText(t, "../../README.md")

	for _, command := range []string{
		"(cd models/openai && GOWORK=off go test -tags=integration -count=1 ./...)",
		"(cd models/ark && GOWORK=off go test -tags=integration -count=1 ./...)",
		"(cd models/agnes && go test -tags=integration -count=1 ./...)",
		"(cd tests/agents && go test -tags=integration -count=1 ./...)",
	} {
		if !strings.Contains(readme, command) {
			t.Fatalf("README missing runnable integration command %q", command)
		}
	}

	for _, command := range []string{
		"GOWORK=off go test -tags=integration -count=1 ./models/openai/...",
		"GOWORK=off go test -tags=integration -count=1 ./models/ark/...",
		"GOWORK=off go test -tags=integration -count=1 ./models/agnes/...",
		"(cd models/agnes && GOWORK=off go test -tags=integration -count=1 ./...)",
	} {
		if strings.Contains(readme, command) {
			t.Fatalf("README contains unsupported integration command %q", command)
		}
	}
}

func TestRepositoryModulesUseCurrentCoreSDK(t *testing.T) {
	const currentCoreSDK = "github.com/gopact-ai/gopact v0.0.7"

	for _, module := range []string{
		"agents/agenttool",
		"agents/planexec",
		"agents/react",
		"models/agnes",
		"models/ark",
		"models/openai",
		"tests/agents",
	} {
		goMod := readRepoText(t, filepath.Join("../..", module, "go.mod"))
		if !strings.Contains(goMod, currentCoreSDK) {
			t.Fatalf("%s/go.mod must require %s", module, currentCoreSDK)
		}
	}
}

func readRepoText(t *testing.T, path string) string {
	t.Helper()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}
