//go:build integration

package workflowtest

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/gopact-ai/gopact/workflow"
)

func TestWorkflowFanOutJoin(t *testing.T) {
	wf := workflow.New[string, string]("fanout")
	plan := wf.Node[string, string]("plan", func(_ context.Context, input string) (string, error) {
		return input, nil
	})
	score := wf.Node[string, string]("score", func(_ context.Context, input string) (string, error) {
		return input + "!", nil
	})
	report := wf.Merge[string]("report", func(_ context.Context, in workflow.Inputs) (string, error) {
		values, err := in.All(score)
		if err != nil {
			return "", err
		}
		sort.Strings(values)
		return strings.Join(values, ","), nil
	})
	plan.Route(func(_ context.Context, _ string) (workflow.Dispatch, error) {
		return plan.Each(score, "left", "right"), nil
	})
	wf.Entry(plan)
	wf.Edge(plan, score)
	wf.Edge(score, report)
	wf.Exit(report)
	out, err := wf.Invoke(context.Background(), "start")
	if err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if out != "left!,right!" {
		t.Fatalf("Invoke() = %q, want left!,right!", out)
	}
}
