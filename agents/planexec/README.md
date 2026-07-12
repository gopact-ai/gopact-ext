# Plan Execute Agent

<!-- gopact:doc-language: en -->

Chinese: [README_zh.md](./README_zh.md)

`planexec` is a fixed Workflow that creates a runtime plan, executes named child steps, optionally replaces the plan, and produces a report.

## Common scenarios

Use it when the ordered steps are not known at construction time and execution needs explicit replan and report stages.

## Execution model

The initial path is `plan → accept-plan → continue → dispatch-step → execute-step → record-step → replan`. From `replan`, a done decision follows `continue → report`; a replacement plan follows `accept-replan → continue → dispatch-step` and continues executing steps. Typed Workflow context holds the plan, cursor, results, and transition count. Plans are validated for child membership, step identity and status, and increasing replacement versions. `WithDirectory`, `WithPlanner`, `WithReplanner`, and `WithReporter` are required; `WithMaxTransitions` can lower the default limit of 32 replan transitions. Only `record-step` writes completed state, and interrupted children resume in their existing child Runs.

## Example

[example_test.go](./example_test.go) creates a one-step release plan, executes it through a child Agent, and reports the result.

```bash
go test -run ExampleNew -count=1 -v
```

The example attaches its own local event handler. With `-v`, the terminal shows bounded Workflow process events from stderr and the test PASS status. The stable business result is written to stdout, captured by Go's example harness, and checked against `// Output:` rather than displayed.

## Advantages

- Plan transitions and replacement versions are explicit and validated.
- Replan and report stages are part of the resumable Workflow.

## Limitations

- It requires more interfaces and lifecycle stages than a fixed pipeline.
- Its algorithm fixes where planning, execution, replanning, and reporting occur.

## When to choose another Agent

Use `sequential` when all steps and their order are known at construction time.
