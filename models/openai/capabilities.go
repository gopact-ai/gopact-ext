package openai

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/gopact-ai/gopact"
)

// Embed creates embeddings for text inputs.
func (c *Model) Embed(ctx context.Context, request gopact.EmbeddingRequest) (gopact.EmbeddingResponse, error) {
	if c == nil {
		return gopact.EmbeddingResponse{}, errors.New("openai: model is nil")
	}
	model := request.Model
	if model == "" {
		model = c.defaultRequest.Model
	}
	if strings.TrimSpace(model) == "" {
		return gopact.EmbeddingResponse{}, errors.New("openai: embedding model is required")
	}
	if len(request.Input) == 0 {
		return gopact.EmbeddingResponse{}, errors.New("openai: embedding input is required")
	}
	for _, input := range request.Input {
		if input == "" {
			return gopact.EmbeddingResponse{}, errors.New("openai: embedding input must not be empty")
		}
	}
	if request.Dimensions < 0 {
		return gopact.EmbeddingResponse{}, errors.New("openai: embedding dimensions must not be negative")
	}

	var response embeddingResponse
	err := c.requestJSON(ctx, http.MethodPost, "/embeddings", embeddingRequest{
		Model: model, Input: request.Input, Dimensions: request.Dimensions, EncodingFormat: "float",
	}, &response)
	if err != nil {
		return gopact.EmbeddingResponse{}, err
	}
	embeddings := make([]gopact.Embedding, len(response.Data))
	for index, item := range response.Data {
		embeddings[index] = gopact.Embedding{Index: item.Index, Vector: item.Embedding}
	}
	sort.Slice(embeddings, func(i, j int) bool { return embeddings[i].Index < embeddings[j].Index })
	return gopact.EmbeddingResponse{
		Model:      response.Model,
		Embeddings: embeddings,
		Usage: gopact.Usage{
			InputTokens: response.Usage.PromptTokens,
			TotalTokens: response.Usage.TotalTokens,
		},
		ProviderMetadata: map[string]any{"object": response.Object},
	}, nil
}

// ListModels returns models available to the configured API key.
func (c *Model) ListModels(ctx context.Context) (gopact.ModelList, error) {
	if c == nil {
		return gopact.ModelList{}, errors.New("openai: model is nil")
	}
	var response modelListResponse
	if err := c.requestJSON(ctx, http.MethodGet, "/models", nil, &response); err != nil {
		return gopact.ModelList{}, err
	}
	models := make([]gopact.ModelInfo, len(response.Data))
	for index, item := range response.Data {
		models[index] = item.modelInfo()
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return gopact.ModelList{
		Models:           models,
		ProviderMetadata: map[string]any{"object": response.Object},
	}, nil
}

// GetModel returns metadata for one model available to the configured API key.
func (c *Model) GetModel(ctx context.Context, modelID string) (gopact.ModelInfo, error) {
	if c == nil {
		return gopact.ModelInfo{}, errors.New("openai: model is nil")
	}
	if strings.TrimSpace(modelID) == "" {
		return gopact.ModelInfo{}, errors.New("openai: model id is required")
	}
	var response modelResource
	if err := c.requestJSON(ctx, http.MethodGet, "/models/"+url.PathEscape(modelID), nil, &response); err != nil {
		return gopact.ModelInfo{}, err
	}
	return response.modelInfo(), nil
}

type embeddingRequest struct {
	Model          string   `json:"model"`
	Input          []string `json:"input"`
	Dimensions     int      `json:"dimensions,omitempty"`
	EncodingFormat string   `json:"encoding_format"`
}

type embeddingResponse struct {
	Object string `json:"object"`
	Model  string `json:"model"`
	Data   []struct {
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Usage struct {
		PromptTokens int `json:"prompt_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
}

type modelListResponse struct {
	Object string          `json:"object"`
	Data   []modelResource `json:"data"`
}

type modelResource struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

func (model modelResource) modelInfo() gopact.ModelInfo {
	return gopact.ModelInfo{
		ID:      model.ID,
		OwnedBy: model.OwnedBy,
		ProviderMetadata: map[string]any{
			"object":  model.Object,
			"created": model.Created,
		},
	}
}
