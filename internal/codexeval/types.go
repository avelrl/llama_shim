package codexeval

import "time"

const (
	StatusPassed            = "passed"
	StatusFailedChecker     = "failed_checker"
	StatusFailedCodexExit   = "failed_codex_exit"
	StatusFailedTransport   = "failed_transport"
	StatusFailedNoToolEvent = "failed_no_tool_event"
	StatusFailedNoFinal     = "failed_no_final_answer"
	StatusFailedRawTool     = "failed_raw_tool_markup"
	StatusFailedContextLeak = "failed_context_leak"
	StatusFailedTimeout     = "failed_timeout"
	StatusFailedSetup       = "failed_setup"
	StatusSkipped           = "skipped"
	StatusQuarantined       = "quarantined"
)

const (
	BucketCodexConfig      = "codex_config"
	BucketShimAuth         = "shim_auth"
	BucketShimTransport    = "shim_transport"
	BucketUpstreamHTTP     = "upstream_http"
	BucketUpstreamStream   = "upstream_stream"
	BucketModelNoTool      = "model_no_tool"
	BucketModelBadToolArgs = "model_bad_tool_args"
	BucketCodexToolMissing = "codex_tool_missing"
	BucketCodexToolExec    = "codex_tool_exec"
	BucketCheckerDiff      = "checker_diff"
	BucketCheckerTests     = "checker_tests"
	BucketRawToolMarkup    = "raw_tool_markup"
	BucketContextLeak      = "context_leak"
	BucketTimeout          = "timeout"
	BucketHarnessBug       = "harness_bug"
)

type Config struct {
	TasksDir            string
	Suite               string
	TaskIDs             []string
	RerunFailedFrom     string
	OutDir              string
	ShimBaseURL         string
	BaseURL             string
	HealthPath          string
	CodexBin            string
	Model               string
	Provider            string
	APIKeyEnv           string
	APIKeyValue         string
	AttemptsOverride    int
	ReasoningEffort     string
	ReasoningSummary    string
	WebSockets          bool
	UnifiedExec         bool
	ApplyPatchFreeform  bool
	SkipHealthCheck     bool
	SkipModelsProbe     bool
	RequestMaxRetries   int
	StreamMaxRetries    int
	StreamIdleTimeoutMS int
}

type Task struct {
	Manifest Manifest
	Dir      string
}

type Manifest struct {
	ID          string            `yaml:"id" json:"id"`
	Title       string            `yaml:"title" json:"title"`
	Category    string            `yaml:"category" json:"category"`
	Suites      []string          `yaml:"suites" json:"suites"`
	Timeout     string            `yaml:"timeout" json:"timeout"`
	Attempts    int               `yaml:"attempts" json:"attempts"`
	Tags        []string          `yaml:"tags" json:"tags,omitempty"`
	Prompt      string            `yaml:"prompt" json:"prompt"`
	Env         map[string]string `yaml:"env" json:"env,omitempty"`
	Expected    Expected          `yaml:"expected" json:"expected"`
	Quarantine  *Quarantine       `yaml:"quarantine" json:"quarantine,omitempty"`
	timeoutOnce time.Duration
}

type Quarantine struct {
	Reason string   `yaml:"reason" json:"reason"`
	Models []string `yaml:"models" json:"models,omitempty"`
}

type Expected struct {
	FinalTextEquals       string               `yaml:"final_text_equals" json:"final_text_equals,omitempty"`
	FinalTextContains     []string             `yaml:"final_text_contains" json:"final_text_contains,omitempty"`
	FinalTextContainsFold []string             `yaml:"final_text_contains_fold" json:"final_text_contains_fold,omitempty"`
	Files                 []FileExpectation    `yaml:"files" json:"files,omitempty"`
	Commands              []CommandExpectation `yaml:"commands" json:"commands,omitempty"`
	CodexEvents           []string             `yaml:"codex_events" json:"codex_events,omitempty"`
	ForbiddenCodexEvents  []string             `yaml:"forbidden_codex_events" json:"forbidden_codex_events,omitempty"`
	ForbiddenOutput       []string             `yaml:"forbidden_output" json:"forbidden_output,omitempty"`
	MinCommandExecutions  int                  `yaml:"min_command_executions" json:"min_command_executions,omitempty"`
	MaxToolCalls          int                  `yaml:"max_tool_calls" json:"max_tool_calls,omitempty"`
}

