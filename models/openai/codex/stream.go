package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact-ext/models/openai/codexauth"
)

const (
	maxStreamFrameBytes = 4 << 20
	maxStreamBytes      = 32 << 20
	maxOutputBytes      = 16 << 20
	maxEventSummary     = 4 << 10
	initialSSEBuffer    = 64 << 10
)

type streamEvent struct {
	Type     string          `json:"type"`
	Delta    string          `json:"delta"`
	Text     string          `json:"text"`
	ItemID   string          `json:"item_id"`
	CallID   string          `json:"call_id"`
	Item     json.RawMessage `json:"item"`
	Response json.RawMessage `json:"response"`
	Code     string          `json:"code"`
	Message  string          `json:"message"`
}

type completedResponse struct {
	ID      string          `json:"id"`
	Model   string          `json:"model"`
	Usage   *completedUsage `json:"usage"`
	EndTurn *bool           `json:"end_turn"`
}

type completedUsage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	TotalTokens  int64 `json:"total_tokens"`
}

type failedResponse struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	IncompleteDetails struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details"`
}

type outputItem struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	Name      string          `json:"name"`
	Arguments string          `json:"arguments"`
	CallID    string          `json:"call_id"`
	Content   []outputContent `json:"content"`
}

type outputContent struct {
	Type    string `json:"type"`
	Text    string `json:"text"`
	Refusal string `json:"refusal"`
}

type streamResult struct {
	text         strings.Builder
	stateParts   []gopact.MessagePart
	calls        []gopact.ToolCall
	seenCallIDs  map[string]struct{}
	toolDeltaIDs map[string]struct{}
	refusal      strings.Builder
	usage        gopact.Usage
	responseID   string
	model        string
	completed    bool
	sawTextDelta bool
	sawRefusal   bool
	textStarted  bool
	textStateAt  int
	stateBytes   int
}

type streamConsumer struct {
	ctx       context.Context
	tokens    codexauth.Tokens
	sinks     []gopact.ModelEventSink
	yieldText func(string) error
}

func (model *Model) execute(ctx context.Context, payload []byte, config gopact.ModelCallConfig, yieldText func(string) error) (streamResult, error) {
	resp, tokens, err := model.openStream(ctx, payload)
	if err != nil {
		return streamResult{}, err
	}
	defer closeResponse(resp)

	result := streamResult{
		seenCallIDs:  make(map[string]struct{}),
		toolDeltaIDs: make(map[string]struct{}),
		model:        responseModel(resp.Header),
	}
	consumer := streamConsumer{ctx: ctx, tokens: tokens, sinks: config.ModelEventSinks, yieldText: yieldText}
	return consumeStream(newSSEDecoder(resp.Body), result, consumer)
}

func consumeStream(decoder *sseDecoder, result streamResult, consumer streamConsumer) (streamResult, error) {
	for {
		data, err := decoder.Next()
		if err != nil {
			return result, streamReadError(err)
		}
		if data == "[DONE]" {
			return result, streamDoneError(result.completed)
		}
		event, err := decodeStreamEvent(data)
		if err != nil {
			return result, err
		}
		terminal, err := result.handleEvent(event, consumer)
		if err != nil {
			return result, err
		}
		if terminal {
			return result, nil
		}
	}
}

func streamReadError(err error) error {
	if errors.Is(err, io.EOF) {
		return fmt.Errorf("codex: response stream closed before response.completed: %w", io.ErrUnexpectedEOF)
	}
	return fmt.Errorf("codex: read response stream: %w", err)
}

func streamDoneError(completed bool) error {
	if completed {
		return nil
	}
	return fmt.Errorf("codex: response stream ended before response.completed: %w", io.ErrUnexpectedEOF)
}

func decodeStreamEvent(data string) (streamEvent, error) {
	var event streamEvent
	if err := json.Unmarshal([]byte(data), &event); err != nil {
		return streamEvent{}, fmt.Errorf("codex: decode response event: %w", err)
	}
	return event, nil
}

