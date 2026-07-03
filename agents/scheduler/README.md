# scheduler

[![CI](https://github.com/gopact-ai/gopact-ext/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/gopact-ai/gopact-ext/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/gopact-ai/gopact-ext/agents/scheduler.svg)](https://pkg.go.dev/github.com/gopact-ai/gopact-ext/agents/scheduler)
[![License](https://img.shields.io/github/license/gopact-ai/gopact-ext)](../../LICENSE)

<!-- gopact:doc-language: en -->

Chinese documentation: [README_zh.md](README_zh.md)

`scheduler` provides provider-neutral background worker primitives for durable agent work. The package owns `RunOnce` and bounded `Drain` orchestration, queue transitions, retry/stop/dead-letter decisions, optional lease ownership, lease renewal, and schedule verification evidence.

Install it with `go get github.com/gopact-ai/gopact-ext/agents/scheduler@v0.1.4`.

```go
queue := scheduler.NewMemoryQueue(scheduler.Job{
	ID:      "job-1",
	Payload: "replay pending work",
	Attempt: 1,
})

worker, err := scheduler.NewWorker(
	queue,
	scheduler.HandlerFunc(func(ctx context.Context, job scheduler.Job) (scheduler.Result, error) {
		return scheduler.Result{Status: scheduler.JobSucceeded}, nil
	}),
	scheduler.WithLease(
		gopact.NewMemoryLeaseBackend(),
		gopact.LeaseRequest{Key: "scheduler/default", Owner: "worker-1", TTL: time.Minute},
	),
	scheduler.WithLeaseRenewalInterval(10*time.Second),
)
if err != nil {
	return err
}

_, err = worker.RunOnce(ctx)
```

`MemoryQueue` is for local development, tests, and single-process workers. Production durability belongs in queue adapters that implement `Queue`; distributed ownership belongs in `gopact.LeaseBackend` adapters.

Run `(cd agents/scheduler && go test -count=1 ./...)` before changing behavior.
