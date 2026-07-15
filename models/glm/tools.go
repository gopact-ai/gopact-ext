package glm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"net/http"
	"strings"
)

const DefaultToolSearchModel = "web-search-pro"

// ToolSearchRequest configures Z.AI's model-driven web-search tool.
type ToolSearchRequest struct {
	Model      string `json:"model"`
	Messages   any    `json:"messages"`
	RequestID  string `json:"request_id,omitempty"`
	Scope      string `json:"scope,omitempty"`
	Location   string `json:"location,omitempty"`
	RecentDays int    `json:"recent_days,omitempty"`
}

// ToolSearchResponse is a completed model-driven web search.
type ToolSearchResponse struct {
	ID        string             `json:"id"`
	Created   int64              `json:"created"`
	RequestID string             `json:"request_id"`
	Choices   []ToolSearchChoice `json:"choices"`
}

// ToolSearchChoice contains one completed search-tool message.
type ToolSearchChoice struct {
	Index        int               `json:"index"`
	FinishReason string            `json:"finish_reason"`
	Message      ToolSearchMessage `json:"message"`
}

// ToolSearchMessage contains structured search-tool calls.
type ToolSearchMessage struct {
	Role      string           `json:"role"`
	ToolCalls []ToolSearchCall `json:"tool_calls"`
}

// ToolSearchCall is an intent, result, or recommendation emitted by the tool.
type ToolSearchCall struct {
	Index          int                       `json:"index"`
	ID             string                    `json:"id"`
	Type           string                    `json:"type"`
	SearchIntent   *ToolSearchIntent         `json:"search_intent"`
	SearchResult   *ToolSearchResult         `json:"search_result"`
	Recommendation *ToolSearchRecommendation `json:"search_recommend"`
}

// ToolSearchIntent describes an optimized search query and inferred intent.
type ToolSearchIntent struct {
	Index    int    `json:"index"`
	Query    string `json:"query"`
	Intent   string `json:"intent"`
	Keywords string `json:"keywords"`
}

// ToolSearchResult is one result emitted by the model-driven search tool.
type ToolSearchResult struct {
	Index     int    `json:"index"`
	Title     string `json:"title"`
	Link      string `json:"link"`
	Content   string `json:"content"`
	Icon      string `json:"icon"`
	Media     string `json:"media"`
	Reference string `json:"refer"`
}

// ToolSearchRecommendation is a suggested follow-up search query.
type ToolSearchRecommendation struct {
	Index int    `json:"index"`
	Query string `json:"query"`
}

// ToolSearchChunk is one streaming model-driven search event.
type ToolSearchChunk struct {
	ID      string                  `json:"id"`
	Created int64                   `json:"created"`
	Choices []ToolSearchChunkChoice `json:"choices"`
}

// ToolSearchChunkChoice contains one streamed search-tool delta.
type ToolSearchChunkChoice struct {
	Index        int             `json:"index"`
	FinishReason string          `json:"finish_reason"`
	Delta        ToolSearchDelta `json:"delta"`
}

// ToolSearchDelta carries streamed search-tool calls.
type ToolSearchDelta struct {
	Role      string           `json:"role"`
	ToolCalls []ToolSearchCall `json:"tool_calls"`
}

// SearchTool runs Z.AI's model-driven web-search tool.
func (model *Model) SearchTool(ctx context.Context, request ToolSearchRequest) (ToolSearchResponse, error) {
	if model == nil {
		return ToolSearchResponse{}, errors.New("glm: model is nil")
	}
	if err := prepareToolSearchRequest(&request); err != nil {
		return ToolSearchResponse{}, err
	}
	var response ToolSearchResponse
	err := model.runtimeJSON(ctx, http.MethodPost, "/tools", request, &response)
	return response, err
}

// StreamSearchTool streams Z.AI model-driven web-search events.
func (model *Model) StreamSearchTool(ctx context.Context, request ToolSearchRequest) iter.Seq2[ToolSearchChunk, error] {
	return func(yield func(ToolSearchChunk, error) bool) {
		if model == nil {
			yield(ToolSearchChunk{}, errors.New("glm: model is nil"))
			return
		}
		if err := prepareToolSearchRequest(&request); err != nil {
			yield(ToolSearchChunk{}, err)
			return
		}
		payload := struct {
			ToolSearchRequest
			Stream bool `json:"stream"`
		}{ToolSearchRequest: request, Stream: true}
		encoded, err := json.Marshal(payload)
		if err != nil {
			yield(ToolSearchChunk{}, fmt.Errorf("glm: encode search tool stream: %w", err))
			return
		}
		endpoint := model.apiBaseURL + "/tools"
		for raw, err := range model.runtimeEventStream(ctx, endpoint, encoded, "application/json") {
			if err != nil {
				yield(ToolSearchChunk{}, err)
				return
			}
			var chunk ToolSearchChunk
			if err := json.Unmarshal(raw, &chunk); err != nil {
				yield(ToolSearchChunk{}, fmt.Errorf("glm: decode search tool stream: %w", err))
				return
			}
			if !yield(chunk, nil) {
				return
			}
		}
	}
}

func prepareToolSearchRequest(request *ToolSearchRequest) error {
	if request.Model == "" {
		request.Model = DefaultToolSearchModel
	}
	if request.Messages == nil {
		return errors.New("glm: search tool messages are required")
	}
	if text, ok := request.Messages.(string); ok && strings.TrimSpace(text) == "" {
		return errors.New("glm: search tool messages are required")
	}
	if request.RecentDays < 0 || request.RecentDays > maxToolRecentDays {
		return errors.New("glm: search tool recent days must be between 1 and 30")
	}
	return nil
}
