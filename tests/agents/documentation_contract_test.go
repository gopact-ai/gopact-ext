package agents

import (
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepositoryDocumentationFilesStayUnderDocExceptReadmes(t *testing.T) {
	err := filepath.WalkDir("../..", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".md") {
			return nil
		}

		rel, err := filepath.Rel("../..", path)
		if err != nil {
			return err
		}
		slashPath := filepath.ToSlash(rel)
		if entry.Name() == "README.md" || entry.Name() == "README_zh.md" || strings.HasPrefix(slashPath, "doc/") {
			return nil
		}
		t.Fatalf("%s is a Markdown document outside doc/ and is not a README", slashPath)
		return nil
	})
	if err != nil {
		t.Fatalf("walk markdown docs: %v", err)
	}
}

func TestRepositoryMarkdownDocsUseSeparatedLanguageFiles(t *testing.T) {
	err := filepath.WalkDir("../..", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".md") {
			return nil
		}

		body := readRepoText(t, path)
		slashPath := filepath.ToSlash(path)
		if strings.HasSuffix(entry.Name(), "_zh.md") {
			if !strings.Contains(body, "<!-- gopact:doc-language: zh -->") {
				t.Fatalf("%s missing Chinese documentation marker", slashPath)
			}
			if strings.Contains(body, "## English") {
				t.Fatalf("%s must not embed English content", slashPath)
			}
			return nil
		}
		if !strings.Contains(body, "<!-- gopact:doc-language: en -->") {
			t.Fatalf("%s missing English documentation marker", slashPath)
		}
		if strings.Contains(body, "## 中文") {
			t.Fatalf("%s must not embed Chinese content", slashPath)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk markdown docs: %v", err)
	}
}

func TestRepositoryReadmeBadgesAndDocIndexAreConfigured(t *testing.T) {
	readme := readRepoText(t, "../../README.md")

	for _, want := range []string{
		"https://github.com/gopact-ai/gopact-ext/actions/workflows/ci.yml/badge.svg?branch=main",
		"https://img.shields.io/github/license/gopact-ai/gopact-ext",
		"https://pkg.go.dev/badge/github.com/gopact-ai/gopact-ext/models/openai.svg",
		"doc/README.md",
		"doc/FEATURES.md",
		"doc/CONTRIBUTING.md",
		"doc/SECURITY.md",
		"doc/CHANGELOG.md",
		"doc/maintainers/repository-governance.md",
	} {
		if !strings.Contains(readme, want) {
			t.Fatalf("README.md missing badge or doc index entry %q", want)
		}
	}
}

func TestModuleReadmesExposeGoReferenceBadges(t *testing.T) {
	for _, module := range []string{
		"agents/agentnode",
		"agents/agenttool",
		"agents/humanreview",
		"agents/planexec",
		"agents/react",
		"agents/scheduler",
		"agents/supervisor",
		"devagent/filesnapshot",
		"devagent/gitdiff",
		"models/agnes",
		"models/ark",
		"models/openai",
	} {
		readme := readRepoText(t, "../../"+module+"/README.md")
		badge := "https://pkg.go.dev/badge/github.com/gopact-ai/gopact-ext/" + module + ".svg"
		if !strings.Contains(readme, badge) {
			t.Fatalf("%s/README.md missing Go Reference badge %q", module, badge)
		}
	}
}
