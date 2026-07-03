package agents

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/semver"
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
	for _, action := range []string{"actions/checkout@v7", "actions/setup-go@v6"} {
		if !strings.Contains(workflow, action) {
			t.Fatalf("workflow missing current GitHub Action %q", action)
		}
	}

	for _, forbidden := range []string{"-tags=integration", ".env"} {
		if strings.Contains(workflow, forbidden) {
			t.Fatalf("workflow contains %q; ext CI must stay mock-only", forbidden)
		}
	}
}

func TestRepositoryOpenSourceGovernanceDocsArePresent(t *testing.T) {
	readme := readRepoText(t, "../../README.md")
	for _, doc := range []struct {
		path     string
		sections []string
	}{
		{
			path: "LICENSE",
			sections: []string{
				"MIT License",
				"Permission is hereby granted",
			},
		},
		{
			path: "doc/CONTRIBUTING.md",
			sections: []string{
				"# Contributing to gopact-ext",
				"## Development Setup",
				"## Verification",
				"## Pull Request Checklist",
			},
		},
		{
			path: "doc/SECURITY.md",
			sections: []string{
				"# Security Policy",
				"## Supported Versions",
				"## Reporting a Vulnerability",
			},
		},
		{
			path: "doc/CHANGELOG.md",
			sections: []string{
				"# Changelog",
				"## Unreleased",
			},
		},
		{
			path: "doc/maintainers/repository-governance.md",
			sections: []string{
				"# Repository Governance",
				"## Pull Request Flow",
				"## Admin Auto-Merge",
				"## Public Release Checks",
			},
		},
	} {
		body := readRepoText(t, "../../"+doc.path)
		for _, section := range doc.sections {
			if !strings.Contains(body, section) {
				t.Fatalf("%s missing section %q", doc.path, section)
			}
		}
		if !strings.Contains(readme, doc.path) {
			t.Fatalf("README missing governance doc link %q", doc.path)
		}
	}
}

func TestRepositoryPublicReadinessAndPRGovernanceAreConfigured(t *testing.T) {
	workflow := readRepoText(t, "../../.github/workflows/ci.yml")
	readiness := readRepoText(t, "../../scripts/public-readiness-check.sh")
	prGovernance := readRepoText(t, "../../.github/workflows/pr-governance.yml")
	adminAutomerge := readRepoText(t, "../../.github/workflows/admin-automerge.yml")
	governanceDoc := readRepoText(t, "../../doc/maintainers/repository-governance.md")

	for _, want := range []string{
		"permissions:",
		"contents: read",
		"fetch-depth: 0",
		"./scripts/public-readiness-check.sh",
	} {
		if !strings.Contains(workflow, want) {
			t.Fatalf("CI workflow missing public readiness control %q", want)
		}
	}
	for _, want := range []string{
		"git ls-files -- .env '.env.*'",
		"git rev-list --all",
		"commit message",
		"api-key-[0-9]{14,}",
		"sk-vx[[:alnum:]_-]{20,}",
		"ep-[0-9]{14}-[[:alnum:]_-]+",
	} {
		if !strings.Contains(readiness, want) {
			t.Fatalf("public readiness script missing %q", want)
		}
	}
	for _, want := range []string{
		"name: pr-governance",
		"pull_request_target:",
		"pull_request_review:",
		"author-policy",
		"collaborators/${author}/permission",
		"== \"APPROVED\"",
	} {
		if !strings.Contains(prGovernance, want) {
			t.Fatalf("PR governance workflow missing %q", want)
		}
	}
	for _, want := range []string{
		"name: admin-automerge",
		"pull_request_target:",
		"gh pr merge",
		"--auto",
		"--squash",
		"--delete-branch",
		"!= \"admin\"",
	} {
		if !strings.Contains(adminAutomerge, want) {
			t.Fatalf("admin automerge workflow missing %q", want)
		}
	}
	for _, want := range []string{
		"author-policy",
		"Admin-authored PRs",
		"Non-admin-authored PRs",
		"Do not configure a global required review count",
		"Require status checks to pass",
	} {
		if !strings.Contains(governanceDoc, want) {
			t.Fatalf("repository governance doc missing %q", want)
		}
	}
}

