package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"llama_shim/internal/domain"
	"llama_shim/internal/storage"
	"llama_shim/internal/storage/sqlite"
)

const (
	responseID        = "resp_governance_smoke"
	conversationID    = "conv_governance_smoke"
	chatCompletionID  = "chatcmpl_governance_smoke"
	fileID            = "file_governance_smoke"
	vectorStoreID     = "vs_governance_smoke"
	codeSessionID     = "ci_governance_smoke"
	containerFileID   = "cfile_governance_smoke"
	smokeCreatedAt    = "2026-05-06T09:00:00Z"
	smokeCreatedUnix  = int64(1778067600)
	smokeFileContents = "governance purge smoke content"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "governance-purge-smoke: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("command is required: seed, verify-present, or verify-purged")
	}
	switch args[0] {
	case "seed":
		return runWithStore(args[1:], seed)
	case "verify-present":
		return runWithStore(args[1:], verifyPresent)
	case "verify-purged":
		return runWithStore(args[1:], verifyPurged)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runWithStore(args []string, fn func(context.Context, *sqlite.Store) error) error {
	fs := flag.NewFlagSet("governance-purge-smoke", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	sqlitePath := fs.String("sqlite", "", "SQLite database path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *sqlitePath == "" {
		return errors.New("-sqlite is required")
	}
	ctx := context.Background()
	store, err := sqlite.Open(ctx, *sqlitePath)
	if err != nil {
		return fmt.Errorf("open sqlite: %w", err)
	}
	defer store.Close()
	return fn(ctx, store)
}

func seed(ctx context.Context, store *sqlite.Store) error {
	response := domain.StoredResponse{
		ID:                   responseID,
		Model:                "test-model",
		RequestJSON:          `{"input":"governance smoke"}`,
		ResponseJSON:         `{"id":"` + responseID + `","object":"response","status":"completed","output":[]}`,
		NormalizedInputItems: []domain.Item{domain.NewInputTextMessage("user", "governance smoke")},
		EffectiveInputItems:  []domain.Item{domain.NewInputTextMessage("user", "governance smoke")},
		Output:               []domain.Item{domain.NewOutputTextMessage("ok")},
		OutputText:           "ok",
		Store:                true,
		CreatedAt:            smokeCreatedAt,
		CompletedAt:          smokeCreatedAt,
	}
	if err := store.SaveResponse(ctx, response); err != nil {
		return fmt.Errorf("save response: %w", err)
	}
	if err := store.SaveResponseReplayArtifacts(ctx, response.ID, []domain.ResponseReplayArtifact{
		{Sequence: 1, EventType: "response.completed", PayloadJSON: `{"type":"response.completed"}`},
	}); err != nil {
		return fmt.Errorf("save response replay artifacts: %w", err)
	}

	conversation := domain.Conversation{
		ID:        conversationID,
		Object:    "conversation",
		Version:   1,
		CreatedAt: smokeCreatedAt,
		UpdatedAt: smokeCreatedAt,
		Items:     []domain.Item{domain.NewInputTextMessage("user", "conversation seed")},
	}
	if err := store.CreateConversation(ctx, conversation); err != nil {
		return fmt.Errorf("save conversation: %w", err)
	}

	completion := domain.StoredChatCompletion{
		ID:           chatCompletionID,
		Model:        "test-chat-model",
		Metadata:     map[string]string{"scope": "governance-smoke"},
		RequestJSON:  `{"model":"test-chat-model","store":true,"messages":[{"role":"user","content":"hello"}]}`,
		ResponseJSON: `{"id":"` + chatCompletionID + `","object":"chat.completion","created":1778067600,"model":"test-chat-model","choices":[]}`,
		CreatedAt:    smokeCreatedUnix,
	}
	if err := store.SaveChatCompletion(ctx, completion); err != nil {
		return fmt.Errorf("save chat completion: %w", err)
	}

	file := domain.StoredFile{
		ID:        fileID,
		Filename:  "governance-smoke.txt",
		Purpose:   "assistants",
		Bytes:     int64(len(smokeFileContents)),
		CreatedAt: smokeCreatedUnix + 1,
		Status:    "processed",
		Content:   []byte(smokeFileContents),
	}
	if err := store.SaveFile(ctx, file); err != nil {
		return fmt.Errorf("save file: %w", err)
	}
	vectorStore := domain.StoredVectorStore{
		ID:           vectorStoreID,
		Name:         "Governance Smoke",
		Metadata:     map[string]string{"scope": "governance-smoke"},
		CreatedAt:    smokeCreatedUnix + 2,
		LastActiveAt: smokeCreatedUnix + 2,
	}
	if err := store.SaveVectorStore(ctx, vectorStore); err != nil {
		return fmt.Errorf("save vector store: %w", err)
	}
	if _, err := store.AttachFileToVectorStore(ctx, vectorStore.ID, file.ID, map[string]any{"scope": "governance-smoke"}, domain.DefaultFileChunkingStrategy(), smokeCreatedUnix+3); err != nil {
		return fmt.Errorf("attach file to vector store: %w", err)
	}

	session := domain.CodeInterpreterSession{
		ID:                  codeSessionID,
		Owner:               "owner",
		Backend:             "docker",
		Status:              "running",
		Name:                "governance-smoke",
		MemoryLimit:         "1g",
		ExpiresAfterMinutes: 20,
		CreatedAt:           smokeCreatedAt,
		LastActiveAt:        smokeCreatedAt,
	}
	if err := store.SaveCodeInterpreterSession(ctx, session); err != nil {
		return fmt.Errorf("save code interpreter session: %w", err)
	}
	if _, err := store.SaveCodeInterpreterContainerFile(ctx, domain.CodeInterpreterContainerFile{
		ID:                containerFileID,
		ContainerID:       session.ID,
		BackingFileID:     file.ID,
		DeleteBackingFile: false,
		Path:              "/workspace/governance-smoke.txt",
		Source:            "generated",
		Bytes:             file.Bytes,
		CreatedAt:         smokeCreatedUnix + 4,
	}); err != nil {
		return fmt.Errorf("save code interpreter container file: %w", err)
	}
	return nil
}

func verifyPresent(ctx context.Context, store *sqlite.Store) error {
	checks := []struct {
		name string
		fn   func() error
	}{
		{name: "response", fn: func() error {
			_, err := store.GetResponse(ctx, responseID)
			return err
		}},
		{name: "conversation", fn: func() error {
			_, _, err := store.GetConversation(ctx, conversationID)
			return err
		}},
		{name: "chat completion", fn: func() error {
			_, err := store.GetChatCompletion(ctx, chatCompletionID)
			return err
		}},
		{name: "file", fn: func() error {
			_, err := store.GetFile(ctx, fileID)
			return err
		}},
		{name: "vector store", fn: func() error {
			_, err := store.GetVectorStore(ctx, vectorStoreID)
			return err
		}},
		{name: "code interpreter session", fn: func() error {
			_, err := store.GetCodeInterpreterSession(ctx, codeSessionID)
			return err
		}},
	}
	for _, check := range checks {
		if err := check.fn(); err != nil {
			return fmt.Errorf("%s should exist: %w", check.name, err)
		}
	}
	return nil
}

func verifyPurged(ctx context.Context, store *sqlite.Store) error {
	checks := []struct {
		name string
		fn   func() error
	}{
		{name: "response", fn: func() error {
			_, err := store.GetResponse(ctx, responseID)
			return err
		}},
		{name: "conversation", fn: func() error {
			_, _, err := store.GetConversation(ctx, conversationID)
			return err
		}},
		{name: "chat completion", fn: func() error {
			_, err := store.GetChatCompletion(ctx, chatCompletionID)
			return err
		}},
		{name: "file", fn: func() error {
			_, err := store.GetFile(ctx, fileID)
			return err
		}},
		{name: "vector store", fn: func() error {
			_, err := store.GetVectorStore(ctx, vectorStoreID)
			return err
		}},
		{name: "code interpreter session", fn: func() error {
			_, err := store.GetCodeInterpreterSession(ctx, codeSessionID)
			return err
		}},
	}
	for _, check := range checks {
		err := check.fn()
		if !errors.Is(err, storage.ErrNotFound) {
			return fmt.Errorf("%s should be purged, got %v", check.name, err)
		}
	}
	return nil
}
