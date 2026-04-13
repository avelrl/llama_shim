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
	"llama_shim/internal/llama"
	"llama_shim/internal/sandbox"
	"llama_shim/internal/service"
	"llama_shim/internal/storage/sqlite"
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

	store, err := sqlite.Open(processCtx, cfg.SQLitePath)
	if err != nil {
		logger.Error("open sqlite", "err", err)
		os.Exit(1)
	}
	defer store.Close()

	llamaClient := llama.NewClient(cfg.LlamaBaseURL, cfg.LlamaTimeout)
	responseService := service.NewResponseService(store, store, llamaClient)
	conversationService := service.NewConversationService(store)
	localCodeInterpreter, err := buildLocalCodeInterpreterRuntimeConfig(cfg)
	if err != nil {
		logger.Error("build code interpreter runtime", "err", err)
		os.Exit(1)
	}
	httpapi.StartLocalCodeInterpreterCleanupLoop(processCtx, logger, localCodeInterpreter, store, store, cfg.ResponsesCodeInterpreterCleanupInterval)

	server := &http.Server{
		Addr: cfg.Addr,
		Handler: httpapi.NewRouter(httpapi.RouterDeps{
			Logger:                                logger,
			LlamaClient:                           llamaClient,
			ResponseService:                       responseService,
			ConversationService:                   conversationService,
			ResponsesMode:                         cfg.ResponsesMode,
			ResponsesCustomToolsMode:              cfg.ResponsesCustomToolsMode,
			ResponsesCodexEnableCompatibility:     cfg.ResponsesCodexEnableCompatibility,
			ResponsesCodexForceToolChoiceRequired: cfg.ResponsesCodexForceToolChoiceRequired,
			LocalCodeInterpreter:                  localCodeInterpreter,
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
		"config_file", cfg.ConfigFile,
		"log_file_path", cfg.LogFilePath,
		"responses_mode", cfg.ResponsesMode,
		"responses_custom_tools_mode", cfg.ResponsesCustomToolsMode,
		"responses_codex_enable_compatibility", cfg.ResponsesCodexEnableCompatibility,
		"responses_codex_force_tool_choice_required", cfg.ResponsesCodexForceToolChoiceRequired,
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
	)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("server stopped", "err", err)
		os.Exit(1)
	}
}

func buildLocalCodeInterpreterRuntimeConfig(cfg config.Config) (httpapi.LocalCodeInterpreterRuntimeConfig, error) {
	switch cfg.ResponsesCodeInterpreterBackend {
	case config.ResponsesCodeInterpreterBackendDisabled:
		return httpapi.LocalCodeInterpreterRuntimeConfig{}, nil
	case config.ResponsesCodeInterpreterBackendUnsafeHost:
		return httpapi.LocalCodeInterpreterRuntimeConfig{
			Backend: sandbox.UnsafeHostBackend{
				PythonBinary: cfg.ResponsesCodeInterpreterPythonBinary,
				Timeout:      cfg.ResponsesCodeInterpreterTimeout,
			},
			InputFileURLPolicy:     cfg.ResponsesCodeInterpreterInputFileURLPolicy,
			InputFileURLAllowHosts: append([]string(nil), cfg.ResponsesCodeInterpreterInputFileURLAllowHosts...),
		}, nil
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
