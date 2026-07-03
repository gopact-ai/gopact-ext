// Package selfbootstrap coordinates development-agent evidence for a self-bootstrap slice.
package selfbootstrap

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact/gopacttest"
)

const (
	nodeAnalyze = "analyze"
	nodePlan    = "plan"
	nodeWrite   = "write"
	nodeTest    = "test"
	nodeReview  = "review"
)

var (
	ErrObjectiveRequired   = errors.New("selfbootstrap: objective is required")
	ErrRunIDRequired       = errors.New("selfbootstrap: run id is required")
	ErrStageRequired       = errors.New("selfbootstrap: workflow stage is required")
	ErrVerificationFailed  = errors.New("selfbootstrap: verification failed")
	ErrReviewRejected      = errors.New("selfbootstrap: review rejected")
	ErrWorkflowUnavailable = errors.New("selfbootstrap: workflow is unavailable")
	ErrPatchPolicyRequired = errors.New("selfbootstrap: patch policy is required")
)

// Request describes one self-bootstrap development slice.
type Request struct {
	Objective  string
	Repository string
	IDs        gopact.RuntimeIDs
	Metadata   map[string]any
}

// Analysis is the analyze-stage output.
type Analysis struct {
	Summary  string
	Metadata map[string]any
}

// PlanStep is one planned implementation or verification step.
type PlanStep struct {
	ID       string
	Summary  string
	Metadata map[string]any
}

// PatchFile describes one file touched by a proposed patch.
type PatchFile struct {
	Path     string
	Intent   string
	Metadata map[string]any
}

// PatchProposal is a generated patch suggestion, not proof that the patch was applied.
type PatchProposal struct {
	ID       string
	Summary  string
	Diff     string
	Files    []PatchFile
	Metadata map[string]any
}

// PatchPolicyInput is the sanitized patch summary passed through the root policy boundary.
type PatchPolicyInput struct {
	ID        string
	Summary   string
	Files     []PatchFile
	HasDiff   bool
	DiffBytes int
	Metadata  map[string]any
}

// Plan is the plan-stage output.
type Plan struct {
	Summary  string
	Steps    []PlanStep
	Patch    *PatchProposal
	Metadata map[string]any
}

// WriteResult captures already-observed write evidence.
type WriteResult struct {
	Summary       string
	Diff          *gopacttest.DiffSnapshot
	FileSnapshots []gopacttest.FileSnapshot
	Metadata      map[string]any
}

// TestResult captures already-observed test and CI gate evidence.
type TestResult struct {
	Summary       string
	Commands      []gopacttest.CommandResult
	Gates         []gopacttest.CIGateResult
	RequiredGates []string
	Metadata      map[string]any
}

// Result carries the durable evidence produced by a workflow run.
type Result struct {
	Analysis      Analysis
	Plan          Plan
	PatchDecision gopact.PolicyDecision
	Write         WriteResult
	Test          TestResult
	Review        gopacttest.ReviewResult
	Checks        []gopact.VerificationCheck
	Report        gopact.VerificationReport
	RunExport     gopact.RunExport
}

// Analyzer produces development-slice analysis.
type Analyzer interface {
	Analyze(context.Context, Request) (Analysis, error)
}

// AnalyzerFunc adapts a function to Analyzer.
type AnalyzerFunc func(context.Context, Request) (Analysis, error)

func (f AnalyzerFunc) Analyze(ctx context.Context, request Request) (Analysis, error) {
	return f(ctx, request)
}

// PlanRequest carries analysis into the planner.
type PlanRequest struct {
	Request  Request
	Analysis Analysis
}

// Planner produces an implementation and verification plan.
type Planner interface {
	Plan(context.Context, PlanRequest) (Plan, error)
}

// PlannerFunc adapts a function to Planner.
type PlannerFunc func(context.Context, PlanRequest) (Plan, error)

func (f PlannerFunc) Plan(ctx context.Context, request PlanRequest) (Plan, error) {
	return f(ctx, request)
}

