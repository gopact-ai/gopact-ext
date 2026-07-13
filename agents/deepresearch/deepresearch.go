// Package deepresearch provides source/evidence based research orchestration.
package deepresearch

import (
	"context"
	"errors"
	"fmt"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact-ext/agents/internal/contract"
	"github.com/gopact-ai/gopact/agent"
	"github.com/gopact-ai/gopact/workflow"
)

const defaultMaxParallelism = 8

// Query is one planned research question.
type Query struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// Source is a deduplicated source candidate or fetched source.
type Source struct {
	ID       string   `json:"id"`
	QueryID  string   `json:"query_id,omitempty"`
	QueryIDs []string `json:"query_ids,omitempty"`
	URI      string   `json:"uri"`
	Title    string   `json:"title"`
	Content  string   `json:"content,omitempty"`
}

// Evidence is a claim grounded in one source.
type Evidence struct {
	ID       string `json:"id"`
	SourceID string `json:"source_id"`
	Claim    string `json:"claim"`
	Quote    string `json:"quote"`
}

// Citation binds one evidence item to its source.
type Citation struct {
	EvidenceID string `json:"evidence_id"`
	SourceID   string `json:"source_id"`
}

// PlanInput is passed to a Planner.
type PlanInput struct{ Request agent.Request }

// SynthesisInput is the complete verified research context.
type SynthesisInput struct {
	Request   agent.Request
	Queries   []Query
	Sources   []Source
	Evidence  []Evidence
	Citations []Citation
}

// Planner creates research queries.
type Planner interface {
	Plan(context.Context, PlanInput) ([]Query, error)
}

// PlannerFunc adapts a planning function.
type PlannerFunc func(context.Context, PlanInput) ([]Query, error)

func (planner PlannerFunc) Plan(ctx context.Context, input PlanInput) ([]Query, error) {
	if planner == nil {
		return nil, errors.New("deepresearch: planner is nil")
	}
	queries, err := planner(ctx, PlanInput{Request: cloneRequest(input.Request)})
	return append([]Query(nil), queries...), err
}

// Discoverer performs one query discovery operation.
type Discoverer interface {
	Discover(context.Context, Query) ([]Source, error)
}

// DiscovererFunc adapts a discovery function.
type DiscovererFunc func(context.Context, Query) ([]Source, error)

func (discoverer DiscovererFunc) Discover(ctx context.Context, query Query) ([]Source, error) {
	if discoverer == nil {
		return nil, errors.New("deepresearch: discoverer is nil")
	}
	sources, err := discoverer(ctx, query)
	return cloneSources(sources), err
}

// Fetcher loads one source body.
type Fetcher interface {
	Fetch(context.Context, Source) (Source, error)
}

// FetcherFunc adapts a source fetch function.
type FetcherFunc func(context.Context, Source) (Source, error)

func (fetcher FetcherFunc) Fetch(ctx context.Context, source Source) (Source, error) {
	if fetcher == nil {
		return Source{}, errors.New("deepresearch: fetcher is nil")
	}
	fetched, err := fetcher(ctx, cloneSource(source))
	return cloneSource(fetched), err
}

// EvidenceExtractor extracts grounded evidence from one fetched source.
type EvidenceExtractor interface {
	Extract(context.Context, Source) ([]Evidence, error)
}

// EvidenceExtractorFunc adapts an extraction function.
type EvidenceExtractorFunc func(context.Context, Source) ([]Evidence, error)

func (extractor EvidenceExtractorFunc) Extract(ctx context.Context, source Source) ([]Evidence, error) {
	if extractor == nil {
		return nil, errors.New("deepresearch: evidence extractor is nil")
	}
	evidence, err := extractor(ctx, cloneSource(source))
	return append([]Evidence(nil), evidence...), err
}

// Synthesizer produces the final research response.
type Synthesizer interface {
	Synthesize(context.Context, SynthesisInput) (agent.Response, error)
}

// SynthesizerFunc adapts a synthesis function.
type SynthesizerFunc func(context.Context, SynthesisInput) (agent.Response, error)

