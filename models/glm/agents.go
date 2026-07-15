package glm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"net/http"
	"strings"
)

const maxAgentStreamEventBytes = 1 << 20

// AgentRequest invokes a public Z.AI specialized agent such as general
// translation, video templates, or slide/poster generation.
type AgentRequest struct {
	AgentID            string              `json:"agent_id"`
	Stream             bool                `json:"stream,omitempty"`
	ConversationID     string              `json:"conversation_id,omitempty"`
	RequestID          string              `json:"request_id,omitempty"`
	UserID             string              `json:"user_id,omitempty"`
	Messages           any                 `json:"messages"`
	CustomVariables    any                 `json:"custom_variables,omitempty"`
	SensitiveWordCheck *SensitiveWordCheck `json:"sensitive_word_check,omitempty"`
	AcceptLanguage     string              `json:"-"`
}

// AgentMessage is one specialized-agent input message.
type AgentMessage struct {
	Role    string         `json:"role"`
	Content []AgentContent `json:"content"`
}

// AgentContent is text or image input for a specialized agent.
type AgentContent struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
}

// AgentResponse is the common envelope returned by Z.AI specialized agents.
// Choices remain raw because translation, video, and slide agents use different
// message shapes.
type AgentResponse struct {
	ID             string            `json:"id"`
	AgentID        string            `json:"agent_id"`
	ConversationID string            `json:"conversation_id"`
	AsyncID        string            `json:"async_id"`
	Status         string            `json:"status"`
	Choices        []json.RawMessage `json:"choices"`
	Usage          AgentUsage        `json:"usage"`
	Error          *AgentError       `json:"error"`
}

// AgentUsage reports specialized-agent token and call usage.
type AgentUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	TotalCalls       int `json:"total_calls"`
}

// AgentError describes a specialized-agent failure.
type AgentError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// AgentResultRequest queries an asynchronous specialized-agent task.
type AgentResultRequest struct {
	AgentID         string `json:"agent_id"`
	AsyncID         string `json:"async_id"`
	ConversationID  string `json:"conversation_id,omitempty"`
	CustomVariables any    `json:"custom_variables,omitempty"`
	AcceptLanguage  string `json:"-"`
}

// AgentConversationRequest queries generated slide/poster conversation assets.
type AgentConversationRequest struct {
	AgentID         string                     `json:"agent_id"`
	ConversationID  string                     `json:"conversation_id"`
	CustomVariables AgentConversationVariables `json:"custom_variables,omitempty"`
	AcceptLanguage  string                     `json:"-"`
}

// AgentConversationVariables controls slide export and page geometry.
type AgentConversationVariables struct {
	IncludePDF bool             `json:"include_pdf,omitempty"`
	Pages      []AgentSlidePage `json:"pages,omitempty"`
}

// AgentSlidePage describes one slide's output geometry in points.
type AgentSlidePage struct {
	Position float64 `json:"position"`
	Width    float64 `json:"width"`
	Height   float64 `json:"height"`
}

// AgentEvent is one specialized-agent SSE event.
type AgentEvent struct {
	Response AgentResponse
	Raw      json.RawMessage
}

// RunAgent invokes a non-streaming Z.AI specialized agent.
func (model *Model) RunAgent(ctx context.Context, request AgentRequest) (AgentResponse, error) {
	if err := validateAgentRequest(request); err != nil {
		return AgentResponse{}, err
	}
	if request.Stream {
		return AgentResponse{}, errors.New("glm: use StreamAgent for streaming agent output")
	}
	var response AgentResponse
	err := model.agentJSON(ctx, "/v1/agents", request.AcceptLanguage, request, &response)
	return response, err
}

// StreamAgent invokes a specialized agent and streams its raw events.
func (model *Model) StreamAgent(ctx context.Context, request AgentRequest) iter.Seq2[AgentEvent, error] {
	return func(yield func(AgentEvent, error) bool) {
		if err := validateAgentRequest(request); err != nil {
			yield(AgentEvent{}, err)
			return
		}
		request.Stream = true
		encoded, err := json.Marshal(request)
		if err != nil {
			yield(AgentEvent{}, fmt.Errorf("glm: encode agent request: %w", err))
			return
		}
		model.streamAgent(ctx, request.AcceptLanguage, encoded, yield)
	}
}

// AgentResult queries the result of an asynchronous specialized-agent task.
func (model *Model) AgentResult(ctx context.Context, request AgentResultRequest) (AgentResponse, error) {
	if strings.TrimSpace(request.AgentID) == "" || strings.TrimSpace(request.AsyncID) == "" {
		return AgentResponse{}, errors.New("glm: agent id and async id are required")
	}
	var response AgentResponse
	err := model.agentJSON(ctx, "/v1/agents/async-result", request.AcceptLanguage, request, &response)
	return response, err
}

