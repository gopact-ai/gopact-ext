# Security Policy

## Supported Versions

`gopact-ext` is pre-v1. Security fixes target the `main` branch and the latest
published extension tags until a stable release line is declared.

## Reporting a Vulnerability

Do not open a public issue for suspected vulnerabilities. Report privately to
the maintainers through the gopact-ai organization owner channel until a
dedicated security advisory process is enabled.

Include:

- affected extension module
- reproduction steps
- impact and trust boundary
- whether provider credentials, prompts, tool payloads, artifacts, or external
  tokens may be exposed

## Handling Guidelines

- Do not include secrets, tokens, raw prompts, raw model responses, raw tool
  args/results, or private customer data in issues, tests, examples, or logs.
- Keep `.env` local and use `.env.example` for placeholders only.
- Provider adapters must preserve redaction, request-shape, timeout,
  cancellation, and error-classification tests.
