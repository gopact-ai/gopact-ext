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
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Media is a streamed binary Z.AI response. The caller must close Body.
type Media struct {
	Body          io.ReadCloser
	ContentType   string
	ContentLength int64
	Header        http.Header
}

// SpeechRequest configures GLM text-to-speech generation.
type SpeechRequest struct {
	Model              string              `json:"model"`
	Input              string              `json:"input"`
	Voice              string              `json:"voice"`
	ResponseFormat     string              `json:"response_format,omitempty"`
	EncodeFormat       string              `json:"encode_format,omitempty"`
	Speed              *float64            `json:"speed,omitempty"`
	Volume             *float64            `json:"volume,omitempty"`
	WatermarkEnabled   *bool               `json:"watermark_enabled,omitempty"`
	SensitiveWordCheck *SensitiveWordCheck `json:"sensitive_word_check,omitempty"`
	RequestID          string              `json:"request_id,omitempty"`
	UserID             string              `json:"user_id,omitempty"`
}

// SpeechEvent is one GLM text-to-speech SSE chunk.
type SpeechEvent struct {
	Choices   []SpeechChoice `json:"choices"`
	RequestID string         `json:"request_id"`
	Created   int64          `json:"created"`
	Error     *SpeechError   `json:"error"`
}

// SpeechChoice contains one streamed audio delta.
type SpeechChoice struct {
	Delta        SpeechDelta `json:"delta"`
	FinishReason string      `json:"finish_reason"`
	Index        int         `json:"index"`
}

// SpeechDelta carries encoded audio content.
type SpeechDelta struct {
	Content string `json:"content"`
	Role    string `json:"role"`
}

// SpeechError describes a failed streamed text-to-speech request.
type SpeechError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// CustomSpeechRequest generates speech from an inline voice sample.
type CustomSpeechRequest struct {
	Model              string
	Input              string
	VoiceText          string
	VoiceData          FileContent
	ResponseFormat     string
	WatermarkEnabled   *bool
	SensitiveWordCheck *SensitiveWordCheck
	RequestID          string
	UserID             string
}

// AsyncChatRequest configures an asynchronous GLM chat completion.
type AsyncChatRequest struct {
	Model              string              `json:"model"`
	Messages           any                 `json:"messages"`
	RequestID          string              `json:"request_id,omitempty"`
	UserID             string              `json:"user_id,omitempty"`
	DoSample           *bool               `json:"do_sample,omitempty"`
	Temperature        *float64            `json:"temperature,omitempty"`
	TopP               *float64            `json:"top_p,omitempty"`
	MaxTokens          int                 `json:"max_tokens,omitempty"`
	Seed               *int64              `json:"seed,omitempty"`
	Stop               any                 `json:"stop,omitempty"`
	SensitiveWordCheck *SensitiveWordCheck `json:"sensitive_word_check,omitempty"`
	Tools              any                 `json:"tools,omitempty"`
	ToolChoice         any                 `json:"tool_choice,omitempty"`
	Metadata           map[string]string   `json:"meta,omitempty"`
	Extra              json.RawMessage     `json:"extra,omitempty"`
	ResponseFormat     json.RawMessage     `json:"response_format,omitempty"`
	Thinking           json.RawMessage     `json:"thinking,omitempty"`
	WatermarkEnabled   *bool               `json:"watermark_enabled,omitempty"`
}

// AsyncChatResponse is a task status or completed asynchronous chat result.
type AsyncChatResponse struct {
	ID         string            `json:"id"`
	RequestID  string            `json:"request_id"`
	Model      string            `json:"model"`
	TaskStatus string            `json:"task_status"`
	Choices    []json.RawMessage `json:"choices"`
	Usage      TokenUsage        `json:"usage"`
}

// FileListQuery configures Z.AI file pagination.
type FileListQuery struct {
	Purpose string
	Limit   int
	After   string
	Order   string
}

// FileList is one page of Z.AI files.
type FileList struct {
	Object  string         `json:"object"`
	Data    []UploadedFile `json:"data"`
	HasMore bool           `json:"has_more"`
}

// DeletedFile confirms deletion of a Z.AI file.
type DeletedFile struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Deleted bool   `json:"deleted"`
}

// FileParserRequest configures synchronous or asynchronous document parsing.
type FileParserRequest struct {
	File     FileContent
	FileType string
	ToolType string
}

