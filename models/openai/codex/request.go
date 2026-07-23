package codex

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/gopact-ai/gopact"
)

const messageRoleDeveloper = "developer"

type responsesRequest struct {
	Model             string              `json:"model"`
	Instructions      string              `json:"instructions,omitempty"`
	Input             []responseInputItem `json:"input"`
	Tools             []responseTool      `json:"tools,omitempty"`
	ToolChoice        any                 `json:"tool_choice"`
	ParallelToolCalls bool                `json:"parallel_tool_calls"`
	Reasoning         *reasoningControls  `json:"reasoning,omitempty"`
	Store             bool                `json:"store"`
	Stream            bool                `json:"stream"`
	Include           []string            `json:"include"`
	MaxOutputTokens   int                 `json:"max_output_tokens,omitempty"`
	Text              *textControls       `json:"text,omitempty"`
	Metadata          map[string]string   `json:"metadata,omitempty"`
}

type responseInputItem struct {
	Type      string            `json:"type"`
	ID        string            `json:"id,omitempty"`
	Role      string            `json:"role,omitempty"`
	Content   []responseContent `json:"content,omitempty"`
	Name      string            `json:"name,omitempty"`
	Arguments string            `json:"arguments,omitempty"`
	CallID    string            `json:"call_id,omitempty"`
	Output    string            `json:"output,omitempty"`

	raw json.RawMessage
}

func (item responseInputItem) MarshalJSON() ([]byte, error) {
	if len(item.raw) > 0 {
		return append([]byte(nil), item.raw...), nil
	}
	type wire responseInputItem
	if item.Type == "function_call_output" {
		return json.Marshal(struct {
			wire
			Output string `json:"output"`
		}{wire: wire(item), Output: item.Output})
	}
	return json.Marshal(wire(item))
}

func (item *responseInputItem) UnmarshalJSON(encoded []byte) error {
	type wire responseInputItem
	var decoded wire
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return err
	}
	*item = responseInputItem(decoded)
	item.raw = append(json.RawMessage(nil), encoded...)
	return nil
}

type responseContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type responseTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
	Strict      bool            `json:"strict"`
}

type reasoningControls struct {
	Effort  string `json:"effort,omitempty"`
	Summary string `json:"summary,omitempty"`
}

type textControls struct {
	Format *textFormat `json:"format,omitempty"`
}

type textFormat struct {
	Type   string          `json:"type"`
	Name   string          `json:"name"`
	Strict bool            `json:"strict"`
	Schema json.RawMessage `json:"schema"`
}

func newResponsesRequest(request gopact.ModelRequest) (responsesRequest, error) {
	if err := validateModelRequest(request, true); err != nil {
		return responsesRequest{}, err
	}
	instructions, input, err := encodeMessages(request.Messages)
	if err != nil {
		return responsesRequest{}, err
	}
	if len(input) == 0 {
		return responsesRequest{}, errors.New("codex: request has no model input")
	}
	tools := make([]responseTool, 0, len(request.Tools))
	for _, spec := range request.Tools {
		schema := append(json.RawMessage(nil), spec.Schema...)
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object"}`)
		}
		tools = append(tools, responseTool{
			Type:        "function",
			Name:        spec.Name,
			Description: spec.Description,
			Parameters:  schema,
		})
	}
	body := responsesRequest{
		Model:             request.Model,
		Instructions:      instructions,
		Input:             input,
		Tools:             tools,
		ToolChoice:        encodeToolChoice(request.ToolChoice),
		ParallelToolCalls: len(tools) > 0,
		Store:             false,
		Stream:            true,
		Include:           []string{"reasoning.encrypted_content"},
		MaxOutputTokens:   request.MaxOutputTokens,
		Metadata:          cloneStringMap(request.Metadata),
	}
	if request.Reasoning.Effort != "" {
		body.Reasoning = &reasoningControls{Effort: request.Reasoning.Effort, Summary: "auto"}
	}
	if len(request.ResponseSchema.Value) > 0 {
		body.Text = &textControls{Format: &textFormat{
			Type:   "json_schema",
			Name:   "response",
			Strict: true,
			Schema: append(json.RawMessage(nil), request.ResponseSchema.Value...),
		}}
	}
	return body, nil
}

