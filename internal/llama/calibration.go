package llama

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"
)

const (
	startupCalibrationStageRecommendationOnly = "recommendation_only"
	startupCalibrationStatusDisabled          = "disabled"
	startupCalibrationStatusRunning           = "running"
	startupCalibrationStatusCompleted         = "completed"
	startupCalibrationStatusFailed            = "failed"
	defaultStartupCalibrationProbeCount       = 3
	defaultStartupCalibrationRequestTimeout   = 8 * time.Second
)

type StartupCalibrationOptions struct {
	Enabled              bool
	ProbeCount           int
	RequestTimeout       time.Duration
	BearerToken          string
	Model                string
	UpstreamTimeout      time.Duration
	ShimWriteTimeout     time.Duration
	CurrentMaxConcurrent int
	CurrentMaxQueueWait  time.Duration
	Logger               *slog.Logger
	Progress             func(StartupCalibrationProgressEvent)
}

type StartupCalibrationSnapshot struct {
	Enabled          bool                              `json:"enabled"`
	Stage            string                            `json:"stage,omitempty"`
	Status           string                            `json:"status"`
	ModelsReady      bool                              `json:"models_ready"`
	Model            string                            `json:"model,omitempty"`
	ProbeCount       int                               `json:"probe_count,omitempty"`
	SuccessfulProbes int                               `json:"successful_probes,omitempty"`
	RequestTimeoutMS int64                             `json:"request_timeout_ms,omitempty"`
	StartedAt        time.Time                         `json:"started_at,omitempty"`
	CompletedAt      time.Time                         `json:"completed_at,omitempty"`
	ObservedLatency  *StartupCalibrationLatencySummary `json:"observed_latency_ms,omitempty"`
	Recommendation   *StartupCalibrationRecommendation `json:"recommendation,omitempty"`
	Error            string                            `json:"error,omitempty"`
}

type StartupCalibrationLatencySummary struct {
	Min int64 `json:"min"`
	P50 int64 `json:"p50"`
	Avg int64 `json:"avg"`
	Max int64 `json:"max"`
}

type StartupCalibrationRecommendation struct {
	TimeoutBudgetMS                int64    `json:"timeout_budget_ms,omitempty"`
	SuggestedMaxConcurrentRequests int      `json:"suggested_max_concurrent_requests,omitempty"`
	SuggestedMaxQueueWaitMS        int64    `json:"suggested_max_queue_wait_ms,omitempty"`
	Warnings                       []string `json:"warnings,omitempty"`
}

type StartupCalibrationProgressEvent struct {
	Step            string `json:"step,omitempty"`
	Method          string `json:"method,omitempty"`
	Path            string `json:"path,omitempty"`
	Success         bool   `json:"success"`
	StatusCode      int    `json:"status_code,omitempty"`
	DurationMS      int64  `json:"duration_ms,omitempty"`
	ProbeIndex      int    `json:"probe_index,omitempty"`
	ProbeCount      int    `json:"probe_count,omitempty"`
	ProbeProfile    string `json:"probe_profile,omitempty"`
	MaxTokens       int    `json:"max_tokens,omitempty"`
	Model           string `json:"model,omitempty"`
	ModelsCount     int    `json:"models_count,omitempty"`
	ResponsePreview string `json:"response_preview,omitempty"`
	Error           string `json:"error,omitempty"`
}

type startupCalibrationProbeSpec struct {
	Name      string
	Messages  []map[string]any
	MaxTokens int
}

