# gopact-ext Documentation

<!-- gopact:doc-language: zh,en -->

## 中文

`doc/` 保存 `gopact-ext` 的项目级文档。根 README 面向第一次访问仓库的用户；模块 README 面向具体 extension 的使用者；本目录记录能力覆盖、贡献流程、安全策略、变更记录和维护者治理规则。

推荐阅读顺序：

1. 先读根 [README](../README.md)，确认应该安装哪个 submodule。
2. 再读 [FEATURES.md](FEATURES.md)，确认目标能力是否有 mock 测试和真实 provider 测试。
3. 修改代码前读 [CONTRIBUTING.md](CONTRIBUTING.md)，按同一套命令本地验证。
4. 发布或开放仓库前读 [maintainers/repository-governance.md](maintainers/repository-governance.md)，确认 PR、CI、自动合并和敏感信息扫描都已生效。

## 文档索引

- [FEATURES.md](FEATURES.md)：extension 能力覆盖矩阵，包含路径、mock 测试命令和可选 integration 测试命令。
- [CONTRIBUTING.md](CONTRIBUTING.md)：开发环境、代码修改约束、验证命令和 PR checklist。
- [SECURITY.md](SECURITY.md)：支持版本、漏洞报告方式和敏感信息处理要求。
- [CHANGELOG.md](CHANGELOG.md)：未发布变更和已发布 extension tag 摘要。
- [maintainers/repository-governance.md](maintainers/repository-governance.md)：分支保护、admin auto-merge、非 admin PR 审批和公开前检查。

## English

`doc/` stores repository-level documentation for `gopact-ext`. The root README is the first-reader entry point, module READMEs document individual extensions, and this directory records capability coverage, contribution rules, security handling, release history, and maintainer governance.

Recommended reading order:

1. Read the root [README](../README.md) to choose the right submodule.
2. Read [FEATURES.md](FEATURES.md) to verify mock coverage and optional provider-backed integration coverage.
3. Read [CONTRIBUTING.md](CONTRIBUTING.md) before changing code.
4. Read [maintainers/repository-governance.md](maintainers/repository-governance.md) before changing repository visibility, merge rules, or release gates.

## Index

- [FEATURES.md](FEATURES.md): capability coverage matrix with runnable verification commands.
- [CONTRIBUTING.md](CONTRIBUTING.md): development setup, change rules, verification commands, and PR checklist.
- [SECURITY.md](SECURITY.md): supported versions, vulnerability reporting, and secret-handling policy.
- [CHANGELOG.md](CHANGELOG.md): unreleased changes and released extension tag summaries.
- [maintainers/repository-governance.md](maintainers/repository-governance.md): branch protection, admin auto-merge, non-admin review, and public-readiness checks.
