package agnes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const (
	maxRuntimeResponseBytes = 64 << 20
	maxRuntimeErrorBytes    = 4 << 10
	maxVideoFrames          = 441
	videoFrameStep          = 8
	maxVideoFrameRate       = 60
)

// ImageRequest configures Agnes text-to-image or image-to-image generation.
type ImageRequest struct {
	Model        string     `json:"model"`
	Prompt       string     `json:"prompt"`
	Size         string     `json:"size"`
	Ratio        string     `json:"ratio,omitempty"`
	ReturnBase64 bool       `json:"return_base64,omitempty"`
	Extra        ImageExtra `json:"extra_body,omitempty"`
}

// ImageExtra carries Agnes image editing and response controls.
type ImageExtra struct {
	Images         []string `json:"image,omitempty"`
	ResponseFormat string   `json:"response_format,omitempty"`
}

// ImageResponse is an Agnes image generation result.
type ImageResponse struct {
	Created int64         `json:"created"`
	Data    []ImageResult `json:"data"`
}

// ImageResult contains a URL or Base64-encoded generated image.
type ImageResult struct {
	URL           string `json:"url"`
	Base64        string `json:"b64_json"`
	RevisedPrompt string `json:"revised_prompt"`
}

// VideoRequest configures an Agnes video task.
type VideoRequest struct {
	Model             string     `json:"model"`
	Prompt            string     `json:"prompt"`
	Image             string     `json:"image,omitempty"`
	Mode              string     `json:"mode,omitempty"`
	Height            int        `json:"height,omitempty"`
	Width             int        `json:"width,omitempty"`
	NumFrames         int        `json:"num_frames,omitempty"`
	FrameRate         float64    `json:"frame_rate,omitempty"`
	NumInferenceSteps int        `json:"num_inference_steps,omitempty"`
	Seed              int64      `json:"seed,omitempty"`
	NegativePrompt    string     `json:"negative_prompt,omitempty"`
	Extra             VideoExtra `json:"extra_body,omitempty"`
}

// VideoExtra carries Agnes keyframe inputs.
type VideoExtra struct {
	Images []string `json:"image,omitempty"`
	Mode   string   `json:"mode,omitempty"`
}

// VideoTask is an Agnes video creation or polling result.
type VideoTask struct {
	ID        string          `json:"id"`
	TaskID    string          `json:"task_id"`
	VideoID   string          `json:"video_id"`
	Object    string          `json:"object"`
	Model     string          `json:"model"`
	Status    string          `json:"status"`
	Progress  int             `json:"progress"`
	CreatedAt int64           `json:"created_at"`
	Seconds   string          `json:"seconds"`
	Size      string          `json:"size"`
	URL       string          `json:"url"`
	Error     json.RawMessage `json:"error"`
}

// GenerateImage calls Agnes image generation and editing.
func (model *Model) GenerateImage(ctx context.Context, request ImageRequest) (ImageResponse, error) {
	if model == nil {
		return ImageResponse{}, errors.New("agnes: model is nil")
	}
	if request.Model == "" {
		request.Model = DefaultImageModel
	}
	if strings.TrimSpace(request.Prompt) == "" {
		return ImageResponse{}, errors.New("agnes: image prompt is required")
	}
	if strings.TrimSpace(request.Size) == "" {
		return ImageResponse{}, errors.New("agnes: image size is required")
	}
	if format := request.Extra.ResponseFormat; format != "" && format != "url" && format != "b64_json" {
		return ImageResponse{}, errors.New("agnes: image response format must be url or b64_json")
	}
	var response ImageResponse
	err := model.requestJSON(ctx, http.MethodPost, model.baseURL+"/images/generations", request, &response)
	return response, err
}

