package openai

import (
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
)

const (
	maxUploadBytes     = 8 << 30
	maxUploadPartBytes = 64 << 20
)

// UploadRequest starts a temporary multipart upload for a large file.
type UploadRequest struct {
	Bytes               int64  `json:"bytes"`
	Filename            string `json:"filename"`
	MIMEType            string `json:"mime_type"`
	Purpose             string `json:"purpose"`
	ExpiresAfterSeconds int    `json:"-"`
}

// MarshalJSON preserves OpenAI's nested expiration policy without exposing an
// otherwise fixed anchor to callers.
func (request UploadRequest) MarshalJSON() ([]byte, error) {
	type wire struct {
		Bytes        int64             `json:"bytes"`
		Filename     string            `json:"filename"`
		MIMEType     string            `json:"mime_type"`
		Purpose      string            `json:"purpose"`
		ExpiresAfter *expirationPolicy `json:"expires_after,omitempty"`
	}
	encoded := wire{
		Bytes: request.Bytes, Filename: request.Filename, MIMEType: request.MIMEType,
		Purpose: request.Purpose,
	}
	if request.ExpiresAfterSeconds > 0 {
		encoded.ExpiresAfter = &expirationPolicy{
			Anchor: "created_at", Seconds: request.ExpiresAfterSeconds,
		}
	}
	return json.Marshal(encoded)
}

// Upload is the temporary object used to assemble a large OpenAI file.
type Upload struct {
	ID        string      `json:"id"`
	Object    string      `json:"object"`
	Bytes     int64       `json:"bytes"`
	CreatedAt int64       `json:"created_at"`
	ExpiresAt int64       `json:"expires_at"`
	Filename  string      `json:"filename"`
	Purpose   string      `json:"purpose"`
	Status    string      `json:"status"`
	File      *FileObject `json:"file"`
}

// UploadPart is one byte chunk attached to an Upload.
type UploadPart struct {
	ID        string `json:"id"`
	Object    string `json:"object"`
	CreatedAt int64  `json:"created_at"`
	UploadID  string `json:"upload_id"`
}

// UploadCompleteRequest selects part order and optionally verifies the result.
type UploadCompleteRequest struct {
	PartIDs []string `json:"part_ids"`
	MD5     string   `json:"md5,omitempty"`
}

// CreateUpload starts a temporary multipart upload.
func (c *Model) CreateUpload(ctx context.Context, request UploadRequest) (Upload, error) {
	if strings.TrimSpace(request.Filename) == "" {
		return Upload{}, errors.New("openai: upload filename is required")
	}
	if strings.TrimSpace(request.MIMEType) == "" {
		return Upload{}, errors.New("openai: upload mime type is required")
	}
	if strings.TrimSpace(request.Purpose) == "" {
		return Upload{}, errors.New("openai: upload purpose is required")
	}
	if request.Bytes < 0 || request.Bytes > maxUploadBytes {
		return Upload{}, errors.New("openai: upload bytes must be between 0 and 8 gb")
	}
	if request.ExpiresAfterSeconds != 0 &&
		(request.ExpiresAfterSeconds < minFileExpirySeconds || request.ExpiresAfterSeconds > maxFileExpirySeconds) {
		return Upload{}, errors.New("openai: upload expiry must be between 3600 and 2592000 seconds")
	}
	var response Upload
	err := c.requestJSON(ctx, http.MethodPost, "/uploads", request, &response)
	return response, err
}

// AddUploadPart attaches one chunk of at most 64 MiB to an Upload.
func (c *Model) AddUploadPart(ctx context.Context, uploadID string, data []byte) (UploadPart, error) {
	if strings.TrimSpace(uploadID) == "" {
		return UploadPart{}, errors.New("openai: upload id is required")
	}
	if len(data) == 0 || len(data) > maxUploadPartBytes {
		return UploadPart{}, errors.New("openai: upload part must contain between 1 byte and 64 mib")
	}
	var response UploadPart
	path := "/uploads/" + url.PathEscape(uploadID) + "/parts"
	err := c.requestMultipartJSON(ctx, http.MethodPost, path, func(writer *multipart.Writer) error {
		return writeMultipartFile(writer, "data", FileContent{
			Filename: "part", ContentType: "application/octet-stream", Data: data,
		})
	}, &response)
	return response, err
}

// CompleteUpload assembles uploaded parts in the supplied order and creates a File.
func (c *Model) CompleteUpload(ctx context.Context, uploadID string, request UploadCompleteRequest) (Upload, error) {
	if strings.TrimSpace(uploadID) == "" {
		return Upload{}, errors.New("openai: upload id is required")
	}
	if len(request.PartIDs) == 0 {
		return Upload{}, errors.New("openai: upload part ids are required")
	}
	for _, partID := range request.PartIDs {
		if strings.TrimSpace(partID) == "" {
			return Upload{}, errors.New("openai: upload part id is required")
		}
	}
	var response Upload
	path := "/uploads/" + url.PathEscape(uploadID) + "/complete"
	err := c.requestJSON(ctx, http.MethodPost, path, request, &response)
	return response, err
}

// CancelUpload prevents further parts from being added to an Upload.
func (c *Model) CancelUpload(ctx context.Context, uploadID string) (Upload, error) {
	if strings.TrimSpace(uploadID) == "" {
		return Upload{}, errors.New("openai: upload id is required")
	}
	var response Upload
	path := "/uploads/" + url.PathEscape(uploadID) + "/cancel"
	err := c.requestJSON(ctx, http.MethodPost, path, nil, &response)
	return response, err
}

type expirationPolicy struct {
	Anchor  string `json:"anchor"`
	Seconds int    `json:"seconds"`
}
