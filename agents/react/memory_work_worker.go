package react

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact/memory"
)

var (
	ErrDeferredMemoryWorkQueueRequired                = errors.New("react: deferred memory work queue is required")
	ErrDeferredMemoryWorkLeaseBackendRequired         = errors.New("react: deferred memory work lease backend is required")
	ErrDeferredMemoryWorkLeaseRenewalIntervalRequired = errors.New("react: deferred memory work lease renewal interval is required")
	ErrDeferredMemoryWorkScheduleDeciderRequired      = errors.New("react: deferred memory work schedule decider is required")
	ErrDeferredMemoryWorkDrainLimitRequired           = errors.New("react: deferred memory work drain limit is required")
)

// DeferredMemoryWorkJob is one host-queued run export containing deferred memory work.
//
// DeliveryID is a queue delivery receipt. Queue implementations may set it on
// Dequeue and require callers to pass the same job value to Complete, Retry,
// DeadLetter, or Stop. It should not be treated as durable job metadata.
type DeferredMemoryWorkJob struct {
	ID          string
	DeliveryID  string
	Export      gopact.RunExport
	Attempt     int
	MaxAttempts int
	Metadata    map[string]any
}

// DeferredMemoryWorkQueue is the host-owned queue contract consumed by DeferredMemoryWorkWorker.
//
// The SDK includes a local in-memory implementation for tests and single-process
// workers. Durable storage, distributed leasing, and production DLQ handling
// remain adapter or host responsibilities; the local in-memory queue can provide
// single-process visibility timeout semantics for tests and lightweight workers.
type DeferredMemoryWorkQueue interface {
	Dequeue(ctx context.Context) (DeferredMemoryWorkJob, bool, error)
	Complete(ctx context.Context, job DeferredMemoryWorkJob, report DeferredMemoryWorkReport) error
	Retry(ctx context.Context, job DeferredMemoryWorkJob, decision DeferredMemoryWorkScheduleDecision) error
	DeadLetter(ctx context.Context, job DeferredMemoryWorkJob, decision DeferredMemoryWorkScheduleDecision) error
	Stop(ctx context.Context, job DeferredMemoryWorkJob, report DeferredMemoryWorkReport, decision DeferredMemoryWorkScheduleDecision) error
}

// DeferredMemoryWorkScheduleRequest is the input to a host retry/stop/DLQ decider.
type DeferredMemoryWorkScheduleRequest struct {
	Job         DeferredMemoryWorkJob
	Report      DeferredMemoryWorkReport
	Attempt     int
	MaxAttempts int
	Metadata    map[string]any
}

// DeferredMemoryWorkScheduleDecider decides the next host queue transition after a failed worker pass.
type DeferredMemoryWorkScheduleDecider interface {
	DecideDeferredMemoryWorkSchedule(ctx context.Context, request DeferredMemoryWorkScheduleRequest) (DeferredMemoryWorkScheduleDecision, error)
}

// DeferredMemoryWorkScheduleDeciderFunc adapts a function into DeferredMemoryWorkScheduleDecider.
type DeferredMemoryWorkScheduleDeciderFunc func(context.Context, DeferredMemoryWorkScheduleRequest) (DeferredMemoryWorkScheduleDecision, error)

// DecideDeferredMemoryWorkSchedule implements DeferredMemoryWorkScheduleDecider.
func (f DeferredMemoryWorkScheduleDeciderFunc) DecideDeferredMemoryWorkSchedule(ctx context.Context, request DeferredMemoryWorkScheduleRequest) (DeferredMemoryWorkScheduleDecision, error) {
	if f == nil {
		return DeferredMemoryWorkScheduleDecision{}, ErrDeferredMemoryWorkScheduleDeciderRequired
	}
	if ctx == nil {
		ctx = context.TODO()
	}
	if err := ctx.Err(); err != nil {
		return DeferredMemoryWorkScheduleDecision{}, err
	}
	return f(ctx, copyDeferredMemoryWorkScheduleRequest(request))
}

// DeferredMemoryWorkWorker consumes host-queued deferred memory jobs one at a time.
type DeferredMemoryWorkWorker struct {
	queue          DeferredMemoryWorkQueue
	executor       gopact.EffectReplayExecutor
	decider        DeferredMemoryWorkScheduleDecider
	recorder       *gopact.VerificationRecorder
	reportRecorder *gopact.VerificationRecorder
	leaseBackend   gopact.LeaseBackend
	leaseRequest   gopact.LeaseRequest
	useLease       bool
	renewLease     bool
	renewEvery     time.Duration
}

