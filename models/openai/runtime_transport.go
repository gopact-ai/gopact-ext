package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
)

const maxRuntimeJSONResponseBytes = 128 << 20

type runtimeEvent struct {
	Type string
	Data json.RawMessage
}

// FileContent is an in-memory file sent to a provider multipart endpoint.
type FileContent struct {
	Filename    string
	ContentType string
	Data        []byte
}

// Media is a streamed binary provider response. The caller must close Body.
type Media struct {
	Body          io.ReadCloser
	ContentType   string
	ContentLength int64
	Header        http.Header
}

func (c *Model) requestJSON(ctx context.Context, method, path string, input, output any) error {
	if c == nil {
		return errors.New("openai: model is nil")
	}
	var encoded []byte
	var err error
	if input != nil {
		encoded, err = json.Marshal(input)
		if err != nil {
			return fmt.Errorf("openai: encode request: %w", err)
		}
	}
	return c.requestEncodedJSON(ctx, method, path, encoded, "application/json", output)
}

func (c *Model) requestMultipartJSON(
	ctx context.Context,
	method, path string,
	write func(*multipart.Writer) error,
	output any,
) error {
	encoded, contentType, err := encodeMultipart(write)
	if err != nil {
		return err
	}
	return c.requestEncodedJSON(ctx, method, path, encoded, contentType, output)
}

