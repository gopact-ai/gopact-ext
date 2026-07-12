package deepresearch_test

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact-ext/agents/deepresearch"
	"github.com/gopact-ai/gopact/agent"
)

// ExampleNew demonstrates the plan, discovery, fetch, evidence, and synthesis research pipeline.
func ExampleNew() {
	target, err := deepresearch.New(
		agent.Identity{Name: "researcher", Description: "builds cited reports", Version: "v1"},
		deepresearch.WithPlanner(deepresearch.PlannerFunc(func(context.Context, deepresearch.PlanInput) ([]deepresearch.Query, error) {
			return []deepresearch.Query{{ID: "q1", Text: "release safety"}}, nil
		})),
		deepresearch.WithDiscoverer(deepresearch.DiscovererFunc(func(context.Context, deepresearch.Query) ([]deepresearch.Source, error) {
			return []deepresearch.Source{{
				ID: "s1", QueryID: "q1", URI: "https://example.test/safety", Title: "Safety report",
			}}, nil
		})),
		deepresearch.WithFetcher(deepresearch.FetcherFunc(func(_ context.Context, source deepresearch.Source) (deepresearch.Source, error) {
			source.Content = "all checks passed"
			return source, nil
		})),
		deepresearch.WithEvidenceExtractor(deepresearch.EvidenceExtractorFunc(func(context.Context, deepresearch.Source) ([]deepresearch.Evidence, error) {
			return []deepresearch.Evidence{{
				ID: "e1", SourceID: "s1", Claim: "release is safe", Quote: "all checks passed",
			}}, nil
		})),
		deepresearch.WithSynthesizer(deepresearch.SynthesizerFunc(func(_ context.Context, input deepresearch.SynthesisInput) (agent.Response, error) {
			return agent.Response{
				Message: gopact.UserMessage(fmt.Sprintf("report with %d citation", len(input.Citations))),
			}, nil
		})),
	)
	if err != nil {
		fmt.Println("new:", err)
		return
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	logEvent := gopact.WithEventHandler(func(ctx context.Context, event gopact.Event) error {
		logger.InfoContext(
			ctx,
			"workflow event",
			"type", event.Type,
			"session_id", event.SessionID,
			"run_id", event.RunID,
			"parent_run_id", event.ParentRunID,
			"node_id", event.NodeID,
			"node_version", event.NodeExecutionVersion,
			"origin", event.Origin,
		)
		return nil
	})
	response, err := target.Invoke(
		context.Background(),
		agent.Request{Messages: []gopact.Message{gopact.UserMessage("research release safety")}},
		gopact.WithSessionID("deepresearch-example"),
		gopact.WithRunID("deepresearch-run"),
		logEvent,
	)
	if err != nil {
		fmt.Println("invoke:", err)
		return
	}
	fmt.Println(response.Message.Parts[0].Text)
	// Output: report with 1 citation
}
