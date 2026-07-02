# Repository Governance

<!-- gopact:doc-language: zh,en -->

## 中文

`gopact-ext` 公开后，`main` 只允许通过 PR 更新。这个规则同样适用于只有一名主要维护者的阶段：PR 可以把 CI、review、自动合并、敏感信息检查和发布证据固定在同一个审计链路里。

## Pull Request Flow

仓库规则：

- Require status checks to pass before merge.
- 必需检查包括 `ci/test` 和 `pr-governance/author-policy`。
- 管理员也受 ruleset 约束。
- 禁止 force push 到 `main`。
- 禁止删除 `main`。
- Do not configure a global required review count。单维护者场景下，全局 required review 会阻塞 admin 自己的 PR；条件审批由 `author-policy` 实现。

Admin-authored PRs 可以在所有 required checks 通过后自动合并。

Non-admin-authored PRs 必须在最新 commit 上获得至少一名 admin 审批。`author-policy` 通过 GitHub API 检查 PR author 与 reviewer 的 repository permission。

## Admin Auto-Merge

`admin-automerge` workflow 对 admin-authored PR 开启 squash auto-merge。它运行在 `pull_request_target` 上，但不 checkout、不执行 PR 代码，只调用 GitHub CLI 配置 auto-merge。

仓库设置应保持：

- allow auto-merge
- allow squash merge
- delete head branches after merge
- disable merge commit and rebase merge unless a release explicitly needs them

## Public Release Checks

开放仓库或发布重要版本前执行：

```bash
./scripts/public-readiness-check.sh
for mod in $(find . -name go.mod -not -path './.git/*' -exec dirname {} \; | sort); do (cd "$mod" && go test -count=1 ./...); done
for mod in $(find . -name go.mod -not -path './.git/*' -exec dirname {} \; | sort); do (cd "$mod" && go test -race -count=1 ./...); done
for mod in $(find . -name go.mod -not -path './.git/*' -exec dirname {} \; | sort); do (cd "$mod" && go vet ./...); done
for mod in $(find . -name go.mod -not -path './.git/*' -exec dirname {} \; | sort); do (cd "$mod" && golangci-lint run ./...); done
for mod in $(find . -name go.mod -not -path './.git/*' -exec dirname {} \; | sort); do (cd "$mod" && go test -coverprofile=coverage.out ./...); done
for mod in $(find . -name go.mod -not -path './.git/*' -exec dirname {} \; | sort); do (cd "$mod" && govulncheck ./...); done
```

公开前必须确认：

- `.env` 和 `.env.*` 没有被 tracked。
- commit message 没有真实 token、endpoint ID、AK/SK 或 provider secret。
- README、module README、FEATURES 和实际测试覆盖一致。
- GitHub Secret Scanning、Push Protection、Dependabot security updates 已开启。
- 规则集覆盖 `main`，并且没有 admin bypass。

## English

After `gopact-ext` is public, `main` is PR-only. This remains useful even with one maintainer because every change carries CI evidence, review state, auto-merge state, and secret-scanning evidence.

Admin-authored PRs may be squash-merged automatically after required checks pass. Non-admin-authored PRs require at least one admin approval on the latest commit. The `author-policy` job implements this conditional rule so the repository does not need a global required review count that would block single-maintainer admin PRs.
