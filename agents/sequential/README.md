# Sequential Agent

<!-- gopact:doc-language: en -->

Chinese: [README_zh.md](./README_zh.md)

`sequential` executes a construction-time list of child Agents in deterministic order through one Workflow.

## Common scenarios

Use it for fixed writer-reviewer, transform-validate, or other ordered pipelines where each child builds on the previous result.

## Execution model

The first child receives the caller request. Each later child receives the previous response message, artifacts, and metadata. A child failure stops the sequence immediately. Each child is a typed Workflow invokable node; Workflow owns lineage, node facts, checkpoint, interruption, and resume. An interrupted child resumes in the same child Run and completed children are not replayed.

## Example

[example_test.go](./example_test.go) passes release notes through a writer and then a reviewer.

```bash
go test -run ExampleNew -count=1 -v
```

The example attaches its own local event handler. With `-v`, the terminal shows bounded Workflow process events from stderr and the test PASS status. The stable business result is written to stdout, captured by Go's example harness, and checked against `// Output:` rather than displayed.

## Advantages

- Handoffs and execution order are deterministic.
- Failure and resume semantics remain simple because only one child is active at a time.

## Limitations

- It has no runtime planning or dynamic routing.
- It does not run independent work concurrently.

## When to choose another Agent

Use `planexec` when steps are created at runtime, or `parallel` when independent branches should fan out concurrently.