// AgentConversation queries slide/poster assets from an agent conversation.
func (model *Model) AgentConversation(
	ctx context.Context,
	request AgentConversationRequest,
) (AgentResponse, error) {
	if strings.TrimSpace(request.AgentID) == "" || strings.TrimSpace(request.ConversationID) == "" {
		return AgentResponse{}, errors.New("glm: agent id and conversation id are required")
	}
	var response AgentResponse
	err := model.agentJSON(ctx, "/v1/agents/conversation", request.AcceptLanguage, request, &response)
	return response, err
}

func validateAgentRequest(request AgentRequest) error {
	if strings.TrimSpace(request.AgentID) == "" {
		return errors.New("glm: agent id is required")
	}
	messages, ok := request.Messages.([]AgentMessage)
	if !ok {
		if request.Messages == nil {
			return errors.New("glm: agent messages are required")
		}
		if text, ok := request.Messages.(string); ok && strings.TrimSpace(text) == "" {
			return errors.New("glm: agent messages are required")
		}
		return nil
	}
	if len(messages) == 0 {
		return errors.New("glm: agent messages are required")
	}
	for _, message := range messages {
		if strings.TrimSpace(message.Role) == "" || len(message.Content) == 0 {
			return errors.New("glm: agent message role and content are required")
		}
		for _, content := range message.Content {
			if content.Type == "text" && content.Text == "" ||
				content.Type == "image_url" && content.ImageURL == "" ||
				content.Type != "text" && content.Type != "image_url" {
				return errors.New("glm: agent content must be non-empty text or image_url")
			}
		}
	}
	return nil
}

func (model *Model) agentJSON(ctx context.Context, path, language string, input, output any) error {
	encoded, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("glm: encode agent request: %w", err)
	}
	response, cancel, err := model.sendAgentRequest(ctx, path, language, encoded, "application/json")
	if err != nil {
		return err
	}
	defer cancel()
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxRuntimeResponseBytes+1))
	if err != nil {
		return fmt.Errorf("glm: read agent response: %w", err)
	}
	if len(data) > maxRuntimeResponseBytes {
		return errors.New("glm: agent response exceeds size limit")
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("glm: status %d: %s", response.StatusCode, model.redactRuntimeError(data))
	}
	if err := json.Unmarshal(data, output); err != nil {
		return fmt.Errorf("glm: decode agent response: %w", err)
	}
	return nil
}

func (model *Model) streamAgent(
	ctx context.Context,
	language string,
	body []byte,
	yield func(AgentEvent, error) bool,
) {
	response, cancel, err := model.sendAgentRequest(ctx, "/v1/agents", language, body, "text/event-stream")
	if err != nil {
		yield(AgentEvent{}, err)
		return
	}
	defer cancel()
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		data, _ := io.ReadAll(io.LimitReader(response.Body, 4<<10))
		yield(AgentEvent{}, fmt.Errorf("glm: status %d: %s", response.StatusCode, model.redactRuntimeError(data)))
		return
	}
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 64<<10), maxAgentStreamEventBytes)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			if data == "[DONE]" {
				return
			}
			continue
		}
		raw := json.RawMessage(append([]byte(nil), data...))
		var event AgentEvent
		if err := json.Unmarshal(raw, &event.Response); err != nil {
			yield(AgentEvent{}, fmt.Errorf("glm: decode agent stream: %w", err))
			return
		}
		event.Raw = raw
		if !yield(event, nil) {
			return
		}
	}
	if err := scanner.Err(); err != nil {
		yield(AgentEvent{}, fmt.Errorf("glm: read agent stream: %w", err))
	}
}

func (model *Model) sendAgentRequest(
	ctx context.Context,
	path, language string,
	body []byte,
	accept string,
) (*http.Response, context.CancelFunc, error) {
	if model == nil {
		return nil, nil, errors.New("glm: model is nil")
	}
	if ctx == nil {
		ctx = context.TODO()
	}
	callCtx, cancel := context.WithTimeout(ctx, model.timeout)
	request, err := http.NewRequestWithContext(
		callCtx, http.MethodPost, model.agentBaseURL()+path, bytes.NewReader(body),
	)
	if err != nil {
		cancel()
		return nil, nil, fmt.Errorf("glm: create agent request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+model.apiKey)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", accept)
	if language != "" {
		request.Header.Set("Accept-Language", language)
	}
	client := *model.httpClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(request)
	if err != nil {
		cancel()
		return nil, nil, fmt.Errorf("glm: agent request: %w", err)
	}
	return response, cancel, nil
}

func (model *Model) agentBaseURL() string {
	return strings.TrimSuffix(model.apiBaseURL, "/paas/v4")
}
