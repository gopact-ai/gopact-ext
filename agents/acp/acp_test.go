package acp

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"

	protocol "github.com/gopact-ai/acp"
	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact/agent"
)

func TestNewRejectsInvalidConfiguration(t *testing.T) {
	identity := agent.Identity{Name: "remote", Description: "remote", Version: "v1"}
	left, right := net.Pipe()
	defer func() { _ = left.Close() }()
	defer func() { _ = right.Close() }()
	tests := []struct {
		name    string
		ctx     context.Context
		input   io.ReadCloser
		output  io.Writer
		options []Option
	}{
		{name: "nil context", input: left, output: left},
		{name: "nil input", ctx: t.Context(), output: left},
		{name: "nil output", ctx: t.Context(), input: left},
		{name: "nil option", ctx: t.Context(), input: left, output: left, options: []Option{nil}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := New(test.ctx, identity, test.input, test.output, test.options...); err == nil {
				t.Fatal("New() error = nil")
			}
		})
	}
}

func TestAgentRoundTripCreatesSessionsAndCancelsPrompt(t *testing.T) {
	clientTransport, agentTransport := net.Pipe()
	remote := &remoteAgent{started: make(chan struct{})}
	remoteConn, err := protocol.NewAgent(agentTransport, agentTransport, func(client *protocol.ClientCaller) protocol.AgentHandler {
		remote.client = client
		return remote
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = remoteConn.Close() }()

	target, err := New(
		t.Context(),
		agent.Identity{Name: "remote", Description: "remote ACP agent", Version: "v1"},
		clientTransport,
		clientTransport,
		WithCwd("/workspace"),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = target.Close() }()

	request := agent.Request{
		Messages:  []gopact.Message{gopact.UserMessage("hello")},
		Artifacts: []gopact.ArtifactRef{{URI: "file:///workspace/spec.md", Kind: "spec"}},
	}
	for range 2 {
		response, invokeErr := target.Invoke(t.Context(), request, gopact.WithSessionID("local-session"))
		if invokeErr != nil {
			t.Fatal(invokeErr)
		}
		if len(response.Message.Parts) != 2 || response.Message.Parts[0].Text != "hello back" ||
			response.Message.Parts[1].Ref == nil || response.Message.Parts[1].Ref.URI != "artifact://answer" ||
			len(response.Artifacts) != 1 || response.Artifacts[0].URI != "artifact://answer" {
			t.Fatalf("Invoke() response = %+v", response)
		}
	}
	newSessions, prompts := remote.snapshot()
	if newSessions != 2 || len(prompts) != 2 || prompts[0].SessionID == prompts[1].SessionID ||
		len(prompts[0].Prompt) != 2 || prompts[0].Prompt[0].Text != "hello" ||
		prompts[0].Prompt[1].URI == nil || *prompts[0].Prompt[1].URI != "file:///workspace/spec.md" {
		t.Fatalf("remote calls: new=%d prompts=%+v", newSessions, prompts)
	}

	remote.block = true
	ctx, cancel := context.WithCancel(context.Background())
	invokeDone := make(chan error, 1)
	go func() {
		_, invokeErr := target.Invoke(ctx, agent.Request{Messages: []gopact.Message{gopact.UserMessage("wait")}}, gopact.WithSessionID("cancel-session"))
		invokeDone <- invokeErr
	}()
	<-remote.started
	cancel()
	if err := <-invokeDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Invoke() error = %v", err)
	}
}

