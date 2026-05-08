package httpapi

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"llama_shim/internal/llama"
	"llama_shim/internal/plugincontract"
)

const defaultDebugTraceMaxEntries = 256

type DebugTraceConfig struct {
	Enabled    bool
	MaxEntries int
}

func normalizeDebugTraceConfig(cfg DebugTraceConfig) DebugTraceConfig {
	if cfg.MaxEntries <= 0 {
		cfg.MaxEntries = defaultDebugTraceMaxEntries
	}
	return cfg
}

type DebugTrace struct {
	Object                 string                    `json:"object"`
	RequestID              string                    `json:"request_id"`
	ClientRequestID        string                    `json:"client_request_id,omitempty"`
	Method                 string                    `json:"method"`
	Path                   string                    `json:"path"`
	Route                  string                    `json:"route,omitempty"`
	Surface                string                    `json:"surface,omitempty"`
	SourceFormat           string                    `json:"source_format,omitempty"`
	Model                  string                    `json:"model,omitempty"`
	Provider               string                    `json:"provider,omitempty"`
	PublicModel            string                    `json:"public_model,omitempty"`
	UpstreamModel          string                    `json:"upstream_model,omitempty"`
	PluginID               string                    `json:"plugin_id,omitempty"`
	PluginVersion          string                    `json:"plugin_version,omitempty"`
	PluginContractVersion  string                    `json:"plugin_contract_version,omitempty"`
	RoutingMode            string                    `json:"routing_mode,omitempty"`
	UpstreamTransport      string                    `json:"upstream_transport,omitempty"`
	SelectedBackend        string                    `json:"selected_backend,omitempty"`
	BackendProjectionClass string                    `json:"backend_projection_class,omitempty"`
	PersistenceDecision    string                    `json:"persistence_decision,omitempty"`
	ReplayClass            string                    `json:"replay_class,omitempty"`
	StreamTransformerClass string                    `json:"stream_transformer_class,omitempty"`
	ToolDecisions          []DebugTraceToolDecision  `json:"tool_decisions,omitempty"`
	Transforms             []DebugTraceTransform     `json:"transforms,omitempty"`
	BackendFailure         *DebugTraceBackendFailure `json:"backend_failure,omitempty"`
	Fallback               *DebugTraceFallback       `json:"fallback,omitempty"`
	RateLimit              *DebugTraceRateLimit      `json:"rate_limit,omitempty"`
	FinalStatus            int                       `json:"final_status"`
	StartedAt              string                    `json:"started_at"`
	CompletedAt            string                    `json:"completed_at,omitempty"`
	DurationMS             int64                     `json:"duration_ms,omitempty"`
	ResponseContentType    string                    `json:"response_content_type,omitempty"`
	RedactionPolicy        string                    `json:"redaction_policy"`
}

type DebugTraceToolDecision struct {
	Index           int    `json:"index"`
	Type            string `json:"type"`
	Family          string `json:"family"`
	Disposition     string `json:"disposition"`
	CapabilityClass string `json:"capability_class"`
	Backend         string `json:"backend,omitempty"`
	LocalSupported  bool   `json:"local_supported"`
	RuntimeEnabled  bool   `json:"runtime_enabled"`
	Reason          string `json:"reason,omitempty"`
}

type DebugTraceTransform struct {
	Stage    string   `json:"stage"`
	Class    string   `json:"class"`
	Fields   []string `json:"fields,omitempty"`
	Decision string   `json:"decision,omitempty"`
}

type DebugTraceBackendFailure struct {
	Class               string `json:"class"`
	Retryable           bool   `json:"retryable"`
	Cooldown            bool   `json:"cooldown"`
	CooldownHintSeconds int    `json:"cooldown_hint_seconds,omitempty"`
	FallbackAllowed     bool   `json:"fallback_allowed"`
	ClientStatus        int    `json:"client_status"`
	ClientType          string `json:"client_type"`
	ClientCode          string `json:"client_code,omitempty"`
	Operation           string `json:"operation,omitempty"`
	UpstreamStatus      int    `json:"upstream_status,omitempty"`
}

