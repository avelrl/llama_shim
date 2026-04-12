package domain

// ProjectLocalTextGenerationContext keeps only text-message items that can be
// rendered through the shim-local llama chat/completions path. Tool items are
// intentionally dropped from generation context and are instead expected to be
// summarized separately when a local execution path needs them.
func ProjectLocalTextGenerationContext(items []Item) ([]Item, error) {
	out := make([]Item, 0, len(items))
	for _, item := range items {
		if item.Type != "message" {
			continue
		}
		if item.HasNonTextMessageContent() {
			return nil, ErrUnsupportedShape
		}
		switch item.Role {
		case "system", "developer", "user", "assistant":
			out = append(out, item)
		default:
			return nil, ErrUnsupportedShape
		}
	}
	return out, nil
}
