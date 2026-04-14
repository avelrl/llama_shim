package httpapi

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var httpDurationBuckets = []float64{5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000, 10000}

type Metrics struct {
	mu sync.Mutex

	httpRequests        map[httpMetricKey]*httpMetricValue
	authFailures        map[string]uint64
	rateLimitRejects    map[string]uint64
	retrievalSearches   map[searchMetricKey]uint64
	codeInterpreterRuns map[string]uint64
	inFlight            map[string]int64
}

type httpMetricKey struct {
	Method string
	Route  string
	Status string
}

type httpMetricValue struct {
	Count   uint64
	SumMS   float64
	Buckets []uint64
}

type searchMetricKey struct {
	Surface string
	Outcome string
}

func NewMetrics() *Metrics {
	return &Metrics{
		httpRequests:        make(map[httpMetricKey]*httpMetricValue),
		authFailures:        make(map[string]uint64),
		rateLimitRejects:    make(map[string]uint64),
		retrievalSearches:   make(map[searchMetricKey]uint64),
		codeInterpreterRuns: make(map[string]uint64),
		inFlight:            make(map[string]int64),
	}
}

func (m *Metrics) ObserveHTTPRequest(method string, route string, status int, duration time.Duration) {
	if m == nil {
		return
	}
	key := httpMetricKey{
		Method: strings.ToUpper(strings.TrimSpace(method)),
		Route:  strings.TrimSpace(route),
		Status: strconv.Itoa(status),
	}
	if key.Method == "" {
		key.Method = "UNKNOWN"
	}
	if key.Route == "" {
		key.Route = "/"
	}

	durationMS := float64(duration.Milliseconds())
	if duration > 0 && durationMS == 0 {
		durationMS = float64(duration) / float64(time.Millisecond)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	value := m.httpRequests[key]
	if value == nil {
		value = &httpMetricValue{Buckets: make([]uint64, len(httpDurationBuckets))}
		m.httpRequests[key] = value
	}
	value.Count++
	value.SumMS += durationMS
	for i, upperBound := range httpDurationBuckets {
		if durationMS <= upperBound {
			value.Buckets[i]++
		}
	}
}

func (m *Metrics) IncAuthFailure(reason string) {
	if m == nil {
		return
	}
	label := strings.TrimSpace(reason)
	if label == "" {
		label = "unknown"
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.authFailures[label]++
}

func (m *Metrics) IncRateLimitReject(scope string) {
	if m == nil {
		return
	}
	label := strings.TrimSpace(scope)
	if label == "" {
		label = "unknown"
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rateLimitRejects[label]++
}

func (m *Metrics) IncRetrievalSearch(surface string, outcome string) {
	if m == nil {
		return
	}
	key := searchMetricKey{
		Surface: normalizeMetricLabel(surface, "unknown"),
		Outcome: normalizeMetricLabel(outcome, "unknown"),
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.retrievalSearches[key]++
}

func (m *Metrics) IncCodeInterpreterRun(outcome string) {
	if m == nil {
		return
	}
	label := normalizeMetricLabel(outcome, "unknown")
	m.mu.Lock()
	defer m.mu.Unlock()
	m.codeInterpreterRuns[label]++
}

func (m *Metrics) AddInFlight(scope string, delta int64) {
	if m == nil {
		return
	}
	label := normalizeMetricLabel(scope, "unknown")
	m.mu.Lock()
	defer m.mu.Unlock()
	m.inFlight[label] += delta
	if m.inFlight[label] < 0 {
		m.inFlight[label] = 0
	}
}

func (m *Metrics) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_, _ = w.Write([]byte(m.renderPrometheus()))
	})
}

func (m *Metrics) renderPrometheus() string {
	if m == nil {
		return ""
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	var builder strings.Builder

	builder.WriteString("# HELP shim_http_requests_total Total HTTP requests handled by the shim.\n")
	builder.WriteString("# TYPE shim_http_requests_total counter\n")
	builder.WriteString("# HELP shim_http_request_duration_ms HTTP request duration in milliseconds.\n")
	builder.WriteString("# TYPE shim_http_request_duration_ms histogram\n")
	httpKeys := make([]httpMetricKey, 0, len(m.httpRequests))
	for key := range m.httpRequests {
		httpKeys = append(httpKeys, key)
	}
	sort.Slice(httpKeys, func(i, j int) bool {
		if httpKeys[i].Route == httpKeys[j].Route {
			if httpKeys[i].Method == httpKeys[j].Method {
				return httpKeys[i].Status < httpKeys[j].Status
			}
			return httpKeys[i].Method < httpKeys[j].Method
		}
		return httpKeys[i].Route < httpKeys[j].Route
	})
	for _, key := range httpKeys {
		value := m.httpRequests[key]
		labels := fmt.Sprintf(`method=%q,route=%q,status=%q`, key.Method, key.Route, key.Status)
		builder.WriteString(fmt.Sprintf("shim_http_requests_total{%s} %d\n", labels, value.Count))
		var cumulative uint64
		for i, upperBound := range httpDurationBuckets {
			cumulative += value.Buckets[i]
			builder.WriteString(fmt.Sprintf("shim_http_request_duration_ms_bucket{%s,le=%q} %d\n", labels, formatPrometheusFloat(upperBound), cumulative))
		}
		builder.WriteString(fmt.Sprintf("shim_http_request_duration_ms_bucket{%s,le=\"+Inf\"} %d\n", labels, value.Count))
		builder.WriteString(fmt.Sprintf("shim_http_request_duration_ms_sum{%s} %s\n", labels, formatPrometheusFloat(value.SumMS)))
		builder.WriteString(fmt.Sprintf("shim_http_request_duration_ms_count{%s} %d\n", labels, value.Count))
	}

	builder.WriteString("# HELP shim_auth_failures_total Total shim auth failures.\n")
	builder.WriteString("# TYPE shim_auth_failures_total counter\n")
	appendStringCounterMetric(&builder, "shim_auth_failures_total", "reason", m.authFailures)

	builder.WriteString("# HELP shim_rate_limit_rejections_total Total shim rate limit rejections.\n")
	builder.WriteString("# TYPE shim_rate_limit_rejections_total counter\n")
	appendStringCounterMetric(&builder, "shim_rate_limit_rejections_total", "scope", m.rateLimitRejects)

	builder.WriteString("# HELP shim_retrieval_searches_total Total retrieval searches executed by the shim.\n")
	builder.WriteString("# TYPE shim_retrieval_searches_total counter\n")
	searchKeys := make([]searchMetricKey, 0, len(m.retrievalSearches))
	for key := range m.retrievalSearches {
		searchKeys = append(searchKeys, key)
	}
	sort.Slice(searchKeys, func(i, j int) bool {
		if searchKeys[i].Surface == searchKeys[j].Surface {
			return searchKeys[i].Outcome < searchKeys[j].Outcome
		}
		return searchKeys[i].Surface < searchKeys[j].Surface
	})
	for _, key := range searchKeys {
		builder.WriteString(fmt.Sprintf("shim_retrieval_searches_total{surface=%q,outcome=%q} %d\n", key.Surface, key.Outcome, m.retrievalSearches[key]))
	}

	builder.WriteString("# HELP shim_code_interpreter_runs_total Total local code interpreter runs executed by the shim.\n")
	builder.WriteString("# TYPE shim_code_interpreter_runs_total counter\n")
	appendStringCounterMetric(&builder, "shim_code_interpreter_runs_total", "outcome", m.codeInterpreterRuns)

	builder.WriteString("# HELP shim_inflight Current in-flight shim operations.\n")
	builder.WriteString("# TYPE shim_inflight gauge\n")
	inFlightKeys := make([]string, 0, len(m.inFlight))
	for key := range m.inFlight {
		inFlightKeys = append(inFlightKeys, key)
	}
	sort.Strings(inFlightKeys)
	for _, key := range inFlightKeys {
		builder.WriteString(fmt.Sprintf("shim_inflight{scope=%q} %d\n", key, m.inFlight[key]))
	}

	return builder.String()
}

func appendStringCounterMetric(builder *strings.Builder, name string, labelName string, values map[string]uint64) {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		builder.WriteString(fmt.Sprintf("%s{%s=%q} %d\n", name, labelName, key, values[key]))
	}
}

func normalizeMetricLabel(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func formatPrometheusFloat(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}
