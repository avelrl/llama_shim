package domain

const (
	ConversationItemOrderAsc  = "asc"
	ConversationItemOrderDesc = "desc"
)

type ListConversationItemsQuery struct {
	ConversationID string
	After          string
	Include        []string
	Limit          int
	Order          string
}

type ConversationItemPage struct {
	Items   []ConversationItem
	HasMore bool
}
