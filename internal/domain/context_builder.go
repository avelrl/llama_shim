package domain

import "fmt"

func BuildLineageContext(lineage []StoredResponse, instructions string, currentInput []Item) ([]Item, error) {
	contextItems := make([]Item, 0, len(currentInput)+(len(lineage)*2)+1)
	for _, response := range lineage {
		contextItems = append(contextItems, response.NormalizedInputItems...)
		if len(response.Output) == 0 {
			return nil, fmt.Errorf("response %s has empty output", response.ID)
		}
		contextItems = append(contextItems, response.Output...)
	}

	return AppendCurrentRequestContext(contextItems, instructions, currentInput), nil
}

func BuildConversationContext(items []ConversationItem, instructions string, currentInput []Item) []Item {
	contextItems := make([]Item, 0, len(items)+len(currentInput)+1)
	for _, item := range items {
		contextItems = append(contextItems, item.Item)
	}

	return AppendCurrentRequestContext(contextItems, instructions, currentInput)
}

func AppendCurrentRequestContext(base []Item, instructions string, currentInput []Item) []Item {
	out := make([]Item, 0, len(base)+len(currentInput)+1)
	out = append(out, base...)
	if instructions != "" {
		// Instructions apply only to the current request, so they are appended
		// immediately before the current input instead of being persisted in history.
		out = append(out, NewInputTextMessage("system", instructions))
	}
	out = append(out, currentInput...)
	return out
}

func AssistantOutputItem(response StoredResponse) (Item, error) {
	if len(response.Output) == 0 {
		return Item{}, fmt.Errorf("response %s has empty output", response.ID)
	}
	return response.Output[0], nil
}

func BuildConversationAppendItems(startSeq int, input []Item, output []Item) []ConversationItem {
	items := make([]ConversationItem, 0, len(input)+len(output))
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
	for _, item := range output {
		items = append(items, ConversationItem{
			Seq:      seq,
			Source:   "response_output",
			Role:     item.Role,
			ItemType: item.Type,
			Item:     item,
		})
		seq++
	}
	return items
}
