package openai

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const DefaultAPIBaseURL = "https://api.openai.com/v1"

// OrganizationUsageKind selects one OpenAI organization usage endpoint.
type OrganizationUsageKind string

// OpenAI organization usage categories.
const (
	OrganizationUsageCompletions         OrganizationUsageKind = "completions"
	OrganizationUsageEmbeddings          OrganizationUsageKind = "embeddings"
	OrganizationUsageModerations         OrganizationUsageKind = "moderations"
	OrganizationUsageImages              OrganizationUsageKind = "images"
	OrganizationUsageAudioSpeeches       OrganizationUsageKind = "audio_speeches"
	OrganizationUsageAudioTranscriptions OrganizationUsageKind = "audio_transcriptions"
	OrganizationUsageVectorStores        OrganizationUsageKind = "vector_stores"
	OrganizationUsageCodeInterpreter     OrganizationUsageKind = "code_interpreter_sessions"
	OrganizationUsageFileSearch          OrganizationUsageKind = "file_search_calls"
	OrganizationUsageWebSearch           OrganizationUsageKind = "web_search_calls"
)

var organizationUsagePaths = map[OrganizationUsageKind]string{
	OrganizationUsageCompletions:         "/organization/usage/completions",
	OrganizationUsageEmbeddings:          "/organization/usage/embeddings",
	OrganizationUsageModerations:         "/organization/usage/moderations",
	OrganizationUsageImages:              "/organization/usage/images",
	OrganizationUsageAudioSpeeches:       "/organization/usage/audio_speeches",
	OrganizationUsageAudioTranscriptions: "/organization/usage/audio_transcriptions",
	OrganizationUsageVectorStores:        "/organization/usage/vector_stores",
	OrganizationUsageCodeInterpreter:     "/organization/usage/code_interpreter_sessions",
	OrganizationUsageFileSearch:          "/organization/usage/file_search_calls",
	OrganizationUsageWebSearch:           "/organization/usage/web_search_calls",
}

// OrganizationUsageQuery filters one organization usage category.
type OrganizationUsageQuery struct {
	StartTime   time.Time
	EndTime     time.Time
	BucketWidth string
	ProjectIDs  []string
	UserIDs     []string
	APIKeyIDs   []string
	Models      []string
	Batch       *bool
	GroupBy     []string
	Limit       int
	Page        string
}

// OrganizationUsagePage is one page of usage buckets.
type OrganizationUsagePage struct {
	Object   string                    `json:"object"`
	Buckets  []OrganizationUsageBucket `json:"data"`
	HasMore  bool                      `json:"has_more"`
	NextPage string                    `json:"next_page"`
}

// OrganizationUsageBucket groups results into one time window.
type OrganizationUsageBucket struct {
	Object    string                    `json:"object"`
	StartTime int64                     `json:"start_time"`
	EndTime   int64                     `json:"end_time"`
	Results   []OrganizationUsageResult `json:"results"`
}

// OrganizationUsageResult contains fields used across OpenAI usage categories.
type OrganizationUsageResult struct {
	Object            string  `json:"object"`
	InputTokens       int64   `json:"input_tokens"`
	OutputTokens      int64   `json:"output_tokens"`
	CachedInputTokens int64   `json:"input_cached_tokens"`
	InputAudioTokens  int64   `json:"input_audio_tokens"`
	OutputAudioTokens int64   `json:"output_audio_tokens"`
	ModelRequests     int64   `json:"num_model_requests"`
	Requests          int64   `json:"num_requests"`
	Characters        int64   `json:"characters"`
	Seconds           int64   `json:"seconds"`
	Images            int64   `json:"images"`
	UsageBytes        int64   `json:"usage_bytes"`
	Sessions          int64   `json:"num_sessions"`
	ProjectID         string  `json:"project_id"`
	UserID            string  `json:"user_id"`
	APIKeyID          string  `json:"api_key_id"`
	Model             string  `json:"model"`
	Batch             bool    `json:"batch"`
	ServiceTier       string  `json:"service_tier"`
	Source            string  `json:"source"`
	Size              string  `json:"size"`
	VectorStoreID     string  `json:"vector_store_id"`
	ContextLevel      string  `json:"context_level"`
	Quantity          float64 `json:"quantity"`
}

// OrganizationCostsQuery filters organization cost buckets.
type OrganizationCostsQuery struct {
	StartTime   time.Time
	EndTime     time.Time
	BucketWidth string
	ProjectIDs  []string
	APIKeyIDs   []string
	GroupBy     []string
	Limit       int
	Page        string
}

// OrganizationCostsPage is one page of cost buckets.
type OrganizationCostsPage struct {
	Object   string                   `json:"object"`
	Buckets  []OrganizationCostBucket `json:"data"`
	HasMore  bool                     `json:"has_more"`
	NextPage string                   `json:"next_page"`
}

// OrganizationCostBucket groups costs into one time window.
type OrganizationCostBucket struct {
	Object    string                   `json:"object"`
	StartTime int64                    `json:"start_time"`
	EndTime   int64                    `json:"end_time"`
	Results   []OrganizationCostResult `json:"results"`
}

// OrganizationCostResult reports one line item.
type OrganizationCostResult struct {
	Object    string     `json:"object"`
	Amount    CostAmount `json:"amount"`
	APIKeyID  string     `json:"api_key_id"`
	LineItem  string     `json:"line_item"`
	ProjectID string     `json:"project_id"`
	Quantity  float64    `json:"quantity"`
}

