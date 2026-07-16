// Package fornax reports gopact Agent traces to ByteDance Fornax.
package fornax

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gopact-ai/gopact"
	"github.com/gopact-ai/gopact/agent"
	"github.com/gopact-ai/gopact/workflow"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

const (
	instrumentationName   = "github.com/gopact-ai/gopact-ext/middleware/byted/fornax"
	spanTypeAttribute     = "cozeloop.span_type"
	inputAttribute        = "cozeloop.input"
	outputAttribute       = "cozeloop.output"
	statusAttribute       = "cozeloop.status_code"
	cutOffAttribute       = "cut_off"
	messageIDAttribute    = "messaging.message.id"
	threadIDAttribute     = "session.id"
	modelNameAttribute    = "model_name"
	inputTokensAttribute  = "input_tokens"
	outputTokensAttribute = "output_tokens"
	totalTokensAttribute  = "tokens"
	finishReasonAttribute = "finish_reason"
	toolNameAttribute     = "tool_name"
	toolCallIDAttribute   = "tool_call_id"
	userIDAttribute       = "user_id"
	deviceIDAttribute     = "device_id"
	psmAttribute          = "psm"
	spaceIDTag            = "fornax_space_id"
	durationTag           = "duration"
	psmFirstSpanTag       = "fornax_psm_first_span"
	languageSystemTag     = "language"
	agentSpanType         = "Agent"
	rootSpanType          = "fornax_query"
	modelSpanType         = "model"
	toolSpanType          = "tool"
	authPath              = "/open-apis/auth/v1/service_accounts/authenticate"
	defaultTracePath      = "/open-api/observability/traces/ingest"
	authVersion           = "auth-v1"
	authTTLSeconds        = 3600
	maxTraceFieldBytes    = 4 << 20
	traceTagDefaultCount  = 3
	decimalBase           = 10
	spaceIDBitSize        = 64
	jwtMinParts           = 2
	failedStatusCode      = -1
)

// Config contains the values required to report traces to Fornax.
// The caller owns how these values are obtained and managed.
type Config struct {
	// AK is the Fornax space access key.
	AK string
	// SK is the Fornax space secret key.
	SK string
	// Region optionally selects the Fornax region, for example CN, SG, US,
	// Asia-SouthEastBD, or I18N-DEV.
	Region string
	// SpaceID optionally verifies the workspace resolved from AK/SK.
	SpaceID string
	// Endpoint optionally overrides the complete OTLP/HTTP trace URL.
	Endpoint string
	// PSM optionally identifies the reporting service. It is sent to Fornax
	// authentication and attached to exported spans.
	PSM string
	// UserID optionally attaches the end-user identity to exported spans.
	UserID string
	// DeviceID optionally attaches the end-user device identity to exported spans.
	DeviceID string
	// Metadata attaches custom string tags to exported spans.
	Metadata map[string]string
}

type contextConfig struct {
	userID   string
	deviceID string
	metadata map[string]string
}

type contextKey struct{}

// WithUserID attaches a request-scoped end-user identity to Fornax spans.
func WithUserID(ctx context.Context, userID string) context.Context {
	config := traceContext(ctx)
	config.userID = userID
	return context.WithValue(contextOrBackground(ctx), contextKey{}, config)
}

// WithDeviceID attaches a request-scoped device identity to Fornax spans.
func WithDeviceID(ctx context.Context, deviceID string) context.Context {
	config := traceContext(ctx)
	config.deviceID = deviceID
	return context.WithValue(contextOrBackground(ctx), contextKey{}, config)
}

// WithMetadata attaches request-scoped custom string tags to Fornax spans.
func WithMetadata(ctx context.Context, metadata map[string]string) context.Context {
	config := traceContext(ctx)
	config.metadata = copyMetadata(metadata)
	return context.WithValue(contextOrBackground(ctx), contextKey{}, config)
}

func traceContext(ctx context.Context) contextConfig {
	if ctx == nil {
		return contextConfig{}
	}
	config, _ := ctx.Value(contextKey{}).(contextConfig)
	config.metadata = copyMetadata(config.metadata)
	return config
}

