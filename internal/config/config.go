package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"llama_shim/internal/compactor"
	"llama_shim/internal/imagegen"
	"llama_shim/internal/retrieval"
	"llama_shim/internal/websearch"

	"github.com/spf13/viper"
)

type Config struct {
	Addr                                           string
	SQLitePath                                     string
	SQLiteMaintenanceCleanupInterval               time.Duration
	LlamaBaseURL                                   string
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
	ShimAuthMode                                   string
	ShimAuthBearerTokens                           []string
	ShimRateLimitEnabled                           bool
	ShimRateLimitRequestsPerMinute                 int
	ShimRateLimitBurst                             int
	ShimMetricsEnabled                             bool
	ShimMetricsPath                                string
	ShimJSONBodyLimitBytes                         int64
	RetrievalFileUploadMaxBytes                    int64
	ChatCompletionsShadowStoreMaxBytes             int64
	ResponsesProxyBufferMaxBytes                   int64
	ResponsesStoredLineageMaxItems                 int
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
	ResponsesWebSearchBackend                      string
	ResponsesWebSearchBaseURL                      string
	ResponsesWebSearchTimeout                      time.Duration
	ResponsesWebSearchMaxResults                   int
	ResponsesImageGenerationBackend                string
	ResponsesImageGenerationBaseURL                string
	ResponsesImageGenerationTimeout                time.Duration
	ResponsesCompactionBackend                     string
	ResponsesCompactionBaseURL                     string
	ResponsesCompactionModel                       string
	ResponsesCompactionTimeout                     time.Duration
	ResponsesCompactionMaxOutputTokens             int
	ResponsesCompactionRetainedItems               int
	ResponsesCompactionMaxInputRunes               int
	ResponsesComputerBackend                       string
	ChatCompletionsStoreWhenOmitted                bool
	ResponsesMode                                  string
	ResponsesWebSocketEnabled                      bool
	ResponsesCustomToolsMode                       string
	ResponsesCodexEnableCompatibility              bool
	ResponsesCodexForceToolChoiceRequired          bool
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

const (
	ResponsesModePreferLocal                                       = "prefer_local"
	ResponsesModePreferUpstream                                    = "prefer_upstream"
	ResponsesModeLocalOnly                                         = "local_only"
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

	cfg := Config{
		Addr:                                           strings.TrimSpace(v.GetString("shim.addr")),
		SQLitePath:                                     strings.TrimSpace(v.GetString("sqlite.path")),
		SQLiteMaintenanceCleanupInterval:               0,
		LlamaBaseURL:                                   strings.TrimRight(strings.TrimSpace(v.GetString("llama.base_url")), "/"),
		ConfigFile:                                     v.ConfigFileUsed(),
		ShimAuthMode:                                   strings.ToLower(strings.TrimSpace(v.GetString("shim.auth.mode"))),
		ShimAuthBearerTokens:                           parseStringList(v, "shim.auth.bearer_tokens"),
		ShimRateLimitEnabled:                           v.GetBool("shim.rate_limit.enabled"),
		ShimMetricsEnabled:                             v.GetBool("shim.metrics.enabled"),
		ShimMetricsPath:                                strings.TrimSpace(v.GetString("shim.metrics.path")),
		LogLevel:                                       slog.LevelInfo,
		LogFilePath:                                    strings.TrimSpace(v.GetString("log.file_path")),
		RetrievalIndexBackend:                          strings.TrimSpace(v.GetString("retrieval.index.backend")),
		RetrievalEmbedderBackend:                       strings.TrimSpace(v.GetString("retrieval.embedder.backend")),
		RetrievalEmbedderBaseURL:                       strings.TrimSpace(v.GetString("retrieval.embedder.base_url")),
		RetrievalEmbedderModel:                         strings.TrimSpace(v.GetString("retrieval.embedder.model")),
		ResponsesWebSearchBackend:                      strings.ToLower(strings.TrimSpace(v.GetString("responses.web_search.backend"))),
		ResponsesWebSearchBaseURL:                      strings.TrimSpace(v.GetString("responses.web_search.base_url")),
		ResponsesImageGenerationBackend:                strings.ToLower(strings.TrimSpace(v.GetString("responses.image_generation.backend"))),
		ResponsesImageGenerationBaseURL:                strings.TrimSpace(v.GetString("responses.image_generation.base_url")),
		ResponsesCompactionBackend:                     strings.ToLower(strings.TrimSpace(v.GetString("responses.compaction.backend"))),
		ResponsesCompactionBaseURL:                     strings.TrimSpace(v.GetString("responses.compaction.base_url")),
		ResponsesCompactionModel:                       strings.TrimSpace(v.GetString("responses.compaction.model")),
		ResponsesComputerBackend:                       strings.ToLower(strings.TrimSpace(v.GetString("responses.computer.backend"))),
		ChatCompletionsStoreWhenOmitted:                v.GetBool("chat_completions.default_store_when_omitted"),
		ResponsesMode:                                  strings.ToLower(strings.TrimSpace(v.GetString("responses.mode"))),
		ResponsesWebSocketEnabled:                      v.GetBool("responses.websocket.enabled"),
		ResponsesCustomToolsMode:                       strings.ToLower(strings.TrimSpace(v.GetString("responses.custom_tools.mode"))),
		ResponsesCodexEnableCompatibility:              v.GetBool("responses.codex.enable_compatibility"),
		ResponsesCodexForceToolChoiceRequired:          v.GetBool("responses.codex.force_tool_choice_required"),
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
	if err := parseDuration(v.GetString("shim.read_timeout"), &cfg.ReadTimeout); err != nil {
		return Config{}, fmt.Errorf("parse shim.read_timeout: %w", err)
	}
	if err := parseDuration(v.GetString("shim.write_timeout"), &cfg.WriteTimeout); err != nil {
		return Config{}, fmt.Errorf("parse shim.write_timeout: %w", err)
	}
	if err := parseDuration(v.GetString("shim.idle_timeout"), &cfg.IdleTimeout); err != nil {
		return Config{}, fmt.Errorf("parse shim.idle_timeout: %w", err)
	}
	if err := parseLogLevel(v.GetString("log.level"), &cfg.LogLevel); err != nil {
		return Config{}, fmt.Errorf("parse log.level: %w", err)
	}
	if err := parseShimAuthMode(cfg.ShimAuthMode); err != nil {
		return Config{}, fmt.Errorf("parse shim.auth.mode: %w", err)
	}
	normalizedRetrieval, err := retrieval.NormalizeConfig(retrieval.Config{
		IndexBackend: cfg.RetrievalIndexBackend,
		Embedder: retrieval.EmbedderConfig{
			Backend: cfg.RetrievalEmbedderBackend,
			BaseURL: cfg.RetrievalEmbedderBaseURL,
			Model:   cfg.RetrievalEmbedderModel,
		},
	})
	if err != nil {
		return Config{}, fmt.Errorf("parse retrieval config: %w", err)
	}
	cfg.RetrievalIndexBackend = normalizedRetrieval.IndexBackend
	cfg.RetrievalEmbedderBackend = normalizedRetrieval.Embedder.Backend
	cfg.RetrievalEmbedderBaseURL = normalizedRetrieval.Embedder.BaseURL
	cfg.RetrievalEmbedderModel = normalizedRetrieval.Embedder.Model
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
	if err := parseCustomToolsMode(cfg.ResponsesCustomToolsMode); err != nil {
		return Config{}, fmt.Errorf("parse responses.custom_tools.mode: %w", err)
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
	if err := parseDuration(v.GetString("responses.code_interpreter.execution_timeout"), &cfg.ResponsesCodeInterpreterTimeout); err != nil {
		return Config{}, fmt.Errorf("parse responses.code_interpreter.execution_timeout: %w", err)
	}
	if err := parseDuration(v.GetString("responses.web_search.timeout"), &cfg.ResponsesWebSearchTimeout); err != nil {
		return Config{}, fmt.Errorf("parse responses.web_search.timeout: %w", err)
	}
	if err := parseDuration(v.GetString("responses.image_generation.timeout"), &cfg.ResponsesImageGenerationTimeout); err != nil {
		return Config{}, fmt.Errorf("parse responses.image_generation.timeout: %w", err)
	}
	normalizedImageGeneration, err := imagegen.NormalizeConfig(imagegen.Config{
		Backend: cfg.ResponsesImageGenerationBackend,
		BaseURL: cfg.ResponsesImageGenerationBaseURL,
		Timeout: cfg.ResponsesImageGenerationTimeout,
	})
	if err != nil {
		return Config{}, fmt.Errorf("parse responses.image_generation config: %w", err)
	}
	cfg.ResponsesImageGenerationBackend = normalizedImageGeneration.Backend
	cfg.ResponsesImageGenerationBaseURL = normalizedImageGeneration.BaseURL
	cfg.ResponsesImageGenerationTimeout = normalizedImageGeneration.Timeout
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

func setDefaults(v *viper.Viper) {
	v.SetDefault("shim.addr", ":8080")
	v.SetDefault("shim.read_timeout", "15s")
	v.SetDefault("shim.write_timeout", "90s")
	v.SetDefault("shim.idle_timeout", "60s")
	v.SetDefault("shim.auth.mode", ShimAuthModeDisabled)
	v.SetDefault("shim.auth.bearer_tokens", []string{})
	v.SetDefault("shim.rate_limit.enabled", false)
	v.SetDefault("shim.rate_limit.requests_per_minute", "120")
	v.SetDefault("shim.rate_limit.burst", "60")
	v.SetDefault("shim.metrics.enabled", true)
	v.SetDefault("shim.metrics.path", "/metrics")
	v.SetDefault("shim.limits.json_body_bytes", "1MiB")
	v.SetDefault("shim.limits.retrieval_file_upload_bytes", "64MiB")
	v.SetDefault("shim.limits.chat_completions_shadow_store_bytes", "64MiB")
	v.SetDefault("shim.limits.responses_proxy_buffer_bytes", "64MiB")
	v.SetDefault("shim.limits.responses_stored_lineage_max_items", "128")
	v.SetDefault("shim.limits.custom_tool_grammar_definition_bytes", "16KiB")
	v.SetDefault("shim.limits.custom_tool_compiled_pattern_bytes", "32KiB")
	v.SetDefault("shim.limits.retrieval_max_concurrent_searches", "8")
	v.SetDefault("shim.limits.retrieval_max_search_queries", "4")
	v.SetDefault("shim.limits.retrieval_max_grounding_chunks", "20")
	v.SetDefault("shim.limits.code_interpreter_max_concurrent_runs", "2")
	v.SetDefault("sqlite.path", "./data/shim.db")
	v.SetDefault("sqlite.maintenance.cleanup_interval", "15m")
	v.SetDefault("llama.base_url", "http://127.0.0.1:8081")
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
	v.SetDefault("chat_completions.default_store_when_omitted", true)
	v.SetDefault("responses.mode", ResponsesModePreferLocal)
	v.SetDefault("responses.websocket.enabled", true)
	v.SetDefault("responses.custom_tools.mode", "auto")
	v.SetDefault("responses.codex.enable_compatibility", true)
	v.SetDefault("responses.codex.force_tool_choice_required", true)
	v.SetDefault("responses.web_search.backend", websearch.BackendDisabled)
	v.SetDefault("responses.web_search.base_url", "")
	v.SetDefault("responses.web_search.timeout", "10s")
	v.SetDefault("responses.web_search.max_results", "10")
	v.SetDefault("responses.image_generation.backend", imagegen.BackendDisabled)
	v.SetDefault("responses.image_generation.base_url", "")
	v.SetDefault("responses.image_generation.timeout", "60s")
	v.SetDefault("responses.compaction.backend", compactor.BackendHeuristic)
	v.SetDefault("responses.compaction.base_url", "")
	v.SetDefault("responses.compaction.model", "")
	v.SetDefault("responses.compaction.timeout", "10s")
	v.SetDefault("responses.compaction.max_output_tokens", "1200")
	v.SetDefault("responses.compaction.retained_items", "8")
	v.SetDefault("responses.compaction.max_input_chars", "60000")
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

func parseResponsesMode(value string) error {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", ResponsesModePreferLocal, ResponsesModePreferUpstream, ResponsesModeLocalOnly:
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