// WriteRequest carries plan context into the writer.
type WriteRequest struct {
	Request       Request
	Analysis      Analysis
	Plan          Plan
	PatchDecision *gopact.PolicyDecision
}

// Writer produces already-observed patch, diff, and file snapshot evidence.
type Writer interface {
	Write(context.Context, WriteRequest) (WriteResult, error)
}

// WriterFunc adapts a function to Writer.
type WriterFunc func(context.Context, WriteRequest) (WriteResult, error)

func (f WriterFunc) Write(ctx context.Context, request WriteRequest) (WriteResult, error) {
	return f(ctx, request)
}

// TestRequest carries write context into the tester.
type TestRequest struct {
	Request       Request
	Analysis      Analysis
	Plan          Plan
	PatchDecision *gopact.PolicyDecision
	Write         WriteResult
}

// Tester produces already-observed command and CI gate evidence.
type Tester interface {
	Test(context.Context, TestRequest) (TestResult, error)
}

// TesterFunc adapts a function to Tester.
type TesterFunc func(context.Context, TestRequest) (TestResult, error)

func (f TesterFunc) Test(ctx context.Context, request TestRequest) (TestResult, error) {
	return f(ctx, request)
}

// ReviewRequest carries all observed pre-review evidence.
type ReviewRequest struct {
	Request       Request
	Analysis      Analysis
	Plan          Plan
	PatchDecision *gopact.PolicyDecision
	Write         WriteResult
	Test          TestResult
	Checks        []gopact.VerificationCheck
}

// Reviewer produces an already-observed human, model, CI, or external review decision.
type Reviewer interface {
	Review(context.Context, ReviewRequest) (gopacttest.ReviewResult, error)
}

// ReviewerFunc adapts a function to Reviewer.
type ReviewerFunc func(context.Context, ReviewRequest) (gopacttest.ReviewResult, error)

func (f ReviewerFunc) Review(ctx context.Context, request ReviewRequest) (gopacttest.ReviewResult, error) {
	return f(ctx, request)
}

// Workflow coordinates one development-agent self-bootstrap slice.
type Workflow struct {
	analyzer    Analyzer
	planner     Planner
	patchPolicy gopact.Policy
	writer      Writer
	tester      Tester
	reviewer    Reviewer
}

// Option configures a workflow.
type Option func(*Workflow) error

// WithAnalyzer sets the analyze stage.
func WithAnalyzer(analyzer Analyzer) Option {
	return func(w *Workflow) error {
		if analyzer == nil {
			return fmt.Errorf("%w: analyzer", ErrStageRequired)
		}
		w.analyzer = analyzer
		return nil
	}
}

// WithPlanner sets the plan stage.
func WithPlanner(planner Planner) Option {
	return func(w *Workflow) error {
		if planner == nil {
			return fmt.Errorf("%w: planner", ErrStageRequired)
		}
		w.planner = planner
		return nil
	}
}

// WithPatchPolicy sets the policy that must allow a plan-stage patch proposal before writing.
func WithPatchPolicy(policy gopact.Policy) Option {
	return func(w *Workflow) error {
		if policy == nil {
			return fmt.Errorf("%w: patch policy", ErrStageRequired)
		}
		w.patchPolicy = policy
		return nil
	}
}

// WithWriter sets the write stage.
func WithWriter(writer Writer) Option {
	return func(w *Workflow) error {
		if writer == nil {
			return fmt.Errorf("%w: writer", ErrStageRequired)
		}
		w.writer = writer
		return nil
	}
}

// WithTester sets the test stage.
func WithTester(tester Tester) Option {
	return func(w *Workflow) error {
		if tester == nil {
			return fmt.Errorf("%w: tester", ErrStageRequired)
		}
		w.tester = tester
		return nil
	}
}