func TestArkProviderUsesPatchedProtobuf(t *testing.T) {
	goMod := readRepoText(t, "../../models/ark/go.mod")
	if err := checkGoModModuleAtLeast(goMod, "models/ark/go.mod", "google.golang.org/protobuf", "v1.33.0"); err != nil {
		t.Fatal(err)
	}
}

func TestGoModModuleVersionContractAcceptsFuturePatchedVersion(t *testing.T) {
	goMod := `module example.test

require (
	google.golang.org/protobuf v1.34.1
)
`
	if err := checkGoModModuleAtLeast(goMod, "test/go.mod", "google.golang.org/protobuf", "v1.33.0"); err != nil {
		t.Fatal(err)
	}
}

func TestGoModModuleVersionContractRejectsVulnerableRequire(t *testing.T) {
	goMod := `module example.test

require google.golang.org/protobuf v1.32.0
`
	if err := checkGoModModuleAtLeast(goMod, "test/go.mod", "google.golang.org/protobuf", "v1.33.0"); err == nil {
		t.Fatal("expected vulnerable protobuf require to fail")
	}
}

func TestGoModModuleVersionContractRejectsVulnerableReplace(t *testing.T) {
	goMod := `module example.test

require google.golang.org/protobuf v1.34.0

replace google.golang.org/protobuf => google.golang.org/protobuf v1.32.0
`
	if err := checkGoModModuleAtLeast(goMod, "test/go.mod", "google.golang.org/protobuf", "v1.33.0"); err == nil {
		t.Fatal("expected vulnerable protobuf replace to fail")
	}
}

func TestGoModModuleVersionContractRejectsDifferentReplacePath(t *testing.T) {
	goMod := `module example.test

require google.golang.org/protobuf v1.34.0

replace google.golang.org/protobuf => example.com/protobuf v1.34.0
`
	err := checkGoModModuleAtLeast(goMod, "test/go.mod", "google.golang.org/protobuf", "v1.33.0")
	if err == nil {
		t.Fatal("expected protobuf replace to a different module path to fail")
	}
	if !strings.Contains(err.Error(), "different module path") {
		t.Fatalf("error = %q, want different module path", err)
	}
}

func TestGoModModuleVersionContractIgnoresUnmatchedVersionReplace(t *testing.T) {
	goMod := `module example.test

require google.golang.org/protobuf v1.34.0

replace google.golang.org/protobuf v1.32.0 => example.com/protobuf v1.34.0
`
	if err := checkGoModModuleAtLeast(goMod, "test/go.mod", "google.golang.org/protobuf", "v1.33.0"); err != nil {
		t.Fatal(err)
	}
}

func TestRepositoryCIWorkflowOptimizesIndependentGatesForParallelFeedback(t *testing.T) {
	workflow := readRepoText(t, "../../.github/workflows/ci.yml")

	for _, want := range []string{
		"concurrency:",
		"group: ${{ github.workflow }}-${{ github.event.pull_request.number || github.ref }}",
		"cancel-in-progress: ${{ github.event_name == 'pull_request' }}",
		"hygiene:",
		"unit:",
		"race:",
		"static:",
		"coverage:",
		"security:",
		"needs: [hygiene, unit, race, static, coverage, security]",
	} {
		if !strings.Contains(workflow, want) {
			t.Fatalf("workflow missing parallel feedback control %q", want)
		}
	}
}

func TestRepositoryIntegrationCommandsRunInsideModules(t *testing.T) {
	readme := readRepoText(t, "../../README.md")
	script := readRepoText(t, "../../scripts/local-agnes-integration.sh")

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

	if !strings.Contains(readme, "./scripts/local-agnes-integration.sh") {
		t.Fatal("README missing local Agnes integration script")
	}
	for _, command := range []string{
		"(cd models/agnes && go test -tags=integration -count=1 ./...)",
		"(cd tests/agents && go test -tags=integration -count=1 ./...)",
	} {
		if !strings.Contains(script, command) {
			t.Fatalf("local Agnes integration script missing command %q", command)
		}
	}
	for _, forbidden := range []string{"models/openai", "models/ark"} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("local Agnes integration script must not run %s", forbidden)
		}
	}
}

