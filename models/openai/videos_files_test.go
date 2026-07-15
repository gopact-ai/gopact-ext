package openai

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestVideoRuntime(t *testing.T) {
	routes := map[string]http.HandlerFunc{
		"POST /videos": func(w http.ResponseWriter, r *http.Request) {
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Errorf("parse video request: %v", err)
			}
			if r.FormValue("prompt") != "cat piano" || r.FormValue("model") != "sora-2" ||
				r.FormValue("input_reference[file_id]") != "file_1" {
				t.Errorf("video request = %#v", r.MultipartForm.Value)
			}
			_, _ = io.WriteString(w, `{"id":"video_1","object":"video","model":"sora-2","status":"queued","progress":0}`)
		},
		"GET /videos": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("limit") != "5" || r.URL.Query().Get("order") != "desc" {
				t.Errorf("video query = %q", r.URL.RawQuery)
			}
			_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"video_1","status":"completed"}],"has_more":false}`)
		},
		"GET /videos/video_1": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"id":"video_1","status":"completed","progress":100}`)
		},
		"DELETE /videos/video_1": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"id":"video_1","object":"video.deleted","deleted":true}`)
		},
		"GET /videos/video_1/content": func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Accept") != "application/binary" {
				t.Errorf("Accept = %q", r.Header.Get("Accept"))
			}
			if r.URL.Query().Get("variant") != "thumbnail" {
				t.Errorf("variant = %q", r.URL.Query().Get("variant"))
			}
			w.Header().Set("Content-Type", "image/webp")
			_, _ = w.Write([]byte("thumb"))
		},
		"POST /videos/video_1/remix": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"id":"video_2","status":"queued","remixed_from_video_id":"video_1"}`)
		},
		"POST /videos/edits": func(w http.ResponseWriter, r *http.Request) {
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Errorf("parse edit: %v", err)
			}
			if r.FormValue("prompt") != "make it blue" {
				t.Errorf("edit prompt = %q", r.FormValue("prompt"))
			}
			if r.FormValue("video[id]") == "" && len(r.MultipartForm.File["video"]) != 1 {
				t.Errorf("edit source = %#v / %#v", r.MultipartForm.Value, r.MultipartForm.File)
			}
			_, _ = io.WriteString(w, `{"id":"video_3","status":"queued"}`)
		},
		"POST /videos/extensions": func(w http.ResponseWriter, r *http.Request) {
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Errorf("parse extension: %v", err)
			}
			if r.FormValue("seconds") != "4" {
				t.Errorf("extension seconds = %q", r.FormValue("seconds"))
			}
			if r.FormValue("video[id]") == "" && len(r.MultipartForm.File["video"]) != 1 {
				t.Errorf("extension source = %#v / %#v", r.MultipartForm.Value, r.MultipartForm.File)
			}
			_, _ = io.WriteString(w, `{"id":"video_4","status":"queued"}`)
		},
		"POST /videos/characters": func(w http.ResponseWriter, r *http.Request) {
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Errorf("parse character: %v", err)
			}
			if r.FormValue("name") != "Milo" {
				t.Errorf("character name = %q", r.FormValue("name"))
			}
			_, _ = io.WriteString(w, `{"id":"char_1","name":"Milo","created_at":1}`)
		},
		"GET /videos/characters/char_1": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"id":"char_1","name":"Milo","created_at":1}`)
		},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dispatchRuntimeTestRoute(t, routes, w, r)
	}))
	defer server.Close()

	model := newCapabilityTestModel(t, server.URL)
	created, err := model.CreateVideo(t.Context(), VideoRequest{
		Model: "sora-2", Prompt: "cat piano", InputReference: &ImageReference{FileID: "file_1"},
	})
	if err != nil || created.ID != "video_1" {
		t.Fatalf("CreateVideo() = %+v, %v", created, err)
	}
	listed, err := model.ListVideos(t.Context(), VideoListQuery{Limit: 5, Order: "desc"})
	if err != nil || len(listed.Data) != 1 {
		t.Fatalf("ListVideos() = %+v, %v", listed, err)
	}
	video, err := model.GetVideo(t.Context(), "video_1")
	if err != nil || video.Progress != 100 {
		t.Fatalf("GetVideo() = %+v, %v", video, err)
	}
	remix, err := model.RemixVideo(t.Context(), "video_1", "jazz")
	if err != nil || remix.RemixedFromVideoID != "video_1" {
		t.Fatalf("RemixVideo() = %+v, %v", remix, err)
	}
	edited, err := model.EditVideo(t.Context(), VideoEditRequest{
		Prompt: "make it blue", VideoFile: &FileContent{Filename: "video.mp4", Data: []byte("video")},
	})
	if err != nil || edited.ID != "video_3" {
		t.Fatalf("EditVideo() = %+v, %v", edited, err)
	}
	edited, err = model.EditVideo(t.Context(), VideoEditRequest{
		Prompt: "make it blue", VideoID: "video_1",
	})
	if err != nil || edited.ID != "video_3" {
		t.Fatalf("EditVideo(id) = %+v, %v", edited, err)
	}
	extended, err := model.ExtendVideo(t.Context(), VideoExtendRequest{
		Prompt: "continue", Seconds: "4", VideoFile: &FileContent{Filename: "video.mp4", Data: []byte("video")},
	})
	if err != nil || extended.ID != "video_4" {
		t.Fatalf("ExtendVideo() = %+v, %v", extended, err)
	}
	extended, err = model.ExtendVideo(t.Context(), VideoExtendRequest{
		Prompt: "continue", Seconds: "4", VideoID: "video_1",
	})
	if err != nil || extended.ID != "video_4" {
		t.Fatalf("ExtendVideo(id) = %+v, %v", extended, err)
	}
	character, err := model.CreateVideoCharacter(t.Context(), VideoCharacterRequest{
		Name: "Milo", Video: FileContent{Filename: "milo.mp4", Data: []byte("video")},
	})
	if err != nil || character.ID != "char_1" {
		t.Fatalf("CreateVideoCharacter() = %+v, %v", character, err)
	}
	character, err = model.GetVideoCharacter(t.Context(), "char_1")
	if err != nil || character.Name != "Milo" {
		t.Fatalf("GetVideoCharacter() = %+v, %v", character, err)
	}
	media, err := model.DownloadVideo(t.Context(), "video_1", "thumbnail")
	if err != nil {
		t.Fatalf("DownloadVideo() error = %v", err)
	}
	data, readErr := io.ReadAll(media.Body)
	closeErr := media.Body.Close()
	if readErr != nil || closeErr != nil || string(data) != "thumb" || media.ContentType != "image/webp" {
		t.Fatalf("download = %q, %q, %v, %v", data, media.ContentType, readErr, closeErr)
	}
	deleted, err := model.DeleteVideo(t.Context(), "video_1")
	if err != nil || !deleted.Deleted {
		t.Fatalf("DeleteVideo() = %+v, %v", deleted, err)
	}
}

func TestFileRuntime(t *testing.T) {
	routes := map[string]http.HandlerFunc{
		"POST /files": func(w http.ResponseWriter, r *http.Request) {
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Errorf("parse upload: %v", err)
			}
			if r.FormValue("purpose") != "user_data" {
				t.Errorf("purpose = %q", r.FormValue("purpose"))
			}
			_, _ = io.WriteString(w, `{"id":"file_1","object":"file","bytes":3,"filename":"notes.txt","purpose":"user_data","status":"processed"}`)
		},
		"GET /files": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("purpose") != "user_data" {
				t.Errorf("file query = %q", r.URL.RawQuery)
			}
			_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"file_1","filename":"notes.txt"}],"has_more":false}`)
		},
		"GET /files/file_1": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"id":"file_1","filename":"notes.txt","purpose":"user_data"}`)
		},
		"GET /files/file_1/content": func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Accept") != "application/binary" {
				t.Errorf("Accept = %q", r.Header.Get("Accept"))
			}
			w.Header().Set("Content-Type", "text/plain")
			_, _ = io.WriteString(w, "abc")
		},
		"DELETE /files/file_1": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"id":"file_1","object":"file","deleted":true}`)
		},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dispatchRuntimeTestRoute(t, routes, w, r)
	}))
	defer server.Close()

	model := newCapabilityTestModel(t, server.URL)
	uploaded, err := model.UploadFile(t.Context(), FileUploadRequest{
		File: FileContent{Filename: "notes.txt", ContentType: "text/plain", Data: []byte("abc")}, Purpose: "user_data",
	})
	if err != nil || uploaded.ID != "file_1" {
		t.Fatalf("UploadFile() = %+v, %v", uploaded, err)
	}
	listed, err := model.ListFiles(t.Context(), FileListQuery{Purpose: "user_data"})
	if err != nil || len(listed.Data) != 1 {
		t.Fatalf("ListFiles() = %+v, %v", listed, err)
	}
	file, err := model.GetFile(t.Context(), "file_1")
	if err != nil || file.Filename != "notes.txt" {
		t.Fatalf("GetFile() = %+v, %v", file, err)
	}
	content, err := model.DownloadFile(t.Context(), "file_1")
	if err != nil {
		t.Fatalf("DownloadFile() error = %v", err)
	}
	data, readErr := io.ReadAll(content.Body)
	closeErr := content.Body.Close()
	if readErr != nil || closeErr != nil || string(data) != "abc" {
		t.Fatalf("file content = %q, %v, %v", data, readErr, closeErr)
	}
	deleted, err := model.DeleteFile(t.Context(), "file_1")
	if err != nil || !deleted.Deleted {
		t.Fatalf("DeleteFile() = %+v, %v", deleted, err)
	}
}

func TestVideoAndFileValidation(t *testing.T) {
	model := newCapabilityTestModel(t, "http://example.com")
	if _, err := model.CreateVideo(t.Context(), VideoRequest{}); err == nil {
		t.Fatal("CreateVideo(empty) error = nil")
	}
	if _, err := model.GetVideo(t.Context(), ""); err == nil {
		t.Fatal("GetVideo(empty) error = nil")
	}
	if _, err := model.UploadFile(t.Context(), FileUploadRequest{}); err == nil {
		t.Fatal("UploadFile(empty) error = nil")
	}
}
