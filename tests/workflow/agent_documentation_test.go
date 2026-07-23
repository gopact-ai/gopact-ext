package workflowtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublicAgentModuleDocumentation(t *testing.T) {
	modules := []string{
		"agenttool",
		"loop",
		"parallel",
		"planexec",
		"react",
		"router",
		"sequential",
		"supervisor",
	}
	englishHeadings := []string{
		"Common scenarios",
		"Execution model",
		"Example",
		"Advantages",
		"Limitations",
		"When to choose another Agent",
	}
	chineseHeadings := []string{
		"常见场景",
		"执行模型",
		"示例",
		"优点",
		"限制",
		"何时选择其他 Agent",
	}

	for _, module := range modules {
		t.Run(module, func(t *testing.T) {
			moduleDir := filepath.Join("..", "..", "agents", module)
			example := requireReadableFile(t, filepath.Join(moduleDir, "example_test.go"))
			english := requireReadableFile(t, filepath.Join(moduleDir, "README.md"))
			chinese := requireReadableFile(t, filepath.Join(moduleDir, "README_zh.md"))

			requireOrderedHeadings(t, "README.md", english, englishHeadings)
			requireOrderedHeadings(t, "README_zh.md", chinese, chineseHeadings)
			requireReferences(t, "README.md", english, "README_zh.md", "example_test.go")
			requireReferences(t, "README_zh.md", chinese, "README.md", "example_test.go")
			requireReferences(t, "example_test.go", example, `"log/slog"`, "gopact.WithEventHandler")
		})
	}
}

func requireReadableFile(t *testing.T, path string) string {
	t.Helper()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	return string(content)
}

func requireOrderedHeadings(t *testing.T, name, content string, headings []string) {
	t.Helper()

	previous := -1
	for _, heading := range headings {
		marker := "## " + heading
		count, position := exactLineOccurrence(content, marker)
		if count != 1 {
			t.Fatalf("%s: heading %q occurs %d times, want exactly once", name, marker, count)
		}

		if position <= previous {
			t.Fatalf("%s: heading %q is out of order", name, marker)
		}
		previous = position
	}
}

func exactLineOccurrence(content, want string) (int, int) {
	count, position := 0, -1
	lineNumber := 0
	for line := range strings.Lines(content) {
		if strings.TrimSpace(line) == want {
			count++
			position = lineNumber
		}
		lineNumber++
	}
	return count, position
}

func requireReferences(t *testing.T, name, content string, references ...string) {
	t.Helper()

	for _, reference := range references {
		if !strings.Contains(content, reference) {
			t.Errorf("%s: missing %q", name, reference)
		}
	}
}
