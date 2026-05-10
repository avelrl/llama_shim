package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"llama_shim/internal/compactor"
	"llama_shim/internal/imagegen"
	"llama_shim/internal/memory"
	"llama_shim/internal/retrieval"
	"llama_shim/internal/storage"
	"llama_shim/internal/websearch"

	"github.com/spf13/viper"
)

type Config struct {
	Addr                                           string
	StorageBackend                                 string
	SQLitePath                                     string
	PostgresDSN                                    string
	SQLiteMaintenanceCleanupInterval               time.Duration
	StorageResponseReplayArtifactsMaxAge           time.Duration
	StorageResponseReplayArtifactsMaxResponses     int
	LlamaBaseURL                                   string
	LlamaProviders                                 []LlamaProvider
	LlamaReadinessBearerToken                      string
	LlamaTimeout                                   time.Duration
	LlamaMaxConcurrentRequests                     int
	LlamaMaxQueueWait                              time.Duration
	LlamaHTTPMaxIdleConns                          int
	LlamaHTTPMaxIdleConnsPerHost                   int
	LlamaHTTPMaxConnsPerHost                       int
	LlamaHTTPIdleConnTimeout                       time.Duration
	LlamaHTTPDialTimeout                           time.Duration
	LlamaHTTPKeepAlive                             time.Duration
	LlamaHTTPTLSHandshakeTimeout                   time.Duration
	LlamaHTTPExpectContinueTimeout                 time.Duration
	ReadTimeout                                    time.Duration
	WriteTimeout                                   time.Duration
	IdleTimeout                                    time.Duration
	ShutdownTimeout                                time.Duration
	ShimAuthMode                                   string
	ShimAuthBearerTokens                           []string
	ShimRateLimitEnabled                           bool
	ShimRateLimitRequestsPerMinute                 int
	ShimRateLimitBurst                             int
	ShimMetricsEnabled                             bool
	ShimMetricsPath                                string
	ShimDebugTracesEnabled                         bool
	ShimDebugTracesMaxEntries                      int
	ShimEvidenceEnabled                            bool
	ShimEvidenceRoot                               string
	ShimEvidenceMaxEntries                         int
	ShimEvidenceStaleAfter                         time.Duration
	UIEnabled                                      bool
	UIBasePath                                     string
	UIPublicStaticAssets                           bool
	ShimJSONBodyLimitBytes                         int64
	RetrievalFileUploadMaxBytes                    int64
	ChatCompletionsShadowStoreMaxBytes             int64
	ChatCompletionsShadowStoreTimeout              time.Duration
	ResponsesProxyBufferMaxBytes                   int64
	ResponsesStoredLineageMaxItems                 int
	ResponsesLocalToolOutputSummaryMaxBytes        int64
	CustomToolGrammarDefinitionMaxBytes            int64
	CustomToolCompiledPatternMaxBytes              int64
	RetrievalMaxConcurrentSearches                 int
	RetrievalMaxSearchQueries                      int
	RetrievalMaxGroundingChunks                    int
	ResponsesCodeInterpreterMaxConcurrentRuns      int
	ResponsesCodeInterpreterGeneratedFiles         int
	ResponsesCodeInterpreterGeneratedFileBytes     int64
	ResponsesCodeInterpreterGeneratedTotalBytes    int64
	ResponsesCodeInterpreterRemoteInputFileBytes   int64
	LogLevel                                       slog.Level
	LogFilePath                                    string
	RetrievalIndexBackend                          string
	RetrievalEmbedderBackend                       string
	RetrievalEmbedderBaseURL                       string
	RetrievalEmbedderModel                         string
	RetrievalPGVectorANNEnabled                    bool
	RetrievalPGVectorANNMethod                     string
	RetrievalPGVectorANNMetric                     string
	RetrievalPGVectorANNDimensions                 int
	RetrievalPGVectorANNHNSWM                      int
	RetrievalPGVectorANNHNSWEFConstruction         int
	RetrievalPGVectorANNIVFFlatLists               int
	ResponsesWebSearchBackend                      string
	ResponsesWebSearchBaseURL                      string
	ResponsesWebSearchTimeout                      time.Duration
	ResponsesWebSearchMaxResults                   int
	ResponsesImageGenerationBackend                string
	ResponsesImageGenerationBaseURL                string
	ResponsesImageGenerationTimeout                time.Duration
	ResponsesImageGenerationComfyUIWorkflow        map[string]any
	ResponsesImageGenerationComfyUIWorkflowPath    string
	ResponsesImageGenerationComfyUIOutputNodeID    string
	ResponsesImageGenerationComfyUIPollInterval    time.Duration
	ResponsesImageGenerationComfyUIMaxWait         time.Duration
	ResponsesImageGenerationComfyUIMaxImageBytes   int64
	ResponsesCompactionBackend                     string
	ResponsesCompactionBaseURL                     string
	ResponsesCompactionModel                       string
	ResponsesCompactionTimeout                     time.Duration
	ResponsesCompactionMaxOutputTokens             int
	ResponsesCompactionRetainedItems               int
	ResponsesCompactionMaxInputRunes               int
	ResponsesMemoryBackend                         string
	ResponsesMemoryInject                          bool
	ResponsesMemoryMaxNotes                        int
	ResponsesMemoryMaxNoteBytes                    int64
	ResponsesMemoryMaxContextBytes                 int64
	ResponsesMemoryMetadataNamespace               string
	ResponsesComputerBackend                       string
	ChatCompletionsStoreWhenOmitted                bool
	ChatCompletionsUpstreamCompatibility           []ChatCompletionsUpstreamCompatibilityRule
	ResponsesMode                                  string
	ResponsesUpstreamTransport                     string
	ResponsesWebSocketEnabled                      bool
	ResponsesCustomToolsMode                       string
	ResponsesConstrainedDecodingBackend            string
	ResponsesUpstreamToolCompatibility             []ResponsesUpstreamToolCompatibilityRule
	ResponsesCodexEnableCompatibility              bool
	ResponsesCodexUpstreamInputCompatibility       []ResponsesCodexUpstreamInputCompatibilityRule
	ResponsesCodexModelMetadata                    []ResponsesCodexModelMetadata
	ResponsesCodeInterpreterBackend                string
	ResponsesCodeInterpreterPythonBinary           string
	ResponsesCodeInterpreterDockerBinary           string
	ResponsesCodeInterpreterDockerImage            string
	ResponsesCodeInterpreterDockerMemory           string
	ResponsesCodeInterpreterDockerCPU              string
	ResponsesCodeInterpreterDockerPids             int
	ResponsesCodeInterpreterTimeout                time.Duration
	ResponsesCodeInterpreterInputFileURLPolicy     string
	ResponsesCodeInterpreterInputFileURLAllowHosts []string
	ResponsesCodeInterpreterCleanupInterval        time.Duration
	ConfigFile                                     string
}

type LlamaProvider struct {
	ID             string               `mapstructure:"id"`
	BaseURL        string               `mapstructure:"base_url"`
	BearerTokenEnv string               `mapstructure:"bearer_token_env"`
	BearerToken    string               `mapstructure:"-"`
	Models         []LlamaProviderModel `mapstructure:"models"`
}

type LlamaProviderModel struct {
	Model         string `mapstructure:"model"`
	UpstreamModel string `mapstructure:"upstream_model"`
}

type ResponsesUpstreamToolCompatibilityRule struct {
	Model         string   `mapstructure:"model"`
	DisabledTools []string `mapstructure:"disabled_tools"`
}

type ChatCompletionsUpstreamCompatibilityRule struct {
	Model                            string `mapstructure:"model"`
	RemapDeveloperRole               bool   `mapstructure:"remap_developer_role"`
	DefaultThinking                  string `mapstructure:"default_thinking"`
	DefaultMaxTokens                 int    `mapstructure:"default_max_tokens"`
	JSONSchemaMode                   string `mapstructure:"json_schema_mode"`
	EnsureToolParameterPropertyTypes bool   `mapstructure:"ensure_tool_parameter_property_types"`
	SanitizeMoonshotToolSchema       bool   `mapstructure:"sanitize_moonshot_tool_schema"`
	OmitEmptyAssistantToolContent    bool   `mapstructure:"omit_empty_assistant_tool_content"`
	RetryInvalidToolArguments        bool   `mapstructure:"retry_invalid_tool_arguments"`
	InvalidToolArgumentsFallback     string `mapstructure:"invalid_tool_arguments_fallback"`
}

type ResponsesCodexUpstreamInputCompatibilityRule struct {
	Model string `mapstructure:"model"`
	Mode  string `mapstructure:"mode"`
}

type ResponsesCodexModelMetadata struct {
	Model                         string                         `mapstructure:"model"`
	DisplayName                   string                         `mapstructure:"display_name"`
	Description                   string                         `mapstructure:"description"`
	ContextWindow                 int64                          `mapstructure:"context_window"`
	MaxContextWindow              int64                          `mapstructure:"max_context_window"`
	AutoCompactTokenLimit         int64                          `mapstructure:"auto_compact_token_limit"`
	EffectiveContextWindowPercent int64                          `mapstructure:"effective_context_window_percent"`
	DefaultReasoningLevel         string                         `mapstructure:"default_reasoning_level"`
	SupportedReasoningLevels      []string                       `mapstructure:"supported_reasoning_levels"`
	SupportsReasoningSummaries    bool                           `mapstructure:"supports_reasoning_summaries"`
	DefaultReasoningSummary       string                         `mapstructure:"default_reasoning_summary"`
	ShellType                     string                         `mapstructure:"shell_type"`
	ApplyPatchToolType            string                         `mapstructure:"apply_patch_tool_type"`
	WebSearchToolType             string                         `mapstructure:"web_search_tool_type"`
	SupportsParallelToolCalls     bool                           `mapstructure:"supports_parallel_tool_calls"`
	SupportVerbosity              bool                           `mapstructure:"support_verbosity"`
	DefaultVerbosity              string                         `mapstructure:"default_verbosity"`
	SupportsImageDetailOriginal   bool                           `mapstructure:"supports_image_detail_original"`
	SupportsSearchTool            bool                           `mapstructure:"supports_search_tool"`
	InputModalities               []string                       `mapstructure:"input_modalities"`
	Visibility                    string                         `mapstructure:"visibility"`
	SupportedInAPI                *bool                          `mapstructure:"supported_in_api"`
	Priority                      *int                           `mapstructure:"priority"`
	AdditionalSpeedTiers          []string                       `mapstructure:"additional_speed_tiers"`
	ExperimentalSupportedTools    []string                       `mapstructure:"experimental_supported_tools"`
	AvailabilityNuxMessage        string                         `mapstructure:"availability_nux_message"`
	TruncationPolicy              ResponsesCodexTruncationPolicy `mapstructure:"truncation_policy"`
	BaseInstructions              string                         `mapstructure:"base_instructions"`
}