func encodeMessages(messages []gopact.Message) (string, []responseInputItem, error) {
	encoder := messageEncoder{
		input:     make([]responseInputItem, 0, len(messages)),
		pending:   make(map[string]struct{}),
		completed: make(map[string]struct{}),
		seenIDs:   make(map[string]struct{}),
	}
	for messageIndex, message := range messages {
		if err := encoder.addMessage(messageIndex, message); err != nil {
			return "", nil, err
		}
	}
	if len(encoder.pending) > 0 {
		return "", nil, fmt.Errorf("codex: %d function call(s) have no tool output", len(encoder.pending))
	}
	return strings.Join(encoder.instructions, "\n\n"), encoder.input, nil
}

type messageEncoder struct {
	instructions []string
	input        []responseInputItem
	pending      map[string]struct{}
	completed    map[string]struct{}
	seenIDs      map[string]struct{}
}

type encodedPart struct {
	content *responseContent
	state   *responseInputItem
	call    *gopact.ToolCall
}

type messageRun struct {
	encoder *messageEncoder
	role    string
	content []responseContent
	calls   []gopact.ToolCall
}

func (encoder *messageEncoder) addMessage(index int, message gopact.Message) error {
	if err := validateMessageRole(message.Role); err != nil {
		return err
	}
	if len(message.ToolCalls) > 0 && message.Role != gopact.MessageRoleAssistant {
		return fmt.Errorf("codex: message %d tool calls are only valid on assistant messages", index)
	}
	if message.ToolCallID != "" && message.Role != gopact.MessageRoleTool {
		return fmt.Errorf("codex: message %d tool call id is only valid on tool messages", index)
	}
	if message.Role != gopact.MessageRoleTool && len(encoder.pending) > 0 {
		return fmt.Errorf("codex: %d function call(s) before message %d have no tool output", len(encoder.pending), index)
	}
	switch message.Role {
	case gopact.MessageRoleSystem:
		return encoder.addInstruction(index, message)
	case gopact.MessageRoleTool:
		return encoder.addToolOutput(index, message)
	default:
		return encoder.addModelMessage(index, message)
	}
}

func validateMessageRole(role string) error {
	switch role {
	case gopact.MessageRoleSystem, messageRoleDeveloper, gopact.MessageRoleUser,
		gopact.MessageRoleAssistant, gopact.MessageRoleTool:
		return nil
	default:
		return fmt.Errorf("codex: unsupported message role %q", role)
	}
}

func (encoder *messageEncoder) addInstruction(index int, message gopact.Message) error {
	value, err := plainMessageText(message)
	if err != nil {
		return fmt.Errorf("codex: system message %d: %w", index, err)
	}
	if value != "" {
		encoder.instructions = append(encoder.instructions, value)
	}
	return nil
}

func (encoder *messageEncoder) addToolOutput(index int, message gopact.Message) error {
	if message.ToolCallID == "" {
		return fmt.Errorf("codex: tool message %d requires a tool call id", index)
	}
	if _, duplicate := encoder.completed[message.ToolCallID]; duplicate {
		return fmt.Errorf("codex: tool message %d has duplicate tool output for %q", index, message.ToolCallID)
	}
	if _, exists := encoder.pending[message.ToolCallID]; !exists {
		return fmt.Errorf("codex: tool message %d references unknown function call %q", index, message.ToolCallID)
	}
	value, err := plainMessageText(message)
	if err != nil {
		return fmt.Errorf("codex: tool message %d: %w", index, err)
	}
	encoder.input = append(encoder.input, responseInputItem{
		Type:   "function_call_output",
		CallID: message.ToolCallID,
		Output: value,
	})
	delete(encoder.pending, message.ToolCallID)
	encoder.completed[message.ToolCallID] = struct{}{}
	return nil
}

func (encoder *messageEncoder) addModelMessage(index int, message gopact.Message) error {
	contentType := "input_text"
	if message.Role == gopact.MessageRoleAssistant {
		contentType = "output_text"
	}
	run := messageRun{encoder: encoder, role: message.Role}
	for partIndex, part := range message.Parts {
		encoded, err := encoder.encodePart(index, partIndex, message.Role, contentType, part)
		if err != nil {
			return err
		}
		run.add(encoded)
	}
	run.flush()
	return encoder.addToolCalls(index, message.ToolCalls, run.calls)
}

func (run *messageRun) add(part encodedPart) {
	if part.content != nil {
		run.content = append(run.content, *part.content)
		return
	}
	run.flush()
	if part.state != nil {
		run.encoder.input = append(run.encoder.input, *part.state)
	}
	if part.call != nil {
		run.calls = append(run.calls, *part.call)
	}
}

