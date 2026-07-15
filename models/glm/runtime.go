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
	"strings"
)

const (
	DefaultImageModel          = "glm-image"
	DefaultTranscriptionModel  = "glm-asr-2512"
	DefaultTokenizerModel      = "glm-4.6"
	DefaultLayoutModel         = "glm-ocr"
	DefaultSearchEngine        = "search-prime"
	maxRuntimeResponseBytes    = 64 << 20
	maxRuntimeStreamEventBytes = 1 << 20
)

// SensitiveWordCheck configures Z.AI's provider-side content filtering.
type SensitiveWordCheck struct {
	Type   string `json:"type,omitempty"`
	Status string `json:"status,omitempty"`
}

// ImageRequest configures synchronous GLM image generation.
type ImageRequest struct {
	Model              string              `json:"model"`
	Prompt             string              `json:"prompt"`
	N                  int                 `json:"n,omitempty"`
	Quality            string              `json:"quality,omitempty"`
	ResponseFormat     string              `json:"response_format,omitempty"`
	Size               string              `json:"size,omitempty"`
	Style              string              `json:"style,omitempty"`
	User               string              `json:"user,omitempty"`
	RequestID          string              `json:"request_id,omitempty"`
	UserID             string              `json:"user_id,omitempty"`
	SensitiveWordCheck *SensitiveWordCheck `json:"sensitive_word_check,omitempty"`
	WatermarkEnabled   *bool               `json:"watermark_enabled,omitempty"`
}

// AsyncImageRequest configures asynchronous GLM image generation.
type AsyncImageRequest struct {
	Model            string `json:"model"`
	Prompt           string `json:"prompt"`
	Quality          string `json:"quality,omitempty"`
	Size             string `json:"size,omitempty"`
	RequestID        string `json:"request_id,omitempty"`
	UserID           string `json:"user_id,omitempty"`
	WatermarkEnabled *bool  `json:"watermark_enabled,omitempty"`
}

// ImageResponse is a synchronous GLM image result.
type ImageResponse struct {
	Created       int64           `json:"created"`
	Data          []ImageResult   `json:"data"`
	ContentFilter []ContentFilter `json:"content_filter"`
}

// ImageResult contains one generated image URL.
type ImageResult struct {
	URL string `json:"url"`
}

// ContentFilter reports one GLM content-safety stage.
type ContentFilter struct {
	Role  string `json:"role"`
	Level int    `json:"level"`
}

// AsyncTask is a newly created GLM image or video task.
type AsyncTask struct {
	Model      string `json:"model"`
	ID         string `json:"id"`
	RequestID  string `json:"request_id"`
	TaskStatus string `json:"task_status"`
}

// VideoCommon contains fields shared by GLM video models.
type VideoCommon struct {
	RequestID          string              `json:"request_id,omitempty"`
	UserID             string              `json:"user_id,omitempty"`
	OffPeak            *bool               `json:"off_peak,omitempty"`
	SensitiveWordCheck *SensitiveWordCheck `json:"sensitive_word_check,omitempty"`
	WatermarkEnabled   *bool               `json:"watermark_enabled,omitempty"`
}

// VideoRequest is one supported GLM video-generation request shape.
type VideoRequest interface {
	isVideoRequest()
}

// CogVideoRequest configures CogVideoX-3 generation.
type CogVideoRequest struct {
	VideoCommon
	Model     string   `json:"model"`
	Prompt    string   `json:"prompt,omitempty"`
	Quality   string   `json:"quality,omitempty"`
	WithAudio bool     `json:"with_audio,omitempty"`
	Images    []string `json:"image_url,omitempty"`
	Size      string   `json:"size,omitempty"`
	FPS       int      `json:"fps,omitempty"`
	Duration  int      `json:"duration,omitempty"`
}

func (CogVideoRequest) isVideoRequest() {}