type ResponsesCodexTruncationPolicy struct {
	Mode  string `mapstructure:"mode"`
	Limit int64  `mapstructure:"limit"`
}

const (
	ResponsesModePreferLocal                                       = "prefer_local"
	ResponsesModePreferUpstream                                    = "prefer_upstream"
	ResponsesModeLocalOnly                                         = "local_only"
	ResponsesUpstreamTransportResponses                            = "responses"
	ResponsesUpstreamTransportChatCompletions                      = "chat_completions"
	ShimAuthModeDisabled                                           = "disabled"
	ShimAuthModeStaticBearer                                       = "static_bearer"
	ResponsesCodeInterpreterBackendDisabled                        = "disabled"
	ResponsesCodeInterpreterBackendUnsafeHost                      = "unsafe_host"
	ResponsesCodeInterpreterBackendDocker                          = "docker"
	ResponsesComputerBackendDisabled                               = "disabled"
	ResponsesComputerBackendChatCompletions                        = "chat_completions"
	ResponsesCodeInterpreterInputFileURLPolicyDisabled             = "disabled"
	ResponsesCodeInterpreterInputFileURLPolicyAllowlist            = "allowlist"
	ResponsesCodeInterpreterInputFileURLPolicyUnsafeAllowHTTPHTTPS = "unsafe_allow_http_https"
	ResponsesConstrainedDecodingBackendShimValidateRepair          = "shim_validate_repair"
	ResponsesConstrainedDecodingBackendVLLM                        = "vllm"
	StorageBackendSQLite                                           = storage.BackendSQLite
	StorageBackendPostgres                                         = storage.BackendPostgres
)

