package react

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"time"
)

var (
	// ErrDeferredMemoryWorkVisibilityTimeoutRequired reports an invalid local queue visibility timeout.
	ErrDeferredMemoryWorkVisibilityTimeoutRequired = errors.New("react: deferred memory work visibility timeout is required")
	// ErrDeferredMemoryWorkDeliveryNotFound reports a terminal transition for a stale local queue delivery.
	ErrDeferredMemoryWorkDeliveryNotFound = errors.New("react: deferred memory work delivery not found")
)

// MemoryDeferredMemoryWorkQueue is an in-process DeferredMemoryWorkQueue.
//
// It is intended for tests, local workers, and single-process deployments. It
// records queue transitions for inspection, but it does not provide durability,
// distributed leases, or cross-process exactly-once guarantees. Use
// NewMemoryDeferredMemoryWorkQueueWithVisibilityTimeout when local workers
// need timed re-delivery for dequeued jobs that never reach a terminal
// transition.
type MemoryDeferredMemoryWorkQueue struct {
	mu                sync.Mutex
	pending           []DeferredMemoryWorkJob
	inFlight          []MemoryDeferredMemoryWorkQueueInFlight
	completed         []MemoryDeferredMemoryWorkQueueRecord
	retried           []MemoryDeferredMemoryWorkQueueRecord
	deadLettered      []MemoryDeferredMemoryWorkQueueRecord
	stopped           []MemoryDeferredMemoryWorkQueueRecord
	visibilityTimeout time.Duration
	nextDeliveryID    uint64
	now               func() time.Time
}

var _ DeferredMemoryWorkQueue = (*MemoryDeferredMemoryWorkQueue)(nil)

// MemoryDeferredMemoryWorkQueueRecord is one observed in-memory queue transition.
type MemoryDeferredMemoryWorkQueueRecord struct {
	Job      DeferredMemoryWorkJob              `json:"job"`
	Report   DeferredMemoryWorkReport           `json:"report,omitempty"`
	Decision DeferredMemoryWorkScheduleDecision `json:"decision,omitempty"`
}

// MemoryDeferredMemoryWorkQueueInFlight is one locally dequeued job awaiting a terminal transition.
type MemoryDeferredMemoryWorkQueueInFlight struct {
	Job       DeferredMemoryWorkJob `json:"job"`
	VisibleAt time.Time             `json:"visible_at"`
}

// MemoryDeferredMemoryWorkQueueSnapshot is a defensive copy of the queue state.
type MemoryDeferredMemoryWorkQueueSnapshot struct {
	Pending      []DeferredMemoryWorkJob                 `json:"pending,omitempty"`
	InFlight     []MemoryDeferredMemoryWorkQueueInFlight `json:"in_flight,omitempty"`
	Completed    []MemoryDeferredMemoryWorkQueueRecord   `json:"completed,omitempty"`
	Retried      []MemoryDeferredMemoryWorkQueueRecord   `json:"retried,omitempty"`
	DeadLettered []MemoryDeferredMemoryWorkQueueRecord   `json:"dead_lettered,omitempty"`
	Stopped      []MemoryDeferredMemoryWorkQueueRecord   `json:"stopped,omitempty"`
}

// NewMemoryDeferredMemoryWorkQueue creates an in-process queue seeded with jobs.
func NewMemoryDeferredMemoryWorkQueue(jobs ...DeferredMemoryWorkJob) *MemoryDeferredMemoryWorkQueue {
	queue := &MemoryDeferredMemoryWorkQueue{}
	for _, job := range jobs {
		queue.pending = append(queue.pending, memoryDeferredMemoryWorkQueuePendingJob(job))
	}
	return queue
}

// NewMemoryDeferredMemoryWorkQueueWithVisibilityTimeout creates an in-process queue with timed re-delivery.
func NewMemoryDeferredMemoryWorkQueueWithVisibilityTimeout(timeout time.Duration, jobs ...DeferredMemoryWorkJob) (*MemoryDeferredMemoryWorkQueue, error) {
	if timeout <= 0 {
		return nil, ErrDeferredMemoryWorkVisibilityTimeoutRequired
	}
	queue := NewMemoryDeferredMemoryWorkQueue(jobs...)
	queue.visibilityTimeout = timeout
	return queue, nil
}

