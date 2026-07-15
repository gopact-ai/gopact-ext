package glm

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSpeechAPIs(t *testing.T) {
	routes := map[string]http.HandlerFunc{
		"POST /api/audio/speech": func(w http.ResponseWriter, r *http.Request) {
			var request map[string]any
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode speech request: %v", err)
			}
			if request["model"] != "glm-tts" || request["voice"] != "tongtong" {
				t.Errorf("speech request = %#v", request)
			}
			if stream, _ := request["stream"].(bool); stream {
				if r.Header.Get("Accept") != "text/event-stream" {
					t.Errorf("Accept = %q", r.Header.Get("Accept"))
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"YXVkaW8=\"},\"index\":0}]}\n\n")
				_, _ = io.WriteString(w, "data: [DONE]\n\n")
				return
			}
			if r.Header.Get("Accept") != "application/octet-stream" {
				t.Errorf("Accept = %q", r.Header.Get("Accept"))
			}
			w.Header().Set("Content-Type", "audio/wav")
			_, _ = io.WriteString(w, "speech")
		},
		"POST /api/audio/customization": func(w http.ResponseWriter, r *http.Request) {
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Errorf("parse customization: %v", err)
				return
			}
			if r.FormValue("model") != "glm-tts" || r.FormValue("input") != "hello" ||
				r.FormValue("sensitive_word_check[type]") != "ALL" ||
				r.FormValue("watermark_enabled") != "true" {
				t.Errorf("customization form = %#v", r.MultipartForm.Value)
			}
			file, _, err := r.FormFile("voice_data")
			if err != nil {
				t.Errorf("voice_data: %v", err)
				return
			}
			defer func() { _ = file.Close() }()
			data, _ := io.ReadAll(file)
			if string(data) != "voice" {
				t.Errorf("voice_data = %q", data)
			}
			w.Header().Set("Content-Type", "audio/wav")
			_, _ = io.WriteString(w, "custom")
		},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer key" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		dispatchRuntimeTestRoute(t, routes, w, r)
	}))
	defer server.Close()

	model := newExtendedRuntimeModel(t, server)
	speed, watermark := 1.25, true
	request := SpeechRequest{
		Model: "glm-tts", Input: "hello", Voice: "tongtong", ResponseFormat: "wav",
		Speed: &speed, WatermarkEnabled: &watermark,
		SensitiveWordCheck: &SensitiveWordCheck{Type: "ALL", Status: "DISABLE"},
	}
	media, err := model.Speech(t.Context(), request)
	if err != nil {
		t.Fatalf("Speech() error = %v", err)
	}
	data, err := io.ReadAll(media.Body)
	if err != nil {
		t.Fatalf("read Speech() body: %v", err)
	}
	if err := media.Body.Close(); err != nil {
		t.Fatalf("close Speech() body: %v", err)
	}
	if string(data) != "speech" || media.ContentType != "audio/wav" {
		t.Fatalf("Speech() = %q, %q", data, media.ContentType)
	}

	var events []SpeechEvent
	for event, err := range model.StreamSpeech(t.Context(), request) {
		if err != nil {
			t.Fatalf("StreamSpeech() error = %v", err)
		}
		events = append(events, event)
	}
	if len(events) != 1 || events[0].Choices[0].Delta.Content != "YXVkaW8=" {
		t.Fatalf("StreamSpeech() = %+v", events)
	}

	custom, err := model.CustomizeSpeech(t.Context(), CustomSpeechRequest{
		Model: "glm-tts", Input: "hello", VoiceText: "sample",
		VoiceData:          FileContent{Filename: "voice.wav", Data: []byte("voice")},
		WatermarkEnabled:   &watermark,
		SensitiveWordCheck: &SensitiveWordCheck{Type: "ALL", Status: "DISABLE"},
	})
	if err != nil {
		t.Fatalf("CustomizeSpeech() error = %v", err)
	}
	customData, err := io.ReadAll(custom.Body)
	if err != nil {
		t.Fatalf("read CustomizeSpeech() body: %v", err)
	}
	if err := custom.Body.Close(); err != nil {
		t.Fatalf("close CustomizeSpeech() body: %v", err)
	}
	if string(customData) != "custom" {
		t.Fatalf("CustomizeSpeech() = %q", customData)
	}
}