// WithReviewer sets the review stage.
func WithReviewer(reviewer Reviewer) Option {
	return func(w *Workflow) error {
		if reviewer == nil {
			return fmt.Errorf("%w: reviewer", ErrStageRequired)
		}
		w.reviewer = reviewer
		return nil
	}
}

// New creates a self-bootstrap workflow.
func New(opts ...Option) (*Workflow, error) {
	w := &Workflow{}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if err := opt(w); err != nil {
			return nil, err
		}
	}
	if w.analyzer == nil {
		return nil, fmt.Errorf("%w: analyzer", ErrStageRequired)
	}
	if w.planner == nil {
		return nil, fmt.Errorf("%w: planner", ErrStageRequired)
	}
	if w.writer == nil {
		return nil, fmt.Errorf("%w: writer", ErrStageRequired)
	}
	if w.tester == nil {
		return nil, fmt.Errorf("%w: tester", ErrStageRequired)
	}
	if w.reviewer == nil {
		return nil, fmt.Errorf("%w: reviewer", ErrStageRequired)
	}
	return w, nil
}

// Run executes one provider-neutral development-agent workflow.
func (w *Workflow) Run(ctx context.Context, request Request) (Result, error) {
	if w == nil {
		return Result{}, ErrWorkflowUnavailable
	}
	if ctx == nil {
		ctx = context.Background()
	}
	request = normalizeRequest(request)
	if err := validateRequest(request); err != nil {
		return Result{}, err
	}

	state := newRunState(request)
	if err := state.record(gopact.Event{Type: gopact.EventRunStarted, IDs: request.IDs}); err != nil {
		return Result{}, err
	}

	analysis, err := w.analyzer.Analyze(ctx, request)
	state.result.Analysis = copyAnalysis(analysis)
	if err != nil {
		if recordErr := state.recordStep(nodeAnalyze, 1, gopact.StepFailed, request, analysis, err); recordErr != nil {
			err = errors.Join(err, recordErr)
		}
		return state.finish(ctx, nodeAnalyze, 1, err, gopact.FailureRuntime)
	}
	if err := state.recordStep(nodeAnalyze, 1, gopact.StepCompleted, request, analysis, nil); err != nil {
		return state.finish(ctx, nodeAnalyze, 1, err, gopact.FailureRuntime)
	}

	planRequest := PlanRequest{Request: request, Analysis: analysis}
	plan, err := w.planner.Plan(ctx, planRequest)
	state.result.Plan = copyPlan(plan)
	if err != nil {
		if recordErr := state.recordStep(nodePlan, 2, gopact.StepFailed, planRequest, plan, err); recordErr != nil {
			err = errors.Join(err, recordErr)
		}
		return state.finish(ctx, nodePlan, 2, err, gopact.FailureRuntime)
	}
	if err := state.recordStep(nodePlan, 2, gopact.StepCompleted, planRequest, plan, nil); err != nil {
		return state.finish(ctx, nodePlan, 2, err, gopact.FailureRuntime)
	}

	patchDecision, err := w.authorizePlanPatch(ctx, state, request, plan)
	if err != nil {
		return state.finish(ctx, nodeWrite, 3, err, gopact.FailurePolicy)
	}

	writeRequest := WriteRequest{Request: request, Analysis: analysis, Plan: plan, PatchDecision: patchDecision}
	write, err := w.writer.Write(ctx, writeRequest)
	state.result.Write = copyWriteResult(write)
	if err != nil {
		if recordErr := state.recordStep(nodeWrite, 3, gopact.StepFailed, writeRequest, write, err); recordErr != nil {
			err = errors.Join(err, recordErr)
		}
		return state.finish(ctx, nodeWrite, 3, err, gopact.FailureRuntime)
	}
	if err := state.recordStep(nodeWrite, 3, gopact.StepCompleted, writeRequest, write, nil); err != nil {
		return state.finish(ctx, nodeWrite, 3, err, gopact.FailureRuntime)
	}
	if err := state.recordWriteChecks(write); err != nil {
		return state.failVerification(ctx, nodeWrite, 3, err)
	}

	testRequest := TestRequest{
		Request:       request,
		Analysis:      analysis,
		Plan:          plan,
		PatchDecision: copyPolicyDecisionPtr(patchDecision),
		Write:         write,
	}
	test, err := w.tester.Test(ctx, testRequest)
	state.result.Test = copyTestResult(test)
	if err != nil {
		if recordErr := state.recordStep(nodeTest, 4, gopact.StepFailed, testRequest, test, err); recordErr != nil {
			err = errors.Join(err, recordErr)
		}
		return state.finish(ctx, nodeTest, 4, err, gopact.FailureRuntime)
	}
	if err := state.recordTestChecks(test); err != nil {
		if recordErr := state.recordStep(nodeTest, 4, gopact.StepFailed, testRequest, test, err); recordErr != nil {
			err = errors.Join(err, recordErr)
		}
		return state.failVerification(ctx, nodeTest, 4, err)
	}
	if err := state.recordStep(nodeTest, 4, gopact.StepCompleted, testRequest, test, nil); err != nil {
		return state.finish(ctx, nodeTest, 4, err, gopact.FailureRuntime)
	}

	reviewRequest := ReviewRequest{
		Request:       request,
		Analysis:      analysis,
		Plan:          plan,
		PatchDecision: copyPolicyDecisionPtr(patchDecision),
		Write:         write,
		Test:          test,
		Checks:        state.verification.Checks(),
	}
	review, err := w.reviewer.Review(ctx, reviewRequest)
	state.result.Review = copyReviewResult(review)
	if err != nil {
		if recordErr := state.recordStep(nodeReview, 5, gopact.StepFailed, reviewRequest, review, err); recordErr != nil {
			err = errors.Join(err, recordErr)
		}
		return state.finish(ctx, nodeReview, 5, err, gopact.FailureFeedback)
	}
	if err := state.recordReviewCheck(review); err != nil {
		if recordErr := state.recordStep(nodeReview, 5, gopact.StepFailed, reviewRequest, review, err); recordErr != nil {
			err = errors.Join(err, recordErr)
		}
		if review.Status == gopacttest.ReviewStatusRejected {
			return state.finish(ctx, nodeReview, 5, errors.Join(ErrReviewRejected, err), gopact.FailureFeedback)
		}
		return state.failVerification(ctx, nodeReview, 5, err)
	}
	if err := state.recordStep(nodeReview, 5, gopact.StepCompleted, reviewRequest, review, nil); err != nil {
		return state.finish(ctx, nodeReview, 5, err, gopact.FailureRuntime)
	}

	return state.finish(ctx, "", 0, nil, "")
}