// FileParserTask is an asynchronous document-parsing task.
type FileParserTask struct {
	TaskID  string `json:"task_id"`
	Message string `json:"message"`
	Success bool   `json:"success"`
}

// FileParserResponse is a completed synchronous parsing result.
type FileParserResponse struct {
	TaskID           string `json:"task_id"`
	Message          string `json:"message"`
	Status           bool   `json:"status"`
	Content          string `json:"content"`
	ParsingResultURL string `json:"parsing_result_url"`
}

// HandwritingRequest configures the Z.AI handwriting OCR endpoint.
type HandwritingRequest struct {
	File         FileContent
	ToolType     string
	LanguageType string
	Probability  *bool
}

// HandwritingResponse is a handwriting OCR result.
type HandwritingResponse struct {
	TaskID         string              `json:"task_id"`
	Message        string              `json:"message"`
	Status         string              `json:"status"`
	WordsResultNum int                 `json:"words_result_num"`
	WordsResult    []HandwritingResult `json:"words_result"`
}

// HandwritingResult is one recognized text region.
type HandwritingResult struct {
	Location    HandwritingLocation    `json:"location"`
	Words       string                 `json:"words"`
	Probability HandwritingProbability `json:"probability"`
}

// HandwritingLocation describes a recognized text region.
type HandwritingLocation struct {
	Left   int `json:"left"`
	Top    int `json:"top"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

// HandwritingProbability reports aggregate recognition confidence.
type HandwritingProbability struct {
	Average  float64 `json:"average"`
	Variance float64 `json:"variance"`
	Minimum  float64 `json:"min"`
}

// Speech generates audio and returns a body that the caller must close.
func (model *Model) Speech(ctx context.Context, request SpeechRequest) (Media, error) {
	if model == nil {
		return Media{}, errors.New("glm: model is nil")
	}
	if err := validateSpeechRequest(request); err != nil {
		return Media{}, err
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return Media{}, fmt.Errorf("glm: encode speech request: %w", err)
	}
	return model.runtimeMedia(
		ctx, http.MethodPost, model.apiBaseURL+"/audio/speech",
		bytes.NewReader(encoded), "application/json", "application/octet-stream",
	)
}

// StreamSpeech streams encoded GLM text-to-speech chunks.
func (model *Model) StreamSpeech(ctx context.Context, request SpeechRequest) iter.Seq2[SpeechEvent, error] {
	return func(yield func(SpeechEvent, error) bool) {
		if model == nil {
			yield(SpeechEvent{}, errors.New("glm: model is nil"))
			return
		}
		if err := validateSpeechRequest(request); err != nil {
			yield(SpeechEvent{}, err)
			return
		}
		payload := struct {
			SpeechRequest
			Stream bool `json:"stream"`
		}{SpeechRequest: request, Stream: true}
		encoded, err := json.Marshal(payload)
		if err != nil {
			yield(SpeechEvent{}, fmt.Errorf("glm: encode speech stream request: %w", err))
			return
		}
		endpoint := model.apiBaseURL + "/audio/speech"
		for raw, err := range model.runtimeEventStream(ctx, endpoint, encoded, "application/json") {
			if err != nil {
				yield(SpeechEvent{}, err)
				return
			}
			var event SpeechEvent
			if err := json.Unmarshal(raw, &event); err != nil {
				yield(SpeechEvent{}, fmt.Errorf("glm: decode speech stream: %w", err))
				return
			}
			if !yield(event, nil) {
				return
			}
		}
	}
}

// CustomizeSpeech generates audio with an inline voice-cloning sample.
func (model *Model) CustomizeSpeech(ctx context.Context, request CustomSpeechRequest) (Media, error) {
	if model == nil {
		return Media{}, errors.New("glm: model is nil")
	}
	if strings.TrimSpace(request.Model) == "" || strings.TrimSpace(request.Input) == "" {
		return Media{}, errors.New("glm: custom speech model and input are required")
	}
	if strings.TrimSpace(request.VoiceData.Filename) == "" || len(request.VoiceData.Data) == 0 {
		return Media{}, errors.New("glm: custom speech voice data is required")
	}
	fields := []multipartField{
		{"model", request.Model}, {"input", request.Input}, {"voice_text", request.VoiceText},
		{"response_format", request.ResponseFormat}, {"request_id", request.RequestID},
		{"user_id", request.UserID},
	}
	fields = appendSensitiveWordFields(fields, request.SensitiveWordCheck)
	if request.WatermarkEnabled != nil {
		fields = append(fields, multipartField{"watermark_enabled", strconv.FormatBool(*request.WatermarkEnabled)})
	}
	body, contentType, err := encodeMultipartFile("voice_data", request.VoiceData, fields)
	if err != nil {
		return Media{}, err
	}
	return model.runtimeMedia(
		ctx, http.MethodPost, model.apiBaseURL+"/audio/customization",
		bytes.NewReader(body), contentType, "application/octet-stream",
	)
}

// CreateAsyncChat starts an asynchronous GLM chat completion.
func (model *Model) CreateAsyncChat(ctx context.Context, request AsyncChatRequest) (AsyncChatResponse, error) {
	if strings.TrimSpace(request.Model) == "" || request.Messages == nil {
		return AsyncChatResponse{}, errors.New("glm: async chat model and messages are required")
	}
	if request.MaxTokens < 0 {
		return AsyncChatResponse{}, errors.New("glm: async chat max tokens must not be negative")
	}
	if request.Temperature != nil && (*request.Temperature < 0 || *request.Temperature > 1) {
		return AsyncChatResponse{}, errors.New("glm: async chat temperature must be between 0 and 1")
	}
	if request.TopP != nil && (*request.TopP < 0 || *request.TopP > 1) {
		return AsyncChatResponse{}, errors.New("glm: async chat top p must be between 0 and 1")
	}
	var response AsyncChatResponse
	err := model.runtimeJSON(ctx, http.MethodPost, "/async/chat/completions", request, &response)
	return response, err
}

// AsyncChatResult returns the current status or completed asynchronous chat output.
func (model *Model) AsyncChatResult(ctx context.Context, taskID string) (AsyncChatResponse, error) {
	if strings.TrimSpace(taskID) == "" {
		return AsyncChatResponse{}, errors.New("glm: async chat task id is required")
	}
	var response AsyncChatResponse
	err := model.runtimeJSON(ctx, http.MethodGet, "/async-result/"+url.PathEscape(taskID), nil, &response)
	return response, err
}

// ListFiles returns one page of Z.AI files.
func (model *Model) ListFiles(ctx context.Context, query FileListQuery) (FileList, error) {
	if query.Limit < 0 {
		return FileList{}, errors.New("glm: file list limit must not be negative")
	}
	values := url.Values{}
	values.Set("purpose", query.Purpose)
	values.Set("after", query.After)
	values.Set("order", query.Order)
	if query.Limit > 0 {
		values.Set("limit", strconv.Itoa(query.Limit))
	}
	var response FileList
	err := model.runtimeJSON(ctx, http.MethodGet, withRuntimeQuery("/files", values), nil, &response)
	return response, err
}

// DeleteFile deletes one Z.AI file.
func (model *Model) DeleteFile(ctx context.Context, fileID string) (DeletedFile, error) {
	if strings.TrimSpace(fileID) == "" {
		return DeletedFile{}, errors.New("glm: file id is required")
	}
	var response DeletedFile
	err := model.runtimeJSON(ctx, http.MethodDelete, "/files/"+url.PathEscape(fileID), nil, &response)
	return response, err
}

// DownloadFile streams one Z.AI file. The caller must close the returned body.
func (model *Model) DownloadFile(ctx context.Context, fileID string) (Media, error) {
	if model == nil {
		return Media{}, errors.New("glm: model is nil")
	}
	if strings.TrimSpace(fileID) == "" {
		return Media{}, errors.New("glm: file id is required")
	}
	endpoint := model.apiBaseURL + "/files/" + url.PathEscape(fileID) + "/content"
	return model.runtimeMedia(ctx, http.MethodGet, endpoint, nil, "", "application/binary")
}

// CreateFileParserTask starts asynchronous document parsing.
func (model *Model) CreateFileParserTask(
	ctx context.Context,
	request FileParserRequest,
) (FileParserTask, error) {
	if model == nil {
		return FileParserTask{}, errors.New("glm: model is nil")
	}
	if err := validateFileParserRequest(request, false); err != nil {
		return FileParserTask{}, err
	}
	body, contentType, err := encodeMultipartFile("file", request.File, []multipartField{
		{"file_type", request.FileType}, {"tool_type", request.ToolType},
	})
	if err != nil {
		return FileParserTask{}, err
	}
	var response FileParserTask
	err = model.runtimeResponse(
		ctx, http.MethodPost, model.apiBaseURL+"/files/parser/create",
		bytes.NewReader(body), contentType, &response,
	)
	return response, err
}

// ParseFile runs synchronous prime document parsing.
func (model *Model) ParseFile(ctx context.Context, request FileParserRequest) (FileParserResponse, error) {
	if model == nil {
		return FileParserResponse{}, errors.New("glm: model is nil")
	}
	if err := validateFileParserRequest(request, true); err != nil {
		return FileParserResponse{}, err
	}
	body, contentType, err := encodeMultipartFile("file", request.File, []multipartField{
		{"file_type", request.FileType}, {"tool_type", request.ToolType},
	})
	if err != nil {
		return FileParserResponse{}, err
	}
	var response FileParserResponse
	err = model.runtimeResponse(
		ctx, http.MethodPost, model.apiBaseURL+"/files/parser/sync",
		bytes.NewReader(body), contentType, &response,
	)
	return response, err
}

// FileParserResult streams a text result or download-link response.
func (model *Model) FileParserResult(ctx context.Context, taskID, format string) (Media, error) {
	if model == nil {
		return Media{}, errors.New("glm: model is nil")
	}
	if strings.TrimSpace(taskID) == "" {
		return Media{}, errors.New("glm: file parser task id is required")
	}
	if format != "text" && format != "download_link" {
		return Media{}, errors.New("glm: file parser format must be text or download_link")
	}
	endpoint := model.apiBaseURL + "/files/parser/result/" + url.PathEscape(taskID) + "/" + format
	return model.runtimeMedia(ctx, http.MethodGet, endpoint, nil, "", "application/binary")
}

// RecognizeHandwriting runs Z.AI handwriting OCR on an uploaded image.
func (model *Model) RecognizeHandwriting(
	ctx context.Context,
	request HandwritingRequest,
) (HandwritingResponse, error) {
	if model == nil {
		return HandwritingResponse{}, errors.New("glm: model is nil")
	}
	if strings.TrimSpace(request.File.Filename) == "" || len(request.File.Data) == 0 {
		return HandwritingResponse{}, errors.New("glm: handwriting file is required")
	}
	if request.ToolType == "" {
		request.ToolType = "hand_write"
	}
	if request.ToolType != "hand_write" {
		return HandwritingResponse{}, errors.New("glm: handwriting tool type must be hand_write")
	}
	fields := []multipartField{
		{"tool_type", request.ToolType}, {"language_type", request.LanguageType},
	}
	if request.Probability != nil {
		fields = append(fields, multipartField{"probability", strconv.FormatBool(*request.Probability)})
	}
	body, contentType, err := encodeMultipartFile("file", request.File, fields)
	if err != nil {
		return HandwritingResponse{}, err
	}
	var response HandwritingResponse
	err = model.runtimeResponse(
		ctx, http.MethodPost, model.apiBaseURL+"/files/ocr",
		bytes.NewReader(body), contentType, &response,
	)
	return response, err
}

func validateSpeechRequest(request SpeechRequest) error {
	if strings.TrimSpace(request.Model) == "" || strings.TrimSpace(request.Input) == "" || strings.TrimSpace(request.Voice) == "" {
		return errors.New("glm: speech model, input, and voice are required")
	}
	if request.Speed != nil && (*request.Speed < 0.5 || *request.Speed > 2) {
		return errors.New("glm: speech speed must be between 0.5 and 2")
	}
	if request.Volume != nil && (*request.Volume <= 0 || *request.Volume > 10) {
		return errors.New("glm: speech volume must be greater than 0 and at most 10")
	}
	return nil
}

func validateFileParserRequest(request FileParserRequest, synchronous bool) error {
	if strings.TrimSpace(request.File.Filename) == "" || len(request.File.Data) == 0 {
		return errors.New("glm: file parser input is required")
	}
	if synchronous {
		if request.ToolType != "prime-sync" {
			return errors.New("glm: synchronous file parser tool type must be prime-sync")
		}
		return nil
	}
	if request.ToolType != "lite" && request.ToolType != "expert" && request.ToolType != "prime" {
		return errors.New("glm: file parser tool type must be lite, expert, or prime")
	}
	return nil
}

type multipartField struct {
	name  string
	value string
}

func appendSensitiveWordFields(fields []multipartField, policy *SensitiveWordCheck) []multipartField {
	if policy == nil {
		return fields
	}
	return append(fields,
		multipartField{"sensitive_word_check[type]", policy.Type},
		multipartField{"sensitive_word_check[status]", policy.Status},
	)
}

func encodeMultipartFile(
	field string,
	file FileContent,
	fields []multipartField,
) ([]byte, string, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile(field, file.Filename)
	if err != nil {
		return nil, "", fmt.Errorf("glm: create multipart file: %w", err)
	}
	if _, err := part.Write(file.Data); err != nil {
		return nil, "", fmt.Errorf("glm: write multipart file: %w", err)
	}
	for _, item := range fields {
		if item.value == "" {
			continue
		}
		if err := writer.WriteField(item.name, item.value); err != nil {
			return nil, "", fmt.Errorf("glm: write multipart field: %w", err)
		}
	}
	if err := writer.Close(); err != nil {
		return nil, "", fmt.Errorf("glm: close multipart request: %w", err)
	}
	return body.Bytes(), writer.FormDataContentType(), nil
}

func (model *Model) runtimeMedia(
	ctx context.Context,
	method, endpoint string,
	body io.Reader,
	contentType, accept string,
) (Media, error) {
	if model == nil {
		return Media{}, errors.New("glm: model is nil")
	}
	if model.httpClient == nil {
		return Media{}, errors.New("glm: http client is nil")
	}
	if ctx == nil {
		ctx = context.TODO()
	}
	callCtx, cancel := context.WithTimeout(ctx, model.timeout)
	request, err := http.NewRequestWithContext(callCtx, method, endpoint, body)
	if err != nil {
		cancel()
		return Media{}, fmt.Errorf("glm: create media request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+model.apiKey)
	request.Header.Set("Accept", accept)
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	client := *model.httpClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(request)
	if err != nil {
		cancel()
		return Media{}, fmt.Errorf("glm: media request: %w", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		defer func() { _ = response.Body.Close() }()
		defer cancel()
		data, _ := io.ReadAll(io.LimitReader(response.Body, 4<<10))
		return Media{}, fmt.Errorf("glm: status %d: %s", response.StatusCode, model.redactRuntimeError(data))
	}
	return Media{
		Body:        cancelReadCloser{ReadCloser: response.Body, cancel: cancel},
		ContentType: response.Header.Get("Content-Type"), ContentLength: response.ContentLength,
		Header: response.Header.Clone(),
	}, nil
}

func (model *Model) runtimeEventStream(
	ctx context.Context,
	endpoint string,
	body []byte,
	contentType string,
) iter.Seq2[json.RawMessage, error] {
	return func(yield func(json.RawMessage, error) bool) {
		media, err := model.runtimeMedia(
			ctx, http.MethodPost, endpoint, bytes.NewReader(body), contentType, "text/event-stream",
		)
		if err != nil {
			yield(nil, err)
			return
		}
		defer func() { _ = media.Body.Close() }()
		scanner := bufio.NewScanner(media.Body)
		scanner.Buffer(make([]byte, 64<<10), maxRuntimeStreamEventBytes)
		var data strings.Builder
		emit := func() bool {
			payload := strings.TrimSpace(data.String())
			data.Reset()
			if payload == "" {
				return true
			}
			if payload == "[DONE]" {
				return false
			}
			raw := json.RawMessage(append([]byte(nil), payload...))
			if !json.Valid(raw) {
				yield(nil, errors.New("glm: stream event is invalid json"))
				return false
			}
			return yield(raw, nil)
		}
		for scanner.Scan() {
			line := scanner.Text()
			switch {
			case line == "":
				if !emit() {
					return
				}
			case strings.HasPrefix(line, "data:"):
				if data.Len() > 0 {
					data.WriteByte('\n')
				}
				data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			}
		}
		if err := scanner.Err(); err != nil {
			yield(nil, fmt.Errorf("glm: read stream: %w", err))
			return
		}
		if data.Len() > 0 {
			emit()
		}
	}
}

func withRuntimeQuery(path string, values url.Values) string {
	for key, entries := range values {
		if len(entries) == 1 && entries[0] == "" {
			values.Del(key)
		}
	}
	if len(values) == 0 {
		return path
	}
	return path + "?" + values.Encode()
}

type cancelReadCloser struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (body cancelReadCloser) Close() error {
	err := body.ReadCloser.Close()
	body.cancel()
	return err
}
