package scheduler

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// MemoryQueueOption configures MemoryQueue.
type MemoryQueueOption func(*MemoryQueue)

// WithMemoryQueueClock overrides the MemoryQueue clock.
func WithMemoryQueueClock(now func() time.Time) MemoryQueueOption {
	return func(q *MemoryQueue) {
		if now != nil {
			q.now = now
		}
	}
}

// CompletedRecord records a completed queue transition.
type CompletedRecord struct {
	Job    Job
	Result Result
}

// ScheduleRecord records a retry, dead-letter, or stop transition.
type ScheduleRecord struct {
	Job      Job
	Result   Result
	Decision ScheduleDecision
}

// MemoryQueueSnapshot is a defensive copy of MemoryQueue state.
type MemoryQueueSnapshot struct {
	Pending      []Job
	Completed    []CompletedRecord
	Retried      []ScheduleRecord
	DeadLettered []ScheduleRecord
	Stopped      []ScheduleRecord
}

// MemoryQueue is an in-process queue for tests and local workers.
type MemoryQueue struct {
	mu           sync.Mutex
	now          func() time.Time
	nextDelivery int
	pending      []Job
	completed    []CompletedRecord
	retried      []ScheduleRecord
	deadLettered []ScheduleRecord
	stopped      []ScheduleRecord
}

var _ Queue = (*MemoryQueue)(nil)

// NewMemoryQueue creates an in-memory queue seeded with pending jobs.
func NewMemoryQueue(jobs ...Job) *MemoryQueue {
	q := &MemoryQueue{now: time.Now}
	for _, job := range jobs {
		q.pending = append(q.pending, copyJob(job))
	}
	return q
}

// NewMemoryQueueWithOptions creates an in-memory queue with options.
func NewMemoryQueueWithOptions(jobs []Job, opts ...MemoryQueueOption) *MemoryQueue {
	q := NewMemoryQueue(jobs...)
	for _, opt := range opts {
		if opt != nil {
			opt(q)
		}
	}
	if q.now == nil {
		q.now = time.Now
	}
	return q
}

// Dequeue returns the first ready pending job.
func (q *MemoryQueue) Dequeue(ctx context.Context) (Job, bool, error) {
	if err := ctxErr(ctx); err != nil {
		return Job{}, false, err
	}
	if q == nil {
		return Job{}, false, nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()

	now := q.currentTime()
	for i, job := range q.pending {
		if !job.NotBefore.IsZero() && job.NotBefore.After(now) {
			continue
		}
		q.pending = append(q.pending[:i], q.pending[i+1:]...)
		q.nextDelivery++
		if job.DeliveryID == "" {
			job.DeliveryID = fmt.Sprintf("delivery-%d", q.nextDelivery)
		}
		return copyJob(job), true, nil
	}
	return Job{}, false, nil
}

// Complete records a completed job transition.
func (q *MemoryQueue) Complete(ctx context.Context, job Job, result Result) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.completed = append(q.completed, CompletedRecord{Job: copyJob(job), Result: copyResult(result)})
	return nil
}

// Retry records a retry transition and enqueues the next attempt.
func (q *MemoryQueue) Retry(ctx context.Context, job Job, decision ScheduleDecision) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	decision = copyScheduleDecision(decision)
	q.retried = append(q.retried, ScheduleRecord{
		Job:      copyJob(job),
		Result:   copyResult(decision.Result),
		Decision: decision,
	})
	next := copyJob(job)
	next.DeliveryID = ""
	next.Attempt = decision.NextAttempt
	if next.Attempt <= 0 {
		next.Attempt = job.Attempt + 1
	}
	if decision.Delay > 0 {
		next.NotBefore = q.currentTime().Add(decision.Delay)
	}
	if next.Metadata == nil {
		next.Metadata = map[string]any{}
	}
	next.Metadata["job_id"] = job.ID
	q.pending = append(q.pending, next)
	return nil
}

// DeadLetter records a dead-letter transition.
func (q *MemoryQueue) DeadLetter(ctx context.Context, job Job, decision ScheduleDecision) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.deadLettered = append(q.deadLettered, ScheduleRecord{
		Job:      copyJob(job),
		Result:   copyResult(decision.Result),
		Decision: copyScheduleDecision(decision),
	})
	return nil
}

// Stop records a stopped transition.
func (q *MemoryQueue) Stop(ctx context.Context, job Job, result Result, decision ScheduleDecision) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.stopped = append(q.stopped, ScheduleRecord{
		Job:      copyJob(job),
		Result:   copyResult(result),
		Decision: copyScheduleDecision(decision),
	})
	return nil
}

// Snapshot returns a defensive copy of queue state.
func (q *MemoryQueue) Snapshot() MemoryQueueSnapshot {
	if q == nil {
		return MemoryQueueSnapshot{}
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	return MemoryQueueSnapshot{
		Pending:      copyJobs(q.pending),
		Completed:    copyCompletedRecords(q.completed),
		Retried:      copyScheduleRecords(q.retried),
		DeadLettered: copyScheduleRecords(q.deadLettered),
		Stopped:      copyScheduleRecords(q.stopped),
	}
}

func (q *MemoryQueue) currentTime() time.Time {
	if q.now == nil {
		return time.Now()
	}
	return q.now()
}

func ctxErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

func copyJobs(in []Job) []Job {
	out := make([]Job, len(in))
	for i, job := range in {
		out[i] = copyJob(job)
	}
	return out
}

func copyCompletedRecords(in []CompletedRecord) []CompletedRecord {
	out := make([]CompletedRecord, len(in))
	for i, record := range in {
		out[i] = CompletedRecord{Job: copyJob(record.Job), Result: copyResult(record.Result)}
	}
	return out
}

func copyScheduleRecords(in []ScheduleRecord) []ScheduleRecord {
	out := make([]ScheduleRecord, len(in))
	for i, record := range in {
		out[i] = ScheduleRecord{
			Job:      copyJob(record.Job),
			Result:   copyResult(record.Result),
			Decision: copyScheduleDecision(record.Decision),
		}
	}
	return out
}