// CostAmount is a currency amount returned by the costs endpoint.
type CostAmount struct {
	Value    float64 `json:"value"`
	Currency string  `json:"currency"`
}

type adminConfig struct {
	baseURL string
	options []Option
}

// AdminOption configures an AdminClient.
type AdminOption func(*adminConfig)

// AdminClient reads organization usage with an OpenAI Admin API key.
type AdminClient struct {
	transport *Model
}

// NewAdminClient creates a client for organization usage and costs. The key
// must be an Admin API key; normal project API keys are not accepted upstream.
func NewAdminClient(apiKey string, options ...AdminOption) (*AdminClient, error) {
	config := adminConfig{baseURL: DefaultAPIBaseURL}
	for _, option := range options {
		if option != nil {
			option(&config)
		}
	}
	transport, err := New("openai.admin", config.baseURL, apiKey, "admin", config.options...)
	if err != nil {
		return nil, err
	}
	return &AdminClient{transport: transport}, nil
}

// WithAdminBaseURL overrides the OpenAI Admin API base URL.
func WithAdminBaseURL(baseURL string) AdminOption {
	return func(config *adminConfig) { config.baseURL = baseURL }
}

// WithAdminHTTPClient sets the Admin API HTTP client.
func WithAdminHTTPClient(client *http.Client) AdminOption {
	return func(config *adminConfig) { config.options = append(config.options, WithHTTPClient(client)) }
}

// WithAdminTimeout bounds each Admin API request.
func WithAdminTimeout(timeout time.Duration) AdminOption {
	return func(config *adminConfig) { config.options = append(config.options, WithTimeout(timeout)) }
}

// WithAdminMaxAttempts sets retries for transient Admin API failures.
func WithAdminMaxAttempts(attempts int) AdminOption {
	return func(config *adminConfig) { config.options = append(config.options, WithMaxAttempts(attempts)) }
}

// WithAdminInsecureHTTP permits HTTP for local development and tests.
func WithAdminInsecureHTTP() AdminOption {
	return func(config *adminConfig) { config.options = append(config.options, WithInsecureHTTP()) }
}

// Usage returns one OpenAI organization usage category.
func (client *AdminClient) Usage(ctx context.Context, kind OrganizationUsageKind, query OrganizationUsageQuery) (OrganizationUsagePage, error) {
	if client == nil || client.transport == nil {
		return OrganizationUsagePage{}, errors.New("openai: admin client is nil")
	}
	path, ok := organizationUsagePaths[kind]
	if !ok {
		return OrganizationUsagePage{}, errors.New("openai: unknown organization usage kind")
	}
	values, err := usageQueryValues(query)
	if err != nil {
		return OrganizationUsagePage{}, err
	}
	var page OrganizationUsagePage
	err = client.transport.requestJSON(ctx, http.MethodGet, path+"?"+values.Encode(), nil, &page)
	return page, err
}

// Costs returns OpenAI organization costs.
func (client *AdminClient) Costs(ctx context.Context, query OrganizationCostsQuery) (OrganizationCostsPage, error) {
	if client == nil || client.transport == nil {
		return OrganizationCostsPage{}, errors.New("openai: admin client is nil")
	}
	values, err := commonOrganizationQuery(
		query.StartTime, query.EndTime, query.BucketWidth,
		query.ProjectIDs, query.GroupBy, query.Limit, query.Page,
	)
	if err != nil {
		return OrganizationCostsPage{}, err
	}
	addStrings(values, "api_key_ids[]", query.APIKeyIDs)
	var page OrganizationCostsPage
	err = client.transport.requestJSON(ctx, http.MethodGet, "/organization/costs?"+values.Encode(), nil, &page)
	return page, err
}

func usageQueryValues(query OrganizationUsageQuery) (url.Values, error) {
	values, err := commonOrganizationQuery(
		query.StartTime, query.EndTime, query.BucketWidth,
		query.ProjectIDs, query.GroupBy, query.Limit, query.Page,
	)
	if err != nil {
		return nil, err
	}
	addStrings(values, "user_ids[]", query.UserIDs)
	addStrings(values, "api_key_ids[]", query.APIKeyIDs)
	addStrings(values, "models[]", query.Models)
	if query.Batch != nil {
		values.Set("batch", strconv.FormatBool(*query.Batch))
	}
	return values, nil
}

func commonOrganizationQuery(
	start, end time.Time,
	bucketWidth string,
	projectIDs, groupBy []string,
	limit int,
	page string,
) (url.Values, error) {
	if start.IsZero() {
		return nil, errors.New("openai: organization usage start time is required")
	}
	if !end.IsZero() && end.Before(start) {
		return nil, errors.New("openai: organization usage end time precedes start time")
	}
	if limit < 0 {
		return nil, errors.New("openai: organization usage limit must not be negative")
	}
	values := url.Values{"start_time": {strconv.FormatInt(start.Unix(), 10)}}
	if !end.IsZero() {
		values.Set("end_time", strconv.FormatInt(end.Unix(), 10))
	}
	if bucketWidth != "" {
		values.Set("bucket_width", bucketWidth)
	}
	addStrings(values, "project_ids[]", projectIDs)
	addStrings(values, "group_by[]", groupBy)
	if limit != 0 {
		values.Set("limit", strconv.Itoa(limit))
	}
	if page != "" {
		values.Set("page", page)
	}
	return values, nil
}

func addStrings(values url.Values, key string, items []string) {
	for _, item := range items {
		values.Add(key, item)
	}
}
