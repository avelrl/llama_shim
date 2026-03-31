package domain

func NewResponse(id, model, outputText, previousResponseID, conversationID string) Response {
	assistantItem := NewOutputTextMessage(outputText)
	response := Response{
		ID:         id,
		Object:     "response",
		Model:      model,
		OutputText: outputText,
		Output:     []MessageItem{assistantItem},
	}
	if previousResponseID != "" {
		response.PreviousResponseID = previousResponseID
	}
	if conversationID != "" {
		response.Conversation = conversationID
	}
	return response
}

func ResponseFromStored(stored StoredResponse) Response {
	return Response{
		ID:                 stored.ID,
		Object:             "response",
		Model:              stored.Model,
		PreviousResponseID: stored.PreviousResponseID,
		Conversation:       stored.ConversationID,
		OutputText:         stored.OutputText,
		Output:             stored.Output,
	}
}
