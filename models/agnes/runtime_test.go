package agnes

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestImageAndVideoAPIs(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/images/generations", func(w http.ResponseWriter, r *http.Request) {
		var request ImageRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode image request: %v", err)
		}
		if request.Model != DefaultImageModel || request.Extra.ResponseFormat != "url" || len(request.Extra.Images) != 1 {
			t.Errorf("image request = %+v", request)
		}
		_, _ = w.Write([]byte(`{"created":1,"data":[{"url":"https://example.com/image.png"}]}`))
	})
	mux.HandleFunc("POST /v1/videos", func(w http.ResponseWriter, r *http.Request) {
		var request VideoRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode video request: %v", err)
		}
		if request.Model != DefaultVideoModel || request.NumFrames != 121 || request.FrameRate != 24 {
			t.Errorf("video request = %+v", request)
		}
		_, _ = w.Write([]byte(`{"id":"task-1","task_id":"task-1","video_id":"video-1","status":"queued"}`))
	})
	mux.HandleFunc("GET /agnesapi", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("video_id") != "video-1" || r.URL.Query().Get("model_name") != DefaultVideoModel {
			t.Errorf("query = %v", r.URL.Query())
		}
		_, _ = w.Write([]byte(`{"id":"task-1","video_id":"video-1","status":"completed","progress":100,"url":"https://example.com/video.mp4"}`))
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if authorization := r.Header.Get("Authorization"); authorization != "Bearer key" {
			t.Errorf("Authorization = %q", authorization)
		}
		mux.ServeHTTP(w, r)
	}))
	defer server.Close()

	model, err := New("key", WithBaseURL(server.URL+"/v1"), WithInsecureHTTP())
	if err != nil {
		t.Fatal(err)
	}
	image, err := model.GenerateImage(t.Context(), ImageRequest{
		Prompt: "edit", Size: "2K",
		Extra: ImageExtra{Images: []string{"https://example.com/input.png"}, ResponseFormat: "url"},
	})
	if err != nil {
		t.Fatalf("GenerateImage() error = %v", err)
	}
	if len(image.Data) != 1 || image.Data[0].URL == "" {
		t.Fatalf("image = %+v", image)
	}
	task, err := model.CreateVideo(t.Context(), VideoRequest{Prompt: "animate", NumFrames: 121, FrameRate: 24})
	if err != nil {
		t.Fatalf("CreateVideo() error = %v", err)
	}
	if task.VideoID != "video-1" {
		t.Fatalf("task = %+v", task)
	}
	result, err := model.Video(t.Context(), task.VideoID, DefaultVideoModel)
	if err != nil {
		t.Fatalf("Video() error = %v", err)
	}
	if result.Status != "completed" || result.URL == "" {
		t.Fatalf("result = %+v", result)
	}
}

func TestCreateVideoValidatesFrames(t *testing.T) {
	model, err := New("key")
	if err != nil {
		t.Fatal(err)
	}
	for _, frames := range []int{2, 442} {
		if _, err := model.CreateVideo(t.Context(), VideoRequest{Prompt: "video", NumFrames: frames}); err == nil {
			t.Fatalf("CreateVideo(frames=%d) error = nil", frames)
		}
	}
}
