package openai

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestImageRuntime(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/images/generations":
			var request ImageRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode image request: %v", err)
			}
			if request.Prompt != "otter" || request.Model != "gpt-image-1.5" {
				t.Errorf("image request = %+v", request)
			}
			_, _ = io.WriteString(w, `{"created":1,"data":[{"b64_json":"aW1hZ2U="}],"usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}`)
		case "/images/edits":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Errorf("parse multipart: %v", err)
			}
			if r.FormValue("prompt") != "combine" || len(r.MultipartForm.File["image[]"]) != 2 {
				t.Errorf("edit form = %#v / %#v", r.MultipartForm.Value, r.MultipartForm.File)
			}
			_, _ = io.WriteString(w, `{"created":2,"data":[{"b64_json":"ZWRpdA=="}]}`)
		case "/images/variations":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Errorf("parse multipart: %v", err)
			}
			if _, _, err := r.FormFile("image"); err != nil {
				t.Errorf("variation image: %v", err)
			}
			_, _ = io.WriteString(w, `{"created":3,"data":[{"url":"https://example.com/image.png"}]}`)
		default:
			http.Error(w, "unexpected", http.StatusNotFound)
		}
	}))
	defer server.Close()

	model := newCapabilityTestModel(t, server.URL)
	generated, err := model.GenerateImage(t.Context(), ImageRequest{Model: "gpt-image-1.5", Prompt: "otter"})
	if err != nil || generated.Usage.TotalTokens != 5 || generated.Data[0].Base64JSON == "" {
		t.Fatalf("GenerateImage() = %+v, %v", generated, err)
	}
	edited, err := model.EditImage(t.Context(), ImageEditRequest{
		Model: "gpt-image-1.5", Prompt: "combine",
		Images: []FileContent{{Filename: "a.png", Data: []byte("a")}, {Filename: "b.png", Data: []byte("b")}},
	})
	if err != nil || edited.Created != 2 {
		t.Fatalf("EditImage() = %+v, %v", edited, err)
	}
	variation, err := model.CreateImageVariation(t.Context(), ImageVariationRequest{
		Image: FileContent{Filename: "a.png", Data: []byte("a")},
	})
	if err != nil || variation.Data[0].URL == "" {
		t.Fatalf("CreateImageVariation() = %+v, %v", variation, err)
	}
}

func TestImageStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if request["stream"] != true {
			t.Errorf("stream = %#v", request["stream"])
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: image_generation.completed\ndata: {\"type\":\"image_generation.completed\",\"b64_json\":\"aW1hZ2U=\"}\n\n")
	}))
	defer server.Close()

	var events []ImageEvent
	for event, err := range newCapabilityTestModel(t, server.URL).StreamImage(t.Context(), ImageRequest{Prompt: "otter"}) {
		if err != nil {
			t.Fatalf("StreamImage() error = %v", err)
		}
		events = append(events, event)
	}
	if len(events) != 1 || events[0].Type != "image_generation.completed" {
		t.Fatalf("events = %+v", events)
	}
}

func TestAudioRuntime(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/audio/speech":
			if r.Header.Get("Content-Type") != "application/json" {
				t.Errorf("speech content type = %q", r.Header.Get("Content-Type"))
			}
			w.Header().Set("Content-Type", "audio/mpeg")
			_, _ = w.Write([]byte("mp3"))
		case "/audio/transcriptions":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Errorf("parse transcription: %v", err)
			}
			if r.FormValue("model") != "gpt-4o-transcribe" {
				t.Errorf("transcription model = %q", r.FormValue("model"))
			}
			_, _ = io.WriteString(w, `{"text":"hello","usage":{"type":"tokens","input_tokens":2,"output_tokens":1,"total_tokens":3}}`)
		case "/audio/translations":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Errorf("parse translation: %v", err)
			}
			w.Header().Set("Content-Type", "text/plain")
			_, _ = io.WriteString(w, "hello in English")
		default:
			http.Error(w, "unexpected", http.StatusNotFound)
		}
	}))
	defer server.Close()

	model := newCapabilityTestModel(t, server.URL)
	speech, err := model.Speech(t.Context(), SpeechRequest{Model: "gpt-4o-mini-tts", Input: "hello", Voice: "alloy"})
	if err != nil {
		t.Fatalf("Speech() error = %v", err)
	}
	data, err := io.ReadAll(speech.Body)
	if err != nil || string(data) != "mp3" || speech.ContentType != "audio/mpeg" {
		t.Fatalf("speech = %q, %q, %v", data, speech.ContentType, err)
	}
	if err := speech.Body.Close(); err != nil {
		t.Fatalf("close speech body: %v", err)
	}
	transcription, err := model.Transcribe(t.Context(), TranscriptionRequest{
		File: FileContent{Filename: "audio.wav", Data: []byte("wav")}, Model: "gpt-4o-transcribe",
	})
	if err != nil || transcription.Text != "hello" || transcription.Usage.TotalTokens != 3 {
		t.Fatalf("Transcribe() = %+v, %v", transcription, err)
	}
	translation, err := model.Translate(t.Context(), TranslationRequest{
		File: FileContent{Filename: "audio.wav", Data: []byte("wav")}, Model: "whisper-1", ResponseFormat: "text",
	})
	if err != nil || translation.Text != "hello in English" {
		t.Fatalf("Translate() = %+v, %v", translation, err)
	}
}

func TestMediaValidation(t *testing.T) {
	model := newCapabilityTestModel(t, "http://example.com")
	if _, err := model.GenerateImage(t.Context(), ImageRequest{}); err == nil {
		t.Fatal("GenerateImage(empty) error = nil")
	}
	if _, err := model.Speech(t.Context(), SpeechRequest{}); err == nil {
		t.Fatal("Speech(empty) error = nil")
	}
	if _, err := model.Transcribe(t.Context(), TranscriptionRequest{}); err == nil {
		t.Fatal("Transcribe(empty) error = nil")
	}
}