func (result *streamResult) handleEvent(event streamEvent, consumer streamConsumer) (bool, error) {
	switch event.Type {
	case "response.output_text.delta":
		return false, result.handleTextDelta(event.Delta, consumer)
	case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
		return false, handleReasoningDelta(event.Delta, consumer)
	case "response.function_call_arguments.delta", "response.custom_tool_call_input.delta":
		return false, result.handleToolDelta(event, consumer)
	case "response.refusal.delta":
		return false, result.handleRefusalDelta(event.Delta, consumer)
	case "response.output_item.done":
		return false, result.addOutputItem(event.Item, consumer)
	case "response.completed":
		return result.handleCompleted(event.Response, consumer)
	case "response.failed":
		return false, responseFailure("failed", event.Response, consumer.tokens)
	case "response.incomplete":
		return false, responseFailure("incomplete", event.Response, consumer.tokens)
	case "error":
		return false, responseEventError(event, consumer.tokens)
	default:
		return false, nil
	}
}

func (result *streamResult) handleTextDelta(value string, consumer streamConsumer) error {
	if value == "" {
		return nil
	}
	result.sawTextDelta = true
	if err := result.appendText(value); err != nil {
		return err
	}
	if err := emitEvent(consumer.ctx, consumer.sinks, gopact.ModelEvent{
		Type: gopact.ModelEventMessageDelta, Source: providerName, Summary: bounded(value),
	}); err != nil {
		return err
	}
	if consumer.yieldText == nil {
		return nil
	}
	return consumer.yieldText(value)
}

func handleReasoningDelta(value string, consumer streamConsumer) error {
	if value == "" {
		return nil
	}
	return emitEvent(consumer.ctx, consumer.sinks, gopact.ModelEvent{
		Type: gopact.ModelEventReasoningDelta, Source: providerName,
		Bytes: []byte(value), Summary: bounded(value),
	})
}

func (result *streamResult) handleToolDelta(event streamEvent, consumer streamConsumer) error {
	if event.Delta == "" {
		return nil
	}
	result.recordToolDelta(event.ItemID)
	result.recordToolDelta(event.CallID)
	identifier := event.CallID
	if identifier == "" {
		identifier = event.ItemID
	}
	return emitEvent(consumer.ctx, consumer.sinks, gopact.ModelEvent{
		Type: gopact.ModelEventToolCallDelta, Source: providerName,
		Bytes: []byte(event.Delta), Summary: bounded(identifier),
	})
}

func (result *streamResult) handleRefusalDelta(value string, consumer streamConsumer) error {
	if value == "" {
		return nil
	}
	result.sawRefusal = true
	result.refusal.WriteString(value)
	return emitEvent(consumer.ctx, consumer.sinks, gopact.ModelEvent{
		Type: gopact.ModelEventRefusal, Source: providerName, Summary: bounded(value),
	})
}

func (result *streamResult) handleCompleted(encoded json.RawMessage, consumer streamConsumer) (bool, error) {
	if err := result.complete(consumer.ctx, encoded, consumer.sinks); err != nil {
		return false, err
	}
	return true, nil
}

func responseEventError(event streamEvent, tokens codexauth.Tokens) error {
	message := redactTokens(event.Message, tokens)
	if event.Code == "" {
		return fmt.Errorf("codex: response error: %s", message)
	}
	return fmt.Errorf("codex: response error: %s: %s", event.Code, message)
}

func (result *streamResult) addOutputItem(encoded json.RawMessage, consumer streamConsumer) error {
	if len(encoded) == 0 {
		return errors.New("codex: output_item.done has no item")
	}
	if len(encoded) > maxStreamFrameBytes {
		return fmt.Errorf("codex: response item exceeds %d bytes", maxStreamFrameBytes)
	}
	var item outputItem
	if err := json.Unmarshal(encoded, &item); err != nil {
		return fmt.Errorf("codex: decode response item: %w", err)
	}
	switch item.Type {
	case "reasoning":
		return result.addStatePart(encoded)
	case "function_call":
		return result.addFunctionCall(item, encoded, consumer)
	case "message":
		return result.addMessageItem(item.Content, consumer)
	default:
		return fmt.Errorf("codex: unsupported output item type %q", item.Type)
	}
}