var startupCalibrationProbeSpecs = []startupCalibrationProbeSpec{
	{
		Name: "quick_ok",
		Messages: []map[string]any{
			{
				"role":    "user",
				"content": "Reply with OK.",
			},
		},
		MaxTokens: 1,
	},
	{
		Name: "reasoning_schedule",
		Messages: []map[string]any{
			{
				"role": "user",
				"content": "Read carefully and reason through the schedule before answering. " +
					"A courier starts at 09:00. Stop A takes 14 minutes and must happen before Stop B. " +
					"Stop B takes 11 minutes. Stop C takes 9 minutes and must happen after Stop A. " +
					"Travel times are: depot->A 7 minutes, A->B 13 minutes, A->C 6 minutes, C->B 8 minutes, and B->depot 12 minutes. " +
					"The courier must return to the depot by 10:00 while visiting A, B, and C and obeying the dependencies. " +
					"Reply with exactly four lines. " +
					"Line 1: VERDICT: FEASIBLE or VERDICT: INFEASIBLE. " +
					"Line 2: ROUTE: followed by the stop order. " +
					"Line 3: TOTAL_MINUTES: followed by the total trip time as an integer. " +
					"Line 4: WHY: followed by a concise explanation of 24 to 40 words.",
			},
		},
		MaxTokens: 224,
	},
	{
		Name: "reasoning_breakdown",
		Messages: []map[string]any{
			{
				"role": "user",
				"content": "Solve the routing puzzle carefully before you answer. " +
					"A technician starts at 08:30 from the depot and must visit A, B, C, and D exactly once. " +
					"Visit constraints: A must happen before C, B must happen before D, and C cannot be last. " +
					"Service times are A 12 minutes, B 9 minutes, C 15 minutes, D 8 minutes. " +
					"Travel times are depot->A 6, depot->B 7, A->B 10, A->C 5, A->D 14, B->A 9, B->C 11, B->D 6, C->A 5, C->B 8, C->D 7, D->A 10, D->B 6, D->C 7, C->depot 9, D->depot 11, B->depot 8, A->depot 7. " +
					"The technician must be back at the depot by 10:05. " +
					"Reply with exactly five lines. " +
					"Line 1: VERDICT: FEASIBLE or VERDICT: INFEASIBLE. " +
					"Line 2: ROUTE: followed by the chosen stop order. " +
					"Line 3: TOTAL_MINUTES: followed by the full round-trip duration as an integer. " +
					"Line 4: CHECKS: followed by a compact dependency check. " +
					"Line 5: WHY: followed by a concise explanation of 35 to 55 words.",
			},
		},
		MaxTokens: 320,
	},
}

func DisabledStartupCalibrationSnapshot() StartupCalibrationSnapshot {
	return StartupCalibrationSnapshot{
		Enabled: false,
		Stage:   startupCalibrationStageRecommendationOnly,
		Status:  startupCalibrationStatusDisabled,
	}
}

func (c *Client) StartupCalibrationSnapshot() StartupCalibrationSnapshot {
	if c == nil {
		return DisabledStartupCalibrationSnapshot()
	}
	c.calibrationMu.RLock()
	defer c.calibrationMu.RUnlock()
	return cloneStartupCalibrationSnapshot(c.calibration)
}

