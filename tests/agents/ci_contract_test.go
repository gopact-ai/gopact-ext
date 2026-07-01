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
	const currentCoreSDK = "github.com/gopact-ai/gopact v0.0.25"

	for _, module := range []string{
		"agents/agenttool",
		"agents/planexec",
		"agents/react",
		"devagent/filesnapshot",
		"devagent/gitdiff",
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

func TestRepositoryDocumentsCurrentExtensionTags(t *testing.T) {
	readme := readRepoText(t, "../../README.md")
	agentsGoMod := readRepoText(t, "go.mod")

	for _, requirement := range []string{
		"github.com/gopact-ai/gopact-ext/agents/agenttool v0.1.7",
		"github.com/gopact-ai/gopact-ext/agents/planexec v0.2.7",
		"github.com/gopact-ai/gopact-ext/agents/react v0.2.7",
		"github.com/gopact-ai/gopact-ext/models/agnes v0.1.7",
	} {
		if !strings.Contains(agentsGoMod, requirement) {
			t.Fatalf("tests/agents/go.mod missing current released module %q", requirement)
		}
	}

	for _, install := range []string{
		"go get github.com/gopact-ai/gopact-ext/agents/agenttool@v0.1.7",
		"go get github.com/gopact-ai/gopact-ext/agents/planexec@v0.2.7",
		"go get github.com/gopact-ai/gopact-ext/agents/react@v0.2.7",
		"go get github.com/gopact-ai/gopact-ext/devagent/filesnapshot@v0.1.6",
		"go get github.com/gopact-ai/gopact-ext/devagent/gitdiff@v0.1.6",
		"go get github.com/gopact-ai/gopact-ext/models/openai@v0.5.9",
		"go get github.com/gopact-ai/gopact-ext/models/ark@v0.2.7",
		"go get github.com/gopact-ai/gopact-ext/models/agnes@v0.1.7",
	} {
		if !strings.Contains(readme, install) {
			t.Fatalf("README missing install command %q", install)
		}
	}
}

func TestModuleReadmesDocumentCurrentExtensionTags(t *testing.T) {
	for path, install := range map[string]string{
		"agents/planexec/README.md":       "go get github.com/gopact-ai/gopact-ext/agents/planexec@v0.2.7",
		"agents/react/README.md":          "go get github.com/gopact-ai/gopact-ext/agents/react@v0.2.7",
		"devagent/filesnapshot/README.md": "go get github.com/gopact-ai/gopact-ext/devagent/filesnapshot@v0.1.6",
		"devagent/gitdiff/README.md":      "go get github.com/gopact-ai/gopact-ext/devagent/gitdiff@v0.1.6",
		"models/agnes/README.md":          "go get github.com/gopact-ai/gopact-ext/models/agnes@v0.1.7",
		"models/ark/README.md":            "go get github.com/gopact-ai/gopact-ext/models/ark@v0.2.7",
		"models/openai/README.md":         "go get github.com/gopact-ai/gopact-ext/models/openai@v0.5.9",
	} {
		if !strings.Contains(readRepoText(t, "../../"+path), install) {
			t.Fatalf("%s missing install command %q", path, install)
		}
	}
}

func TestFeatureCoverageMatrixDocumentsExtensionCapabilities(t *testing.T) {
	matrix := readRepoText(t, "../../FEATURES.md")
	readme := readRepoText(t, "../../README.md")
	if !strings.Contains(readme, "FEATURES.md") {
		t.Fatal("README must link to FEATURES.md")
	}

	tests := []struct {
		capability         string
		path               string
		mockCommand        string
		integrationCommand string
	}{
		{"agent as tool", "agents/agenttool", "(cd agents/agenttool && go test -count=1 ./...)", ""},
		{"Plan-Execute agent template with approval, checkpoint, and cancel", "agents/planexec", "(cd agents/planexec && go test -count=1 ./...)", ""},
		{"ReAct agent template", "agents/react", "(cd agents/react && go test -count=1 ./...)", ""},
		{"file snapshot evidence", "devagent/filesnapshot", "(cd devagent/filesnapshot && go test -count=1 ./...)", ""},
		{"git diff evidence", "devagent/gitdiff", "(cd devagent/gitdiff && go test -count=1 ./...)", ""},
		{"OpenAI provider", "models/openai", "(cd models/openai && go test -count=1 ./...)", "(cd models/openai && GOWORK=off go test -tags=integration -count=1 ./...)"},
		{"Ark provider", "models/ark", "(cd models/ark && go test -count=1 ./...)", "(cd models/ark && GOWORK=off go test -tags=integration -count=1 ./...)"},
		{"Agnes provider", "models/agnes", "(cd models/agnes && go test -count=1 ./...)", "(cd models/agnes && go test -tags=integration -count=1 ./...)"},
		{"Agnes-backed agent templates", "tests/agents", "(cd tests/agents && go test -count=1 ./...)", "(cd tests/agents && go test -tags=integration -count=1 ./...)"},
	}

	for _, tt := range tests {
		t.Run(tt.capability, func(t *testing.T) {
			for _, want := range []string{tt.capability, tt.path, tt.mockCommand} {
				if !strings.Contains(matrix, want) {
					t.Fatalf("FEATURES.md missing %q", want)
				}
			}
			if tt.integrationCommand != "" && !strings.Contains(matrix, tt.integrationCommand) {
				t.Fatalf("FEATURES.md missing integration command %q", tt.integrationCommand)
			}
			assertTestedModule(t, tt.path)
		})
	}
}

func assertTestedModule(t *testing.T, path string) {
	t.Helper()

	entries, err := os.ReadDir(filepath.Join("../..", path))
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), "_test.go") {
			return
		}
	}
	t.Fatalf("%s missing *_test.go", path)
}

func readRepoText(t *testing.T, path string) string {
	t.Helper()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}