func (result *streamResult) addFunctionCall(item outputItem, encoded json.RawMessage, consumer streamConsumer) error {
	if item.CallID == "" || item.Name == "" {
		return errors.New("codex: function call is incomplete")
	}
	if !json.Valid([]byte(item.Arguments)) {
		return fmt.Errorf("codex: function call %q has invalid arguments", item.Name)
	}
	if _, exists := result.seenCallIDs[item.CallID]; exists {
		return fmt.Errorf("codex: duplicate function call id %q", item.CallID)
	}
	result.seenCallIDs[item.CallID] = struct{}{}
	result.calls = append(result.calls, gopact.ToolCall{
		ID: item.CallID, Name: item.Name, Arguments: json.RawMessage(item.Arguments), SourceRef: providerName,
	})
	if err := result.addStatePart(encoded); err != nil {
		return err
	}
	if result.hasToolDelta(item) {
		return nil
	}
	return emitEvent(consumer.ctx, consumer.sinks, gopact.ModelEvent{
		Type: gopact.ModelEventToolCallDelta, Source: providerName,
		Bytes: []byte(item.Arguments), Summary: bounded(item.CallID),
	})
}

func (result *streamResult) recordToolDelta(identifier string) {
	if identifier == "" {
		return
	}
	if result.toolDeltaIDs == nil {
		result.toolDeltaIDs = make(map[string]struct{})
	}
	result.toolDeltaIDs[identifier] = struct{}{}
}

func (result *streamResult) hasToolDelta(item outputItem) bool {
	_, sawItem := result.toolDeltaIDs[item.ID]
	_, sawCall := result.toolDeltaIDs[item.CallID]
	return sawItem || sawCall
}

func (result *streamResult) addMessageItem(contents []outputContent, consumer streamConsumer) error {
	for _, content := range contents {
		if err := result.addMessageContent(content, consumer); err != nil {
			return err
		}
	}
	return nil
}

func (result *streamResult) addMessageContent(content outputContent, consumer streamConsumer) error {
	switch content.Type {
	case "output_text":
		return result.addCompletedText(content.Text, consumer)
	case "refusal":
		return result.addCompletedRefusal(content.Refusal, consumer)
	default:
		return nil
	}
}

func (result *streamResult) addCompletedText(value string, consumer streamConsumer) error {
	if result.sawTextDelta || value == "" {
		return nil
	}
	if err := result.appendText(value); err != nil {
		return err
	}
	if err := emitEvent(consumer.ctx, consumer.sinks, gopact.ModelEvent{
		Type: gopact.ModelEventMessageDelta, Source: providerName, Summary: bounded(value),
	}); err != nil {
		return err
	}
	if consumer.yieldText == nil {
		return nil
	}
	return consumer.yieldText(value)
}

func (result *streamResult) addCompletedRefusal(value string, consumer streamConsumer) error {
	if result.sawRefusal || value == "" {
		return nil
	}
	result.refusal.WriteString(value)
	return emitEvent(consumer.ctx, consumer.sinks, gopact.ModelEvent{
		Type: gopact.ModelEventRefusal, Source: providerName, Summary: bounded(value),
	})
}

func (result *streamResult) addStatePart(encoded json.RawMessage) error {
	canonical, err := canonicalResponseState(encoded)
	if err != nil {
		return err
	}
	result.stateBytes += len(canonical)
	if result.stateBytes > maxOutputBytes {
		return fmt.Errorf("codex: response state exceeds %d bytes", maxOutputBytes)
	}
	result.stateParts = append(result.stateParts, gopact.MessagePart{
		Type: MessagePartTypeResponseItem,
		Text: string(canonical),
	})
	return nil
}

func (result *streamResult) appendText(value string) error {
	if result.text.Len()+len(value) > maxOutputBytes {
		return fmt.Errorf("codex: response text exceeds %d bytes", maxOutputBytes)
	}
	if !result.textStarted {
		result.textStarted = true
		result.textStateAt = len(result.stateParts)
	}
	result.text.WriteString(value)
	return nil
}

