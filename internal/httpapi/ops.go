package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	StaticBearerAuthModeDisabled     = "disabled"
	StaticBearerAuthModeStaticBearer = "static_bearer"
)

type StaticBearerAuthConfig struct {
	Mode         string
	BearerTokens []string

	tokenSubjects map[string]string
}

func normalizeStaticBearerAuthConfig(cfg StaticBearerAuthConfig) (StaticBearerAuthConfig, error) {
	cfg.Mode = strings.ToLower(strings.TrimSpace(cfg.Mode))
	if cfg.Mode == "" {
		cfg.Mode = StaticBearerAuthModeDisabled
	}
	switch cfg.Mode {
	case StaticBearerAuthModeDisabled:
		cfg.BearerTokens = nil
		cfg.tokenSubjects = nil
		return cfg, nil
	case StaticBearerAuthModeStaticBearer:
	default:
		return StaticBearerAuthConfig{}, fmt.Errorf("unsupported shim.auth.mode %q", cfg.Mode)
	}

	tokenSubjects := make(map[string]string)
	normalized := make([]string, 0, len(cfg.BearerTokens))
	for _, candidate := range cfg.BearerTokens {
		token := strings.TrimSpace(candidate)
		if token == "" {
			continue
		}
		if _, ok := tokenSubjects[token]; ok {
			continue
		}
		tokenSubjects[token] = "token_" + authTokenFingerprint(token)
		normalized = append(normalized, token)
	}
	if len(tokenSubjects) == 0 {
		return StaticBearerAuthConfig{}, errors.New("shim.auth.bearer_tokens must not be empty when shim.auth.mode=static_bearer")
	}
	cfg.BearerTokens = normalized
	cfg.tokenSubjects = tokenSubjects
	return cfg, nil
}

func (c StaticBearerAuthConfig) Enabled() bool {
	return c.Mode == StaticBearerAuthModeStaticBearer && len(c.tokenSubjects) > 0
}

func (c StaticBearerAuthConfig) subjectForToken(token string) (string, bool) {
	subject, ok := c.tokenSubjects[strings.TrimSpace(token)]
	return subject, ok
}

type RateLimitConfig struct {
	Enabled           bool
	RequestsPerMinute int
	Burst             int
}

func normalizeRateLimitConfig(cfg RateLimitConfig) (RateLimitConfig, error) {
	if !cfg.Enabled {
		return RateLimitConfig{}, nil
	}
	if cfg.RequestsPerMinute <= 0 {
		return RateLimitConfig{}, errors.New("shim.rate_limit.requests_per_minute must be greater than zero when rate limiting is enabled")
	}
	if cfg.Burst <= 0 {
		cfg.Burst = min(60, cfg.RequestsPerMinute)
		if cfg.Burst <= 0 {
			cfg.Burst = 1
		}
	}
	return cfg, nil
}

type MetricsConfig struct {
	Enabled bool
	Path    string
}

func normalizeMetricsConfig(cfg MetricsConfig) MetricsConfig {
	if !cfg.Enabled {
		cfg.Path = ""
		return cfg
	}
	if strings.TrimSpace(cfg.Path) == "" {
		cfg.Path = "/metrics"
	}
	if !strings.HasPrefix(cfg.Path, "/") {
		cfg.Path = "/" + cfg.Path
	}
	return cfg
}

type ServiceLimits struct {
	JSONBodyBytes                     int64
	RetrievalFileUploadBytes          int64
	ChatCompletionsShadowStoreBytes   int64
	ChatCompletionsShadowStoreTimeout time.Duration
	ResponsesProxyBufferBytes         int64
	CustomToolGrammarDefinitionBytes  int64
	CustomToolCompiledPatternBytes    int64
	RetrievalMaxConcurrentSearches    int
	RetrievalMaxSearchQueries         int
	RetrievalMaxGroundingChunks       int
	CodeInterpreterMaxConcurrentRuns  int
}

