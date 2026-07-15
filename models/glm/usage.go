package glm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxUsageResponseBytes = 4 << 20

// UsagePeriod bounds a detailed Coding Plan usage query.
type UsagePeriod struct {
	Start time.Time
	End   time.Time
}

// PlanUsage reports current Coding Plan quota windows.
type PlanUsage struct {
	Level  string
	Limits []PlanLimit
}

// PlanLimit reports one Coding Plan quota.
type PlanLimit struct {
	Type          string
	Unit          int
	Number        int
	Usage         int64
	Current       int64
	Remaining     int64
	Percentage    float64
	NextResetTime time.Time
	Details       []PlanLimitDetail
}

// PlanLimitDetail reports usage attributed to one model or tool.
type PlanLimitDetail struct {
	Model string `json:"modelCode"`
	Usage int64  `json:"usage"`
}

// ModelUsageReport reports Coding Plan model calls and tokens over time.
type ModelUsageReport struct {
	Granularity string              `json:"granularity"`
	Times       []string            `json:"x_time"`
	Calls       []int64             `json:"modelCallCount"`
	Tokens      []int64             `json:"tokensUsage"`
	Total       ModelUsageTotal     `json:"totalUsage"`
	Models      []ModelUsageSeries  `json:"modelDataList"`
	Summary     []ModelUsageSummary `json:"modelSummaryList"`
}

// ModelUsageTotal reports totals for a model usage period.
type ModelUsageTotal struct {
	Calls       int64               `json:"totalModelCallCount"`
	TotalTokens int64               `json:"totalTokensUsage"`
	Models      []ModelUsageSummary `json:"modelSummaryList"`
}

// ModelUsageSeries reports one model's usage over time.
type ModelUsageSeries struct {
	Model       string  `json:"modelName"`
	SortOrder   int     `json:"sortOrder"`
	Tokens      []int64 `json:"tokensUsage"`
	TotalTokens int64   `json:"totalTokens"`
}

// ModelUsageSummary reports one model's total usage.
type ModelUsageSummary struct {
	Model       string `json:"modelName"`
	TotalTokens int64  `json:"totalTokens"`
	SortOrder   int    `json:"sortOrder"`
}

// ToolUsageReport reports Coding Plan tool calls over time.
type ToolUsageReport struct {
	Granularity     string             `json:"granularity"`
	Times           []string           `json:"x_time"`
	NetworkSearches []int64            `json:"networkSearchCount"`
	WebReads        []int64            `json:"webReadMcpCount"`
	RepositoryReads []int64            `json:"zreadMcpCount"`
	Total           ToolUsageTotal     `json:"totalUsage"`
	Tools           []ToolUsageSeries  `json:"toolDataList"`
	Summary         []ToolUsageSummary `json:"toolSummaryList"`
}

// ToolUsageTotal reports totals for a tool usage period.
type ToolUsageTotal struct {
	NetworkSearches int64              `json:"totalNetworkSearchCount"`
	SearchMCP       int64              `json:"totalSearchMcpCount"`
	WebReads        int64              `json:"totalWebReadMcpCount"`
	RepositoryReads int64              `json:"totalZreadMcpCount"`
	Details         []ToolUsageDetail  `json:"toolDetails"`
	Tools           []ToolUsageSummary `json:"toolSummaryList"`
}

// ToolUsageSeries reports one tool's usage over time.
type ToolUsageSeries struct {
	Code       string  `json:"toolCode"`
	Name       string  `json:"toolName"`
	SortOrder  int     `json:"sortOrder"`
	Usage      []int64 `json:"usageCount"`
	TotalUsage int64   `json:"totalUsageCount"`
}

// ToolUsageSummary reports one tool's total usage.
type ToolUsageSummary struct {
	Code       string `json:"toolCode"`
	Name       string `json:"toolName"`
	NameI18n   string `json:"toolNameI18n"`
	TotalUsage int64  `json:"totalUsageCount"`
	SortOrder  int    `json:"sortOrder"`
}

// ToolUsageDetail reports one metered tool total.
type ToolUsageDetail struct {
	Model      string `json:"modelName"`
	TotalUsage int64  `json:"totalUsageCount"`
}

