package acp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sync"

	protocol "github.com/gopact-ai/acp"
	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact/agent"
)

// Agent invokes one remote ACP Agent over a caller-owned connection.
type Agent struct {
	identity agent.Identity
	caller   *protocol.AgentCaller
	conn     *protocol.Conn
	cwd      string

	mu    sync.Mutex
	turns map[protocol.SessionID]*turn
}

var _ agent.Agent = (*Agent)(nil)

// Option configures an Agent during construction.
type Option interface{ apply(*config) }

type optionFunc func(*config)

func (option optionFunc) apply(config *config) { option(config) }

type config struct{ cwd string }

// WithCwd sets the working directory sent when creating remote ACP sessions.
// The zero value sends an empty working directory.
func WithCwd(cwd string) Option {
	return optionFunc(func(config *config) { config.cwd = cwd })
}

// New connects to and initializes one remote ACP Agent.
func New(ctx context.Context, identity agent.Identity, input io.ReadCloser, output io.Writer, options ...Option) (*Agent, error) {
	if ctx == nil {
		return nil, errors.New("acp: context is nil")
	}
	if identity.Name == "" || identity.Description == "" || identity.Version == "" {
		return nil, errors.New("acp: agent identity is incomplete")
	}
	if isNil(input) || isNil(output) {
		return nil, errors.New("acp: input and output are required")
	}
	configuration := config{}
	for _, option := range options {
		if option == nil {
			return nil, errors.New("acp: option is nil")
		}
		option.apply(&configuration)
	}
	target := &Agent{
		identity: identity,
		cwd:      configuration.cwd,
		turns:    make(map[protocol.SessionID]*turn),
	}
	conn, err := protocol.NewClient(input, output, func(caller *protocol.AgentCaller) protocol.ClientHandler {
		target.caller = caller
		return target
	})
	if err != nil {
		return nil, err
	}
	target.conn = conn
	initialized, err := target.caller.Initialize(ctx, &protocol.InitializeRequest{
		ProtocolVersion: protocol.ProtocolVersionV1,
	})
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("acp: initialize: %w", err)
	}
	if initialized.ProtocolVersion != protocol.ProtocolVersionV1 {
		_ = conn.Close()
		return nil, fmt.Errorf("acp: unsupported protocol version %d", initialized.ProtocolVersion)
	}
	return target, nil
}