func (synthesizer SynthesizerFunc) Synthesize(ctx context.Context, input SynthesisInput) (agent.Response, error) {
	if synthesizer == nil {
		return agent.Response{}, errors.New("deepresearch: synthesizer is nil")
	}
	response, err := synthesizer(ctx, cloneSynthesisInput(input))
	return cloneResponse(response), err
}

// CitationVerifier applies additional citation policy after structural integrity checks.
type CitationVerifier interface {
	Verify(context.Context, SynthesisInput) error
}

// CitationVerifierFunc adapts a verifier function.
type CitationVerifierFunc func(context.Context, SynthesisInput) error

func (verifier CitationVerifierFunc) Verify(ctx context.Context, input SynthesisInput) error {
	if verifier == nil {
		return errors.New("deepresearch: citation verifier is nil")
	}
	return verifier(ctx, cloneSynthesisInput(input))
}

// Option configures an Agent during construction.
type Option interface{ apply(*config) }
type optionFunc func(*config)

func (option optionFunc) apply(config *config) { option(config) }

type config struct {
	planner         Planner
	discoverer      Discoverer
	fetcher         Fetcher
	extractor       EvidenceExtractor
	synthesizer     Synthesizer
	verifier        CitationVerifier
	parallelism     int
	workflowOptions []workflow.BuildOption
	validation      *contract.Validator
}

func WithPlanner(planner Planner) Option {
	return optionFunc(func(config *config) { config.planner = planner })
}

func WithDiscoverer(discoverer Discoverer) Option {
	return optionFunc(func(config *config) { config.discoverer = discoverer })
}

func WithFetcher(fetcher Fetcher) Option {
	return optionFunc(func(config *config) { config.fetcher = fetcher })
}

func WithEvidenceExtractor(extractor EvidenceExtractor) Option {
	return optionFunc(func(config *config) { config.extractor = extractor })
}

func WithSynthesizer(synthesizer Synthesizer) Option {
	return optionFunc(func(config *config) { config.synthesizer = synthesizer })
}

func WithCitationVerifier(verifier CitationVerifier) Option {
	return optionFunc(func(config *config) {
		config.verifier = verifier
		config.validation.Present("citation verifier", verifier)
	})
}

// WithMaxParallelism bounds concurrent query discovery.
func WithMaxParallelism(limit int) Option {
	return optionFunc(func(config *config) {
		config.parallelism = limit
		config.validation.Positive("max parallelism", limit)
	})
}

// WithWorkflowOptions configures the underlying Workflow.
func WithWorkflowOptions(options ...workflow.BuildOption) Option {
	return optionFunc(func(config *config) {
		config.workflowOptions = append([]workflow.BuildOption(nil), options...)
	})
}

// State is the Agent-domain research context.
type State struct {
	Request      agent.Request `json:"request"`
	Queries      []Query       `json:"queries"`
	Sources      []Source      `json:"sources"`
	Evidence     []Evidence    `json:"evidence"`
	NextFetch    int           `json:"next_fetch"`
	NextEvidence int           `json:"next_evidence"`
}

type control struct {
	Done   bool
	Source Source
}
type discoveryResult struct {
	Query   Query
	Sources []Source
}
type evidenceResult struct {
	Source   Source
	Evidence []Evidence
}

// Agent executes the research pipeline through one Workflow.
type Agent struct{ workflow *agent.WorkflowAgent }

var _ agent.Agent = (*Agent)(nil)