// DeferredMemoryWorkWorkerOption configures DeferredMemoryWorkWorker.
type DeferredMemoryWorkWorkerOption func(*DeferredMemoryWorkWorker)

// WithDeferredMemoryWorkScheduleDecider configures the retry/stop/DLQ decider used after failed worker passes.
func WithDeferredMemoryWorkScheduleDecider(decider DeferredMemoryWorkScheduleDecider) DeferredMemoryWorkWorkerOption {
	return func(w *DeferredMemoryWorkWorker) {
		w.decider = decider
	}
}

// WithDeferredMemoryWorkRecorder records both worker pass reports and schedule decisions.
func WithDeferredMemoryWorkRecorder(recorder *gopact.VerificationRecorder) DeferredMemoryWorkWorkerOption {
	return func(w *DeferredMemoryWorkWorker) {
		w.reportRecorder = recorder
		w.recorder = recorder
	}
}

// WithDeferredMemoryWorkScheduleRecorder records schedule decisions as verification evidence.
func WithDeferredMemoryWorkScheduleRecorder(recorder *gopact.VerificationRecorder) DeferredMemoryWorkWorkerOption {
	return func(w *DeferredMemoryWorkWorker) {
		w.recorder = recorder
	}
}

// WithDeferredMemoryWorkReportRecorder records each dequeued worker pass as memory replay verification evidence.
func WithDeferredMemoryWorkReportRecorder(recorder *gopact.VerificationRecorder) DeferredMemoryWorkWorkerOption {
	return func(w *DeferredMemoryWorkWorker) {
		w.reportRecorder = recorder
	}
}

// WithDeferredMemoryWorkLease gates each RunOnce call with a worker ownership lease.
//
// The lease is acquired before dequeue, checked before queue transitions, and
// released before RunOnce returns. WithDeferredMemoryWorkLeaseRenewalInterval
// can renew the held lease during a pass; durable queue visibility semantics
// remain the queue adapter's responsibility.
func WithDeferredMemoryWorkLease(leases gopact.LeaseBackend, request gopact.LeaseRequest) DeferredMemoryWorkWorkerOption {
	return func(w *DeferredMemoryWorkWorker) {
		w.leaseBackend = leases
		w.leaseRequest = request
		w.leaseRequest.Metadata = copyAnyMap(request.Metadata)
		w.useLease = true
	}
}

// WithDeferredMemoryWorkLeaseRenewalInterval renews a held RunOnce lease while a worker pass is running.
func WithDeferredMemoryWorkLeaseRenewalInterval(interval time.Duration) DeferredMemoryWorkWorkerOption {
	return func(w *DeferredMemoryWorkWorker) {
		w.renewLease = true
		w.renewEvery = interval
	}
}

// NewDeferredMemoryWorkWorker creates a worker for host-managed deferred memory jobs.
//
// If no schedule decider option is supplied, the worker uses the default
// retry/backoff decider. Queueing, sleeping, durable DLQ storage, and
// distributed visibility semantics remain queue or host responsibilities.
// WithDeferredMemoryWorkLease can optionally gate one RunOnce pass with a host
// LeaseBackend.
func NewDeferredMemoryWorkWorker(queue DeferredMemoryWorkQueue, executor gopact.EffectReplayExecutor, opts ...DeferredMemoryWorkWorkerOption) (*DeferredMemoryWorkWorker, error) {
	if queue == nil {
		return nil, ErrDeferredMemoryWorkQueueRequired
	}
	if executor == nil {
		return nil, ErrMemoryWorkExecutorRequired
	}
	worker := &DeferredMemoryWorkWorker{
		queue:    queue,
		executor: executor,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(worker)
		}
	}
	if worker.useLease {
		if err := validateDeferredMemoryWorkLeaseConfig(worker.leaseBackend, worker.leaseRequest); err != nil {
			return nil, err
		}
	}
	if worker.renewLease {
		if worker.renewEvery <= 0 {
			return nil, ErrDeferredMemoryWorkLeaseRenewalIntervalRequired
		}
	}
	if worker.decider == nil {
		decider, err := NewDeferredMemoryWorkRetryDecider(DeferredMemoryWorkRetryPolicy{})
		if err != nil {
			return nil, err
		}
		worker.decider = decider
	}
	return worker, nil
}