func (w *Workflow) authorizePlanPatch(
	ctx context.Context,
	state *runState,
	request Request,
	plan Plan,
) (*gopact.PolicyDecision, error) {
	if !hasPatchProposal(plan.Patch) {
		return nil, nil
	}
	if w.patchPolicy == nil {
		return nil, ErrPatchPolicyRequired
	}

	policyRequest := patchPolicyRequest(request, plan)
	if err := state.record(gopact.NewPolicyRequestedEvent(policyRequest)); err != nil {
		return nil, err
	}
	decision, err := w.patchPolicy.Decide(ctx, policyRequest)
	state.result.PatchDecision = copyPolicyDecision(decision)
	if err != nil {
		return nil, fmt.Errorf("selfbootstrap: patch policy: %w", err)
	}
	if err := state.record(gopact.NewPolicyDecidedEvent(policyRequest, decision)); err != nil {
		return nil, err
	}
	if err := gopact.RecordPolicyDecisionCheck(state.verification, policyRequest, decision); err != nil {
		return nil, errors.Join(&gopact.PolicyDeniedError{Decision: decision, Request: policyRequest}, err)
	}
	copied := copyPolicyDecision(decision)
	return &copied, nil
}

type runState struct {
	request      Request
	recorder     *gopact.RunRecorder
	verification *gopact.VerificationRecorder
	result       Result
}