// New creates a research Agent from functional options.
func New(identity agent.Identity, options ...Option) (*Agent, error) {
	configuration := config{
		verifier: noOpCitationVerifier{}, parallelism: defaultMaxParallelism,
		validation: contract.New("deepresearch").Identity("agent", identity),
	}
	for _, option := range options {
		if option != nil {
			option.apply(&configuration)
		}
	}
	if err := configuration.validation.
		Present("planner", configuration.planner).
		Present("discoverer", configuration.discoverer).
		Present("fetcher", configuration.fetcher).
		Present("evidence extractor", configuration.extractor).
		Present("synthesizer", configuration.synthesizer).
		Err(); err != nil {
		return nil, err
	}
	buildOptions := append([]workflow.BuildOption(nil), configuration.workflowOptions...)
	buildOptions = append(
		buildOptions,
		workflow.WithTopologyVersion(identity.Version),
		workflow.WithMaxParallelism(configuration.parallelism),
	)
	wf := workflow.New[agent.Request, agent.Response](identity.Name, buildOptions...)
	state := wf.Context(func(request agent.Request) State { return State{Request: cloneRequest(request)} })
	plan := wf.Node("plan", func(ctx context.Context, request agent.Request) ([]Query, error) {
		queries, err := configuration.planner.Plan(ctx, PlanInput{Request: cloneRequest(request)})
		if err != nil {
			return nil, fmt.Errorf("deepresearch: plan queries: %w", err)
		}
		return queries, nil
	})
	accept := wf.Node("accept-plan", func(ctx context.Context, queries []Query) ([]Query, error) {
		if err := validateQueries(queries); err != nil {
			return nil, err
		}
		current, err := state.Get(ctx)
		if err != nil {
			return nil, err
		}
		current.Queries = append([]Query(nil), queries...)
		if err := state.Set(ctx, current); err != nil {
			return nil, err
		}
		return append([]Query(nil), queries...), nil
	})
	discover := wf.Node("discover", func(ctx context.Context, query Query) (discoveryResult, error) {
		sources, err := configuration.discoverer.Discover(ctx, query)
		if err != nil {
			return discoveryResult{}, fmt.Errorf("deepresearch: discover query %q: %w", query.ID, err)
		}
		return discoveryResult{Query: query, Sources: cloneSources(sources)}, nil
	})
	collect := wf.Merge("collect-sources", func(ctx context.Context, inputs workflow.Inputs) (control, error) {
		results, err := inputs.All(discover)
		if err != nil {
			return control{}, err
		}
		var discovered []Source
		for _, result := range results {
			discovered = append(discovered, cloneSources(result.Sources)...)
		}
		sources := deduplicateSources(discovered)
		if len(sources) == 0 {
			return control{}, errors.New("deepresearch: discovery returned no sources")
		}
		current, err := state.Get(ctx)
		if err != nil {
			return control{}, err
		}
		if err := validateCoverage(current.Queries, sources); err != nil {
			return control{}, err
		}
		current.Sources = sources
		if err := state.Set(ctx, current); err != nil {
			return control{}, err
		}
		return control{}, nil
	})
	continueFetch := wf.Merge("continue-fetch", func(ctx context.Context, _ workflow.Inputs) (control, error) {
		current, err := state.Get(ctx)
		if err != nil {
			return control{}, err
		}
		if current.NextFetch >= len(current.Sources) {
			return control{Done: true}, nil
		}
		return control{Source: cloneSource(current.Sources[current.NextFetch])}, nil
	})
	fetch := wf.Node("fetch-source", func(ctx context.Context, source Source) (Source, error) {
		fetched, err := configuration.fetcher.Fetch(ctx, source)
		if err != nil {
			return Source{}, fmt.Errorf("deepresearch: fetch source %q: %w", source.ID, err)
		}
		return normalizeFetched(source, fetched)
	})
	recordSource := wf.Node("record-source", func(ctx context.Context, fetched Source) (control, error) {
		current, err := state.Get(ctx)
		if err != nil {
			return control{}, err
		}
		if current.NextFetch >= len(current.Sources) || current.Sources[current.NextFetch].ID != fetched.ID {
			return control{}, errors.New("deepresearch: fetched source does not match cursor")
		}
		current.Sources[current.NextFetch] = cloneSource(fetched)
		current.NextFetch++
		if err := state.Set(ctx, current); err != nil {
			return control{}, err
		}
		return control{}, nil
	})
	continueEvidence := wf.Merge("continue-evidence", func(ctx context.Context, _ workflow.Inputs) (control, error) {
		current, err := state.Get(ctx)
		if err != nil {
			return control{}, err
		}
		if current.NextEvidence >= len(current.Sources) {
			return control{Done: true}, nil
		}
		return control{Source: cloneSource(current.Sources[current.NextEvidence])}, nil
	})
	extract := wf.Node("extract-evidence", func(ctx context.Context, source Source) (evidenceResult, error) {
		evidence, err := configuration.extractor.Extract(ctx, source)
		if err != nil {
			return evidenceResult{}, fmt.Errorf("deepresearch: extract source %q: %w", source.ID, err)
		}
		for _, item := range evidence {
			if err := item.validate(); err != nil {
				return evidenceResult{}, err
			}
			if item.SourceID != source.ID {
				return evidenceResult{}, fmt.Errorf("deepresearch: evidence %q points to %q, want %q", item.ID, item.SourceID, source.ID)
			}
		}
		return evidenceResult{Source: cloneSource(source), Evidence: append([]Evidence(nil), evidence...)}, nil
	})
	recordEvidence := wf.Node("record-evidence", func(ctx context.Context, result evidenceResult) (control, error) {
		current, err := state.Get(ctx)
		if err != nil {
			return control{}, err
		}
		if current.NextEvidence >= len(current.Sources) || current.Sources[current.NextEvidence].ID != result.Source.ID {
			return control{}, errors.New("deepresearch: evidence source does not match cursor")
		}
		current.Evidence = append(current.Evidence, result.Evidence...)
		current.NextEvidence++
		if err := state.Set(ctx, current); err != nil {
			return control{}, err
		}
		return control{}, nil
	})
	verify := wf.Node("verify-citations", func(ctx context.Context, _ control) (SynthesisInput, error) {
		current, err := state.Get(ctx)
		if err != nil {
			return SynthesisInput{}, err
		}
		if len(current.Evidence) == 0 {
			return SynthesisInput{}, errors.New("deepresearch: evidence ledger is empty")
		}
		input := synthesisInput(current)
		if err := (structuralCitationVerifier{}).Verify(ctx, input); err != nil {
			return SynthesisInput{}, fmt.Errorf("deepresearch: citation integrity: %w", err)
		}
		if err := configuration.verifier.Verify(ctx, cloneSynthesisInput(input)); err != nil {
			return SynthesisInput{}, fmt.Errorf("deepresearch: citation verification: %w", err)
		}
		return input, nil
	})
	synthesize := wf.Node("synthesize", func(ctx context.Context, input SynthesisInput) (agent.Response, error) {
		response, err := configuration.synthesizer.Synthesize(ctx, input)
		if err != nil {
			return agent.Response{}, fmt.Errorf("deepresearch: synthesize: %w", err)
		}
		return cloneResponse(response), nil
	})
	accept.Route(func(_ context.Context, queries []Query) (workflow.Dispatch, error) {
		return accept.Each(discover, append([]Query(nil), queries...)...).WithSettle(workflow.SettleAll()), nil
	})
	continueFetch.Route(func(_ context.Context, current control) (workflow.Dispatch, error) {
		if current.Done {
			return continueFetch.To(continueEvidence), nil
		}
		return continueFetch.Once(fetch, cloneSource(current.Source)), nil
	})
	continueEvidence.Route(func(_ context.Context, current control) (workflow.Dispatch, error) {
		if current.Done {
			return continueEvidence.Once(verify, control{}), nil
		}
		return continueEvidence.Once(extract, cloneSource(current.Source)), nil
	})
	wf.Entry(plan)
	wf.Edge(plan, accept)
	wf.Edge(accept, discover)
	wf.Edge(discover, collect)
	wf.Edge(collect, continueFetch)
	wf.Edge(continueFetch, fetch)
	wf.Edge(continueFetch, continueEvidence)
	wf.Edge(fetch, recordSource)
	wf.Edge(recordSource, continueFetch)
	wf.Edge(continueEvidence, extract)
	wf.Edge(continueEvidence, verify)
	wf.Edge(extract, recordEvidence)
	wf.Edge(recordEvidence, continueEvidence)
	wf.Edge(verify, synthesize)
	wf.Exit(synthesize)
	facade, err := agent.NewWorkflowAgent(identity, wf)
	if err != nil {
		return nil, fmt.Errorf("deepresearch: build workflow: %w", err)
	}
	return &Agent{workflow: facade}, nil
}

