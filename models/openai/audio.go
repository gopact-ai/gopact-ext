package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
)

// SpeechRequest configures text-to-speech generation. Voice may be a built-in
// voice string or a custom voice object such as map[string]any{"id": "voice_1"}.
type SpeechRequest struct {
	Model          string   `json:"model"`
	Input          string   `json:"input"`
	Voice          any      `json:"voice"`
	Instructions   string   `json:"instructions,omitempty"`
	ResponseFormat string   `json:"response_format,omitempty"`
	Speed          *float64 `json:"speed,omitempty"`
	StreamFormat   string   `json:"stream_format,omitempty"`
}

// TranscriptionRequest configures speech-to-text for an uploaded audio file.
type TranscriptionRequest struct {
	File                   FileContent
	Model                  string
	Language               string
	Prompt                 string
	Temperature            *float64
	ChunkingStrategy       any
	Include                []string
	KnownSpeakerNames      []string
	KnownSpeakerReferences []string
	ResponseFormat         string
	TimestampGranularities []string
}

// TranscriptionResponse is a JSON, diarized JSON, verbose JSON, or plain-text
// transcription result.
type TranscriptionResponse struct {
	Task     string            `json:"task"`
	Language string            `json:"language"`
	Duration float64           `json:"duration"`
	Text     string            `json:"text"`
	Logprobs []json.RawMessage `json:"logprobs"`
	Segments []json.RawMessage `json:"segments"`
	Words    []json.RawMessage `json:"words"`
	Usage    AudioUsage        `json:"usage"`
}

// AudioUsage reports token- or duration-based speech-to-text usage.
type AudioUsage struct {
	Type              string  `json:"type"`
	InputTokens       int     `json:"input_tokens"`
	OutputTokens      int     `json:"output_tokens"`
	TotalTokens       int     `json:"total_tokens"`
	Seconds           float64 `json:"seconds"`
	InputTokenDetails struct {
		AudioTokens int `json:"audio_tokens"`
		TextTokens  int `json:"text_tokens"`
	} `json:"input_token_details"`
}

// TranslationRequest configures Whisper audio-to-English translation.
type TranslationRequest struct {
	File           FileContent
	Model          string
	Prompt         string
	ResponseFormat string
	Temperature    *float64
}

// TranslationResponse is an audio-to-English result.
type TranslationResponse struct {
	Text string `json:"text"`
}

// AudioEvent is one raw speech or transcription SSE event.
type AudioEvent struct {
	Type string
	Data json.RawMessage
}

// Speech generates audio and returns a body that the caller must close.
func (c *Model) Speech(ctx context.Context, request SpeechRequest) (Media, error) {
	if err := validateSpeechRequest(request); err != nil {
		return Media{}, err
	}
	if request.StreamFormat == "sse" {
		return Media{}, errors.New("openai: use StreamSpeech for SSE speech output")
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return Media{}, fmt.Errorf("openai: encode speech request: %w", err)
	}
	return c.requestMedia(ctx, http.MethodPost, "/audio/speech", encoded, "application/json", "application/octet-stream")
}

// StreamSpeech streams raw text-to-speech SSE events.
func (c *Model) StreamSpeech(ctx context.Context, request SpeechRequest) iter.Seq2[AudioEvent, error] {
	return func(yield func(AudioEvent, error) bool) {
		if err := validateSpeechRequest(request); err != nil {
			yield(AudioEvent{}, err)
			return
		}
		request.StreamFormat = "sse"
		encoded, err := json.Marshal(request)
		if err != nil {
			yield(AudioEvent{}, fmt.Errorf("openai: encode speech stream request: %w", err))
			return
		}
		yieldAudioEvents(yield, c.streamJSON(ctx, "/audio/speech", encoded, "application/json"))
	}
}

// Transcribe converts uploaded audio into text.
func (c *Model) Transcribe(ctx context.Context, request TranscriptionRequest) (TranscriptionResponse, error) {
	if err := validateTranscriptionRequest(request); err != nil {
		return TranscriptionResponse{}, err
	}
	data, err := c.requestMultipartText(ctx, "/audio/transcriptions", func(writer *multipart.Writer) error {
		return writeTranscriptionMultipart(writer, request, false)
	})
	if err != nil {
		return TranscriptionResponse{}, err
	}
	if isPlainAudioFormat(request.ResponseFormat) {
		return TranscriptionResponse{Text: string(data)}, nil
	}
	var response TranscriptionResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return TranscriptionResponse{}, fmt.Errorf("openai: decode transcription response: %w", err)
	}
	return response, nil
}

// StreamTranscription streams raw speech-to-text SSE events.
func (c *Model) StreamTranscription(
	ctx context.Context,
	request TranscriptionRequest,
) iter.Seq2[AudioEvent, error] {
	return func(yield func(AudioEvent, error) bool) {
		if err := validateTranscriptionRequest(request); err != nil {
			yield(AudioEvent{}, err)
			return
		}
		encoded, contentType, err := encodeMultipart(func(writer *multipart.Writer) error {
			return writeTranscriptionMultipart(writer, request, true)
		})
		if err != nil {
			yield(AudioEvent{}, err)
			return
		}
		yieldAudioEvents(yield, c.streamJSON(ctx, "/audio/transcriptions", encoded, contentType))
	}
}