func TestRepositoryEnvExampleDocumentsProviderCredentials(t *testing.T) {
	readme := readRepoText(t, "../../README.md")
	envExample := readRepoText(t, "../../.env.example")
	gitignore := readRepoText(t, "../../.gitignore")

	for _, key := range []string{
		"GOPACT_LLM_BASEURL",
		"GOPACT_LLM_TOKEN",
		"GOPACT_LLM_MODEL",
		"GOPACT_AGNES_API_KEY",
		"GOPACT_AGNES_SK",
		"GOPACT_ARK_API_KEY",
		"GOPACT_OPENAI_API_KEY",
	} {
		if !strings.Contains(readme, key) {
			t.Fatalf("README missing provider credential key %q", key)
		}
		if !strings.Contains(envExample, key) {
			t.Fatalf(".env.example missing provider credential key %q", key)
		}
	}
	if !strings.Contains(gitignore, ".env") {
		t.Fatal(".gitignore must keep .env local")
	}
}

func TestRepositoryModulesUseCurrentCoreSDK(t *testing.T) {
	const currentCoreSDK = "github.com/gopact-ai/gopact v0.0.42"

	for _, module := range []string{
		"agents/agentnode",
		"agents/agenttool",
		"agents/planexec",
		"agents/react",
		"agents/supervisor",
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

func TestAgnesProviderUsesCurrentOpenAIExtension(t *testing.T) {
	goMod := readRepoText(t, "../../models/agnes/go.mod")
	const currentOpenAIExtension = "github.com/gopact-ai/gopact-ext/models/openai v0.5.20"
	if !strings.Contains(goMod, currentOpenAIExtension) {
		t.Fatalf("models/agnes/go.mod must require %s", currentOpenAIExtension)
	}
}

func TestRepositoryDocumentsCurrentExtensionTags(t *testing.T) {
	readme := readRepoText(t, "../../README.md")
	agentsGoMod := readRepoText(t, "go.mod")

	for _, requirement := range []string{
		"github.com/gopact-ai/gopact-ext/agents/agentnode v0.1.0",
		"github.com/gopact-ai/gopact-ext/agents/agenttool v0.1.19",
		"github.com/gopact-ai/gopact-ext/agents/planexec v0.2.20",
		"github.com/gopact-ai/gopact-ext/agents/react v0.2.18",
		"github.com/gopact-ai/gopact-ext/agents/supervisor v0.1.6",
		"github.com/gopact-ai/gopact-ext/models/agnes v0.1.21",
	} {
		if !strings.Contains(agentsGoMod, requirement) {
			t.Fatalf("tests/agents/go.mod missing current released module %q", requirement)
		}
	}

	for _, install := range []string{
		"go get github.com/gopact-ai/gopact-ext/agents/agentnode@v0.1.0",
		"go get github.com/gopact-ai/gopact-ext/agents/agenttool@v0.1.19",
		"go get github.com/gopact-ai/gopact-ext/agents/planexec@v0.2.20",
		"go get github.com/gopact-ai/gopact-ext/agents/react@v0.2.18",
		"go get github.com/gopact-ai/gopact-ext/agents/supervisor@v0.1.6",
		"go get github.com/gopact-ai/gopact-ext/devagent/filesnapshot@v0.1.17",
		"go get github.com/gopact-ai/gopact-ext/devagent/gitdiff@v0.1.17",
		"go get github.com/gopact-ai/gopact-ext/models/openai@v0.5.20",
		"go get github.com/gopact-ai/gopact-ext/models/ark@v0.2.18",
		"go get github.com/gopact-ai/gopact-ext/models/agnes@v0.1.21",
	} {
		if !strings.Contains(readme, install) {
			t.Fatalf("README missing install command %q", install)
		}
	}
}

func TestModuleReadmesDocumentCurrentExtensionTags(t *testing.T) {
	for path, install := range map[string]string{
		"agents/agentnode/README.md":      "go get github.com/gopact-ai/gopact-ext/agents/agentnode@v0.1.0",
		"agents/planexec/README.md":       "go get github.com/gopact-ai/gopact-ext/agents/planexec@v0.2.20",
		"agents/react/README.md":          "go get github.com/gopact-ai/gopact-ext/agents/react@v0.2.18",
		"agents/supervisor/README.md":     "go get github.com/gopact-ai/gopact-ext/agents/supervisor@v0.1.6",
		"devagent/filesnapshot/README.md": "go get github.com/gopact-ai/gopact-ext/devagent/filesnapshot@v0.1.17",
		"devagent/gitdiff/README.md":      "go get github.com/gopact-ai/gopact-ext/devagent/gitdiff@v0.1.17",
		"models/agnes/README.md":          "go get github.com/gopact-ai/gopact-ext/models/agnes@v0.1.21",
		"models/ark/README.md":            "go get github.com/gopact-ai/gopact-ext/models/ark@v0.2.18",
		"models/openai/README.md":         "go get github.com/gopact-ai/gopact-ext/models/openai@v0.5.20",
	} {
		if !strings.Contains(readRepoText(t, "../../"+path), install) {
			t.Fatalf("%s missing install command %q", path, install)
		}
	}
}

func TestFeatureCoverageMatrixDocumentsExtensionCapabilities(t *testing.T) {
	matrix := readRepoText(t, "../../doc/FEATURES.md")
	readme := readRepoText(t, "../../README.md")
	if !strings.Contains(readme, "doc/FEATURES.md") {
		t.Fatal("README must link to FEATURES.md")
	}

	tests := []struct {
		capability         string
		path               string
		mockCommand        string
		integrationCommand string
	}{
		{"agent as graph node", "agents/agentnode", "(cd agents/agentnode && go test -count=1 ./...)", ""},
		{"agent as tool", "agents/agenttool", "(cd agents/agenttool && go test -count=1 ./...)", ""},
		{"Plan-Execute agent template with replan, approval, checkpoint, and cancel", "agents/planexec", "(cd agents/planexec && go test -count=1 ./...)", ""},
		{"Plan-Execute golden trajectory", "agents/planexec", "(cd agents/planexec && go test -count=1 ./...)", ""},
		{"ReAct agent template", "agents/react", "(cd agents/react && go test -count=1 ./...)", ""},
		{"ReAct verification export process records and step-export resume", "agents/react", "(cd agents/react && go test -count=1 ./...)", ""},
		{"Supervisor agent template", "agents/supervisor", "(cd agents/supervisor && go test -count=1 ./...)", ""},
		{"ReAct tool loop with model options and runtime IDs", "tests/agents", "(cd tests/agents && go test -count=1 ./...)", ""},
		{"ReAct checkpoint resume with tool, memory, and verification", "tests/agents", "(cd tests/agents && go test -count=1 ./...)", "(cd tests/agents && go test -tags=integration -count=1 ./...)"},
		{"Plan-Execute model planner and executor with request options", "tests/agents", "(cd tests/agents && go test -count=1 ./...)", "(cd tests/agents && go test -tags=integration -count=1 ./...)"},
		{"Plan-Execute approval checkpoint resume", "tests/agents", "(cd tests/agents && go test -count=1 ./...)", ""},
		{"Agent-as-Tool A2A delegation success and failure evidence", "tests/agents", "(cd tests/agents && go test -count=1 ./...)", "(cd tests/agents && go test -tags=integration -count=1 ./...)"},
		{"Supervisor routing to Plan-Execute child with runtime IDs", "tests/agents", "(cd tests/agents && go test -count=1 ./...)", "(cd tests/agents && go test -tags=integration -count=1 ./...)"},
		{"A2A agent node inside graph workflow", "tests/agents", "(cd tests/agents && go test -count=1 ./...)", ""},
		{"file snapshot evidence", "devagent/filesnapshot", "(cd devagent/filesnapshot && go test -count=1 ./...)", ""},
		{"git diff evidence", "devagent/gitdiff", "(cd devagent/gitdiff && go test -count=1 ./...)", ""},
		{"OpenAI provider", "models/openai", "(cd models/openai && go test -count=1 ./...)", "(cd models/openai && GOWORK=off go test -tags=integration -count=1 ./...)"},
		{"Ark provider", "models/ark", "(cd models/ark && go test -count=1 ./...)", "(cd models/ark && GOWORK=off go test -tags=integration -count=1 ./...)"},
		{"Agnes provider", "models/agnes", "(cd models/agnes && go test -count=1 ./...)", "(cd models/agnes && go test -tags=integration -count=1 ./...)"},
		{"Agnes provider streaming", "models/agnes", "(cd models/agnes && go test -count=1 ./...)", "(cd models/agnes && go test -tags=integration -count=1 ./...)"},
		{"Agnes provider tool calling", "models/agnes", "(cd models/agnes && go test -count=1 ./...)", "(cd models/agnes && go test -tags=integration -count=1 ./...)"},
		{"Agnes provider structured output", "models/agnes", "(cd models/agnes && go test -count=1 ./...)", "(cd models/agnes && go test -tags=integration -count=1 ./...)"},
		{"Agnes provider thinking toggle", "models/agnes", "(cd models/agnes && go test -count=1 ./...)", "(cd models/agnes && go test -tags=integration -count=1 ./...)"},
		{"Agnes provider error classification", "models/agnes", "(cd models/agnes && go test -count=1 ./...)", "(cd models/agnes && go test -tags=integration -count=1 ./...)"},
		{"Agnes provider cancel and timeout", "models/agnes", "(cd models/agnes && go test -count=1 ./...)", "(cd models/agnes && go test -tags=integration -count=1 ./...)"},
		{"Agnes-backed agent templates", "tests/agents", "(cd tests/agents && go test -count=1 ./...)", "(cd tests/agents && go test -tags=integration -count=1 ./...)"},
		{"Agnes-backed ReAct, Plan-Execute, Agent-as-Tool, Supervisor, and AgentNode templates", "tests/agents", "(cd tests/agents && go test -count=1 ./...)", "(cd tests/agents && go test -tags=integration -count=1 ./...)"},
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

func TestAgnesProviderFeatureCoverageUsesConcreteTests(t *testing.T) {
	clientTests := readRepoText(t, "../../models/agnes/client_test.go")
	integrationTests := readRepoText(t, "../../models/agnes/integration_test.go")
	allTests := clientTests + "\n" + integrationTests

	for _, want := range []string{
		"TestNewClientSupportsFullFeatureMock",
		"TestNewClientClassifiesStatusErrors",
		"TestAgnesIntegrationFullFeature",
		"TestAgnesIntegrationStructuredOutput",
		"TestAgnesIntegrationToolCall",
		"TestAgnesIntegrationCancelAndTimeout",
	} {
		if !strings.Contains(allTests, want) {
			t.Fatalf("Agnes provider missing concrete test %q", want)
		}
	}
}

func TestAgentTemplateFeatureCoverageUsesConcreteTests(t *testing.T) {
	matrix := readRepoText(t, "../../doc/FEATURES.md")
	matrixZH := readRepoText(t, "../../doc/FEATURES_zh.md")
	mockTests := readRepoText(t, "templates_mock_test.go")
	integrationTests := readRepoText(t, "agnes_integration_test.go")
	reactTests := readRepoText(t, "../../agents/react/react_test.go")
	allTests := mockTests + "\n" + integrationTests + "\n" + reactTests

	for _, capability := range []string{
		"ReAct tool loop with model options and runtime IDs",
		"ReAct checkpoint resume with tool, memory, and verification",
		"ReAct verification export process records and step-export resume",
		"Plan-Execute model planner and executor with request options",
		"Plan-Execute approval checkpoint resume",
		"Agent-as-Tool A2A delegation success and failure evidence",
		"Supervisor routing to Plan-Execute child with runtime IDs",
		"A2A agent node inside graph workflow",
		"Agnes-backed ReAct, Plan-Execute, Agent-as-Tool, Supervisor, and AgentNode templates",
	} {
		if !strings.Contains(matrix, capability) {
			t.Fatalf("FEATURES.md missing agent template capability %q", capability)
		}
		if !strings.Contains(matrixZH, capability) {
			t.Fatalf("FEATURES_zh.md missing agent template capability %q", capability)
		}
	}

	for _, testName := range []string{
		"TestReActTemplateRunsToolThenFinalWithMockModel",
		"TestPlanExecTemplateRunsWithMockModel",
		"TestPlanExecTemplateResumesApprovalCheckpointWithMockModel",
		"TestReActTemplateCanUsePlanExecAgentAsToolWithMockModel",
		"TestReActTemplateFailsWhenPlanExecAgentToolFailsWithMockModel",
		"TestSupervisorTemplateRoutesToPlanExecChildWithMockModel",
		"TestAgentNodeDelegatesA2AAgentInsideGraphWithMock",
		"TestAgnesIntegrationReActTemplateCapabilities",
		"TestAgnesIntegrationPlanExecuteTemplate",
		"TestAgnesIntegrationAgentAsToolTemplate",
		"TestAgnesIntegrationSupervisorTemplate",
		"TestAgnesIntegrationAgentNodeTemplate",
		"TestAgentRunsVerifierBeforeCompletingRun",
		"TestAgentVerificationExportRecordsResumeInterventionProcessRecords",
		"TestAgentResumesToolApprovalFromInterruptedStepExport",
	} {
		if !strings.Contains(allTests, testName) {
			t.Fatalf("agent templates missing concrete test %q", testName)
		}
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

func checkGoModModuleAtLeast(goMod, path, module, minVersion string) error {
	file, err := modfile.Parse(path, []byte(goMod), nil)
	if err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}

	requiredVersion := ""
	for _, requirement := range file.Require {
		if requirement.Mod.Path == module {
			requiredVersion = requirement.Mod.Version
			break
		}
	}
	if requiredVersion == "" {
		return fmt.Errorf("%s must require %s >= %s", path, module, minVersion)
	}
	if err := checkStableSemverAtLeast(requiredVersion, minVersion); err != nil {
		return fmt.Errorf("%s requires %s %s: %w", path, module, requiredVersion, err)
	}

	for _, replacement := range file.Replace {
		if replacement.Old.Path != module {
			continue
		}
		if replacement.Old.Version != "" && replacement.Old.Version != requiredVersion {
			continue
		}
		if replacement.New.Path != module {
			return fmt.Errorf("%s replaces %s with different module path %s", path, module, replacement.New.Path)
		}
		if replacement.New.Version == "" {
			return fmt.Errorf("%s replaces %s with %s without a verifiable module version", path, module, replacement.New.Path)
		}
		if err := checkStableSemverAtLeast(replacement.New.Version, minVersion); err != nil {
			return fmt.Errorf("%s replaces %s with %s %s: %w", path, module, replacement.New.Path, replacement.New.Version, err)
		}
	}
	return nil
}

func checkStableSemverAtLeast(version, minVersion string) error {
	if !semver.IsValid(version) || semver.Prerelease(version) != "" {
		return fmt.Errorf("version %q is not a stable Go semver", version)
	}
	if !semver.IsValid(minVersion) || semver.Prerelease(minVersion) != "" {
		return fmt.Errorf("minimum version %q is not a stable Go semver", minVersion)
	}
	if semver.Compare(version, minVersion) < 0 {
		return fmt.Errorf("version must be >= %s", minVersion)
	}
	return nil
}
