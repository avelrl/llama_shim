package config_test

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"llama_shim/internal/config"
)

func disableDotEnv(t *testing.T) {
	t.Helper()
	t.Setenv("SHIM_DOTENV", filepath.Join(t.TempDir(), "missing.env"))
}

func TestLoadFromYAMLFile(t *testing.T) {
	disableDotEnv(t)
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	writeFile(t, configPath, `
shim:
  addr: ":9090"
  read_timeout: 5s
  write_timeout: 30s
  idle_timeout: 45s
  auth:
    mode: static_bearer
    bearer_tokens:
      - token-a
      - token-b
  rate_limit:
    enabled: true
    requests_per_minute: 240
    burst: 40
  metrics:
    enabled: true
    path: /metrics
  limits:
    json_body_bytes: 2MiB
    retrieval_file_upload_bytes: 32MiB
    chat_completions_shadow_store_timeout: 3s
    responses_proxy_buffer_bytes: 96MiB
    responses_stored_lineage_max_items: 32
    custom_tool_grammar_definition_bytes: 24KiB
    custom_tool_compiled_pattern_bytes: 40KiB
    retrieval_max_concurrent_searches: 6
    retrieval_max_search_queries: 3
    retrieval_max_grounding_chunks: 12
    code_interpreter_max_concurrent_runs: 4
sqlite:
  path: ./tmp/test.db
  maintenance:
    cleanup_interval: 17m
storage:
  backend: sqlite
llama:
  base_url: http://127.0.0.1:9091
  timeout: 12s
  max_concurrent_requests: 6
  max_queue_wait: 12s
  http:
    max_idle_conns: 48
    max_idle_conns_per_host: 24
    max_conns_per_host: 12
    idle_conn_timeout: 75s
    dial_timeout: 4s
    keep_alive: 21s
    tls_handshake_timeout: 8s
    expect_continue_timeout: 1500ms
log:
  level: debug
  file_path: ./tmp/shim.log
retrieval:
  index:
    backend: lexical
  embedder:
    backend: embedanything
    base_url: http://127.0.0.1:9099
    model: snowflake-arctic-embed-l-v2.0
chat_completions:
  default_store_when_omitted: false
  upstream_compatibility:
    models:
      - model: deepseek-*
        remap_developer_role: true
        default_thinking: disabled
        default_max_tokens: 32000
        json_schema_mode: json_object_instruction
        ensure_tool_parameter_property_types: true
        omit_empty_assistant_tool_content: true
responses:
  mode: prefer_upstream
  websocket:
    enabled: false
  web_search:
    backend: searxng
    base_url: http://127.0.0.1:8084
    timeout: 9s
    max_results: 7
  image_generation:
    backend: responses
    base_url: http://127.0.0.1:8188
    timeout: 95s
  compaction:
    backend: model_assisted_text
    base_url: http://127.0.0.1:8189
    model: local-compact
    timeout: 11s
    max_output_tokens: 900
    retained_items: 5
    max_input_chars: 45000
  computer:
    backend: chat_completions
  custom_tools:
    mode: bridge
  constrained_decoding:
    backend: vllm
  upstream_tool_compatibility:
    models:
      - model: Kimi-*
        disabled_tools:
          - image_generation
          - namespace_tool
          - computer
          - image_generation
  codex:
    enable_compatibility: true
    force_tool_choice_required: true
    force_tool_choice_required_disabled_models:
      - Kimi-*
      - qwen3-*
    upstream_input_compatibility:
      models:
        - model: Kimi-*
          mode: stringify
    model_metadata:
      models:
        - model: Kimi-K2.6
          display_name: Kimi K2.6
          context_window: 128000
          max_context_window: 256000
          auto_compact_token_limit: 100000
          effective_context_window_percent: 90
          default_reasoning_level: high
          supported_reasoning_levels: [low, medium, high]
          supports_reasoning_summaries: true
          default_reasoning_summary: none
          shell_type: shell_command
          apply_patch_tool_type: freeform
          web_search_tool_type: text_and_image
          supports_parallel_tool_calls: true
          support_verbosity: true
          default_verbosity: medium
          supports_image_detail_original: true
          supports_search_tool: true
          input_modalities: [text]
          visibility: list
          supported_in_api: true
          priority: 0
          additional_speed_tiers: [fast]
          experimental_supported_tools: [list_dir]
          availability_nux_message: Available through llama_shim.
          truncation_policy:
            mode: tokens
            limit: 12000
          base_instructions: Custom Codex instructions.
  code_interpreter:
    backend: docker
    python_binary: /opt/homebrew/bin/python3
    input_file_url_policy: allowlist
    input_file_url_allow_hosts:
      - files.example.com
      - "*.trusted.internal"
    docker:
      binary: /usr/local/bin/docker
      image: ghcr.io/acme/llama-shim-code-interpreter:latest
      memory_limit: 512m
      cpu_limit: "1.5"
      pids_limit: 96
    execution_timeout: 45s
    cleanup_interval: 2m
    limits:
      generated_files: 4
      generated_file_bytes: 1MiB
      generated_total_bytes: 3MiB
      remote_input_file_bytes: 12MiB
`)

	cfg, err := config.Load(configPath)
	require.NoError(t, err)
	require.Equal(t, ":9090", cfg.Addr)
	require.Equal(t, "sqlite", cfg.StorageBackend)
	require.Equal(t, "./tmp/test.db", cfg.SQLitePath)
	require.Equal(t, 17*time.Minute, cfg.SQLiteMaintenanceCleanupInterval)
	require.Equal(t, "http://127.0.0.1:9091", cfg.LlamaBaseURL)
	require.Equal(t, 12*time.Second, cfg.LlamaTimeout)
	require.Equal(t, 6, cfg.LlamaMaxConcurrentRequests)
	require.Equal(t, 12*time.Second, cfg.LlamaMaxQueueWait)
	require.Equal(t, 48, cfg.LlamaHTTPMaxIdleConns)
	require.Equal(t, 24, cfg.LlamaHTTPMaxIdleConnsPerHost)
	require.Equal(t, 12, cfg.LlamaHTTPMaxConnsPerHost)
	require.Equal(t, 75*time.Second, cfg.LlamaHTTPIdleConnTimeout)
	require.Equal(t, 4*time.Second, cfg.LlamaHTTPDialTimeout)
	require.Equal(t, 21*time.Second, cfg.LlamaHTTPKeepAlive)
	require.Equal(t, 8*time.Second, cfg.LlamaHTTPTLSHandshakeTimeout)
	require.Equal(t, 1500*time.Millisecond, cfg.LlamaHTTPExpectContinueTimeout)
	require.Equal(t, 5*time.Second, cfg.ReadTimeout)
	require.Equal(t, 30*time.Second, cfg.WriteTimeout)
	require.Equal(t, 45*time.Second, cfg.IdleTimeout)
	require.Equal(t, config.ShimAuthModeStaticBearer, cfg.ShimAuthMode)
	require.Equal(t, []string{"token-a", "token-b"}, cfg.ShimAuthBearerTokens)
	require.True(t, cfg.ShimRateLimitEnabled)
	require.Equal(t, 240, cfg.ShimRateLimitRequestsPerMinute)
	require.Equal(t, 40, cfg.ShimRateLimitBurst)
	require.True(t, cfg.ShimMetricsEnabled)
	require.Equal(t, "/metrics", cfg.ShimMetricsPath)
	require.EqualValues(t, 2<<20, cfg.ShimJSONBodyLimitBytes)
	require.EqualValues(t, 32<<20, cfg.RetrievalFileUploadMaxBytes)
	require.Equal(t, 3*time.Second, cfg.ChatCompletionsShadowStoreTimeout)
	require.EqualValues(t, 96<<20, cfg.ResponsesProxyBufferMaxBytes)
	require.Equal(t, 32, cfg.ResponsesStoredLineageMaxItems)
	require.EqualValues(t, 24<<10, cfg.CustomToolGrammarDefinitionMaxBytes)
	require.EqualValues(t, 40<<10, cfg.CustomToolCompiledPatternMaxBytes)
	require.Equal(t, 6, cfg.RetrievalMaxConcurrentSearches)
	require.Equal(t, 3, cfg.RetrievalMaxSearchQueries)
	require.Equal(t, 12, cfg.RetrievalMaxGroundingChunks)
	require.Equal(t, 4, cfg.ResponsesCodeInterpreterMaxConcurrentRuns)
	require.Equal(t, slog.LevelDebug, cfg.LogLevel)
	require.Equal(t, "./tmp/shim.log", cfg.LogFilePath)
	require.Equal(t, "lexical", cfg.RetrievalIndexBackend)
	require.Equal(t, "embedanything", cfg.RetrievalEmbedderBackend)
	require.Equal(t, "http://127.0.0.1:9099", cfg.RetrievalEmbedderBaseURL)
	require.Equal(t, "snowflake-arctic-embed-l-v2.0", cfg.RetrievalEmbedderModel)
	require.False(t, cfg.ChatCompletionsStoreWhenOmitted)
	require.Equal(t, []config.ChatCompletionsUpstreamCompatibilityRule{
		{
			Model:                            "deepseek-*",
			RemapDeveloperRole:               true,
			DefaultThinking:                  "disabled",
			DefaultMaxTokens:                 32000,
			JSONSchemaMode:                   "json_object_instruction",
			EnsureToolParameterPropertyTypes: true,
			OmitEmptyAssistantToolContent:    true,
		},
	}, cfg.ChatCompletionsUpstreamCompatibility)
	require.Equal(t, config.ResponsesModePreferUpstream, cfg.ResponsesMode)
	require.False(t, cfg.ResponsesWebSocketEnabled)
	require.Equal(t, "searxng", cfg.ResponsesWebSearchBackend)
	require.Equal(t, "http://127.0.0.1:8084", cfg.ResponsesWebSearchBaseURL)
	require.Equal(t, 9*time.Second, cfg.ResponsesWebSearchTimeout)
	require.Equal(t, 7, cfg.ResponsesWebSearchMaxResults)
	require.Equal(t, "responses", cfg.ResponsesImageGenerationBackend)
	require.Equal(t, "http://127.0.0.1:8188", cfg.ResponsesImageGenerationBaseURL)
	require.Equal(t, 95*time.Second, cfg.ResponsesImageGenerationTimeout)
	require.Equal(t, "model_assisted_text", cfg.ResponsesCompactionBackend)
	require.Equal(t, "http://127.0.0.1:8189", cfg.ResponsesCompactionBaseURL)
	require.Equal(t, "local-compact", cfg.ResponsesCompactionModel)
	require.Equal(t, 11*time.Second, cfg.ResponsesCompactionTimeout)
	require.Equal(t, 900, cfg.ResponsesCompactionMaxOutputTokens)
	require.Equal(t, 5, cfg.ResponsesCompactionRetainedItems)
	require.Equal(t, 45000, cfg.ResponsesCompactionMaxInputRunes)
	require.Equal(t, config.ResponsesComputerBackendChatCompletions, cfg.ResponsesComputerBackend)
	require.Equal(t, "bridge", cfg.ResponsesCustomToolsMode)
	require.Equal(t, config.ResponsesConstrainedDecodingBackendVLLM, cfg.ResponsesConstrainedDecodingBackend)
	require.Equal(t, []config.ResponsesUpstreamToolCompatibilityRule{
		{Model: "Kimi-*", DisabledTools: []string{"image_generation", "namespace_tool", "computer"}},
	}, cfg.ResponsesUpstreamToolCompatibility)
	require.True(t, cfg.ResponsesCodexEnableCompatibility)
	require.True(t, cfg.ResponsesCodexForceToolChoiceRequired)
	require.Equal(t, []string{"Kimi-*", "qwen3-*"}, cfg.ResponsesCodexForceToolChoiceRequiredDisabledModels)
	require.Equal(t, []config.ResponsesCodexUpstreamInputCompatibilityRule{
		{Model: "Kimi-*", Mode: "stringify"},
	}, cfg.ResponsesCodexUpstreamInputCompatibility)
	require.Len(t, cfg.ResponsesCodexModelMetadata, 1)
	codexMetadata := cfg.ResponsesCodexModelMetadata[0]
	require.Equal(t, "Kimi-K2.6", codexMetadata.Model)
	require.Equal(t, "Kimi K2.6", codexMetadata.DisplayName)
	require.Equal(t, "OpenAI-compatible upstream routed through llama_shim.", codexMetadata.Description)
	require.EqualValues(t, 128000, codexMetadata.ContextWindow)
	require.EqualValues(t, 256000, codexMetadata.MaxContextWindow)
	require.EqualValues(t, 100000, codexMetadata.AutoCompactTokenLimit)
	require.EqualValues(t, 90, codexMetadata.EffectiveContextWindowPercent)
	require.Equal(t, "high", codexMetadata.DefaultReasoningLevel)
	require.Equal(t, []string{"low", "medium", "high"}, codexMetadata.SupportedReasoningLevels)
	require.True(t, codexMetadata.SupportsReasoningSummaries)
	require.Equal(t, "none", codexMetadata.DefaultReasoningSummary)
	require.Equal(t, "shell_command", codexMetadata.ShellType)
	require.Equal(t, "freeform", codexMetadata.ApplyPatchToolType)
	require.Equal(t, "text_and_image", codexMetadata.WebSearchToolType)
	require.True(t, codexMetadata.SupportsParallelToolCalls)
	require.True(t, codexMetadata.SupportVerbosity)
	require.Equal(t, "medium", codexMetadata.DefaultVerbosity)
	require.True(t, codexMetadata.SupportsImageDetailOriginal)
	require.True(t, codexMetadata.SupportsSearchTool)
	require.Equal(t, []string{"text"}, codexMetadata.InputModalities)
	require.Equal(t, "list", codexMetadata.Visibility)
	require.NotNil(t, codexMetadata.SupportedInAPI)
	require.True(t, *codexMetadata.SupportedInAPI)
	require.NotNil(t, codexMetadata.Priority)
	require.Equal(t, 0, *codexMetadata.Priority)
	require.Equal(t, []string{"fast"}, codexMetadata.AdditionalSpeedTiers)
	require.Equal(t, []string{"list_dir"}, codexMetadata.ExperimentalSupportedTools)
	require.Equal(t, "Available through llama_shim.", codexMetadata.AvailabilityNuxMessage)
	require.Equal(t, config.ResponsesCodexTruncationPolicy{Mode: "tokens", Limit: 12000}, codexMetadata.TruncationPolicy)
	require.Equal(t, "Custom Codex instructions.", codexMetadata.BaseInstructions)
	require.Equal(t, config.ResponsesCodeInterpreterBackendDocker, cfg.ResponsesCodeInterpreterBackend)
	require.Equal(t, "/opt/homebrew/bin/python3", cfg.ResponsesCodeInterpreterPythonBinary)
	require.Equal(t, "/usr/local/bin/docker", cfg.ResponsesCodeInterpreterDockerBinary)
	require.Equal(t, "ghcr.io/acme/llama-shim-code-interpreter:latest", cfg.ResponsesCodeInterpreterDockerImage)
	require.Equal(t, "512m", cfg.ResponsesCodeInterpreterDockerMemory)
	require.Equal(t, "1.5", cfg.ResponsesCodeInterpreterDockerCPU)
	require.Equal(t, 96, cfg.ResponsesCodeInterpreterDockerPids)
	require.Equal(t, 45*time.Second, cfg.ResponsesCodeInterpreterTimeout)
	require.Equal(t, config.ResponsesCodeInterpreterInputFileURLPolicyAllowlist, cfg.ResponsesCodeInterpreterInputFileURLPolicy)
	require.Equal(t, []string{"files.example.com", "*.trusted.internal"}, cfg.ResponsesCodeInterpreterInputFileURLAllowHosts)
	require.Equal(t, 2*time.Minute, cfg.ResponsesCodeInterpreterCleanupInterval)
	require.Equal(t, 4, cfg.ResponsesCodeInterpreterGeneratedFiles)
	require.EqualValues(t, 1<<20, cfg.ResponsesCodeInterpreterGeneratedFileBytes)
	require.EqualValues(t, 3<<20, cfg.ResponsesCodeInterpreterGeneratedTotalBytes)
	require.EqualValues(t, 12<<20, cfg.ResponsesCodeInterpreterRemoteInputFileBytes)
	require.Equal(t, configPath, cfg.ConfigFile)
}