// DeferredMemoryWorkWorkerResult is the observable result of one RunOnce call.
type DeferredMemoryWorkWorkerResult struct {
	Dequeued bool
	Job      DeferredMemoryWorkJob
	Report   DeferredMemoryWorkReport
	Decision DeferredMemoryWorkScheduleDecision
}

// DeferredMemoryWorkDrainResult summarizes a bounded worker drain.
type DeferredMemoryWorkDrainResult struct {
	Dequeued     int
	Completed    int
	Retried      int
	DeadLettered int
	Stopped      int
	Results      []DeferredMemoryWorkWorkerResult
}

// Drain runs RunOnce until the queue is empty, limit is reached, or a terminal error occurs.
func (w *DeferredMemoryWorkWorker) Drain(ctx context.Context, limit int) (DeferredMemoryWorkDrainResult, error) {
	if limit <= 0 {
		return DeferredMemoryWorkDrainResult{}, ErrDeferredMemoryWorkDrainLimitRequired
	}
	if ctx == nil {
		ctx = context.TODO()
	}

	var drain DeferredMemoryWorkDrainResult
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

// RunOnce dequeues at most one job, runs one worker pass, and applies the host queue transition.
func (w *DeferredMemoryWorkWorker) RunOnce(ctx context.Context) (result DeferredMemoryWorkWorkerResult, err error) {
	if w == nil || w.queue == nil {
		return DeferredMemoryWorkWorkerResult{}, ErrDeferredMemoryWorkQueueRequired
	}
	if w.executor == nil {
		return DeferredMemoryWorkWorkerResult{}, ErrMemoryWorkExecutorRequired
	}
	if ctx == nil {
		ctx = context.TODO()
	}

	lease, err := w.acquireDeferredMemoryWorkLease(ctx)
	if err != nil {
		return DeferredMemoryWorkWorkerResult{}, err
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
		return DeferredMemoryWorkWorkerResult{}, fmt.Errorf("react: dequeue deferred memory work: %w", err)
	}
	if !ok {
		return DeferredMemoryWorkWorkerResult{}, nil
	}

	job = normalizeDeferredMemoryWorkJob(job)
	result = DeferredMemoryWorkWorkerResult{
		Dequeued: true,
		Job:      copyDeferredMemoryWorkJob(job),
	}
	report, runErr := RunDeferredMemoryWork(ctx, job.Export, w.executor)
	result.Report = report
	if err := w.recordDeferredMemoryWorkReport(report); err != nil {
		return result, err
	}
	if report.Status != DeferredMemoryWorkFailed {
		if err := ensureDeferredMemoryWorkLease(ctx, lease); err != nil {
			return result, err
		}
		if err := w.queue.Complete(ctx, job, report); err != nil {
			return result, fmt.Errorf("react: complete deferred memory work: %w", err)
		}
		return result, runErr
	}
	if w.decider == nil {
		if runErr != nil {
			return result, errors.Join(ErrDeferredMemoryWorkScheduleDeciderRequired, runErr)
		}
		return result, ErrDeferredMemoryWorkScheduleDeciderRequired
	}

	request := DeferredMemoryWorkScheduleRequest{
		Job:         copyDeferredMemoryWorkJob(job),
		Report:      report,
		Attempt:     job.Attempt,
		MaxAttempts: job.MaxAttempts,
		Metadata:    copyAnyMap(job.Metadata),
	}
	decision, err := w.decider.DecideDeferredMemoryWorkSchedule(ctx, request)
	if err != nil {
		return result, fmt.Errorf("react: decide deferred memory work schedule: %w", err)
	}
	decision = normalizeDeferredMemoryWorkScheduleDecision(job, report, decision)
	result.Decision = decision

	if err := w.applyScheduleDecision(ctx, job, report, decision, lease); err != nil {
		return result, err
	}
	if w.recorder != nil {
		if err := RecordDeferredMemoryWorkScheduleCheck(w.recorder, decision); err != nil {
			return result, err
		}
	}
	return result, deferredMemoryWorkScheduleTerminalError(report, decision)
}

func (w *DeferredMemoryWorkWorker) recordDeferredMemoryWorkReport(report DeferredMemoryWorkReport) error {
	if w.reportRecorder == nil {
		return nil
	}
	err := RecordDeferredMemoryWorkCheck(w.reportRecorder, report)
	if errors.Is(err, memory.ErrReplayVerificationFailed) {
		return nil
	}
	return err
}

func (w *DeferredMemoryWorkWorker) applyScheduleDecision(ctx context.Context, job DeferredMemoryWorkJob, report DeferredMemoryWorkReport, decision DeferredMemoryWorkScheduleDecision, lease *deferredMemoryWorkLeaseSession) error {
	if err := ensureDeferredMemoryWorkLease(ctx, lease); err != nil {
		return err
	}
	switch decision.Action {
	case DeferredMemoryWorkScheduleRetry:
		if err := w.queue.Retry(ctx, job, decision); err != nil {
			return fmt.Errorf("react: retry deferred memory work: %w", err)
		}
	case DeferredMemoryWorkScheduleDeadLetter:
		if err := w.queue.DeadLetter(ctx, job, decision); err != nil {
			return fmt.Errorf("react: dead-letter deferred memory work: %w", err)
		}
	case DeferredMemoryWorkScheduleStop:
		if err := w.queue.Stop(ctx, job, report, decision); err != nil {
			return fmt.Errorf("react: stop deferred memory work: %w", err)
		}
	default:
		return ErrDeferredMemoryWorkScheduleDecisionRequired
	}
	return nil
}

func validateDeferredMemoryWorkLeaseConfig(leases gopact.LeaseBackend, request gopact.LeaseRequest) error {
	if leases == nil {
		return ErrDeferredMemoryWorkLeaseBackendRequired
	}
	if request.Key == "" {
		return gopact.ErrLeaseKeyRequired
	}
	if request.Owner == "" {
		return gopact.ErrLeaseOwnerRequired
	}
	if request.TTL <= 0 {
		return gopact.ErrLeaseTTLRequired
	}
	return nil
}

func (w *DeferredMemoryWorkWorker) acquireDeferredMemoryWorkLease(ctx context.Context) (*deferredMemoryWorkLeaseSession, error) {
	if !w.useLease {
		return nil, nil
	}
	record, err := w.leaseBackend.AcquireLease(ctx, w.leaseRequest)
	if err != nil {
		return nil, err
	}
	session := &deferredMemoryWorkLeaseSession{
		leases:     w.leaseBackend,
		request:    w.leaseRequest,
		current:    copyDeferredMemoryWorkLeaseRecord(record),
		renewEvery: w.renewEvery,
		renewLease: w.renewLease,
	}
	session.start(context.WithoutCancel(ctx))
	return session, nil
}

func ensureDeferredMemoryWorkLease(ctx context.Context, session *deferredMemoryWorkLeaseSession) error {
	if session == nil {
		return nil
	}
	session.renewMu.Lock()
	defer session.renewMu.Unlock()

	if err := session.err(); err != nil {
		return err
	}
	lease := session.record()
	if lease.Token == "" {
		return gopact.ErrLeaseNotHeld
	}
	current, ok, err := session.leases.GetLease(ctx, lease.Key)
	if err != nil {
		return err
	}
	if !ok || current.Owner != lease.Owner || current.Token != lease.Token {
		return gopact.ErrLeaseNotHeld
	}
	return nil
}

type deferredMemoryWorkLeaseSession struct {
	leases     gopact.LeaseBackend
	request    gopact.LeaseRequest
	renewEvery time.Duration
	renewLease bool

	mu      sync.Mutex
	current gopact.LeaseRecord
	errVal  error
	cancel  context.CancelFunc
	done    chan struct{}
	renewMu sync.Mutex
}

func (s *deferredMemoryWorkLeaseSession) start(ctx context.Context) {
	if s == nil || !s.renewLease || s.renewEvery <= 0 {
		return
	}
	renewCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	s.mu.Lock()
	s.cancel = cancel
	s.done = done
	s.mu.Unlock()
	go s.renewLoop(renewCtx, done)
}

func (s *deferredMemoryWorkLeaseSession) renewLoop(ctx context.Context, done chan<- struct{}) {
	defer close(done)
	ticker := time.NewTicker(s.renewEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !s.renewOnce(ctx) {
				return
			}
		}
	}
}

