package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact-ext/models/openai/codexauth"
)

// SubscriptionUsage reports ChatGPT plan limits for Codex.
type SubscriptionUsage struct {
	Plan                  string                `json:"plan_type"`
	RateLimit             *RateLimitStatus      `json:"rate_limit"`
	Credits               *CreditStatus         `json:"credits"`
	SpendControl          *SpendControlStatus   `json:"spend_control"`
	AdditionalRateLimits  []AdditionalRateLimit `json:"additional_rate_limits"`
	RateLimitReached      *RateLimitReached     `json:"rate_limit_reached_type"`
	RateLimitResetCredits *ResetCreditsSummary  `json:"rate_limit_reset_credits"`
}

// RateLimitStatus reports whether calls are allowed and the active windows.
type RateLimitStatus struct {
	Allowed      bool             `json:"allowed"`
	LimitReached bool             `json:"limit_reached"`
	Primary      *RateLimitWindow `json:"primary_window"`
	Secondary    *RateLimitWindow `json:"secondary_window"`
}

// RateLimitWindow reports one subscription usage window.
type RateLimitWindow struct {
	UsedPercent        int   `json:"used_percent"`
	WindowSeconds      int   `json:"limit_window_seconds"`
	ResetAfterSeconds  int   `json:"reset_after_seconds"`
	ResetAtUnixSeconds int64 `json:"reset_at"`
}

// CreditStatus reports optional ChatGPT credits.
type CreditStatus struct {
	HasCredits          bool              `json:"has_credits"`
	Unlimited           bool              `json:"unlimited"`
	Balance             string            `json:"balance"`
	ApproxLocalMessages []json.RawMessage `json:"approx_local_messages"`
	ApproxCloudMessages []json.RawMessage `json:"approx_cloud_messages"`
}

// SpendControlStatus reports an optional workspace spending cap.
type SpendControlStatus struct {
	Reached         bool               `json:"reached"`
	IndividualLimit *SpendControlLimit `json:"individual_limit"`
}

// SpendControlLimit reports one spending cap and its remaining balance.
type SpendControlLimit struct {
	Source             string `json:"source"`
	Limit              string `json:"limit"`
	Used               string `json:"used"`
	Remaining          string `json:"remaining"`
	UsedPercent        int    `json:"used_percent"`
	RemainingPercent   int    `json:"remaining_percent"`
	ResetAfterSeconds  int    `json:"reset_after_seconds"`
	ResetAtUnixSeconds int64  `json:"reset_at"`
}

// AdditionalRateLimit reports a separately metered Codex feature.
type AdditionalRateLimit struct {
	Name           string           `json:"limit_name"`
	MeteredFeature string           `json:"metered_feature"`
	RateLimit      *RateLimitStatus `json:"rate_limit"`
}

// RateLimitReached classifies the exhausted limit.
type RateLimitReached struct {
	Type string `json:"type"`
}

// ResetCreditsSummary reports available rate-limit reset credits.
type ResetCreditsSummary struct {
	Available int64 `json:"available_count"`
}

type refreshAttempt struct {
	response  *http.Response
	sendErr   error
	tokens    codexauth.Tokens
	refreshed bool
}

// ListModels returns the Codex models available to the ChatGPT account.
func (model *Model) ListModels(ctx context.Context) (gopact.ModelList, error) {
	if model == nil {
		return gopact.ModelList{}, errors.New("codex: model is nil")
	}
	endpoint, err := url.Parse(model.baseURL + "/models")
	if err != nil {
		return gopact.ModelList{}, fmt.Errorf("codex: create models URL: %w", err)
	}
	query := endpoint.Query()
	query.Set("client_version", model.clientVersion)
	endpoint.RawQuery = query.Encode()
	var response codexModelsResponse
	headers, err := model.getJSON(ctx, endpoint.String(), &response)
	if err != nil {
		return gopact.ModelList{}, err
	}
	sort.SliceStable(response.Models, func(i, j int) bool {
		if response.Models[i].Priority == response.Models[j].Priority {
			return response.Models[i].Slug < response.Models[j].Slug
		}
		return response.Models[i].Priority < response.Models[j].Priority
	})
	models := make([]gopact.ModelInfo, len(response.Models))
	for index, item := range response.Models {
		metadata := item.Metadata
		delete(metadata, "slug")
		delete(metadata, "display_name")
		delete(metadata, "description")
		delete(metadata, "input_modalities")
		models[index] = gopact.ModelInfo{
			ID:               item.Slug,
			DisplayName:      item.DisplayName,
			Description:      item.Description,
			InputModalities:  codexModalities(item.InputModalities),
			OutputModalities: []gopact.Modality{gopact.ModalityText},
			ProviderMetadata: metadata,
		}
	}
	return gopact.ModelList{
		Models: models,
		ProviderMetadata: map[string]any{
			"etag": headers.Get("ETag"),
		},
	}, nil
}

