package storage

import (
	"context"

	"llama_shim/internal/domain"
)

const BackendSQLite = "sqlite"

type HealthStore interface {
	PingContext(ctx context.Context) error
	Close() error
}

type Store interface {
	HealthStore
	ResponseStore
	ResponseConversationStore
	ConversationStore
	FileStore
	FileBackingStore
	VectorStore
	ChatCompletionStore
	CodeInterpreterStore
}

type ResponseStore interface {
	GetResponse(ctx context.Context, id string) (domain.StoredResponse, error)
	GetResponseLineage(ctx context.Context, id string, maxItems int) ([]domain.StoredResponse, error)
	SaveResponse(ctx context.Context, response domain.StoredResponse) error
	SaveResponseReplayArtifacts(ctx context.Context, responseID string, artifacts []domain.ResponseReplayArtifact) error
	GetResponseReplayArtifacts(ctx context.Context, responseID string) ([]domain.ResponseReplayArtifact, error)
	DeleteResponse(ctx context.Context, id string) error
}

type ResponseConversationStore interface {
	GetConversation(ctx context.Context, id string) (domain.Conversation, []domain.ConversationItem, error)
	SaveResponseAndAppendConversation(ctx context.Context, conversation domain.Conversation, response domain.StoredResponse, input []domain.Item, output []domain.Item) error
}

type ConversationStore interface {
	CreateConversation(ctx context.Context, conversation domain.Conversation) error
	GetConversation(ctx context.Context, id string) (domain.Conversation, []domain.ConversationItem, error)
	GetConversationItem(ctx context.Context, conversationID, itemID string) (domain.ConversationItem, error)
	ListConversationItems(ctx context.Context, query domain.ListConversationItemsQuery) (domain.ConversationItemPage, error)
	AppendConversationItems(ctx context.Context, conversation domain.Conversation, items []domain.Item, createdAt string) ([]domain.ConversationItem, error)
	DeleteConversationItem(ctx context.Context, conversation domain.Conversation, itemID, updatedAt string) error
}

type FileStore interface {
	SaveFile(ctx context.Context, file domain.StoredFile) error
	GetFile(ctx context.Context, id string) (domain.StoredFile, error)
	ListFiles(ctx context.Context, query domain.ListFilesQuery) (domain.StoredFilePage, error)
	DeleteFile(ctx context.Context, id string) error
}

type FileBackingStore interface {
	SaveFile(ctx context.Context, file domain.StoredFile) error
	GetFile(ctx context.Context, id string) (domain.StoredFile, error)
	DeleteFile(ctx context.Context, id string) error
}

type VectorStore interface {
	SaveVectorStore(ctx context.Context, store domain.StoredVectorStore) error
	AttachFileToVectorStore(ctx context.Context, vectorStoreID, fileID string, attributes map[string]any, strategy domain.FileChunkingStrategy, createdAt int64) (domain.StoredVectorStoreFile, error)
	GetVectorStore(ctx context.Context, id string) (domain.StoredVectorStore, error)
	ListVectorStores(ctx context.Context, query domain.ListVectorStoresQuery) (domain.StoredVectorStorePage, error)
	DeleteVectorStore(ctx context.Context, id string) error
	SaveVectorStoreFile(ctx context.Context, file domain.StoredVectorStoreFile, content []string) error
	GetVectorStoreFile(ctx context.Context, vectorStoreID, fileID string) (domain.StoredVectorStoreFile, error)
	ListVectorStoreFiles(ctx context.Context, query domain.ListVectorStoreFilesQuery) (domain.StoredVectorStoreFilePage, error)
	DeleteVectorStoreFile(ctx context.Context, vectorStoreID, fileID string) error
	SearchVectorStore(ctx context.Context, query domain.VectorStoreSearchQuery) (domain.VectorStoreSearchPage, error)
}

type ChatCompletionStore interface {
	SaveChatCompletion(ctx context.Context, completion domain.StoredChatCompletion) error
	GetChatCompletion(ctx context.Context, id string) (domain.StoredChatCompletion, error)
	ListChatCompletions(ctx context.Context, query domain.ListStoredChatCompletionsQuery) (domain.StoredChatCompletionPage, error)
	UpdateChatCompletionMetadata(ctx context.Context, id string, metadata map[string]string) (domain.StoredChatCompletion, error)
	DeleteChatCompletion(ctx context.Context, id string) error
	ListChatCompletionMessages(ctx context.Context, completionID string, query domain.ListStoredChatCompletionMessagesQuery) (domain.StoredChatCompletionMessagePage, error)
}

type CodeInterpreterStore interface {
	GetCodeInterpreterSession(ctx context.Context, id string) (domain.CodeInterpreterSession, error)
	ListCodeInterpreterSessions(ctx context.Context, query domain.ListCodeInterpreterSessionsQuery) (domain.CodeInterpreterSessionPage, error)
	SaveCodeInterpreterSession(ctx context.Context, session domain.CodeInterpreterSession) error
	TouchCodeInterpreterSession(ctx context.Context, id string, lastActiveAt string) error
	DeleteCodeInterpreterSession(ctx context.Context, id string) error
	GetCodeInterpreterContainerFile(ctx context.Context, containerID string, id string) (domain.CodeInterpreterContainerFile, error)
	GetCodeInterpreterContainerFileByPath(ctx context.Context, containerID string, containerPath string) (domain.CodeInterpreterContainerFile, error)
	ListCodeInterpreterContainerFiles(ctx context.Context, query domain.ListCodeInterpreterContainerFilesQuery) (domain.CodeInterpreterContainerFilePage, error)
	SaveCodeInterpreterContainerFile(ctx context.Context, file domain.CodeInterpreterContainerFile) (domain.CodeInterpreterContainerFile, error)
	DeleteCodeInterpreterContainerFile(ctx context.Context, containerID string, id string) error
	CountCodeInterpreterContainerFileBackingReferences(ctx context.Context, backingFileID string) (int, error)
}