func (s *deferredMemoryWorkLeaseSession) renewOnce(ctx context.Context) bool {
	s.renewMu.Lock()
	defer s.renewMu.Unlock()

	lease := s.record()
	if lease.Token == "" {
		return false
	}
	renewed, err := s.leases.RenewLease(ctx, gopact.LeaseRenewRequest{
		Key:   lease.Key,
		Owner: lease.Owner,
		Token: lease.Token,
		TTL:   s.request.TTL,
	})
	if err != nil {
		s.setErr(err)
		return false
	}
	s.mu.Lock()
	s.current = copyDeferredMemoryWorkLeaseRecord(renewed)
	s.mu.Unlock()
	return true
}

func (s *deferredMemoryWorkLeaseSession) release(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if err := s.stop(ctx); err != nil {
		return err
	}
	lease := s.record()
	if lease.Token == "" {
		return nil
	}
	err := s.leases.ReleaseLease(ctx, gopact.LeaseReleaseRequest{
		Key:   lease.Key,
		Owner: lease.Owner,
		Token: lease.Token,
	})
	if errors.Is(err, gopact.ErrLeaseNotHeld) {
		return nil
	}
	return err
}

func (s *deferredMemoryWorkLeaseSession) stop(ctx context.Context) error {
	s.mu.Lock()
	cancel := s.cancel
	done := s.done
	s.cancel = nil
	s.done = nil
	s.mu.Unlock()
	if cancel == nil || done == nil {
		return nil
	}
	cancel()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *deferredMemoryWorkLeaseSession) record() gopact.LeaseRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return copyDeferredMemoryWorkLeaseRecord(s.current)
}