// SubscriptionUsage returns ChatGPT plan limits and credits for Codex.
func (model *Model) SubscriptionUsage(ctx context.Context) (SubscriptionUsage, error) {
	if model == nil {
		return SubscriptionUsage{}, errors.New("codex: model is nil")
	}
	var usage SubscriptionUsage
	_, err := model.getJSON(ctx, model.subscriptionUsageURL(), &usage)
	return usage, err
}

func (model *Model) subscriptionUsageURL() string {
	if strings.HasSuffix(model.baseURL, "/codex") {
		return strings.TrimSuffix(model.baseURL, "/codex") + "/wham/usage"
	}
	return model.baseURL + "/usage"
}

func (model *Model) getJSON(ctx context.Context, endpoint string, output any) (http.Header, error) {
	callCtx, cancel := model.callContext(ctx)
	defer cancel()
	tokens, err := model.tokenSource.Token(callCtx)
	if err != nil {
		return nil, fmt.Errorf("codex: resolve tokens: %w", err)
	}
	if err := validateTokens(tokens); err != nil {
		return nil, err
	}
	refreshed := false
	for attempt := 1; attempt <= model.maxAttempts; {
		response, sendErr := model.sendGET(callCtx, endpoint, tokens)
		nextTokens, didRefresh, refreshErr := model.refreshUnauthorized(callCtx, refreshAttempt{
			response: response, sendErr: sendErr, tokens: tokens, refreshed: refreshed,
		})
		if refreshErr != nil {
			return nil, refreshErr
		}
		if didRefresh {
			tokens = nextTokens
			refreshed = true
			continue
		}
		if sendErr == nil && response != nil && successfulStatus(response.StatusCode) {
			return decodeJSONResponse(response, output)
		}
		if sendErr == nil && response != nil && (!retryableStatus(response.StatusCode) || attempt == model.maxAttempts) {
			return nil, model.httpError(response, tokens)
		}
		if sendErr != nil && attempt == model.maxAttempts {
			return nil, fmt.Errorf("codex: send request: %w", sendErr)
		}
		delay := retryDelay(response, attempt)
		closeResponse(response)
		if err := waitRetry(callCtx, delay); err != nil {
			return nil, err
		}
		attempt++
	}
	return nil, errors.New("codex: request attempts exhausted")
}

func (model *Model) refreshUnauthorized(ctx context.Context, attempt refreshAttempt) (codexauth.Tokens, bool, error) {
	if attempt.sendErr != nil || attempt.response == nil || attempt.refreshed {
		return attempt.tokens, false, nil
	}
	if attempt.response.StatusCode != http.StatusUnauthorized {
		return attempt.tokens, false, nil
	}
	source, ok := model.tokenSource.(RefreshingTokenSource)
	if !ok {
		return attempt.tokens, false, nil
	}
	closeResponse(attempt.response)
	tokens, err := source.Refresh(ctx)
	if err != nil {
		return codexauth.Tokens{}, false, fmt.Errorf("codex: refresh after unauthorized: %w", err)
	}
	if err := validateTokens(tokens); err != nil {
		return codexauth.Tokens{}, false, err
	}
	return tokens, true, nil
}

func (model *Model) sendGET(ctx context.Context, endpoint string, tokens codexauth.Tokens) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+tokens.AccessToken)
	if tokens.AccountID != "" {
		request.Header.Set("ChatGPT-Account-ID", tokens.AccountID)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Originator", model.originator)
	request.Header.Set("User-Agent", defaultUserAgent)
	return model.httpClient.Do(request)
}

func decodeJSONResponse(response *http.Response, output any) (http.Header, error) {
	defer closeResponse(response)
	encoded, err := io.ReadAll(io.LimitReader(response.Body, maxRequestBytes+1))
	if err != nil {
		return nil, fmt.Errorf("codex: read JSON response: %w", err)
	}
	if len(encoded) > maxRequestBytes {
		return nil, errors.New("codex: JSON response exceeds size limit")
	}
	if err := json.Unmarshal(encoded, output); err != nil {
		return nil, fmt.Errorf("codex: decode JSON response: %w", err)
	}
	return response.Header.Clone(), nil
}

func codexModalities(values []string) []gopact.Modality {
	modalities := make([]gopact.Modality, len(values))
	for index, value := range values {
		modalities[index] = gopact.Modality(value)
	}
	return modalities
}

type codexModelsResponse struct {
	Models []codexModelResource `json:"models"`
}

type codexModelResource struct {
	Slug            string         `json:"slug"`
	DisplayName     string         `json:"display_name"`
	Description     string         `json:"description"`
	Priority        int            `json:"priority"`
	InputModalities []string       `json:"input_modalities"`
	Metadata        map[string]any `json:"-"`
}

func (model *codexModelResource) UnmarshalJSON(encoded []byte) error {
	type wire codexModelResource
	var decoded wire
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return err
	}
	var metadata map[string]any
	if err := json.Unmarshal(encoded, &metadata); err != nil {
		return err
	}
	*model = codexModelResource(decoded)
	model.Metadata = metadata
	return nil
}