func (run *messageRun) flush() {
	if len(run.content) == 0 {
		return
	}
	run.encoder.input = append(run.encoder.input, responseInputItem{
		Type: "message", Role: run.role, Content: run.content,
	})
	run.content = nil
}

func (encoder *messageEncoder) encodePart(msgIndex, partIndex int, role, contentType string, part gopact.MessagePart) (encodedPart, error) {
	if part.Ref != nil {
		return encodedPart{}, fmt.Errorf("codex: message %d part %d has an unsupported reference", msgIndex, partIndex)
	}
	switch part.Type {
	case gopact.MessagePartTypeText:
		return textPart(contentType, part.Text), nil
	case MessagePartTypeResponseItem:
		return encoder.statePart(msgIndex, partIndex, role, part.Text)
	default:
		return encodedPart{}, fmt.Errorf("codex: unsupported message part %q", part.Type)
	}
}

func textPart(contentType, value string) encodedPart {
	content := responseContent{Type: contentType, Text: value}
	return encodedPart{content: &content}
}

func (encoder *messageEncoder) statePart(msgIndex, partIndex int, role, value string) (encodedPart, error) {
	if role != gopact.MessageRoleAssistant {
		return encodedPart{}, errors.New("codex: response state is only valid on assistant messages")
	}
	item, state, err := decodeResponseState(value)
	if err != nil {
		return encodedPart{}, fmt.Errorf("codex: message %d part %d: %w", msgIndex, partIndex, err)
	}
	encoded := encodedPart{state: &item}
	if state.Type == "function_call" {
		call := gopact.ToolCall{
			ID: state.CallID, Name: state.Name, Arguments: json.RawMessage(state.Arguments),
		}
		encoded.call = &call
	}
	return encoded, nil
}

func (encoder *messageEncoder) addToolCalls(index int, calls, opaque []gopact.ToolCall) error {
	opaqueByID, err := indexOpaqueCalls(opaque)
	if err != nil {
		return err
	}
	for _, call := range calls {
		if err := encoder.addToolCall(index, call, opaqueByID); err != nil {
			return err
		}
		delete(opaqueByID, call.ID)
	}
	for id := range opaqueByID {
		return fmt.Errorf("codex: opaque function call %q has no canonical tool call", id)
	}
	return nil
}

func indexOpaqueCalls(calls []gopact.ToolCall) (map[string]gopact.ToolCall, error) {
	indexed := make(map[string]gopact.ToolCall, len(calls))
	for _, call := range calls {
		if _, duplicate := indexed[call.ID]; duplicate {
			return nil, fmt.Errorf("codex: duplicate opaque function call id %q", call.ID)
		}
		indexed[call.ID] = call
	}
	return indexed, nil
}

func (encoder *messageEncoder) addToolCall(index int, call gopact.ToolCall, opaque map[string]gopact.ToolCall) error {
	arguments, err := encoder.validateToolCall(index, call)
	if err != nil {
		return err
	}
	if state, exists := opaque[call.ID]; exists {
		if state.Name != call.Name || !equalJSON(state.Arguments, []byte(arguments)) {
			return fmt.Errorf("codex: opaque function call %q conflicts with canonical tool call", call.ID)
		}
	} else {
		encoder.input = append(encoder.input, responseInputItem{
			Type: "function_call", Name: call.Name, Arguments: arguments, CallID: call.ID,
		})
	}
	encoder.seenIDs[call.ID] = struct{}{}
	encoder.pending[call.ID] = struct{}{}
	return nil
}

func (encoder *messageEncoder) validateToolCall(index int, call gopact.ToolCall) (string, error) {
	if call.ID == "" || call.Name == "" {
		return "", fmt.Errorf("codex: assistant message %d tool call id and name are required", index)
	}
	if _, duplicate := encoder.seenIDs[call.ID]; duplicate {
		return "", fmt.Errorf("codex: duplicate function call id %q", call.ID)
	}
	if call.ArgumentsRef != "" {
		return "", fmt.Errorf("codex: tool call %q arguments ref is not supported", call.ID)
	}
	if len(call.Arguments) == 0 {
		return "{}", nil
	}
	if !json.Valid(call.Arguments) {
		return "", fmt.Errorf("codex: tool call %q has invalid arguments", call.ID)
	}
	return string(call.Arguments), nil
}

