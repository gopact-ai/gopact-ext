# Contributing to gopact-ext

<!-- gopact:doc-language: en -->

Chinese documentation: [CONTRIBUTING_zh.md](CONTRIBUTING_zh.md)

`gopact-ext` is a multi-module Go repository. Each extension must stay independently installable, testable, and releasable. Cross-template tests belong in `tests/agents`; a user should not need every provider or template dependency to adopt one module.

## Development Setup

Install Go 1.25 or newer, clone the repository, and keep work on a pull-request branch:

```bash
git clone https://github.com/gopact-ai/gopact-ext.git
cd gopact-ext
git switch -c your-change
```

Each subdirectory with a `go.mod` is an independent module. Run module-local commands from the module directory when editing providers, templates, or development-agent helpers.

## Verification

CI is mock-only. It must pass without credentials, `.env`, or external provider access.

```bash
git diff --check
for mod in $(find . -name go.mod -not -path './.git/*' -exec dirname {} \; | sort); do (cd "$mod" && go mod tidy); done
git diff --exit-code
for mod in $(find . -name go.mod -not -path './.git/*' -exec dirname {} \; | sort); do (cd "$mod" && go test -count=1 ./...); done
for mod in $(find . -name go.mod -not -path './.git/*' -exec dirname {} \; | sort); do (cd "$mod" && go test -race -count=1 ./...); done
for mod in $(find . -name go.mod -not -path './.git/*' -exec dirname {} \; | sort); do (cd "$mod" && go vet ./...); done
for mod in $(find . -name go.mod -not -path './.git/*' -exec dirname {} \; | sort); do (cd "$mod" && golangci-lint run ./...); done
for mod in $(find . -name go.mod -not -path './.git/*' -exec dirname {} \; | sort); do (cd "$mod" && go test -coverprofile=coverage.out ./...); done
for mod in $(find . -name go.mod -not -path './.git/*' -exec dirname {} \; | sort); do (cd "$mod" && govulncheck ./...); done
```

Real provider checks are opt-in integration tests. Use local `.env` values and never commit credentials.

## Pull Request Checklist

- Keep provider-specific request shaping inside `models/*`.
- Keep agent templates provider-neutral and dependent on core `gopact` contracts.
- Document every public option, environment variable, and integration command added by the change.
- Add or update mock tests for deterministic CI behavior.
- Add integration tests only behind the `integration` build tag.
- Confirm generated coverage, security, and public-readiness checks pass before requesting review.
