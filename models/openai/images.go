package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
)

// ImageRequest configures text-to-image generation.
type ImageRequest struct {
	Model             string `json:"model,omitempty"`
	Prompt            string `json:"prompt"`
	Background        string `json:"background,omitempty"`
	Moderation        string `json:"moderation,omitempty"`
	N                 int    `json:"n,omitempty"`
	OutputCompression *int   `json:"output_compression,omitempty"`
	OutputFormat      string `json:"output_format,omitempty"`
	PartialImages     *int   `json:"partial_images,omitempty"`
	Quality           string `json:"quality,omitempty"`
	ResponseFormat    string `json:"response_format,omitempty"`
	Size              string `json:"size,omitempty"`
	Style             string `json:"style,omitempty"`
	User              string `json:"user,omitempty"`
}

// ImageEditRequest configures an image edit using one or more uploads.
type ImageEditRequest struct {
	Model             string
	Prompt            string
	Images            []FileContent
	Mask              *FileContent
	Background        string
	InputFidelity     string
	N                 int
	OutputCompression *int
	OutputFormat      string
	PartialImages     *int
	Quality           string
	ResponseFormat    string
	Size              string
	User              string
}

// ImageReference identifies an image by a URL or an uploaded OpenAI file ID.
type ImageReference struct {
	ImageURL string `json:"image_url,omitempty"`
	FileID   string `json:"file_id,omitempty"`
}

// ImageVariationRequest configures the legacy DALL-E 2 variation endpoint.
type ImageVariationRequest struct {
	Image          FileContent
	Model          string
	N              int
	ResponseFormat string
	Size           string
	User           string
}

// ImageResponse is an OpenAI image generation or edit result.
type ImageResponse struct {
	Created      int64         `json:"created"`
	Background   string        `json:"background"`
	Data         []ImageResult `json:"data"`
	OutputFormat string        `json:"output_format"`
	Quality      string        `json:"quality"`
	Size         string        `json:"size"`
	Usage        ImageUsage    `json:"usage"`
}

// ImageResult contains one generated image URL or Base64 payload.
type ImageResult struct {
	Base64JSON    string `json:"b64_json"`
	RevisedPrompt string `json:"revised_prompt"`
	URL           string `json:"url"`
}

// ImageUsage reports token use for GPT Image models.
type ImageUsage struct {
	InputTokens        int `json:"input_tokens"`
	OutputTokens       int `json:"output_tokens"`
	TotalTokens        int `json:"total_tokens"`
	InputTokensDetails struct {
		ImageTokens int `json:"image_tokens"`
		TextTokens  int `json:"text_tokens"`
	} `json:"input_tokens_details"`
	OutputTokensDetails struct {
		ImageTokens int `json:"image_tokens"`
		TextTokens  int `json:"text_tokens"`
	} `json:"output_tokens_details"`
}

// ImageEvent is one raw image-generation or image-edit SSE event.
type ImageEvent struct {
	Type string
	Data json.RawMessage
}

// GenerateImage creates images from a prompt.
func (c *Model) GenerateImage(ctx context.Context, request ImageRequest) (ImageResponse, error) {
	if err := validateImageRequest(request); err != nil {
		return ImageResponse{}, err
	}
	var response ImageResponse
	err := c.requestJSON(ctx, http.MethodPost, "/images/generations", request, &response)
	return response, err
}

// StreamImage streams partial and completed image-generation events.
func (c *Model) StreamImage(ctx context.Context, request ImageRequest) iter.Seq2[ImageEvent, error] {
	return func(yield func(ImageEvent, error) bool) {
		if err := validateImageRequest(request); err != nil {
			yield(ImageEvent{}, err)
			return
		}
		payload := struct {
			ImageRequest
			Stream bool `json:"stream"`
		}{ImageRequest: request, Stream: true}
		encoded, err := json.Marshal(payload)
		if err != nil {
			yield(ImageEvent{}, fmt.Errorf("openai: encode image stream request: %w", err))
			return
		}
		yieldImageEvents(yield, c.streamJSON(ctx, "/images/generations", encoded, "application/json"))
	}
}

// EditImage edits one or more uploaded or referenced images.
func (c *Model) EditImage(ctx context.Context, request ImageEditRequest) (ImageResponse, error) {
	if err := validateImageEditRequest(request); err != nil {
		return ImageResponse{}, err
	}
	var response ImageResponse
	err := c.requestMultipartJSON(ctx, http.MethodPost, "/images/edits", func(writer *multipart.Writer) error {
		return writeImageEditMultipart(writer, request, false)
	}, &response)
	return response, err
}

