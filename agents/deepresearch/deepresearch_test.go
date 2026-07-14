package deepresearch

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact/agent"
	"github.com/gopact-ai/gopact/gopacttest"
	"github.com/gopact-ai/gopact/workflow"
)

type researchCalls struct {
	planner    int
	discoverer int
	fetcher    int
	extractor  int
	verifier   int
	synthesis  int
}

func TestNewRequiresPipelineOptions(t *testing.T) {
	_, err := New(testIdentity())
	if err == nil {
		t.Fatal("New() error = nil")
	}
	for _, name := range []string{"planner", "discoverer", "fetcher", "evidence extractor", "synthesizer"} {
		if !strings.Contains(err.Error(), name) {
			t.Fatalf("New() error = %v, want %q", err, name)
		}
	}
}

func TestDeepResearchRunsWorkflowPipelineAndDeduplicatesSources(t *testing.T) {
	calls := &researchCalls{}
	store := workflow.NewMemoryStore()
	target := newResearchAgent(t, calls, WithWorkflowOptions(
		workflow.WithStore(store),
	))
	var nodes []string
	response, err := target.Invoke(
		context.Background(),
		agent.Request{Messages: []gopact.Message{gopact.UserMessage("research")}},
		gopact.WithRunID("research-workflow"),
		gopact.WithStrictEventHandler(func(_ context.Context, event gopact.Event) error {
			if event.RunID == "research-workflow" && event.Type == workflow.EventNodeCompleted {
				nodes = append(nodes, event.NodeID)
			}
			return nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if response.Message.Parts[0].Text != "report" || *calls != (researchCalls{1, 2, 1, 1, 1, 1}) {
		t.Fatalf("response/calls = %+v/%+v", response, calls)
	}
	want := []string{
		"plan", "accept-plan", "discover", "discover", "collect-sources",
		"continue-fetch", "fetch-source", "record-source", "continue-fetch",
		"continue-evidence", "extract-evidence", "record-evidence", "continue-evidence",
		"verify-citations", "synthesize",
	}
	if !reflect.DeepEqual(nodes, want) {
		t.Fatalf("completed nodes = %v, want %v", nodes, want)
	}
	checkpoint, err := store.Load(context.Background(), "research-workflow")
	if err != nil || checkpoint.Status != workflow.CheckpointCompleted {
		t.Fatalf("Load() = %+v, %v, want completed checkpoint", checkpoint, err)
	}
}

func TestDeepResearchDiscoveryHonorsParallelism(t *testing.T) {
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	releaseDiscovery := sync.OnceFunc(func() { close(release) })
	defer releaseDiscovery()
	target, err := New(
		testIdentity(),
		WithPlanner(PlannerFunc(func(context.Context, PlanInput) ([]Query, error) {
			return []Query{{ID: "q1", Text: "first"}, {ID: "q2", Text: "second"}}, nil
		})),
		WithDiscoverer(DiscovererFunc(func(_ context.Context, query Query) ([]Source, error) {
			started <- struct{}{}
			<-release
			return []Source{{ID: "s-" + query.ID, QueryID: query.ID, URI: "https://example.test/" + query.ID, Title: query.Text}}, nil
		})),
		WithFetcher(FetcherFunc(func(_ context.Context, source Source) (Source, error) { return source, nil })),
		WithEvidenceExtractor(EvidenceExtractorFunc(func(_ context.Context, source Source) ([]Evidence, error) {
			return []Evidence{{ID: "e-" + source.ID, SourceID: source.ID, Claim: "claim", Quote: "quote"}}, nil
		})),
		WithSynthesizer(SynthesizerFunc(func(context.Context, SynthesisInput) (agent.Response, error) {
			return agent.Response{Message: gopact.UserMessage("report")}, nil
		})),
		WithMaxParallelism(2),
	)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := target.Invoke(context.Background(), agent.Request{})
		done <- err
	}()
	for range 2 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("two discovery calls did not run concurrently")
		}
	}
	releaseDiscovery()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestStructuralCitationVerifierRejectsUnknownEvidence(t *testing.T) {
	err := (structuralCitationVerifier{}).Verify(context.Background(), SynthesisInput{
		Sources:   []Source{{ID: "s1"}},
		Evidence:  []Evidence{{ID: "e1", SourceID: "missing", Claim: "c", Quote: "q"}},
		Citations: []Citation{{EvidenceID: "e1", SourceID: "missing"}},
	})
	if err == nil {
		t.Fatal("Verify() error = nil")
	}
}

func TestDeepResearchRejectsInvalidLedgerBeforeCustomVerifier(t *testing.T) {
	verifierCalls := 0
	target, err := New(
		testIdentity(),
		WithPlanner(PlannerFunc(func(context.Context, PlanInput) ([]Query, error) {
			return []Query{{ID: "q1", Text: "topic"}}, nil
		})),
		WithDiscoverer(DiscovererFunc(func(context.Context, Query) ([]Source, error) {
			return []Source{{ID: "s1", QueryID: "q1", URI: "https://example.test", Title: "source"}}, nil
		})),
		WithFetcher(FetcherFunc(func(_ context.Context, source Source) (Source, error) { return source, nil })),
		WithEvidenceExtractor(EvidenceExtractorFunc(func(context.Context, Source) ([]Evidence, error) {
			return []Evidence{
				{ID: "duplicate", SourceID: "s1", Claim: "first", Quote: "first"},
				{ID: "duplicate", SourceID: "s1", Claim: "second", Quote: "second"},
			}, nil
		})),
		WithCitationVerifier(CitationVerifierFunc(func(context.Context, SynthesisInput) error {
			verifierCalls++
			return nil
		})),
		WithSynthesizer(SynthesizerFunc(func(context.Context, SynthesisInput) (agent.Response, error) {
			return agent.Response{Message: gopact.UserMessage("invalid")}, nil
		})),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = target.Invoke(context.Background(), agent.Request{})
	if err == nil || !strings.Contains(err.Error(), "duplicate evidence") || verifierCalls != 0 {
		t.Fatalf("Invoke() error/verifier calls = %v/%d, want structural rejection", err, verifierCalls)
	}
}

type mutatingVerifier struct{}

func (mutatingVerifier) Verify(_ context.Context, input SynthesisInput) error {
	input.Sources[0].QueryIDs[0] = "mutated"
	input.Evidence[0].Claim = "mutated"
	return nil
}

func TestDeepResearchProtectsSynthesisFromVerifierMutation(t *testing.T) {
	target, err := New(
		testIdentity(),
		WithPlanner(PlannerFunc(func(context.Context, PlanInput) ([]Query, error) {
			return []Query{{ID: "q1", Text: "topic"}}, nil
		})),
		WithDiscoverer(DiscovererFunc(func(context.Context, Query) ([]Source, error) {
			return []Source{{ID: "s1", QueryIDs: []string{"q1"}, URI: "https://example.test", Title: "source"}}, nil
		})),
		WithFetcher(FetcherFunc(func(_ context.Context, source Source) (Source, error) { return source, nil })),
		WithEvidenceExtractor(EvidenceExtractorFunc(func(context.Context, Source) ([]Evidence, error) {
			return []Evidence{{ID: "e1", SourceID: "s1", Claim: "original", Quote: "quote"}}, nil
		})),
		WithCitationVerifier(mutatingVerifier{}),
		WithSynthesizer(SynthesizerFunc(func(_ context.Context, input SynthesisInput) (agent.Response, error) {
			if input.Sources[0].QueryIDs[0] != "q1" || input.Evidence[0].Claim != "original" {
				return agent.Response{}, errors.New("synthesis received verifier mutation")
			}
			return agent.Response{Message: gopact.UserMessage("report")}, nil
		})),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := target.Invoke(context.Background(), agent.Request{}); err != nil {
		t.Fatal(err)
	}
}

func TestDeepResearchResumeDoesNotRepeatCommittedPhaseWork(t *testing.T) {
	for _, nodeID := range []string{"accept-plan", "record-source", "record-evidence", "verify-citations"} {
		t.Run(nodeID, func(t *testing.T) {
			calls := &researchCalls{}
			target := newResearchAgent(t, calls)
			sinkErr := errors.New("research sink failed")
			failed := false
			runID := "research-" + nodeID
			_, err := target.Invoke(
				context.Background(), agent.Request{}, gopact.WithRunID(runID),
				gopact.WithStrictEventHandler(func(_ context.Context, event gopact.Event) error {
					if event.RunID == runID && event.Type == workflow.EventNodeCompleted && event.NodeID == nodeID && !failed {
						failed = true
						return sinkErr
					}
					return nil
				}),
			)
			if !errors.Is(err, sinkErr) {
				t.Fatalf("Invoke() error = %v, want sink failure", err)
			}
			response, err := target.Invoke(context.Background(), agent.Request{}, workflow.WithResume(workflow.ResumeRequest{RunID: runID}))
			if err != nil {
				t.Fatal(err)
			}
			if response.Message.Parts[0].Text != "report" || *calls != (researchCalls{1, 2, 1, 1, 1, 1}) {
				t.Fatalf("response/calls = %+v/%+v, want each phase once", response, calls)
			}
		})
	}
}

func TestDeepResearchAgentConformance(t *testing.T) {
	target, err := New(
		testIdentity(),
		WithPlanner(PlannerFunc(func(context.Context, PlanInput) ([]Query, error) {
			return []Query{{ID: "q1", Text: "topic"}}, nil
		})),
		WithDiscoverer(DiscovererFunc(func(context.Context, Query) ([]Source, error) {
			return []Source{{ID: "s1", QueryID: "q1", URI: "https://example.test", Title: "source"}}, nil
		})),
		WithFetcher(FetcherFunc(func(_ context.Context, source Source) (Source, error) { return source, nil })),
		WithEvidenceExtractor(EvidenceExtractorFunc(func(context.Context, Source) ([]Evidence, error) {
			return []Evidence{{ID: "e1", SourceID: "s1", Claim: "claim", Quote: "quote"}}, nil
		})),
		WithSynthesizer(SynthesizerFunc(func(context.Context, SynthesisInput) (agent.Response, error) {
			return agent.Response{Message: gopact.UserMessage("report")}, nil
		})),
	)
	if err != nil {
		t.Fatal(err)
	}
	gopacttest.RequireAgentConformance(t, gopacttest.AgentConformanceCase{
		Agent: target, Request: agent.Request{},
		Validate: func(response agent.Response) error {
			if len(response.Message.Parts) == 0 {
				return errors.New("empty response")
			}
			return nil
		},
	})
}

func newResearchAgent(t *testing.T, calls *researchCalls, options ...Option) *Agent {
	t.Helper()
	configuration := []Option{
		WithPlanner(PlannerFunc(func(context.Context, PlanInput) ([]Query, error) {
			calls.planner++
			return []Query{{ID: "q1", Text: "first"}, {ID: "q2", Text: "second"}}, nil
		})),
		WithDiscoverer(DiscovererFunc(func(_ context.Context, query Query) ([]Source, error) {
			calls.discoverer++
			return []Source{{ID: "s1", QueryID: query.ID, URI: "https://example.test", Title: "source"}}, nil
		})),
		WithFetcher(FetcherFunc(func(_ context.Context, source Source) (Source, error) {
			calls.fetcher++
			source.Content = "content"
			return source, nil
		})),
		WithEvidenceExtractor(EvidenceExtractorFunc(func(context.Context, Source) ([]Evidence, error) {
			calls.extractor++
			return []Evidence{{ID: "e1", SourceID: "s1", Claim: "claim", Quote: "quote"}}, nil
		})),
		WithCitationVerifier(CitationVerifierFunc(func(context.Context, SynthesisInput) error {
			calls.verifier++
			return nil
		})),
		WithSynthesizer(SynthesizerFunc(func(_ context.Context, input SynthesisInput) (agent.Response, error) {
			calls.synthesis++
			if len(input.Sources) != 1 || len(input.Citations) != 1 {
				return agent.Response{}, errors.New("unexpected synthesis ledger")
			}
			return agent.Response{Message: gopact.UserMessage("report")}, nil
		})),
		WithMaxParallelism(1),
	}
	configuration = append(configuration, options...)
	target, err := New(testIdentity(), configuration...)
	if err != nil {
		t.Fatal(err)
	}
	return target
}

func testIdentity() agent.Identity {
	return agent.Identity{Name: "research", Description: "research", Version: "v1"}
}