func Load(configPath string) (Config, error) {
	if err := loadDotEnv(resolveDotEnvPath()); err != nil {
		return Config{}, err
	}

	v := viper.New()
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	setDefaults(v)

	if err := readConfigFileNamed(v, resolveConfigPath(configPath), "config"); err != nil {
		return Config{}, err
	}

	imageGenerationSection := normalizeConfigMap(v.Get("responses.image_generation"))
	comfyUISection := normalizeConfigMap(imageGenerationSection["comfyui"])
	comfyUIWorkflow := normalizeConfigMap(v.Get("responses.image_generation.comfyui.workflow"))
	if len(comfyUIWorkflow) == 0 {
		comfyUIWorkflow = normalizeConfigMap(comfyUISection["workflow"])
	}

	cfg := Config{
		Addr:                                           strings.TrimSpace(v.GetString("shim.addr")),
		StorageBackend:                                 strings.ToLower(strings.TrimSpace(v.GetString("storage.backend"))),
		SQLitePath:                                     strings.TrimSpace(v.GetString("sqlite.path")),
		PostgresDSN:                                    strings.TrimSpace(v.GetString("postgres.dsn")),
		SQLiteMaintenanceCleanupInterval:               0,
		LlamaBaseURL:                                   strings.TrimRight(strings.TrimSpace(v.GetString("llama.base_url")), "/"),
		LlamaReadinessBearerToken:                      strings.TrimSpace(v.GetString("llama.readiness_bearer_token")),
		ConfigFile:                                     v.ConfigFileUsed(),
		ShimAuthMode:                                   strings.ToLower(strings.TrimSpace(v.GetString("shim.auth.mode"))),
		ShimAuthBearerTokens:                           parseStringList(v, "shim.auth.bearer_tokens"),
		ShimRateLimitEnabled:                           v.GetBool("shim.rate_limit.enabled"),
		ShimMetricsEnabled:                             v.GetBool("shim.metrics.enabled"),
		ShimMetricsPath:                                strings.TrimSpace(v.GetString("shim.metrics.path")),
		ShimDebugTracesEnabled:                         v.GetBool("shim.debug_traces.enabled"),
		ShimEvidenceEnabled:                            v.GetBool("shim.evidence.enabled"),
		ShimEvidenceRoot:                               strings.TrimSpace(v.GetString("shim.evidence.root")),
		UIEnabled:                                      v.GetBool("ui.enabled"),
		UIPublicStaticAssets:                           v.GetBool("ui.public_static_assets"),
		LogLevel:                                       slog.LevelInfo,
		LogFilePath:                                    strings.TrimSpace(v.GetString("log.file_path")),
		RetrievalIndexBackend:                          strings.TrimSpace(v.GetString("retrieval.index.backend")),
		RetrievalEmbedderBackend:                       strings.TrimSpace(v.GetString("retrieval.embedder.backend")),
		RetrievalEmbedderBaseURL:                       strings.TrimSpace(v.GetString("retrieval.embedder.base_url")),
		RetrievalEmbedderModel:                         strings.TrimSpace(v.GetString("retrieval.embedder.model")),
		RetrievalPGVectorANNEnabled:                    v.GetBool("retrieval.index.pgvector.ann.enabled"),
		RetrievalPGVectorANNMethod:                     strings.TrimSpace(v.GetString("retrieval.index.pgvector.ann.method")),
		RetrievalPGVectorANNMetric:                     strings.TrimSpace(v.GetString("retrieval.index.pgvector.ann.metric")),
		ResponsesWebSearchBackend:                      strings.ToLower(strings.TrimSpace(v.GetString("responses.web_search.backend"))),
		ResponsesWebSearchBaseURL:                      strings.TrimSpace(v.GetString("responses.web_search.base_url")),
		ResponsesImageGenerationBackend:                strings.ToLower(strings.TrimSpace(v.GetString("responses.image_generation.backend"))),
		ResponsesImageGenerationBaseURL:                strings.TrimSpace(v.GetString("responses.image_generation.base_url")),
		ResponsesImageGenerationComfyUIWorkflow:        comfyUIWorkflow,
		ResponsesImageGenerationComfyUIWorkflowPath:    firstNonEmptyConfigString(v.GetString("responses.image_generation.comfyui.workflow_path"), configString(comfyUISection["workflow_path"])),
		ResponsesImageGenerationComfyUIOutputNodeID:    firstNonEmptyConfigString(v.GetString("responses.image_generation.comfyui.output_node_id"), configString(comfyUISection["output_node_id"])),
		ResponsesCompactionBackend:                     strings.ToLower(strings.TrimSpace(v.GetString("responses.compaction.backend"))),
		ResponsesCompactionBaseURL:                     strings.TrimSpace(v.GetString("responses.compaction.base_url")),
		ResponsesCompactionModel:                       strings.TrimSpace(v.GetString("responses.compaction.model")),
		ResponsesMemoryBackend:                         strings.ToLower(strings.TrimSpace(v.GetString("responses.memory.backend"))),
		ResponsesMemoryInject:                          v.GetBool("responses.memory.inject"),
		ResponsesMemoryMetadataNamespace:               strings.TrimSpace(v.GetString("responses.memory.metadata_namespace")),
		ResponsesComputerBackend:                       strings.ToLower(strings.TrimSpace(v.GetString("responses.computer.backend"))),
		ChatCompletionsStoreWhenOmitted:                v.GetBool("chat_completions.default_store_when_omitted"),
		ResponsesMode:                                  strings.ToLower(strings.TrimSpace(v.GetString("responses.mode"))),
		ResponsesUpstreamTransport:                     strings.ToLower(strings.TrimSpace(v.GetString("responses.upstream_transport"))),
		ResponsesWebSocketEnabled:                      v.GetBool("responses.websocket.enabled"),
		ResponsesCustomToolsMode:                       strings.ToLower(strings.TrimSpace(v.GetString("responses.custom_tools.mode"))),
		ResponsesConstrainedDecodingBackend:            strings.ToLower(strings.TrimSpace(v.GetString("responses.constrained_decoding.backend"))),
		ResponsesCodexEnableCompatibility:              v.GetBool("responses.codex.enable_compatibility"),
		ResponsesCodeInterpreterBackend:                strings.ToLower(strings.TrimSpace(v.GetString("responses.code_interpreter.backend"))),
		ResponsesCodeInterpreterPythonBinary:           strings.TrimSpace(v.GetString("responses.code_interpreter.python_binary")),
		ResponsesCodeInterpreterDockerBinary:           strings.TrimSpace(v.GetString("responses.code_interpreter.docker.binary")),
		ResponsesCodeInterpreterDockerImage:            strings.TrimSpace(v.GetString("responses.code_interpreter.docker.image")),
		ResponsesCodeInterpreterDockerMemory:           strings.TrimSpace(v.GetString("responses.code_interpreter.docker.memory_limit")),
		ResponsesCodeInterpreterDockerCPU:              strings.TrimSpace(v.GetString("responses.code_interpreter.docker.cpu_limit")),
		ResponsesCodeInterpreterInputFileURLPolicy:     strings.ToLower(strings.TrimSpace(v.GetString("responses.code_interpreter.input_file_url_policy"))),
		ResponsesCodeInterpreterInputFileURLAllowHosts: parseStringList(v, "responses.code_interpreter.input_file_url_allow_hosts"),
	}
	if cfg.ResponsesCodeInterpreterBackend == "" {
		if v.GetBool("responses.code_interpreter.enable_unsafe_host_executor") {
			return Config{}, fmt.Errorf("parse responses.code_interpreter.enable_unsafe_host_executor: %w", strconv.ErrSyntax)
		} else {
			cfg.ResponsesCodeInterpreterBackend = ResponsesCodeInterpreterBackendDisabled
		}
	}
	llamaProviders, err := parseLlamaProviders(v)
	if err != nil {
		return Config{}, err
	}
	cfg.LlamaProviders = llamaProviders
	chatCompletionsUpstreamCompatibility, err := parseChatCompletionsUpstreamCompatibility(v)
	if err != nil {
		return Config{}, err
	}
	cfg.ChatCompletionsUpstreamCompatibility = chatCompletionsUpstreamCompatibility
	upstreamToolCompatibility, err := parseResponsesUpstreamToolCompatibility(v)
	if err != nil {
		return Config{}, err
	}
	cfg.ResponsesUpstreamToolCompatibility = upstreamToolCompatibility
	codexUpstreamInputCompatibility, err := parseResponsesCodexUpstreamInputCompatibility(v)
	if err != nil {
		return Config{}, err
	}
	cfg.ResponsesCodexUpstreamInputCompatibility = codexUpstreamInputCompatibility
	codexModelMetadata, err := parseResponsesCodexModelMetadata(v)
	if err != nil {
		return Config{}, err
	}
	cfg.ResponsesCodexModelMetadata = codexModelMetadata
	storageBackend, err := storage.NormalizeBackend(cfg.StorageBackend)
	if err != nil {
		return Config{}, fmt.Errorf("parse storage.backend: %w", err)
	}
	cfg.StorageBackend = storageBackend
	if cfg.StorageBackend == StorageBackendPostgres && strings.TrimSpace(cfg.PostgresDSN) == "" {
		return Config{}, fmt.Errorf("parse postgres.dsn: %w", strconv.ErrSyntax)
	}

	if err := parseDuration(v.GetString("llama.timeout"), &cfg.LlamaTimeout); err != nil {
		return Config{}, fmt.Errorf("parse llama.timeout: %w", err)
	}
	llamaMaxConcurrentRequests, err := parseNonNegativeInt(v.GetString("llama.max_concurrent_requests"))
	if err != nil {
		return Config{}, fmt.Errorf("parse llama.max_concurrent_requests: %w", err)
	}
	cfg.LlamaMaxConcurrentRequests = llamaMaxConcurrentRequests
	if err := parseDuration(v.GetString("llama.max_queue_wait"), &cfg.LlamaMaxQueueWait); err != nil {
		return Config{}, fmt.Errorf("parse llama.max_queue_wait: %w", err)
	}
	llamaHTTPMaxIdleConns, err := parsePositiveInt(v.GetString("llama.http.max_idle_conns"))
	if err != nil {
		return Config{}, fmt.Errorf("parse llama.http.max_idle_conns: %w", err)
	}
	cfg.LlamaHTTPMaxIdleConns = llamaHTTPMaxIdleConns
	llamaHTTPMaxIdleConnsPerHost, err := parsePositiveInt(v.GetString("llama.http.max_idle_conns_per_host"))
	if err != nil {
		return Config{}, fmt.Errorf("parse llama.http.max_idle_conns_per_host: %w", err)
	}
	cfg.LlamaHTTPMaxIdleConnsPerHost = llamaHTTPMaxIdleConnsPerHost
	llamaHTTPMaxConnsPerHost, err := parsePositiveInt(v.GetString("llama.http.max_conns_per_host"))
	if err != nil {
		return Config{}, fmt.Errorf("parse llama.http.max_conns_per_host: %w", err)
	}
	cfg.LlamaHTTPMaxConnsPerHost = llamaHTTPMaxConnsPerHost
	if err := parseDuration(v.GetString("llama.http.idle_conn_timeout"), &cfg.LlamaHTTPIdleConnTimeout); err != nil {
		return Config{}, fmt.Errorf("parse llama.http.idle_conn_timeout: %w", err)
	}
	if err := parseDuration(v.GetString("llama.http.dial_timeout"), &cfg.LlamaHTTPDialTimeout); err != nil {
		return Config{}, fmt.Errorf("parse llama.http.dial_timeout: %w", err)
	}
	if err := parseDuration(v.GetString("llama.http.keep_alive"), &cfg.LlamaHTTPKeepAlive); err != nil {
		return Config{}, fmt.Errorf("parse llama.http.keep_alive: %w", err)
	}
	if err := parseDuration(v.GetString("llama.http.tls_handshake_timeout"), &cfg.LlamaHTTPTLSHandshakeTimeout); err != nil {
		return Config{}, fmt.Errorf("parse llama.http.tls_handshake_timeout: %w", err)
	}
	if err := parseDuration(v.GetString("llama.http.expect_continue_timeout"), &cfg.LlamaHTTPExpectContinueTimeout); err != nil {
		return Config{}, fmt.Errorf("parse llama.http.expect_continue_timeout: %w", err)
	}
	if err := parseDuration(v.GetString("sqlite.maintenance.cleanup_interval"), &cfg.SQLiteMaintenanceCleanupInterval); err != nil {
		return Config{}, fmt.Errorf("parse sqlite.maintenance.cleanup_interval: %w", err)
	}
	if err := parseDuration(v.GetString("storage.retention.response_replay_artifacts.max_age"), &cfg.StorageResponseReplayArtifactsMaxAge); err != nil {
		return Config{}, fmt.Errorf("parse storage.retention.response_replay_artifacts.max_age: %w", err)
	}
	responseReplayArtifactsMaxResponses, err := parseNonNegativeInt(v.GetString("storage.retention.response_replay_artifacts.max_responses"))
	if err != nil {
		return Config{}, fmt.Errorf("parse storage.retention.response_replay_artifacts.max_responses: %w", err)
	}
	cfg.StorageResponseReplayArtifactsMaxResponses = responseReplayArtifactsMaxResponses
	if err := parseDuration(v.GetString("shim.read_timeout"), &cfg.ReadTimeout); err != nil {
		return Config{}, fmt.Errorf("parse shim.read_timeout: %w", err)
	}
	if err := parseDuration(v.GetString("shim.write_timeout"), &cfg.WriteTimeout); err != nil {
		return Config{}, fmt.Errorf("parse shim.write_timeout: %w", err)
	}
	if err := parseDuration(v.GetString("shim.idle_timeout"), &cfg.IdleTimeout); err != nil {
		return Config{}, fmt.Errorf("parse shim.idle_timeout: %w", err)
	}
	if err := parseDuration(v.GetString("shim.shutdown_timeout"), &cfg.ShutdownTimeout); err != nil {
		return Config{}, fmt.Errorf("parse shim.shutdown_timeout: %w", err)
	}
	if err := parseLogLevel(v.GetString("log.level"), &cfg.LogLevel); err != nil {
		return Config{}, fmt.Errorf("parse log.level: %w", err)
	}
	annDimensions, err := parseNonNegativeInt(v.GetString("retrieval.index.pgvector.ann.dimensions"))
	if err != nil {
		return Config{}, fmt.Errorf("parse retrieval.index.pgvector.ann.dimensions: %w", err)
	}
	cfg.RetrievalPGVectorANNDimensions = annDimensions
	annHNSWM, err := parseNonNegativeInt(v.GetString("retrieval.index.pgvector.ann.hnsw_m"))
	if err != nil {
		return Config{}, fmt.Errorf("parse retrieval.index.pgvector.ann.hnsw_m: %w", err)
	}
	cfg.RetrievalPGVectorANNHNSWM = annHNSWM
	annHNSWEFConstruction, err := parseNonNegativeInt(v.GetString("retrieval.index.pgvector.ann.hnsw_ef_construction"))
	if err != nil {
		return Config{}, fmt.Errorf("parse retrieval.index.pgvector.ann.hnsw_ef_construction: %w", err)
	}
	cfg.RetrievalPGVectorANNHNSWEFConstruction = annHNSWEFConstruction
	annIVFFlatLists, err := parseNonNegativeInt(v.GetString("retrieval.index.pgvector.ann.ivfflat_lists"))
	if err != nil {
		return Config{}, fmt.Errorf("parse retrieval.index.pgvector.ann.ivfflat_lists: %w", err)
	}
	cfg.RetrievalPGVectorANNIVFFlatLists = annIVFFlatLists
	if err := parseShimAuthMode(cfg.ShimAuthMode); err != nil {
		return Config{}, fmt.Errorf("parse shim.auth.mode: %w", err)
	}
	normalizedRetrieval, err := retrieval.NormalizeConfig(cfg.RetrievalConfig())
	if err != nil {
		return Config{}, fmt.Errorf("parse retrieval config: %w", err)
	}
	cfg.RetrievalIndexBackend = normalizedRetrieval.IndexBackend
	cfg.RetrievalEmbedderBackend = normalizedRetrieval.Embedder.Backend
	cfg.RetrievalEmbedderBaseURL = normalizedRetrieval.Embedder.BaseURL
	cfg.RetrievalEmbedderModel = normalizedRetrieval.Embedder.Model
	cfg.RetrievalPGVectorANNEnabled = normalizedRetrieval.PGVector.ANN.Enabled
	cfg.RetrievalPGVectorANNMethod = normalizedRetrieval.PGVector.ANN.Method
	cfg.RetrievalPGVectorANNMetric = normalizedRetrieval.PGVector.ANN.Metric
	cfg.RetrievalPGVectorANNDimensions = normalizedRetrieval.PGVector.ANN.Dimensions
	cfg.RetrievalPGVectorANNHNSWM = normalizedRetrieval.PGVector.ANN.HNSWM
	cfg.RetrievalPGVectorANNHNSWEFConstruction = normalizedRetrieval.PGVector.ANN.HNSWEFConstruction
	cfg.RetrievalPGVectorANNIVFFlatLists = normalizedRetrieval.PGVector.ANN.IVFFlatLists
	if cfg.RetrievalIndexBackend == retrieval.IndexBackendPGVector {
		if cfg.StorageBackend != StorageBackendPostgres {
			return Config{}, fmt.Errorf("parse retrieval.index.backend: %q requires storage.backend=%q", retrieval.IndexBackendPGVector, StorageBackendPostgres)
		}
		if cfg.RetrievalEmbedderBackend == retrieval.EmbedderBackendDisabled {
			return Config{}, fmt.Errorf("parse retrieval.embedder.backend: %q requires a configured embedder backend", retrieval.IndexBackendPGVector)
		}
	}
	if cfg.StorageBackend == StorageBackendPostgres {
		switch cfg.RetrievalIndexBackend {
		case retrieval.IndexBackendLexical, retrieval.IndexBackendPGVector:
		default:
			return Config{}, fmt.Errorf("parse retrieval.index.backend: storage.backend=%q supports %q or %q, got %q", StorageBackendPostgres, retrieval.IndexBackendLexical, retrieval.IndexBackendPGVector, cfg.RetrievalIndexBackend)
		}
	}
	normalizedWebSearch, err := websearch.NormalizeConfig(websearch.Config{
		Backend:    cfg.ResponsesWebSearchBackend,
		BaseURL:    cfg.ResponsesWebSearchBaseURL,
		MaxResults: 0,
	})
	if err != nil {
		return Config{}, fmt.Errorf("parse responses.web_search config: %w", err)
	}
	cfg.ResponsesWebSearchBackend = normalizedWebSearch.Backend
	cfg.ResponsesWebSearchBaseURL = normalizedWebSearch.BaseURL
	if err := parseResponsesMode(cfg.ResponsesMode); err != nil {
		return Config{}, fmt.Errorf("parse responses.mode: %w", err)
	}
	if err := parseResponsesUpstreamTransport(cfg.ResponsesUpstreamTransport); err != nil {
		return Config{}, fmt.Errorf("parse responses.upstream_transport: %w", err)
	}
	if err := parseCustomToolsMode(cfg.ResponsesCustomToolsMode); err != nil {
		return Config{}, fmt.Errorf("parse responses.custom_tools.mode: %w", err)
	}
	if err := parseConstrainedDecodingBackend(cfg.ResponsesConstrainedDecodingBackend); err != nil {
		return Config{}, fmt.Errorf("parse responses.constrained_decoding.backend: %w", err)
	}
	if err := parseComputerBackend(cfg.ResponsesComputerBackend); err != nil {
		return Config{}, fmt.Errorf("parse responses.computer.backend: %w", err)
	}
	if err := parseCodeInterpreterBackend(cfg.ResponsesCodeInterpreterBackend); err != nil {
		return Config{}, fmt.Errorf("parse responses.code_interpreter.backend: %w", err)
	}
	if err := parseCodeInterpreterInputFileURLPolicy(cfg.ResponsesCodeInterpreterInputFileURLPolicy); err != nil {
		return Config{}, fmt.Errorf("parse responses.code_interpreter.input_file_url_policy: %w", err)
	}
	jsonBodyLimit, err := parseByteSize(v.GetString("shim.limits.json_body_bytes"))
	if err != nil {
		return Config{}, fmt.Errorf("parse shim.limits.json_body_bytes: %w", err)
	}
	cfg.ShimJSONBodyLimitBytes = jsonBodyLimit
	retrievalUploadLimit, err := parseByteSize(v.GetString("shim.limits.retrieval_file_upload_bytes"))
	if err != nil {
		return Config{}, fmt.Errorf("parse shim.limits.retrieval_file_upload_bytes: %w", err)
	}
	cfg.RetrievalFileUploadMaxBytes = retrievalUploadLimit
	chatCompletionShadowStoreLimit, err := parseByteSize(v.GetString("shim.limits.chat_completions_shadow_store_bytes"))
	if err != nil {
		return Config{}, fmt.Errorf("parse shim.limits.chat_completions_shadow_store_bytes: %w", err)
	}
	cfg.ChatCompletionsShadowStoreMaxBytes = chatCompletionShadowStoreLimit
	if err := parseDuration(v.GetString("shim.limits.chat_completions_shadow_store_timeout"), &cfg.ChatCompletionsShadowStoreTimeout); err != nil {
		return Config{}, fmt.Errorf("parse shim.limits.chat_completions_shadow_store_timeout: %w", err)
	}
	responsesProxyBufferLimit, err := parseByteSize(v.GetString("shim.limits.responses_proxy_buffer_bytes"))
	if err != nil {
		return Config{}, fmt.Errorf("parse shim.limits.responses_proxy_buffer_bytes: %w", err)
	}
	cfg.ResponsesProxyBufferMaxBytes = responsesProxyBufferLimit
	responsesStoredLineageMaxItems, err := parsePositiveInt(v.GetString("shim.limits.responses_stored_lineage_max_items"))
	if err != nil {
		return Config{}, fmt.Errorf("parse shim.limits.responses_stored_lineage_max_items: %w", err)
	}
	cfg.ResponsesStoredLineageMaxItems = responsesStoredLineageMaxItems
	responsesLocalToolOutputSummaryLimit, err := parseByteSize(v.GetString("shim.limits.responses_local_tool_output_summary_bytes"))
	if err != nil {
		return Config{}, fmt.Errorf("parse shim.limits.responses_local_tool_output_summary_bytes: %w", err)
	}
	cfg.ResponsesLocalToolOutputSummaryMaxBytes = responsesLocalToolOutputSummaryLimit
	customToolGrammarDefinitionLimit, err := parseByteSize(v.GetString("shim.limits.custom_tool_grammar_definition_bytes"))
	if err != nil {
		return Config{}, fmt.Errorf("parse shim.limits.custom_tool_grammar_definition_bytes: %w", err)
	}
	cfg.CustomToolGrammarDefinitionMaxBytes = customToolGrammarDefinitionLimit
	customToolCompiledPatternLimit, err := parseByteSize(v.GetString("shim.limits.custom_tool_compiled_pattern_bytes"))
	if err != nil {
		return Config{}, fmt.Errorf("parse shim.limits.custom_tool_compiled_pattern_bytes: %w", err)
	}
	cfg.CustomToolCompiledPatternMaxBytes = customToolCompiledPatternLimit
	retrievalMaxConcurrentSearches, err := parsePositiveInt(v.GetString("shim.limits.retrieval_max_concurrent_searches"))
	if err != nil {
		return Config{}, fmt.Errorf("parse shim.limits.retrieval_max_concurrent_searches: %w", err)
	}
	cfg.RetrievalMaxConcurrentSearches = retrievalMaxConcurrentSearches
	retrievalMaxSearchQueries, err := parsePositiveInt(v.GetString("shim.limits.retrieval_max_search_queries"))
	if err != nil {
		return Config{}, fmt.Errorf("parse shim.limits.retrieval_max_search_queries: %w", err)
	}
	cfg.RetrievalMaxSearchQueries = retrievalMaxSearchQueries
	retrievalMaxGroundingChunks, err := parsePositiveInt(v.GetString("shim.limits.retrieval_max_grounding_chunks"))
	if err != nil {
		return Config{}, fmt.Errorf("parse shim.limits.retrieval_max_grounding_chunks: %w", err)
	}
	cfg.RetrievalMaxGroundingChunks = retrievalMaxGroundingChunks
	codeInterpreterMaxConcurrentRuns, err := parsePositiveInt(v.GetString("shim.limits.code_interpreter_max_concurrent_runs"))
	if err != nil {
		return Config{}, fmt.Errorf("parse shim.limits.code_interpreter_max_concurrent_runs: %w", err)
	}
	cfg.ResponsesCodeInterpreterMaxConcurrentRuns = codeInterpreterMaxConcurrentRuns
	rateLimitRPM, err := parsePositiveInt(v.GetString("shim.rate_limit.requests_per_minute"))
	if err != nil {
		return Config{}, fmt.Errorf("parse shim.rate_limit.requests_per_minute: %w", err)
	}
	cfg.ShimRateLimitRequestsPerMinute = rateLimitRPM
	rateLimitBurst, err := parsePositiveInt(v.GetString("shim.rate_limit.burst"))
	if err != nil {
		return Config{}, fmt.Errorf("parse shim.rate_limit.burst: %w", err)
	}
	cfg.ShimRateLimitBurst = rateLimitBurst
	debugTracesMaxEntries, err := parseNonNegativeInt(v.GetString("shim.debug_traces.max_entries"))
	if err != nil {
		return Config{}, fmt.Errorf("parse shim.debug_traces.max_entries: %w", err)
	}
	cfg.ShimDebugTracesMaxEntries = debugTracesMaxEntries
	evidenceMaxEntries, err := parseNonNegativeInt(v.GetString("shim.evidence.max_entries"))
	if err != nil {
		return Config{}, fmt.Errorf("parse shim.evidence.max_entries: %w", err)
	}
	cfg.ShimEvidenceMaxEntries = evidenceMaxEntries
	if err := parseDuration(v.GetString("shim.evidence.stale_after"), &cfg.ShimEvidenceStaleAfter); err != nil {
		return Config{}, fmt.Errorf("parse shim.evidence.stale_after: %w", err)
	}
	uiBasePath, err := normalizeUIBasePath(v.GetString("ui.base_path"))
	if err != nil {
		return Config{}, fmt.Errorf("parse ui.base_path: %w", err)
	}
	cfg.UIBasePath = uiBasePath
	if err := parseDuration(v.GetString("responses.code_interpreter.execution_timeout"), &cfg.ResponsesCodeInterpreterTimeout); err != nil {
		return Config{}, fmt.Errorf("parse responses.code_interpreter.execution_timeout: %w", err)
	}
	if err := parseDuration(v.GetString("responses.web_search.timeout"), &cfg.ResponsesWebSearchTimeout); err != nil {
		return Config{}, fmt.Errorf("parse responses.web_search.timeout: %w", err)
	}
	if err := parseDuration(v.GetString("responses.image_generation.timeout"), &cfg.ResponsesImageGenerationTimeout); err != nil {
		return Config{}, fmt.Errorf("parse responses.image_generation.timeout: %w", err)
	}
	comfyUIPollInterval := firstNonEmptyConfigString(v.GetString("responses.image_generation.comfyui.poll_interval"), configString(comfyUISection["poll_interval"]))
	if err := parseDuration(comfyUIPollInterval, &cfg.ResponsesImageGenerationComfyUIPollInterval); err != nil {
		return Config{}, fmt.Errorf("parse responses.image_generation.comfyui.poll_interval: %w", err)
	}
	comfyUIMaxWait := firstNonEmptyConfigString(v.GetString("responses.image_generation.comfyui.max_wait"), configString(comfyUISection["max_wait"]))
	if err := parseDuration(comfyUIMaxWait, &cfg.ResponsesImageGenerationComfyUIMaxWait); err != nil {
		return Config{}, fmt.Errorf("parse responses.image_generation.comfyui.max_wait: %w", err)
	}
	comfyUIMaxImageBytesConfig := firstNonEmptyConfigString(v.GetString("responses.image_generation.comfyui.max_image_bytes"), configString(comfyUISection["max_image_bytes"]))
	comfyUIMaxImageBytes, err := parseByteSize(comfyUIMaxImageBytesConfig)
	if err != nil {
		return Config{}, fmt.Errorf("parse responses.image_generation.comfyui.max_image_bytes: %w", err)
	}
	cfg.ResponsesImageGenerationComfyUIMaxImageBytes = comfyUIMaxImageBytes
	normalizedImageGeneration, err := imagegen.NormalizeConfig(imagegen.Config{
		Backend: cfg.ResponsesImageGenerationBackend,
		BaseURL: cfg.ResponsesImageGenerationBaseURL,
		Timeout: cfg.ResponsesImageGenerationTimeout,
		ComfyUI: imagegen.ComfyUIConfig{
			Workflow:      cfg.ResponsesImageGenerationComfyUIWorkflow,
			WorkflowPath:  cfg.ResponsesImageGenerationComfyUIWorkflowPath,
			OutputNodeID:  cfg.ResponsesImageGenerationComfyUIOutputNodeID,
			PollInterval:  cfg.ResponsesImageGenerationComfyUIPollInterval,
			MaxWait:       cfg.ResponsesImageGenerationComfyUIMaxWait,
			MaxImageBytes: cfg.ResponsesImageGenerationComfyUIMaxImageBytes,
		},
	})
	if err != nil {
		return Config{}, fmt.Errorf("parse responses.image_generation config: %w", err)
	}
	cfg.ResponsesImageGenerationBackend = normalizedImageGeneration.Backend
	cfg.ResponsesImageGenerationBaseURL = normalizedImageGeneration.BaseURL
	cfg.ResponsesImageGenerationTimeout = normalizedImageGeneration.Timeout
	cfg.ResponsesImageGenerationComfyUIWorkflow = normalizedImageGeneration.ComfyUI.Workflow
	cfg.ResponsesImageGenerationComfyUIWorkflowPath = normalizedImageGeneration.ComfyUI.WorkflowPath
	cfg.ResponsesImageGenerationComfyUIOutputNodeID = normalizedImageGeneration.ComfyUI.OutputNodeID
	cfg.ResponsesImageGenerationComfyUIPollInterval = normalizedImageGeneration.ComfyUI.PollInterval
	cfg.ResponsesImageGenerationComfyUIMaxWait = normalizedImageGeneration.ComfyUI.MaxWait
	cfg.ResponsesImageGenerationComfyUIMaxImageBytes = normalizedImageGeneration.ComfyUI.MaxImageBytes
	if err := parseDuration(v.GetString("responses.compaction.timeout"), &cfg.ResponsesCompactionTimeout); err != nil {
		return Config{}, fmt.Errorf("parse responses.compaction.timeout: %w", err)
	}
	compactionMaxOutputTokens, err := parseNonNegativeInt(v.GetString("responses.compaction.max_output_tokens"))
	if err != nil {
		return Config{}, fmt.Errorf("parse responses.compaction.max_output_tokens: %w", err)
	}
	cfg.ResponsesCompactionMaxOutputTokens = compactionMaxOutputTokens
	compactionRetainedItems, err := parseNonNegativeInt(v.GetString("responses.compaction.retained_items"))
	if err != nil {
		return Config{}, fmt.Errorf("parse responses.compaction.retained_items: %w", err)
	}
	cfg.ResponsesCompactionRetainedItems = compactionRetainedItems
	compactionMaxInputRunes, err := parseNonNegativeInt(v.GetString("responses.compaction.max_input_chars"))
	if err != nil {
		return Config{}, fmt.Errorf("parse responses.compaction.max_input_chars: %w", err)
	}
	cfg.ResponsesCompactionMaxInputRunes = compactionMaxInputRunes
	if strings.TrimSpace(cfg.ResponsesCompactionBaseURL) == "" && cfg.ResponsesCompactionBackend == compactor.BackendModelAssistedText {
		cfg.ResponsesCompactionBaseURL = cfg.LlamaBaseURL
	}
	normalizedCompaction, err := compactor.NormalizeConfig(compactor.Config{
		Backend:         cfg.ResponsesCompactionBackend,
		BaseURL:         cfg.ResponsesCompactionBaseURL,
		Model:           cfg.ResponsesCompactionModel,
		Timeout:         cfg.ResponsesCompactionTimeout,
		MaxOutputTokens: cfg.ResponsesCompactionMaxOutputTokens,
		RetainedItems:   cfg.ResponsesCompactionRetainedItems,
		MaxInputRunes:   cfg.ResponsesCompactionMaxInputRunes,
	})
	if err != nil {
		return Config{}, fmt.Errorf("parse responses.compaction config: %w", err)
	}
	cfg.ResponsesCompactionBackend = normalizedCompaction.Backend
	cfg.ResponsesCompactionBaseURL = normalizedCompaction.BaseURL
	cfg.ResponsesCompactionModel = normalizedCompaction.Model
	cfg.ResponsesCompactionTimeout = normalizedCompaction.Timeout
	cfg.ResponsesCompactionMaxOutputTokens = normalizedCompaction.MaxOutputTokens
	cfg.ResponsesCompactionRetainedItems = normalizedCompaction.RetainedItems
	cfg.ResponsesCompactionMaxInputRunes = normalizedCompaction.MaxInputRunes
	memoryMaxNotes, err := parseNonNegativeInt(v.GetString("responses.memory.max_notes"))
	if err != nil {
		return Config{}, fmt.Errorf("parse responses.memory.max_notes: %w", err)
	}
	memoryMaxNoteBytes, err := parseByteSize(v.GetString("responses.memory.max_note_bytes"))
	if err != nil {
		return Config{}, fmt.Errorf("parse responses.memory.max_note_bytes: %w", err)
	}
	memoryMaxContextBytes, err := parseByteSize(v.GetString("responses.memory.max_context_bytes"))
	if err != nil {
		return Config{}, fmt.Errorf("parse responses.memory.max_context_bytes: %w", err)
	}
	normalizedMemory, err := memory.NormalizeConfig(memory.Config{
		Backend:           cfg.ResponsesMemoryBackend,
		Inject:            cfg.ResponsesMemoryInject,
		MaxNotes:          memoryMaxNotes,
		MaxNoteBytes:      int(memoryMaxNoteBytes),
		MaxContextBytes:   int(memoryMaxContextBytes),
		MetadataNamespace: cfg.ResponsesMemoryMetadataNamespace,
	})
	if err != nil {
		return Config{}, fmt.Errorf("parse responses.memory config: %w", err)
	}
	cfg.ResponsesMemoryBackend = normalizedMemory.Backend
	cfg.ResponsesMemoryInject = normalizedMemory.Inject
	cfg.ResponsesMemoryMaxNotes = normalizedMemory.MaxNotes
	cfg.ResponsesMemoryMaxNoteBytes = int64(normalizedMemory.MaxNoteBytes)
	cfg.ResponsesMemoryMaxContextBytes = int64(normalizedMemory.MaxContextBytes)
	cfg.ResponsesMemoryMetadataNamespace = normalizedMemory.MetadataNamespace
	if err := parseDuration(v.GetString("responses.code_interpreter.cleanup_interval"), &cfg.ResponsesCodeInterpreterCleanupInterval); err != nil {
		return Config{}, fmt.Errorf("parse responses.code_interpreter.cleanup_interval: %w", err)
	}
	pidsLimit, err := parsePositiveInt(v.GetString("responses.code_interpreter.docker.pids_limit"))
	if err != nil {
		return Config{}, fmt.Errorf("parse responses.code_interpreter.docker.pids_limit: %w", err)
	}
	cfg.ResponsesCodeInterpreterDockerPids = pidsLimit
	if cfg.ResponsesCodeInterpreterPythonBinary == "" {
		return Config{}, fmt.Errorf("parse responses.code_interpreter.python_binary: %w", strconv.ErrSyntax)
	}
	if cfg.ResponsesCodeInterpreterDockerBinary == "" {
		return Config{}, fmt.Errorf("parse responses.code_interpreter.docker.binary: %w", strconv.ErrSyntax)
	}
	if cfg.ResponsesCodeInterpreterDockerImage == "" {
		return Config{}, fmt.Errorf("parse responses.code_interpreter.docker.image: %w", strconv.ErrSyntax)
	}
	if cfg.ResponsesCodeInterpreterDockerMemory == "" {
		return Config{}, fmt.Errorf("parse responses.code_interpreter.docker.memory_limit: %w", strconv.ErrSyntax)
	}
	if cfg.ResponsesCodeInterpreterDockerCPU == "" {
		return Config{}, fmt.Errorf("parse responses.code_interpreter.docker.cpu_limit: %w", strconv.ErrSyntax)
	}
	generatedFiles, err := parsePositiveInt(v.GetString("responses.code_interpreter.limits.generated_files"))
	if err != nil {
		return Config{}, fmt.Errorf("parse responses.code_interpreter.limits.generated_files: %w", err)
	}
	cfg.ResponsesCodeInterpreterGeneratedFiles = generatedFiles
	generatedFileBytes, err := parseByteSize(v.GetString("responses.code_interpreter.limits.generated_file_bytes"))
	if err != nil {
		return Config{}, fmt.Errorf("parse responses.code_interpreter.limits.generated_file_bytes: %w", err)
	}
	cfg.ResponsesCodeInterpreterGeneratedFileBytes = generatedFileBytes
	generatedTotalBytes, err := parseByteSize(v.GetString("responses.code_interpreter.limits.generated_total_bytes"))
	if err != nil {
		return Config{}, fmt.Errorf("parse responses.code_interpreter.limits.generated_total_bytes: %w", err)
	}
	cfg.ResponsesCodeInterpreterGeneratedTotalBytes = generatedTotalBytes
	remoteInputFileBytes, err := parseByteSize(v.GetString("responses.code_interpreter.limits.remote_input_file_bytes"))
	if err != nil {
		return Config{}, fmt.Errorf("parse responses.code_interpreter.limits.remote_input_file_bytes: %w", err)
	}
	cfg.ResponsesCodeInterpreterRemoteInputFileBytes = remoteInputFileBytes
	webSearchMaxResults, err := parsePositiveInt(v.GetString("responses.web_search.max_results"))
	if err != nil {
		return Config{}, fmt.Errorf("parse responses.web_search.max_results: %w", err)
	}
	cfg.ResponsesWebSearchMaxResults = webSearchMaxResults
	return cfg, nil
}

