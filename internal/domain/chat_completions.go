package domain

const (
	ChatCompletionOrderAsc  = "asc"
	ChatCompletionOrderDesc = "desc"
)

type StoredChatCompletion struct {
	ID           string
	Model        string
	Metadata     map[string]string
	RequestJSON  string
	ResponseJSON string
	CreatedAt    int64
}

type ListStoredChatCompletionsQuery struct {
	Model    string
	Metadata map[string]string
	After    string
	Limit    int
	Order    string
}

type StoredChatCompletionPage struct {
	Completions []StoredChatCompletion
	HasMore     bool
}
