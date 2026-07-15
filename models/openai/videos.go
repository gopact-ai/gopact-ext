package openai

import (
	"context"
	"errors"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// VideoRequest configures a Sora video generation job. InputFile and
// InputReference are mutually exclusive.
type VideoRequest struct {
	Model          string          `json:"model,omitempty"`
	Prompt         string          `json:"prompt"`
	Seconds        string          `json:"seconds,omitempty"`
	Size           string          `json:"size,omitempty"`
	InputReference *ImageReference `json:"input_reference,omitempty"`
	InputFile      *FileContent    `json:"-"`
}

// Video is one asynchronous OpenAI video job.
type Video struct {
	ID                 string      `json:"id"`
	Object             string      `json:"object"`
	Model              string      `json:"model"`
	Status             string      `json:"status"`
	Progress           int         `json:"progress"`
	Prompt             string      `json:"prompt"`
	CreatedAt          int64       `json:"created_at"`
	CompletedAt        int64       `json:"completed_at"`
	ExpiresAt          int64       `json:"expires_at"`
	Seconds            string      `json:"seconds"`
	Size               string      `json:"size"`
	Quality            string      `json:"quality"`
	RemixedFromVideoID string      `json:"remixed_from_video_id"`
	Error              *VideoError `json:"error"`
}

// VideoError describes an asynchronous video generation failure.
type VideoError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// VideoListQuery configures video-job pagination.
type VideoListQuery struct {
	After string
	Limit int
	Order string
}

// VideoList is one page of generated videos.
type VideoList struct {
	Object  string  `json:"object"`
	Data    []Video `json:"data"`
	FirstID string  `json:"first_id"`
	LastID  string  `json:"last_id"`
	HasMore bool    `json:"has_more"`
}

// DeletedResource confirms deletion of a provider resource.
type DeletedResource struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Deleted bool   `json:"deleted"`
}

// VideoEditRequest configures a video edit. Set exactly one of VideoID and
// VideoFile.
type VideoEditRequest struct {
	Prompt    string
	VideoID   string
	VideoFile *FileContent
}

// VideoExtendRequest configures a video extension. Set exactly one of VideoID
// and VideoFile.
type VideoExtendRequest struct {
	Prompt    string
	Seconds   string
	VideoID   string
	VideoFile *FileContent
}

// VideoCharacterRequest creates a reusable video character from an upload.
type VideoCharacterRequest struct {
	Name  string
	Video FileContent
}

// VideoCharacter is a reusable character accepted by the video API.
type VideoCharacter struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt int64  `json:"created_at"`
}

// CreateVideo starts an asynchronous video generation job.
func (c *Model) CreateVideo(ctx context.Context, request VideoRequest) (Video, error) {
	if err := validateVideoRequest(request); err != nil {
		return Video{}, err
	}
	var response Video
	err := c.requestMultipartJSON(ctx, http.MethodPost, "/videos", func(writer *multipart.Writer) error {
		if request.InputFile != nil {
			if err := writeMultipartFile(writer, "input_reference", *request.InputFile); err != nil {
				return err
			}
		} else if request.InputReference != nil {
			fields := []struct{ name, value string }{
				{"input_reference[file_id]", request.InputReference.FileID},
				{"input_reference[image_url]", request.InputReference.ImageURL},
			}
			if err := writeMultipartFields(writer, fields); err != nil {
				return err
			}
		}
		return writeMultipartFields(writer, []struct{ name, value string }{
			{"model", request.Model}, {"prompt", request.Prompt},
			{"seconds", request.Seconds}, {"size", request.Size},
		})
	}, &response)
	return response, err
}

// GetVideo fetches the current metadata and status for a video job.
func (c *Model) GetVideo(ctx context.Context, videoID string) (Video, error) {
	if strings.TrimSpace(videoID) == "" {
		return Video{}, errors.New("openai: video id is required")
	}
	var response Video
	err := c.requestJSON(ctx, http.MethodGet, "/videos/"+url.PathEscape(videoID), nil, &response)
	return response, err
}

// ListVideos returns one page of video jobs for the current project.
func (c *Model) ListVideos(ctx context.Context, query VideoListQuery) (VideoList, error) {
	if query.Limit < 0 || query.Limit > 100 {
		return VideoList{}, errors.New("openai: video list limit must be between 1 and 100")
	}
	if query.Order != "" && query.Order != "asc" && query.Order != "desc" {
		return VideoList{}, errors.New("openai: video list order must be asc or desc")
	}
	values := url.Values{}
	if query.After != "" {
		values.Set("after", query.After)
	}
	if query.Limit > 0 {
		values.Set("limit", strconv.Itoa(query.Limit))
	}
	if query.Order != "" {
		values.Set("order", query.Order)
	}
	var response VideoList
	err := c.requestJSON(ctx, http.MethodGet, withQuery("/videos", values), nil, &response)
	return response, err
}

// DeleteVideo permanently deletes a completed or failed video and its assets.
func (c *Model) DeleteVideo(ctx context.Context, videoID string) (DeletedResource, error) {
	if strings.TrimSpace(videoID) == "" {
		return DeletedResource{}, errors.New("openai: video id is required")
	}
	var response DeletedResource
	err := c.requestJSON(ctx, http.MethodDelete, "/videos/"+url.PathEscape(videoID), nil, &response)
	return response, err
}