func equalJSON(left, right []byte) bool {
	var compactLeft, compactRight bytes.Buffer
	if json.Compact(&compactLeft, left) != nil || json.Compact(&compactRight, right) != nil {
		return false
	}
	return bytes.Equal(compactLeft.Bytes(), compactRight.Bytes())
}

type responseState struct {
	Type      string `json:"type"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

func decodeResponseState(value string) (responseInputItem, responseState, error) {
	encoded := []byte(value)
	if len(encoded) == 0 || !json.Valid(encoded) {
		return responseInputItem{}, responseState{}, errors.New("codex: response state is invalid JSON")
	}
	if len(encoded) > maxStreamFrameBytes {
		return responseInputItem{}, responseState{}, fmt.Errorf("codex: response state exceeds %d bytes", maxStreamFrameBytes)
	}
	var state responseState
	if err := json.Unmarshal(encoded, &state); err != nil {
		return responseInputItem{}, responseState{}, fmt.Errorf("codex: decode response state: %w", err)
	}
	switch state.Type {
	case "reasoning":
	case "function_call":
		if state.CallID == "" || state.Name == "" {
			return responseInputItem{}, responseState{}, errors.New("codex: function call state is incomplete")
		}
		if !json.Valid([]byte(state.Arguments)) {
			return responseInputItem{}, responseState{}, fmt.Errorf("codex: function call %q has invalid arguments", state.Name)
		}
	default:
		return responseInputItem{}, responseState{}, fmt.Errorf("codex: unsupported response state type %q", state.Type)
	}
	canonical, err := canonicalResponseState(encoded)
	if err != nil {
		return responseInputItem{}, responseState{}, err
	}
	return responseInputItem{
		Type: state.Type, Name: state.Name, Arguments: state.Arguments, CallID: state.CallID, raw: canonical,
	}, state, nil
}

func canonicalResponseState(encoded []byte) (json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &object); err != nil {
		return nil, fmt.Errorf("codex: decode response state object: %w", err)
	}
	if object == nil {
		return nil, errors.New("codex: response state must be an object")
	}
	// Response item IDs and terminal status are output-only unless the caller
	// opts into a stored Responses conversation. This provider is stateless.
	delete(object, "id")
	delete(object, "status")
	canonical, err := json.Marshal(object)
	if err != nil {
		return nil, fmt.Errorf("codex: encode response state object: %w", err)
	}
	return canonical, nil
}

func plainMessageText(message gopact.Message) (string, error) {
	var text strings.Builder
	for _, part := range message.Parts {
		if part.Type != gopact.MessagePartTypeText || part.Ref != nil {
			return "", fmt.Errorf("unsupported message part %q", part.Type)
		}
		text.WriteString(part.Text)
	}
	return text.String(), nil
}

func validateModelRequest(request gopact.ModelRequest, required bool) error {
	if required && request.Model == "" {
		return errors.New("codex: request model is required")
	}
	if required && len(request.Messages) == 0 {
		return errors.New("codex: request has no messages")
	}
	if request.MaxOutputTokens < 0 {
		return errors.New("codex: max output tokens must not be negative")
	}
	if request.Temperature != nil {
		return errors.New("codex: temperature is not supported")
	}
	if request.TopP != nil {
		return errors.New("codex: top p is not supported")
	}
	if len(request.Stop) > 0 {
		return errors.New("codex: stop sequences are not supported")
	}
	if request.Seed != nil {
		return errors.New("codex: seed is not supported")
	}
	if len(request.Modalities) > 0 {
		return errors.New("codex: modalities are not supported")
	}
	if len(request.OutputProtocols) > 0 {
		return errors.New("codex: output protocols are not supported")
	}
	if request.ResponseSchema.URI != "" {
		return errors.New("codex: response schema URI is not supported")
	}
	if err := validateResponseSchema(request.ResponseSchema.Value); err != nil {
		return err
	}
	if err := validateTools(request.Tools); err != nil {
		return err
	}
	if err := validateToolChoice(request.ToolChoice, request.Tools); err != nil {
		return err
	}
	for key := range request.Extensions {
		return fmt.Errorf("codex: unknown request extension %q", key)
	}
	if len(request.Messages) > 0 {
		if _, _, err := encodeMessages(request.Messages); err != nil {
			return err
		}
	}
	return nil
}

func validateResponseSchema(schema json.RawMessage) error {
	if len(schema) == 0 {
		return nil
	}
	if err := validateJSONObject(schema); err != nil {
		return fmt.Errorf("codex: response schema: %w", err)
	}
	return nil
}

func validateTools(tools []gopact.ToolSpec) error {
	seen := make(map[string]struct{}, len(tools))
	for _, tool := range tools {
		if err := validateTool(tool, seen); err != nil {
			return err
		}
		seen[tool.Name] = struct{}{}
	}
	return nil
}

func validateTool(tool gopact.ToolSpec, seen map[string]struct{}) error {
	if tool.Name == "" {
		return errors.New("codex: tool name is required")
	}
	if _, exists := seen[tool.Name]; exists {
		return fmt.Errorf("codex: duplicate tool %q", tool.Name)
	}
	if len(tool.Schema) == 0 {
		return nil
	}
	if err := validateJSONObject(tool.Schema); err != nil {
		return fmt.Errorf("codex: tool %q schema: %w", tool.Name, err)
	}
	return nil
}

func validateJSONObject(encoded []byte) error {
	var object map[string]any
	if err := json.Unmarshal(encoded, &object); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	if object == nil {
		return errors.New("must be a JSON object")
	}
	return nil
}

func validateToolChoice(choice gopact.ToolChoice, tools []gopact.ToolSpec) error {
	switch choice.Mode {
	case "", gopact.ToolChoiceModeAuto, gopact.ToolChoiceModeNone:
		return nil
	case gopact.ToolChoiceModeRequired:
		return validateRequiredToolChoice(tools)
	case gopact.ToolChoiceModeNamed:
		return validateNamedToolChoice(choice.Name, tools)
	default:
		return fmt.Errorf("codex: unknown tool choice mode %q", choice.Mode)
	}
}

func validateRequiredToolChoice(tools []gopact.ToolSpec) error {
	if len(tools) == 0 {
		return errors.New("codex: required tool choice has no tools")
	}
	return nil
}

func validateNamedToolChoice(name string, tools []gopact.ToolSpec) error {
	if name == "" {
		return errors.New("codex: named tool choice requires a name")
	}
	for _, tool := range tools {
		if tool.Name == name {
			return nil
		}
	}
	return fmt.Errorf("codex: named tool choice %q is not advertised", name)
}

func encodeToolChoice(choice gopact.ToolChoice) any {
	switch choice.Mode {
	case "", gopact.ToolChoiceModeAuto:
		return "auto"
	case gopact.ToolChoiceModeNone, gopact.ToolChoiceModeRequired:
		return choice.Mode
	case gopact.ToolChoiceModeNamed:
		return map[string]any{"type": "function", "name": choice.Name}
	default:
		return "auto"
	}
}

func cloneModelRequest(request gopact.ModelRequest) gopact.ModelRequest {
	request.Messages = cloneMessages(request.Messages)
	request.Tools = cloneToolSpecs(request.Tools)
	request.Modalities = append([]gopact.Modality(nil), request.Modalities...)
	request.Stop = append([]string(nil), request.Stop...)
	request.OutputProtocols = append([]gopact.OutputProtocol(nil), request.OutputProtocols...)
	request.ResponseSchema.Value = append(json.RawMessage(nil), request.ResponseSchema.Value...)
	request.Temperature = cloneFloat(request.Temperature)
	request.TopP = cloneFloat(request.TopP)
	request.Seed = cloneInt64(request.Seed)
	request.Metadata = cloneStringMap(request.Metadata)
	request.Extensions = cloneAnyMap(request.Extensions)
	return request
}

func cloneMessages(messages []gopact.Message) []gopact.Message {
	cloned := make([]gopact.Message, len(messages))
	for index, message := range messages {
		cloned[index] = cloneMessage(message)
	}
	return cloned
}

func cloneMessage(message gopact.Message) gopact.Message {
	return message.Clone()
}

func cloneToolSpecs(tools []gopact.ToolSpec) []gopact.ToolSpec {
	cloned := make([]gopact.ToolSpec, len(tools))
	for index, tool := range tools {
		tool.Schema = append(json.RawMessage(nil), tool.Schema...)
		tool.Metadata = cloneStringMap(tool.Metadata)
		cloned[index] = tool
	}
	return cloned
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

func cloneAnyMap(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	cloned := make(map[string]any, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func cloneFloat(value *float64) *float64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
