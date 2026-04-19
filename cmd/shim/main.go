package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"

	"llama_shim/internal/config"
	"llama_shim/internal/httpapi"
	"llama_shim/internal/imagegen"
	"llama_shim/internal/llama"
	"llama_shim/internal/retrieval"
	"llama_shim/internal/sandbox"
	"llama_shim/internal/service"
	"llama_shim/internal/storage/sqlite"
	"llama_shim/internal/websearch"
)

func main() {
	configPath := flag.String("config", "", "path to YAML config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}

	logWriter, logFile, err := buildLogWriter(cfg.LogFilePath)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "open log file: %v\n", err)
		os.Exit(1)
	}
	if logFile != nil {
		defer logFile.Close()
	}

	logger := slog.New(slog.NewJSONHandler(logWriter, &slog.HandlerOptions{
		Level: cfg.LogLevel,
	}))
	processCtx, processCancel := context.WithCancel(context.Background())
	defer processCancel()

	retrievalEmbedder, err := retrieval.NewEmbedder(retrieval.EmbedderConfig{
		Backend: cfg.RetrievalEmbedderBackend,
		BaseURL: cfg.RetrievalEmbedderBaseURL,
		Model:   cfg.RetrievalEmbedderModel,
	})
	if err != nil {
		logger.Error("build retrieval embedder", "err", err)
		os.Exit(1)
	}

	store, err := sqlite.OpenWithOptions(processCtx, cfg.SQLitePath, sqlite.OpenOptions{
		Retrieval: retrieval.Config{
			IndexBackend: cfg.RetrievalIndexBackend,
			Embedder: retrieval.EmbedderConfig{
				Backend: cfg.RetrievalEmbedderBackend,
				BaseURL: cfg.RetrievalEmbedderBaseURL,
				Model:   cfg.RetrievalEmbedderModel,
			},
		},
		Embedder: retrievalEmbedder,
	})
	if err != nil {
		logger.Error("open sqlite", "err", err)
		os.Exit(1)
	}
	defer store.Close()

	llamaClient := llama.NewClient(cfg.LlamaBaseURL, cfg.LlamaTimeout)
	responseService := service.NewResponseService(store, store, llamaClient)
	conversationService := service.NewConversationService(store)
	metrics := httpapi.NewMetrics()
	localComputer, err := buildLocalComputerRuntimeConfig(cfg)
	if err != nil {
		logger.Error("build computer runtime", "err", err)
		os.Exit(1)
	}
	localCodeInterpreter, err := buildLocalCodeInterpreterRuntimeConfig(cfg)
	if err != nil {
		logger.Error("build code interpreter runtime", "err", err)
		os.Exit(1)
	}
	webSearchProvider, err := websearch.NewProvider(websearch.Config{
		Backend:    cfg.ResponsesWebSearchBackend,
		BaseURL:    cfg.ResponsesWebSearchBaseURL,
		Timeout:    cfg.ResponsesWebSearchTimeout,
		MaxResults: cfg.ResponsesWebSearchMaxResults,
	})
	if err != nil {
		logger.Error("build web search provider", "err", err)
		os.Exit(1)
	}
	imageGenerationProvider, err := imagegen.NewProvider(imagegen.Config{
		Backend: cfg.ResponsesImageGenerationBackend,
		BaseURL: cfg.ResponsesImageGenerationBaseURL,
		Timeout: cfg.ResponsesImageGenerationTimeout,
	})
	if err != nil {
		logger.Error("build image generation provider", "err", err)
		os.Exit(1)
	}
	httpapi.StartLocalCodeInterpreterCleanupLoop(processCtx, logger, localCodeInterpreter, store, store, cfg.ResponsesCodeInterpreterCleanupInterval)
	startSQLiteMaintenanceCleanupLoop(processCtx, logger, store, cfg.SQLiteMaintenanceCleanupInterval)

	server := &http.Server{
		Addr: cfg.Addr,
		Handler: httpapi.NewRouter(httpapi.RouterDeps{
			Logger:              logger,
			LlamaClient:         llamaClient,
			ResponseService:     responseService,
			ConversationService: conversationService,
			Auth:                httpapi.StaticBearerAuthConfig{Mode: cfg.ShimAuthMode, BearerTokens: cfg.ShimAuthBearerTokens},
			RateLimit:           httpapi.RateLimitConfig{Enabled: cfg.ShimRateLimitEnabled, RequestsPerMinute: cfg.ShimRateLimitRequestsPerMinute, Burst: cfg.ShimRateLimitBurst},
			MetricsConfig:       httpapi.MetricsConfig{Enabled: cfg.ShimMetricsEnabled, Path: cfg.ShimMetricsPath},
			Metrics:             metrics,
			ServiceLimits: httpapi.ServiceLimits{
				JSONBodyBytes:                    cfg.ShimJSONBodyLimitBytes,
				RetrievalFileUploadBytes:         cfg.RetrievalFileUploadMaxBytes,
				ChatCompletionsShadowStoreBytes:  cfg.ChatCompletionsShadowStoreMaxBytes,
				CustomToolGrammarDefinitionBytes: cfg.CustomToolGrammarDefinitionMaxBytes,
				CustomToolCompiledPatternBytes:   cfg.CustomToolCompiledPatternMaxBytes,
				RetrievalMaxConcurrentSearches:   cfg.RetrievalMaxConcurrentSearches,
				RetrievalMaxSearchQueries:        cfg.RetrievalMaxSearchQueries,
				RetrievalMaxGroundingChunks:      cfg.RetrievalMaxGroundingChunks,
				CodeInterpreterMaxConcurrentRuns: cfg.ResponsesCodeInterpreterMaxConcurrentRuns,
			},
			ChatCompletionsStoreWhenOmitted:       cfg.ChatCompletionsStoreWhenOmitted,
			ResponsesMode:                         cfg.ResponsesMode,
			ResponsesCustomToolsMode:              cfg.ResponsesCustomToolsMode,
			ResponsesCodexEnableCompatibility:     cfg.ResponsesCodexEnableCompatibility,
			ResponsesCodexForceToolChoiceRequired: cfg.ResponsesCodexForceToolChoiceRequired,
			ResponsesWebSearchBackend:             cfg.ResponsesWebSearchBackend,
			ResponsesImageGenerationBackend:       cfg.ResponsesImageGenerationBackend,
			WebSearchProvider:                     webSearchProvider,
			ImageGenerationProvider:               imageGenerationProvider,
			LocalComputer:                         localComputer,
			LocalCodeInterpreter:                  localCodeInterpreter,
			RetrievalIndexBackend:                 cfg.RetrievalIndexBackend,
			RetrievalEmbedder:                     retrievalEmbedder,
			Store:                                 store,
		}),
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  cfg.IdleTimeout,
	}

	logger.Info(
		"shim listening",
		"addr", cfg.Addr,
		"llama_base_url", cfg.LlamaBaseURL,
		"sqlite_path", cfg.SQLitePath,
		"sqlite_maintenance_cleanup_interval", cfg.SQLiteMaintenanceCleanupInterval,
		"config_file", cfg.ConfigFile,
		"log_file_path", cfg.LogFilePath,
		"shim_auth_mode", cfg.ShimAuthMode,
		"shim_auth_bearer_token_count", len(cfg.ShimAuthBearerTokens),
		"shim_rate_limit_enabled", cfg.ShimRateLimitEnabled,
		"shim_rate_limit_requests_per_minute", cfg.ShimRateLimitRequestsPerMinute,
		"shim_rate_limit_burst", cfg.ShimRateLimitBurst,
		"shim_metrics_enabled", cfg.ShimMetricsEnabled,
		"shim_metrics_path", cfg.ShimMetricsPath,
		"shim_json_body_limit_bytes", cfg.ShimJSONBodyLimitBytes,
		"shim_retrieval_file_upload_max_bytes", cfg.RetrievalFileUploadMaxBytes,
		"shim_chat_completions_shadow_store_max_bytes", cfg.ChatCompletionsShadowStoreMaxBytes,
		"shim_custom_tool_grammar_definition_max_bytes", cfg.CustomToolGrammarDefinitionMaxBytes,
		"shim_custom_tool_compiled_pattern_max_bytes", cfg.CustomToolCompiledPatternMaxBytes,
		"shim_retrieval_max_concurrent_searches", cfg.RetrievalMaxConcurrentSearches,
		"shim_retrieval_max_search_queries", cfg.RetrievalMaxSearchQueries,
		"shim_retrieval_max_grounding_chunks", cfg.RetrievalMaxGroundingChunks,
		"shim_code_interpreter_max_concurrent_runs", cfg.ResponsesCodeInterpreterMaxConcurrentRuns,
		"retrieval_index_backend", cfg.RetrievalIndexBackend,
		"retrieval_embedder_backend", cfg.RetrievalEmbedderBackend,
		"retrieval_embedder_base_url", cfg.RetrievalEmbedderBaseURL,
		"retrieval_embedder_model", cfg.RetrievalEmbedderModel,
		"chat_completions_default_store_when_omitted", cfg.ChatCompletionsStoreWhenOmitted,
		"responses_mode", cfg.ResponsesMode,
		"responses_custom_tools_mode", cfg.ResponsesCustomToolsMode,
		"responses_codex_enable_compatibility", cfg.ResponsesCodexEnableCompatibility,
		"responses_codex_force_tool_choice_required", cfg.ResponsesCodexForceToolChoiceRequired,
		"responses_web_search_backend", cfg.ResponsesWebSearchBackend,
		"responses_web_search_base_url", cfg.ResponsesWebSearchBaseURL,
		"responses_web_search_timeout", cfg.ResponsesWebSearchTimeout,
		"responses_web_search_max_results", cfg.ResponsesWebSearchMaxResults,
		"responses_image_generation_backend", cfg.ResponsesImageGenerationBackend,
		"responses_image_generation_base_url", cfg.ResponsesImageGenerationBaseURL,
		"responses_image_generation_timeout", cfg.ResponsesImageGenerationTimeout,
		"responses_computer_backend", cfg.ResponsesComputerBackend,
		"responses_code_interpreter_backend", cfg.ResponsesCodeInterpreterBackend,
		"responses_code_interpreter_python_binary", cfg.ResponsesCodeInterpreterPythonBinary,
		"responses_code_interpreter_docker_binary", cfg.ResponsesCodeInterpreterDockerBinary,
		"responses_code_interpreter_docker_image", cfg.ResponsesCodeInterpreterDockerImage,
		"responses_code_interpreter_docker_memory_limit", cfg.ResponsesCodeInterpreterDockerMemory,
		"responses_code_interpreter_docker_cpu_limit", cfg.ResponsesCodeInterpreterDockerCPU,
		"responses_code_interpreter_docker_pids_limit", cfg.ResponsesCodeInterpreterDockerPids,
		"responses_code_interpreter_execution_timeout", cfg.ResponsesCodeInterpreterTimeout,
		"responses_code_interpreter_input_file_url_policy", cfg.ResponsesCodeInterpreterInputFileURLPolicy,
		"responses_code_interpreter_input_file_url_allow_hosts", cfg.ResponsesCodeInterpreterInputFileURLAllowHosts,
		"responses_code_interpreter_cleanup_interval", cfg.ResponsesCodeInterpreterCleanupInterval,
		"responses_code_interpreter_generated_files_limit", cfg.ResponsesCodeInterpreterGeneratedFiles,
		"responses_code_interpreter_generated_file_bytes_limit", cfg.ResponsesCodeInterpreterGeneratedFileBytes,
		"responses_code_interpreter_generated_total_bytes_limit", cfg.ResponsesCodeInterpreterGeneratedTotalBytes,
		"responses_code_interpreter_remote_input_file_bytes_limit", cfg.ResponsesCodeInterpreterRemoteInputFileBytes,
	)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("server stopped", "err", err)
		os.Exit(1)
	}
}

