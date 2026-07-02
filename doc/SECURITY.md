# Security Policy

<!-- gopact:doc-language: en -->

Chinese documentation: [SECURITY_zh.md](SECURITY_zh.md)

`gopact-ext` handles provider tokens, tool payloads, model responses, agent events, and engineering evidence. The security baseline is that real credentials never enter the repository, sensitive data never enters CI output, and provider errors are safe to inspect in public logs.

## Supported Versions

The `main` branch and the latest released tag for each active extension module receive security fixes. Older pre-v1 extension tags are best-effort unless a maintainer explicitly marks them as supported in a release note.

## Reporting a Vulnerability

Do not open public issues for suspected vulnerabilities. Report privately through the `gopact-ai` maintainer channel until GitHub Security Advisory handling is enabled. Include the affected module, reproduction steps, impact boundary, and whether any secret may already have appeared in a fork, CI log, issue, PR comment, or commit message.

## Secret Handling

Never commit `.env`, provider keys, endpoint IDs, request logs with credentials, or model responses that contain user secrets. If a secret reaches git history, rotate the credential first, then remove the exposure and document the incident in the private maintainer channel.