type FileExpectation struct {
	Path            string `yaml:"path" json:"path"`
	Exists          *bool  `yaml:"exists" json:"exists,omitempty"`
	Absent          bool   `yaml:"absent" json:"absent,omitempty"`
	Equals          string `yaml:"equals" json:"equals,omitempty"`
	EqualsTrimSpace string `yaml:"equals_trim_space" json:"equals_trim_space,omitempty"`
	Contains        string `yaml:"contains" json:"contains,omitempty"`
	Matches         string `yaml:"matches" json:"matches,omitempty"`
}

type CommandExpectation struct {
	Name    string            `yaml:"name" json:"name,omitempty"`
	Command string            `yaml:"command" json:"command"`
	Timeout string            `yaml:"timeout" json:"timeout,omitempty"`
	Env     map[string]string `yaml:"env" json:"env,omitempty"`
}

type Environment struct {
	RunID              string   `json:"run_id"`
	StartedAt          string   `json:"started_at"`
	GitCommit          string   `json:"git_commit,omitempty"`
	GitDirty           bool     `json:"git_dirty"`
	CodexBin           string   `json:"codex_bin"`
	CodexVersion       string   `json:"codex_version,omitempty"`
	Model              string   `json:"model"`
	Provider           string   `json:"provider"`
	ShimBaseURL        string   `json:"shim_base_url"`
	BaseURL            string   `json:"base_url"`
	APIKeyEnv          string   `json:"api_key_env"`
	APIKeyPresent      bool     `json:"api_key_present"`
	Suite              string   `json:"suite"`
	TaskIDs            []string `json:"task_ids,omitempty"`
	RerunFailedFrom    string   `json:"rerun_failed_from,omitempty"`
	WebSockets         bool     `json:"websockets"`
	UnifiedExec        bool     `json:"unified_exec"`
	ApplyPatchFreeform bool     `json:"apply_patch_freeform"`
	ReasoningEffort    string   `json:"reasoning_effort"`
	ReasoningSummary   string   `json:"reasoning_summary"`
}

type Summary struct {
	RunID          string         `json:"run_id"`
	StartedAt      string         `json:"started_at"`
	CompletedAt    string         `json:"completed_at"`
	DurationMS     int64          `json:"duration_ms"`
	Environment    Environment    `json:"environment"`
	Counts         map[string]int `json:"counts"`
	FailureBuckets map[string]int `json:"failure_buckets"`
	Tasks          []TaskResult   `json:"tasks"`
	ArtifactRoot   string         `json:"artifact_root"`
}

type TaskResult struct {
	ID             string          `json:"id"`
	Title          string          `json:"title,omitempty"`
	Category       string          `json:"category,omitempty"`
	Status         string          `json:"status"`
	FailureBucket  string          `json:"failure_bucket,omitempty"`
	Attempts       []AttemptResult `json:"attempts"`
	DurationMS     int64           `json:"duration_ms"`
	ArtifactDir    string          `json:"artifact_dir"`
	QuarantineNote string          `json:"quarantine_note,omitempty"`
}

type AttemptResult struct {
	Attempt       int             `json:"attempt"`
	Status        string          `json:"status"`
	FailureBucket string          `json:"failure_bucket,omitempty"`
	DurationMS    int64           `json:"duration_ms"`
	ExitCode      int             `json:"exit_code,omitempty"`
	Error         string          `json:"error,omitempty"`
	ArtifactDir   string          `json:"artifact_dir"`
	CheckResult   CheckResult     `json:"check_result"`
	Events        CodexEventStats `json:"events"`
}

type CheckResult struct {
	Passed       bool           `json:"passed"`
	Failures     []CheckFailure `json:"failures,omitempty"`
	FinalText    string         `json:"final_text,omitempty"`
	CommandCount int            `json:"command_count"`
	FileChanges  int            `json:"file_changes"`
	ToolCalls    int            `json:"tool_calls"`
}

type CheckFailure struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

type CodexEventStats struct {
	Total           int      `json:"total"`
	Types           []string `json:"types"`
	AgentMessages   int      `json:"agent_messages"`
	CommandStarted  int      `json:"command_started"`
	CommandComplete int      `json:"command_completed"`
	FileChanges     int      `json:"file_changes"`
	ToolCalls       int      `json:"tool_calls"`
	TurnCompleted   bool     `json:"turn_completed"`
	TurnFailed      bool     `json:"turn_failed"`
}