func newRunState(request Request) *runState {
	return &runState{
		request:      request,
		recorder:     gopact.NewRunRecorder(),
		verification: gopact.NewVerificationRecorder(),
	}
}

func (s *runState) record(event gopact.Event) error {
	return s.recorder.Record(event.WithRuntimeDefaults(s.request.IDs))
}

func (s *runState) recordStep(node string, step int, phase gopact.StepPhase, input, output any, err error) error {
	snapshot := gopact.StepSnapshot{
		ID:        fmt.Sprintf("%s:%d:%s", s.request.IDs.RunID, step, node),
		Step:      step,
		Node:      node,
		Phase:     phase,
		IDs:       s.request.IDs,
		Input:     input,
		Output:    output,
		StartedAt: time.Now(),
		Metadata:  map[string]any{"objective": s.request.Objective},
	}
	if err != nil {
		snapshot.Error = err.Error()
	}
	if phase != gopact.StepRunning {
		snapshot.CompletedAt = snapshot.StartedAt
	}
	eventType := gopact.EventNodeCompleted
	if phase == gopact.StepFailed {
		eventType = gopact.EventNodeFailed
	}
	return s.record(gopact.Event{
		Type:         eventType,
		Node:         node,
		Step:         step,
		StepSnapshot: &snapshot,
		Err:          err,
	})
}