func TestEnvOverridesYAMLFile(t *testing.T) {
	disableDotEnv(t)
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	writeFile(t, configPath, `
shim:
  addr: ":9090"
llama:
  timeout: 12s
log:
  level: info
responses:
  mode: prefer_upstream
  codex:
    enable_compatibility: false
    force_tool_choice_required: false
  code_interpreter:
    backend: disabled
    python_binary: python3
    docker:
      binary: docker
      image: python:3.12-slim
      memory_limit: 256m
      cpu_limit: "0.5"
      pids_limit: 64
    execution_timeout: 20s
`)

	t.Setenv("SHIM_ADDR", ":7070")
	t.Setenv("SHIM_AUTH_MODE", "static_bearer")
	t.Setenv("SHIM_AUTH_BEARER_TOKENS", "token-1,token-2")
	t.Setenv("SHIM_RATE_LIMIT_ENABLED", "true")
	t.Setenv("SHIM_RATE_LIMIT_REQUESTS_PER_MINUTE", "180")
	t.Setenv("SHIM_RATE_LIMIT_BURST", "30")
	t.Setenv("SHIM_METRICS_ENABLED", "false")
	t.Setenv("SHIM_METRICS_PATH", "/internal/metrics")
	t.Setenv("STORAGE_BACKEND", "sqlite")
	t.Setenv("SHIM_LIMITS_JSON_BODY_BYTES", "3MiB")
	t.Setenv("SHIM_LIMITS_RETRIEVAL_FILE_UPLOAD_BYTES", "48MiB")
	t.Setenv("SHIM_LIMITS_CHAT_COMPLETIONS_SHADOW_STORE_TIMEOUT", "4s")
	t.Setenv("SHIM_LIMITS_RESPONSES_PROXY_BUFFER_BYTES", "80MiB")
	t.Setenv("SHIM_LIMITS_RESPONSES_STORED_LINEAGE_MAX_ITEMS", "24")
	t.Setenv("SHIM_LIMITS_CUSTOM_TOOL_GRAMMAR_DEFINITION_BYTES", "20KiB")
	t.Setenv("SHIM_LIMITS_CUSTOM_TOOL_COMPILED_PATTERN_BYTES", "36KiB")
	t.Setenv("SHIM_LIMITS_RETRIEVAL_MAX_CONCURRENT_SEARCHES", "9")
	t.Setenv("SHIM_LIMITS_RETRIEVAL_MAX_SEARCH_QUERIES", "5")
	t.Setenv("SHIM_LIMITS_RETRIEVAL_MAX_GROUNDING_CHUNKS", "11")
	t.Setenv("SHIM_LIMITS_CODE_INTERPRETER_MAX_CONCURRENT_RUNS", "7")
	t.Setenv("SQLITE_MAINTENANCE_CLEANUP_INTERVAL", "21m")
	t.Setenv("LLAMA_TIMEOUT", "25s")
	t.Setenv("LLAMA_MAX_CONCURRENT_REQUESTS", "7")
	t.Setenv("LLAMA_MAX_QUEUE_WAIT", "14s")
	t.Setenv("LLAMA_HTTP_MAX_IDLE_CONNS", "40")
	t.Setenv("LLAMA_HTTP_MAX_IDLE_CONNS_PER_HOST", "20")
	t.Setenv("LLAMA_HTTP_MAX_CONNS_PER_HOST", "10")
	t.Setenv("LLAMA_HTTP_IDLE_CONN_TIMEOUT", "80s")
	t.Setenv("LLAMA_HTTP_DIAL_TIMEOUT", "5s")
	t.Setenv("LLAMA_HTTP_KEEP_ALIVE", "18s")
	t.Setenv("LLAMA_HTTP_TLS_HANDSHAKE_TIMEOUT", "9s")
	t.Setenv("LLAMA_HTTP_EXPECT_CONTINUE_TIMEOUT", "1200ms")
	t.Setenv("LOG_LEVEL", "warn")
	t.Setenv("LOG_FILE_PATH", "./override.log")
	t.Setenv("RETRIEVAL_INDEX_BACKEND", "lexical")
	t.Setenv("RETRIEVAL_EMBEDDER_BACKEND", "openai_compatible")
	t.Setenv("RETRIEVAL_EMBEDDER_BASE_URL", "http://127.0.0.1:8082")
	t.Setenv("RETRIEVAL_EMBEDDER_MODEL", "text-embedding-3-small")
	t.Setenv("CHAT_COMPLETIONS_DEFAULT_STORE_WHEN_OMITTED", "true")
	t.Setenv("RESPONSES_MODE", "local_only")
	t.Setenv("RESPONSES_CONSTRAINED_DECODING_BACKEND", "vllm")
	t.Setenv("RESPONSES_WEB_SEARCH_BACKEND", "searxng")
	t.Setenv("RESPONSES_WEB_SEARCH_BASE_URL", "http://127.0.0.1:8181")
	t.Setenv("RESPONSES_WEB_SEARCH_TIMEOUT", "8s")
	t.Setenv("RESPONSES_WEB_SEARCH_MAX_RESULTS", "6")
	t.Setenv("RESPONSES_IMAGE_GENERATION_BACKEND", "responses")
	t.Setenv("RESPONSES_IMAGE_GENERATION_BASE_URL", "http://127.0.0.1:8282")
	t.Setenv("RESPONSES_IMAGE_GENERATION_TIMEOUT", "70s")
	t.Setenv("RESPONSES_COMPACTION_BACKEND", "model_assisted_text")
	t.Setenv("RESPONSES_COMPACTION_BASE_URL", "http://127.0.0.1:8283")
	t.Setenv("RESPONSES_COMPACTION_MODEL", "env-compact")
	t.Setenv("RESPONSES_COMPACTION_TIMEOUT", "12s")
	t.Setenv("RESPONSES_COMPACTION_MAX_OUTPUT_TOKENS", "777")
	t.Setenv("RESPONSES_COMPACTION_RETAINED_ITEMS", "6")
	t.Setenv("RESPONSES_COMPACTION_MAX_INPUT_CHARS", "50000")
	t.Setenv("RESPONSES_COMPUTER_BACKEND", "chat_completions")
	t.Setenv("RESPONSES_CODEX_ENABLE_COMPATIBILITY", "true")
	t.Setenv("RESPONSES_CODEX_FORCE_TOOL_CHOICE_REQUIRED", "true")
	t.Setenv("RESPONSES_CODEX_FORCE_TOOL_CHOICE_REQUIRED_DISABLED_MODELS", "Kimi-*,qwen3-*")
	t.Setenv("RESPONSES_CODE_INTERPRETER_BACKEND", "docker")
	t.Setenv("RESPONSES_CODE_INTERPRETER_PYTHON_BINARY", "/usr/bin/python3")
	t.Setenv("RESPONSES_CODE_INTERPRETER_DOCKER_BINARY", "/usr/bin/docker")
	t.Setenv("RESPONSES_CODE_INTERPRETER_DOCKER_IMAGE", "python:3.12-alpine")
	t.Setenv("RESPONSES_CODE_INTERPRETER_DOCKER_MEMORY_LIMIT", "768m")
	t.Setenv("RESPONSES_CODE_INTERPRETER_DOCKER_CPU_LIMIT", "2")
	t.Setenv("RESPONSES_CODE_INTERPRETER_DOCKER_PIDS_LIMIT", "128")
	t.Setenv("RESPONSES_CODE_INTERPRETER_EXECUTION_TIMEOUT", "33s")
	t.Setenv("RESPONSES_CODE_INTERPRETER_INPUT_FILE_URL_POLICY", "unsafe_allow_http_https")
	t.Setenv("RESPONSES_CODE_INTERPRETER_INPUT_FILE_URL_ALLOW_HOSTS", "files.example.com,*.trusted.internal")
	t.Setenv("RESPONSES_CODE_INTERPRETER_CLEANUP_INTERVAL", "90s")
	t.Setenv("RESPONSES_CODE_INTERPRETER_LIMITS_GENERATED_FILES", "5")
	t.Setenv("RESPONSES_CODE_INTERPRETER_LIMITS_GENERATED_FILE_BYTES", "4MiB")
	t.Setenv("RESPONSES_CODE_INTERPRETER_LIMITS_GENERATED_TOTAL_BYTES", "10MiB")
	t.Setenv("RESPONSES_CODE_INTERPRETER_LIMITS_REMOTE_INPUT_FILE_BYTES", "25MiB")

	cfg, err := config.Load(configPath)
	require.NoError(t, err)
	require.Equal(t, ":7070", cfg.Addr)
	require.Equal(t, "sqlite", cfg.StorageBackend)
	require.Equal(t, 21*time.Minute, cfg.SQLiteMaintenanceCleanupInterval)
	require.Equal(t, config.ShimAuthModeStaticBearer, cfg.ShimAuthMode)
	require.Equal(t, []string{"token-1", "token-2"}, cfg.ShimAuthBearerTokens)
	require.True(t, cfg.ShimRateLimitEnabled)
	require.Equal(t, 180, cfg.ShimRateLimitRequestsPerMinute)
	require.Equal(t, 30, cfg.ShimRateLimitBurst)
	require.False(t, cfg.ShimMetricsEnabled)
	require.Equal(t, "/internal/metrics", cfg.ShimMetricsPath)
	require.EqualValues(t, 3<<20, cfg.ShimJSONBodyLimitBytes)
	require.EqualValues(t, 48<<20, cfg.RetrievalFileUploadMaxBytes)
	require.Equal(t, 4*time.Second, cfg.ChatCompletionsShadowStoreTimeout)
	require.EqualValues(t, 80<<20, cfg.ResponsesProxyBufferMaxBytes)
	require.Equal(t, 24, cfg.ResponsesStoredLineageMaxItems)
	require.EqualValues(t, 20<<10, cfg.CustomToolGrammarDefinitionMaxBytes)
	require.EqualValues(t, 36<<10, cfg.CustomToolCompiledPatternMaxBytes)
	require.Equal(t, 9, cfg.RetrievalMaxConcurrentSearches)
	require.Equal(t, 5, cfg.RetrievalMaxSearchQueries)
	require.Equal(t, 11, cfg.RetrievalMaxGroundingChunks)
	require.Equal(t, 7, cfg.ResponsesCodeInterpreterMaxConcurrentRuns)
	require.Equal(t, 25*time.Second, cfg.LlamaTimeout)
	require.Equal(t, 7, cfg.LlamaMaxConcurrentRequests)
	require.Equal(t, 14*time.Second, cfg.LlamaMaxQueueWait)
	require.Equal(t, 40, cfg.LlamaHTTPMaxIdleConns)
	require.Equal(t, 20, cfg.LlamaHTTPMaxIdleConnsPerHost)
	require.Equal(t, 10, cfg.LlamaHTTPMaxConnsPerHost)
	require.Equal(t, 80*time.Second, cfg.LlamaHTTPIdleConnTimeout)
	require.Equal(t, 5*time.Second, cfg.LlamaHTTPDialTimeout)
	require.Equal(t, 18*time.Second, cfg.LlamaHTTPKeepAlive)
	require.Equal(t, 9*time.Second, cfg.LlamaHTTPTLSHandshakeTimeout)
	require.Equal(t, 1200*time.Millisecond, cfg.LlamaHTTPExpectContinueTimeout)
	require.Equal(t, slog.LevelWarn, cfg.LogLevel)
	require.Equal(t, "./override.log", cfg.LogFilePath)
	require.Equal(t, "lexical", cfg.RetrievalIndexBackend)
	require.Equal(t, "openai_compatible", cfg.RetrievalEmbedderBackend)
	require.Equal(t, "http://127.0.0.1:8082", cfg.RetrievalEmbedderBaseURL)
	require.Equal(t, "text-embedding-3-small", cfg.RetrievalEmbedderModel)
	require.True(t, cfg.ChatCompletionsStoreWhenOmitted)
	require.Empty(t, cfg.ChatCompletionsUpstreamCompatibility)
	require.Equal(t, config.ResponsesModeLocalOnly, cfg.ResponsesMode)
	require.Equal(t, config.ResponsesConstrainedDecodingBackendVLLM, cfg.ResponsesConstrainedDecodingBackend)
	require.Equal(t, "searxng", cfg.ResponsesWebSearchBackend)
	require.Equal(t, "http://127.0.0.1:8181", cfg.ResponsesWebSearchBaseURL)
	require.Equal(t, 8*time.Second, cfg.ResponsesWebSearchTimeout)
	require.Equal(t, 6, cfg.ResponsesWebSearchMaxResults)
	require.Equal(t, "responses", cfg.ResponsesImageGenerationBackend)
	require.Equal(t, "http://127.0.0.1:8282", cfg.ResponsesImageGenerationBaseURL)
	require.Equal(t, 70*time.Second, cfg.ResponsesImageGenerationTimeout)
	require.Equal(t, "model_assisted_text", cfg.ResponsesCompactionBackend)
	require.Equal(t, "http://127.0.0.1:8283", cfg.ResponsesCompactionBaseURL)
	require.Equal(t, "env-compact", cfg.ResponsesCompactionModel)
	require.Equal(t, 12*time.Second, cfg.ResponsesCompactionTimeout)
	require.Equal(t, 777, cfg.ResponsesCompactionMaxOutputTokens)
	require.Equal(t, 6, cfg.ResponsesCompactionRetainedItems)
	require.Equal(t, 50000, cfg.ResponsesCompactionMaxInputRunes)
	require.Equal(t, config.ResponsesComputerBackendChatCompletions, cfg.ResponsesComputerBackend)
	require.True(t, cfg.ResponsesCodexEnableCompatibility)
	require.True(t, cfg.ResponsesCodexForceToolChoiceRequired)
	require.Equal(t, []string{"Kimi-*", "qwen3-*"}, cfg.ResponsesCodexForceToolChoiceRequiredDisabledModels)
	require.Equal(t, config.ResponsesCodeInterpreterBackendDocker, cfg.ResponsesCodeInterpreterBackend)
	require.Equal(t, "/usr/bin/python3", cfg.ResponsesCodeInterpreterPythonBinary)
	require.Equal(t, "/usr/bin/docker", cfg.ResponsesCodeInterpreterDockerBinary)
	require.Equal(t, "python:3.12-alpine", cfg.ResponsesCodeInterpreterDockerImage)
	require.Equal(t, "768m", cfg.ResponsesCodeInterpreterDockerMemory)
	require.Equal(t, "2", cfg.ResponsesCodeInterpreterDockerCPU)
	require.Equal(t, 128, cfg.ResponsesCodeInterpreterDockerPids)
	require.Equal(t, 33*time.Second, cfg.ResponsesCodeInterpreterTimeout)
	require.Equal(t, config.ResponsesCodeInterpreterInputFileURLPolicyUnsafeAllowHTTPHTTPS, cfg.ResponsesCodeInterpreterInputFileURLPolicy)
	require.Equal(t, []string{"files.example.com", "*.trusted.internal"}, cfg.ResponsesCodeInterpreterInputFileURLAllowHosts)
	require.Equal(t, 90*time.Second, cfg.ResponsesCodeInterpreterCleanupInterval)
	require.Equal(t, 5, cfg.ResponsesCodeInterpreterGeneratedFiles)
	require.EqualValues(t, 4<<20, cfg.ResponsesCodeInterpreterGeneratedFileBytes)
	require.EqualValues(t, 10<<20, cfg.ResponsesCodeInterpreterGeneratedTotalBytes)
	require.EqualValues(t, 25<<20, cfg.ResponsesCodeInterpreterRemoteInputFileBytes)
}

