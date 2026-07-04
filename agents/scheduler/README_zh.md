# scheduler

[![CI](https://github.com/gopact-ai/gopact-ext/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/gopact-ai/gopact-ext/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/gopact-ai/gopact-ext/agents/scheduler.svg)](https://pkg.go.dev/github.com/gopact-ai/gopact-ext/agents/scheduler)
[![License](https://img.shields.io/github/license/gopact-ai/gopact-ext)](../../LICENSE)

<!-- gopact:doc-language: zh -->

[英文文档](README.md)

`scheduler` 提供 provider-neutral 的后台 worker 原语，用于承载可持久化的 agent 后台任务。模块负责 `RunOnce`、有界 `Drain`、队列状态转换、retry/stop/dead-letter 决策、可选 lease ownership、lease renewal 和 schedule verification evidence。

安装：

```bash
go get github.com/gopact-ai/gopact-ext/agents/scheduler@v0.1.8
```

用法：

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

`MemoryQueue` 只用于本地开发、测试和单进程 worker。生产级持久化由实现 `Queue` 的 adapter 负责；分布式 worker ownership 由 `gopact.LeaseBackend` adapter 负责。

验证：

```bash
(cd agents/scheduler && go test -count=1 ./...)
```
