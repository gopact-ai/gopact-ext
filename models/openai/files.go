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

const (
	minFileExpirySeconds = 3_600
	maxFileExpirySeconds = 2_592_000
	maxFileListLimit     = 10_000
)

// FileUploadRequest uploads a file for Responses, vision, evals, or another
// OpenAI file-backed runtime. ExpiresAfterSeconds is optional.
type FileUploadRequest struct {
	File                FileContent
	Purpose             string
	ExpiresAfterSeconds int
}

// FileObject is metadata for one uploaded OpenAI file.
type FileObject struct {
	ID            string `json:"id"`
	Object        string `json:"object"`
	Bytes         int64  `json:"bytes"`
	CreatedAt     int64  `json:"created_at"`
	ExpiresAt     int64  `json:"expires_at"`
	Filename      string `json:"filename"`
	Purpose       string `json:"purpose"`
	Status        string `json:"status"`
	StatusDetails string `json:"status_details"`
}

// FileListQuery configures uploaded-file pagination.
type FileListQuery struct {
	After   string
	Limit   int
	Order   string
	Purpose string
}

// FileList is one page of uploaded files.
type FileList struct {
	Object  string       `json:"object"`
	Data    []FileObject `json:"data"`
	FirstID string       `json:"first_id"`
	LastID  string       `json:"last_id"`
	HasMore bool         `json:"has_more"`
}

// UploadFile uploads one file for use by OpenAI runtime APIs.
func (c *Model) UploadFile(ctx context.Context, request FileUploadRequest) (FileObject, error) {
	if strings.TrimSpace(request.Purpose) == "" {
		return FileObject{}, errors.New("openai: file purpose is required")
	}
	if len(request.File.Data) == 0 || strings.TrimSpace(request.File.Filename) == "" {
		return FileObject{}, errors.New("openai: upload file is required")
	}
	if request.ExpiresAfterSeconds != 0 &&
		(request.ExpiresAfterSeconds < minFileExpirySeconds || request.ExpiresAfterSeconds > maxFileExpirySeconds) {
		return FileObject{}, errors.New("openai: file expiry must be between 3600 and 2592000 seconds")
	}
	var response FileObject
	err := c.requestMultipartJSON(ctx, http.MethodPost, "/files", func(writer *multipart.Writer) error {
		if err := writeMultipartFile(writer, "file", request.File); err != nil {
			return err
		}
		fields := []struct{ name, value string }{{"purpose", request.Purpose}}
		if request.ExpiresAfterSeconds > 0 {
			fields = append(fields,
				struct{ name, value string }{"expires_after[anchor]", "created_at"},
				struct{ name, value string }{
					"expires_after[seconds]", strconv.Itoa(request.ExpiresAfterSeconds),
				},
			)
		}
		return writeMultipartFields(writer, fields)
	}, &response)
	return response, err
}

// GetFile returns metadata for one uploaded file.
func (c *Model) GetFile(ctx context.Context, fileID string) (FileObject, error) {
	if strings.TrimSpace(fileID) == "" {
		return FileObject{}, errors.New("openai: file id is required")
	}
	var response FileObject
	err := c.requestJSON(ctx, http.MethodGet, "/files/"+url.PathEscape(fileID), nil, &response)
	return response, err
}

// ListFiles returns one page of uploaded files.
func (c *Model) ListFiles(ctx context.Context, query FileListQuery) (FileList, error) {
	if query.Limit < 0 || query.Limit > maxFileListLimit {
		return FileList{}, errors.New("openai: file list limit must be between 1 and 10000")
	}
	if query.Order != "" && query.Order != "asc" && query.Order != "desc" {
		return FileList{}, errors.New("openai: file list order must be asc or desc")
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
	if query.Purpose != "" {
		values.Set("purpose", query.Purpose)
	}
	var response FileList
	err := c.requestJSON(ctx, http.MethodGet, withQuery("/files", values), nil, &response)
	return response, err
}

// DeleteFile deletes an uploaded file.
func (c *Model) DeleteFile(ctx context.Context, fileID string) (DeletedResource, error) {
	if strings.TrimSpace(fileID) == "" {
		return DeletedResource{}, errors.New("openai: file id is required")
	}
	var response DeletedResource
	err := c.requestJSON(ctx, http.MethodDelete, "/files/"+url.PathEscape(fileID), nil, &response)
	return response, err
}

// DownloadFile streams uploaded file content. The caller must close the body.
func (c *Model) DownloadFile(ctx context.Context, fileID string) (Media, error) {
	if strings.TrimSpace(fileID) == "" {
		return Media{}, errors.New("openai: file id is required")
	}
	call := runtimeCall{
		method: http.MethodGet, path: "/files/" + url.PathEscape(fileID) + "/content",
		accept: "application/binary",
	}
	return c.requestMedia(ctx, call)
}