func TestAsyncChatFilesParsingOCRAndModeration(t *testing.T) {
	routes := map[string]http.HandlerFunc{
		"POST /api/async/chat/completions": func(w http.ResponseWriter, r *http.Request) {
			var request AsyncChatRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode async chat request: %v", err)
			}
			if request.Model != "glm-5" || request.Metadata["trace"] != "test" {
				t.Errorf("async chat request = %+v", request)
			}
			_, _ = io.WriteString(w, `{"id":"chat-task","task_status":"PROCESSING"}`)
		},
		"GET /api/async-result/chat-task": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"id":"chat-task","task_status":"SUCCESS","choices":[{}],"usage":{"total_tokens":3}}`)
		},
		"GET /api/files": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("purpose") != "retrieval" || r.URL.Query().Get("limit") != "2" ||
				r.URL.Query().Get("after") != "cursor" {
				t.Errorf("file list request = %s %s", r.Method, r.URL.String())
			}
			_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"file-1","filename":"doc.txt"}],"has_more":false}`)
		},
		"DELETE /api/files/file-1": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"id":"file-1","object":"file","deleted":true}`)
		},
		"GET /api/files/file-1/content": func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Accept") != "application/binary" {
				t.Errorf("file content Accept = %q", r.Header.Get("Accept"))
			}
			_, _ = io.WriteString(w, "document")
		},
		"POST /api/files/parser/create": func(w http.ResponseWriter, r *http.Request) {
			checkParserForm(t, r, "prime")
			_, _ = io.WriteString(w, `{"task_id":"parser-task","success":true}`)
		},
		"POST /api/files/parser/sync": func(w http.ResponseWriter, r *http.Request) {
			checkParserForm(t, r, "prime-sync")
			_, _ = io.WriteString(w, `{"task_id":"sync-task","status":true,"content":"parsed"}`)
		},
		"GET /api/files/parser/result/parser-task/text": func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Accept") != "application/binary" {
				t.Errorf("parser result Accept = %q", r.Header.Get("Accept"))
			}
			_, _ = io.WriteString(w, "parsed")
		},
		"POST /api/files/ocr": func(w http.ResponseWriter, r *http.Request) {
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Errorf("parse OCR form: %v", err)
				return
			}
			if r.FormValue("tool_type") != "hand_write" || r.FormValue("probability") != "true" {
				t.Errorf("OCR form = %#v", r.MultipartForm.Value)
			}
			_, _ = io.WriteString(w, `{"task_id":"ocr-task","status":"SUCCESS","words_result_num":1,"words_result":[{"words":"hello","location":{},"probability":{}}]}`)
		},
		"POST /api/moderations": func(w http.ResponseWriter, r *http.Request) {
			var request ModerationRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode moderation request: %v", err)
			}
			if request.Model != "moderation" || request.Input != "hello" {
				t.Errorf("moderation request = %+v", request)
			}
			_, _ = io.WriteString(w, `{"model":"moderation","input":{"type":"text","safe":true}}`)
		},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer key" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		dispatchRuntimeTestRoute(t, routes, w, r)
	}))
	defer server.Close()

	model := newExtendedRuntimeModel(t, server)
	task, err := model.CreateAsyncChat(t.Context(), AsyncChatRequest{
		Model: "glm-5", Messages: []map[string]any{{"role": "user", "content": "hello"}},
		Metadata: map[string]string{"trace": "test"},
	})
	if err != nil || task.ID != "chat-task" {
		t.Fatalf("CreateAsyncChat() = %+v, %v", task, err)
	}
	result, err := model.AsyncChatResult(t.Context(), task.ID)
	if err != nil || result.TaskStatus != "SUCCESS" || result.Usage.TotalTokens != 3 {
		t.Fatalf("AsyncChatResult() = %+v, %v", result, err)
	}

	files, err := model.ListFiles(t.Context(), FileListQuery{Purpose: "retrieval", Limit: 2, After: "cursor"})
	if err != nil || len(files.Data) != 1 || files.Data[0].ID != "file-1" {
		t.Fatalf("ListFiles() = %+v, %v", files, err)
	}
	deleted, err := model.DeleteFile(t.Context(), "file-1")
	if err != nil || !deleted.Deleted {
		t.Fatalf("DeleteFile() = %+v, %v", deleted, err)
	}
	file, err := model.DownloadFile(t.Context(), "file-1")
	if err != nil {
		t.Fatalf("DownloadFile() error = %v", err)
	}
	fileData, _ := io.ReadAll(file.Body)
	_ = file.Body.Close()
	if string(fileData) != "document" {
		t.Fatalf("DownloadFile() = %q", fileData)
	}

	parserRequest := FileParserRequest{
		File: FileContent{Filename: "document.pdf", Data: []byte("pdf")}, ToolType: "prime",
	}
	parserTask, err := model.CreateFileParserTask(t.Context(), parserRequest)
	if err != nil || parserTask.TaskID != "parser-task" {
		t.Fatalf("CreateFileParserTask() = %+v, %v", parserTask, err)
	}
	parserRequest.ToolType = "prime-sync"
	parsed, err := model.ParseFile(t.Context(), parserRequest)
	if err != nil || parsed.Content != "parsed" {
		t.Fatalf("ParseFile() = %+v, %v", parsed, err)
	}
	parserResult, err := model.FileParserResult(t.Context(), parserTask.TaskID, "text")
	if err != nil {
		t.Fatalf("FileParserResult() error = %v", err)
	}
	parsedData, _ := io.ReadAll(parserResult.Body)
	_ = parserResult.Body.Close()
	if string(parsedData) != "parsed" {
		t.Fatalf("FileParserResult() = %q", parsedData)
	}

	probability := true
	ocr, err := model.RecognizeHandwriting(t.Context(), HandwritingRequest{
		File: FileContent{Filename: "note.png", Data: []byte("png")}, Probability: &probability,
	})
	if err != nil || len(ocr.WordsResult) != 1 || ocr.WordsResult[0].Words != "hello" {
		t.Fatalf("RecognizeHandwriting() = %+v, %v", ocr, err)
	}
	moderation, err := model.Moderate(t.Context(), ModerationRequest{Model: "moderation", Input: "hello"})
	if err != nil || moderation.Model != "moderation" {
		t.Fatalf("Moderate() = %+v, %v", moderation, err)
	}
}

func TestExtendedRuntimeValidatesRequiredInput(t *testing.T) {
	model, err := New("key")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := model.Speech(t.Context(), SpeechRequest{}); err == nil {
		t.Fatal("Speech() error = nil")
	}
	if _, err := model.CreateAsyncChat(t.Context(), AsyncChatRequest{}); err == nil {
		t.Fatal("CreateAsyncChat() error = nil")
	}
	if _, err := model.ListFiles(t.Context(), FileListQuery{Limit: -1}); err == nil {
		t.Fatal("ListFiles() error = nil")
	}
	if _, err := model.FileParserResult(t.Context(), "task", "json"); err == nil {
		t.Fatal("FileParserResult() error = nil")
	}
	if _, err := model.RecognizeHandwriting(t.Context(), HandwritingRequest{}); err == nil {
		t.Fatal("RecognizeHandwriting() error = nil")
	}
}

func newExtendedRuntimeModel(t *testing.T, server *httptest.Server) *Model {
	t.Helper()
	model, err := New(
		"key",
		WithChatBaseURL(server.URL+"/coding"),
		WithAPIBaseURL(server.URL+"/api"),
		WithHTTPClient(server.Client()),
		WithInsecureHTTP(),
	)
	if err != nil {
		t.Fatal(err)
	}
	return model
}

func checkParserForm(t *testing.T, request *http.Request, toolType string) {
	t.Helper()
	if err := request.ParseMultipartForm(1 << 20); err != nil {
		t.Errorf("parse file parser form: %v", err)
		return
	}
	if request.FormValue("tool_type") != toolType {
		t.Errorf("tool_type = %q", request.FormValue("tool_type"))
	}
	file, _, err := request.FormFile("file")
	if err != nil {
		t.Errorf("parser file: %v", err)
		return
	}
	defer func() { _ = file.Close() }()
	data, _ := io.ReadAll(file)
	if string(data) != "pdf" {
		t.Errorf("parser file = %q", data)
	}
}