// Identity returns the immutable Agent identity.
func (target *Agent) Identity() agent.Identity {
	if target == nil || target.workflow == nil {
		return agent.Identity{}
	}
	return target.workflow.Identity()
}

// Invoke executes the complete research pipeline.
func (target *Agent) Invoke(ctx context.Context, request agent.Request, options ...gopact.RunOption) (agent.Response, error) {
	if target == nil || target.workflow == nil {
		return agent.Response{}, errors.New("deepresearch: agent is nil")
	}
	return target.workflow.Invoke(ctx, cloneRequest(request), options...)
}

func synthesisInput(state State) SynthesisInput {
	return SynthesisInput{
		Request: cloneRequest(state.Request), Queries: append([]Query(nil), state.Queries...),
		Sources: cloneSources(state.Sources), Evidence: append([]Evidence(nil), state.Evidence...),
		Citations: citationsFor(state.Evidence),
	}
}

func normalizeFetched(source Source, fetched Source) (Source, error) {
	if fetched.ID == "" {
		fetched.ID = source.ID
	}
	if fetched.ID != source.ID {
		return Source{}, fmt.Errorf("deepresearch: fetch changed source identity from %q to %q", source.ID, fetched.ID)
	}
	if fetched.QueryID == "" {
		fetched.QueryID = source.QueryID
	}
	if len(fetched.QueryIDs) == 0 {
		fetched.QueryIDs = append([]string(nil), source.QueryIDs...)
	}
	if fetched.URI == "" {
		fetched.URI = source.URI
	}
	return cloneSource(fetched), nil
}