func buildLocalComputerRuntimeConfig(cfg config.Config) (httpapi.LocalComputerRuntimeConfig, error) {
	switch cfg.ResponsesComputerBackend {
	case "", config.ResponsesComputerBackendDisabled:
		return httpapi.LocalComputerRuntimeConfig{}, nil
	case config.ResponsesComputerBackendChatCompletions:
		return httpapi.LocalComputerRuntimeConfig{
			Backend: httpapi.LocalComputerBackendChatCompletions,
		}, nil
	default:
		return httpapi.LocalComputerRuntimeConfig{}, fmt.Errorf("unsupported computer backend %q", cfg.ResponsesComputerBackend)
	}
}

func buildLocalCodeInterpreterRuntimeConfig(cfg config.Config) (httpapi.LocalCodeInterpreterRuntimeConfig, error) {
	limits := httpapi.LocalCodeInterpreterLimits{
		GeneratedFiles:       cfg.ResponsesCodeInterpreterGeneratedFiles,
		GeneratedFileBytes:   int(cfg.ResponsesCodeInterpreterGeneratedFileBytes),
		GeneratedTotalBytes:  int(cfg.ResponsesCodeInterpreterGeneratedTotalBytes),
		RemoteInputFileBytes: int(cfg.ResponsesCodeInterpreterRemoteInputFileBytes),
	}
	switch cfg.ResponsesCodeInterpreterBackend {
	case config.ResponsesCodeInterpreterBackendDisabled:
		return httpapi.LocalCodeInterpreterRuntimeConfig{}, nil
	case config.ResponsesCodeInterpreterBackendDocker:
		return httpapi.LocalCodeInterpreterRuntimeConfig{
			Backend: sandbox.DockerBackend{
				DockerBinary: cfg.ResponsesCodeInterpreterDockerBinary,
				Image:        cfg.ResponsesCodeInterpreterDockerImage,
				Timeout:      cfg.ResponsesCodeInterpreterTimeout,
				MemoryLimit:  cfg.ResponsesCodeInterpreterDockerMemory,
				CPULimit:     cfg.ResponsesCodeInterpreterDockerCPU,
				PidsLimit:    cfg.ResponsesCodeInterpreterDockerPids,
			},
			Limits:                 limits,
			InputFileURLPolicy:     cfg.ResponsesCodeInterpreterInputFileURLPolicy,
			InputFileURLAllowHosts: append([]string(nil), cfg.ResponsesCodeInterpreterInputFileURLAllowHosts...),
		}, nil
	default:
		return httpapi.LocalCodeInterpreterRuntimeConfig{}, fmt.Errorf("unsupported code interpreter backend %q", cfg.ResponsesCodeInterpreterBackend)
	}
}

func buildLogWriter(logFilePath string) (io.Writer, *os.File, error) {
	if logFilePath == "" {
		return os.Stdout, nil, nil
	}

	if dir := filepath.Dir(logFilePath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, nil, err
		}
	}

	file, err := os.OpenFile(logFilePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, nil, err
	}
	return io.MultiWriter(os.Stdout, file), file, nil
}