func TestAgentSupportsConcurrentInvocations(t *testing.T) {
	clientTransport, agentTransport := net.Pipe()
	remote := &remoteAgent{}
	remoteConn, err := protocol.NewAgent(agentTransport, agentTransport, func(client *protocol.ClientCaller) protocol.AgentHandler {
		remote.client = client
		return remote
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = remoteConn.Close() }()
	target, err := New(t.Context(), agent.Identity{Name: "remote", Description: "remote", Version: "v1"}, clientTransport, clientTransport)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = target.Close() }()

	const calls = 4
	results := make(chan error, calls)
	for range calls {
		go func() {
			response, invokeErr := target.Invoke(t.Context(), agent.Request{Messages: []gopact.Message{gopact.UserMessage("hello")}})
			if invokeErr == nil && response.Message.Parts[0].Text != "hello back" {
				invokeErr = errors.New("unexpected response")
			}
			results <- invokeErr
		}()
	}
	for range calls {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	newSessions, _ := remote.snapshot()
	if newSessions != calls {
		t.Fatalf("remote sessions = %d, want %d", newSessions, calls)
	}
}

func TestAgentRequiresSessionIDAndRejectsUnsupportedContent(t *testing.T) {
	clientTransport, agentTransport := net.Pipe()
	remoteConn, err := protocol.NewAgent(agentTransport, agentTransport, func(*protocol.ClientCaller) protocol.AgentHandler {
		return &remoteAgent{}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = remoteConn.Close() }()
	target, err := New(t.Context(), agent.Identity{Name: "remote", Description: "remote", Version: "v1"}, clientTransport, clientTransport)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = target.Close() }()
	if _, err := target.Invoke(t.Context(), agent.Request{}, gopact.WithSessionID("one"), gopact.WithSessionID("two")); !errors.Is(err, gopact.ErrRunConfig) {
		t.Fatalf("Invoke() error = %v, want ErrRunConfig", err)
	}
	if _, err := promptContent(agent.Request{Messages: []gopact.Message{{
		Role:  gopact.MessageRoleUser,
		Parts: []gopact.MessagePart{{Type: "image"}},
	}}}); err == nil {
		t.Fatal("promptContent() unsupported part error = nil")
	}
	for _, request := range []agent.Request{
		{Artifacts: []gopact.ArtifactRef{{Kind: "spec"}}},
		{Messages: []gopact.Message{{Parts: []gopact.MessagePart{{Type: gopact.MessagePartTypeArtifact, Ref: &gopact.ArtifactRef{Kind: "spec"}}}}}},
	} {
		if _, err := promptContent(request); err == nil {
			t.Fatal("promptContent() empty artifact URI error = nil")
		}
	}
	content, err := promptContent(agent.Request{Artifacts: []gopact.ArtifactRef{{URI: "artifact://unnamed"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(content) != 1 || content[0].Name != "artifact://unnamed" {
		t.Fatalf("promptContent() unnamed artifact = %+v", content)
	}
}

func TestAgentHandlesNonTerminalStopReason(t *testing.T) {
	clientTransport, agentTransport := net.Pipe()
	remote := &remoteAgent{stopReason: protocol.StopReasonMaxTokens}
	remoteConn, err := protocol.NewAgent(agentTransport, agentTransport, func(client *protocol.ClientCaller) protocol.AgentHandler {
		remote.client = client
		return remote
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = remoteConn.Close() }()
	target, err := New(t.Context(), agent.Identity{Name: "remote", Description: "remote", Version: "v1"}, clientTransport, clientTransport)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = target.Close() }()
	response, err := target.Invoke(t.Context(), agent.Request{Messages: []gopact.Message{gopact.UserMessage("hello")}})
	if err != nil {
		t.Fatal(err)
	}
	if response.Metadata["acp.stop_reason"] != string(protocol.StopReasonMaxTokens) || response.Message.Parts[0].Text != "hello back" {
		t.Fatalf("Invoke() response = %+v", response)
	}
}

type remoteAgent struct {
	mu         sync.Mutex
	client     *protocol.ClientCaller
	sessions   int
	prompts    []protocol.PromptRequest
	block      bool
	started    chan struct{}
	stopReason protocol.StopReason
}

func (*remoteAgent) Initialize(context.Context, *protocol.InitializeRequest) (*protocol.InitializeResponse, error) {
	return &protocol.InitializeResponse{ProtocolVersion: protocol.ProtocolVersionV1}, nil
}

func (remote *remoteAgent) NewSession(context.Context, *protocol.NewSessionRequest) (*protocol.NewSessionResponse, error) {
	remote.mu.Lock()
	defer remote.mu.Unlock()
	remote.sessions++
	return &protocol.NewSessionResponse{SessionID: protocol.SessionID("remote-session-" + string(rune('0'+remote.sessions)))}, nil
}

func (remote *remoteAgent) Prompt(ctx context.Context, request *protocol.PromptRequest) (*protocol.PromptResponse, error) {
	remote.mu.Lock()
	remote.prompts = append(remote.prompts, *request)
	block := remote.block
	remote.mu.Unlock()
	if block {
		close(remote.started)
		<-ctx.Done()
		return &protocol.PromptResponse{StopReason: protocol.StopReasonCanceled}, nil
	}
	if err := remote.client.Update(ctx, &protocol.SessionNotification{
		SessionID: request.SessionID,
		Update:    protocol.AgentMessageChunkSessionUpdate(protocol.TextContentBlock("hello back")),
	}); err != nil {
		return nil, err
	}
	if err := remote.client.Update(ctx, &protocol.SessionNotification{
		SessionID: request.SessionID,
		Update:    protocol.AgentMessageChunkSessionUpdate(protocol.ResourceLinkContentBlock("answer", "artifact://answer")),
	}); err != nil {
		return nil, err
	}
	stopReason := remote.stopReason
	if stopReason == "" {
		stopReason = protocol.StopReasonEndTurn
	}
	return &protocol.PromptResponse{StopReason: stopReason}, nil
}

func (*remoteAgent) Cancel(context.Context, *protocol.CancelNotification) error { return nil }

func (remote *remoteAgent) snapshot() (int, []protocol.PromptRequest) {
	remote.mu.Lock()
	defer remote.mu.Unlock()
	return remote.sessions, append([]protocol.PromptRequest(nil), remote.prompts...)
}

var _ io.ReadWriteCloser = (net.Conn)(nil)