func (c *Client) RunStartupCalibration(ctx context.Context, options StartupCalibrationOptions) StartupCalibrationSnapshot {
	if c == nil {
		return DisabledStartupCalibrationSnapshot()
	}

	if strings.TrimSpace(options.BearerToken) == "" {
		options.BearerToken = c.startupCalibrationBearerToken
	}
	options = normalizeStartupCalibrationOptions(options)
	if !options.Enabled {
		snapshot := DisabledStartupCalibrationSnapshot()
		c.setStartupCalibrationSnapshot(snapshot)
		return snapshot
	}

	runningSnapshot := StartupCalibrationSnapshot{
		Enabled:          true,
		Stage:            startupCalibrationStageRecommendationOnly,
		Status:           startupCalibrationStatusRunning,
		ProbeCount:       options.ProbeCount,
		RequestTimeoutMS: options.RequestTimeout.Milliseconds(),
		StartedAt:        time.Now().UTC(),
	}
	if options.Model != "" {
		runningSnapshot.Model = options.Model
	}
	c.setStartupCalibrationSnapshot(runningSnapshot)

	logger := options.Logger
	if logger == nil {
		logger = c.logger
	}
	if logger != nil {
		logger.Info(
			"startup calibration started",
			"probe_count", options.ProbeCount,
			"request_timeout", options.RequestTimeout,
			"token_configured", strings.TrimSpace(options.BearerToken) != "",
			"model_override", options.Model,
			"stage", startupCalibrationStageRecommendationOnly,
		)
	}

	modelStart := time.Now()
	modelsResult, err := c.listModelsDetailedWithBearerToken(ctx, options.BearerToken)
	modelDuration := time.Since(modelStart)
	modelPreview := summarizeStartupCalibrationModelsPreview(modelsResult.ModelIDs)
	if modelPreview == "" {
		modelPreview = summarizeStartupCalibrationPreviewBytes(modelsResult.Body)
	}
	if err != nil {
		emitStartupCalibrationProgress(options.Progress, StartupCalibrationProgressEvent{
			Step:            "models",
			Method:          "GET",
			Path:            "/v1/models",
			Success:         false,
			StatusCode:      startupCalibrationStatusCodeFromError(err, modelsResult.StatusCode),
			DurationMS:      modelDuration.Milliseconds(),
			ModelsCount:     len(modelsResult.ModelIDs),
			ResponsePreview: startupCalibrationFirstNonEmptyPreview(modelPreview, startupCalibrationPreviewFromError(err)),
			Error:           err.Error(),
		})
	}
	modelIDs := modelsResult.ModelIDs
	if err != nil {
		failedSnapshot := runningSnapshot
		failedSnapshot.Status = startupCalibrationStatusFailed
		failedSnapshot.CompletedAt = time.Now().UTC()
		failedSnapshot.Error = err.Error()
		c.setStartupCalibrationSnapshot(failedSnapshot)
		if logger != nil {
			logger.Warn("startup calibration failed", "err", err)
		}
		return failedSnapshot
	}
	emitStartupCalibrationProgress(options.Progress, StartupCalibrationProgressEvent{
		Step:            "models",
		Method:          "GET",
		Path:            "/v1/models",
		Success:         true,
		StatusCode:      modelsResult.StatusCode,
		DurationMS:      modelDuration.Milliseconds(),
		ModelsCount:     len(modelIDs),
		ResponsePreview: modelPreview,
	})
	runningSnapshot.ModelsReady = true

	model := options.Model
	if model == "" {
		model = firstNonEmptyModelID(modelIDs)
		if model == "" {
			err = errors.New("llama models response did not contain a probeable model id")
			failedSnapshot := runningSnapshot
			failedSnapshot.Status = startupCalibrationStatusFailed
			failedSnapshot.CompletedAt = time.Now().UTC()
			failedSnapshot.Error = err.Error()
			c.setStartupCalibrationSnapshot(failedSnapshot)
			if logger != nil {
				logger.Warn("startup calibration failed", "err", err)
			}
			return failedSnapshot
		}
	}
	runningSnapshot.Model = model
	c.setStartupCalibrationSnapshot(runningSnapshot)

	successfulDurations := make([]time.Duration, 0, options.ProbeCount)
	var lastErr error
	for i := 0; i < options.ProbeCount; i++ {
		probeSpec := startupCalibrationProbeSpecAt(i)
		probeCtx, cancel := context.WithTimeout(ctx, options.RequestTimeout)
		start := time.Now()
		probeResult, err := c.runStartupCalibrationProbe(probeCtx, model, options.BearerToken, i)
		duration := time.Since(start)
		cancel()
		progressEvent := StartupCalibrationProgressEvent{
			Step:            "probe",
			Method:          httpMethodPost,
			Path:            "/v1/chat/completions",
			ProbeIndex:      i + 1,
			ProbeCount:      options.ProbeCount,
			ProbeProfile:    probeSpec.Name,
			MaxTokens:       probeSpec.MaxTokens,
			Model:           model,
			StatusCode:      probeResult.StatusCode,
			DurationMS:      duration.Milliseconds(),
			ResponsePreview: probeResult.ResponsePreview,
		}
		if err != nil {
			lastErr = fmt.Errorf("probe %d: %w", i+1, err)
			progressEvent.Success = false
			progressEvent.StatusCode = startupCalibrationStatusCodeFromError(err, probeResult.StatusCode)
			progressEvent.ResponsePreview = startupCalibrationFirstNonEmptyPreview(progressEvent.ResponsePreview, startupCalibrationPreviewFromError(err))
			progressEvent.Error = err.Error()
			emitStartupCalibrationProgress(options.Progress, progressEvent)
			if logger != nil {
				logger.Warn(
					"startup calibration probe failed",
					"probe_index", i+1,
					"probe_profile", probeSpec.Name,
					"max_tokens", probeSpec.MaxTokens,
					"duration_ms", duration.Milliseconds(),
					"err", err,
				)
			}
			continue
		}
		successfulDurations = append(successfulDurations, duration)
		progressEvent.Success = true
		emitStartupCalibrationProgress(options.Progress, progressEvent)
		if logger != nil {
			logger.Info(
				"startup calibration probe finished",
				"probe_index", i+1,
				"probe_profile", probeSpec.Name,
				"max_tokens", probeSpec.MaxTokens,
				"duration_ms", duration.Milliseconds(),
			)
		}
	}

	completedSnapshot := runningSnapshot
	completedSnapshot.CompletedAt = time.Now().UTC()
	completedSnapshot.SuccessfulProbes = len(successfulDurations)
	if len(successfulDurations) == 0 {
		completedSnapshot.Status = startupCalibrationStatusFailed
		if lastErr != nil {
			completedSnapshot.Error = fmt.Sprintf("all startup chat probes failed: %v", lastErr)
		} else {
			completedSnapshot.Error = "all startup chat probes failed"
		}
	} else {
		completedSnapshot.Status = startupCalibrationStatusCompleted
		completedSnapshot.ObservedLatency = summarizeStartupCalibrationLatencies(successfulDurations)
		completedSnapshot.Recommendation = buildStartupCalibrationRecommendation(successfulDurations, options, lastErr)
	}
	c.setStartupCalibrationSnapshot(completedSnapshot)

	if logger != nil {
		logAttrs := []any{
			"model", completedSnapshot.Model,
			"probe_count", completedSnapshot.ProbeCount,
			"successful_probes", completedSnapshot.SuccessfulProbes,
			"status", completedSnapshot.Status,
		}
		if completedSnapshot.ObservedLatency != nil {
			logAttrs = append(logAttrs,
				"latency_min_ms", completedSnapshot.ObservedLatency.Min,
				"latency_p50_ms", completedSnapshot.ObservedLatency.P50,
				"latency_avg_ms", completedSnapshot.ObservedLatency.Avg,
				"latency_max_ms", completedSnapshot.ObservedLatency.Max,
			)
		}
		if completedSnapshot.Recommendation != nil {
			logAttrs = append(logAttrs,
				"suggested_max_concurrent_requests", completedSnapshot.Recommendation.SuggestedMaxConcurrentRequests,
				"suggested_max_queue_wait_ms", completedSnapshot.Recommendation.SuggestedMaxQueueWaitMS,
				"timeout_budget_ms", completedSnapshot.Recommendation.TimeoutBudgetMS,
				"warnings", completedSnapshot.Recommendation.Warnings,
			)
		}
		if completedSnapshot.Error != "" {
			logAttrs = append(logAttrs, "err", completedSnapshot.Error)
		}
		logger.Info("startup calibration finished", logAttrs...)
	}

	return completedSnapshot
}

