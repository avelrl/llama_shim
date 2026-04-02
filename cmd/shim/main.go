package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"llama_shim/internal/config"
	"llama_shim/internal/httpapi"
	"llama_shim/internal/llama"
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

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: cfg.LogLevel,
	}))

	ctx := context.Background()
	store, err := sqlite.Open(ctx, cfg.SQLitePath)
	if err != nil {
		logger.Error("open sqlite", "err", err)
		os.Exit(1)
	}
	defer store.Close()

	llamaClient := llama.NewClient(cfg.LlamaBaseURL, cfg.LlamaTimeout)
	responseService := service.NewResponseService(store, store, llamaClient)
	conversationService := service.NewConversationService(store)

	server := &http.Server{
		Addr: cfg.Addr,
		Handler: httpapi.NewRouter(httpapi.RouterDeps{
			Logger:                                logger,
			LlamaClient:                           llamaClient,
			ResponseService:                       responseService,
			ConversationService:                   conversationService,
			ResponsesCustomToolsMode:              cfg.ResponsesCustomToolsMode,
			ResponsesCodexForceToolChoiceRequired: cfg.ResponsesCodexForceToolChoiceRequired,
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
		"responses_custom_tools_mode", cfg.ResponsesCustomToolsMode,
		"responses_codex_force_tool_choice_required", cfg.ResponsesCodexForceToolChoiceRequired,
	)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("server stopped", "err", err)
		os.Exit(1)
	}
}
