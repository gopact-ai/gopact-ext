# Agent Tool

<!-- gopact:doc-language: en -->

Chinese: [README_zh.md](./README_zh.md)

`agenttool` adapts one child `agent.Agent` into a model-visible `agent.InvokableTool` without creating a second runtime.

## Common scenarios

Use it when a model must call an existing child Agent through a tool boundary and the tool protocol needs explicit request and response mapping.

## Execution model

`Invoke` maps `ToolCall` to `agent.Request`, forwards Workflow child options to the Agent, and maps `agent.Response` to `ToolResult`. Workflow owns child lineage, interruption, checkpoint, and resume. `Adapter.Input` must be deterministic and replay-safe; external effects belong in the child Agent.

## Example

[example_test.go](./example_test.go) adapts a child Agent into a tool and executes one delegated task.

```bash
go test -run ExampleNew -count=1 -v
```

The example attaches its own local event handler. With `-v`, the terminal shows bounded Workflow process events from stderr and the test PASS status. The stable business result is written to stdout, captured by Go's example harness, and checked against `// Output:` rather than displayed.

## Advantages

- Deterministic protocol mapping without a second runtime or event stream.
- Child execution keeps normal Workflow lineage and resume behavior.

## Limitations

- Adapter input must be deterministic and replay-safe.
- The adapter does not add supervision, planning, or business-side effects.

## When to choose another Agent

Use direct Workflow child composition when the model does not need to see the child as a tool.