type DebugTraceFallback struct {
	Attempted bool   `json:"attempted"`
	Allowed   bool   `json:"allowed"`
	Reason    string `json:"reason,omitempty"`
	Target    string `json:"target,omitempty"`
	Result    string `json:"result,omitempty"`
}

type DebugTraceRateLimit struct {
	Allowed   bool   `json:"allowed"`
	Limit     int    `json:"limit,omitempty"`
	Remaining int    `json:"remaining,omitempty"`
	Reset     string `json:"reset,omitempty"`
}

type debugTraceContextValue struct {
	store     *DebugTraceStore
	requestID string
}

const debugTraceContextKey contextKey = "debug_trace"

type DebugTraceStore struct {
	mu         sync.Mutex
	maxEntries int
	order      []string
	entries    map[string]*DebugTrace
}

func NewDebugTraceStore(maxEntries int) *DebugTraceStore {
	if maxEntries <= 0 {
		maxEntries = defaultDebugTraceMaxEntries
	}
	return &DebugTraceStore{
		maxEntries: maxEntries,
		entries:    make(map[string]*DebugTrace, maxEntries),
	}
}

func newDebugTraceStoreForConfig(cfg DebugTraceConfig) *DebugTraceStore {
	cfg = normalizeDebugTraceConfig(cfg)
	if !cfg.Enabled {
		return nil
	}
	return NewDebugTraceStore(cfg.MaxEntries)
}

func (s *DebugTraceStore) Begin(ctx context.Context, r *http.Request, started time.Time) context.Context {
	if s == nil || r == nil {
		return ctx
	}
	requestID := strings.TrimSpace(RequestIDFromContext(ctx))
	if requestID == "" {
		return ctx
	}
	trace := &DebugTrace{
		Object:          "shim.debug_trace",
		RequestID:       requestID,
		ClientRequestID: ClientRequestIDFromContext(ctx),
		Method:          r.Method,
		Path:            requestPath(r),
		Surface:         inferDebugTraceSurface(r.URL.Path),
		SourceFormat:    inferDebugTraceSourceFormat(r.Method, r.URL.Path),
		StartedAt:       started.UTC().Format(time.RFC3339Nano),
		RedactionPolicy: "metadata_only_no_prompts_no_headers_no_file_contents",
	}
	s.mu.Lock()
	s.entries[requestID] = trace
	s.order = append(s.order, requestID)
	for len(s.order) > s.maxEntries {
		oldest := s.order[0]
		s.order = s.order[1:]
		delete(s.entries, oldest)
	}
	s.mu.Unlock()
	return context.WithValue(ctx, debugTraceContextKey, debugTraceContextValue{store: s, requestID: requestID})
}

func (s *DebugTraceStore) Finish(ctx context.Context, status int, route string, contentType string, duration time.Duration, completed time.Time) {
	if s == nil {
		return
	}
	s.update(ctx, func(trace *DebugTrace) {
		trace.Route = strings.TrimSpace(route)
		trace.FinalStatus = status
		trace.ResponseContentType = strings.TrimSpace(contentType)
		trace.DurationMS = duration.Milliseconds()
		trace.CompletedAt = completed.UTC().Format(time.RFC3339Nano)
	})
}

