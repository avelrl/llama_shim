package domain

import "fmt"

func BuildLineageContext(lineage []StoredResponse, instructions string, currentInput []MessageItem) ([]MessageItem, error) {
	contextItems := make([]MessageItem, 0, len(currentInput)+(len(lineage)*2)+1)
	for _, response := range lineage {
		contextItems = append(contextItems, response.NormalizedInputItems...)
		assistantItem, err := AssistantOutputItem(response)
		if err != nil {
			return nil, err
		}
		contextItems = append(contextItems, assistantItem)
	}

	return AppendCurrentRequestContext(contextItems, instructions, currentInput), nil
}

func BuildConversationContext(items []ConversationItem, instructions string, currentInput []MessageItem) []MessageItem {
	contextItems := make([]MessageItem, 0, len(items)+len(currentInput)+1)
	for _, item := range items {
		contextItems = append(contextItems, item.Item)
	}

	return AppendCurrentRequestContext(contextItems, instructions, currentInput)
}

func AppendCurrentRequestContext(base []MessageItem, instructions string, currentInput []MessageItem) []MessageItem {
	out := make([]MessageItem, 0, len(base)+len(currentInput)+1)
	out = append(out, base...)
	if instructions != "" {
		// Instructions apply only to the current request, so they are appended
		// immediately before the current input instead of being persisted in history.
		out = append(out, NewInputTextMessage("system", instructions))
	}
	out = append(out, currentInput...)
	return out
}

func AssistantOutputItem(response StoredResponse) (MessageItem, error) {
	if len(response.Output) == 0 {
		return MessageItem{}, fmt.Errorf("response %s has empty output", response.ID)
	}
	return response.Output[0], nil
}

func BuildConversationAppendItems(startSeq int, input []MessageItem, assistant MessageItem) []ConversationItem {
	items := make([]ConversationItem, 0, len(input)+1)
	seq := startSeq
	for _, item := range input {
		items = append(items, ConversationItem{
			Seq:      seq,
			Source:   "response_input",
			Role:     item.Role,
			ItemType: item.Type,
			Item:     item,
		})
		seq++
	}
	items = append(items, ConversationItem{
		Seq:      seq,
		Source:   "response_output",
		Role:     assistant.Role,
		ItemType: assistant.Type,
		Item:     assistant,
	})
	return items
}
