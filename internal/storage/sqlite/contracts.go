package sqlite

import "llama_shim/internal/storage"

var (
	_ storage.Store                     = (*Store)(nil)
	_ storage.HealthStore               = (*Store)(nil)
	_ storage.ResponseStore             = (*Store)(nil)
	_ storage.ResponseConversationStore = (*Store)(nil)
	_ storage.ConversationStore         = (*Store)(nil)
	_ storage.FileStore                 = (*Store)(nil)
	_ storage.FileBackingStore          = (*Store)(nil)
	_ storage.VectorStore               = (*Store)(nil)
	_ storage.ChatCompletionStore       = (*Store)(nil)
	_ storage.CodeInterpreterStore      = (*Store)(nil)
)
