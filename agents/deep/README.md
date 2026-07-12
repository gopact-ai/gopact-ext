# Deep Agent

<!-- gopact:doc-language: en -->

Chinese: [README_zh.md](./README_zh.md)

`deep` is a Workflow-backed Agent for executing a bounded, validated long-horizon task ledger through named child Agents while carrying artifact context forward.

## Common scenarios

Use it when a Planner can produce an ordered task ledger whose tasks reference existing child Agents and later tasks need artifacts from earlier results.

## Execution model

The fixed graph is `plan → accept-plan → continue → build-context → execute-task → record-task → continue | finish`. The typed Workflow state stores the plan, cursor, results, progress, and artifact context; `build-context` projects the original messages, metadata, and accumulated `ContextRefs` into each child request. Plans are validated for task limits, unique identities, pending status, and Directory membership. Only `record-task` advances completed progress, and an interrupted child resumes in the same child Run without replaying completed tasks.

## Example

[example_test.go](./example_test.go) validates and executes one named child task that produces a report.

```bash
go test -run ExampleNew -count=1 -v
```

The example attaches its own local event handler. With `-v`, the terminal shows bounded Workflow process events from stderr and the test PASS status. The stable business result is written to stdout, captured by Go's example harness, and checked against `// Output:` rather than displayed.

## Advantages

- Validated task identities and resumable child progress.
- Explicit artifact context can flow from completed tasks to later tasks.

## Limitations

- Planning is one-shot and tasks execute in plan order.
- Retrieval, replanning, reporting policy, and citation semantics are outside this fixed algorithm.

## When to choose another Agent

Use `planexec` when runtime replan and report stages are required. Use `deepresearch` when evidence and citation integrity are the primary concern.