func TestLoadUsesCodexSafeDefaults(t *testing.T) {
	disableDotEnv(t)
	tempDir := t.TempDir()
	previousWD, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(tempDir))
	t.Cleanup(func() {
		_ = os.Chdir(previousWD)
	})

	cfg, err := config.Load("")
	require.NoError(t, err)
	require.Equal(t, config.ShimAuthModeDisabled, cfg.ShimAuthMode)
	require.Empty(t, cfg.ShimAuthBearerTokens)
	require.False(t, cfg.ShimRateLimitEnabled)
	require.Equal(t, 120, cfg.ShimRateLimitRequestsPerMinute)
	require.Equal(t, 60, cfg.ShimRateLimitBurst)
	require.True(t, cfg.ShimMetricsEnabled)
	require.Equal(t, "/metrics", cfg.ShimMetricsPath)
	require.EqualValues(t, 1<<20, cfg.ShimJSONBodyLimitBytes)
	require.EqualValues(t, 64<<20, cfg.RetrievalFileUploadMaxBytes)
	require.EqualValues(t, 64<<20, cfg.ResponsesProxyBufferMaxBytes)
	require.Equal(t, 128, cfg.ResponsesStoredLineageMaxItems)
	require.EqualValues(t, 16<<10, cfg.CustomToolGrammarDefinitionMaxBytes)
	require.EqualValues(t, 32<<10, cfg.CustomToolCompiledPatternMaxBytes)
	require.Equal(t, 8, cfg.RetrievalMaxConcurrentSearches)
	require.Equal(t, 4, cfg.RetrievalMaxSearchQueries)
	require.Equal(t, 20, cfg.RetrievalMaxGroundingChunks)
	require.Equal(t, 2, cfg.ResponsesCodeInterpreterMaxConcurrentRuns)
	require.Equal(t, 15*time.Minute, cfg.SQLiteMaintenanceCleanupInterval)
	require.Equal(t, "lexical", cfg.RetrievalIndexBackend)
	require.Equal(t, "disabled", cfg.RetrievalEmbedderBackend)
	require.Empty(t, cfg.RetrievalEmbedderBaseURL)
	require.Empty(t, cfg.RetrievalEmbedderModel)
	require.True(t, cfg.ChatCompletionsStoreWhenOmitted)
	require.Equal(t, config.ResponsesModePreferLocal, cfg.ResponsesMode)
	require.Equal(t, "disabled", cfg.ResponsesImageGenerationBackend)
	require.Empty(t, cfg.ResponsesImageGenerationBaseURL)
	require.Equal(t, 0*time.Second, cfg.ResponsesImageGenerationTimeout)
	require.Equal(t, "heuristic", cfg.ResponsesCompactionBackend)
	require.Empty(t, cfg.ResponsesCompactionBaseURL)
	require.Empty(t, cfg.ResponsesCompactionModel)
	require.Equal(t, 0*time.Second, cfg.ResponsesCompactionTimeout)
	require.Equal(t, 0, cfg.ResponsesCompactionMaxOutputTokens)
	require.Equal(t, 0, cfg.ResponsesCompactionRetainedItems)
	require.Equal(t, 0, cfg.ResponsesCompactionMaxInputRunes)
	require.Equal(t, config.ResponsesComputerBackendDisabled, cfg.ResponsesComputerBackend)
	require.Equal(t, "auto", cfg.ResponsesCustomToolsMode)
	require.Equal(t, config.ResponsesConstrainedDecodingBackendShimValidateRepair, cfg.ResponsesConstrainedDecodingBackend)
	require.True(t, cfg.ResponsesCodexEnableCompatibility)
	require.True(t, cfg.ResponsesCodexForceToolChoiceRequired)
	require.Empty(t, cfg.ResponsesCodexUpstreamInputCompatibility)
	require.Equal(t, config.ResponsesCodeInterpreterBackendDisabled, cfg.ResponsesCodeInterpreterBackend)
	require.Equal(t, "python3", cfg.ResponsesCodeInterpreterPythonBinary)
	require.Equal(t, "docker", cfg.ResponsesCodeInterpreterDockerBinary)
	require.Equal(t, "python:3.12-slim", cfg.ResponsesCodeInterpreterDockerImage)
	require.Equal(t, "1g", cfg.ResponsesCodeInterpreterDockerMemory)
	require.Equal(t, "0.5", cfg.ResponsesCodeInterpreterDockerCPU)
	require.Equal(t, 64, cfg.ResponsesCodeInterpreterDockerPids)
	require.Equal(t, 20*time.Second, cfg.ResponsesCodeInterpreterTimeout)
	require.Equal(t, config.ResponsesCodeInterpreterInputFileURLPolicyDisabled, cfg.ResponsesCodeInterpreterInputFileURLPolicy)
	require.Empty(t, cfg.ResponsesCodeInterpreterInputFileURLAllowHosts)
	require.Equal(t, time.Minute, cfg.ResponsesCodeInterpreterCleanupInterval)
	require.Equal(t, 8, cfg.ResponsesCodeInterpreterGeneratedFiles)
	require.EqualValues(t, 2<<20, cfg.ResponsesCodeInterpreterGeneratedFileBytes)
	require.EqualValues(t, 8<<20, cfg.ResponsesCodeInterpreterGeneratedTotalBytes)
	require.EqualValues(t, 50<<20, cfg.ResponsesCodeInterpreterRemoteInputFileBytes)
	require.Equal(t, 4, cfg.LlamaMaxConcurrentRequests)
	require.Equal(t, time.Duration(0), cfg.LlamaMaxQueueWait)
	require.Equal(t, 32, cfg.LlamaHTTPMaxIdleConns)
	require.Equal(t, 16, cfg.LlamaHTTPMaxIdleConnsPerHost)
	require.Equal(t, 8, cfg.LlamaHTTPMaxConnsPerHost)
	require.Equal(t, 90*time.Second, cfg.LlamaHTTPIdleConnTimeout)
	require.Equal(t, 10*time.Second, cfg.LlamaHTTPDialTimeout)
	require.Equal(t, 30*time.Second, cfg.LlamaHTTPKeepAlive)
	require.Equal(t, 10*time.Second, cfg.LlamaHTTPTLSHandshakeTimeout)
	require.Equal(t, time.Second, cfg.LlamaHTTPExpectContinueTimeout)
	require.Equal(t, config.StorageBackendSQLite, cfg.StorageBackend)
}