// DownloadVideo streams a generated video, thumbnail, or spritesheet. The caller
// must close the returned body.
func (c *Model) DownloadVideo(ctx context.Context, videoID, variant string) (Media, error) {
	if strings.TrimSpace(videoID) == "" {
		return Media{}, errors.New("openai: video id is required")
	}
	if variant != "" && variant != "video" && variant != "thumbnail" && variant != "spritesheet" {
		return Media{}, errors.New("openai: video download variant must be video, thumbnail, or spritesheet")
	}
	values := url.Values{}
	if variant != "" {
		values.Set("variant", variant)
	}
	path := "/videos/" + url.PathEscape(videoID) + "/content"
	return c.requestMedia(ctx, http.MethodGet, withQuery(path, values), nil, "", "application/binary")
}

// RemixVideo creates a new video job from a completed video and updated prompt.
func (c *Model) RemixVideo(ctx context.Context, videoID, prompt string) (Video, error) {
	if strings.TrimSpace(videoID) == "" {
		return Video{}, errors.New("openai: video id is required")
	}
	if strings.TrimSpace(prompt) == "" {
		return Video{}, errors.New("openai: video remix prompt is required")
	}
	var response Video
	err := c.requestJSON(ctx, http.MethodPost, "/videos/"+url.PathEscape(videoID)+"/remix", struct {
		Prompt string `json:"prompt"`
	}{Prompt: prompt}, &response)
	return response, err
}

// EditVideo creates a new video job by editing an upload or completed video ID.
func (c *Model) EditVideo(ctx context.Context, request VideoEditRequest) (Video, error) {
	if err := validateVideoSource(request.Prompt, request.VideoID, request.VideoFile); err != nil {
		return Video{}, err
	}
	var response Video
	err := c.requestMultipartJSON(ctx, http.MethodPost, "/videos/edits", func(writer *multipart.Writer) error {
		if request.VideoFile != nil {
			if err := writeMultipartFile(writer, "video", *request.VideoFile); err != nil {
				return err
			}
		} else if err := writer.WriteField("video[id]", request.VideoID); err != nil {
			return err
		}
		return writer.WriteField("prompt", request.Prompt)
	}, &response)
	return response, err
}

// ExtendVideo creates an extension job for an upload or completed video ID.
func (c *Model) ExtendVideo(ctx context.Context, request VideoExtendRequest) (Video, error) {
	if err := validateVideoSource(request.Prompt, request.VideoID, request.VideoFile); err != nil {
		return Video{}, err
	}
	if err := validateVideoSeconds(request.Seconds, true); err != nil {
		return Video{}, err
	}
	var response Video
	err := c.requestMultipartJSON(ctx, http.MethodPost, "/videos/extensions", func(writer *multipart.Writer) error {
		if request.VideoFile != nil {
			if err := writeMultipartFile(writer, "video", *request.VideoFile); err != nil {
				return err
			}
		} else if err := writer.WriteField("video[id]", request.VideoID); err != nil {
			return err
		}
		return writeMultipartFields(writer, []struct{ name, value string }{
			{"prompt", request.Prompt}, {"seconds", request.Seconds},
		})
	}, &response)
	return response, err
}

// CreateVideoCharacter creates a reusable video character from an upload.
func (c *Model) CreateVideoCharacter(
	ctx context.Context,
	request VideoCharacterRequest,
) (VideoCharacter, error) {
	if strings.TrimSpace(request.Name) == "" {
		return VideoCharacter{}, errors.New("openai: video character name is required")
	}
	if len(request.Video.Data) == 0 || strings.TrimSpace(request.Video.Filename) == "" {
		return VideoCharacter{}, errors.New("openai: video character file is required")
	}
	var response VideoCharacter
	err := c.requestMultipartJSON(ctx, http.MethodPost, "/videos/characters", func(writer *multipart.Writer) error {
		if err := writeMultipartFile(writer, "video", request.Video); err != nil {
			return err
		}
		return writer.WriteField("name", request.Name)
	}, &response)
	return response, err
}

// GetVideoCharacter fetches a reusable video character.
func (c *Model) GetVideoCharacter(ctx context.Context, characterID string) (VideoCharacter, error) {
	if strings.TrimSpace(characterID) == "" {
		return VideoCharacter{}, errors.New("openai: video character id is required")
	}
	var response VideoCharacter
	err := c.requestJSON(ctx, http.MethodGet, "/videos/characters/"+url.PathEscape(characterID), nil, &response)
	return response, err
}

func validateVideoRequest(request VideoRequest) error {
	if strings.TrimSpace(request.Prompt) == "" {
		return errors.New("openai: video prompt is required")
	}
	if request.InputFile != nil && request.InputReference != nil {
		return errors.New("openai: video accepts one input reference")
	}
	if request.InputReference != nil &&
		(request.InputReference.FileID == "") == (request.InputReference.ImageURL == "") {
		return errors.New("openai: video reference requires exactly one file id or image URL")
	}
	return validateVideoSeconds(request.Seconds, false)
}

func validateVideoSource(prompt, videoID string, videoFile *FileContent) error {
	if strings.TrimSpace(prompt) == "" {
		return errors.New("openai: video prompt is required")
	}
	if (strings.TrimSpace(videoID) == "") == (videoFile == nil) {
		return errors.New("openai: video request requires exactly one video id or file")
	}
	if videoFile != nil && (len(videoFile.Data) == 0 || strings.TrimSpace(videoFile.Filename) == "") {
		return errors.New("openai: video file is required")
	}
	return nil
}

func validateVideoSeconds(seconds string, required bool) error {
	if seconds == "" && !required {
		return nil
	}
	value, err := strconv.Atoi(seconds)
	if err != nil || value <= 0 {
		return errors.New("openai: video seconds must be a positive integer string")
	}
	return nil
}