func (result *streamResult) complete(ctx context.Context, encoded json.RawMessage, sinks []gopact.ModelEventSink) error {
	if len(encoded) == 0 {
		return errors.New("codex: response.completed has no response")
	}
	var response completedResponse
	if err := json.Unmarshal(encoded, &response); err != nil {
		return fmt.Errorf("codex: decode completed response: %w", err)
	}
	if response.ID == "" {
		return errors.New("codex: completed response has no id")
	}
	result.responseID = response.ID
	if response.Model != "" {
		result.model = response.Model
	}
	if response.Usage != nil {
		usage, err := response.Usage.toGopact()
		if err != nil {
			return err
		}
		result.usage = usage
		payload, err := json.Marshal(struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
			TotalTokens  int `json:"total_tokens"`
		}{
			InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens, TotalTokens: usage.TotalTokens,
		})
		if err != nil {
			return fmt.Errorf("codex: encode usage event: %w", err)
		}
		if err := emitEvent(ctx, sinks, gopact.ModelEvent{
			Type: gopact.ModelEventUsage, Source: providerName, Payload: payload,
		}); err != nil {
			return err
		}
	}
	result.completed = true
	return emitEvent(ctx, sinks, gopact.ModelEvent{
		Type: gopact.ModelEventFinish, Source: providerName, Summary: result.finishReason(),
	})
}

func (usage completedUsage) toGopact() (gopact.Usage, error) {
	input, err := tokenCount(usage.InputTokens)
	if err != nil {
		return gopact.Usage{}, fmt.Errorf("codex: input tokens: %w", err)
	}
	output, err := tokenCount(usage.OutputTokens)
	if err != nil {
		return gopact.Usage{}, fmt.Errorf("codex: output tokens: %w", err)
	}
	total, err := tokenCount(usage.TotalTokens)
	if err != nil {
		return gopact.Usage{}, fmt.Errorf("codex: total tokens: %w", err)
	}
	return gopact.Usage{InputTokens: input, OutputTokens: output, TotalTokens: total}, nil
}

func tokenCount(value int64) (int, error) {
	converted := int(value)
	if value < 0 || int64(converted) != value {
		return 0, errors.New("value is out of range")
	}
	return converted, nil
}

func (result *streamResult) response() (gopact.ModelResponse, error) {
	if !result.completed {
		return gopact.ModelResponse{}, errors.New("codex: response is incomplete")
	}
	message := gopact.Message{Role: gopact.MessageRoleAssistant, Parts: result.messageParts()}
	intent, message, err := result.responseIntent(message)
	if err != nil {
		return gopact.ModelResponse{}, err
	}
	return gopact.ModelResponse{
		Message:      message,
		Intent:       intent,
		Usage:        result.usage,
		FinishReason: result.finishReason(),
		ProviderMetadata: map[string]any{
			"id": result.responseID, "model": result.model, "provider": providerName,
		},
	}, nil
}

func (result *streamResult) messageParts() []gopact.MessagePart {
	capacity := len(result.stateParts)
	if result.text.Len() > 0 {
		capacity++
	}
	parts := make([]gopact.MessagePart, 0, capacity)
	if result.text.Len() == 0 {
		return append(parts, result.stateParts...)
	}
	split := min(result.textStateAt, len(result.stateParts))
	parts = append(parts, result.stateParts[:split]...)
	parts = append(parts, gopact.MessagePart{Type: gopact.MessagePartTypeText, Text: result.text.String()})
	return append(parts, result.stateParts[split:]...)
}

func (result *streamResult) responseIntent(message gopact.Message) (gopact.ModelIntent, gopact.Message, error) {
	switch {
	case len(result.calls) > 0 && result.refusal.Len() > 0:
		return nil, message, errors.New("codex: response contains both tool calls and a refusal")
	case len(result.calls) > 0:
		message.ToolCalls = cloneToolCalls(result.calls)
		return gopact.ToolCallIntent{}, message, nil
	case result.refusal.Len() > 0:
		return result.refusalIntent(message)
	default:
		return gopact.FinalIntent{}, message, nil
	}
}

func (result *streamResult) refusalIntent(message gopact.Message) (gopact.ModelIntent, gopact.Message, error) {
	refusalMessage := gopact.Message{
		Role:  gopact.MessageRoleAssistant,
		Parts: []gopact.MessagePart{{Type: gopact.MessagePartTypeText, Text: result.refusal.String()}},
	}
	intent := gopact.RefusalIntent{Refusal: gopact.Refusal{
		Reason: "provider_refusal", Message: refusalMessage, Ref: result.responseID,
	}}
	if len(message.Parts) == 0 {
		message = refusalMessage
	}
	return intent, message, nil
}