func (s *deferredMemoryWorkLeaseSession) err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.errVal
}

func (s *deferredMemoryWorkLeaseSession) setErr(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.errVal = err
	if errors.Is(err, gopact.ErrLeaseNotHeld) {
		s.current = gopact.LeaseRecord{}
	}
}

func copyDeferredMemoryWorkLeaseRecord(in gopact.LeaseRecord) gopact.LeaseRecord {
	out := in
	out.Metadata = copyAnyMap(in.Metadata)
	return out
}

func (r *DeferredMemoryWorkDrainResult) record(result DeferredMemoryWorkWorkerResult) {
	r.Dequeued++
	r.Results = append(r.Results, copyDeferredMemoryWorkWorkerResult(result))
	if result.Report.Status != DeferredMemoryWorkFailed {
		r.Completed++
		return
	}
	switch result.Decision.Action {
	case DeferredMemoryWorkScheduleRetry:
		r.Retried++
	case DeferredMemoryWorkScheduleDeadLetter:
		r.DeadLettered++
	case DeferredMemoryWorkScheduleStop:
		r.Stopped++
	}
}

func deferredMemoryWorkScheduleTerminalError(report DeferredMemoryWorkReport, decision DeferredMemoryWorkScheduleDecision) error {
	switch {
	case decision.Action == DeferredMemoryWorkScheduleDeadLetter:
		return ErrDeferredMemoryWorkDeadLettered
	case decision.Action == DeferredMemoryWorkScheduleStop && report.Status == DeferredMemoryWorkFailed:
		return ErrDeferredMemoryWorkScheduleFailed
	default:
		return nil
	}
}

func normalizeDeferredMemoryWorkJob(job DeferredMemoryWorkJob) DeferredMemoryWorkJob {
	if job.Attempt <= 0 {
		job.Attempt = 1
	}
	job.Metadata = copyAnyMap(job.Metadata)
	return job
}

func normalizeDeferredMemoryWorkScheduleDecision(job DeferredMemoryWorkJob, report DeferredMemoryWorkReport, decision DeferredMemoryWorkScheduleDecision) DeferredMemoryWorkScheduleDecision {
	decision.Report = report
	if decision.Attempt <= 0 {
		decision.Attempt = job.Attempt
	}
	if decision.MaxAttempts <= 0 {
		decision.MaxAttempts = job.MaxAttempts
	}
	if decision.Action == DeferredMemoryWorkScheduleRetry && decision.NextAttempt <= 0 {
		decision.NextAttempt = decision.Attempt + 1
	}
	metadata := copyAnyMap(job.Metadata)
	for key, value := range decision.Metadata {
		if metadata == nil {
			metadata = make(map[string]any)
		}
		metadata[key] = value
	}
	if job.ID != "" {
		if metadata == nil {
			metadata = make(map[string]any)
		}
		metadata["job_id"] = job.ID
	}
	decision.Metadata = metadata
	return decision
}

func copyDeferredMemoryWorkScheduleRequest(in DeferredMemoryWorkScheduleRequest) DeferredMemoryWorkScheduleRequest {
	out := in
	out.Job = copyDeferredMemoryWorkJob(in.Job)
	out.Metadata = copyAnyMap(in.Metadata)
	return out
}

