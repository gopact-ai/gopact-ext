// Package scheduler provides reusable leased background worker primitives.
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/gopact-ai/gopact"
)

var (
	ErrQueueRequired                = errors.New("scheduler: queue is required")
	ErrHandlerRequired              = errors.New("scheduler: handler is required")
	ErrScheduleDeciderRequired      = errors.New("scheduler: schedule decider is required")
	ErrScheduleDecisionRequired     = errors.New("scheduler: schedule decision is required")
	ErrDrainLimitRequired           = errors.New("scheduler: drain limit is required")
	ErrLeaseBackendRequired         = errors.New("scheduler: lease backend is required")
	ErrLeaseRenewalIntervalRequired = errors.New("scheduler: lease renewal interval is required")
	ErrLeaseLost                    = errors.New("scheduler: lease lost")
	ErrJobDeadLettered              = errors.New("scheduler: job dead-lettered")
	ErrJobStoppedAfterFailure       = errors.New("scheduler: job stopped after failure")
	ErrRetryPolicyInvalid           = errors.New("scheduler: retry policy is invalid")
)

const (
	defaultRetryMaxAttempts   = 3
	defaultRetryInitialDelay  = 100 * time.Millisecond
	defaultRetryMaxDelay      = 30 * time.Second
	defaultRetryBackoffFactor = 2

	VerificationCheckSchedule        = "scheduler-job-schedule"
	VerificationEvidenceTypeSchedule = "scheduler_schedule"
)

// Job is one queued background unit.
type Job struct {
	ID          string
	DeliveryID  string
	Payload     any
	Attempt     int
	MaxAttempts int
	NotBefore   time.Time
	Metadata    map[string]any
}

// JobStatus is the observable result class for one handler pass.
type JobStatus string

const (
	JobSucceeded JobStatus = "succeeded"
	JobFailed    JobStatus = "failed"
)

// Result describes one handler pass.
type Result struct {
	Status   JobStatus
	Output   any
	Error    string
	Metadata map[string]any
}

// Queue is the host-owned queue contract consumed by Worker.
type Queue interface {
	Dequeue(ctx context.Context) (Job, bool, error)
	Complete(ctx context.Context, job Job, result Result) error
	Retry(ctx context.Context, job Job, decision ScheduleDecision) error
	DeadLetter(ctx context.Context, job Job, decision ScheduleDecision) error
	Stop(ctx context.Context, job Job, result Result, decision ScheduleDecision) error
}

// Handler executes one dequeued job.
type Handler interface {
	HandleJob(ctx context.Context, job Job) (Result, error)
}

// HandlerFunc adapts a function into a Handler.
type HandlerFunc func(context.Context, Job) (Result, error)