func (c Config) MaintenanceCleanupPolicy() storage.MaintenanceCleanupPolicy {
	return storage.MaintenanceCleanupPolicy{
		ResponseReplayArtifactsMaxAge:       c.StorageResponseReplayArtifactsMaxAge,
		ResponseReplayArtifactsMaxResponses: c.StorageResponseReplayArtifactsMaxResponses,
	}
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("shim.addr", ":8080")
	v.SetDefault("shim.read_timeout", "15s")
	v.SetDefault("shim.write_timeout", "90s")
	v.SetDefault("shim.idle_timeout", "60s")
	v.SetDefault("shim.shutdown_timeout", "30s")
	v.SetDefault("shim.auth.mode", ShimAuthModeDisabled)
	v.SetDefault("shim.auth.bearer_tokens", []string{})
	v.SetDefault("shim.rate_limit.enabled", false)
	v.SetDefault("shim.rate_limit.requests_per_minute", "120")
	v.SetDefault("shim.rate_limit.burst", "60")
	v.SetDefault("shim.metrics.enabled", true)
	v.SetDefault("shim.metrics.path", "/metrics")
	v.SetDefault("shim.debug_traces.enabled", true)
	v.SetDefault("shim.debug_traces.max_entries", "256")
	v.SetDefault("shim.evidence.enabled", true)
	v.SetDefault("shim.evidence.root", ".tmp")
	v.SetDefault("shim.evidence.max_entries", "50")
	v.SetDefault("shim.evidence.stale_after", "168h")
	v.SetDefault("ui.enabled", false)
	v.SetDefault("ui.base_path", "/ui/")
	v.SetDefault("ui.public_static_assets", true)
	v.SetDefault("shim.limits.json_body_bytes", "1MiB")
	v.SetDefault("shim.limits.retrieval_file_upload_bytes", "64MiB")
	v.SetDefault("shim.limits.chat_completions_shadow_store_bytes", "64MiB")
	v.SetDefault("shim.limits.chat_completions_shadow_store_timeout", "5s")
	v.SetDefault("shim.limits.responses_proxy_buffer_bytes", "64MiB")
	v.SetDefault("shim.limits.responses_stored_lineage_max_items", "128")
	v.SetDefault("shim.limits.responses_local_tool_output_summary_bytes", "64KiB")
	v.SetDefault("shim.limits.custom_tool_grammar_definition_bytes", "16KiB")
	v.SetDefault("shim.limits.custom_tool_compiled_pattern_bytes", "32KiB")
	v.SetDefault("shim.limits.retrieval_max_concurrent_searches", "8")
	v.SetDefault("shim.limits.retrieval_max_search_queries", "4")
	v.SetDefault("shim.limits.retrieval_max_grounding_chunks", "20")
	v.SetDefault("shim.limits.code_interpreter_max_concurrent_runs", "2")
	v.SetDefault("storage.backend", storage.BackendSQLite)
	v.SetDefault("sqlite.path", "./data/shim.db")
	v.SetDefault("postgres.dsn", "")
	v.SetDefault("sqlite.maintenance.cleanup_interval", "15m")
	v.SetDefault("storage.retention.response_replay_artifacts.max_age", "0s")
	v.SetDefault("storage.retention.response_replay_artifacts.max_responses", "0")
	v.SetDefault("llama.base_url", "http://127.0.0.1:8081")
	v.SetDefault("llama.providers", []map[string]any{})
	v.SetDefault("llama.readiness_bearer_token", "")
	v.SetDefault("llama.timeout", "60s")
	v.SetDefault("llama.max_concurrent_requests", "4")
	v.SetDefault("llama.max_queue_wait", "0s")
	v.SetDefault("llama.http.max_idle_conns", "32")
	v.SetDefault("llama.http.max_idle_conns_per_host", "16")
	v.SetDefault("llama.http.max_conns_per_host", "8")
	v.SetDefault("llama.http.idle_conn_timeout", "90s")
	v.SetDefault("llama.http.dial_timeout", "10s")
	v.SetDefault("llama.http.keep_alive", "30s")
	v.SetDefault("llama.http.tls_handshake_timeout", "10s")
	v.SetDefault("llama.http.expect_continue_timeout", "1s")
	v.SetDefault("log.level", "info")
	v.SetDefault("log.file_path", "")
	v.SetDefault("retrieval.index.backend", retrieval.IndexBackendLexical)
	v.SetDefault("retrieval.embedder.backend", retrieval.EmbedderBackendDisabled)
	v.SetDefault("retrieval.embedder.base_url", "")
	v.SetDefault("retrieval.embedder.model", "")
	v.SetDefault("retrieval.index.pgvector.ann.enabled", false)
	v.SetDefault("retrieval.index.pgvector.ann.method", retrieval.PGVectorANNMethodHNSW)
	v.SetDefault("retrieval.index.pgvector.ann.metric", retrieval.PGVectorANNMetricCosine)
	v.SetDefault("retrieval.index.pgvector.ann.dimensions", "0")
	v.SetDefault("retrieval.index.pgvector.ann.hnsw_m", "16")
	v.SetDefault("retrieval.index.pgvector.ann.hnsw_ef_construction", "64")
	v.SetDefault("retrieval.index.pgvector.ann.ivfflat_lists", "100")
	v.SetDefault("chat_completions.default_store_when_omitted", true)
	v.SetDefault("chat_completions.upstream_compatibility.models", []map[string]any{})
	v.SetDefault("responses.mode", ResponsesModePreferLocal)
	v.SetDefault("responses.upstream_transport", ResponsesUpstreamTransportResponses)
	v.SetDefault("responses.websocket.enabled", true)
	v.SetDefault("responses.custom_tools.mode", "auto")
	v.SetDefault("responses.constrained_decoding.backend", ResponsesConstrainedDecodingBackendShimValidateRepair)
	v.SetDefault("responses.upstream_tool_compatibility.models", []map[string]any{})
	v.SetDefault("responses.codex.enable_compatibility", true)
	v.SetDefault("responses.codex.upstream_input_compatibility.models", []map[string]any{})
	v.SetDefault("responses.codex.model_metadata.models", []map[string]any{})
	v.SetDefault("responses.web_search.backend", websearch.BackendDisabled)
	v.SetDefault("responses.web_search.base_url", "")
	v.SetDefault("responses.web_search.timeout", "10s")
	v.SetDefault("responses.web_search.max_results", "10")
	v.SetDefault("responses.image_generation.backend", imagegen.BackendDisabled)
	v.SetDefault("responses.image_generation.base_url", "")
	v.SetDefault("responses.image_generation.timeout", "60s")
	v.SetDefault("responses.image_generation.comfyui.workflow", map[string]any{})
	v.SetDefault("responses.image_generation.comfyui.workflow_path", "")
	v.SetDefault("responses.image_generation.comfyui.output_node_id", "")
	v.SetDefault("responses.image_generation.comfyui.poll_interval", "500ms")
	v.SetDefault("responses.image_generation.comfyui.max_wait", "120s")
	v.SetDefault("responses.image_generation.comfyui.max_image_bytes", "16MiB")
	v.SetDefault("responses.compaction.backend", compactor.BackendHeuristic)
	v.SetDefault("responses.compaction.base_url", "")
	v.SetDefault("responses.compaction.model", "")
	v.SetDefault("responses.compaction.timeout", "10s")
	v.SetDefault("responses.compaction.max_output_tokens", "1200")
	v.SetDefault("responses.compaction.retained_items", "8")
	v.SetDefault("responses.compaction.max_input_chars", "60000")
	v.SetDefault("responses.memory.backend", memory.BackendDisabled)
	v.SetDefault("responses.memory.inject", false)
	v.SetDefault("responses.memory.max_notes", "8")
	v.SetDefault("responses.memory.max_note_bytes", "2KiB")
	v.SetDefault("responses.memory.max_context_bytes", "8KiB")
	v.SetDefault("responses.memory.metadata_namespace", memory.DefaultMetadataNamespace)
	v.SetDefault("responses.computer.backend", ResponsesComputerBackendDisabled)
	v.SetDefault("responses.code_interpreter.backend", "")
	v.SetDefault("responses.code_interpreter.enable_unsafe_host_executor", false)
	v.SetDefault("responses.code_interpreter.python_binary", "python3")
	v.SetDefault("responses.code_interpreter.execution_timeout", "20s")
	v.SetDefault("responses.code_interpreter.docker.binary", "docker")
	v.SetDefault("responses.code_interpreter.docker.image", "python:3.12-slim")
	v.SetDefault("responses.code_interpreter.docker.memory_limit", "1g")
	v.SetDefault("responses.code_interpreter.docker.cpu_limit", "0.5")
	v.SetDefault("responses.code_interpreter.docker.pids_limit", "64")
	v.SetDefault("responses.code_interpreter.input_file_url_policy", ResponsesCodeInterpreterInputFileURLPolicyDisabled)
	v.SetDefault("responses.code_interpreter.input_file_url_allow_hosts", []string{})
	v.SetDefault("responses.code_interpreter.cleanup_interval", "1m")
	v.SetDefault("responses.code_interpreter.limits.generated_files", "8")
	v.SetDefault("responses.code_interpreter.limits.generated_file_bytes", "2MiB")
	v.SetDefault("responses.code_interpreter.limits.generated_total_bytes", "8MiB")
	v.SetDefault("responses.code_interpreter.limits.remote_input_file_bytes", "50MiB")
}