// PlanUsage returns current Coding Plan quota windows.
func (model *Model) PlanUsage(ctx context.Context) (PlanUsage, error) {
	wire, err := requestUsage[planUsageWire](model, ctx, "/quota/limit", nil)
	if err != nil {
		return PlanUsage{}, err
	}
	result := PlanUsage{Level: wire.Level, Limits: make([]PlanLimit, len(wire.Limits))}
	for index, limit := range wire.Limits {
		result.Limits[index] = PlanLimit{
			Type: limit.Type, Unit: limit.Unit, Number: limit.Number,
			Usage: limit.Usage, Current: limit.Current, Remaining: limit.Remaining,
			Percentage: limit.Percentage, Details: limit.Details,
		}
		if limit.NextResetTime != 0 {
			result.Limits[index].NextResetTime = time.UnixMilli(limit.NextResetTime)
		}
	}
	return result, nil
}

// ModelUsage returns Coding Plan model usage for a period.
func (model *Model) ModelUsage(ctx context.Context, period UsagePeriod) (ModelUsageReport, error) {
	query, err := usagePeriodQuery(period)
	if err != nil {
		return ModelUsageReport{}, err
	}
	return requestUsage[ModelUsageReport](model, ctx, "/model-usage", query)
}

// ToolUsage returns Coding Plan tool usage for a period.
func (model *Model) ToolUsage(ctx context.Context, period UsagePeriod) (ToolUsageReport, error) {
	query, err := usagePeriodQuery(period)
	if err != nil {
		return ToolUsageReport{}, err
	}
	return requestUsage[ToolUsageReport](model, ctx, "/tool-usage", query)
}

func usagePeriodQuery(period UsagePeriod) (url.Values, error) {
	if period.Start.IsZero() || period.End.IsZero() {
		return nil, errors.New("glm: usage start and end times are required")
	}
	if period.End.Before(period.Start) {
		return nil, errors.New("glm: usage end time precedes start time")
	}
	return url.Values{
		"startTime": {period.Start.Format(time.DateTime)},
		"endTime":   {period.End.Format(time.DateTime)},
	}, nil
}

func requestUsage[T any](model *Model, ctx context.Context, path string, query url.Values) (T, error) {
	var zero T
	if model == nil {
		return zero, errors.New("glm: model is nil")
	}
	if ctx == nil {
		ctx = context.TODO()
	}
	endpoint, err := url.Parse(strings.TrimRight(model.monitorURL, "/") + path)
	if err != nil || !endpoint.IsAbs() || endpoint.Host == "" {
		return zero, errors.New("glm: invalid usage base URL")
	}
	if endpoint.Scheme != "https" && !(model.allowHTTP && endpoint.Scheme == "http") {
		return zero, errors.New("glm: usage base URL must use HTTPS")
	}
	endpoint.RawQuery = query.Encode()
	callCtx, cancel := context.WithTimeout(ctx, model.timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(callCtx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return zero, fmt.Errorf("glm: create usage request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", model.apiKey)
	client := *model.httpClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(request)
	if err != nil {
		return zero, fmt.Errorf("glm: usage request: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	encoded, err := io.ReadAll(io.LimitReader(response.Body, maxUsageResponseBytes+1))
	if err != nil {
		return zero, fmt.Errorf("glm: read usage response: %w", err)
	}
	if len(encoded) > maxUsageResponseBytes {
		return zero, errors.New("glm: usage response exceeds size limit")
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return zero, fmt.Errorf("glm: usage status %d: %s", response.StatusCode, boundedUsageError(encoded, model.apiKey))
	}
	var envelope usageEnvelope[T]
	if err := json.Unmarshal(encoded, &envelope); err != nil {
		return zero, fmt.Errorf("glm: decode usage response: %w", err)
	}
	if !envelope.Success {
		return zero, fmt.Errorf("glm: usage code %d: %s", envelope.Code, boundedUsageError([]byte(envelope.Message), model.apiKey))
	}
	return envelope.Data, nil
}

func boundedUsageError(encoded []byte, secret string) string {
	const limit = 4 << 10
	if len(encoded) > limit {
		encoded = encoded[:limit]
	}
	return strings.ReplaceAll(string(encoded), secret, "[redacted]")
}

type usageEnvelope[T any] struct {
	Success bool   `json:"success"`
	Code    int    `json:"code"`
	Message string `json:"msg"`
	Data    T      `json:"data"`
}

type planUsageWire struct {
	Level  string `json:"level"`
	Limits []struct {
		Type          string            `json:"type"`
		Unit          int               `json:"unit"`
		Number        int               `json:"number"`
		Usage         int64             `json:"usage"`
		Current       int64             `json:"currentValue"`
		Remaining     int64             `json:"remaining"`
		Percentage    float64           `json:"percentage"`
		NextResetTime int64             `json:"nextResetTime"`
		Details       []PlanLimitDetail `json:"usageDetails"`
	} `json:"limits"`
}
