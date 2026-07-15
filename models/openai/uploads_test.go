package openai

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

func TestMultipartUploadLifecycle(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %q", r.Method)
		}
		switch r.URL.Path {
		case "/uploads":
			var request map[string]any
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode create request: %v", err)
			}
			if request["filename"] != "large.jsonl" || request["mime_type"] != "application/jsonl" || request["purpose"] != "user_data" {
				t.Errorf("create request = %+v", request)
			}
			expires, _ := request["expires_after"].(map[string]any)
			if expires["anchor"] != "created_at" || expires["seconds"] != float64(3600) {
				t.Errorf("expires_after = %+v", expires)
			}
			_, _ = w.Write([]byte(`{"id":"upload-1","status":"pending","bytes":4}`))
		case "/uploads/upload-1/parts":
			if err := r.ParseMultipartForm(maxUploadPartBytes + 1); err != nil {
				t.Errorf("parse multipart: %v", err)
			}
			file, _, err := r.FormFile("data")
			if err != nil {
				t.Errorf("data part: %v", err)
				return
			}
			defer func() { _ = file.Close() }()
			data, _ := io.ReadAll(file)
			if string(data) != "part" {
				t.Errorf("part data = %q", data)
			}
			_, _ = w.Write([]byte(`{"id":"part-1","object":"upload.part","upload_id":"upload-1"}`))
		case "/uploads/upload-1/complete":
			var request UploadCompleteRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode complete request: %v", err)
			}
			if !slices.Equal(request.PartIDs, []string{"part-1"}) || request.MD5 != "checksum" {
				t.Errorf("complete request = %+v", request)
			}
			_, _ = w.Write([]byte(`{"id":"upload-1","status":"completed","file":{"id":"file-1"}}`))
		case "/uploads/upload-2/cancel":
			_, _ = w.Write([]byte(`{"id":"upload-2","status":"cancelled"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	model := newCapabilityTestModel(t, server.URL)
	upload, err := model.CreateUpload(t.Context(), UploadRequest{
		Bytes: 4, Filename: "large.jsonl", MIMEType: "application/jsonl",
		Purpose: "user_data", ExpiresAfterSeconds: 3600,
	})
	if err != nil || upload.ID != "upload-1" {
		t.Fatalf("CreateUpload() = %+v, %v", upload, err)
	}
	part, err := model.AddUploadPart(t.Context(), upload.ID, []byte("part"))
	if err != nil || part.ID != "part-1" {
		t.Fatalf("AddUploadPart() = %+v, %v", part, err)
	}
	completed, err := model.CompleteUpload(t.Context(), upload.ID, UploadCompleteRequest{
		PartIDs: []string{part.ID}, MD5: "checksum",
	})
	if err != nil || completed.File == nil || completed.File.ID != "file-1" {
		t.Fatalf("CompleteUpload() = %+v, %v", completed, err)
	}
	cancelled, err := model.CancelUpload(t.Context(), "upload-2")
	if err != nil || cancelled.Status != "cancelled" {
		t.Fatalf("CancelUpload() = %+v, %v", cancelled, err)
	}
}

func TestMultipartUploadValidation(t *testing.T) {
	model := &Model{}
	if _, err := model.CreateUpload(t.Context(), UploadRequest{}); err == nil {
		t.Fatal("CreateUpload() error = nil")
	}
	if _, err := model.AddUploadPart(t.Context(), "upload-1", nil); err == nil {
		t.Fatal("AddUploadPart() error = nil")
	}
	if _, err := model.CompleteUpload(t.Context(), "upload-1", UploadCompleteRequest{}); err == nil {
		t.Fatal("CompleteUpload() error = nil")
	}
	if _, err := model.CancelUpload(t.Context(), ""); err == nil {
		t.Fatal("CancelUpload() error = nil")
	}
}