func normalizeStartupCalibrationOptions(options StartupCalibrationOptions) StartupCalibrationOptions {
	if options.ProbeCount <= 0 {
		options.ProbeCount = defaultStartupCalibrationProbeCount
	}
	if options.RequestTimeout <= 0 {
		options.RequestTimeout = defaultStartupCalibrationRequestTimeout
	}
	return options
}

func (c *Client) setStartupCalibrationSnapshot(snapshot StartupCalibrationSnapshot) {
	c.calibrationMu.Lock()
	defer c.calibrationMu.Unlock()
	c.calibration = cloneStartupCalibrationSnapshot(snapshot)
}

func cloneStartupCalibrationSnapshot(snapshot StartupCalibrationSnapshot) StartupCalibrationSnapshot {
	cloned := snapshot
	if snapshot.ObservedLatency != nil {
		latency := *snapshot.ObservedLatency
		cloned.ObservedLatency = &latency
	}
	if snapshot.Recommendation != nil {
		recommendation := *snapshot.Recommendation
		recommendation.Warnings = append([]string(nil), snapshot.Recommendation.Warnings...)
		cloned.Recommendation = &recommendation
	}
	return cloned
}

type startupCalibrationProbeResult struct {
	StatusCode      int
	ResponsePreview string
}

func (c *Client) runStartupCalibrationProbe(ctx context.Context, model string, bearerToken string, probeIndex int) (startupCalibrationProbeResult, error) {
	requestBody, err := buildStartupCalibrationRequest(model, probeIndex)
	if err != nil {
		return startupCalibrationProbeResult{}, fmt.Errorf("build startup calibration request: %w", err)
	}
	result, err := c.doJSONRequestDetailedWithBearerToken(ctx, httpMethodPost, "/v1/chat/completions", requestBody, "upstream_startup_calibration", 1<<20, bearerToken)
	probeResult := startupCalibrationProbeResult{
		StatusCode:      result.StatusCode,
		ResponsePreview: summarizeStartupCalibrationResponsePreview(result.Body),
	}
	if err != nil {
		return probeResult, err
	}
	return probeResult, nil
}