func copyDeferredMemoryWorkJob(in DeferredMemoryWorkJob) DeferredMemoryWorkJob {
	out := in
	out.Export = copyDeferredMemoryWorkRunExport(in.Export)
	out.Metadata = copyAnyMap(in.Metadata)
	return out
}

func copyDeferredMemoryWorkReport(in DeferredMemoryWorkReport) DeferredMemoryWorkReport {
	out := in
	out.Plan = copyDeferredMemoryWorkRunEffectReplayPlan(in.Plan)
	out.Results = copyDeferredMemoryWorkRunEffectReplayResults(in.Results)
	return out
}

func copyDeferredMemoryWorkScheduleDecision(in DeferredMemoryWorkScheduleDecision) DeferredMemoryWorkScheduleDecision {
	out := in
	out.Report = copyDeferredMemoryWorkReport(in.Report)
	out.Metadata = copyAnyMap(in.Metadata)
	return out
}

func copyDeferredMemoryWorkWorkerResult(in DeferredMemoryWorkWorkerResult) DeferredMemoryWorkWorkerResult {
	out := in
	out.Job = copyDeferredMemoryWorkJob(in.Job)
	out.Report = copyDeferredMemoryWorkReport(in.Report)
	out.Decision = copyDeferredMemoryWorkScheduleDecision(in.Decision)
	return out
}

func copyDeferredMemoryWorkRunExport(in gopact.RunExport) gopact.RunExport {
	out := in
	out.Events = append([]gopact.Event(nil), in.Events...)
	out.Steps = copyStepSnapshots(in.Steps)
	out.Tasks = append([]gopact.TaskRecord(nil), in.Tasks...)
	out.Inputs = append([]gopact.InputRecord(nil), in.Inputs...)
	out.Interventions = append([]gopact.InterventionRecord(nil), in.Interventions...)
	out.Failures = append([]gopact.FailureAttribution(nil), in.Failures...)
	out.EntropyAudits = append([]gopact.EntropyAudit(nil), in.EntropyAudits...)
	out.VerificationReports = append([]gopact.VerificationReport(nil), in.VerificationReports...)
	out.Metadata = copyAnyMap(in.Metadata)
	return out
}

func copyStepSnapshots(in []gopact.StepSnapshot) []gopact.StepSnapshot {
	if len(in) == 0 {
		return nil
	}
	out := make([]gopact.StepSnapshot, len(in))
	for i, snapshot := range in {
		out[i] = copyStepSnapshot(snapshot)
	}
	return out
}

func copyDeferredMemoryWorkRunEffectReplayPlan(in gopact.RunEffectReplayPlan) gopact.RunEffectReplayPlan {
	out := in
	out.Decisions = copyDeferredMemoryWorkRunEffectReplayDecisions(in.Decisions)
	return out
}

func copyDeferredMemoryWorkRunEffectReplayDecisions(in []gopact.RunEffectReplayDecision) []gopact.RunEffectReplayDecision {
	if len(in) == 0 {
		return nil
	}
	out := make([]gopact.RunEffectReplayDecision, len(in))
	for i, decision := range in {
		out[i] = decision
		out[i].Decision = copyDeferredMemoryWorkEffectReplayDecision(decision.Decision)
	}
	return out
}

func copyDeferredMemoryWorkEffectReplayDecision(in gopact.EffectReplayDecision) gopact.EffectReplayDecision {
	out := in
	out.Effect = copyDeferredMemoryWorkEffectRecord(in.Effect)
	return out
}

func copyDeferredMemoryWorkRunEffectReplayResults(in []gopact.RunEffectReplayResult) []gopact.RunEffectReplayResult {
	if len(in) == 0 {
		return nil
	}
	out := make([]gopact.RunEffectReplayResult, len(in))
	for i, result := range in {
		out[i] = result
		out[i].Result = copyDeferredMemoryWorkEffectReplayResult(result.Result)
	}
	return out
}

func copyDeferredMemoryWorkEffectReplayResult(in gopact.EffectReplayResult) gopact.EffectReplayResult {
	out := in
	out.Effect = copyDeferredMemoryWorkEffectRecord(in.Effect)
	out.Metadata = copyAnyMap(in.Metadata)
	return out
}

func copyDeferredMemoryWorkEffectRecord(in gopact.EffectRecord) gopact.EffectRecord {
	records := copyEffectRecords([]gopact.EffectRecord{in})
	if len(records) == 0 {
		return gopact.EffectRecord{}
	}
	return records[0]
}
