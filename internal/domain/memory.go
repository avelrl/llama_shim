package domain

type MemoryNote struct {
	ID               string
	Scope            string
	SessionID        string
	Text             string
	Source           string
	SourceResponseID string
	Metadata         map[string]string
	CreatedAt        string
	UpdatedAt        string
	LastUsedAt       string
}

type ListMemoryNotesQuery struct {
	SessionID     string
	IncludeGlobal bool
	Limit         int
}