func (s *DebugTraceStore) Get(requestID string) (DebugTrace, bool) {
	if s == nil {
		return DebugTrace{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	trace, ok := s.entries[strings.TrimSpace(requestID)]
	if !ok {
		return DebugTrace{}, false
	}
	return cloneDebugTrace(*trace), true
}

func (s *DebugTraceStore) List(limit int) []DebugTrace {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 || limit > s.maxEntries {
		limit = min(20, s.maxEntries)
	}
	out := make([]DebugTrace, 0, min(limit, len(s.order)))
	for idx := len(s.order) - 1; idx >= 0 && len(out) < limit; idx-- {
		if trace := s.entries[s.order[idx]]; trace != nil {
			out = append(out, cloneDebugTrace(*trace))
		}
	}
	return out
}

func (s *DebugTraceStore) update(ctx context.Context, apply func(*DebugTrace)) {
	state, ok := ctx.Value(debugTraceContextKey).(debugTraceContextValue)
	if !ok || state.store != s || strings.TrimSpace(state.requestID) == "" || apply == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	trace := s.entries[state.requestID]
	if trace == nil {
		return
	}
	apply(trace)
}

func recordDebugTrace(ctx context.Context, apply func(*DebugTrace)) {
	state, ok := ctx.Value(debugTraceContextKey).(debugTraceContextValue)
	if !ok || state.store == nil {
		return
	}
	state.store.update(ctx, apply)
}

func RecordDebugTraceUpstreamRoute(ctx context.Context, route llama.UpstreamRoute) {
	recordDebugTrace(ctx, func(trace *DebugTrace) {
		trace.Provider = strings.TrimSpace(route.ProviderID)
		trace.PublicModel = strings.TrimSpace(route.PublicModel)
		trace.UpstreamModel = strings.TrimSpace(route.UpstreamModel)
		trace.PluginID = strings.TrimSpace(route.PluginID)
		trace.PluginVersion = strings.TrimSpace(route.PluginVersion)
		if trace.PluginID != "" {
			trace.PluginContractVersion = plugincontract.SchemaVersion
		}
	})
}

func RecordDebugTraceBackendFailure(ctx context.Context, decision backendFailureDecision, operation string, upstreamStatus int) {
	recordDebugTrace(ctx, func(trace *DebugTrace) {
		trace.BackendFailure = &DebugTraceBackendFailure{
			Class:               string(decision.Class),
			Retryable:           decision.Retryable,
			Cooldown:            decision.Cooldown,
			CooldownHintSeconds: decision.CooldownHintSeconds,
			FallbackAllowed:     decision.FallbackAllowed,
			ClientStatus:        decision.ClientStatus,
			ClientType:          decision.ClientType,
			ClientCode:          decision.ClientCode,
			Operation:           strings.TrimSpace(operation),
			UpstreamStatus:      upstreamStatus,
		}
	})
}

func RecordDebugTraceFallback(ctx context.Context, attempted bool, allowed bool, reason string, target string, result string) {
	recordDebugTrace(ctx, func(trace *DebugTrace) {
		trace.Fallback = &DebugTraceFallback{
			Attempted: attempted,
			Allowed:   allowed,
			Reason:    strings.TrimSpace(reason),
			Target:    strings.TrimSpace(target),
			Result:    strings.TrimSpace(result),
		}
	})
}

func RecordDebugTraceBackendProjection(ctx context.Context, selectedBackend string, projectionClass string) {
	recordDebugTrace(ctx, func(trace *DebugTrace) {
		trace.SelectedBackend = strings.TrimSpace(selectedBackend)
		trace.BackendProjectionClass = strings.TrimSpace(projectionClass)
	})
}

func RecordDebugTraceRateLimit(ctx context.Context, decision rateLimitDecision) {
	recordDebugTrace(ctx, func(trace *DebugTrace) {
		trace.RateLimit = &DebugTraceRateLimit{
			Allowed:   decision.Allowed,
			Limit:     decision.Limit,
			Remaining: decision.Remaining,
			Reset:     formatRateLimitReset(decision.Reset),
		}
	})
}

func recordDebugTraceResponsesCreate(ctx context.Context, request CreateResponseRequest, classifications responseToolClassifications, createRoute responsesCreateRoute, handler *responseHandler) {
	toolDecisions := make([]DebugTraceToolDecision, 0, len(classifications))
	for _, classification := range classifications {
		toolDecisions = append(toolDecisions, DebugTraceToolDecision{
			Index:           classification.Index,
			Type:            classification.Type,
			Family:          classification.Family,
			Disposition:     classification.Disposition,
			CapabilityClass: classification.CapabilityClass,
			Backend:         classification.Backend,
			LocalSupported:  classification.LocalSupported,
			RuntimeEnabled:  classification.RuntimeEnabled,
			Reason:          classification.Reason,
		})
	}
	recordDebugTrace(ctx, func(trace *DebugTrace) {
		trace.Surface = "responses"
		trace.SourceFormat = "responses.create"
		trace.Model = strings.TrimSpace(request.Model)
		trace.RoutingMode = handler.responsesMode
		trace.UpstreamTransport = handler.responsesUpstreamTransport
		trace.SelectedBackend = createRoute.String()
		trace.BackendProjectionClass = createRoute.ProjectionClass()
		trace.PersistenceDecision = persistenceDecisionForResponseRequest(request)
		trace.ToolDecisions = toolDecisions
		if request.Stream != nil && *request.Stream {
			trace.StreamTransformerClass = "responses_sse"
		}
	})
}

func recordDebugTraceDerivedResponse(ctx context.Context, request CreateResponseRequest, sourceFormat string, handler *responseHandler, selectedBackend string, projectionClass string) {
	recordDebugTrace(ctx, func(trace *DebugTrace) {
		trace.Surface = "responses"
		trace.SourceFormat = sourceFormat
		trace.Model = strings.TrimSpace(request.Model)
		trace.RoutingMode = handler.responsesMode
		trace.UpstreamTransport = handler.responsesUpstreamTransport
		trace.SelectedBackend = selectedBackend
		trace.BackendProjectionClass = projectionClass
		trace.PersistenceDecision = persistenceDecisionForResponseRequest(request)
	})
}

func recordDebugTraceChatCompletion(ctx context.Context, model string, route *llama.UpstreamRoute, compatibility upstreamCompatibilityTraceSummary, shouldStore bool) {
	recordDebugTrace(ctx, func(trace *DebugTrace) {
		trace.Surface = "chat_completions"
		trace.SourceFormat = "chat.completions.create"
		trace.Model = strings.TrimSpace(model)
		trace.SelectedBackend = "proxy"
		trace.BackendProjectionClass = "chat_completions_proxy"
		if shouldStore {
			trace.PersistenceDecision = "shadow_store"
		} else {
			trace.PersistenceDecision = "passthrough"
		}
		if route != nil {
			trace.Provider = strings.TrimSpace(route.ProviderID)
			trace.PublicModel = strings.TrimSpace(route.PublicModel)
			trace.UpstreamModel = strings.TrimSpace(route.UpstreamModel)
			trace.PluginID = strings.TrimSpace(route.PluginID)
			trace.PluginVersion = strings.TrimSpace(route.PluginVersion)
			if trace.PluginID != "" {
				trace.PluginContractVersion = plugincontract.SchemaVersion
			}
		}
		fields := compatibility.Fields()
		if len(fields) > 0 {
			trace.Transforms = append(trace.Transforms, DebugTraceTransform{
				Stage:    "chat_completions.upstream_compatibility",
				Class:    "request_projection",
				Fields:   fields,
				Decision: "applied",
			})
		}
	})
}

type upstreamCompatibilityTraceSummary struct {
	DeveloperRolesRemapped             bool
	DefaultThinkingDisabled            bool
	DefaultMaxTokensApplied            bool
	JSONSchemaDowngraded               bool
	ToolParameterPropertyTypesEnsured  bool
	EmptyAssistantToolContentOmitted   bool
	MoonshotToolSchemaSanitized        bool
	InvalidToolArgumentsRetried        bool
	OmittedEmptyAssistantToolContent   bool
	SanitizedMoonshotToolSchemaRequest bool
}

func (s upstreamCompatibilityTraceSummary) Fields() []string {
	fields := make([]string, 0, 8)
	add := func(enabled bool, field string) {
		if enabled {
			fields = append(fields, field)
		}
	}
	add(s.DeveloperRolesRemapped, "developer_role")
	add(s.DefaultThinkingDisabled, "thinking")
	add(s.DefaultMaxTokensApplied, "max_tokens")
	add(s.JSONSchemaDowngraded, "response_format")
	add(s.ToolParameterPropertyTypesEnsured, "tools.parameters.properties")
	add(s.EmptyAssistantToolContentOmitted, "messages.assistant.content")
	add(s.MoonshotToolSchemaSanitized || s.SanitizedMoonshotToolSchemaRequest, "tools.function.parameters")
	add(s.InvalidToolArgumentsRetried, "tool_calls.function.arguments")
	add(s.OmittedEmptyAssistantToolContent, "messages.assistant.content")
	sort.Strings(fields)
	return compactStrings(fields)
}

func cloneDebugTrace(trace DebugTrace) DebugTrace {
	trace.ToolDecisions = append([]DebugTraceToolDecision(nil), trace.ToolDecisions...)
	trace.Transforms = append([]DebugTraceTransform(nil), trace.Transforms...)
	for idx := range trace.Transforms {
		trace.Transforms[idx].Fields = append([]string(nil), trace.Transforms[idx].Fields...)
	}
	if trace.BackendFailure != nil {
		copyFailure := *trace.BackendFailure
		trace.BackendFailure = &copyFailure
	}
	if trace.Fallback != nil {
		copyFallback := *trace.Fallback
		trace.Fallback = &copyFallback
	}
	if trace.RateLimit != nil {
		copyRateLimit := *trace.RateLimit
		trace.RateLimit = &copyRateLimit
	}
	return trace
}

func requestPath(r *http.Request) string {
	if r == nil || r.URL == nil {
		return ""
	}
	return r.URL.Path
}

func inferDebugTraceSurface(path string) string {
	switch {
	case strings.HasPrefix(path, "/v1/responses"):
		return "responses"
	case strings.HasPrefix(path, "/v1/chat/completions"):
		return "chat_completions"
	case strings.HasPrefix(path, "/v1/conversations"):
		return "conversations"
	case strings.HasPrefix(path, "/v1/files"):
		return "files"
	case strings.HasPrefix(path, "/v1/vector_stores"):
		return "vector_stores"
	case strings.HasPrefix(path, "/v1/containers"):
		return "containers"
	case strings.HasPrefix(path, "/debug/"):
		return "debug"
	default:
		return "proxy"
	}
}

func inferDebugTraceSourceFormat(method string, path string) string {
	if method == http.MethodPost && path == "/v1/responses" {
		return "responses.create"
	}
	if method == http.MethodPost && path == "/v1/responses/input_tokens" {
		return "responses.input_tokens"
	}
	if method == http.MethodPost && path == "/v1/responses/compact" {
		return "responses.compact"
	}
	if method == http.MethodGet && strings.HasPrefix(path, "/v1/responses/") {
		return "responses.retrieve"
	}
	if method == http.MethodPost && path == "/v1/chat/completions" {
		return "chat.completions.create"
	}
	return strings.Trim(strings.ToLower(method)+" "+path, " ")
}

func persistenceDecisionForResponseRequest(request CreateResponseRequest) string {
	if request.Store == nil {
		return "default"
	}
	if *request.Store {
		return "store_true"
	}
	return "store_false"
}

func shouldTraceRequest(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" || isHealthPath(path) || path == "/metrics" {
		return false
	}
	if strings.HasPrefix(path, "/debug/traces") {
		return false
	}
	return true
}

func debugTraceListHandler(store *DebugTraceStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			WriteError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed", "")
			return
		}
		if store == nil {
			WriteError(w, http.StatusNotFound, "not_found_error", "debug traces are disabled", "")
			return
		}
		limit := parseDebugTraceLimit(r.URL.Query().Get("limit"))
		WriteJSON(w, http.StatusOK, map[string]any{
			"object": "list",
			"data":   store.List(limit),
		})
	}
}