// HandleJob implements Handler.
func (f HandlerFunc) HandleJob(ctx context.Context, job Job) (Result, error) {
	if f == nil {
		return Result{}, ErrHandlerRequired
	}
	if ctx == nil {
		ctx = context.TODO()
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	return f(ctx, copyJob(job))
}

// ScheduleAction describes the next queue transition for a failed job pass.
type ScheduleAction string

const (
	ScheduleStop       ScheduleAction = "stop"
	ScheduleRetry      ScheduleAction = "retry"
	ScheduleDeadLetter ScheduleAction = "dead_letter"
)

// ScheduleRequest is passed to a retry/stop/DLQ decider.
type ScheduleRequest struct {
	Job         Job
	Result      Result
	Attempt     int
	MaxAttempts int
	Metadata    map[string]any
}

// ScheduleDecision is an observed scheduling decision after one handler pass.
type ScheduleDecision struct {
	Job         Job
	Result      Result
	Action      ScheduleAction
	Attempt     int
	NextAttempt int
	MaxAttempts int
	Delay       time.Duration
	Reason      string
	Metadata    map[string]any
}

// ScheduleDecider decides the next queue transition for a failed job.
type ScheduleDecider interface {
	DecideSchedule(ctx context.Context, request ScheduleRequest) (ScheduleDecision, error)
}

// ScheduleDeciderFunc adapts a function into ScheduleDecider.
type ScheduleDeciderFunc func(context.Context, ScheduleRequest) (ScheduleDecision, error)

// DecideSchedule implements ScheduleDecider.
func (f ScheduleDeciderFunc) DecideSchedule(ctx context.Context, request ScheduleRequest) (ScheduleDecision, error) {
	if f == nil {
		return ScheduleDecision{}, ErrScheduleDeciderRequired
	}
	if ctx == nil {
		ctx = context.TODO()
	}
	if err := ctx.Err(); err != nil {
		return ScheduleDecision{}, err
	}
	return f(ctx, copyScheduleRequest(request))
}

// RetryPolicy configures the default retry/backoff decider.
type RetryPolicy struct {
	MaxAttempts   int
	InitialDelay  time.Duration
	MaxDelay      time.Duration
	BackoffFactor int
	Reason        string
	Metadata      map[string]any
}

// RetryDecider retries failed jobs with capped exponential backoff.
type RetryDecider struct {
	policy RetryPolicy
}

var _ ScheduleDecider = (*RetryDecider)(nil)

// NewRetryDecider creates a default retry/backoff decider.
func NewRetryDecider(policy RetryPolicy) (*RetryDecider, error) {
	if err := validateRetryPolicy(policy); err != nil {
		return nil, err
	}
	policy.Metadata = copyAnyMap(policy.Metadata)
	return &RetryDecider{policy: policy}, nil
}

// DecideSchedule implements ScheduleDecider.
func (d *RetryDecider) DecideSchedule(ctx context.Context, request ScheduleRequest) (ScheduleDecision, error) {
	if d == nil {
		return ScheduleDecision{}, ErrScheduleDeciderRequired
	}
	if ctx == nil {
		ctx = context.TODO()
	}
	if err := ctx.Err(); err != nil {
		return ScheduleDecision{}, err
	}
	request = copyScheduleRequest(request)
	attempt := requestAttempt(request)
	if attempt <= 0 {
		attempt = 1
	}
	maxAttempts := d.maxAttempts(request)
	decision := ScheduleDecision{
		Job:         copyJob(request.Job),
		Result:      copyResult(request.Result),
		Attempt:     attempt,
		MaxAttempts: maxAttempts,
		Metadata:    d.decisionMetadata(request.Metadata),
	}
	if attempt >= maxAttempts {
		decision.Action = ScheduleDeadLetter
		decision.Reason = d.reason("max attempts reached")
		return decision, nil
	}
	decision.Action = ScheduleRetry
	decision.NextAttempt = attempt + 1
	decision.Delay = d.delay(attempt)
	decision.Reason = d.reason("retry scheduled")
	return decision, nil
}

// Worker consumes queued jobs one at a time.
type Worker struct {
	queue        Queue
	handler      Handler
	decider      ScheduleDecider
	recorder     *gopact.VerificationRecorder
	leaseBackend gopact.LeaseBackend
	leaseRequest gopact.LeaseRequest
	useLease     bool
	renewLease   bool
	renewEvery   time.Duration
}

// WorkerOption configures Worker.
type WorkerOption func(*Worker)

// WithScheduleDecider configures the retry/stop/DLQ decider.
func WithScheduleDecider(decider ScheduleDecider) WorkerOption {
	return func(w *Worker) {
		w.decider = decider
	}
}

// WithRecorder records schedule decisions as verification evidence.
func WithRecorder(recorder *gopact.VerificationRecorder) WorkerOption {
	return func(w *Worker) {
		w.recorder = recorder
	}
}

// WithLease gates each RunOnce call with a worker ownership lease.
func WithLease(leases gopact.LeaseBackend, request gopact.LeaseRequest) WorkerOption {
	return func(w *Worker) {
		w.leaseBackend = leases
		w.leaseRequest = request
		w.leaseRequest.Metadata = copyAnyMap(request.Metadata)
		w.useLease = true
	}
}

// WithLeaseRenewalInterval renews a held RunOnce lease while the handler runs.
func WithLeaseRenewalInterval(interval time.Duration) WorkerOption {
	return func(w *Worker) {
		w.renewLease = true
		w.renewEvery = interval
	}
}

// NewWorker creates a background worker.
func NewWorker(queue Queue, handler Handler, opts ...WorkerOption) (*Worker, error) {
	if queue == nil {
		return nil, ErrQueueRequired
	}
	if handler == nil {
		return nil, ErrHandlerRequired
	}
	worker := &Worker{queue: queue, handler: handler}
	for _, opt := range opts {
		if opt != nil {
			opt(worker)
		}
	}
	if worker.useLease && worker.leaseBackend == nil {
		return nil, ErrLeaseBackendRequired
	}
	if worker.renewLease && worker.renewEvery <= 0 {
		return nil, ErrLeaseRenewalIntervalRequired
	}
	if worker.renewLease && !worker.useLease {
		return nil, ErrLeaseBackendRequired
	}
	if worker.decider == nil {
		decider, err := NewRetryDecider(RetryPolicy{})
		if err != nil {
			return nil, err
		}
		worker.decider = decider
	}
	return worker, nil
}

// WorkerResult is the observable result of one RunOnce call.
type WorkerResult struct {
	Dequeued bool
	Job      Job
	Result   Result
	Decision ScheduleDecision
}

// DrainResult summarizes a bounded worker drain.
type DrainResult struct {
	Dequeued     int
	Completed    int
	Retried      int
	DeadLettered int
	Stopped      int
	Results      []WorkerResult
}

// Drain runs RunOnce until the queue is empty, limit is reached, or a terminal error occurs.
func (w *Worker) Drain(ctx context.Context, limit int) (DrainResult, error) {
	if limit <= 0 {
		return DrainResult{}, ErrDrainLimitRequired
	}
	if ctx == nil {
		ctx = context.TODO()
	}
	var drain DrainResult
	for i := 0; i < limit; i++ {
		result, err := w.RunOnce(ctx)
		if !result.Dequeued {
			return drain, err
		}
		drain.record(result)
		if err != nil {
			return drain, err
		}
	}
	return drain, nil
}

// RunOnce dequeues at most one job, handles it, and applies the queue transition.
func (w *Worker) RunOnce(ctx context.Context) (result WorkerResult, err error) {
	if w == nil || w.queue == nil {
		return WorkerResult{}, ErrQueueRequired
	}
	if w.handler == nil {
		return WorkerResult{}, ErrHandlerRequired
	}
	if ctx == nil {
		ctx = context.TODO()
	}
	lease, err := w.acquireLease(ctx)
	if err != nil {
		return WorkerResult{}, err
	}
	if lease != nil {
		defer func() {
			if releaseErr := lease.release(context.WithoutCancel(ctx)); releaseErr != nil {
				err = errors.Join(err, releaseErr)
			}
		}()
	}

	job, ok, err := w.queue.Dequeue(ctx)
	if err != nil {
		return WorkerResult{}, fmt.Errorf("scheduler: dequeue job: %w", err)
	}
	if !ok {
		return WorkerResult{}, nil
	}
	result.Dequeued = true
	result.Job = copyJob(job)

	handlerResult, handlerErr := w.handler.HandleJob(ctx, job)
	result.Result = normalizeResult(handlerResult, handlerErr)
	if result.Result.Status == JobSucceeded {
		if err := w.ensureLeaseHeld(ctx, lease); err != nil {
			return result, err
		}
		return result, w.queue.Complete(ctx, job, result.Result)
	}

	decision, err := w.decide(ctx, job, result.Result)
	if err != nil {
		return result, err
	}
	result.Decision = decision
	if err := w.recordSchedule(decision); err != nil {
		return result, err
	}
	if err := w.ensureLeaseHeld(ctx, lease); err != nil {
		return result, err
	}
	switch decision.Action {
	case ScheduleRetry:
		return result, w.queue.Retry(ctx, job, decision)
	case ScheduleDeadLetter:
		if err := w.queue.DeadLetter(ctx, job, decision); err != nil {
			return result, err
		}
		return result, ErrJobDeadLettered
	case ScheduleStop:
		if err := w.queue.Stop(ctx, job, result.Result, decision); err != nil {
			return result, err
		}
		return result, ErrJobStoppedAfterFailure
	default:
		return result, ErrScheduleDecisionRequired
	}
}

func (w *Worker) decide(ctx context.Context, job Job, result Result) (ScheduleDecision, error) {
	if w.decider == nil {
		return ScheduleDecision{}, ErrScheduleDeciderRequired
	}
	attempt := job.Attempt
	if attempt <= 0 {
		attempt = 1
	}
	decision, err := w.decider.DecideSchedule(ctx, ScheduleRequest{
		Job:         copyJob(job),
		Result:      copyResult(result),
		Attempt:     attempt,
		MaxAttempts: job.MaxAttempts,
		Metadata:    copyAnyMap(job.Metadata),
	})
	if err != nil {
		return ScheduleDecision{}, err
	}
	decision.Job = copyJob(job)
	decision.Result = copyResult(result)
	if decision.Attempt <= 0 {
		decision.Attempt = attempt
	}
	if decision.MaxAttempts <= 0 {
		decision.MaxAttempts = job.MaxAttempts
	}
	if !decision.Action.valid() {
		return ScheduleDecision{}, ErrScheduleDecisionRequired
	}
	if decision.Action == ScheduleRetry && decision.NextAttempt <= 0 {
		decision.NextAttempt = decision.Attempt + 1
	}
	decision.Metadata = copyAnyMap(decision.Metadata)
	return decision, nil
}

func (w *Worker) recordSchedule(decision ScheduleDecision) error {
	if w.recorder == nil {
		return nil
	}
	return w.recorder.Record(scheduleCheck(decision))
}

func (w *Worker) acquireLease(ctx context.Context) (*heldLease, error) {
	if !w.useLease {
		return nil, nil
	}
	record, err := w.leaseBackend.AcquireLease(ctx, w.leaseRequest)
	if err != nil {
		return nil, err
	}
	lease := &heldLease{
		backend: w.leaseBackend,
		record:  record,
		ttl:     w.leaseRequest.TTL,
	}
	if w.renewLease {
		lease.startRenewal(ctx, w.renewEvery)
	}
	return lease, nil
}

func (w *Worker) ensureLeaseHeld(ctx context.Context, lease *heldLease) error {
	if lease == nil {
		return nil
	}
	return lease.ensureHeld(ctx)
}

func (d *DrainResult) record(result WorkerResult) {
	d.Dequeued++
	d.Results = append(d.Results, copyWorkerResult(result))
	switch result.Decision.Action {
	case ScheduleRetry:
		d.Retried++
	case ScheduleDeadLetter:
		d.DeadLettered++
	case ScheduleStop:
		d.Stopped++
	default:
		if result.Result.Status == JobSucceeded {
			d.Completed++
		}
	}
}

func normalizeResult(result Result, err error) Result {
	out := copyResult(result)
	if err != nil {
		out.Status = JobFailed
		out.Error = err.Error()
		return out
	}
	if out.Status == "" {
		out.Status = JobSucceeded
	}
	return out
}

func validateRetryPolicy(policy RetryPolicy) error {
	switch {
	case policy.MaxAttempts < 0:
		return fmt.Errorf("scheduler: max attempts must be non-negative: %w", ErrRetryPolicyInvalid)
	case policy.InitialDelay < 0:
		return fmt.Errorf("scheduler: initial delay must be non-negative: %w", ErrRetryPolicyInvalid)
	case policy.MaxDelay < 0:
		return fmt.Errorf("scheduler: max delay must be non-negative: %w", ErrRetryPolicyInvalid)
	case policy.BackoffFactor < 0:
		return fmt.Errorf("scheduler: backoff factor must be non-negative: %w", ErrRetryPolicyInvalid)
	default:
		return nil
	}
}

func requestAttempt(request ScheduleRequest) int {
	if request.Attempt > 0 {
		return request.Attempt
	}
	return request.Job.Attempt
}

func (d *RetryDecider) maxAttempts(request ScheduleRequest) int {
	if d.policy.MaxAttempts > 0 {
		return d.policy.MaxAttempts
	}
	if request.MaxAttempts > 0 {
		return request.MaxAttempts
	}
	if request.Job.MaxAttempts > 0 {
		return request.Job.MaxAttempts
	}
	return defaultRetryMaxAttempts
}

func (d *RetryDecider) delay(attempt int) time.Duration {
	delay := d.initialDelay()
	factor := d.backoffFactor()
	maxDelay := d.maxDelay()
	for i := 1; i < attempt; i++ {
		if factor <= 1 {
			return capRetryDelay(delay, maxDelay)
		}
		if delay >= maxDelay || delay > maxDelay/time.Duration(factor) {
			return maxDelay
		}
		delay *= time.Duration(factor)
	}
	return capRetryDelay(delay, maxDelay)
}

func (d *RetryDecider) initialDelay() time.Duration {
	if d.policy.InitialDelay > 0 {
		return d.policy.InitialDelay
	}
	return defaultRetryInitialDelay
}

func (d *RetryDecider) maxDelay() time.Duration {
	if d.policy.MaxDelay > 0 {
		return d.policy.MaxDelay
	}
	return defaultRetryMaxDelay
}

func (d *RetryDecider) backoffFactor() int {
	if d.policy.BackoffFactor > 0 {
		return d.policy.BackoffFactor
	}
	return defaultRetryBackoffFactor
}

func (d *RetryDecider) reason(fallback string) string {
	if d.policy.Reason != "" {
		return d.policy.Reason
	}
	return fallback
}

func (d *RetryDecider) decisionMetadata(requestMetadata map[string]any) map[string]any {
	metadata := copyAnyMap(requestMetadata)
	for key, value := range d.policy.Metadata {
		if metadata == nil {
			metadata = map[string]any{}
		}
		metadata[key] = copyAnyValue(value)
	}
	return metadata
}

func capRetryDelay(delay, maxDelay time.Duration) time.Duration {
	if delay > maxDelay {
		return maxDelay
	}
	return delay
}

func (a ScheduleAction) valid() bool {
	switch a {
	case ScheduleStop, ScheduleRetry, ScheduleDeadLetter:
		return true
	default:
		return false
	}
}

// RecordScheduleCheck records an already-observed schedule decision.
func RecordScheduleCheck(recorder *gopact.VerificationRecorder, decision ScheduleDecision) error {
	if recorder == nil {
		return errors.New("scheduler: verification recorder is nil")
	}
	if !decision.Action.valid() {
		return ErrScheduleDecisionRequired
	}
	check := scheduleCheck(decision)
	if err := recorder.Record(check); err != nil {
		return err
	}
	if check.Status == gopact.VerificationStatusFailed {
		if decision.Action == ScheduleDeadLetter {
			return ErrJobDeadLettered
		}
		return ErrJobStoppedAfterFailure
	}
	return nil
}

func scheduleCheck(decision ScheduleDecision) gopact.VerificationCheck {
	status := scheduleStatus(decision)
	return gopact.VerificationCheck{
		ID:      VerificationCheckSchedule + ":" + scheduleRef(decision),
		Name:    "scheduler job schedule",
		Status:  status,
		Summary: scheduleSummary(status, decision),
		Evidence: []gopact.VerificationEvidence{
			{
				Type:     VerificationEvidenceTypeSchedule,
				Ref:      scheduleRef(decision),
				Summary:  scheduleEvidenceSummary(decision),
				Metadata: scheduleMetadata(decision),
			},
		},
		Metadata: scheduleMetadata(decision),
	}
}

func scheduleStatus(decision ScheduleDecision) gopact.VerificationStatus {
	switch decision.Action {
	case ScheduleDeadLetter:
		return gopact.VerificationStatusFailed
	case ScheduleStop:
		if decision.Result.Status == JobFailed {
			return gopact.VerificationStatusFailed
		}
		return gopact.VerificationStatusSkipped
	default:
		return gopact.VerificationStatusPassed
	}
}

func scheduleSummary(status gopact.VerificationStatus, decision ScheduleDecision) string {
	switch status {
	case gopact.VerificationStatusFailed:
		if decision.Action == ScheduleDeadLetter {
			return "job dead-lettered"
		}
		return "job stopped after failure"
	case gopact.VerificationStatusSkipped:
		return "job scheduling stopped"
	default:
		if decision.Action == ScheduleRetry && decision.NextAttempt > 0 {
			return fmt.Sprintf("job retry scheduled for attempt %d", decision.NextAttempt)
		}
		return "job schedule decision recorded"
	}
}

func scheduleEvidenceSummary(decision ScheduleDecision) string {
	if decision.Reason != "" {
		return decision.Reason
	}
	return string(decision.Action)
}

func scheduleMetadata(decision ScheduleDecision) map[string]any {
	metadata := map[string]any{
		"ref":     scheduleRef(decision),
		"job_id":  decision.Job.ID,
		"action":  string(decision.Action),
		"attempt": decision.Attempt,
		"status":  string(decision.Result.Status),
	}
	if decision.NextAttempt > 0 {
		metadata["next_attempt"] = decision.NextAttempt
	}
	if decision.MaxAttempts > 0 {
		metadata["max_attempts"] = decision.MaxAttempts
	}
	if decision.Delay > 0 {
		metadata["delay_ms"] = decision.Delay.Milliseconds()
	}
	if decision.Reason != "" {
		metadata["reason"] = decision.Reason
	}
	if decision.Result.Error != "" {
		metadata["error"] = decision.Result.Error
	}
	if keys := supplementalMetadataKeys(decision.Metadata); len(keys) > 0 {
		metadata["metadata_keys"] = keys
	}
	mergeSupplementalMetadata(metadata, decision.Metadata)
	return metadata
}

func scheduleRef(decision ScheduleDecision) string {
	if decision.Job.ID == "" {
		return fmt.Sprintf("attempt-%d", decision.Attempt)
	}
	return fmt.Sprintf("%s:attempt-%d", decision.Job.ID, decision.Attempt)
}

func supplementalMetadataKeys(supplemental map[string]any) []string {
	if len(supplemental) == 0 {
		return nil
	}
	keys := make([]string, 0, len(supplemental))
	for key := range supplemental {
		if !reservedMetadataKey(key) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func mergeSupplementalMetadata(metadata map[string]any, supplemental map[string]any) {
	for key, value := range supplemental {
		if reservedMetadataKey(key) {
			continue
		}
		metadata[key] = copyAnyValue(value)
	}
}

func reservedMetadataKey(key string) bool {
	switch key {
	case "ref", "job_id", "action", "attempt", "next_attempt", "max_attempts", "delay_ms", "reason", "status", "error", "metadata_keys":
		return true
	default:
		return false
	}
}

type heldLease struct {
	backend gopact.LeaseBackend
	record  gopact.LeaseRecord
	ttl     time.Duration

	mu       sync.Mutex
	renewErr error
	stop     chan struct{}
	done     chan struct{}
}

func (l *heldLease) startRenewal(ctx context.Context, interval time.Duration) {
	l.stop = make(chan struct{})
	l.done = make(chan struct{})
	go func() {
		defer close(l.done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-l.stop:
				return
			case <-ctx.Done():
				l.setRenewErr(ctx.Err())
				return
			case <-ticker.C:
				if err := l.renew(context.WithoutCancel(ctx)); err != nil {
					l.setRenewErr(err)
					return
				}
			}
		}
	}()
}

func (l *heldLease) renew(ctx context.Context) error {
	l.mu.Lock()
	record := l.record
	l.mu.Unlock()
	renewed, err := l.backend.RenewLease(ctx, gopact.LeaseRenewRequest{
		Key:   record.Key,
		Owner: record.Owner,
		Token: record.Token,
		TTL:   l.ttl,
	})
	if err != nil {
		return err
	}
	l.mu.Lock()
	l.record = renewed
	l.mu.Unlock()
	return nil
}

func (l *heldLease) ensureHeld(ctx context.Context) error {
	if err := l.getRenewErr(); err != nil {
		return fmt.Errorf("%w: %v", ErrLeaseLost, err)
	}
	l.mu.Lock()
	record := l.record
	l.mu.Unlock()
	current, ok, err := l.backend.GetLease(ctx, record.Key)
	if err != nil {
		return err
	}
	if !ok || current.Owner != record.Owner || current.Token != record.Token {
		return ErrLeaseLost
	}
	return nil
}

func (l *heldLease) release(ctx context.Context) error {
	if l.stop != nil {
		close(l.stop)
		<-l.done
	}
	l.mu.Lock()
	record := l.record
	l.mu.Unlock()
	return l.backend.ReleaseLease(ctx, gopact.LeaseReleaseRequest{
		Key:   record.Key,
		Owner: record.Owner,
		Token: record.Token,
	})
}

func (l *heldLease) setRenewErr(err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.renewErr = err
}

func (l *heldLease) getRenewErr() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.renewErr
}

func copyWorkerResult(in WorkerResult) WorkerResult {
	return WorkerResult{
		Dequeued: in.Dequeued,
		Job:      copyJob(in.Job),
		Result:   copyResult(in.Result),
		Decision: copyScheduleDecision(in.Decision),
	}
}

func copyScheduleRequest(in ScheduleRequest) ScheduleRequest {
	return ScheduleRequest{
		Job:         copyJob(in.Job),
		Result:      copyResult(in.Result),
		Attempt:     in.Attempt,
		MaxAttempts: in.MaxAttempts,
		Metadata:    copyAnyMap(in.Metadata),
	}
}

func copyScheduleDecision(in ScheduleDecision) ScheduleDecision {
	return ScheduleDecision{
		Job:         copyJob(in.Job),
		Result:      copyResult(in.Result),
		Action:      in.Action,
		Attempt:     in.Attempt,
		NextAttempt: in.NextAttempt,
		MaxAttempts: in.MaxAttempts,
		Delay:       in.Delay,
		Reason:      in.Reason,
		Metadata:    copyAnyMap(in.Metadata),
	}
}

func copyJob(in Job) Job {
	out := in
	out.Metadata = copyAnyMap(in.Metadata)
	return out
}

func copyResult(in Result) Result {
	out := in
	out.Metadata = copyAnyMap(in.Metadata)
	return out
}

func copyAnyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = copyAnyValue(value)
	}
	return out
}

func copyAnyValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return copyAnyMap(typed)
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = copyAnyValue(item)
		}
		return out
	case []string:
		return append([]string(nil), typed...)
	default:
		return value
	}
}
