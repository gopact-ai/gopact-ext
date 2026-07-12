# 🤖 Router Agent

<!-- gopact:doc-language: en -->

Chinese: [README_zh.md](./README_zh.md)

`router` makes one typed routing decision and invokes exactly one child from an immutable `agent.Directory`.

## Common scenarios

Use it for support classification, specialist selection, or another one-shot dispatch where only one child should handle the request.

## Execution model

Selection and the selected child are Workflow nodes. The selector sees the request and available child identities, then names one child. Workflow owns execution facts, lineage, checkpoint, interruption, and resume. The Router does not supervise the selected child or replan afterward.

## Example

[example_test.go](./example_test.go) classifies a timeout request and dispatches it to technical support.

```bash
go test -run ExampleNew -count=1 -v
```

The example attaches its own local event handler. With `-v`, the terminal shows bounded Workflow process events from stderr and the test PASS status. The stable business result is written to stdout, captured by Go's example harness, and checked against `// Output:` rather than displayed.

## Advantages

- The smallest dynamic dispatch surface: one selection and one child invocation.
- Directory membership and child identity remain explicit.

## Limitations

- Selection happens exactly once.
- There is no supervision, iterative delegation, or replanning after selection.

## When to choose another Agent

Use `supervisor` when the decision must use accumulated child results and delegate repeatedly.