func resolveConfigPath(configPath string) string {
	if strings.TrimSpace(configPath) != "" {
		return configPath
	}
	return strings.TrimSpace(os.Getenv("SHIM_CONFIG"))
}

func resolveDotEnvPath() string {
	if override := strings.TrimSpace(os.Getenv("SHIM_DOTENV")); override != "" {
		return override
	}
	return ".env"
}

func loadDotEnv(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read dotenv file %q: %w", path, err)
	}
	for idx, rawLine := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(strings.TrimRight(rawLine, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return fmt.Errorf("parse dotenv file %q line %d: missing '='", path, idx+1)
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return fmt.Errorf("parse dotenv file %q line %d: empty key", path, idx+1)
		}
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 {
			switch value[0] {
			case '"', '\'':
				if value[len(value)-1] == value[0] {
					value = value[1 : len(value)-1]
				}
			}
		}
		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("set dotenv env %q from %q: %w", key, path, err)
		}
	}
	return nil
}

func readConfigFile(v *viper.Viper, configPath string) error {
	return readConfigFileNamed(v, configPath, "config")
}

func readConfigFileNamed(v *viper.Viper, configPath string, configName string) error {
	if configPath != "" {
		v.SetConfigFile(configPath)
		if err := v.ReadInConfig(); err != nil {
			return fmt.Errorf("read config file %q: %w", configPath, err)
		}
		return nil
	}

	v.SetConfigName(strings.TrimSpace(configName))
	v.AddConfigPath(".")
	if err := v.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if errors.As(err, &notFound) {
			return nil
		}
		return fmt.Errorf("read config file: %w", err)
	}
	return nil
}

