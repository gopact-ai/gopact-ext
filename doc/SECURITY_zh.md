# Security Policy

<!-- gopact:doc-language: zh -->

[英文文档](./SECURITY.md)

## 中文

`gopact-ext` 直接处理 provider token、tool payload、模型响应、agent event 和工程证据。安全策略的核心目标是：真实凭据不进入仓库，敏感数据不进入 CI 输出，provider adapter 的错误和日志默认可公开。

## Supported Versions

`gopact-ext` 仍处于 pre-v1。安全修复优先落在 `main`，并回补到最新发布的 extension tag。稳定版本线建立后，本节会改为明确的版本支持表。

## Reporting a Vulnerability

不要为疑似漏洞创建公开 issue。请通过 `gopact-ai` 组织维护者私有渠道报告，直到仓库启用 GitHub Security Advisory 流程。

报告时请包含：

- 受影响的 extension 模块和版本。
- 最小复现步骤。
- 影响边界：provider token、prompt、tool args/result、artifact、agent event、用户数据或本地文件。
- 是否已在 fork、CI log、issue、PR 评论或 commit message 中暴露敏感信息。

处理要求：

- 不提交 `.env`、真实 token、真实 endpoint ID、私有 prompt、原始模型响应或客户数据。
- `.env.example` 只能包含占位值。
- public readiness check 必须扫描 tracked file 和 commit message 中的高置信敏感模式。
- provider adapter 必须保留 timeout、cancel、错误分类、request shaping 和 redaction 相关测试。