func deduplicateSources(sources []Source) []Source {
	positions := make(map[string]int, len(sources))
	result := make([]Source, 0, len(sources))
	for _, source := range sources {
		key := sourceKey(source)
		if key == "" {
			continue
		}
		if index, ok := positions[key]; ok {
			result[index].QueryIDs = mergeQueryIDs(result[index], source)
			continue
		}
		cloned := cloneSource(source)
		cloned.QueryIDs = mergeQueryIDs(Source{}, cloned)
		positions[key] = len(result)
		result = append(result, cloned)
	}
	return result
}

func sourceKey(source Source) string {
	if source.ID != "" {
		return source.ID
	}
	return source.URI
}

func mergeQueryIDs(left, right Source) []string {
	seen := map[string]struct{}{}
	ids := appendSourceQueryIDs(nil, seen, left)
	return appendSourceQueryIDs(ids, seen, right)
}

func appendSourceQueryIDs(ids []string, seen map[string]struct{}, source Source) []string {
	values := source.QueryIDs
	if len(values) == 0 && source.QueryID != "" {
		values = []string{source.QueryID}
	}
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		ids = append(ids, value)
	}
	return ids
}

func validateCoverage(queries []Query, sources []Source) error {
	covered := map[string]struct{}{}
	for _, source := range sources {
		for _, queryID := range mergeQueryIDs(Source{}, source) {
			covered[queryID] = struct{}{}
		}
	}
	for _, query := range queries {
		if _, ok := covered[query.ID]; !ok {
			return fmt.Errorf("deepresearch: query %q has no discovered source", query.ID)
		}
	}
	return nil
}

func citationsFor(evidence []Evidence) []Citation {
	citations := make([]Citation, len(evidence))
	for index, item := range evidence {
		citations[index] = Citation{EvidenceID: item.ID, SourceID: item.SourceID}
	}
	return citations
}

type noOpCitationVerifier struct{}

func (noOpCitationVerifier) Verify(context.Context, SynthesisInput) error { return nil }

type structuralCitationVerifier struct{}