func (result *streamResult) finishReason() string {
	if len(result.calls) > 0 {
		return "tool_calls"
	}
	if result.refusal.Len() > 0 {
		return "refusal"
	}
	return "stop"
}

func cloneToolCalls(calls []gopact.ToolCall) []gopact.ToolCall {
	cloned := make([]gopact.ToolCall, len(calls))
	for index, call := range calls {
		call.Arguments = append(json.RawMessage(nil), call.Arguments...)
		cloned[index] = call
	}
	return cloned
}

func responseFailure(kind string, encoded json.RawMessage, tokens codexauth.Tokens) error {
	var response failedResponse
	if len(encoded) > 0 {
		if err := json.Unmarshal(encoded, &response); err != nil {
			return fmt.Errorf("codex: decode %s response: %w", kind, err)
		}
	}
	code := response.Error.Code
	message := response.Error.Message
	if kind == "incomplete" && message == "" {
		message = response.IncompleteDetails.Reason
	}
	message = redactTokens(message, tokens)
	if code != "" && message != "" {
		return fmt.Errorf("codex: response %s: %s: %s", kind, code, message)
	}
	if code != "" {
		return fmt.Errorf("codex: response %s: %s", kind, code)
	}
	if message != "" {
		return fmt.Errorf("codex: response %s: %s", kind, message)
	}
	return fmt.Errorf("codex: response %s", kind)
}

func emitEvent(ctx context.Context, sinks []gopact.ModelEventSink, event gopact.ModelEvent) error {
	for _, sink := range sinks {
		if sink == nil {
			continue
		}
		if err := sink.EmitModelEvent(ctx, event); err != nil {
			return err
		}
	}
	return nil
}

func bounded(value string) string {
	if len(value) <= maxEventSummary {
		return value
	}
	return value[:maxEventSummary]
}

func responseModel(headers http.Header) string {
	if model := headers.Get("OpenAI-Model"); model != "" {
		return model
	}
	return headers.Get("X-OpenAI-Model")
}

type sseDecoder struct {
	scanner *bufio.Scanner
	data    strings.Builder
	total   int
	eof     bool
}

func newSSEDecoder(reader io.Reader) *sseDecoder {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, initialSSEBuffer), maxStreamFrameBytes)
	return &sseDecoder{scanner: scanner}
}

func (decoder *sseDecoder) Next() (string, error) {
	if decoder.eof {
		return "", io.EOF
	}
	for decoder.scanner.Scan() {
		line := strings.TrimSuffix(decoder.scanner.Text(), "\r")
		value, ready, err := decoder.acceptLine(line)
		if err != nil {
			return "", err
		}
		if ready {
			return value, nil
		}
	}
	return decoder.finish()
}

func (decoder *sseDecoder) acceptLine(line string) (string, bool, error) {
	decoder.total += len(line) + 1
	if decoder.total > maxStreamBytes {
		return "", false, fmt.Errorf("stream exceeds %d bytes", maxStreamBytes)
	}
	if line == "" {
		return decoder.endEvent()
	}
	if strings.HasPrefix(line, ":") {
		return "", false, nil
	}
	field, value, found := strings.Cut(line, ":")
	if !found || field != "data" {
		return "", false, nil
	}
	value = strings.TrimPrefix(value, " ")
	if decoder.data.Len() > 0 {
		decoder.data.WriteByte('\n')
	}
	if decoder.data.Len()+len(value) > maxStreamFrameBytes {
		return "", false, fmt.Errorf("event exceeds %d bytes", maxStreamFrameBytes)
	}
	decoder.data.WriteString(value)
	return "", false, nil
}

func (decoder *sseDecoder) endEvent() (string, bool, error) {
	if decoder.data.Len() == 0 {
		return "", false, nil
	}
	return decoder.takeData(), true, nil
}

func (decoder *sseDecoder) finish() (string, error) {
	if err := decoder.scanner.Err(); err != nil {
		return "", err
	}
	decoder.eof = true
	if decoder.data.Len() > 0 {
		return decoder.takeData(), nil
	}
	return "", io.EOF
}

func (decoder *sseDecoder) takeData() string {
	value := decoder.data.String()
	decoder.data.Reset()
	return value
}
