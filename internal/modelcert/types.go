package modelcert

import "time"

const (
	VerdictConfigured          = "configured"
	VerdictShimStartFailed     = "shim_start_failed"
	VerdictAPICompatPassed     = "api_compat_passed"
	VerdictAPICompatFailed     = "api_compat_failed"
	VerdictCodexClean          = "codex_clean"
	VerdictCodexRetryDependent = "codex_retry_dependent"
	VerdictCodexFailed         = "codex_failed"
	VerdictNeedsOperatorReview = "needs_operator_review"
)

type Manifest struct {
	Models []ModelEntry `yaml:"models"`
}

type ModelEntry struct {
	Model     string        `yaml:"model"`
	Provider  ProviderEntry `yaml:"provider"`
	Readiness Readiness     `yaml:"readiness"`
	Tester    TesterConfig  `yaml:"tester"`
	Codex     CodexConfig   `yaml:"codex"`
}

type ProviderEntry struct {
	ID             string `yaml:"id"`
	BaseURL        string `yaml:"base_url"`
	BearerTokenEnv string `yaml:"bearer_token_env"`
	UpstreamModel  string `yaml:"upstream_model"`
}

type Readiness struct {
	RequireReadyz *bool `yaml:"require_readyz"`
}

type TesterConfig struct {
	Command            string `yaml:"command"`
	Mode               string `yaml:"mode"`
	Gate               string `yaml:"gate"`
	Profile            string `yaml:"profile"`
	ModelsConfig       string `yaml:"models_config"`
	SuiteConfig        string `yaml:"suite_config"`
	CapabilitiesConfig string `yaml:"capabilities_config"`
}

type CodexConfig struct {
	Profiles           []string `yaml:"profiles"`
	Attempts           int      `yaml:"attempts"`
	ContextWindow      int64    `yaml:"context_window"`
	ApplyPatchToolType string   `yaml:"apply_patch_tool_type"`
	ShellType          string   `yaml:"shell_type"`
	ReasoningEffort    string   `yaml:"reasoning_effort"`
	Skip               bool     `yaml:"skip"`
}

type RunOptions struct {
	ManifestPath       string
	BaseConfigPath     string
	OutDir             string
	Models             []string
	ExternalTesterDir  string
	TesterCommand      string
	ShimCommand        string
	CodexAutoCommand   string
	CodexCurateCommand string
	SkipShim           bool
	SkipTester         bool
	SkipCodex          bool
	RequireTester      bool
	RunID              string
}

type Summary struct {
	Object        string         `json:"object"`
	Status        string         `json:"status"`
	RunID         string         `json:"run_id"`
	GeneratedAt   string         `json:"generated_at"`
	OutDir        string         `json:"out_dir"`
	Models        []ModelSummary `json:"models"`
	FixCandidates []FixCandidate `json:"fix_candidates,omitempty"`
}

type ModelSummary struct {
	Model              string            `json:"model"`
	Slug               string            `json:"slug"`
	Verdict            string            `json:"verdict"`
	ProviderID         string            `json:"provider_id"`
	ProviderModel      string            `json:"provider_model"`
	UpstreamModel      string            `json:"upstream_model"`
	ShimBaseURL        string            `json:"shim_base_url,omitempty"`
	ArtifactDir        string            `json:"artifact_dir"`
	ConfigPath         string            `json:"config_path,omitempty"`
	HealthStatus       int               `json:"health_status,omitempty"`
	ReadyzStatus       int               `json:"readyz_status,omitempty"`
	CapabilitiesStatus int               `json:"capabilities_status,omitempty"`
	Tester             StageSummary      `json:"tester"`
	Codex              StageSummary      `json:"codex"`
	TraceSummaryPath   string            `json:"trace_summary_path,omitempty"`
	FailureNotesPath   string            `json:"failure_notes_path,omitempty"`
	PossibleOwner      string            `json:"possible_owner,omitempty"`
	Signals            []string          `json:"signals,omitempty"`
	Artifacts          map[string]string `json:"artifacts,omitempty"`
	StartedAt          string            `json:"started_at"`
	CompletedAt        string            `json:"completed_at,omitempty"`
	DurationMS         int64             `json:"duration_ms,omitempty"`
}

type StageSummary struct {
	Status   string `json:"status"`
	ExitCode int    `json:"exit_code,omitempty"`
	Path     string `json:"path,omitempty"`
	Error    string `json:"error,omitempty"`
}

type FixCandidate struct {
	Model       string   `json:"model"`
	Stage       string   `json:"stage"`
	Owner       string   `json:"owner"`
	Category    string   `json:"category"`
	Confidence  string   `json:"confidence"`
	Signals     []string `json:"signals,omitempty"`
	Artifact    string   `json:"artifact,omitempty"`
	Description string   `json:"description"`
}

type TraceList struct {
	Object string       `json:"object"`
	Data   []DebugTrace `json:"data"`
}

type DebugTrace struct {
	RequestID              string                    `json:"request_id"`
	ClientRequestID        string                    `json:"client_request_id,omitempty"`
	Method                 string                    `json:"method"`
	Path                   string                    `json:"path"`
	Route                  string                    `json:"route,omitempty"`
	Surface                string                    `json:"surface,omitempty"`
	Model                  string                    `json:"model,omitempty"`
	Provider               string                    `json:"provider,omitempty"`
	PublicModel            string                    `json:"public_model,omitempty"`
	UpstreamModel          string                    `json:"upstream_model,omitempty"`
	SelectedBackend        string                    `json:"selected_backend,omitempty"`
	BackendProjectionClass string                    `json:"backend_projection_class,omitempty"`
	ToolDecisions          []map[string]any          `json:"tool_decisions,omitempty"`
	Transforms             []map[string]any          `json:"transforms,omitempty"`
	BackendFailure         *DebugTraceBackendFailure `json:"backend_failure,omitempty"`
	FinalStatus            int                       `json:"final_status"`
	DurationMS             int64                     `json:"duration_ms,omitempty"`
}

type DebugTraceBackendFailure struct {
	Class          string `json:"class"`
	ClientStatus   int    `json:"client_status"`
	ClientType     string `json:"client_type"`
	ClientCode     string `json:"client_code,omitempty"`
	Operation      string `json:"operation,omitempty"`
	UpstreamStatus int    `json:"upstream_status,omitempty"`
}

type TraceSummary struct {
	Object          string         `json:"object"`
	TraceCount      int            `json:"trace_count"`
	BySurface       map[string]int `json:"by_surface"`
	ByFinalStatus   map[string]int `json:"by_final_status"`
	ByProvider      map[string]int `json:"by_provider"`
	ByPublicModel   map[string]int `json:"by_public_model"`
	ByUpstreamModel map[string]int `json:"by_upstream_model"`
	ByBackend       map[string]int `json:"by_backend"`
	ByFailureClass  map[string]int `json:"by_failure_class"`
	SlowRequests    []TraceDigest  `json:"slow_requests,omitempty"`
	FailedRequests  []TraceDigest  `json:"failed_requests,omitempty"`
}

type TraceDigest struct {
	RequestID       string `json:"request_id"`
	ClientRequestID string `json:"client_request_id,omitempty"`
	Surface         string `json:"surface,omitempty"`
	Status          int    `json:"status"`
	DurationMS      int64  `json:"duration_ms,omitempty"`
	FailureClass    string `json:"failure_class,omitempty"`
}

type clock interface {
	Now() time.Time
}