func TestLoadRejectsLegacyUnsafeHostAlias(t *testing.T) {
	disableDotEnv(t)
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	writeFile(t, configPath, `
responses:
  code_interpreter:
    enable_unsafe_host_executor: true
`)

	_, err := config.Load(configPath)
	require.Error(t, err)
	require.ErrorContains(t, err, "parse responses.code_interpreter.enable_unsafe_host_executor")
}

func TestLoadReadsDotEnvWhenEnvUnset(t *testing.T) {
	tempDir := t.TempDir()
	previousWD, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(tempDir))
	t.Cleanup(func() {
		_ = os.Chdir(previousWD)
	})

	writeFile(t, filepath.Join(tempDir, ".env"), `
SHIM_ADDR=:9191
`)
	t.Setenv("SHIM_DOTENV", filepath.Join(tempDir, ".env"))

	cfg, err := config.Load("")
	require.NoError(t, err)
	require.Equal(t, ":9191", cfg.Addr)
}

func TestLoadRejectsUnsafeHostCodeInterpreterBackend(t *testing.T) {
	disableDotEnv(t)
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	writeFile(t, configPath, `
responses:
  code_interpreter:
    backend: unsafe_host
`)

	_, err := config.Load(configPath)
	require.Error(t, err)
	require.ErrorContains(t, err, "parse responses.code_interpreter.backend")
}