// StreamImageEdit streams partial and completed image-edit events.
func (c *Model) StreamImageEdit(ctx context.Context, request ImageEditRequest) iter.Seq2[ImageEvent, error] {
	return func(yield func(ImageEvent, error) bool) {
		if err := validateImageEditRequest(request); err != nil {
			yield(ImageEvent{}, err)
			return
		}
		encoded, contentType, err := encodeMultipart(func(writer *multipart.Writer) error {
			return writeImageEditMultipart(writer, request, true)
		})
		if err != nil {
			yield(ImageEvent{}, err)
			return
		}
		yieldImageEvents(yield, c.streamJSON(ctx, "/images/edits", encoded, contentType))
	}
}

// CreateImageVariation creates DALL-E 2 variations of one uploaded PNG.
func (c *Model) CreateImageVariation(
	ctx context.Context,
	request ImageVariationRequest,
) (ImageResponse, error) {
	if len(request.Image.Data) == 0 || strings.TrimSpace(request.Image.Filename) == "" {
		return ImageResponse{}, errors.New("openai: variation image is required")
	}
	if request.N < 0 || request.N > 10 {
		return ImageResponse{}, errors.New("openai: image count must be between 1 and 10")
	}
	var response ImageResponse
	err := c.requestMultipartJSON(ctx, http.MethodPost, "/images/variations", func(writer *multipart.Writer) error {
		if err := writeMultipartFile(writer, "image", request.Image); err != nil {
			return err
		}
		fields := []struct{ name, value string }{
			{"model", request.Model}, {"response_format", request.ResponseFormat},
			{"size", request.Size}, {"user", request.User},
		}
		if request.N > 0 {
			fields = append(fields, struct{ name, value string }{"n", strconv.Itoa(request.N)})
		}
		return writeMultipartFields(writer, fields)
	}, &response)
	return response, err
}

func validateImageRequest(request ImageRequest) error {
	if strings.TrimSpace(request.Prompt) == "" {
		return errors.New("openai: image prompt is required")
	}
	return validateImageOptions(request.N, request.OutputCompression, request.PartialImages)
}

func validateImageEditRequest(request ImageEditRequest) error {
	if strings.TrimSpace(request.Prompt) == "" {
		return errors.New("openai: image edit prompt is required")
	}
	if len(request.Images) == 0 {
		return errors.New("openai: image edit requires at least one image")
	}
	if len(request.Images) > 16 {
		return errors.New("openai: image edit accepts at most 16 images")
	}
	return validateImageOptions(request.N, request.OutputCompression, request.PartialImages)
}

func validateImageOptions(count int, compression, partialImages *int) error {
	if count < 0 || count > 10 {
		return errors.New("openai: image count must be between 1 and 10")
	}
	if compression != nil && (*compression < 0 || *compression > 100) {
		return errors.New("openai: image compression must be between 0 and 100")
	}
	if partialImages != nil && (*partialImages < 0 || *partialImages > 3) {
		return errors.New("openai: partial image count must be between 0 and 3")
	}
	return nil
}

func writeImageEditMultipart(writer *multipart.Writer, request ImageEditRequest, stream bool) error {
	field := "image"
	if len(request.Images) > 1 {
		field = "image[]"
	}
	for _, image := range request.Images {
		if err := writeMultipartFile(writer, field, image); err != nil {
			return err
		}
	}
	if request.Mask != nil {
		if err := writeMultipartFile(writer, "mask", *request.Mask); err != nil {
			return err
		}
	}
	fields := []struct{ name, value string }{
		{"model", request.Model}, {"prompt", request.Prompt}, {"background", request.Background},
		{"input_fidelity", request.InputFidelity}, {"output_format", request.OutputFormat},
		{"quality", request.Quality}, {"response_format", request.ResponseFormat},
		{"size", request.Size}, {"user", request.User},
	}
	if request.N > 0 {
		fields = append(fields, struct{ name, value string }{"n", strconv.Itoa(request.N)})
	}
	if request.OutputCompression != nil {
		fields = append(fields, struct{ name, value string }{"output_compression", strconv.Itoa(*request.OutputCompression)})
	}
	if request.PartialImages != nil {
		fields = append(fields, struct{ name, value string }{"partial_images", strconv.Itoa(*request.PartialImages)})
	}
	if stream {
		fields = append(fields, struct{ name, value string }{"stream", "true"})
	}
	return writeMultipartFields(writer, fields)
}

func writeMultipartFields(writer *multipart.Writer, fields []struct{ name, value string }) error {
	for _, field := range fields {
		if field.value == "" {
			continue
		}
		if err := writer.WriteField(field.name, field.value); err != nil {
			return err
		}
	}
	return nil
}

func yieldImageEvents(yield func(ImageEvent, error) bool, events iter.Seq2[runtimeEvent, error]) {
	for event, err := range events {
		if !yield(ImageEvent(event), err) {
			return
		}
	}
}