// Translate converts uploaded audio into English.
func (c *Model) Translate(ctx context.Context, request TranslationRequest) (TranslationResponse, error) {
	if strings.TrimSpace(request.Model) == "" {
		return TranslationResponse{}, errors.New("openai: translation model is required")
	}
	if len(request.File.Data) == 0 || strings.TrimSpace(request.File.Filename) == "" {
		return TranslationResponse{}, errors.New("openai: translation audio file is required")
	}
	if request.Temperature != nil && (*request.Temperature < 0 || *request.Temperature > 1) {
		return TranslationResponse{}, errors.New("openai: translation temperature must be between 0 and 1")
	}
	data, err := c.requestMultipartText(ctx, "/audio/translations", func(writer *multipart.Writer) error {
		if err := writeMultipartFile(writer, "file", request.File); err != nil {
			return err
		}
		fields := []struct{ name, value string }{
			{"model", request.Model}, {"prompt", request.Prompt}, {"response_format", request.ResponseFormat},
		}
		if request.Temperature != nil {
			fields = append(fields, struct{ name, value string }{
				"temperature", strconv.FormatFloat(*request.Temperature, 'f', -1, 64),
			})
		}
		return writeMultipartFields(writer, fields)
	})
	if err != nil {
		return TranslationResponse{}, err
	}
	if request.ResponseFormat != "" && request.ResponseFormat != "json" && request.ResponseFormat != "verbose_json" {
		return TranslationResponse{Text: string(data)}, nil
	}
	var response TranslationResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return TranslationResponse{}, fmt.Errorf("openai: decode translation response: %w", err)
	}
	return response, nil
}

func validateSpeechRequest(request SpeechRequest) error {
	if strings.TrimSpace(request.Model) == "" {
		return errors.New("openai: speech model is required")
	}
	if request.Input == "" {
		return errors.New("openai: speech input is required")
	}
	if request.Voice == nil {
		return errors.New("openai: speech voice is required")
	}
	if voice, ok := request.Voice.(string); ok && strings.TrimSpace(voice) == "" {
		return errors.New("openai: speech voice is required")
	}
	if request.Speed != nil && (*request.Speed < 0.25 || *request.Speed > 4) {
		return errors.New("openai: speech speed must be between 0.25 and 4")
	}
	return nil
}

func validateTranscriptionRequest(request TranscriptionRequest) error {
	if strings.TrimSpace(request.Model) == "" {
		return errors.New("openai: transcription model is required")
	}
	if len(request.File.Data) == 0 || strings.TrimSpace(request.File.Filename) == "" {
		return errors.New("openai: transcription audio file is required")
	}
	if request.Temperature != nil && (*request.Temperature < 0 || *request.Temperature > 1) {
		return errors.New("openai: transcription temperature must be between 0 and 1")
	}
	if len(request.KnownSpeakerNames) != len(request.KnownSpeakerReferences) {
		return errors.New("openai: known speaker names and references must have equal lengths")
	}
	if len(request.KnownSpeakerNames) > 4 {
		return errors.New("openai: at most four known speakers are supported")
	}
	return nil
}

func writeTranscriptionMultipart(writer *multipart.Writer, request TranscriptionRequest, stream bool) error {
	if err := writeMultipartFile(writer, "file", request.File); err != nil {
		return err
	}
	fields := []struct{ name, value string }{
		{"model", request.Model}, {"language", request.Language}, {"prompt", request.Prompt},
		{"response_format", request.ResponseFormat},
	}
	if request.Temperature != nil {
		fields = append(fields, struct{ name, value string }{
			"temperature", strconv.FormatFloat(*request.Temperature, 'f', -1, 64),
		})
	}
	if request.ChunkingStrategy != nil {
		value, err := multipartJSONValue(request.ChunkingStrategy)
		if err != nil {
			return err
		}
		fields = append(fields, struct{ name, value string }{"chunking_strategy", value})
	}
	for _, value := range request.Include {
		fields = append(fields, struct{ name, value string }{"include[]", value})
	}
	for _, value := range request.KnownSpeakerNames {
		fields = append(fields, struct{ name, value string }{"known_speaker_names[]", value})
	}
	for _, value := range request.KnownSpeakerReferences {
		fields = append(fields, struct{ name, value string }{"known_speaker_references[]", value})
	}
	for _, value := range request.TimestampGranularities {
		fields = append(fields, struct{ name, value string }{"timestamp_granularities[]", value})
	}
	if stream {
		fields = append(fields, struct{ name, value string }{"stream", "true"})
	}
	return writeMultipartFields(writer, fields)
}

func multipartJSONValue(value any) (string, error) {
	if text, ok := value.(string); ok {
		return text, nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("openai: encode multipart field: %w", err)
	}
	return string(encoded), nil
}

func isPlainAudioFormat(format string) bool {
	return format == "text" || format == "srt" || format == "vtt"
}

func yieldAudioEvents(yield func(AudioEvent, error) bool, events iter.Seq2[runtimeEvent, error]) {
	for event, err := range events {
		if !yield(AudioEvent(event), err) {
			return
		}
	}
}
