package acp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/gopact-ai/gopact"
	coreacp "github.com/gopact-ai/gopact/acp"
	"github.com/gopact-ai/gopact/agent"
)

func TestAgentProcessE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestACPAgentProcess$")
	command.WaitDelay = time.Second
	command.Env = append(os.Environ(), "GOPACT_ACP_HELPER=1")
	input, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	defer func() {
		_ = output.Close()
		if !waited {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	}()

	target, err := New(
		ctx,
		agent.Identity{Name: "process", Description: "process ACP agent", Version: "v1"},
		input,
		output,
		WithCwd(t.TempDir()),
	)
	if err != nil {
		t.Fatal(err)
	}
	response, err := target.Invoke(ctx, agent.Request{
		Messages:  []gopact.Message{gopact.UserMessage("hello process")},
		Artifacts: []gopact.ArtifactRef{{URI: "file:///tmp/process.md", Kind: "spec"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.Message.Role != gopact.MessageRoleAssistant || len(response.Message.Parts) != 2 ||
		response.Message.Parts[0].Type != gopact.MessagePartTypeText || response.Message.Parts[0].Text != "hello process back" ||
		response.Message.Parts[1].Type != gopact.MessagePartTypeArtifact || response.Message.Parts[1].Ref == nil ||
		response.Message.Parts[1].Ref.URI != "artifact://process" || response.Message.Parts[1].Ref.Kind != "process" ||
		len(response.Artifacts) != 1 || response.Artifacts[0].URI != "artifact://process" || response.Artifacts[0].Kind != "process" {
		t.Fatalf("Invoke() response = %+v", response)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	err = command.Wait()
	waited = true
	if err != nil {
		t.Fatal(err)
	}
	if err := target.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestACPAgentProcess(t *testing.T) {
	if os.Getenv("GOPACT_ACP_HELPER") != "1" {
		t.Skip("helper process")
	}
	conn, err := coreacp.NewAgent(os.Stdin, os.Stdout, processTarget{})
	if err != nil {
		t.Fatal(err)
	}
	<-conn.Done()
	if err := conn.Err(); err != nil && !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
}

type processTarget struct{}

func (processTarget) Identity() agent.Identity {
	return agent.Identity{Name: "process", Description: "process ACP agent", Version: "v1"}
}

func (processTarget) Invoke(_ context.Context, request agent.Request, options ...gopact.RunOption) (agent.Response, error) {
	config := gopact.ResolveRunOptions(options...)
	if config.SessionID == "" || len(request.Messages) != 1 || len(request.Messages[0].Parts) != 2 ||
		request.Messages[0].Parts[0].Text != "hello process" || request.Messages[0].Parts[1].Ref == nil ||
		request.Messages[0].Parts[1].Ref.URI != "file:///tmp/process.md" || request.Messages[0].Parts[1].Ref.Kind != "spec" ||
		len(request.Artifacts) != 1 || request.Artifacts[0].URI != "file:///tmp/process.md" || request.Artifacts[0].Kind != "spec" {
		return agent.Response{}, fmt.Errorf("unexpected request: %+v", request)
	}
	return agent.Response{
		Message:   gopact.Message{Role: gopact.MessageRoleAssistant, Parts: []gopact.MessagePart{{Type: gopact.MessagePartTypeText, Text: "hello process back"}}},
		Artifacts: []gopact.ArtifactRef{{URI: "artifact://process", Kind: "process"}},
	}, nil
}