func (c *Model) requestEncodedJSON(
	ctx context.Context,
	method, path string,
	body []byte,
	contentType string,
	output any,
) error {
	if c == nil {
		return errors.New("openai: model is nil")
	}
	callCtx, cancel := c.callContext(ctx)
	defer cancel()
	request, err := c.newRuntimeRequest(callCtx, method, path, body, contentType, "application/json")
	if err != nil {
		return err
	}
	response, err := c.do(request)
	if err != nil {
		return fmt.Errorf("openai: request: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	encoded, err := io.ReadAll(io.LimitReader(response.Body, maxRuntimeJSONResponseBytes+1))
	if err != nil {
		return fmt.Errorf("openai: read response: %w", err)
	}
	if len(encoded) > maxRuntimeJSONResponseBytes {
		return errors.New("openai: response exceeds size limit")
	}
	if !successfulStatus(response.StatusCode) {
		return Error{
			StatusCode: response.StatusCode,
			Body:       c.redact(bounded(strings.TrimSpace(string(encoded)))),
			Retryable:  retryableStatus(response.StatusCode),
		}
	}
	if output == nil || len(bytes.TrimSpace(encoded)) == 0 {
		return nil
	}
	if err := json.Unmarshal(encoded, output); err != nil {
		return fmt.Errorf("openai: decode response: %w", err)
	}
	return nil
}

func (c *Model) requestMedia(
	ctx context.Context,
	method, path string,
	body []byte,
	contentType, accept string,
) (Media, error) {
	if c == nil {
		return Media{}, errors.New("openai: model is nil")
	}
	callCtx, cancel := c.callContext(ctx)
	request, err := c.newRuntimeRequest(callCtx, method, path, body, contentType, accept)
	if err != nil {
		cancel()
		return Media{}, err
	}
	response, err := c.do(request)
	if err != nil {
		cancel()
		return Media{}, fmt.Errorf("openai: request: %w", err)
	}
	if !successfulStatus(response.StatusCode) {
		defer func() { _ = response.Body.Close() }()
		defer cancel()
		message, _ := io.ReadAll(io.LimitReader(response.Body, maxTextBytes))
		return Media{}, Error{
			StatusCode: response.StatusCode,
			Body:       c.redact(bounded(strings.TrimSpace(string(message)))),
			Retryable:  retryableStatus(response.StatusCode),
		}
	}
	return Media{
		Body:          cancelReadCloser{ReadCloser: response.Body, cancel: cancel},
		ContentType:   response.Header.Get("Content-Type"),
		ContentLength: response.ContentLength,
		Header:        response.Header.Clone(),
	}, nil
}

func (c *Model) requestMultipartText(
	ctx context.Context,
	path string,
	write func(*multipart.Writer) error,
) ([]byte, error) {
	if c == nil {
		return nil, errors.New("openai: model is nil")
	}
	encoded, contentType, err := encodeMultipart(write)
	if err != nil {
		return nil, err
	}
	callCtx, cancel := c.callContext(ctx)
	defer cancel()
	request, err := c.newRuntimeRequest(callCtx, http.MethodPost, path, encoded, contentType, "text/plain")
	if err != nil {
		return nil, err
	}
	response, err := c.do(request)
	if err != nil {
		return nil, fmt.Errorf("openai: request: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxRuntimeJSONResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("openai: read response: %w", err)
	}
	if len(data) > maxRuntimeJSONResponseBytes {
		return nil, errors.New("openai: response exceeds size limit")
	}
	if !successfulStatus(response.StatusCode) {
		return nil, Error{
			StatusCode: response.StatusCode,
			Body:       c.redact(bounded(strings.TrimSpace(string(data)))),
			Retryable:  retryableStatus(response.StatusCode),
		}
	}
	return data, nil
}

func (c *Model) streamJSON(
	ctx context.Context,
	path string,
	body []byte,
	contentType string,
) iter.Seq2[runtimeEvent, error] {
	return c.streamEncodedJSON(ctx, http.MethodPost, path, body, contentType)
}

func (c *Model) streamEncodedJSON(
	ctx context.Context,
	method, path string,
	body []byte,
	contentType string,
) iter.Seq2[runtimeEvent, error] {
	return func(yield func(runtimeEvent, error) bool) {
		if c == nil {
			yield(runtimeEvent{}, errors.New("openai: model is nil"))
			return
		}
		callCtx, cancel := c.callContext(ctx)
		defer cancel()
		request, err := c.newRuntimeRequest(callCtx, method, path, body, contentType, "text/event-stream")
		if err != nil {
			yield(runtimeEvent{}, err)
			return
		}
		response, err := c.do(request)
		if err != nil {
			yield(runtimeEvent{}, fmt.Errorf("openai: stream request: %w", err))
			return
		}
		defer func() { _ = response.Body.Close() }()
		if !successfulStatus(response.StatusCode) {
			message, _ := io.ReadAll(io.LimitReader(response.Body, maxTextBytes))
			yield(runtimeEvent{}, Error{
				StatusCode: response.StatusCode,
				Body:       c.redact(bounded(strings.TrimSpace(string(message)))),
				Retryable:  retryableStatus(response.StatusCode),
			})
			return
		}

		scanner := bufio.NewScanner(response.Body)
		scanner.Buffer(make([]byte, 64<<10), maxStreamFrameBytes)
		var eventType string
		var data strings.Builder
		emit := func() bool {
			payload := strings.TrimSpace(data.String())
			data.Reset()
			if payload == "" {
				eventType = ""
				return true
			}
			if payload == "[DONE]" {
				return false
			}
			raw := json.RawMessage(append([]byte(nil), payload...))
			if !json.Valid(raw) {
				yield(runtimeEvent{}, errors.New("openai: stream event is invalid JSON"))
				return false
			}
			var header struct {
				Type string `json:"type"`
			}
			_ = json.Unmarshal(raw, &header)
			if header.Type == "" {
				header.Type = eventType
			}
			eventType = ""
			return yield(runtimeEvent{Type: header.Type, Data: raw}, nil)
		}
		for scanner.Scan() {
			line := scanner.Text()
			switch {
			case line == "":
				if !emit() {
					return
				}
			case strings.HasPrefix(line, "event:"):
				eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			case strings.HasPrefix(line, "data:"):
				if data.Len() > 0 {
					data.WriteByte('\n')
				}
				data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			}
		}
		if err := scanner.Err(); err != nil {
			yield(runtimeEvent{}, fmt.Errorf("openai: read stream: %w", err))
			return
		}
		if data.Len() > 0 {
			emit()
		}
	}
}

func (c *Model) newRuntimeRequest(
	ctx context.Context,
	method, path string,
	body []byte,
	contentType, accept string,
) (*http.Request, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return nil, fmt.Errorf("openai: create request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+c.apiKey)
	if accept != "" {
		request.Header.Set("Accept", accept)
	}
	if body != nil && contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	return request, nil
}

func encodeMultipart(write func(*multipart.Writer) error) ([]byte, string, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := write(writer); err != nil {
		_ = writer.Close()
		return nil, "", fmt.Errorf("openai: encode multipart request: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, "", fmt.Errorf("openai: encode multipart request: %w", err)
	}
	return body.Bytes(), writer.FormDataContentType(), nil
}

func writeMultipartFile(writer *multipart.Writer, field string, file FileContent) error {
	if strings.TrimSpace(file.Filename) == "" {
		return errors.New("file name is required")
	}
	if len(file.Data) == 0 {
		return errors.New("file data is required")
	}
	contentType := file.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	disposition := mime.FormatMediaType("form-data", map[string]string{
		"name": field, "filename": file.Filename,
	})
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", disposition)
	header.Set("Content-Type", contentType)
	part, err := writer.CreatePart(header)
	if err != nil {
		return err
	}
	_, err = part.Write(file.Data)
	return err
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