func TestLoadRejectsUnsupportedRetrievalBackend(t *testing.T) {
	disableDotEnv(t)
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	writeFile(t, configPath, `
retrieval:
  index:
    backend: bogus
`)

	_, err := config.Load(configPath)
	require.Error(t, err)
	require.ErrorContains(t, err, `unsupported retrieval index backend "bogus"`)
}

func TestLoadRejectsUnsupportedStorageBackend(t *testing.T) {
	disableDotEnv(t)
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	writeFile(t, configPath, `
storage:
  backend: postgres
`)

	_, err := config.Load(configPath)
	require.Error(t, err)
	require.ErrorContains(t, err, `unsupported storage backend "postgres"`)
}

func TestLoadRejectsImageGenerationResponsesBackendWithoutBaseURL(t *testing.T) {
	disableDotEnv(t)
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	writeFile(t, configPath, `
responses:
  image_generation:
    backend: responses
`)

	_, err := config.Load(configPath)
	require.Error(t, err)
	require.ErrorContains(t, err, "responses.image_generation.base_url must not be empty")
}

func TestLoadDefaultsCompactionBaseURLToLlamaBaseURL(t *testing.T) {
	disableDotEnv(t)
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	writeFile(t, configPath, `
llama:
  base_url: http://127.0.0.1:9091
responses:
  compaction:
    backend: model_assisted_text
    model: local-compact
`)

	cfg, err := config.Load(configPath)
	require.NoError(t, err)
	require.Equal(t, "model_assisted_text", cfg.ResponsesCompactionBackend)
	require.Equal(t, "http://127.0.0.1:9091", cfg.ResponsesCompactionBaseURL)
	require.Equal(t, "local-compact", cfg.ResponsesCompactionModel)
	require.Equal(t, 10*time.Second, cfg.ResponsesCompactionTimeout)
	require.Equal(t, 1200, cfg.ResponsesCompactionMaxOutputTokens)
	require.Equal(t, 8, cfg.ResponsesCompactionRetainedItems)
	require.Equal(t, 60000, cfg.ResponsesCompactionMaxInputRunes)
}

func TestLoadRejectsCompactionModelAssistedWithoutModel(t *testing.T) {
	disableDotEnv(t)
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	writeFile(t, configPath, `
responses:
  compaction:
    backend: model_assisted_text
`)

	_, err := config.Load(configPath)
	require.Error(t, err)
	require.ErrorContains(t, err, "responses.compaction.model must not be empty")
}

func TestLoadRejectsUnsupportedComputerBackend(t *testing.T) {
	disableDotEnv(t)
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	writeFile(t, configPath, `
responses:
  computer:
    backend: bogus
`)

	_, err := config.Load(configPath)
	require.Error(t, err)
	require.ErrorContains(t, err, "parse responses.computer.backend")
}