func parseDuration(value string, dst *time.Duration) error {
	parsed, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil {
		return err
	}
	*dst = parsed
	return nil
}

func parseLogLevel(value string, dst *slog.Level) error {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "debug":
		*dst = slog.LevelDebug
	case "info":
		*dst = slog.LevelInfo
	case "warn", "warning":
		*dst = slog.LevelWarn
	case "error":
		*dst = slog.LevelError
	default:
		if n, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
			*dst = slog.Level(n)
			return nil
		}
		return strconv.ErrSyntax
	}

	return nil
}

func parseCustomToolsMode(value string) error {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "bridge", "passthrough", "auto":
		return nil
	default:
		return strconv.ErrSyntax
	}
}

func parseConstrainedDecodingBackend(value string) error {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", ResponsesConstrainedDecodingBackendShimValidateRepair, ResponsesConstrainedDecodingBackendVLLM:
		return nil
	default:
		return strconv.ErrSyntax
	}
}

func parseLlamaProviders(v *viper.Viper) ([]LlamaProvider, error) {
	var providers []LlamaProvider
	if err := v.UnmarshalKey("llama.providers", &providers); err != nil {
		return nil, fmt.Errorf("parse llama.providers: %w", err)
	}

	seenProviders := make(map[string]struct{}, len(providers))
	for providerIdx := range providers {
		provider := &providers[providerIdx]
		provider.ID = strings.TrimSpace(provider.ID)
		provider.BaseURL = strings.TrimRight(strings.TrimSpace(provider.BaseURL), "/")
		provider.BearerTokenEnv = strings.TrimSpace(provider.BearerTokenEnv)
		if provider.ID == "" {
			return nil, fmt.Errorf("parse llama.providers: id is required")
		}
		if strings.Contains(provider.ID, "/") || strings.ContainsAny(provider.ID, " \t\r\n") {
			return nil, fmt.Errorf("parse llama.providers: id %q must not contain slashes or whitespace", provider.ID)
		}
		if _, exists := seenProviders[provider.ID]; exists {
			return nil, fmt.Errorf("parse llama.providers: duplicate provider id %q", provider.ID)
		}
		seenProviders[provider.ID] = struct{}{}
		if provider.BaseURL == "" {
			return nil, fmt.Errorf("parse llama.providers: base_url is required for provider %q", provider.ID)
		}
		if len(provider.Models) == 0 {
			return nil, fmt.Errorf("parse llama.providers: provider %q must configure at least one model", provider.ID)
		}
		if provider.BearerTokenEnv != "" {
			provider.BearerToken = strings.TrimSpace(os.Getenv(provider.BearerTokenEnv))
		}
		seenModels := make(map[string]struct{}, len(provider.Models))
		for modelIdx := range provider.Models {
			model := &provider.Models[modelIdx]
			model.Model = strings.TrimSpace(model.Model)
			model.UpstreamModel = strings.TrimSpace(model.UpstreamModel)
			if model.Model == "" {
				return nil, fmt.Errorf("parse llama.providers: model is required for provider %q", provider.ID)
			}
			if strings.HasPrefix(model.Model, "/") || strings.HasSuffix(model.Model, "/") || strings.ContainsAny(model.Model, " \t\r\n") {
				return nil, fmt.Errorf("parse llama.providers: model %q for provider %q must not have empty slash parts or whitespace", model.Model, provider.ID)
			}
			if _, exists := seenModels[model.Model]; exists {
				return nil, fmt.Errorf("parse llama.providers: duplicate model %q for provider %q", model.Model, provider.ID)
			}
			seenModels[model.Model] = struct{}{}
			if model.UpstreamModel == "" {
				model.UpstreamModel = model.Model
			}
		}
	}
	return providers, nil
}

