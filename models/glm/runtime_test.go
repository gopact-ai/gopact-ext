package glm

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRuntimeAPIs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if authorization := r.Header.Get("Authorization"); authorization != "Bearer key" {
			t.Errorf("Authorization = %q", authorization)
		}
		switch r.URL.Path {
		case "/api/images/generations":
			var request ImageRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode image request: %v", err)
			}
			if request.N != 2 || request.SensitiveWordCheck == nil || request.WatermarkEnabled == nil {
				t.Errorf("image request = %+v", request)
			}
			_, _ = w.Write([]byte(`{"created":1,"data":[{"url":"https://example.com/image.png"}]}`))
		case "/api/async/images/generations":
			var request AsyncImageRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode async image request: %v", err)
			}
			if request.WatermarkEnabled == nil || !*request.WatermarkEnabled {
				t.Errorf("async image request = %+v", request)
			}
			_, _ = w.Write([]byte(`{"model":"glm-image","id":"image-task","task_status":"PROCESSING"}`))
		case "/api/videos/generations":
			var request CogVideoRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode video request: %v", err)
			}
			if request.Model != "cogvideox-3" || request.Prompt != "video" ||
				request.SensitiveWordCheck == nil || request.WatermarkEnabled == nil {
				t.Errorf("video request = %+v", request)
			}
			_, _ = w.Write([]byte(`{"model":"cogvideox-3","id":"video-task","task_status":"PROCESSING"}`))
		case "/api/async-result/video-task":
			_, _ = w.Write([]byte(`{"model":"cogvideox-3","task_status":"SUCCESS","video_result":[{"url":"https://example.com/video.mp4"}]}`))
		case "/api/web_search":
			var request SearchRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode search request: %v", err)
			}
			if request.ContentSize != "high" || request.IncludeImage == nil || !*request.IncludeImage {
				t.Errorf("search request = %+v", request)
			}
			_, _ = w.Write([]byte(`{"id":"search-1","search_result":[{"title":"Result","link":"https://example.com"}]}`))
		case "/api/reader":
			_, _ = w.Write([]byte(`{"id":"reader-1","reader_result":{"title":"Page","content":"body","url":"https://example.com"}}`))
		case "/api/layout_parsing":
			_, _ = w.Write([]byte(`{"id":"ocr-1","model":"glm-ocr","md_results":"# Title"}`))
		case "/api/tokenizer":
			_, _ = w.Write([]byte(`{"id":"tokens-1","usage":{"prompt_tokens":3,"total_tokens":3}}`))
		case "/api/audio/transcriptions":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Errorf("parse transcription: %v", err)
			}
			if r.FormValue("model") != DefaultTranscriptionModel || r.FormValue("file_base64") != "aGVsbG8=" ||
				r.FormValue("sensitive_word_check[type]") != "ALL" {
				t.Errorf("transcription form = %#v", r.MultipartForm.Value)
			}
			_, _ = w.Write([]byte(`{"id":"audio-1","model":"glm-asr-2512","text":"hello"}`))
		case "/api/files":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Errorf("parse multipart: %v", err)
			}
			if r.FormValue("purpose") != "agent" {
				t.Errorf("purpose = %q", r.FormValue("purpose"))
			}
			_, _ = w.Write([]byte(`{"id":"file-1","object":"file","bytes":3,"filename":"terms.txt","purpose":"agent","created_at":1}`))
		case "/api/v1/agents":
			_, _ = w.Write([]byte(`{"id":"agent-1","agent_id":"general_translation","status":"success","choices":[{"index":0,"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3,"total_calls":1}}`))
		case "/api/v1/agents/async-result":
			_, _ = w.Write([]byte(`{"status":"success","agent_id":"vidu_template_agent","async_id":"async-1","choices":[{"index":0}]}`))
		case "/api/v1/agents/conversation":
			_, _ = w.Write([]byte(`{"conversation_id":"conversation-1","agent_id":"slides_glm_agent","choices":[{"index":0}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	model, err := New(
		"key",
		WithChatBaseURL(server.URL+"/coding"),
		WithAPIBaseURL(server.URL+"/api"),
		WithInsecureHTTP(),
	)
	if err != nil {
		t.Fatal(err)
	}
	watermark, includeImage := true, true
	policy := &SensitiveWordCheck{Type: "ALL", Status: "DISABLE"}
	image, err := model.GenerateImage(t.Context(), ImageRequest{
		Prompt: "image", N: 2, SensitiveWordCheck: policy, WatermarkEnabled: &watermark,
	})
	if err != nil || len(image.Data) != 1 {
		t.Fatalf("GenerateImage() = %+v, %v", image, err)
	}
	asyncImage, err := model.CreateImage(t.Context(), AsyncImageRequest{Prompt: "image", WatermarkEnabled: &watermark})
	if err != nil || asyncImage.ID != "image-task" {
		t.Fatalf("CreateImage() = %+v, %v", asyncImage, err)
	}
	video, err := model.CreateVideo(t.Context(), CogVideoRequest{
		VideoCommon: VideoCommon{SensitiveWordCheck: policy, WatermarkEnabled: &watermark},
		Model:       "cogvideox-3", Prompt: "video",
	})
	if err != nil || video.ID != "video-task" {
		t.Fatalf("CreateVideo() = %+v, %v", video, err)
	}
	result, err := model.AsyncResult(t.Context(), video.ID)
	if err != nil || len(result.Videos) != 1 {
		t.Fatalf("AsyncResult() = %+v, %v", result, err)
	}
	search, err := model.Search(t.Context(), SearchRequest{
		Query: "gopact", ContentSize: "high", IncludeImage: &includeImage, SensitiveWordCheck: policy,
	})
	if err != nil || len(search.Results) != 1 {
		t.Fatalf("Search() = %+v, %v", search, err)
	}
	reader, err := model.ReadURL(t.Context(), ReaderRequest{URL: "https://example.com"})
	if err != nil || reader.Result.Title != "Page" {
		t.Fatalf("ReadURL() = %+v, %v", reader, err)
	}
	layout, err := model.ParseLayout(t.Context(), LayoutRequest{File: "https://example.com/doc.pdf"})
	if err != nil || layout.Markdown != "# Title" {
		t.Fatalf("ParseLayout() = %+v, %v", layout, err)
	}
	tokens, err := model.Tokenize(t.Context(), TokenizerRequest{Messages: []TokenizerMessage{{Role: "user", Content: "hello"}}})
	if err != nil || tokens.Usage.TotalTokens != 3 {
		t.Fatalf("Tokenize() = %+v, %v", tokens, err)
	}
	audio, err := model.Transcribe(t.Context(), TranscriptionRequest{
		FileBase64: "aGVsbG8=", SensitiveWordCheck: policy,
	})
	if err != nil || audio.Text != "hello" {
		t.Fatalf("Transcribe() = %+v, %v", audio, err)
	}
	file, err := model.UploadFile(t.Context(), "terms.txt", []byte("abc"))
	if err != nil || file.ID != "file-1" {
		t.Fatalf("UploadFile() = %+v, %v", file, err)
	}
	agent, err := model.RunAgent(t.Context(), AgentRequest{
		AgentID:  "general_translation",
		Messages: []AgentMessage{{Role: "user", Content: []AgentContent{{Type: "text", Text: "hello"}}}},
	})
	if err != nil || agent.ID != "agent-1" || agent.Usage.TotalTokens != 3 {
		t.Fatalf("RunAgent() = %+v, %v", agent, err)
	}
	agentResult, err := model.AgentResult(t.Context(), AgentResultRequest{
		AgentID: "vidu_template_agent", AsyncID: "async-1",
	})
	if err != nil || agentResult.Status != "success" {
		t.Fatalf("AgentResult() = %+v, %v", agentResult, err)
	}
	conversation, err := model.AgentConversation(t.Context(), AgentConversationRequest{
		AgentID: "slides_glm_agent", ConversationID: "conversation-1",
	})
	if err != nil || conversation.ConversationID != "conversation-1" {
		t.Fatalf("AgentConversation() = %+v, %v", conversation, err)
	}
}

func TestStreamTranscription(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parse transcription: %v", err)
		}
		if r.FormValue("stream") != "true" || r.FormValue("hotwords[]") != "gopact" {
			t.Errorf("transcription form = %#v", r.MultipartForm.Value)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"id\":\"audio-1\",\"type\":\"transcript.text.delta\",\"delta\":\"hel\"}\n\n"))
		_, _ = w.Write([]byte("data: {\"id\":\"audio-1\",\"type\":\"transcript.text.done\",\"delta\":\"hello\"}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	model, err := New("key", WithAPIBaseURL(server.URL), WithInsecureHTTP())
	if err != nil {
		t.Fatal(err)
	}
	var events []TranscriptionEvent
	for event, err := range model.StreamTranscription(t.Context(), TranscriptionRequest{
		File: &FileContent{Filename: "audio.wav", Data: []byte("wav")}, Hotwords: []string{"gopact"},
	}) {
		if err != nil {
			t.Fatalf("StreamTranscription() error = %v", err)
		}
		events = append(events, event)
	}
	if len(events) != 2 || events[0].Delta != "hel" || events[1].Type != "transcript.text.done" {
		t.Fatalf("events = %+v", events)
	}
}

func TestRuntimeAPIsValidateRequiredInput(t *testing.T) {
	model, err := New("key")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := model.Search(t.Context(), SearchRequest{}); err == nil {
		t.Fatal("Search() error = nil")
	}
	if _, err := model.AsyncResult(t.Context(), ""); err == nil {
		t.Fatal("AsyncResult() error = nil")
	}
	if _, err := model.RunAgent(t.Context(), AgentRequest{}); err == nil {
		t.Fatal("RunAgent() error = nil")
	}
}
