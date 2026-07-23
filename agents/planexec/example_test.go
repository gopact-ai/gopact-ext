package planexec_test

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact-ext/agents/planexec"
	"github.com/gopact-ai/gopact/agent"
)

type localAgent struct {
	identity agent.Identity
	result   string
}

func (target localAgent) Identity() agent.Identity { return target.identity }

func (target localAgent) Invoke(context.Context, agent.Request, ...gopact.RunOption) (agent.Response, error) {
	return agent.Response{Message: gopact.UserMessage(target.result)}, nil
}

// ExampleNew demonstrates planning, executing, replanning, and reporting one task.
func ExampleNew() {
	catalog := agent.NewCatalog()
	if err := catalog.Add(localAgent{
		identity: agent.Identity{Name: "worker", Description: "verifies releases", Version: "v1"},
		result:   "verified",
	}); err != nil {
		fmt.Println("add worker:", err)
		return
	}
	directory, err := catalog.Compile()
	if err != nil {
		fmt.Println("compile:", err)
		return
	}
	target, err := planexec.New(
		agent.Identity{Name: "release-manager", Description: "plans release verification", Version: "v1"},
		planexec.WithDirectory(directory),
		planexec.WithPlanner(planexec.PlannerFunc(func(context.Context, agent.Request) (planexec.Plan, error) {
			return planexec.Plan{
				ID:      "release",
				Version: 1,
				Steps: []planexec.Step{{
					ID:          "verify",
					Description: "verify release",
					AgentName:   "worker",
					Status:      planexec.StepPending,
				}},
			}, nil
		})),
		planexec.WithReplanner(planexec.ReplannerFunc(func(context.Context, planexec.ReplanInput) (planexec.ReplanDecision, error) {
			return planexec.ReplanDecision{Done: true}, nil
		})),
		planexec.WithReporter(planexec.ReporterFunc(func(_ context.Context, input planexec.ReportInput) (agent.Response, error) {
			return agent.Response{Message: gopact.UserMessage("report: " + input.Results[0].Response.Message.Parts[0].Text)}, nil
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
		agent.Request{Messages: []gopact.Message{gopact.UserMessage("release")}},
		gopact.WithSessionID("planexec-example"),
		gopact.WithRunID("planexec-run"),
		logEvent,
	)
	if err != nil {
		fmt.Println("invoke:", err)
		return
	}
	fmt.Println(response.Message.Parts[0].Text)
	// Output: report: verified
}