func normalizeServiceLimits(limits ServiceLimits) ServiceLimits {
	if limits.JSONBodyBytes <= 0 {
		limits.JSONBodyBytes = 1 << 20
	}
	if limits.RetrievalFileUploadBytes <= 0 {
		limits.RetrievalFileUploadBytes = 64 << 20
	}
	if limits.ChatCompletionsShadowStoreBytes <= 0 {
		limits.ChatCompletionsShadowStoreBytes = 64 << 20
	}
	if limits.ChatCompletionsShadowStoreTimeout <= 0 {
		limits.ChatCompletionsShadowStoreTimeout = 5 * time.Second
	}
	if limits.ResponsesProxyBufferBytes <= 0 {
		limits.ResponsesProxyBufferBytes = 64 << 20
	}
	if limits.CustomToolGrammarDefinitionBytes <= 0 {
		limits.CustomToolGrammarDefinitionBytes = 16 << 10
	}
	if limits.CustomToolCompiledPatternBytes <= 0 {
		limits.CustomToolCompiledPatternBytes = 32 << 10
	}
	if limits.RetrievalMaxConcurrentSearches <= 0 {
		limits.RetrievalMaxConcurrentSearches = 8
	}
	if limits.RetrievalMaxSearchQueries <= 0 {
		limits.RetrievalMaxSearchQueries = 4
	}
	if limits.RetrievalMaxGroundingChunks <= 0 {
		limits.RetrievalMaxGroundingChunks = 20
	}
	if limits.CodeInterpreterMaxConcurrentRuns <= 0 {
		limits.CodeInterpreterMaxConcurrentRuns = 2
	}
	return limits
}

type LocalCodeInterpreterLimits struct {
	GeneratedFiles       int
	GeneratedFileBytes   int
	GeneratedTotalBytes  int
	RemoteInputFileBytes int
}

func normalizeLocalCodeInterpreterLimits(limits LocalCodeInterpreterLimits) LocalCodeInterpreterLimits {
	if limits.GeneratedFiles <= 0 {
		limits.GeneratedFiles = 8
	}
	if limits.GeneratedFileBytes <= 0 {
		limits.GeneratedFileBytes = 2 << 20
	}
	if limits.GeneratedTotalBytes <= 0 {
		limits.GeneratedTotalBytes = 8 << 20
	}
	if limits.RemoteInputFileBytes <= 0 {
		limits.RemoteInputFileBytes = 50 << 20
	}
	return limits
}

type rateLimitDecision struct {
	Allowed   bool
	Limit     int
	Remaining int
	Reset     time.Duration
}

type tokenBucketLimiter struct {
	mu      sync.Mutex
	rate    float64
	burst   float64
	buckets map[string]tokenBucketState
}

type tokenBucketState struct {
	Tokens     float64
	LastRefill time.Time
}

func newTokenBucketLimiter(cfg RateLimitConfig) *tokenBucketLimiter {
	if !cfg.Enabled {
		return nil
	}
	return &tokenBucketLimiter{
		rate:    float64(cfg.RequestsPerMinute) / 60.0,
		burst:   float64(cfg.Burst),
		buckets: make(map[string]tokenBucketState),
	}
}