func isNil(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

// Identity returns the immutable local identity for the remote Agent.
func (target *Agent) Identity() agent.Identity {
	if target == nil {
		return agent.Identity{}
	}
	return target.identity
}

// Invoke sends one prompt turn to the remote ACP Agent.
func (target *Agent) Invoke(ctx context.Context, request agent.Request, options ...gopact.RunOption) (agent.Response, error) {
	if target == nil || target.conn == nil {
		return agent.Response{}, errors.New("acp: agent is nil")
	}
	if err := ctx.Err(); err != nil {
		return agent.Response{}, err
	}
	config := gopact.ResolveRunOptions(options...)
	if err := config.RunConfigError(); err != nil {
		return agent.Response{}, err
	}
	content, err := promptContent(request.Clone())
	if err != nil {
		return agent.Response{}, err
	}
	sessionID, err := target.newSession(ctx)
	if err != nil {
		return agent.Response{}, err
	}
	active, err := target.startTurn(sessionID)
	if err != nil {
		return agent.Response{}, err
	}
	defer target.finishTurn(sessionID, active)

	response, err := target.caller.Prompt(ctx, &protocol.PromptRequest{SessionID: sessionID, Prompt: content})
	if err != nil {
		if ctx.Err() != nil {
			return agent.Response{}, ctx.Err()
		}
		return agent.Response{}, fmt.Errorf("acp: prompt: %w", err)
	}
	if response.StopReason == protocol.StopReasonCanceled {
		return agent.Response{}, context.Canceled
	}
	result, err := active.response()
	if err != nil {
		return agent.Response{}, err
	}
	if len(result.Message.Parts) == 0 {
		return agent.Response{}, errors.New("acp: prompt returned no agent message")
	}
	if response.StopReason != protocol.StopReasonEndTurn {
		result.Metadata = map[string]string{"acp.stop_reason": string(response.StopReason)}
	}
	return result, nil
}

// Close stops the ACP connection.
func (target *Agent) Close() error {
	if target == nil || target.conn == nil {
		return nil
	}
	return target.conn.Close()
}

func (target *Agent) newSession(ctx context.Context) (protocol.SessionID, error) {
	created, err := target.caller.NewSession(ctx, &protocol.NewSessionRequest{Cwd: target.cwd})
	if err != nil {
		return "", fmt.Errorf("acp: create session: %w", err)
	}
	return created.SessionID, nil
}

func (target *Agent) startTurn(sessionID protocol.SessionID) (*turn, error) {
	target.mu.Lock()
	defer target.mu.Unlock()
	if target.turns[sessionID] != nil {
		return nil, errors.New("acp: session prompt already active")
	}
	active := &turn{}
	target.turns[sessionID] = active
	return active, nil
}

func (target *Agent) finishTurn(sessionID protocol.SessionID, active *turn) {
	target.mu.Lock()
	defer target.mu.Unlock()
	if target.turns[sessionID] == active {
		delete(target.turns, sessionID)
	}
}

func (target *Agent) RequestPermission(context.Context, *protocol.RequestPermissionRequest) (*protocol.RequestPermissionResponse, error) {
	return &protocol.RequestPermissionResponse{Outcome: protocol.CanceledRequestPermissionOutcome()}, nil
}

func (target *Agent) Update(_ context.Context, notification *protocol.SessionNotification) error {
	target.mu.Lock()
	active := target.turns[notification.SessionID]
	target.mu.Unlock()
	if active == nil {
		return errors.New("acp: update has no active prompt")
	}
	return active.add(notification.Update)
}

type turn struct {
	mu        sync.Mutex
	parts     []gopact.MessagePart
	artifacts []gopact.ArtifactRef
	err       error
}

func (active *turn) add(update protocol.SessionUpdate) error {
	if update.SessionUpdate != protocol.SessionUpdateTypeAgentMessageChunk {
		return nil
	}
	content, ok := update.Content.(protocol.ContentBlock)
	if !ok {
		return errors.New("acp: agent message update has invalid content")
	}
	active.mu.Lock()
	defer active.mu.Unlock()
	if active.err != nil {
		return nil
	}
	switch content.Type {
	case protocol.ContentBlockTypeText:
		active.parts = append(active.parts, gopact.MessagePart{Type: gopact.MessagePartTypeText, Text: content.Text})
	case protocol.ContentBlockTypeResourceLink:
		return active.addResourceLink(content)
	default:
		active.err = fmt.Errorf("acp: unsupported response content %q", content.Type)
	}
	return active.err
}

func (active *turn) addResourceLink(content protocol.ContentBlock) error {
	if content.URI == nil {
		active.err = errors.New("acp: resource link URI is required")
		return active.err
	}
	ref := gopact.ArtifactRef{URI: *content.URI, Kind: content.Name}
	active.parts = append(active.parts, gopact.MessagePart{Type: gopact.MessagePartTypeArtifact, Ref: &ref})
	active.artifacts = append(active.artifacts, ref)
	return nil
}

func (active *turn) response() (agent.Response, error) {
	active.mu.Lock()
	defer active.mu.Unlock()
	if active.err != nil {
		return agent.Response{}, active.err
	}
	return agent.Response{
		Message:   gopact.Message{Role: gopact.MessageRoleAssistant, Parts: append([]gopact.MessagePart(nil), active.parts...)},
		Artifacts: append([]gopact.ArtifactRef(nil), active.artifacts...),
	}, nil
}

func promptContent(request agent.Request) ([]protocol.ContentBlock, error) {
	var content []protocol.ContentBlock
	seen := make(map[gopact.ArtifactRef]struct{})
	for index, message := range request.Messages {
		blocks, refs, err := messageContent(message)
		if err != nil {
			return nil, fmt.Errorf("acp: request message %d: %w", index, err)
		}
		content = append(content, blocks...)
		for _, ref := range refs {
			seen[ref] = struct{}{}
		}
	}
	for _, artifact := range request.Artifacts {
		if _, exists := seen[artifact]; exists {
			continue
		}
		block, err := artifactContent(artifact)
		if err != nil {
			return nil, err
		}
		content = append(content, block)
	}
	return content, nil
}

func messageContent(message gopact.Message) ([]protocol.ContentBlock, []gopact.ArtifactRef, error) {
	if len(message.ToolCalls) > 0 || message.ToolCallID != "" {
		return nil, nil, errors.New("tool messages are unsupported")
	}
	var content []protocol.ContentBlock
	var refs []gopact.ArtifactRef
	for _, part := range message.Parts {
		block, ref, err := messagePartContent(part)
		if err != nil {
			return nil, nil, err
		}
		content = append(content, block)
		refs = append(refs, ref...)
	}
	return content, refs, nil
}

func messagePartContent(part gopact.MessagePart) (protocol.ContentBlock, []gopact.ArtifactRef, error) {
	switch {
	case part.Type == gopact.MessagePartTypeText && part.Ref == nil:
		return protocol.TextContentBlock(part.Text), nil, nil
	case part.Type == gopact.MessagePartTypeArtifact && part.Ref != nil:
		block, err := artifactContent(*part.Ref)
		return block, []gopact.ArtifactRef{*part.Ref}, err
	default:
		return protocol.ContentBlock{}, nil, fmt.Errorf("unsupported message part %q", part.Type)
	}
}

func artifactContent(artifact gopact.ArtifactRef) (protocol.ContentBlock, error) {
	if artifact.URI == "" {
		return protocol.ContentBlock{}, errors.New("artifact URI is required")
	}
	name := artifact.Kind
	if name == "" {
		name = artifact.URI
	}
	return protocol.ResourceLinkContentBlock(name, artifact.URI), nil
}
