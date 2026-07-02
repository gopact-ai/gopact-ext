# Contributing to gopact-ext

`gopact-ext` contains official extension modules for `gopact`. Keep each
extension independently usable: every module owns its `go.mod`, tests, README,
and release tag.

## Development Setup

Prerequisites:

- Go 1.25.11
- Git
- `golangci-lint` v2.8.0
- `govulncheck` v1.1.4

Clone and verify the repository:

```bash
git clone git@github.com:gopact-ai/gopact-ext.git
cd gopact-ext
for mod in $(find . -name go.mod -not -path './.git/*' -exec dirname {} \; | sort); do
  (cd "$mod" && go test -count=1 ./...)
done
```

## Change Guidelines

- Keep provider adapters thin and provider-neutral at the `gopact` boundary.
- Put provider-specific API paths, request shaping, thinking controls,
  structured output, and tool calling inside the provider module.
- Keep real-service tests behind the `integration` build tag.
- Do not commit `.env`, real API keys, model endpoint IDs, prompts, or raw
  provider responses.
- Update module README files and root install commands when releasing tags.

## Verification

Before opening a pull request, run:

```bash
git diff --check
./scripts/public-readiness-check.sh
for mod in $(find . -name go.mod -not -path './.git/*' -exec dirname {} \; | sort); do (cd "$mod" && go mod tidy); done
git diff --exit-code
for mod in $(find . -name go.mod -not -path './.git/*' -exec dirname {} \; | sort); do (cd "$mod" && go test -count=1 ./...); done
for mod in $(find . -name go.mod -not -path './.git/*' -exec dirname {} \; | sort); do (cd "$mod" && go test -race -count=1 ./...); done
for mod in $(find . -name go.mod -not -path './.git/*' -exec dirname {} \; | sort); do (cd "$mod" && go vet ./...); done
for mod in $(find . -name go.mod -not -path './.git/*' -exec dirname {} \; | sort); do (cd "$mod" && golangci-lint run ./...); done
for mod in $(find . -name go.mod -not -path './.git/*' -exec dirname {} \; | sort); do (cd "$mod" && go test -coverprofile=coverage.out ./...); done
for mod in $(find . -name go.mod -not -path './.git/*' -exec dirname {} \; | sort); do (cd "$mod" && govulncheck ./...); done
```

## Pull Request Checklist

- Tests cover changed behavior or the changed documentation contract.
- CI remains mock-only; real provider checks stay opt-in with integration tags.
- Public README and module README install commands point to released tags.
- No generated noise, local `.env`, raw prompts, API keys, or endpoint IDs are
  tracked.