func debugTraceGetHandler(store *DebugTraceStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			WriteError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed", "")
			return
		}
		if store == nil {
			WriteError(w, http.StatusNotFound, "not_found_error", "debug traces are disabled", "")
			return
		}
		requestID := strings.Trim(strings.TrimPrefix(r.URL.Path, "/debug/traces/"), "/")
		if requestID == "" {
			WriteError(w, http.StatusNotFound, "not_found_error", "debug trace not found", "")
			return
		}
		trace, ok := store.Get(requestID)
		if !ok {
			WriteError(w, http.StatusNotFound, "not_found_error", "debug trace not found", "")
			return
		}
		WriteJSON(w, http.StatusOK, trace)
	}
}

func parseDebugTraceLimit(value string) int {
	if strings.TrimSpace(value) == "" {
		return 0
	}
	limit, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0
	}
	return limit
}

func compactStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := values[:0]
	var previous string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || value == previous {
			continue
		}
		out = append(out, value)
		previous = value
	}
	return out
}

func (route responsesCreateRoute) String() string {
	switch route {
	case responsesCreateRouteProxy:
		return "proxy"
	case responsesCreateRouteLocalWebSearch:
		return "local_web_search"
	case responsesCreateRouteLocalImageGeneration:
		return "local_image_generation"
	case responsesCreateRouteLocalFileSearch:
		return "local_file_search"
	case responsesCreateRouteLocalComputer:
		return "local_computer"
	case responsesCreateRouteLocalMCP:
		return "local_mcp"
	case responsesCreateRouteLocalToolSearch:
		return "local_tool_search"
	case responsesCreateRouteLocalCodeInterpreter:
		return "local_code_interpreter"
	case responsesCreateRouteLocalToolLoop:
		return "local_tool_loop"
	case responsesCreateRouteLocalState:
		return "local_state"
	case responsesCreateRouteLocalStateViaUpstream:
		return "local_state_via_upstream"
	case responsesCreateRouteLocalOnlyUnsupported:
		return "local_only_unsupported"
	case responsesCreateRouteLocalWebSearchDisabled:
		return "local_web_search_disabled"
	case responsesCreateRouteLocalImageGenerationDisabled:
		return "local_image_generation_disabled"
	case responsesCreateRouteLocalComputerDisabled:
		return "local_computer_disabled"
	case responsesCreateRouteLocalCodeInterpreterDisabled:
		return "local_code_interpreter_disabled"
	default:
		return "unknown"
	}
}

func (route responsesCreateRoute) ProjectionClass() string {
	switch route {
	case responsesCreateRouteProxy:
		return "native_responses_proxy"
	case responsesCreateRouteLocalWebSearch, responsesCreateRouteLocalImageGeneration, responsesCreateRouteLocalFileSearch, responsesCreateRouteLocalComputer, responsesCreateRouteLocalMCP, responsesCreateRouteLocalToolSearch, responsesCreateRouteLocalCodeInterpreter:
		return "local_tool_runtime"
	case responsesCreateRouteLocalToolLoop:
		return "chat_completions_tool_loop"
	case responsesCreateRouteLocalState:
		return "local_state"
	case responsesCreateRouteLocalStateViaUpstream:
		return "local_state_upstream_projection"
	case responsesCreateRouteLocalOnlyUnsupported, responsesCreateRouteLocalWebSearchDisabled, responsesCreateRouteLocalImageGenerationDisabled, responsesCreateRouteLocalComputerDisabled, responsesCreateRouteLocalCodeInterpreterDisabled:
		return "unsupported"
	default:
		return "unknown"
	}
}