// ViduTextVideoRequest configures Vidu text-to-video generation.
type ViduTextVideoRequest struct {
	VideoCommon
	Model             string `json:"model"`
	Prompt            string `json:"prompt"`
	Style             string `json:"style,omitempty"`
	Duration          int    `json:"duration,omitempty"`
	AspectRatio       string `json:"aspect_ratio,omitempty"`
	Size              string `json:"size,omitempty"`
	MovementAmplitude string `json:"movement_amplitude,omitempty"`
}

func (ViduTextVideoRequest) isVideoRequest() {}

// ViduImageVideoRequest configures Vidu image-to-video generation.
type ViduImageVideoRequest struct {
	VideoCommon
	Model             string `json:"model"`
	Prompt            string `json:"prompt,omitempty"`
	Image             string `json:"image_url,omitempty"`
	Duration          int    `json:"duration,omitempty"`
	Size              string `json:"size,omitempty"`
	MovementAmplitude string `json:"movement_amplitude,omitempty"`
	WithAudio         bool   `json:"with_audio,omitempty"`
}

func (ViduImageVideoRequest) isVideoRequest() {}

// ViduFramesVideoRequest configures Vidu first/last-frame generation.
type ViduFramesVideoRequest struct {
	VideoCommon
	Model             string   `json:"model"`
	Prompt            string   `json:"prompt,omitempty"`
	Images            []string `json:"image_url"`
	Duration          int      `json:"duration,omitempty"`
	Size              string   `json:"size,omitempty"`
	MovementAmplitude string   `json:"movement_amplitude,omitempty"`
	WithAudio         bool     `json:"with_audio,omitempty"`
}

func (ViduFramesVideoRequest) isVideoRequest() {}

// ViduReferenceVideoRequest configures Vidu reference-image generation.
type ViduReferenceVideoRequest struct {
	VideoCommon
	Model             string   `json:"model"`
	Prompt            string   `json:"prompt,omitempty"`
	Images            []string `json:"image_url"`
	Duration          int      `json:"duration,omitempty"`
	AspectRatio       string   `json:"aspect_ratio,omitempty"`
	Size              string   `json:"size,omitempty"`
	MovementAmplitude string   `json:"movement_amplitude,omitempty"`
	WithAudio         bool     `json:"with_audio,omitempty"`
}

func (ViduReferenceVideoRequest) isVideoRequest() {}

// AsyncResult is a queried GLM image or video task result.
type AsyncResult struct {
	Model      string        `json:"model"`
	TaskStatus string        `json:"task_status"`
	RequestID  string        `json:"request_id"`
	Videos     []VideoResult `json:"video_result"`
	Images     []ImageResult `json:"image_result"`
}

// VideoResult contains one generated video and cover URL.
type VideoResult struct {
	URL      string `json:"url"`
	CoverURL string `json:"cover_image_url"`
}

// FileContent is an in-memory file sent to a GLM multipart endpoint.
type FileContent struct {
	Filename string
	Data     []byte
}

// TranscriptionRequest configures GLM speech-to-text with an uploaded file or
// Base64 audio. Set exactly one of File and FileBase64.
type TranscriptionRequest struct {
	File               *FileContent
	FileBase64         string              `json:"file_base64"`
	Model              string              `json:"model"`
	Prompt             string              `json:"prompt,omitempty"`
	Hotwords           []string            `json:"hotwords,omitempty"`
	RequestID          string              `json:"request_id,omitempty"`
	UserID             string              `json:"user_id,omitempty"`
	SensitiveWordCheck *SensitiveWordCheck `json:"sensitive_word_check,omitempty"`
}

// TranscriptionResponse is a GLM speech-to-text result.
type TranscriptionResponse struct {
	ID        string `json:"id"`
	Created   int64  `json:"created"`
	RequestID string `json:"request_id"`
	Model     string `json:"model"`
	Text      string `json:"text"`
}

// TranscriptionEvent is one streaming GLM speech-to-text event.
type TranscriptionEvent struct {
	ID      string `json:"id"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Type    string `json:"type"`
	Delta   string `json:"delta"`
}

// UploadedFile is a file accepted for Z.AI agent workflows.
type UploadedFile struct {
	ID        string `json:"id"`
	Object    string `json:"object"`
	Bytes     int64  `json:"bytes"`
	Filename  string `json:"filename"`
	Purpose   string `json:"purpose"`
	CreatedAt int64  `json:"created_at"`
}

// TokenizerMessage is one text or multimodal tokenizer message. Content may be
// a string or a provider content-part array.
type TokenizerMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content,omitempty"`
}

