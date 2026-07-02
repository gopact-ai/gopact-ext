# Repository Governance

This repository uses pull requests as the only write path to `main` after it is
made public. The rule exists even for a single maintainer: it keeps CI, review
state, and release evidence attached to every extension change.

## Pull Request Flow

- Require status checks to pass before merge.
- Require the `ci` workflow `test` job.
- Require the `pr-governance` workflow `author-policy` job.
- Include administrators in branch protection or ruleset enforcement.
- Block force-pushes and branch deletion on `main`.
- Do not configure a global required review count. The `author-policy` check
  enforces the conditional review rule without blocking a single admin working
  alone.

Admin-authored PRs may merge after required CI checks pass.

Non-admin-authored PRs require at least one admin approval on the latest commit.
The `author-policy` job checks the PR author's repository permission and the
reviewer's permission through GitHub's API.

## Admin Auto-Merge

The `admin-automerge` workflow enables squash auto-merge for admin-authored PRs.
It does not check out or execute pull request code. Non-admin-authored PRs are
left for an admin to approve and merge after `author-policy` passes.

Repository settings should be:

- allow auto-merge
- allow squash merge
- delete head branches after merge
- disable merge commits and rebase merge unless a release requires them

## Public Release Checks

Before changing repository visibility to public, run:

```bash
./scripts/public-readiness-check.sh
for mod in $(find . -name go.mod -not -path './.git/*' -exec dirname {} \; | sort); do (cd "$mod" && go test -count=1 ./...); done
for mod in $(find . -name go.mod -not -path './.git/*' -exec dirname {} \; | sort); do (cd "$mod" && go test -race -count=1 ./...); done
for mod in $(find . -name go.mod -not -path './.git/*' -exec dirname {} \; | sort); do (cd "$mod" && go vet ./...); done
for mod in $(find . -name go.mod -not -path './.git/*' -exec dirname {} \; | sort); do (cd "$mod" && golangci-lint run ./...); done
for mod in $(find . -name go.mod -not -path './.git/*' -exec dirname {} \; | sort); do (cd "$mod" && go test -coverprofile=coverage.out ./...); done
for mod in $(find . -name go.mod -not -path './.git/*' -exec dirname {} \; | sort); do (cd "$mod" && govulncheck ./...); done
```

The readiness script checks tracked files and commit messages for high-confidence
secret patterns. It reports file names and commit hashes only; it does not print
matched secret contents.
