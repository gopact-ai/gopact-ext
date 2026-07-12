# Loop Agent

<!-- gopact:doc-language: en -->

Chinese: [README_zh.md](./README_zh.md)

`loop` repeatedly invokes one fixed child Agent until a typed code condition stops the Workflow or a hard iteration limit is reached.

## Common scenarios

Use it for deterministic refinement, polling, or validation cycles where the same child performs every iteration and code owns the stop rule.

## Execution model

The Workflow alternates `child` and `condition` nodes. After each response, a typed `Condition` returns `DecisionContinue` or `DecisionStop`; `WithMaxIterations` sets a positive hard limit. Repetitions create new node execution versions in the same parent Run. Workflow owns checkpoint, child continuation, and resume, so completed iterations are not replayed.

## Example

[example_test.go](./example_test.go) improves one draft three times before the typed condition stops the loop.

```bash
go test -run ExampleNew -count=1 -v
```

The example attaches its own local event handler. With `-v`, the terminal shows bounded Workflow process events from stderr and the test PASS status. The stable business result is written to stdout, captured by Go's example harness, and checked against `// Output:` rather than displayed.

## Advantages

- The stop rule is explicit, typed, deterministic, and bounded.
- Resume keeps completed iterations instead of starting the loop again.

## Limitations

- Every iteration uses one fixed child.
- The model does not choose tools or control the loop.

## When to choose another Agent

Use `react` for a model-owned tool loop, or another multi-child Agent when iterations need dynamic delegation.