// CreateVideo starts an asynchronous Agnes video generation task.
func (model *Model) CreateVideo(ctx context.Context, request VideoRequest) (VideoTask, error) {
	if model == nil {
		return VideoTask{}, errors.New("agnes: model is nil")
	}
	if request.Model == "" {
		request.Model = DefaultVideoModel
	}
	if strings.TrimSpace(request.Prompt) == "" {
		return VideoTask{}, errors.New("agnes: video prompt is required")
	}
	if request.NumFrames < 0 || request.NumFrames > maxVideoFrames || request.NumFrames > 0 && (request.NumFrames-1)%videoFrameStep != 0 {
		return VideoTask{}, errors.New("agnes: video frames must be at most 441 and follow 8n+1")
	}
	if request.FrameRate < 0 || request.FrameRate > maxVideoFrameRate {
		return VideoTask{}, errors.New("agnes: video frame rate must be between 1 and 60")
	}
	if request.Height < 0 || request.Width < 0 || request.NumInferenceSteps < 0 {
		return VideoTask{}, errors.New("agnes: video dimensions and inference steps must not be negative")
	}
	var task VideoTask
	err := model.requestJSON(ctx, http.MethodPost, model.baseURL+"/videos", request, &task)
	return task, err
}

// Video gets an Agnes video result by the recommended video ID.
func (model *Model) Video(ctx context.Context, videoID, modelName string) (VideoTask, error) {
	if model == nil {
		return VideoTask{}, errors.New("agnes: model is nil")
	}
	if strings.TrimSpace(videoID) == "" {
		return VideoTask{}, errors.New("agnes: video id is required")
	}
	endpoint, err := url.Parse(model.runtimeRoot() + "/agnesapi")
	if err != nil {
		return VideoTask{}, fmt.Errorf("agnes: create video URL: %w", err)
	}
	query := endpoint.Query()
	query.Set("video_id", videoID)
	if modelName != "" {
		query.Set("model_name", modelName)
	}
	endpoint.RawQuery = query.Encode()
	var task VideoTask
	err = model.requestJSON(ctx, http.MethodGet, endpoint.String(), nil, &task)
	return task, err
}

// LegacyVideo gets an Agnes video result by a legacy task ID.
func (model *Model) LegacyVideo(ctx context.Context, taskID string) (VideoTask, error) {
	if model == nil {
		return VideoTask{}, errors.New("agnes: model is nil")
	}
	if strings.TrimSpace(taskID) == "" {
		return VideoTask{}, errors.New("agnes: video task id is required")
	}
	var task VideoTask
	err := model.requestJSON(ctx, http.MethodGet, model.baseURL+"/videos/"+url.PathEscape(taskID), nil, &task)
	return task, err
}

func (model *Model) runtimeRoot() string {
	return strings.TrimSuffix(model.baseURL, "/v1")
}

func (model *Model) requestJSON(ctx context.Context, method, endpoint string, input, output any) error {
	if model == nil {
		return errors.New("agnes: model is nil")
	}
	if ctx == nil {
		ctx = context.TODO()
	}
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return fmt.Errorf("agnes: encode request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	callCtx, cancel := context.WithTimeout(ctx, model.timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(callCtx, method, endpoint, body)
	if err != nil {
		return fmt.Errorf("agnes: create request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+model.apiKey)
	request.Header.Set("Accept", "application/json")
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	client := *model.httpClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("agnes: request: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	encoded, err := io.ReadAll(io.LimitReader(response.Body, maxRuntimeResponseBytes+1))
	if err != nil {
		return fmt.Errorf("agnes: read response: %w", err)
	}
	if len(encoded) > maxRuntimeResponseBytes {
		return errors.New("agnes: response exceeds size limit")
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("agnes: status %d: %s", response.StatusCode, model.redactRuntimeError(encoded))
	}
	if err := json.Unmarshal(encoded, output); err != nil {
		return fmt.Errorf("agnes: decode response: %w", err)
	}
	return nil
}

func (model *Model) redactRuntimeError(encoded []byte) string {
	if len(encoded) > maxRuntimeErrorBytes {
		encoded = encoded[:maxRuntimeErrorBytes]
	}
	return strings.ReplaceAll(string(encoded), model.apiKey, "[redacted]")
}