const httpMethodPost = "POST"

func buildStartupCalibrationRequest(model string, probeIndex int) ([]byte, error) {
	spec := startupCalibrationProbeSpecAt(probeIndex)
	return json.Marshal(map[string]any{
		"model":       model,
		"messages":    spec.Messages,
		"max_tokens":  spec.MaxTokens,
		"temperature": 0,
	})
}

func startupCalibrationProbeSpecAt(probeIndex int) startupCalibrationProbeSpec {
	if len(startupCalibrationProbeSpecs) == 0 {
		return startupCalibrationProbeSpec{}
	}
	index := probeIndex % len(startupCalibrationProbeSpecs)
	if index < 0 {
		index += len(startupCalibrationProbeSpecs)
	}
	return startupCalibrationProbeSpecs[index]
}

func summarizeStartupCalibrationLatencies(samples []time.Duration) *StartupCalibrationLatencySummary {
	if len(samples) == 0 {
		return nil
	}
	values := make([]int64, 0, len(samples))
	var total int64
	for _, sample := range samples {
		ms := sample.Milliseconds()
		if sample > 0 && ms == 0 {
			ms = int64(sample / time.Millisecond)
		}
		values = append(values, ms)
		total += ms
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	return &StartupCalibrationLatencySummary{
		Min: values[0],
		P50: values[len(values)/2],
		Avg: total / int64(len(values)),
		Max: values[len(values)-1],
	}
}

func buildStartupCalibrationRecommendation(samples []time.Duration, options StartupCalibrationOptions, lastErr error) *StartupCalibrationRecommendation {
	if len(samples) == 0 {
		return nil
	}
	maxLatency := samples[0]
	for _, sample := range samples[1:] {
		if sample > maxLatency {
			maxLatency = sample
		}
	}
	timeoutBudget := minPositiveDuration(options.UpstreamTimeout, options.ShimWriteTimeout)
	recommendation := &StartupCalibrationRecommendation{
		SuggestedMaxConcurrentRequests: 4,
		SuggestedMaxQueueWaitMS:        0,
	}
	if timeoutBudget > 0 {
		recommendation.TimeoutBudgetMS = timeoutBudget.Milliseconds()
		ratio := float64(maxLatency) / float64(timeoutBudget)
		switch {
		case ratio >= 0.85:
			recommendation.SuggestedMaxConcurrentRequests = 1
		case ratio >= 0.70:
			recommendation.SuggestedMaxConcurrentRequests = 2
		case ratio >= 0.55:
			recommendation.SuggestedMaxConcurrentRequests = 3
		case ratio >= 0.40:
			recommendation.SuggestedMaxConcurrentRequests = 4
		case ratio >= 0.25:
			recommendation.SuggestedMaxConcurrentRequests = 6
		default:
			recommendation.SuggestedMaxConcurrentRequests = 8
		}
		slack := timeoutBudget - maxLatency
		if slack > 5*time.Second {
			queueWait := slack / 2
			if queueWait > 15*time.Second {
				queueWait = 15 * time.Second
			}
			recommendation.SuggestedMaxQueueWaitMS = queueWait.Milliseconds()
		}
		if maxLatency >= timeoutBudget*8/10 {
			recommendation.Warnings = append(recommendation.Warnings, "observed hot-path latency is close to the current timeout budget")
		}
	} else {
		recommendation.Warnings = append(recommendation.Warnings, "no finite timeout budget was available for recommendation sizing")
	}
	if options.CurrentMaxConcurrent > 0 && options.CurrentMaxConcurrent > recommendation.SuggestedMaxConcurrentRequests {
		recommendation.Warnings = append(
			recommendation.Warnings,
			fmt.Sprintf("current llama.max_concurrent_requests=%d is above the conservative recommendation of %d", options.CurrentMaxConcurrent, recommendation.SuggestedMaxConcurrentRequests),
		)
	}
	if options.CurrentMaxQueueWait > 0 && recommendation.SuggestedMaxQueueWaitMS == 0 {
		recommendation.Warnings = append(recommendation.Warnings, "current llama.max_queue_wait is larger than the remaining observed timeout slack")
	}
	if len(samples) < defaultStartupCalibrationProbeCount {
		recommendation.Warnings = append(recommendation.Warnings, "fewer than 3 successful probes were collected; treat the recommendation as low confidence")
	}
	if lastErr != nil {
		recommendation.Warnings = append(recommendation.Warnings, fmt.Sprintf("at least one startup probe failed: %v", lastErr))
	}
	return recommendation
}

func minPositiveDuration(values ...time.Duration) time.Duration {
	var result time.Duration
	for _, value := range values {
		if value <= 0 {
			continue
		}
		if result == 0 || value < result {
			result = value
		}
	}
	return result
}

func firstNonEmptyModelID(ids []string) string {
	for _, id := range ids {
		if id != "" {
			return id
		}
	}
	return ""
}

func emitStartupCalibrationProgress(progress func(StartupCalibrationProgressEvent), event StartupCalibrationProgressEvent) {
	if progress == nil {
		return
	}
	progress(event)
}

func startupCalibrationStatusCodeFromError(err error, fallback int) int {
	if fallback > 0 {
		return fallback
	}
	var upstreamErr *UpstreamError
	if errors.As(err, &upstreamErr) {
		return upstreamErr.StatusCode
	}
	return 0
}

func startupCalibrationPreviewFromError(err error) string {
	var upstreamErr *UpstreamError
	if errors.As(err, &upstreamErr) {
		return summarizeStartupCalibrationPreview(upstreamErr.Message)
	}
	return ""
}

func summarizeStartupCalibrationResponsePreview(body []byte) string {
	if len(bytes.TrimSpace(body)) == 0 {
		return ""
	}
	if text, err := extractAssistantText(body); err == nil {
		return normalizeStartupCalibrationContent(text)
	}
	return summarizeStartupCalibrationPreview(string(body))
}

func summarizeStartupCalibrationModelsPreview(ids []string) string {
	trimmed := make([]string, 0, min(3, len(ids)))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		trimmed = append(trimmed, id)
		if len(trimmed) == 3 {
			break
		}
	}
	if len(trimmed) == 0 {
		return ""
	}
	return strings.Join(trimmed, ", ")
}

func summarizeStartupCalibrationPreviewBytes(body []byte) string {
	return summarizeStartupCalibrationPreview(string(body))
}

func summarizeStartupCalibrationPreview(value string) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if value == "" {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= 160 {
		return value
	}
	return string(runes[:157]) + "..."
}

func normalizeStartupCalibrationContent(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\r\n", "\n"))
	if value == "" {
		return ""
	}
	return value
}

func startupCalibrationFirstNonEmptyPreview(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