// Enqueue appends a job to the in-memory pending queue.
func (q *MemoryDeferredMemoryWorkQueue) Enqueue(ctx context.Context, job DeferredMemoryWorkJob) error {
	if err := deferredMemoryWorkQueueContextErr(ctx); err != nil {
		return err
	}
	if q == nil {
		return ErrDeferredMemoryWorkQueueRequired
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.pending = append(q.pending, memoryDeferredMemoryWorkQueuePendingJob(job))
	return nil
}

// Dequeue removes the next pending job, if one is available.
func (q *MemoryDeferredMemoryWorkQueue) Dequeue(ctx context.Context) (DeferredMemoryWorkJob, bool, error) {
	if err := deferredMemoryWorkQueueContextErr(ctx); err != nil {
		return DeferredMemoryWorkJob{}, false, err
	}
	if q == nil {
		return DeferredMemoryWorkJob{}, false, ErrDeferredMemoryWorkQueueRequired
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	now := q.currentTime()
	q.requeueExpiredInFlightLocked(now)
	if len(q.pending) == 0 {
		return DeferredMemoryWorkJob{}, false, nil
	}
	job := q.pending[0]
	q.pending = q.pending[1:]
	if q.visibilityTimeout > 0 {
		job = q.withDeliveryIDLocked(job)
		q.inFlight = append(q.inFlight, MemoryDeferredMemoryWorkQueueInFlight{
			Job:       copyDeferredMemoryWorkJob(job),
			VisibleAt: now.Add(q.visibilityTimeout),
		})
	}
	return copyDeferredMemoryWorkJob(job), true, nil
}

// Complete records successful handling of a dequeued job.
func (q *MemoryDeferredMemoryWorkQueue) Complete(ctx context.Context, job DeferredMemoryWorkJob, report DeferredMemoryWorkReport) error {
	if err := deferredMemoryWorkQueueContextErr(ctx); err != nil {
		return err
	}
	if q == nil {
		return ErrDeferredMemoryWorkQueueRequired
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	job, err := q.clearActiveJobLocked(job)
	if err != nil {
		return err
	}
	q.completed = append(q.completed, MemoryDeferredMemoryWorkQueueRecord{
		Job:    copyDeferredMemoryWorkJob(job),
		Report: copyDeferredMemoryWorkReport(report),
	})
	return nil
}

// Retry records a retry decision and appends the job back to the pending queue.
func (q *MemoryDeferredMemoryWorkQueue) Retry(ctx context.Context, job DeferredMemoryWorkJob, decision DeferredMemoryWorkScheduleDecision) error {
	if err := deferredMemoryWorkQueueContextErr(ctx); err != nil {
		return err
	}
	if q == nil {
		return ErrDeferredMemoryWorkQueueRequired
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	job, err := q.clearActiveJobLocked(job)
	if err != nil {
		return err
	}
	record := MemoryDeferredMemoryWorkQueueRecord{
		Job:      copyDeferredMemoryWorkJob(job),
		Decision: copyDeferredMemoryWorkScheduleDecision(decision),
	}
	q.retried = append(q.retried, record)
	q.pending = append(q.pending, deferredMemoryWorkRetryJob(job, decision))
	return nil
}

// DeadLetter records that a failed job was moved to the local dead-letter set.
func (q *MemoryDeferredMemoryWorkQueue) DeadLetter(ctx context.Context, job DeferredMemoryWorkJob, decision DeferredMemoryWorkScheduleDecision) error {
	if err := deferredMemoryWorkQueueContextErr(ctx); err != nil {
		return err
	}
	if q == nil {
		return ErrDeferredMemoryWorkQueueRequired
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	job, err := q.clearActiveJobLocked(job)
	if err != nil {
		return err
	}
	q.deadLettered = append(q.deadLettered, MemoryDeferredMemoryWorkQueueRecord{
		Job:      copyDeferredMemoryWorkJob(job),
		Decision: copyDeferredMemoryWorkScheduleDecision(decision),
	})
	return nil
}

// Stop records that the host stopped scheduling a job.
func (q *MemoryDeferredMemoryWorkQueue) Stop(ctx context.Context, job DeferredMemoryWorkJob, report DeferredMemoryWorkReport, decision DeferredMemoryWorkScheduleDecision) error {
	if err := deferredMemoryWorkQueueContextErr(ctx); err != nil {
		return err
	}
	if q == nil {
		return ErrDeferredMemoryWorkQueueRequired
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	job, err := q.clearActiveJobLocked(job)
	if err != nil {
		return err
	}
	q.stopped = append(q.stopped, MemoryDeferredMemoryWorkQueueRecord{
		Job:      copyDeferredMemoryWorkJob(job),
		Report:   copyDeferredMemoryWorkReport(report),
		Decision: copyDeferredMemoryWorkScheduleDecision(decision),
	})
	return nil
}

// Snapshot returns a defensive copy of the in-memory queue state.
func (q *MemoryDeferredMemoryWorkQueue) Snapshot() MemoryDeferredMemoryWorkQueueSnapshot {
	if q == nil {
		return MemoryDeferredMemoryWorkQueueSnapshot{}
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	return MemoryDeferredMemoryWorkQueueSnapshot{
		Pending:      copyDeferredMemoryWorkJobs(q.pending),
		InFlight:     copyMemoryDeferredMemoryWorkQueueInFlight(q.inFlight),
		Completed:    copyMemoryDeferredMemoryWorkQueueRecords(q.completed),
		Retried:      copyMemoryDeferredMemoryWorkQueueRecords(q.retried),
		DeadLettered: copyMemoryDeferredMemoryWorkQueueRecords(q.deadLettered),
		Stopped:      copyMemoryDeferredMemoryWorkQueueRecords(q.stopped),
	}
}

func deferredMemoryWorkRetryJob(job DeferredMemoryWorkJob, decision DeferredMemoryWorkScheduleDecision) DeferredMemoryWorkJob {
	out := copyDeferredMemoryWorkJob(job)
	if decision.NextAttempt > 0 {
		out.Attempt = decision.NextAttempt
	} else {
		out.Attempt++
	}
	if decision.MaxAttempts > 0 {
		out.MaxAttempts = decision.MaxAttempts
	}
	out.Metadata = copyAnyMap(job.Metadata)
	for key, value := range decision.Metadata {
		if out.Metadata == nil {
			out.Metadata = map[string]any{}
		}
		out.Metadata[key] = value
	}
	return normalizeDeferredMemoryWorkJob(out)
}

func (q *MemoryDeferredMemoryWorkQueue) currentTime() time.Time {
	if q.now != nil {
		return q.now()
	}
	return time.Now()
}

func (q *MemoryDeferredMemoryWorkQueue) withDeliveryIDLocked(job DeferredMemoryWorkJob) DeferredMemoryWorkJob {
	q.nextDeliveryID++
	out := copyDeferredMemoryWorkJob(job)
	out.DeliveryID = strconv.FormatUint(q.nextDeliveryID, 10)
	return out
}

func (q *MemoryDeferredMemoryWorkQueue) requeueExpiredInFlightLocked(now time.Time) {
	if q.visibilityTimeout <= 0 || len(q.inFlight) == 0 {
		return
	}
	expired := make([]DeferredMemoryWorkJob, 0, len(q.inFlight))
	kept := q.inFlight[:0]
	for _, record := range q.inFlight {
		if !record.VisibleAt.After(now) {
			expired = append(expired, stripMemoryDeferredMemoryWorkQueueDeliveryID(record.Job))
			continue
		}
		kept = append(kept, record)
	}
	q.inFlight = kept
	if len(expired) == 0 {
		return
	}
	pending := make([]DeferredMemoryWorkJob, 0, len(expired)+len(q.pending))
	pending = append(pending, expired...)
	pending = append(pending, q.pending...)
	q.pending = pending
}

func (q *MemoryDeferredMemoryWorkQueue) clearActiveJobLocked(job DeferredMemoryWorkJob) (DeferredMemoryWorkJob, error) {
	q.requeueExpiredInFlightLocked(q.currentTime())
	stripped := stripMemoryDeferredMemoryWorkQueueDeliveryID(job)
	if q.visibilityTimeout > 0 {
		if !q.removeInFlightLocked(job) {
			return DeferredMemoryWorkJob{}, ErrDeferredMemoryWorkDeliveryNotFound
		}
		return stripped, nil
	}
	q.removePendingLocked(stripped)
	return stripped, nil
}

func (q *MemoryDeferredMemoryWorkQueue) removeInFlightLocked(job DeferredMemoryWorkJob) bool {
	index := -1
	for i, record := range q.inFlight {
		if sameMemoryDeferredMemoryWorkQueueDelivery(record.Job, job) {
			index = i
			break
		}
	}
	if index < 0 {
		return false
	}
	copy(q.inFlight[index:], q.inFlight[index+1:])
	q.inFlight = q.inFlight[:len(q.inFlight)-1]
	return true
}

func (q *MemoryDeferredMemoryWorkQueue) removePendingLocked(job DeferredMemoryWorkJob) {
	index := -1
	for i, pending := range q.pending {
		if sameMemoryDeferredMemoryWorkQueueDelivery(pending, job) {
			index = i
			break
		}
	}
	if index < 0 {
		return
	}
	copy(q.pending[index:], q.pending[index+1:])
	q.pending = q.pending[:len(q.pending)-1]
}

func sameMemoryDeferredMemoryWorkQueueDelivery(a, b DeferredMemoryWorkJob) bool {
	aDelivery := memoryDeferredMemoryWorkQueueDeliveryID(a)
	bDelivery := memoryDeferredMemoryWorkQueueDeliveryID(b)
	if aDelivery != "" || bDelivery != "" {
		return aDelivery != "" && aDelivery == bDelivery
	}
	return a.ID != "" && a.ID == b.ID
}

func memoryDeferredMemoryWorkQueueDeliveryID(job DeferredMemoryWorkJob) string {
	return job.DeliveryID
}

func stripMemoryDeferredMemoryWorkQueueDeliveryID(job DeferredMemoryWorkJob) DeferredMemoryWorkJob {
	out := copyDeferredMemoryWorkJob(job)
	out.DeliveryID = ""
	return out
}

func memoryDeferredMemoryWorkQueuePendingJob(job DeferredMemoryWorkJob) DeferredMemoryWorkJob {
	return stripMemoryDeferredMemoryWorkQueueDeliveryID(normalizeDeferredMemoryWorkJob(copyDeferredMemoryWorkJob(job)))
}

func deferredMemoryWorkQueueContextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

func copyDeferredMemoryWorkJobs(in []DeferredMemoryWorkJob) []DeferredMemoryWorkJob {
	if len(in) == 0 {
		return nil
	}
	out := make([]DeferredMemoryWorkJob, len(in))
	for i, job := range in {
		out[i] = copyDeferredMemoryWorkJob(job)
	}
	return out
}

func copyMemoryDeferredMemoryWorkQueueInFlight(in []MemoryDeferredMemoryWorkQueueInFlight) []MemoryDeferredMemoryWorkQueueInFlight {
	if len(in) == 0 {
		return nil
	}
	out := make([]MemoryDeferredMemoryWorkQueueInFlight, len(in))
	for i, record := range in {
		out[i] = MemoryDeferredMemoryWorkQueueInFlight{
			Job:       stripMemoryDeferredMemoryWorkQueueDeliveryID(record.Job),
			VisibleAt: record.VisibleAt,
		}
	}
	return out
}

func copyMemoryDeferredMemoryWorkQueueRecords(in []MemoryDeferredMemoryWorkQueueRecord) []MemoryDeferredMemoryWorkQueueRecord {
	if len(in) == 0 {
		return nil
	}
	out := make([]MemoryDeferredMemoryWorkQueueRecord, len(in))
	for i, record := range in {
		out[i] = MemoryDeferredMemoryWorkQueueRecord{
			Job:      copyDeferredMemoryWorkJob(record.Job),
			Report:   copyDeferredMemoryWorkReport(record.Report),
			Decision: copyDeferredMemoryWorkScheduleDecision(record.Decision),
		}
	}
	return out
}
