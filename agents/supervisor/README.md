# Supervisor Agent

<!-- gopact:doc-language: en -->

Chinese: [README_zh.md](./README_zh.md)

`supervisor` iteratively delegates to child Agents and lets a Decider produce the final response from accumulated delegation results.

## Common scenarios

Use it when the next child cannot be selected once up front and each decision may depend on results collected in earlier rounds.

## Execution model

The fixed graph is `start → decide → delegate → record → decide | finish`. Typed Workflow context stores the request, round, and delegation results. The Decider receives typed `DecisionInput`; delegated Directory Agents run as invokable Workflow nodes. Interrupted children resume in the same child Runs without repeating accepted decisions or completed delegations. `WithMaxRounds` bounds successful delegations but still permits a final synthesis decision after the bound is reached.

## Example

[example_test.go](./example_test.go) delegates one research task and then returns the accumulated result.

```bash
go test -run ExampleNew -count=1 -v
```

The example attaches its own local event handler. With `-v`, the terminal shows bounded Workflow process events from stderr and the test PASS status. The stable business result is written to stdout, captured by Go's example harness, and checked against `// Output:` rather than displayed.

## Advantages

- Each decision can use all accumulated child results.
- Dynamic delegation and final synthesis remain explicit, resumable Workflow stages.

## Limitations

- Delegation requires a bounded round count and a reliable Decider.
- The iterative control loop adds cost and variability compared with one-shot routing.

## When to choose another Agent

Use `router` when one-shot selection is enough, or `sequential` when the entire pipeline is fixed at construction time.