func contextOrBackground(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

// Middleware wraps Agents with Fornax tracing.
type Middleware struct {
	provider *sdktrace.TracerProvider
	tracer   trace.Tracer
	tags     []attribute.KeyValue

	closeOnce sync.Once
	closeErr  error
}

// New creates a Fornax middleware from explicit configuration.
func New(ctx context.Context, config Config) (*Middleware, error) {
	auth, err := authenticate(ctx, config)
	if err != nil {
		return nil, err
	}
	return newWithAuth(ctx, auth)
}

type authConfig struct {
	spaceID       string
	endpoint      string
	authorization string
	psm           string
	userID        string
	deviceID      string
	metadata      map[string]string
}

func newWithAuth(ctx context.Context, config authConfig) (*Middleware, error) {
	spaceID := strings.TrimSpace(config.spaceID)
	endpoint := strings.TrimSpace(config.endpoint)
	if err := validateSpaceID(spaceID); err != nil {
		return nil, err
	}
	parsedEndpoint, err := url.Parse(endpoint)
	if err != nil || (parsedEndpoint.Scheme != "http" && parsedEndpoint.Scheme != "https") || parsedEndpoint.Host == "" {
		return nil, errors.New("fornax: endpoint must be an absolute HTTP URL")
	}
	if strings.TrimSpace(config.authorization) == "" || strings.ContainsAny(config.authorization, "\r\n") {
		return nil, errors.New("fornax: authorization is invalid")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	exporter := &traceExporter{
		client:        http.DefaultClient,
		endpoint:      endpoint,
		authorization: config.authorization,
		spaceID:       spaceID,
		serviceName:   effectivePSM(config.psm),
	}
	return newMiddleware(sdktrace.NewTracerProvider(sdktrace.WithBatcher(exporter)), traceTags(config)), nil
}

func authenticate(ctx context.Context, config Config) (authConfig, error) {
	return authenticateWithHost(ctx, config, hostForRegion(config.Region))
}

func authenticateWithHost(ctx context.Context, config Config, host string) (authConfig, error) {
	ak := strings.TrimSpace(config.AK)
	sk := strings.TrimSpace(config.SK)
	if ak == "" {
		return authConfig{}, errors.New("fornax: AK is required")
	}
	if sk == "" {
		return authConfig{}, errors.New("fornax: SK is required")
	}
	if strings.ContainsAny(ak, "\r\n") || strings.ContainsAny(sk, "\r\n") {
		return authConfig{}, errors.New("fornax: AK/SK is invalid")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	psm := strings.TrimSpace(config.PSM)
	if strings.ContainsAny(psm, "\r\n") {
		return authConfig{}, errors.New("fornax: PSM is invalid")
	}
	psm = effectivePSM(psm)
	token, err := fetchJWTToken(ctx, tokenRequest{
		client: http.DefaultClient,
		host:   host,
		ak:     ak,
		sk:     sk,
		region: config.Region,
		psm:    psm,
	})
	if err != nil {
		return authConfig{}, fmt.Errorf("fornax: get token: %w", err)
	}
	if token.jwt == "" || token.spaceID <= 0 {
		return authConfig{}, errors.New("fornax: authentication returned an invalid token")
	}
	spaceID := strconv.FormatInt(token.spaceID, decimalBase)
	if expected := strings.TrimSpace(config.SpaceID); expected != "" && expected != spaceID {
		return authConfig{}, fmt.Errorf("fornax: space ID mismatch: configured %s, authenticated %s", expected, spaceID)
	}
	endpoint := strings.TrimSpace(config.Endpoint)
	if endpoint == "" {
		endpoint = strings.TrimRight(host, "/") + defaultTracePath
	}
	return authConfig{
		spaceID:       spaceID,
		endpoint:      endpoint,
		authorization: token.jwt,
		psm:           psm,
		userID:        config.UserID,
		deviceID:      config.DeviceID,
		metadata:      copyMetadata(config.Metadata),
	}, nil
}

type traceExporter struct {
	client        *http.Client
	endpoint      string
	authorization string
	spaceID       string
	serviceName   string
}

func (e *traceExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	if len(spans) == 0 {
		return nil
	}
	payload := traceIngestRequest{Spans: make([]uploadSpan, 0, len(spans))}
	for _, span := range spans {
		payload.Spans = append(payload.Spans, uploadSpanFrom(span, e.spaceID, e.serviceName))
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, e.endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", e.authorization)
	request.Header.Set("Agw-Js-Conv", "str")
	response, err := e.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("fornax: export traces HTTP %d", response.StatusCode)
	}
	var result baseResponse
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return fmt.Errorf("fornax: decode trace export response: %w", err)
	}
	if result.Code != 0 {
		return fmt.Errorf("fornax: export traces code %d: %s", result.Code, result.Msg)
	}
	return nil
}

func (*traceExporter) Shutdown(context.Context) error {
	return nil
}

type traceIngestRequest struct {
	Spans []uploadSpan `json:"spans"`
}

type uploadSpan struct {
	StartedATMicros  int64              `json:"started_at_micros"`
	LogID            string             `json:"log_id"`
	SpanID           string             `json:"span_id"`
	ParentID         string             `json:"parent_id"`
	TraceID          string             `json:"trace_id"`
	DurationMicros   int64              `json:"duration_micros"`
	ServiceName      string             `json:"service_name"`
	WorkspaceID      string             `json:"workspace_id"`
	SpanName         string             `json:"span_name"`
	SpanType         string             `json:"span_type"`
	StatusCode       int32              `json:"status_code"`
	Input            string             `json:"input"`
	Output           string             `json:"output"`
	ObjectStorage    string             `json:"object_storage"`
	SystemTagsString map[string]string  `json:"system_tags_string"`
	SystemTagsLong   map[string]int64   `json:"system_tags_long"`
	SystemTagsDouble map[string]float64 `json:"system_tags_double"`
	TagsString       map[string]string  `json:"tags_string"`
	TagsLong         map[string]int64   `json:"tags_long"`
	TagsDouble       map[string]float64 `json:"tags_double"`
	TagsBool         map[string]bool    `json:"tags_bool"`
}

type baseResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
}

func uploadSpanFrom(span sdktrace.ReadOnlySpan, spaceID, serviceName string) uploadSpan {
	out := uploadSpan{
		StartedATMicros: span.StartTime().UnixMicro(),
		SpanID:          span.SpanContext().SpanID().String(),
		ParentID:        parentID(span),
		TraceID:         span.SpanContext().TraceID().String(),
		DurationMicros:  span.EndTime().Sub(span.StartTime()).Microseconds(),
		ServiceName:     effectivePSM(serviceName),
		WorkspaceID:     spaceID,
		SpanName:        span.Name(),
		SpanType:        "graph",
	}
	for _, attr := range span.Attributes() {
		applySpanAttribute(&out, attr)
	}
	applyDefaultSpanTags(&out)
	if span.Status().Code == codes.Error && out.StatusCode == 0 {
		out.StatusCode = failedStatusCode
	}
	return out
}

func applyDefaultSpanTags(span *uploadSpan) {
	if span.SystemTagsString == nil {
		span.SystemTagsString = map[string]string{}
	}
	span.SystemTagsString[languageSystemTag] = "go"
	if span.TagsString == nil {
		span.TagsString = map[string]string{}
	}
	span.TagsString[spaceIDTag] = span.WorkspaceID
	if span.TagsLong == nil {
		span.TagsLong = map[string]int64{}
	}
	span.TagsLong[durationTag] = span.DurationMicros
	if span.SpanType == rootSpanType {
		if span.TagsBool == nil {
			span.TagsBool = map[string]bool{}
		}
		span.TagsBool[psmFirstSpanTag] = true
	}
}

func parentID(span sdktrace.ReadOnlySpan) string {
	if !span.Parent().SpanID().IsValid() {
		return "0"
	}
	return span.Parent().SpanID().String()
}

func applySpanAttribute(span *uploadSpan, attr attribute.KeyValue) {
	key := string(attr.Key)
	switch key {
	case spanTypeAttribute:
		span.SpanType = attr.Value.AsString()
	case inputAttribute:
		span.Input = attr.Value.AsString()
	case outputAttribute:
		span.Output = attr.Value.AsString()
	case statusAttribute:
		span.StatusCode = int32(attr.Value.AsInt64())
	default:
		addTag(span, key, attr.Value)
	}
}

func addTag(span *uploadSpan, key string, value attribute.Value) {
	switch value.Type() {
	case attribute.BOOL:
		if span.TagsBool == nil {
			span.TagsBool = make(map[string]bool)
		}
		span.TagsBool[key] = value.AsBool()
	case attribute.INT64:
		if span.TagsLong == nil {
			span.TagsLong = make(map[string]int64)
		}
		span.TagsLong[key] = value.AsInt64()
	case attribute.FLOAT64:
		if span.TagsDouble == nil {
			span.TagsDouble = make(map[string]float64)
		}
		span.TagsDouble[key] = value.AsFloat64()
	default:
		if span.TagsString == nil {
			span.TagsString = make(map[string]string)
		}
		span.TagsString[key] = value.AsString()
	}
}

type jwtToken struct {
	jwt     string
	spaceID int64
}

type tokenRequest struct {
	client *http.Client
	host   string
	ak     string
	sk     string
	region string
	psm    string
}

func fetchJWTToken(ctx context.Context, input tokenRequest) (jwtToken, error) {
	client := input.client
	if input.client == nil {
		client = http.DefaultClient
	}
	requestBody := authRequest{
		PSM:     input.psm,
		IsTCE:   true,
		Env:     os.Getenv("ENV"),
		IsBOE:   strings.EqualFold(strings.TrimSpace(input.region), "BOE"),
		Stage:   os.Getenv("TCE_STAGE"),
		Payload: "",
	}
	body, err := json.Marshal(requestBody)
	if err != nil {
		return jwtToken{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(input.host, "/")+authPath, strings.NewReader(string(body)))
	if err != nil {
		return jwtToken{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Agw-Js-Conv", "str")
	request.Header.Set("Fornax-Auth", genAuthSignature(input.ak, input.sk, body, time.Now()))
	response, err := client.Do(request)
	if err != nil {
		return jwtToken{}, err
	}
	defer response.Body.Close()

	var authResponse authResponse
	if err := json.NewDecoder(response.Body).Decode(&authResponse); err != nil {
		return jwtToken{}, err
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return jwtToken{}, fmt.Errorf("authenticate HTTP %d: %s", response.StatusCode, authResponse.message())
	}
	if authResponse.Code != nil && *authResponse.Code != 0 {
		return jwtToken{}, fmt.Errorf("authenticate code %d: %s", *authResponse.Code, authResponse.message())
	}
	if authResponse.BaseResp != nil && authResponse.BaseResp.StatusCode != 0 {
		return jwtToken{}, fmt.Errorf("authenticate base response code %d: %s", authResponse.BaseResp.StatusCode, authResponse.BaseResp.StatusMessage)
	}
	spaceID, err := spaceIDFromJWT(authResponse.JWTToken)
	if err != nil {
		return jwtToken{}, err
	}
	return jwtToken{jwt: authResponse.JWTToken, spaceID: spaceID}, nil
}

func copyMetadata(metadata map[string]string) map[string]string {
	if len(metadata) == 0 {
		return nil
	}
	copied := make(map[string]string, len(metadata))
	for key, value := range metadata {
		copied[key] = value
	}
	return copied
}

func effectivePSM(psm string) string {
	psm = strings.TrimSpace(psm)
	if psm == "" {
		return "unknown_psm"
	}
	return psm
}

func traceTags(config authConfig) []attribute.KeyValue {
	return tagAttributes(config.psm, config.userID, config.deviceID, config.metadata)
}

func tagAttributes(psm, userID, deviceID string, metadata map[string]string) []attribute.KeyValue {
	tags := make([]attribute.KeyValue, 0, len(metadata)+traceTagDefaultCount)
	for key, value := range metadata {
		key = strings.TrimSpace(key)
		if key == "" || reservedTraceAttribute(key) {
			continue
		}
		tags = append(tags, attribute.String(key, value))
	}
	if psm := strings.TrimSpace(psm); psm != "" {
		tags = append(tags, attribute.String(psmAttribute, psm))
	}
	if userID := strings.TrimSpace(userID); userID != "" {
		tags = append(tags, attribute.String(userIDAttribute, userID))
	}
	if deviceID := strings.TrimSpace(deviceID); deviceID != "" {
		tags = append(tags, attribute.String(deviceIDAttribute, deviceID))
	}
	return tags
}

func requestTags(ctx context.Context, defaults []attribute.KeyValue) []attribute.KeyValue {
	config := traceContext(ctx)
	if config.userID == "" && config.deviceID == "" && len(config.metadata) == 0 {
		return defaults
	}
	tags := append([]attribute.KeyValue{}, defaults...)
	tags = append(tags, tagAttributes("", config.userID, config.deviceID, config.metadata)...)
	return tags
}

func reservedTraceAttribute(key string) bool {
	switch key {
	case spanTypeAttribute, inputAttribute, outputAttribute, statusAttribute, cutOffAttribute:
		return true
	default:
		return false
	}
}

type authRequest struct {
	PSM      string `json:"psm"`
	Cluster  string `json:"cluster"`
	Env      string `json:"env"`
	IsBOE    bool   `json:"isBOE"`
	IsTCE    bool   `json:"isTCE"`
	Payload  string `json:"payload"`
	ZTIToken string `json:"ztiToken"`
	Stage    string `json:"stage"`
}

type authResponse struct {
	JWTToken string    `json:"jwtToken"`
	Code     *int32    `json:"code,omitempty"`
	Msg      *string   `json:"msg,omitempty"`
	BaseResp *baseResp `json:"baseResp,omitempty"`
}

type baseResp struct {
	StatusCode    int32  `json:"statusCode,omitempty"`
	StatusMessage string `json:"statusMessage,omitempty"`
}

func (r authResponse) message() string {
	if r.Msg != nil {
		return *r.Msg
	}
	if r.BaseResp != nil {
		return r.BaseResp.StatusMessage
	}
	return ""
}

func genAuthSignature(ak, sk string, payload []byte, now time.Time) string {
	signKeyInfo := fmt.Sprintf("%s/%s/%d/%d", authVersion, ak, now.Unix(), authTTLSeconds)
	signKey := sha256HMAC([]byte(sk), []byte(signKeyInfo))
	signResult := sha256HMAC([]byte(signKey), payload)
	return fmt.Sprintf("%s/%s", signKeyInfo, signResult)
}

func sha256HMAC(key, data []byte) string {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(data)
	return fmt.Sprintf("%x", mac.Sum(nil))
}

func spaceIDFromJWT(token string) (int64, error) {
	parts := strings.Split(token, ".")
	if len(parts) < jwtMinParts {
		return 0, errors.New("fornax: authentication returned a malformed JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return 0, fmt.Errorf("fornax: decode JWT payload: %w", err)
	}
	var claims struct {
		SpaceID int64 `json:"space_id"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return 0, fmt.Errorf("fornax: parse JWT payload: %w", err)
	}
	if claims.SpaceID <= 0 {
		return 0, errors.New("fornax: JWT payload does not contain a valid space_id")
	}
	return claims.SpaceID, nil
}

func hostForRegion(region string) string {
	switch strings.TrimSpace(region) {
	case "", "CN", "BOE":
		return "https://fornax.bytedance.net"
	case "SG", "BOEI18N":
		return "https://fornax.byteintl.net"
	case "US":
		return "https://fornax-va.byteintl.net"
	case "Asia-SouthEastBD":
		return "https://fornax-i18nbd.byteintl.net"
	case "I18N-DEV":
		return "https://fornax-i18n.byteintl.net"
	default:
		return "https://fornax.bytedance.net"
	}
}

func validateSpaceID(spaceID string) error {
	if _, err := strconv.ParseUint(spaceID, decimalBase, spaceIDBitSize); err != nil || spaceID == "0" {
		return errors.New("fornax: space ID must be a positive integer")
	}
	return nil
}

func newMiddleware(provider *sdktrace.TracerProvider, tags []attribute.KeyValue) *Middleware {
	return &Middleware{provider: provider, tracer: provider.Tracer(instrumentationName), tags: tags}
}

// Use wraps target with Fornax tracing.
func (m *Middleware) Use(target agent.Agent) agent.Agent {
	if streaming, ok := target.(agent.StreamingAgent); ok {
		return m.UseStreaming(streaming)
	}
	return &tracedAgent{middleware: m, target: target}
}

// UseStreaming wraps a streaming target without removing its streaming API.
func (m *Middleware) UseStreaming(target agent.StreamingAgent) agent.StreamingAgent {
	traced := &tracedAgent{middleware: m, target: target}
	return &tracedStreamingAgent{tracedAgent: traced, streaming: target}
}

// Close flushes pending spans and releases exporter resources.
func (m *Middleware) Close(ctx context.Context) error {
	if m == nil || m.provider == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	m.closeOnce.Do(func() {
		m.closeErr = m.provider.Shutdown(ctx)
	})
	return m.closeErr
}

type tracedAgent struct {
	middleware *Middleware
	target     agent.Agent
}

func (a *tracedAgent) Identity() agent.Identity {
	if a == nil || a.target == nil {
		return agent.Identity{}
	}
	return a.target.Identity()
}

func (a *tracedAgent) Invoke(ctx context.Context, request agent.Request, options ...gopact.RunOption) (response agent.Response, err error) {
	spanCtx, root, agentSpan, sink, inputCutOff, err := a.startTrace(ctx, request)
	if err != nil {
		return agent.Response{}, err
	}
	defer func() {
		sink.finish(err)
		agentSpan.End()
		root.End()
	}()

	options = append(options, gopact.WithEventSink(sink))
	response, err = a.target.Invoke(spanCtx, request, options...)
	finishAgent(agentSpan, response, err)
	finishRoot(root, response, err, inputCutOff)
	return response, err
}

func (a *tracedAgent) startTrace(ctx context.Context, request agent.Request) (context.Context, trace.Span, trace.Span, *eventSink, bool, error) {
	if a == nil || a.middleware == nil || a.middleware.tracer == nil {
		return nil, nil, nil, nil, false, errors.New("fornax: middleware is nil")
	}
	if a.target == nil {
		return nil, nil, nil, nil, false, errors.New("fornax: target agent is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	tags := requestTags(ctx, a.middleware.tags)
	identity := a.target.Identity()
	name := identity.Name
	if name == "" {
		name = "agent"
	}
	attributes := []attribute.KeyValue{
		attribute.String(spanTypeAttribute, rootSpanType),
		attribute.String("agent_name", name),
	}
	attributes = append(attributes, tags...)
	input, inputCutOff := traceJSON(fornaxQueryInput(request))
	if input != "" {
		attributes = append(attributes, attribute.String(inputAttribute, input))
	}
	if inputCutOff {
		attributes = append(attributes, attribute.String(cutOffAttribute, `["input"]`))
	}
	rootCtx, root := a.middleware.tracer.Start(ctx, name, trace.WithAttributes(attributes...))
	agentCtx, agentSpan := a.middleware.tracer.Start(rootCtx, name, trace.WithAttributes(
		append([]attribute.KeyValue{
			attribute.String(spanTypeAttribute, agentSpanType),
			attribute.String("agent_name", name),
		}, tags...)...,
	))
	setTraceJSON(agentSpan, inputAttribute, agentInput(request), new(bool))
	return agentCtx, root, agentSpan, newEventSink(a.middleware.tracer, agentCtx, root, tags), inputCutOff, nil
}

func finishRoot(root trace.Span, output any, err error, inputCutOff bool) {
	if err != nil {
		markError(root, err)
		return
	}
	root.SetAttributes(attribute.Int(statusAttribute, 0))
	var rootOutput any
	switch response := output.(type) {
	case agent.Response:
		rootOutput = fornaxQueryOutput(response)
	default:
		rootOutput = queryPayload{Contents: textContents(valueText(response))}
	}
	encoded, outputCutOff := traceJSON(rootOutput)
	if encoded != "" {
		root.SetAttributes(attribute.String(outputAttribute, encoded))
	}
	if outputCutOff {
		setCutOff(root, inputCutOff, true)
	}
}

func finishAgent(span trace.Span, response agent.Response, err error) {
	if err != nil {
		markError(span, err)
		return
	}
	span.SetAttributes(attribute.Int(statusAttribute, 0))
	setTraceJSON(span, outputAttribute, agentOutput(response), new(bool))
}

func traceJSON(value any) (string, bool) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", false
	}
	if len(encoded) > maxTraceFieldBytes {
		return "", true
	}
	return string(encoded), false
}

func setCutOff(span trace.Span, input, output bool) {
	switch {
	case input && output:
		span.SetAttributes(attribute.String(cutOffAttribute, `["input","output"]`))
	case input:
		span.SetAttributes(attribute.String(cutOffAttribute, `["input"]`))
	case output:
		span.SetAttributes(attribute.String(cutOffAttribute, `["output"]`))
	}
}

type tracedStreamingAgent struct {
	*tracedAgent
	streaming agent.StreamingAgent
}

func (a *tracedStreamingAgent) InvokeStream(ctx context.Context, request agent.Request, options ...gopact.RunOption) iter.Seq2[agent.Chunk, error] {
	return func(yield func(agent.Chunk, error) bool) {
		if a == nil || a.streaming == nil || a.tracedAgent == nil {
			yield(agent.Chunk{}, errors.New("fornax: target streaming agent is nil"))
			return
		}
		spanCtx, root, agentSpan, sink, inputCutOff, err := a.startTrace(ctx, request)
		if err != nil {
			yield(agent.Chunk{}, err)
			return
		}
		output := newStreamOutput()
		var streamErr error
		completed := false
		defer func() {
			if !completed && streamErr == nil {
				streamErr = spanCtx.Err()
				if streamErr == nil {
					streamErr = context.Canceled
				}
			}
			finishAgent(agentSpan, agent.Response{Message: gopact.Message{
				Role:  gopact.MessageRoleAssistant,
				Parts: []gopact.MessagePart{{Type: gopact.MessagePartTypeText, Text: string(output.value())}},
			}}, streamErr)
			finishRoot(root, output.value(), streamErr, inputCutOff)
			if output.truncated {
				setCutOff(root, inputCutOff, true)
			}
			sink.finish(streamErr)
			agentSpan.End()
			root.End()
		}()

		options = append(options, gopact.WithEventSink(sink))
		for chunk, itemErr := range a.streaming.InvokeStream(spanCtx, request, options...) {
			if itemErr != nil {
				streamErr = itemErr
				yield(chunk, itemErr)
				return
			}
			output.add(chunk)
			if !yield(chunk, nil) {
				return
			}
		}
		completed = true
	}
}

type streamOutput struct {
	data      []byte
	truncated bool
}

func newStreamOutput() *streamOutput {
	return &streamOutput{data: []byte{'['}}
}

func (o *streamOutput) add(chunk agent.Chunk) {
	if o.truncated {
		return
	}
	separatorBytes := 0
	if len(o.data) > 1 {
		separatorBytes = 1
	}
	remaining := maxTraceFieldBytes - len(o.data) - separatorBytes - 1
	if remaining < 0 || !chunkMayFit(chunk, remaining) {
		o.truncated = true
		return
	}
	encoded, err := json.Marshal(chunk)
	if err != nil || len(encoded) > remaining {
		o.truncated = true
		return
	}
	if separatorBytes > 0 {
		o.data = append(o.data, ',')
	}
	o.data = append(o.data, encoded...)
}

func (o *streamOutput) value() json.RawMessage {
	return append(o.data, ']')
}

func chunkMayFit(chunk agent.Chunk, remaining int) bool {
	const (
		emptyChunkBytes       = len(`{"Text":"","Parts":[]}`)
		emptyPartBytes        = len(`{"Type":"","Text":"","Ref":null}`)
		emptyArtifactRefBytes = len(`{"URI":"","Kind":"","Digest":""}`)
		nullBytes             = len("null")
	)
	if !consumeBytes(&remaining, emptyChunkBytes) || !consumeBytes(&remaining, len(chunk.Text)) {
		return false
	}
	for _, part := range chunk.Parts {
		if !consumeBytes(&remaining, emptyPartBytes) ||
			!consumeBytes(&remaining, len(part.Type)) ||
			!consumeBytes(&remaining, len(part.Text)) {
			return false
		}
		if part.Ref != nil && (!consumeBytes(&remaining, emptyArtifactRefBytes-nullBytes) ||
			!consumeBytes(&remaining, len(part.Ref.URI)) ||
			!consumeBytes(&remaining, len(part.Ref.Kind)) ||
			!consumeBytes(&remaining, len(part.Ref.Digest))) {
			return false
		}
	}
	return true
}

type queryPayload struct {
	Contents []queryContent `json:"contents,omitempty"`
}

type queryContent struct {
	ContentType string `json:"content_type,omitempty"`
	Text        string `json:"text,omitempty"`
}

type modelInputPayload struct {
	Messages   []modelMessagePayload `json:"messages,omitempty"`
	Tools      []modelToolPayload    `json:"tools,omitempty"`
	ToolChoice *modelToolChoice      `json:"tool_choice,omitempty"`
}

type modelOutputPayload struct {
	Choices []modelChoicePayload `json:"choices"`
}

type modelChoicePayload struct {
	FinishReason string              `json:"finish_reason"`
	Index        int64               `json:"index"`
	Message      modelMessagePayload `json:"message"`
}

type modelMessagePayload struct {
	Role       string                 `json:"role"`
	Content    string                 `json:"content,omitempty"`
	Parts      []modelPartPayload     `json:"parts,omitempty"`
	ToolCalls  []modelToolCallPayload `json:"tool_calls,omitempty"`
	ToolCallID string                 `json:"tool_call_id,omitempty"`
	Name       string                 `json:"name,omitempty"`
}

type modelPartPayload struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type modelToolPayload struct {
	Type     string                   `json:"type"`
	Function modelToolFunctionPayload `json:"function"`
}

type modelToolFunctionPayload struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
	Arguments   string          `json:"arguments,omitempty"`
}

type modelToolCallPayload struct {
	ID       string                   `json:"id,omitempty"`
	Type     string                   `json:"type,omitempty"`
	Function modelToolFunctionPayload `json:"function"`
}

type modelToolChoice struct {
	Type     string                   `json:"type"`
	Function *modelToolChoiceFunction `json:"function,omitempty"`
}

type modelToolChoiceFunction struct {
	Name string `json:"name"`
}

func fornaxQueryInput(request agent.Request) queryPayload {
	return queryPayload{Contents: textContents(messagesText(request.Messages))}
}

func fornaxQueryOutput(response agent.Response) queryPayload {
	return queryPayload{Contents: textContents(messageText(response.Message))}
}

func agentInput(request agent.Request) string {
	return messagesText(request.Messages)
}

func agentOutput(response agent.Response) string {
	return messageText(response.Message)
}

func textContents(text string) []queryContent {
	if text == "" {
		return nil
	}
	return []queryContent{{ContentType: "text", Text: text}}
}

func messagesText(messages []gopact.Message) string {
	var builder strings.Builder
	for _, message := range messages {
		text := messageText(message)
		if text == "" {
			continue
		}
		if builder.Len() > 0 {
			builder.WriteByte('\n')
		}
		if message.Role != "" {
			builder.WriteString(message.Role)
			builder.WriteString(": ")
		}
		builder.WriteString(text)
	}
	return builder.String()
}

func messageText(message gopact.Message) string {
	var builder strings.Builder
	for _, part := range message.Parts {
		if part.Text == "" {
			continue
		}
		if builder.Len() > 0 {
			builder.WriteByte('\n')
		}
		builder.WriteString(part.Text)
	}
	return builder.String()
}

func valueText(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []byte:
		return string(typed)
	case json.RawMessage:
		return string(typed)
	default:
		return marshalText(typed)
	}
}

func marshalText(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func modelInput(request gopact.ModelRequest) modelInputPayload {
	payload := modelInputPayload{
		Messages: modelMessages(request.Messages),
		Tools:    modelTools(request.Tools),
	}
	if request.ToolChoice.Mode != "" {
		choiceType := request.ToolChoice.Mode
		if choiceType == gopact.ToolChoiceModeNamed {
			choiceType = "function"
		}
		payload.ToolChoice = &modelToolChoice{Type: choiceType}
		if request.ToolChoice.Name != "" {
			payload.ToolChoice.Function = &modelToolChoiceFunction{Name: request.ToolChoice.Name}
		}
	}
	return payload
}

func modelOutput(response gopact.ModelResponse) modelOutputPayload {
	message := modelMessage(response.Message)
	if intent, ok := response.Intent.(gopact.ToolCallIntent); ok {
		message.ToolCalls = modelToolCalls(intent.Calls)
	}
	if intent, ok := response.Intent.(*gopact.ToolCallIntent); ok && intent != nil {
		message.ToolCalls = modelToolCalls(intent.Calls)
	}
	return modelOutputPayload{Choices: []modelChoicePayload{{
		FinishReason: response.FinishReason,
		Index:        0,
		Message:      message,
	}}}
}

func modelMessages(messages []gopact.Message) []modelMessagePayload {
	if len(messages) == 0 {
		return nil
	}
	out := make([]modelMessagePayload, 0, len(messages))
	for _, message := range messages {
		out = append(out, modelMessage(message))
	}
	return out
}

func modelMessage(message gopact.Message) modelMessagePayload {
	payload := modelMessagePayload{
		Role:    message.Role,
		Content: messageText(message),
	}
	if !messagePartsAreTextOnly(message.Parts) {
		payload.Parts = modelParts(message.Parts)
	}
	return payload
}

func messagePartsAreTextOnly(parts []gopact.MessagePart) bool {
	for _, part := range parts {
		if part.Type != "" && part.Type != gopact.MessagePartTypeText {
			return false
		}
	}
	return true
}

func modelParts(parts []gopact.MessagePart) []modelPartPayload {
	if len(parts) == 0 {
		return nil
	}
	out := make([]modelPartPayload, 0, len(parts))
	for _, part := range parts {
		if part.Text == "" {
			continue
		}
		partType := part.Type
		if partType == "" {
			partType = gopact.MessagePartTypeText
		}
		out = append(out, modelPartPayload{Type: partType, Text: part.Text})
	}
	return out
}

func modelTools(tools []gopact.ToolSpec) []modelToolPayload {
	if len(tools) == 0 {
		return nil
	}
	out := make([]modelToolPayload, 0, len(tools))
	for _, tool := range tools {
		out = append(out, modelToolPayload{
			Type: "function",
			Function: modelToolFunctionPayload{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.Schema,
			},
		})
	}
	return out
}

func modelToolCall(call gopact.ToolCall) modelToolCallPayload {
	return modelToolCallPayload{
		ID:   call.ID,
		Type: "function",
		Function: modelToolFunctionPayload{
			Name:      call.Name,
			Arguments: string(call.Arguments),
		},
	}
}

func modelToolCalls(calls []gopact.ToolCall) []modelToolCallPayload {
	if len(calls) == 0 {
		return nil
	}
	out := make([]modelToolCallPayload, 0, len(calls))
	for _, call := range calls {
		out = append(out, modelToolCall(call))
	}
	return out
}

func toolInput(call gopact.ToolCall) any {
	if len(call.Arguments) == 0 {
		return map[string]string{"tool_call_id": call.ID, "tool_name": call.Name}
	}
	var decoded any
	if err := json.Unmarshal(call.Arguments, &decoded); err == nil {
		return decoded
	}
	return string(call.Arguments)
}

func toolOutput(outcome gopact.ToolOutcome) any {
	if result, ok := outcome.(gopact.ToolResultOutcome); ok {
		return result.Result.Preview
	}
	if result, ok := outcome.(*gopact.ToolResultOutcome); ok && result != nil {
		return result.Result.Preview
	}
	return outcome
}

func consumeBytes(remaining *int, count int) bool {
	if count > *remaining {
		return false
	}
	*remaining -= count
	return true
}

type spanState struct {
	ctx  context.Context
	span trace.Span
	root bool
}

type nodeSpanState struct {
	span         trace.Span
	inputCutOff  bool
	outputCutOff bool
	failed       bool
}

type eventSink struct {
	tracer  trace.Tracer
	rootCtx context.Context
	root    trace.Span
	tags    []attribute.KeyValue

	mu        sync.Mutex
	rootRunID string
	runs      map[string]spanState
	nodes     map[string]nodeSpanState
}

func newEventSink(tracer trace.Tracer, rootCtx context.Context, root trace.Span, tags []attribute.KeyValue) *eventSink {
	return &eventSink{
		tracer: tracer, rootCtx: rootCtx, root: root, tags: tags,
		runs: make(map[string]spanState), nodes: make(map[string]nodeSpanState),
	}
}

func (s *eventSink) Emit(_ context.Context, event gopact.Event) error {
	if s == nil || s.tracer == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch event.Type {
	case workflow.EventWorkflowStarted, workflow.EventWorkflowResumed,
		workflow.EventWorkflowRetryStarted, workflow.EventWorkflowJumpStarted:
		s.startRun(event)
	case workflow.EventWorkflowCompleted, workflow.EventWorkflowFailed,
		workflow.EventWorkflowCanceled, workflow.EventWorkflowTerminated,
		workflow.EventWorkflowInterrupted:
		s.finishRun(event)
	case workflow.EventNodeStarted:
		s.startNode(event)
	case workflow.EventNodeRetrying, workflow.EventNodeCompleted,
		workflow.EventNodeCanceled, workflow.EventNodeSuperseded,
		workflow.EventNodeSkipped, workflow.EventNodeFailed:
		s.finishNode(event)
	}
	return nil
}

func (s *eventSink) startRun(event gopact.Event) {
	if event.RunID == "" {
		return
	}
	if _, exists := s.runs[event.RunID]; exists {
		return
	}
	if s.rootRunID == "" && event.ParentRunID == "" {
		s.rootRunID = event.RunID
		s.root.SetAttributes(runAttributes(event, rootSpanType)...)
		s.runs[event.RunID] = spanState{ctx: s.rootCtx, span: s.root, root: true}
		return
	}
	parent := s.rootCtx
	if parentRun, exists := s.runs[event.ParentRunID]; exists {
		parent = parentRun.ctx
	}
	name := event.DefinitionID
	if name == "" {
		name = "agent"
	}
	ctx, span := s.tracer.Start(parent, name,
		trace.WithTimestamp(eventTime(event)),
		trace.WithAttributes(append(runAttributes(event, agentSpanType), s.tags...)...),
	)
	s.runs[event.RunID] = spanState{ctx: ctx, span: span}
}

func (s *eventSink) finishRun(event gopact.Event) {
	state, exists := s.runs[event.RunID]
	if !exists {
		return
	}
	if event.Type == workflow.EventWorkflowCompleted {
		state.span.SetAttributes(attribute.Int(statusAttribute, 0))
	} else {
		markError(state.span, errors.New(event.Type))
	}
	if state.root {
		return
	}
	state.span.End(trace.WithTimestamp(eventTime(event)))
	delete(s.runs, event.RunID)
}

func (s *eventSink) startNode(event gopact.Event) {
	key := nodeKey(event)
	if key == "" {
		return
	}
	if _, exists := s.nodes[key]; exists {
		return
	}
	parent := s.rootCtx
	if run, exists := s.runs[event.RunID]; exists {
		parent = run.ctx
	}
	name := event.NodeID
	if name == "" {
		name = "node"
	}
	_, span := s.tracer.Start(parent, name,
		trace.WithTimestamp(eventTime(event)),
		trace.WithAttributes(append(nodeAttributes(event), s.tags...)...),
	)
	s.nodes[key] = nodeSpanState{span: span}
}

func (s *eventSink) finishNode(event gopact.Event) {
	key := nodeKey(event)
	state, exists := s.nodes[key]
	if !exists {
		return
	}
	if (event.Type == workflow.EventNodeCompleted || event.Type == workflow.EventNodeSkipped) && !state.failed {
		state.span.SetAttributes(attribute.Int(statusAttribute, 0))
	} else {
		markError(state.span, errors.New(event.Type))
	}
	state.span.End(trace.WithTimestamp(eventTime(event)))
	delete(s.nodes, key)
}

func (s *eventSink) finish(err error) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, state := range s.nodes {
		if err != nil {
			markError(state.span, err)
		}
		state.span.End()
		delete(s.nodes, key)
	}
	for runID, state := range s.runs {
		if state.root {
			continue
		}
		if err != nil {
			markError(state.span, err)
		}
		state.span.End()
		delete(s.runs, runID)
	}
}

func (s *eventSink) EmitModelEvent(ctx context.Context, event gopact.ModelEvent) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key, state, ok := s.activeNode(ctx)
	if !ok {
		return nil
	}
	switch event.Type {
	case gopact.ModelEventCallStarted:
		startModelSpan(state.span, event.Request, &state.inputCutOff)
	case gopact.ModelEventCallFinished:
		if finishModelSpan(state.span, event, &state.outputCutOff) {
			state.failed = true
		}
	case gopact.ModelEventUsage:
		if event.Response != nil {
			setUsageAttributes(state.span, event.Response.Usage)
		}
	}
	setCutOff(state.span, state.inputCutOff, state.outputCutOff)
	s.nodes[key] = state
	return nil
}

func startModelSpan(span trace.Span, request *gopact.ModelRequest, inputCutOff *bool) {
	span.SetAttributes(attribute.String(spanTypeAttribute, modelSpanType))
	if request == nil {
		return
	}
	setTraceJSON(span, inputAttribute, modelInput(*request), inputCutOff)
	if request.Model != "" {
		span.SetAttributes(attribute.String(modelNameAttribute, request.Model))
	}
}

func finishModelSpan(span trace.Span, event gopact.ModelEvent, outputCutOff *bool) bool {
	if event.Response != nil {
		setModelResponseAttributes(span, *event.Response, outputCutOff)
	}
	if event.Err == nil {
		return false
	}
	markError(span, event.Err)
	return true
}

func (s *eventSink) EmitToolEvent(ctx context.Context, event gopact.ToolEvent) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key, state, ok := s.activeNode(ctx)
	if !ok {
		return nil
	}
	switch event.Type {
	case gopact.ToolEventCallStarted:
		state.span.SetAttributes(
			attribute.String(spanTypeAttribute, toolSpanType),
			attribute.String(toolNameAttribute, event.Call.Name),
			attribute.String(toolCallIDAttribute, event.Call.ID),
		)
		setTraceJSON(state.span, inputAttribute, toolInput(event.Call), &state.inputCutOff)
	case gopact.ToolEventCallFinished:
		setTraceJSON(state.span, outputAttribute, toolOutput(event.Outcome), &state.outputCutOff)
		if event.Err != nil {
			markError(state.span, event.Err)
			state.failed = true
		} else if _, failed := event.Outcome.(gopact.ToolErrorOutcome); failed {
			markError(state.span, errors.New("tool error outcome"))
			state.failed = true
		} else if value, failed := event.Outcome.(*gopact.ToolErrorOutcome); failed && value != nil {
			markError(state.span, errors.New("tool error outcome"))
			state.failed = true
		}
	}
	setCutOff(state.span, state.inputCutOff, state.outputCutOff)
	s.nodes[key] = state
	return nil
}

func (s *eventSink) activeNode(ctx context.Context) (string, nodeSpanState, bool) {
	info := workflow.RunInfoFromContext(ctx)
	if info.RunID == "" || info.ActivationID == "" {
		return "", nodeSpanState{}, false
	}
	activationID := info.RunID + "/" + info.ActivationID
	key := info.RunID + "\x00" + activationID
	if info.Attempt > 0 {
		key = info.RunID + "\x00" + activationID + "/attempt-" + strconv.Itoa(info.Attempt)
	}
	state, ok := s.nodes[key]
	return key, state, ok
}

func setModelResponseAttributes(span trace.Span, response gopact.ModelResponse, outputCutOff *bool) {
	setTraceJSON(span, outputAttribute, modelOutput(response), outputCutOff)
	setUsageAttributes(span, response.Usage)
	if response.FinishReason != "" {
		span.SetAttributes(attribute.String(finishReasonAttribute, response.FinishReason))
	}
}

func setUsageAttributes(span trace.Span, usage gopact.Usage) {
	if usage.InputTokens != 0 {
		span.SetAttributes(attribute.Int(inputTokensAttribute, usage.InputTokens))
	}
	if usage.OutputTokens != 0 {
		span.SetAttributes(attribute.Int(outputTokensAttribute, usage.OutputTokens))
	}
	if usage.TotalTokens != 0 {
		span.SetAttributes(attribute.Int(totalTokensAttribute, usage.TotalTokens))
	}
}

func setTraceJSON(span trace.Span, key string, value any, cutOff *bool) {
	encoded, truncated := traceJSON(value)
	if encoded != "" {
		span.SetAttributes(attribute.String(key, encoded))
	}
	if truncated {
		*cutOff = true
	}
}

func runAttributes(event gopact.Event, spanType string) []attribute.KeyValue {
	attributes := []attribute.KeyValue{
		attribute.String(spanTypeAttribute, spanType),
		attribute.String("gopact.run_id", event.RunID),
	}
	if event.DefinitionID != "" {
		attributes = append(attributes, attribute.String("agent_name", event.DefinitionID))
	}
	if spanType == rootSpanType {
		attributes = append(attributes, attribute.String(messageIDAttribute, event.RunID))
		if event.SessionID != "" {
			attributes = append(attributes, attribute.String(threadIDAttribute, event.SessionID))
		}
	}
	if event.ParentRunID != "" {
		attributes = append(attributes, attribute.String("gopact.parent_run_id", event.ParentRunID))
	}
	return attributes
}

func nodeAttributes(event gopact.Event) []attribute.KeyValue {
	attributes := []attribute.KeyValue{
		attribute.String(spanTypeAttribute, nodeSpanType(event.NodeID)),
		attribute.String("gopact.run_id", event.RunID),
		attribute.String("gopact.node_id", event.NodeID),
		attribute.String("gopact.activation_id", event.ActivationID),
		attribute.String("gopact.attempt_id", event.AttemptID),
	}
	if nodeSpanType(event.NodeID) == "tool" {
		attributes = append(attributes, attribute.String(toolNameAttribute, event.NodeID))
	}
	return attributes
}

func nodeSpanType(nodeID string) string {
	switch strings.ToLower(nodeID) {
	case "model":
		return "model"
	case "tool":
		return "tool"
	default:
		return "graph"
	}
}

func nodeKey(event gopact.Event) string {
	if event.AttemptID != "" {
		return event.RunID + "\x00" + event.AttemptID
	}
	if event.ActivationID != "" {
		return event.RunID + "\x00" + event.ActivationID
	}
	return ""
}

func eventTime(event gopact.Event) time.Time {
	if event.Timestamp.IsZero() {
		return time.Now()
	}
	return event.Timestamp
}

func markError(span trace.Span, err error) {
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
	span.SetAttributes(
		attribute.String("error", err.Error()),
		attribute.Int(statusAttribute, failedStatusCode),
	)
}