func (structuralCitationVerifier) Verify(_ context.Context, input SynthesisInput) error {
	sources := make(map[string]struct{}, len(input.Sources))
	validator := contract.New("deepresearch")
	for index, source := range input.Sources {
		validator.Required(fmt.Sprintf("source %d id", index), source.ID).Unique("source", source.ID)
		if source.ID != "" {
			sources[source.ID] = struct{}{}
		}
	}
	evidence := make(map[string]string, len(input.Evidence))
	for index, item := range input.Evidence {
		validator.
			Required(fmt.Sprintf("evidence %d id", index), item.ID).
			Required(fmt.Sprintf("evidence %d source", index), item.SourceID).
			Required(fmt.Sprintf("evidence %d claim", index), item.Claim).
			Required(fmt.Sprintf("evidence %d quote", index), item.Quote).
			Unique("evidence", item.ID)
		_, sourceExists := sources[item.SourceID]
		validator.Check(sourceExists, "evidence %q references unknown source %q", item.ID, item.SourceID)
		if item.ID != "" {
			evidence[item.ID] = item.SourceID
		}
	}
	seen := make(map[string]struct{}, len(input.Citations))
	for index, citation := range input.Citations {
		validator.
			Required(fmt.Sprintf("citation %d evidence", index), citation.EvidenceID).
			Required(fmt.Sprintf("citation %d source", index), citation.SourceID).
			Unique("citation", citation.EvidenceID)
		evidenceSource, evidenceExists := evidence[citation.EvidenceID]
		_, sourceExists := sources[citation.SourceID]
		validator.
			Check(evidenceExists, "citation %q references unknown evidence", citation.EvidenceID).
			Check(sourceExists, "citation %q references unknown source", citation.EvidenceID).
			Check(!evidenceExists || evidenceSource == citation.SourceID, "citation %q points to %q, want %q", citation.EvidenceID, citation.SourceID, evidenceSource)
		if citation.EvidenceID != "" {
			seen[citation.EvidenceID] = struct{}{}
		}
	}
	return validator.Check(len(seen) == len(evidence), "not every evidence item has a citation").Err()
}

func validateQueries(queries []Query) error {
	validator := contract.New("deepresearch").NonEmpty("queries", len(queries))
	for index, query := range queries {
		validator.
			Required(fmt.Sprintf("query %d id", index), query.ID).
			Required(fmt.Sprintf("query %d text", index), query.Text).
			Unique("query", query.ID)
	}
	return validator.Err()
}

func (item Evidence) validate() error {
	return contract.New("deepresearch").
		Required("evidence id", item.ID).
		Required("evidence source", item.SourceID).
		Required("evidence claim", item.Claim).
		Required("evidence quote", item.Quote).
		Err()
}

func cloneState(state State) State {
	state.Request = cloneRequest(state.Request)
	state.Queries = append([]Query(nil), state.Queries...)
	state.Sources = cloneSources(state.Sources)
	state.Evidence = append([]Evidence(nil), state.Evidence...)
	return state
}

func cloneSynthesisInput(input SynthesisInput) SynthesisInput {
	input.Request = cloneRequest(input.Request)
	input.Queries = append([]Query(nil), input.Queries...)
	input.Sources = cloneSources(input.Sources)
	input.Evidence = append([]Evidence(nil), input.Evidence...)
	input.Citations = append([]Citation(nil), input.Citations...)
	return input
}

func cloneSource(source Source) Source {
	source.QueryIDs = append([]string(nil), source.QueryIDs...)
	return source
}

func cloneSources(sources []Source) []Source {
	if sources == nil {
		return nil
	}
	cloned := make([]Source, len(sources))
	for index, source := range sources {
		cloned[index] = cloneSource(source)
	}
	return cloned
}

func cloneRequest(request agent.Request) agent.Request {
	request.Messages = cloneMessages(request.Messages)
	request.Artifacts = append([]gopact.ArtifactRef(nil), request.Artifacts...)
	request.Metadata = cloneStringMap(request.Metadata)
	return request
}

func cloneResponse(response agent.Response) agent.Response {
	response.Message = cloneMessage(response.Message)
	response.Artifacts = append([]gopact.ArtifactRef(nil), response.Artifacts...)
	response.Metadata = cloneStringMap(response.Metadata)
	return response
}

func cloneMessages(messages []gopact.Message) []gopact.Message {
	if messages == nil {
		return nil
	}
	cloned := make([]gopact.Message, len(messages))
	for index, message := range messages {
		cloned[index] = cloneMessage(message)
	}
	return cloned
}

func cloneMessage(message gopact.Message) gopact.Message {
	message.Parts = append([]gopact.MessagePart(nil), message.Parts...)
	for index := range message.Parts {
		if message.Parts[index].Ref != nil {
			ref := *message.Parts[index].Ref
			message.Parts[index].Ref = &ref
		}
	}
	return message
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}