func parseChatCompletionsUpstreamCompatibility(v *viper.Viper) ([]ChatCompletionsUpstreamCompatibilityRule, error) {
	var rules []ChatCompletionsUpstreamCompatibilityRule
	if err := v.UnmarshalKey("chat_completions.upstream_compatibility.models", &rules); err != nil {
		return nil, fmt.Errorf("parse chat_completions.upstream_compatibility.models: %w", err)
	}

	for i := range rules {
		rules[i].Model = strings.TrimSpace(rules[i].Model)
		if rules[i].Model == "" {
			return nil, fmt.Errorf("parse chat_completions.upstream_compatibility.models: model is required")
		}
		defaultThinking, err := normalizeConfigEnum(rules[i].DefaultThinking, "passthrough", []string{"passthrough", "disabled"})
		if err != nil {
			return nil, fmt.Errorf("parse chat_completions.upstream_compatibility.models: default_thinking: %w", err)
		}
		jsonSchemaMode, err := normalizeConfigEnum(rules[i].JSONSchemaMode, "passthrough", []string{"passthrough", "json_object_instruction"})
		if err != nil {
			return nil, fmt.Errorf("parse chat_completions.upstream_compatibility.models: json_schema_mode: %w", err)
		}
		rules[i].DefaultThinking = defaultThinking
		rules[i].JSONSchemaMode = jsonSchemaMode
		if rules[i].DefaultMaxTokens < 0 {
			return nil, fmt.Errorf("parse chat_completions.upstream_compatibility.models: default_max_tokens must be greater than or equal to 0")
		}
	}
	return rules, nil
}

func parseResponsesUpstreamToolCompatibility(v *viper.Viper) ([]ResponsesUpstreamToolCompatibilityRule, error) {
	var rules []ResponsesUpstreamToolCompatibilityRule
	if err := v.UnmarshalKey("responses.upstream_tool_compatibility.models", &rules); err != nil {
		return nil, fmt.Errorf("parse responses.upstream_tool_compatibility.models: %w", err)
	}

	for i := range rules {
		rules[i].Model = strings.TrimSpace(rules[i].Model)
		if rules[i].Model == "" {
			return nil, fmt.Errorf("parse responses.upstream_tool_compatibility.models: model is required")
		}
		seen := make(map[string]struct{}, len(rules[i].DisabledTools))
		disabled := make([]string, 0, len(rules[i].DisabledTools))
		for _, tool := range rules[i].DisabledTools {
			normalized := strings.ToLower(strings.TrimSpace(tool))
			if normalized == "" {
				continue
			}
			if _, ok := seen[normalized]; ok {
				continue
			}
			seen[normalized] = struct{}{}
			disabled = append(disabled, normalized)
		}
		rules[i].DisabledTools = disabled
	}
	return rules, nil
}

func parseResponsesCodexUpstreamInputCompatibility(v *viper.Viper) ([]ResponsesCodexUpstreamInputCompatibilityRule, error) {
	var rules []ResponsesCodexUpstreamInputCompatibilityRule
	if err := v.UnmarshalKey("responses.codex.upstream_input_compatibility.models", &rules); err != nil {
		return nil, fmt.Errorf("parse responses.codex.upstream_input_compatibility.models: %w", err)
	}

	for i := range rules {
		rules[i].Model = strings.TrimSpace(rules[i].Model)
		if rules[i].Model == "" {
			return nil, fmt.Errorf("parse responses.codex.upstream_input_compatibility.models: model is required")
		}
		mode, err := normalizeConfigEnum(rules[i].Mode, "auto", []string{"auto", "structured", "stringify"})
		if err != nil {
			return nil, fmt.Errorf("parse responses.codex.upstream_input_compatibility.models: mode: %w", err)
		}
		rules[i].Mode = mode
	}
	return rules, nil
}

