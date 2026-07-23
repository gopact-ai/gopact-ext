# 🤖 ReAct Agent

<!-- gopact:doc-language: en -->

Chinese: [README_zh.md](./README_zh.md)

`react` is a Workflow-backed Agent for a bounded loop in which one model chooses tools, consumes their observations, repairs responses, and finishes.

## Common scenarios

Use it when a model must own the next-action decision and completed model turns or tool batches must survive interruption without replay.

## Execution model

The observable graph starts with `prepare → model`. A final response follows `model → finish`; repair follows `model → continue → prepare`; tools follow `model → continue → dispatch-tools → tool → observe-tools → continue`. From there, `continue` returns to `dispatch-tools` while another batch remains, otherwise it returns to `prepare` for the next model turn. Typed Workflow state holds the turn, messages, pending observations, tool calls, artifacts, and metadata. `prepare` consumes pending observations once and builds the exact model request with configured tool specs. Direct tools in one batch run concurrently; every observation retains its originating call ID even when completion order differs, while invokable tools remain serial barriers. `WithLimits` bounds turns, total tool calls, and parallel direct tools.

## Example

[example_test.go](./example_test.go) performs one model-requested lookup, feeds the observation back, and returns a final answer.

```bash
go test -run ExampleNew -count=1 -v
```

The example attaches its own local event handler. With `-v`, the terminal shows bounded Workflow process events from stderr and the test PASS status. The stable business result is written to stdout, captured by Go's example harness, and checked against `// Output:` rather than displayed.

## Advantages

- Natural model and tool feedback with resumable tool outcomes.
- Tool batches are bounded and observations preserve model call order.

## Limitations

- Context projection and the model-tool loop algorithm are fixed.
- The model owns control decisions, so behavior is less deterministic than code-owned orchestration.

## When to choose another Agent

Use `loop`, `router`, `sequential`, or another code-owned Workflow when deterministic orchestration is preferable.