func (l *tokenBucketLimiter) allow(key string, now time.Time) rateLimitDecision {
	if l == nil {
		return rateLimitDecision{Allowed: true}
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	state, ok := l.buckets[key]
	if !ok {
		state = tokenBucketState{
			Tokens:     l.burst,
			LastRefill: now,
		}
	}

	elapsed := now.Sub(state.LastRefill).Seconds()
	if elapsed > 0 {
		state.Tokens = minFloat(l.burst, state.Tokens+elapsed*l.rate)
		state.LastRefill = now
	}

	if state.Tokens >= 1 {
		state.Tokens--
		remaining := int(math.Floor(state.Tokens))
		l.buckets[key] = state
		return rateLimitDecision{
			Allowed:   true,
			Limit:     int(l.burst),
			Remaining: max(remaining, 0),
			Reset:     resetAfter(state.Tokens, l.rate),
		}
	}

	l.buckets[key] = state
	return rateLimitDecision{
		Allowed:   false,
		Limit:     int(l.burst),
		Remaining: 0,
		Reset:     resetAfter(state.Tokens, l.rate),
	}
}

func resetAfter(tokens float64, rate float64) time.Duration {
	if rate <= 0 {
		return 0
	}
	missing := 1 - tokens
	if missing <= 0 {
		return 0
	}
	seconds := missing / rate
	return time.Duration(math.Ceil(seconds*1000)) * time.Millisecond
}

type concurrencyGate struct {
	scope   string
	metrics *Metrics
	sem     chan struct{}
}

func newConcurrencyGate(scope string, maxConcurrent int, metrics *Metrics) *concurrencyGate {
	if maxConcurrent <= 0 {
		return nil
	}
	return &concurrencyGate{
		scope:   scope,
		metrics: metrics,
		sem:     make(chan struct{}, maxConcurrent),
	}
}

func (g *concurrencyGate) tryAcquire() (func(), error) {
	if g == nil {
		return func() {}, nil
	}
	select {
	case g.sem <- struct{}{}:
		if g.metrics != nil {
			g.metrics.AddInFlight(g.scope, 1)
		}
		return func() {
			<-g.sem
			if g.metrics != nil {
				g.metrics.AddInFlight(g.scope, -1)
			}
		}, nil
	default:
		return nil, &rateLimitExceededError{
			Message: fmt.Sprintf("%s concurrency limit exceeded", g.scope),
			Code:    "rate_limit_exceeded",
		}
	}
}

type rateLimitExceededError struct {
	Message string
	Code    string
}

func (e *rateLimitExceededError) Error() string {
	if e == nil || strings.TrimSpace(e.Message) == "" {
		return "rate limit exceeded"
	}
	return e.Message
}

func authTokenFingerprint(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:4])
}

func extractBearerToken(header string) (string, bool) {
	parts := strings.Fields(strings.TrimSpace(header))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", false
	}
	token := strings.TrimSpace(parts[1])
	if token == "" {
		return "", false
	}
	return token, true
}

func requestRateLimitKey(r *http.Request) string {
	if subject := AuthSubjectFromContext(r.Context()); subject != "" {
		return subject
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil && host != "" {
		return "ip_" + host
	}
	if remote := strings.TrimSpace(r.RemoteAddr); remote != "" {
		return "ip_" + remote
	}
	return "anonymous"
}

func setAuthSubject(ctx context.Context, subject string) context.Context {
	return context.WithValue(ctx, authSubjectKey, strings.TrimSpace(subject))
}

func setClientRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, clientRequestIDKey, strings.TrimSpace(requestID))
}

func setRequestRateLimit(ctx context.Context, info rateLimitDecision) context.Context {
	return context.WithValue(ctx, requestRateLimitKeyCtx, info)
}

func ClientRequestIDFromContext(ctx context.Context) string {
	value, _ := ctx.Value(clientRequestIDKey).(string)
	return value
}

func AuthSubjectFromContext(ctx context.Context) string {
	value, _ := ctx.Value(authSubjectKey).(string)
	return value
}

func RequestRateLimitFromContext(ctx context.Context) (rateLimitDecision, bool) {
	value, ok := ctx.Value(requestRateLimitKeyCtx).(rateLimitDecision)
	return value, ok
}

func requestRouteLabel(r *http.Request) string {
	if r == nil {
		return ""
	}
	if pattern := strings.TrimSpace(r.Pattern); pattern != "" {
		return pattern
	}
	if r.URL != nil && strings.TrimSpace(r.URL.Path) != "" {
		return r.URL.Path
	}
	return ""
}

func isASCII(value string) bool {
	for i := 0; i < len(value); i++ {
		if value[i] > 127 {
			return false
		}
	}
	return true
}

func formatRateLimitReset(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	seconds := int(math.Ceil(d.Seconds()))
	if seconds < 1 {
		seconds = 1
	}
	return fmt.Sprintf("%ds", seconds)
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
