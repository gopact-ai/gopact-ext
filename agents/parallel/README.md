# Parallel Agent

<!-- gopact:doc-language: en -->

Chinese: [README_zh.md](./README_zh.md)

`parallel` fans one request out to a fixed set of independent child Agents and merges their declaration-ordered results with an explicit Reducer.

## Common scenarios

Use it for independent security, quality, policy, or specialist reviews that can run concurrently and then be combined deterministically.

## Execution model

Each branch receives an independent copy of the original request. Workflow provides fan-out, `SettleAll`, child lineage, checkpoint, and multi-interrupt resume; `WithMaxParallelism` bounds active branches. The Reducer runs only after all branches settle and receives defensive results in child declaration order, never completion order. A real branch failure is not hidden by sibling cancellation.

## Example

[example_test.go](./example_test.go) runs security and quality reviews concurrently and reduces them in declaration order.

```bash
go test -run ExampleNew -count=1 -v
```

The example attaches its own local event handler. With `-v`, the terminal shows bounded Workflow process events from stderr and the test PASS status. The stable business result is written to stdout, captured by Go's example harness, and checked against `// Output:` rather than displayed.

## Advantages

- Bounded concurrency with deterministic merge order.
- Branch request and result isolation prevents sibling mutation from changing other branches.

## Limitations

- Branches must be independent and cannot consume sibling results.
- Merge policy must be expressed explicitly by the Reducer.

## When to choose another Agent

Use `sequential` when one child must receive and build on the previous child's output.
