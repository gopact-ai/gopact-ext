# 🤖 Deep Research Agent

<!-- gopact:doc-language: en -->

Chinese: [README_zh.md](./README_zh.md)

`deepresearch` is a fixed Workflow for source discovery, evidence extraction, citation verification, and cited synthesis.

## Common scenarios

Use it when a report must preserve explicit source, evidence, and citation relationships instead of producing an untracked answer.

## Execution model

The Workflow performs query planning, discovery fan-out and merge, per-source fetch, evidence extraction, structural citation verification, and synthesis. Queries, deduplicated sources, evidence, and source cursors live in typed Workflow context. Discovery defaults to a parallelism limit of 8 and can be changed with `WithMaxParallelism`. Planner, Discoverer, Fetcher, EvidenceExtractor, and Synthesizer are required; CitationVerifier is optional. Identity, uniqueness, coverage, and reference integrity are checked before optional business citation policy and synthesis.

## Example

[example_test.go](./example_test.go) plans one query, fetches one source, extracts evidence, verifies its citation, and synthesizes a report.

```bash
go test -run ExampleNew -count=1 -v
```

The example attaches its own local event handler. With `-v`, the terminal shows bounded Workflow process events from stderr and the test PASS status. The stable business result is written to stdout, captured by Go's example harness, and checked against `// Output:` rather than displayed.

## Advantages

- Structural source, evidence, and citation integrity is enforced before synthesis.
- Research stages and intermediate identities remain explicit Workflow facts.

## Limitations

- The research pipeline is fixed and requires more component setup than a general task Agent.
- Business citation policy can add checks but cannot replace structural verification.

## When to choose another Agent

Use `deep` when the work is a general child-task ledger and does not require an evidence and citation ledger.
