// Package supervisor provides a minimal child-agent routing template.
package supervisor

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"strings"

	"github.com/gopact-ai/gopact"
)

var (
	ErrRouterRequired     = errors.New("supervisor: router is required")
	ErrChildRequired      = errors.New("supervisor: child runnable is required")
	ErrChildNameRequired  = errors.New("supervisor: child name is required")
	ErrRouteAgentRequired = errors.New("supervisor: routed agent is required")
	ErrRouteAgentUnknown  = errors.New("supervisor: routed agent is unknown")
	ErrInvalidInput       = errors.New("supervisor: invalid input")
)

type State struct {
	Task          string
	SelectedAgent string
	Trace         []string
}

type Request struct {
	Task  string
	Input any
}

type Route struct {
	Agent string
	Input any
}

type Router interface {
	Route(context.Context, Request) (Route, error)
}

type RouterFunc func(context.Context, Request) (Route, error)

func (f RouterFunc) Route(ctx context.Context, request Request) (Route, error) {
	if f == nil {
		return Route{}, ErrRouterRequired
	}
	return f(ctx, request)
}

type Child struct {
	Name     string
	Runnable gopact.EventRunnable
}

type Agent struct {
	router   Router
	children map[string]gopact.EventRunnable
}

var _ gopact.StateRunnable[State] = (*Agent)(nil)

func New(router Router, children ...Child) (*Agent, error) {
	if router == nil {
		return nil, ErrRouterRequired
	}
	agent := &Agent{router: router, children: make(map[string]gopact.EventRunnable, len(children))}
	for _, child := range children {
		name := strings.TrimSpace(child.Name)
		if name == "" {
			return nil, ErrChildNameRequired
		}
		if child.Runnable == nil {
			return nil, ErrChildRequired
		}
		agent.children[name] = child.Runnable
	}
	return agent, nil
}

// Invoke runs the supervisor through the typed result-first API.
func (a *Agent) Invoke(ctx context.Context, input State, opts ...gopact.RunOption) (State, error) {
	var output State
	cfg := gopact.ResolveRunOptions(opts...)
	for event, err := range a.Run(ctx, input, opts...) {
		if cfg.EventSink != nil {
			if sinkErr := cfg.EventSink.Emit(ctx, event); sinkErr != nil {
				return output, sinkErr
			}
		}
		if snapshot := event.StepSnapshot; snapshot != nil {
			if state, ok := snapshot.Output.(State); ok {
				output = state
			}
		}
		if err != nil {
			return output, err
		}
	}
	return output, nil
}

func (a *Agent) Run(ctx context.Context, input any, opts ...gopact.RunOption) iter.Seq2[gopact.Event, error] {
	return func(yield func(gopact.Event, error) bool) {
		if ctx == nil {
			ctx = context.TODO()
		}
		cfg := gopact.ResolveRunOptions(opts...)
		contextIDs, _ := gopact.RuntimeIDsFromContext(ctx)
		ids := cfg.IDs.WithDefaults(contextIDs)
		ctx = gopact.ContextWithRuntimeIDs(ctx, ids)

		state, err := inputState(input)
		if err != nil {
			yield(runFailed(ids, "", err), err)
			return
		}
		if a == nil || a.router == nil {
			yield(runFailed(ids, "", ErrRouterRequired), ErrRouterRequired)
			return
		}
		if !yield(gopact.Event{Type: gopact.EventRunStarted, IDs: ids}.WithRuntimeDefaults(ids), nil) {
			return
		}
		if !yield(routeStarted(ids, state), nil) {
			return
		}

		route, err := a.router.Route(ctx, Request{Task: state.Task, Input: input})
		if err != nil {
			yield(runFailed(ids, "", err), err)
			return
		}
		route.Agent = strings.TrimSpace(route.Agent)
		if route.Agent == "" {
			yield(runFailed(ids, "", ErrRouteAgentRequired), ErrRouteAgentRequired)
			return
		}
		child, ok := a.children[route.Agent]
		if !ok {
			err := fmt.Errorf("%w: %s", ErrRouteAgentUnknown, route.Agent)
			yield(runFailed(ids, route.Agent, err), err)
			return
		}

		state.SelectedAgent = route.Agent
		state.Trace = append(state.Trace, "route:"+route.Agent)
		if !yield(routeCompleted(ids, state), nil) {
			return
		}

		childIDs := ids
		childIDs.AgentID = route.Agent
		childOpts := append([]gopact.RunOption(nil), opts...)
		childOpts = append(childOpts, gopact.WithRuntimeIDs(childIDs))
		childInput := route.Input
		if childInput == nil {
			childInput = input
		}
		for event, childErr := range child.Run(gopact.ContextWithRuntimeIDs(ctx, childIDs), childInput, childOpts...) {
			event = event.WithRuntimeDefaults(childIDs)
			if childErr != nil && event.Err == nil {
				event.Err = childErr
			}
			if !yield(event, nil) {
				return
			}
			if childErr != nil {
				yield(runFailed(ids, route.Agent, childErr), childErr)
				return
			}
		}
		yield(gopact.Event{
			Type:     gopact.EventRunCompleted,
			IDs:      ids,
			Metadata: map[string]any{"selected_agent": route.Agent},
		}.WithRuntimeDefaults(ids), nil)
	}
}

func inputState(input any) (State, error) {
	switch value := input.(type) {
	case State:
		if strings.TrimSpace(value.Task) == "" {
			return State{}, ErrInvalidInput
		}
		return value, nil
	case string:
		if strings.TrimSpace(value) == "" {
			return State{}, ErrInvalidInput
		}
		return State{Task: value}, nil
	default:
		return State{}, fmt.Errorf("%w: got %T", ErrInvalidInput, input)
	}
}

func routeStarted(ids gopact.RuntimeIDs, state State) gopact.Event {
	return gopact.Event{
		Type: gopact.EventNodeStarted,
		IDs:  ids,
		Node: "route",
		Step: 1,
		StepSnapshot: &gopact.StepSnapshot{
			ID:    "supervisor:route",
			Step:  1,
			Node:  "route",
			Phase: gopact.StepRunning,
			IDs:   ids,
			Input: state,
		},
		Metadata: map[string]any{"template": "supervisor"},
	}.WithRuntimeDefaults(ids)
}

func routeCompleted(ids gopact.RuntimeIDs, state State) gopact.Event {
	return gopact.Event{
		Type: gopact.EventNodeCompleted,
		IDs:  ids,
		Node: "route",
		Step: 1,
		StepSnapshot: &gopact.StepSnapshot{
			ID:       "supervisor:route",
			Step:     1,
			Node:     "route",
			Phase:    gopact.StepCompleted,
			IDs:      ids,
			Output:   state,
			Metadata: map[string]any{"selected_agent": state.SelectedAgent},
		},
		Metadata: map[string]any{
			"template":       "supervisor",
			"selected_agent": state.SelectedAgent,
		},
	}.WithRuntimeDefaults(ids)
}

func runFailed(ids gopact.RuntimeIDs, selectedAgent string, err error) gopact.Event {
	metadata := map[string]any{"template": "supervisor"}
	if selectedAgent != "" {
		metadata["selected_agent"] = selectedAgent
	}
	return gopact.Event{
		Type:     gopact.EventRunFailed,
		IDs:      ids,
		Err:      err,
		Metadata: metadata,
	}.WithRuntimeDefaults(ids)
}
