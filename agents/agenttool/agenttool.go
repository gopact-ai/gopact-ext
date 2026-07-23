// Package agenttool adapts one Agent into a Run-aware tool.
package agenttool

import (
	"context"
	"errors"
	"fmt"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact-ext/agents/internal/contract"
	"github.com/gopact-ai/gopact/agent"
)

// Adapter maps the tool and Agent protocol boundaries.
// Input must be deterministic and side-effect-free because durable resume replays it.
type Adapter interface {
	Input(context.Context, gopact.ToolCall) (agent.Request, error)
	Output(context.Context, agent.Response) (gopact.ToolResult, error)
}

// AdapterFuncs adapts two functions into an Adapter.
type AdapterFuncs struct {
	InputFunc  func(context.Context, gopact.ToolCall) (agent.Request, error)
	OutputFunc func(context.Context, agent.Response) (gopact.ToolResult, error)
}

func (adapter AdapterFuncs) validate() error {
	return contract.New("agenttool").
		Present("input adapter", adapter.InputFunc).
		Present("output adapter", adapter.OutputFunc).
		Err()
}

// Input maps one tool call into a child Agent request.
func (adapter AdapterFuncs) Input(ctx context.Context, call gopact.ToolCall) (agent.Request, error) {
	if adapter.InputFunc == nil {
		return agent.Request{}, errors.New("agenttool: input adapter is nil")
	}
	request, err := adapter.InputFunc(ctx, cloneToolCall(call))
	return request.Clone(), err
}

// Output maps one child Agent response into a tool result.
func (adapter AdapterFuncs) Output(ctx context.Context, response agent.Response) (gopact.ToolResult, error) {
	if adapter.OutputFunc == nil {
		return gopact.ToolResult{}, errors.New("agenttool: output adapter is nil")
	}
	result, err := adapter.OutputFunc(ctx, response.Clone())
	return cloneToolResult(result), err
}

// Tool exposes one child Agent through a typed Workflow boundary.
type Tool struct {
	spec    gopact.ToolSpec
	child   agent.Agent
	adapter Adapter
}

var _ agent.InvokableTool = (*Tool)(nil)

// New creates an immutable Agent-backed Tool.
func New(spec gopact.ToolSpec, child agent.Agent, adapter Adapter) (*Tool, error) {
	spec = cloneToolSpec(spec)
	if err := contract.New("agenttool").
		Required("tool name", spec.Name).
		OptionalJSON("tool schema", spec.Schema).
		Present("child", child).
		Present("adapter", adapter).
		Err(); err != nil {
		return nil, err
	}
	if validated, ok := adapter.(interface{ validate() error }); ok {
		if err := validated.validate(); err != nil {
			return nil, err
		}
	}
	if err := contract.New("agenttool").Identity("child", child.Identity()).Err(); err != nil {
		return nil, err
	}
	return &Tool{spec: spec, child: child, adapter: adapter}, nil
}

// Spec returns an independent model-visible tool contract.
func (tool *Tool) Spec() gopact.ToolSpec {
	if tool == nil {
		return gopact.ToolSpec{}
	}
	return cloneToolSpec(tool.spec)
}

// Invoke maps one tool call to the child Agent and forwards Workflow child options.
func (tool *Tool) Invoke(ctx context.Context, call gopact.ToolCall, options ...gopact.RunOption) (gopact.ToolOutcome, error) {
	if tool == nil {
		return nil, errors.New("agenttool: tool is nil")
	}
	if err := tool.validateCall(call); err != nil {
		return nil, err
	}
	request, err := tool.adapter.Input(ctx, cloneToolCall(call))
	if err != nil {
		return nil, fmt.Errorf("agenttool: adapt input: %w", err)
	}
	response, err := tool.child.Invoke(ctx, request.Clone(), options...)
	if err != nil {
		return nil, fmt.Errorf("agenttool: invoke child: %w", err)
	}
	result, err := tool.adapter.Output(ctx, response.Clone())
	if err != nil {
		return nil, fmt.Errorf("agenttool: adapt output: %w", err)
	}
	return gopact.ToolResultOutcome{CallID: call.ID, Name: call.Name, Result: cloneToolResult(result)}, nil
}

func (tool *Tool) validateCall(call gopact.ToolCall) error {
	if err := contract.New("agenttool").
		Required("tool call id", call.ID).
		Required("tool call name", call.Name).
		OptionalJSON("tool call arguments", call.Arguments).
		Err(); err != nil {
		return err
	}
	if call.Name != tool.spec.Name {
		return fmt.Errorf("agenttool: call name %q does not match tool %q", call.Name, tool.spec.Name)
	}
	return nil
}

func cloneToolCall(call gopact.ToolCall) gopact.ToolCall {
	call.Arguments = append([]byte(nil), call.Arguments...)
	return call
}

func cloneToolSpec(spec gopact.ToolSpec) gopact.ToolSpec {
	spec.Schema = append([]byte(nil), spec.Schema...)
	spec.Metadata = cloneStringMap(spec.Metadata)
	return spec
}

func cloneToolResult(result gopact.ToolResult) gopact.ToolResult {
	result.ArtifactRefs = append([]gopact.ArtifactRef(nil), result.ArtifactRefs...)
	result.EffectRefs = append([]gopact.ArtifactRef(nil), result.EffectRefs...)
	return result
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