func (s *runState) recordWriteChecks(write WriteResult) error {
	var errs []error
	if write.Diff != nil {
		diff := *write.Diff
		diff.Metadata = mergeMetadata(diff.Metadata, write.Metadata)
		if err := gopacttest.RecordDiffCheck(s.verification, diff); err != nil {
			errs = append(errs, err)
		}
	}
	for _, snapshot := range write.FileSnapshots {
		snapshot.Metadata = mergeMetadata(snapshot.Metadata, write.Metadata)
		if err := gopacttest.RecordFileSnapshotCheck(s.verification, snapshot); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (s *runState) recordTestChecks(test TestResult) error {
	var errs []error
	for _, command := range test.Commands {
		command.Metadata = mergeMetadata(command.Metadata, test.Metadata)
		if err := gopacttest.RecordCommandCheck(s.verification, command); err != nil {
			errs = append(errs, err)
		}
	}
	if len(test.Gates) > 0 {
		gates := make([]gopacttest.CIGateResult, len(test.Gates))
		for i, gate := range test.Gates {
			gate.Metadata = mergeMetadata(gate.Metadata, test.Metadata)
			gates[i] = gate
		}
		if err := gopacttest.RecordCIGateSuiteCheck(s.verification, gopacttest.CIGateSuite{
			ID:            "ci-gates:selfbootstrap",
			Name:          "Self-bootstrap CI gates",
			RequiredGates: append([]string(nil), test.RequiredGates...),
			Results:       gates,
			Metadata:      copyMetadata(test.Metadata),
		}); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (s *runState) recordReviewCheck(review gopacttest.ReviewResult) error {
	return gopacttest.RecordReviewCheck(s.verification, review)
}

func (s *runState) failVerification(ctx context.Context, node string, step int, err error) (Result, error) {
	return s.finish(ctx, node, step, errors.Join(ErrVerificationFailed, err), gopact.FailureVerification)
}

func (s *runState) finish(
	ctx context.Context,
	node string,
	step int,
	runErr error,
	failureKind gopact.FailureKind,
) (Result, error) {
	if err := ctx.Err(); err != nil {
		runErr = errors.Join(runErr, err)
		if failureKind == "" {
			failureKind = gopact.FailureContext
		}
	}
	if runErr != nil {
		if failureKind == "" {
			failureKind = gopact.FailureRuntime
		}
		if err := s.recorder.RecordFailure(gopact.FailureAttribution{
			ID:       fmt.Sprintf("%s:observed-failure:%s:%d", s.request.IDs.RunID, fallbackNode(node), step),
			Kind:     failureKind,
			IDs:      s.request.IDs,
			Node:     node,
			Step:     step,
			Summary:  "self-bootstrap workflow failed",
			Error:    runErr.Error(),
			Evidence: failedCheckEvidence(s.verification.Checks()),
		}); err != nil {
			runErr = errors.Join(runErr, err)
		}
		if err := s.record(gopact.Event{
			Type: gopact.EventRunFailed,
			Node: node,
			Step: step,
			Metadata: map[string]any{
				gopact.EventMetadataFailureKind: failureKind,
			},
			Err: runErr,
		}); err != nil {
			runErr = errors.Join(runErr, err)
		}
	} else {
		if err := s.record(gopact.Event{Type: gopact.EventRunCompleted}); err != nil {
			runErr = err
		}
	}

	export, exportErr := s.recorder.Export()
	if exportErr != nil {
		if runErr != nil {
			return s.result, errors.Join(runErr, exportErr)
		}
		return s.result, exportErr
	}
	if err := gopact.RecordRunExportCheck(s.verification, export); err != nil && runErr == nil {
		runErr = errors.Join(ErrVerificationFailed, err)
	}
	report, reportErr := s.verification.Report(export)
	if reportErr != nil {
		if runErr != nil {
			return s.result, errors.Join(runErr, reportErr)
		}
		return s.result, reportErr
	}
	bundled, embedErr := gopact.EmbedVerificationReport(export, report)
	if embedErr != nil {
		if runErr != nil {
			return s.result, errors.Join(runErr, embedErr)
		}
		return s.result, embedErr
	}

	s.result.RunExport = bundled
	s.result.Report = report
	s.result.Checks = report.Checks
	if runErr != nil {
		return s.result, runErr
	}
	if report.Status != gopact.VerificationStatusPassed {
		return s.result, ErrVerificationFailed
	}
	return s.result, nil
}

func validateRequest(request Request) error {
	if strings.TrimSpace(request.Objective) == "" {
		return ErrObjectiveRequired
	}
	if strings.TrimSpace(request.IDs.RunID) == "" {
		return ErrRunIDRequired
	}
	return nil
}

func normalizeRequest(request Request) Request {
	request.Objective = strings.TrimSpace(request.Objective)
	request.Repository = strings.TrimSpace(request.Repository)
	request.Metadata = copyMetadata(request.Metadata)
	if request.IDs.AgentID == "" {
		request.IDs.AgentID = "devagent-selfbootstrap"
	}
	return request
}

func failedCheckEvidence(checks []gopact.VerificationCheck) []gopact.VerificationEvidence {
	var evidence []gopact.VerificationEvidence
	for _, check := range checks {
		if check.Status != gopact.VerificationStatusFailed {
			continue
		}
		evidence = append(evidence, check.Evidence...)
	}
	if len(evidence) > 0 {
		return evidence
	}
	return []gopact.VerificationEvidence{
		{Type: "event", Ref: "selfbootstrap:failure", Summary: "self-bootstrap workflow failed"},
	}
}

func fallbackNode(node string) string {
	if node == "" {
		return "run"
	}
	return node
}

func mergeMetadata(left, right map[string]any) map[string]any {
	out := copyMetadata(left)
	for key, value := range right {
		if _, exists := out[key]; exists {
			continue
		}
		out[key] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func copyAnalysis(in Analysis) Analysis {
	in.Metadata = copyMetadata(in.Metadata)
	return in
}

func copyPlan(in Plan) Plan {
	out := in
	out.Metadata = copyMetadata(in.Metadata)
	out.Steps = make([]PlanStep, len(in.Steps))
	for i, step := range in.Steps {
		step.Metadata = copyMetadata(step.Metadata)
		out.Steps[i] = step
	}
	if in.Patch != nil {
		patch := copyPatchProposal(*in.Patch)
		out.Patch = &patch
	}
	return out
}

func copyWriteResult(in WriteResult) WriteResult {
	out := in
	out.Metadata = copyMetadata(in.Metadata)
	if in.Diff != nil {
		diff := *in.Diff
		diff.Files = append([]string(nil), in.Diff.Files...)
		diff.Metadata = copyMetadata(in.Diff.Metadata)
		out.Diff = &diff
	}
	out.FileSnapshots = make([]gopacttest.FileSnapshot, len(in.FileSnapshots))
	for i, snapshot := range in.FileSnapshots {
		snapshot.Metadata = copyMetadata(snapshot.Metadata)
		out.FileSnapshots[i] = snapshot
	}
	return out
}

func copyTestResult(in TestResult) TestResult {
	out := in
	out.Metadata = copyMetadata(in.Metadata)
	out.RequiredGates = append([]string(nil), in.RequiredGates...)
	out.Commands = make([]gopacttest.CommandResult, len(in.Commands))
	for i, command := range in.Commands {
		command.Command = append([]string(nil), command.Command...)
		command.Metadata = copyMetadata(command.Metadata)
		out.Commands[i] = command
	}
	out.Gates = make([]gopacttest.CIGateResult, len(in.Gates))
	for i, gate := range in.Gates {
		gate.Result.Command = append([]string(nil), gate.Result.Command...)
		gate.Result.Metadata = copyMetadata(gate.Result.Metadata)
		gate.Metadata = copyMetadata(gate.Metadata)
		out.Gates[i] = gate
	}
	return out
}

func copyReviewResult(in gopacttest.ReviewResult) gopacttest.ReviewResult {
	in.Metadata = copyMetadata(in.Metadata)
	return in
}

func copyPatchProposal(in PatchProposal) PatchProposal {
	out := in
	out.Metadata = copyMetadata(in.Metadata)
	out.Files = make([]PatchFile, len(in.Files))
	for i, file := range in.Files {
		file.Metadata = copyMetadata(file.Metadata)
		out.Files[i] = file
	}
	return out
}

func copyPatchFiles(in []PatchFile) []PatchFile {
	out := make([]PatchFile, len(in))
	for i, file := range in {
		file.Metadata = copyMetadata(file.Metadata)
		out[i] = file
	}
	return out
}

func copyPolicyDecision(in gopact.PolicyDecision) gopact.PolicyDecision {
	out := in
	out.Metadata = copyMetadata(in.Metadata)
	return out
}

func copyPolicyDecisionPtr(in *gopact.PolicyDecision) *gopact.PolicyDecision {
	if in == nil {
		return nil
	}
	out := copyPolicyDecision(*in)
	return &out
}

func hasPatchProposal(patch *PatchProposal) bool {
	if patch == nil {
		return false
	}
	return strings.TrimSpace(patch.ID) != "" ||
		strings.TrimSpace(patch.Summary) != "" ||
		strings.TrimSpace(patch.Diff) != "" ||
		len(patch.Files) > 0 ||
		len(patch.Metadata) > 0
}

func patchPolicyRequest(request Request, plan Plan) gopact.PolicyRequest {
	return gopact.PolicyRequest{
		IDs:      request.IDs,
		Boundary: gopact.PolicyBoundarySandbox,
		Action:   gopact.PolicyActionWrite,
		Input:    patchPolicyInput(plan.Patch),
		Metadata: map[string]any{
			"objective":  request.Objective,
			"repository": request.Repository,
			"stage":      nodeWrite,
		},
	}
}

func patchPolicyInput(patch *PatchProposal) PatchPolicyInput {
	if patch == nil {
		return PatchPolicyInput{}
	}
	return PatchPolicyInput{
		ID:        patch.ID,
		Summary:   patch.Summary,
		Files:     copyPatchFiles(patch.Files),
		HasDiff:   strings.TrimSpace(patch.Diff) != "",
		DiffBytes: len(patch.Diff),
		Metadata:  copyMetadata(patch.Metadata),
	}
}

func copyMetadata(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
