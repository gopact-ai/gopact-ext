package openai

import (
	"context"
	"errors"
	"net/http"
)

// ModerationRequest classifies text, string arrays, or OpenAI multimodal input
// objects. Input must be JSON-marshalable.
type ModerationRequest struct {
	Model string `json:"model,omitempty"`
	Input any    `json:"input"`
}

// ModerationResponse is an OpenAI content-classification result.
type ModerationResponse struct {
	ID      string             `json:"id"`
	Model   string             `json:"model"`
	Results []ModerationResult `json:"results"`
}

// ModerationResult contains category flags, scores, and applied input types.
type ModerationResult struct {
	Flagged                   bool                `json:"flagged"`
	Categories                map[string]bool     `json:"categories"`
	CategoryScores            map[string]float64  `json:"category_scores"`
	CategoryAppliedInputTypes map[string][]string `json:"category_applied_input_types"`
}

// Moderate classifies potentially harmful text or image input.
func (c *Model) Moderate(ctx context.Context, request ModerationRequest) (ModerationResponse, error) {
	if request.Input == nil {
		return ModerationResponse{}, errors.New("openai: moderation input is required")
	}
	var response ModerationResponse
	err := c.requestJSON(ctx, http.MethodPost, "/moderations", request, &response)
	return response, err
}