// TokenizerRequest configures GLM token counting.
type TokenizerRequest struct {
	Model     string             `json:"model"`
	Messages  []TokenizerMessage `json:"messages"`
	Tools     []json.RawMessage  `json:"tools,omitempty"`
	RequestID string             `json:"request_id,omitempty"`
	UserID    string             `json:"user_id,omitempty"`
}

// TokenizerResponse is a GLM token count result.
type TokenizerResponse struct {
	ID        string     `json:"id"`
	Created   int64      `json:"created"`
	RequestID string     `json:"request_id"`
	Usage     TokenUsage `json:"usage"`
}

// TokenUsage reports GLM token counts, including multimodal inputs.
type TokenUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	ImageTokens      int `json:"image_tokens"`
	VideoTokens      int `json:"video_tokens"`
	CachedTokens     int `json:"cached_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// LayoutRequest configures GLM OCR and layout parsing.
type LayoutRequest struct {
	Model                   string `json:"model"`
	File                    string `json:"file"`
	ReturnCropImages        bool   `json:"return_crop_images,omitempty"`
	NeedLayoutVisualization bool   `json:"need_layout_visualization,omitempty"`
	StartPage               int    `json:"start_page_id,omitempty"`
	EndPage                 int    `json:"end_page_id,omitempty"`
	RequestID               string `json:"request_id,omitempty"`
	UserID                  string `json:"user_id,omitempty"`
}

// LayoutResponse is a GLM OCR and document-layout result.
type LayoutResponse struct {
	ID            string           `json:"id"`
	Created       int64            `json:"created"`
	Model         string           `json:"model"`
	Markdown      string           `json:"md_results"`
	Pages         [][]LayoutDetail `json:"layout_details"`
	Visualization []string         `json:"layout_visualization"`
	Data          LayoutData       `json:"data_info"`
	Usage         struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
		Details          struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
	RequestID string `json:"request_id"`
}

// LayoutDetail is one recognized document element.
type LayoutDetail struct {
	Index   int       `json:"index"`
	Label   string    `json:"label"`
	Bounds  []float64 `json:"bbox_2d"`
	Content string    `json:"content"`
	Height  int       `json:"height"`
	Width   int       `json:"width"`
}

// LayoutData describes parsed document pages.
type LayoutData struct {
	PageCount int          `json:"num_pages"`
	Pages     []LayoutPage `json:"pages"`
}

// LayoutPage describes one source page.
type LayoutPage struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

// SearchRequest configures Z.AI web search.
type SearchRequest struct {
	Engine             string              `json:"search_engine"`
	Query              string              `json:"search_query"`
	Count              int                 `json:"count,omitempty"`
	Domain             string              `json:"search_domain_filter,omitempty"`
	Recency            string              `json:"search_recency_filter,omitempty"`
	ContentSize        string              `json:"content_size,omitempty"`
	SearchIntent       *bool               `json:"search_intent,omitempty"`
	IncludeImage       *bool               `json:"include_image,omitempty"`
	RequestID          string              `json:"request_id,omitempty"`
	UserID             string              `json:"user_id,omitempty"`
	SensitiveWordCheck *SensitiveWordCheck `json:"sensitive_word_check,omitempty"`
}

// SearchResponse is a Z.AI web search result.
type SearchResponse struct {
	ID      string         `json:"id"`
	Created int64          `json:"created"`
	Results []SearchResult `json:"search_result"`
}

// SearchResult is one Z.AI web result.
type SearchResult struct {
	Title       string `json:"title"`
	Content     string `json:"content"`
	Link        string `json:"link"`
	Media       string `json:"media"`
	Icon        string `json:"icon"`
	Reference   string `json:"refer"`
	PublishDate string `json:"publish_date"`
}

// ReaderRequest configures Z.AI web-page extraction.
type ReaderRequest struct {
	URL               string `json:"url"`
	TimeoutSeconds    int    `json:"timeout,omitempty"`
	NoCache           bool   `json:"no_cache,omitempty"`
	ReturnFormat      string `json:"return_format,omitempty"`
	RetainImages      *bool  `json:"retain_images,omitempty"`
	NoGFM             bool   `json:"no_gfm,omitempty"`
	KeepImageDataURLs bool   `json:"keep_img_data_url,omitempty"`
	WithImageSummary  bool   `json:"with_images_summary,omitempty"`
	WithLinkSummary   bool   `json:"with_links_summary,omitempty"`
}

// ReaderResponse is a Z.AI web-page extraction result.
type ReaderResponse struct {
	ID        string       `json:"id"`
	Created   int64        `json:"created"`
	RequestID string       `json:"request_id"`
	Model     string       `json:"model"`
	Result    ReaderResult `json:"reader_result"`
}

// ReaderResult contains extracted page content and metadata.
type ReaderResult struct {
	Content     string          `json:"content"`
	Description string          `json:"description"`
	Title       string          `json:"title"`
	URL         string          `json:"url"`
	External    json.RawMessage `json:"external"`
	Metadata    json.RawMessage `json:"metadata"`
}

// GenerateImage calls synchronous GLM image generation.
func (model *Model) GenerateImage(ctx context.Context, request ImageRequest) (ImageResponse, error) {
	if request.Model == "" {
		request.Model = DefaultImageModel
	}
	if strings.TrimSpace(request.Prompt) == "" {
		return ImageResponse{}, errors.New("glm: image prompt is required")
	}
	var response ImageResponse
	err := model.runtimeJSON(ctx, http.MethodPost, "/images/generations", request, &response)
	return response, err
}

// CreateImage starts asynchronous GLM image generation.
func (model *Model) CreateImage(ctx context.Context, request AsyncImageRequest) (AsyncTask, error) {
	if request.Model == "" {
		request.Model = DefaultImageModel
	}
	if strings.TrimSpace(request.Prompt) == "" {
		return AsyncTask{}, errors.New("glm: image prompt is required")
	}
	var task AsyncTask
	err := model.runtimeJSON(ctx, http.MethodPost, "/async/images/generations", request, &task)
	return task, err
}

// CreateVideo starts asynchronous GLM video generation.
func (model *Model) CreateVideo(ctx context.Context, request VideoRequest) (AsyncTask, error) {
	if request == nil {
		return AsyncTask{}, errors.New("glm: video request is required")
	}
	if err := validateVideoRequest(request); err != nil {
		return AsyncTask{}, err
	}
	var task AsyncTask
	err := model.runtimeJSON(ctx, http.MethodPost, "/videos/generations", request, &task)
	return task, err
}

// AsyncResult gets an asynchronous GLM image or video result.
func (model *Model) AsyncResult(ctx context.Context, taskID string) (AsyncResult, error) {
	if strings.TrimSpace(taskID) == "" {
		return AsyncResult{}, errors.New("glm: task id is required")
	}
	var result AsyncResult
	err := model.runtimeJSON(ctx, http.MethodGet, "/async-result/"+url.PathEscape(taskID), nil, &result)
	return result, err
}

// Transcribe converts uploaded or base64-encoded audio to text.
func (model *Model) Transcribe(ctx context.Context, request TranscriptionRequest) (TranscriptionResponse, error) {
	if model == nil {
		return TranscriptionResponse{}, errors.New("glm: model is nil")
	}
	if err := prepareTranscriptionRequest(&request); err != nil {
		return TranscriptionResponse{}, err
	}
	body, contentType, err := encodeTranscriptionRequest(request, false)
	if err != nil {
		return TranscriptionResponse{}, err
	}
	var response TranscriptionResponse
	err = model.runtimeResponse(
		ctx, http.MethodPost, model.apiBaseURL+"/audio/transcriptions",
		bytes.NewReader(body), contentType, &response,
	)
	return response, err
}

// StreamTranscription streams GLM speech-to-text delta and completion events.
func (model *Model) StreamTranscription(
	ctx context.Context,
	request TranscriptionRequest,
) iter.Seq2[TranscriptionEvent, error] {
	return func(yield func(TranscriptionEvent, error) bool) {
		if model == nil {
			yield(TranscriptionEvent{}, errors.New("glm: model is nil"))
			return
		}
		if err := prepareTranscriptionRequest(&request); err != nil {
			yield(TranscriptionEvent{}, err)
			return
		}
		body, contentType, err := encodeTranscriptionRequest(request, true)
		if err != nil {
			yield(TranscriptionEvent{}, err)
			return
		}
		model.streamTranscription(ctx, body, contentType, yield)
	}
}

// UploadFile uploads an auxiliary file for Z.AI agent workflows.
func (model *Model) UploadFile(ctx context.Context, filename string, data []byte) (UploadedFile, error) {
	if model == nil {
		return UploadedFile{}, errors.New("glm: model is nil")
	}
	if strings.TrimSpace(filename) == "" {
		return UploadedFile{}, errors.New("glm: upload filename is required")
	}
	if len(data) == 0 {
		return UploadedFile{}, errors.New("glm: upload file is empty")
	}
	if len(data) > 100<<20 {
		return UploadedFile{}, errors.New("glm: upload file exceeds 100 MB")
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("purpose", "agent"); err != nil {
		return UploadedFile{}, fmt.Errorf("glm: write upload purpose: %w", err)
	}
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return UploadedFile{}, fmt.Errorf("glm: create upload part: %w", err)
	}
	if _, err := part.Write(data); err != nil {
		return UploadedFile{}, fmt.Errorf("glm: write upload file: %w", err)
	}
	if err := writer.Close(); err != nil {
		return UploadedFile{}, fmt.Errorf("glm: close upload: %w", err)
	}
	var response UploadedFile
	err = model.runtimeResponse(ctx, http.MethodPost, model.apiBaseURL+"/files", &body, writer.FormDataContentType(), &response)
	return response, err
}

// Tokenize counts tokens for GLM messages.
func (model *Model) Tokenize(ctx context.Context, request TokenizerRequest) (TokenizerResponse, error) {
	if request.Model == "" {
		request.Model = DefaultTokenizerModel
	}
	if len(request.Messages) == 0 {
		return TokenizerResponse{}, errors.New("glm: tokenizer messages are required")
	}
	var response TokenizerResponse
	err := model.runtimeJSON(ctx, http.MethodPost, "/tokenizer", request, &response)
	return response, err
}

// ParseLayout runs GLM OCR and document layout parsing.
func (model *Model) ParseLayout(ctx context.Context, request LayoutRequest) (LayoutResponse, error) {
	if request.Model == "" {
		request.Model = DefaultLayoutModel
	}
	if strings.TrimSpace(request.File) == "" {
		return LayoutResponse{}, errors.New("glm: layout file URL or base64 data is required")
	}
	if request.StartPage < 0 || request.EndPage < 0 || request.EndPage > 0 && request.StartPage > request.EndPage {
		return LayoutResponse{}, errors.New("glm: invalid layout page range")
	}
	var response LayoutResponse
	err := model.runtimeJSON(ctx, http.MethodPost, "/layout_parsing", request, &response)
	return response, err
}

// Search calls Z.AI web search.
func (model *Model) Search(ctx context.Context, request SearchRequest) (SearchResponse, error) {
	if request.Engine == "" {
		request.Engine = DefaultSearchEngine
	}
	if strings.TrimSpace(request.Query) == "" {
		return SearchResponse{}, errors.New("glm: search query is required")
	}
	if request.Count < 0 || request.Count > 50 {
		return SearchResponse{}, errors.New("glm: search count must be between 1 and 50")
	}
	var response SearchResponse
	err := model.runtimeJSON(ctx, http.MethodPost, "/web_search", request, &response)
	return response, err
}

// ReadURL extracts a web page through Z.AI Reader.
func (model *Model) ReadURL(ctx context.Context, request ReaderRequest) (ReaderResponse, error) {
	if strings.TrimSpace(request.URL) == "" {
		return ReaderResponse{}, errors.New("glm: reader URL is required")
	}
	if request.TimeoutSeconds < 0 {
		return ReaderResponse{}, errors.New("glm: reader timeout must not be negative")
	}
	var response ReaderResponse
	err := model.runtimeJSON(ctx, http.MethodPost, "/reader", request, &response)
	return response, err
}

func validateVideoRequest(request VideoRequest) error {
	switch value := request.(type) {
	case CogVideoRequest:
		if value.Model == "" || value.Prompt == "" && len(value.Images) == 0 {
			return errors.New("glm: cogvideo model and prompt or images are required")
		}
	case ViduTextVideoRequest:
		if value.Model == "" || strings.TrimSpace(value.Prompt) == "" {
			return errors.New("glm: vidu text video model and prompt are required")
		}
	case ViduImageVideoRequest:
		if value.Model == "" || value.Prompt == "" && value.Image == "" {
			return errors.New("glm: vidu image video model and prompt or image are required")
		}
	case ViduFramesVideoRequest:
		if value.Model == "" || len(value.Images) == 0 || len(value.Images) > 2 {
			return errors.New("glm: vidu frame video requires a model and one or two images")
		}
	case ViduReferenceVideoRequest:
		if value.Model == "" || len(value.Images) == 0 || len(value.Images) > 3 {
			return errors.New("glm: vidu reference video requires a model and one to three images")
		}
	default:
		return errors.New("glm: unsupported video request type")
	}
	return nil
}

func (model *Model) runtimeJSON(ctx context.Context, method, path string, input, output any) error {
	if model == nil {
		return errors.New("glm: model is nil")
	}
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return fmt.Errorf("glm: encode request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	contentType := ""
	if input != nil {
		contentType = "application/json"
	}
	return model.runtimeResponse(ctx, method, model.apiBaseURL+path, body, contentType, output)
}

func prepareTranscriptionRequest(request *TranscriptionRequest) error {
	if request.Model == "" {
		request.Model = DefaultTranscriptionModel
	}
	hasFile := request.File != nil
	hasBase64 := strings.TrimSpace(request.FileBase64) != ""
	if hasFile == hasBase64 {
		return errors.New("glm: transcription requires exactly one audio file or base64 input")
	}
	if hasFile {
		if strings.TrimSpace(request.File.Filename) == "" || len(request.File.Data) == 0 {
			return errors.New("glm: transcription audio file is required")
		}
		if len(request.File.Data) > 25<<20 {
			return errors.New("glm: transcription audio file exceeds 25 MB")
		}
	}
	if len(request.FileBase64) > (25<<20)*4/3+4 {
		return errors.New("glm: transcription base64 audio exceeds 25 MB")
	}
	if len(request.Hotwords) > 100 {
		return errors.New("glm: transcription accepts at most 100 hotwords")
	}
	return nil
}

func encodeTranscriptionRequest(request TranscriptionRequest, stream bool) ([]byte, string, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if request.File != nil {
		part, err := writer.CreateFormFile("file", request.File.Filename)
		if err != nil {
			return nil, "", fmt.Errorf("glm: create transcription file: %w", err)
		}
		if _, err := part.Write(request.File.Data); err != nil {
			return nil, "", fmt.Errorf("glm: write transcription file: %w", err)
		}
	} else if err := writer.WriteField("file_base64", request.FileBase64); err != nil {
		return nil, "", fmt.Errorf("glm: write transcription base64 audio: %w", err)
	}
	fields := []struct{ name, value string }{
		{"model", request.Model}, {"prompt", request.Prompt},
		{"request_id", request.RequestID}, {"user_id", request.UserID},
	}
	for _, field := range fields {
		if field.value != "" {
			if err := writer.WriteField(field.name, field.value); err != nil {
				return nil, "", fmt.Errorf("glm: write transcription field: %w", err)
			}
		}
	}
	for _, hotword := range request.Hotwords {
		if err := writer.WriteField("hotwords[]", hotword); err != nil {
			return nil, "", fmt.Errorf("glm: write transcription hotword: %w", err)
		}
	}
	if request.SensitiveWordCheck != nil {
		fields := []struct{ name, value string }{
			{"sensitive_word_check[type]", request.SensitiveWordCheck.Type},
			{"sensitive_word_check[status]", request.SensitiveWordCheck.Status},
		}
		for _, field := range fields {
			if field.value == "" {
				continue
			}
			if err := writer.WriteField(field.name, field.value); err != nil {
				return nil, "", fmt.Errorf("glm: write transcription sensitive-word policy: %w", err)
			}
		}
	}
	if stream {
		if err := writer.WriteField("stream", "true"); err != nil {
			return nil, "", fmt.Errorf("glm: write transcription stream flag: %w", err)
		}
	}
	if err := writer.Close(); err != nil {
		return nil, "", fmt.Errorf("glm: close transcription request: %w", err)
	}
	return body.Bytes(), writer.FormDataContentType(), nil
}

func (model *Model) streamTranscription(
	ctx context.Context,
	body []byte,
	contentType string,
	yield func(TranscriptionEvent, error) bool,
) {
	if ctx == nil {
		ctx = context.TODO()
	}
	callCtx, cancel := context.WithTimeout(ctx, model.timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(
		callCtx, http.MethodPost, model.apiBaseURL+"/audio/transcriptions", bytes.NewReader(body),
	)
	if err != nil {
		yield(TranscriptionEvent{}, fmt.Errorf("glm: create transcription stream: %w", err))
		return
	}
	request.Header.Set("Authorization", "Bearer "+model.apiKey)
	request.Header.Set("Accept", "text/event-stream")
	request.Header.Set("Content-Type", contentType)
	client := *model.httpClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(request)
	if err != nil {
		yield(TranscriptionEvent{}, fmt.Errorf("glm: transcription stream: %w", err))
		return
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		data, _ := io.ReadAll(io.LimitReader(response.Body, 4<<10))
		yield(TranscriptionEvent{}, fmt.Errorf("glm: status %d: %s", response.StatusCode, model.redactRuntimeError(data)))
		return
	}
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 64<<10), maxRuntimeStreamEventBytes)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}
		if data == "[DONE]" {
			return
		}
		var event TranscriptionEvent
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			yield(TranscriptionEvent{}, fmt.Errorf("glm: decode transcription stream: %w", err))
			return
		}
		if !yield(event, nil) {
			return
		}
	}
	if err := scanner.Err(); err != nil {
		yield(TranscriptionEvent{}, fmt.Errorf("glm: read transcription stream: %w", err))
	}
}

func (model *Model) runtimeResponse(ctx context.Context, method, endpoint string, body io.Reader, contentType string, output any) error {
	if model == nil {
		return errors.New("glm: model is nil")
	}
	if ctx == nil {
		ctx = context.TODO()
	}
	callCtx, cancel := context.WithTimeout(ctx, model.timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(callCtx, method, endpoint, body)
	if err != nil {
		return fmt.Errorf("glm: create request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+model.apiKey)
	request.Header.Set("Accept", "application/json")
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	client := *model.httpClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("glm: request: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	encoded, err := io.ReadAll(io.LimitReader(response.Body, maxRuntimeResponseBytes+1))
	if err != nil {
		return fmt.Errorf("glm: read response: %w", err)
	}
	if len(encoded) > maxRuntimeResponseBytes {
		return errors.New("glm: response exceeds size limit")
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("glm: status %d: %s", response.StatusCode, model.redactRuntimeError(encoded))
	}
	if err := json.Unmarshal(encoded, output); err != nil {
		return fmt.Errorf("glm: decode response: %w", err)
	}
	return nil
}

func (model *Model) redactRuntimeError(encoded []byte) string {
	const limit = 4 << 10
	if len(encoded) > limit {
		encoded = encoded[:limit]
	}
	return strings.ReplaceAll(string(encoded), model.apiKey, "[redacted]")
}