func parseResponsesCodexModelMetadata(v *viper.Viper) ([]ResponsesCodexModelMetadata, error) {
	var models []ResponsesCodexModelMetadata
	if err := v.UnmarshalKey("responses.codex.model_metadata.models", &models); err != nil {
		return nil, fmt.Errorf("parse responses.codex.model_metadata.models: %w", err)
	}

	for i := range models {
		models[i].Model = strings.TrimSpace(models[i].Model)
		if models[i].Model == "" {
			return nil, fmt.Errorf("parse responses.codex.model_metadata.models: model is required")
		}
		if models[i].DisplayName = strings.TrimSpace(models[i].DisplayName); models[i].DisplayName == "" {
			models[i].DisplayName = models[i].Model
		}
		if models[i].Description = strings.TrimSpace(models[i].Description); models[i].Description == "" {
			models[i].Description = "OpenAI-compatible upstream routed through llama_shim."
		}
		if models[i].ContextWindow < 0 {
			return nil, fmt.Errorf("parse responses.codex.model_metadata.models: context_window must be non-negative")
		}
		if models[i].MaxContextWindow < 0 {
			return nil, fmt.Errorf("parse responses.codex.model_metadata.models: max_context_window must be non-negative")
		}
		if models[i].AutoCompactTokenLimit < 0 {
			return nil, fmt.Errorf("parse responses.codex.model_metadata.models: auto_compact_token_limit must be non-negative")
		}
		if models[i].EffectiveContextWindowPercent < 0 {
			return nil, fmt.Errorf("parse responses.codex.model_metadata.models: effective_context_window_percent must be non-negative")
		}
		if models[i].EffectiveContextWindowPercent == 0 {
			models[i].EffectiveContextWindowPercent = 95
		}
		if models[i].EffectiveContextWindowPercent > 100 {
			return nil, fmt.Errorf("parse responses.codex.model_metadata.models: effective_context_window_percent must be <= 100")
		}
		level, err := normalizeConfigEnum(models[i].DefaultReasoningLevel, "high", []string{"none", "minimal", "low", "medium", "high", "xhigh"})
		if err != nil {
			return nil, fmt.Errorf("parse responses.codex.model_metadata.models: default_reasoning_level: %w", err)
		}
		models[i].DefaultReasoningLevel = level
		if len(models[i].SupportedReasoningLevels) == 0 {
			models[i].SupportedReasoningLevels = []string{"low", "medium", "high"}
		}
		for j, value := range models[i].SupportedReasoningLevels {
			level, err := normalizeConfigEnum(value, "", []string{"none", "minimal", "low", "medium", "high", "xhigh"})
			if err != nil {
				return nil, fmt.Errorf("parse responses.codex.model_metadata.models: supported_reasoning_levels: %w", err)
			}
			models[i].SupportedReasoningLevels[j] = level
		}
		summary, err := normalizeConfigEnum(models[i].DefaultReasoningSummary, "none", []string{"auto", "concise", "detailed", "none"})
		if err != nil {
			return nil, fmt.Errorf("parse responses.codex.model_metadata.models: default_reasoning_summary: %w", err)
		}
		models[i].DefaultReasoningSummary = summary
		shellType, err := normalizeConfigEnum(models[i].ShellType, "shell_command", []string{"default", "local", "unified_exec", "disabled", "shell_command"})
		if err != nil {
			return nil, fmt.Errorf("parse responses.codex.model_metadata.models: shell_type: %w", err)
		}
		models[i].ShellType = shellType
		applyPatchToolType, err := normalizeConfigEnum(models[i].ApplyPatchToolType, "", []string{"freeform", "function"})
		if err != nil {
			return nil, fmt.Errorf("parse responses.codex.model_metadata.models: apply_patch_tool_type: %w", err)
		}
		models[i].ApplyPatchToolType = applyPatchToolType
		webSearchToolType, err := normalizeConfigEnum(models[i].WebSearchToolType, "text", []string{"text", "text_and_image"})
		if err != nil {
			return nil, fmt.Errorf("parse responses.codex.model_metadata.models: web_search_tool_type: %w", err)
		}
		models[i].WebSearchToolType = webSearchToolType
		defaultVerbosity, err := normalizeConfigEnum(models[i].DefaultVerbosity, "", []string{"low", "medium", "high"})
		if err != nil {
			return nil, fmt.Errorf("parse responses.codex.model_metadata.models: default_verbosity: %w", err)
		}
		models[i].DefaultVerbosity = defaultVerbosity
		if len(models[i].InputModalities) == 0 {
			models[i].InputModalities = []string{"text"}
		}
		for j, value := range models[i].InputModalities {
			modality, err := normalizeConfigEnum(value, "", []string{"text", "image"})
			if err != nil {
				return nil, fmt.Errorf("parse responses.codex.model_metadata.models: input_modalities: %w", err)
			}
			models[i].InputModalities[j] = modality
		}
		visibility, err := normalizeConfigEnum(models[i].Visibility, "list", []string{"list", "hide", "none"})
		if err != nil {
			return nil, fmt.Errorf("parse responses.codex.model_metadata.models: visibility: %w", err)
		}
		models[i].Visibility = visibility
		if models[i].SupportedInAPI == nil {
			value := true
			models[i].SupportedInAPI = &value
		}
		if models[i].Priority == nil {
			value := 100
			models[i].Priority = &value
		}
		models[i].AdditionalSpeedTiers = normalizeStringList(models[i].AdditionalSpeedTiers)
		models[i].ExperimentalSupportedTools = normalizeStringList(models[i].ExperimentalSupportedTools)
		models[i].AvailabilityNuxMessage = strings.TrimSpace(models[i].AvailabilityNuxMessage)
		truncationMode, err := normalizeConfigEnum(models[i].TruncationPolicy.Mode, "bytes", []string{"bytes", "tokens"})
		if err != nil {
			return nil, fmt.Errorf("parse responses.codex.model_metadata.models: truncation_policy.mode: %w", err)
		}
		models[i].TruncationPolicy.Mode = truncationMode
		if models[i].TruncationPolicy.Limit < 0 {
			return nil, fmt.Errorf("parse responses.codex.model_metadata.models: truncation_policy.limit must be non-negative")
		}
		if models[i].TruncationPolicy.Limit == 0 {
			models[i].TruncationPolicy.Limit = 10000
		}
	}
	return models, nil
}

func normalizeStringList(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := values[:0]
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func normalizeConfigEnum(value string, defaultValue string, allowed []string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	if normalized == "" {
		return defaultValue, nil
	}
	for _, candidate := range allowed {
		if normalized == candidate {
			return normalized, nil
		}
	}
	return "", strconv.ErrSyntax
}

func parseResponsesMode(value string) error {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", ResponsesModePreferLocal, ResponsesModePreferUpstream, ResponsesModeLocalOnly:
		return nil
	default:
		return strconv.ErrSyntax
	}
}

func parseResponsesUpstreamTransport(value string) error {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", ResponsesUpstreamTransportResponses, ResponsesUpstreamTransportChatCompletions:
		return nil
	default:
		return strconv.ErrSyntax
	}
}

func parseShimAuthMode(value string) error {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", ShimAuthModeDisabled, ShimAuthModeStaticBearer:
		return nil
	default:
		return strconv.ErrSyntax
	}
}

func normalizeUIBasePath(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		trimmed = "/ui/"
	}
	if !strings.HasPrefix(trimmed, "/") || strings.ContainsAny(trimmed, "?#\\") || strings.Contains(trimmed, "..") {
		return "", strconv.ErrSyntax
	}
	cleaned := path.Clean(trimmed)
	if cleaned == "/" || strings.Contains(cleaned, "/../") || strings.HasSuffix(cleaned, "/..") || reservedUIBasePath(cleaned) {
		return "", strconv.ErrSyntax
	}
	return strings.TrimRight(cleaned, "/") + "/", nil
}

func reservedUIBasePath(cleaned string) bool {
	reserved := []string{"/v1", "/debug", "/healthz", "/readyz", "/metrics", "/api"}
	for _, prefix := range reserved {
		if cleaned == prefix || strings.HasPrefix(cleaned, prefix+"/") {
			return true
		}
	}
	return false
}

func parseCodeInterpreterBackend(value string) error {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case ResponsesCodeInterpreterBackendDisabled, ResponsesCodeInterpreterBackendDocker:
		return nil
	default:
		return strconv.ErrSyntax
	}
}

func parseComputerBackend(value string) error {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", ResponsesComputerBackendDisabled, ResponsesComputerBackendChatCompletions:
		return nil
	default:
		return strconv.ErrSyntax
	}
}

func parseCodeInterpreterInputFileURLPolicy(value string) error {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case ResponsesCodeInterpreterInputFileURLPolicyDisabled,
		ResponsesCodeInterpreterInputFileURLPolicyAllowlist,
		ResponsesCodeInterpreterInputFileURLPolicyUnsafeAllowHTTPHTTPS:
		return nil
	default:
		return strconv.ErrSyntax
	}
}

func parsePositiveInt(value string) (int, error) {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0, err
	}
	if parsed <= 0 {
		return 0, strconv.ErrSyntax
	}
	return parsed, nil
}

func parseNonNegativeInt(value string) (int, error) {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0, err
	}
	if parsed < 0 {
		return 0, strconv.ErrSyntax
	}
	return parsed, nil
}

func parseByteSize(value string) (int64, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0, strconv.ErrSyntax
	}

	suffixes := []struct {
		Suffix string
		Scale  int64
	}{
		{"kib", 1 << 10},
		{"mib", 1 << 20},
		{"gib", 1 << 30},
		{"kb", 1 << 10},
		{"mb", 1 << 20},
		{"gb", 1 << 30},
		{"b", 1},
	}
	lower := strings.ToLower(trimmed)
	for _, suffix := range suffixes {
		if !strings.HasSuffix(lower, suffix.Suffix) {
			continue
		}
		base := strings.TrimSpace(trimmed[:len(trimmed)-len(suffix.Suffix)])
		parsed, err := strconv.ParseInt(base, 10, 64)
		if err != nil || parsed <= 0 {
			return 0, strconv.ErrSyntax
		}
		return parsed * suffix.Scale, nil
	}

	parsed, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil || parsed <= 0 {
		return 0, strconv.ErrSyntax
	}
	return parsed, nil
}

func parseStringList(v *viper.Viper, key string) []string {
	values := v.GetStringSlice(key)
	if len(values) == 0 {
		if raw := strings.TrimSpace(v.GetString(key)); raw != "" {
			values = strings.Split(raw, ",")
		}
	} else if len(values) == 1 && strings.Contains(values[0], ",") {
		values = strings.Split(values[0], ",")
	}

	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		normalized := strings.ToLower(trimmed)
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func normalizeConfigMap(value any) map[string]any {
	normalized, ok := normalizeConfigValue(value).(map[string]any)
	if !ok {
		return map[string]any{}
	}
	return normalized
}

func firstNonEmptyConfigString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func configString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	default:
		if value == nil {
			return ""
		}
		return fmt.Sprint(value)
	}
}

func normalizeConfigValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[key] = normalizeConfigValue(item)
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[fmt.Sprint(key)] = normalizeConfigValue(item)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = normalizeConfigValue(item)
		}
		return out
	default:
		return value
	}
}
