package httpapi

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"llama_shim/internal/domain"
)

const (
	defaultResponseInputItemsLimit = 20
	maxResponseInputItemsLimit     = 100
)

type responseRetrieveQuery struct {
	Include            []string
	IncludeObfuscation bool
	StartingAfter      int
	Stream             bool
}

type responseInputItemsQuery struct {
	After   string
	Include []string
	Limit   int
	Order   string
}

type responseReplayEvent struct {
	eventType string
	payload   map[string]any
}

func parseResponseRetrieveQuery(r *http.Request) (responseRetrieveQuery, error) {
	values := r.URL.Query()

	stream, _, err := parseOptionalBoolQuery(values, "stream")
	if err != nil {
		return responseRetrieveQuery{}, err
	}
	includeObfuscation, ok, err := parseOptionalBoolQuery(values, "include_obfuscation")
	if err != nil {
		return responseRetrieveQuery{}, err
	}
	if !ok {
		includeObfuscation = true
	}
	startingAfter, ok, err := parseOptionalIntQuery(values, "starting_after")
	if err != nil {
		return responseRetrieveQuery{}, err
	}
	if ok && startingAfter < 0 {
		return responseRetrieveQuery{}, domain.NewValidationError("starting_after", "starting_after must be greater than or equal to 0")
	}

	return responseRetrieveQuery{
		Include:            parseResponseIncludeValues(values),
		IncludeObfuscation: includeObfuscation,
		StartingAfter:      startingAfter,
		Stream:             stream,
	}, nil
}

func parseResponseInputItemsQuery(r *http.Request) (responseInputItemsQuery, error) {
	values := r.URL.Query()

	query := responseInputItemsQuery{
		After:   strings.TrimSpace(values.Get("after")),
		Include: parseResponseIncludeValues(values),
		Limit:   defaultResponseInputItemsLimit,
		Order:   domain.ConversationItemOrderDesc,
	}

	if rawLimit := strings.TrimSpace(values.Get("limit")); rawLimit != "" {
		limit, err := strconv.Atoi(rawLimit)
		if err != nil || limit < 1 || limit > maxResponseInputItemsLimit {
			return responseInputItemsQuery{}, domain.NewValidationError("limit", "limit must be between 1 and 100")
		}
		query.Limit = limit
	}

	if rawOrder := strings.TrimSpace(values.Get("order")); rawOrder != "" {
		switch rawOrder {
		case domain.ConversationItemOrderAsc, domain.ConversationItemOrderDesc:
			query.Order = rawOrder
		default:
			return responseInputItemsQuery{}, domain.NewValidationError("order", "order must be one of asc or desc")
		}
	}

	return query, nil
}

func paginateResponseInputItems(items []domain.Item, query responseInputItemsQuery) (listConversationItemsResponse, error) {
	ordered := append([]domain.Item(nil), items...)
	if query.Order == domain.ConversationItemOrderDesc {
		reverseResponseItems(ordered)
	}

	start := 0
	if query.After != "" {
		afterIndex := -1
		for idx, item := range ordered {
			if strings.TrimSpace(item.ID()) == query.After {
				afterIndex = idx
				break
			}
		}
		if afterIndex < 0 {
			return listConversationItemsResponse{}, domain.NewValidationError("after", "after cursor was not found")
		}
		start = afterIndex + 1
	}

	end := start + query.Limit
	if end > len(ordered) {
		end = len(ordered)
	}
	pageItems := ordered[start:end]

	response := listConversationItemsResponse{
		Object:  "list",
		Data:    make([]map[string]any, 0, len(pageItems)),
		HasMore: end < len(ordered),
	}
	for _, item := range pageItems {
		response.Data = append(response.Data, item.Map())
	}
	if len(response.Data) > 0 {
		firstID := payloadID(response.Data[0])
		lastID := payloadID(response.Data[len(response.Data)-1])
		response.FirstID = &firstID
		response.LastID = &lastID
	}
	return response, nil
}

func reverseResponseItems(items []domain.Item) {
	for left, right := 0, len(items)-1; left < right; left, right = left+1, right-1 {
		items[left], items[right] = items[right], items[left]
	}
}

func parseResponseIncludeValues(values url.Values) []string {
	rawIncludes := values["include"]
	if len(rawIncludes) == 0 {
		return nil
	}

	includes := make([]string, 0, len(rawIncludes))
	for _, rawInclude := range rawIncludes {
		for _, part := range strings.Split(rawInclude, ",") {
			include := strings.TrimSpace(part)
			if include == "" {
				continue
			}
			includes = append(includes, include)
		}
	}
	if len(includes) == 0 {
		return nil
	}
	return includes
}

func parseOptionalBoolQuery(values url.Values, name string) (bool, bool, error) {
	raw := strings.TrimSpace(values.Get(name))
	if raw == "" {
		return false, false, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, false, domain.NewValidationError(name, name+" must be a boolean")
	}
	return value, true, nil
}

func parseOptionalIntQuery(values url.Values, name string) (int, bool, error) {
	raw := strings.TrimSpace(values.Get(name))
	if raw == "" {
		return 0, false, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, false, domain.NewValidationError(name, name+" must be an integer")
	}
	return value, true, nil
}

func writeResponseReplayAsSSE(w http.ResponseWriter, response domain.Response, startingAfter int, includeObfuscation bool) error {
	emitter, err := newResponseStreamEmitter(w, false)
	if err != nil {
		return err
	}

	events := buildResponseReplayEvents(response, includeObfuscation)
	for idx, event := range events {
		sequence := idx + 1
		if sequence <= startingAfter {
			continue
		}
		event.payload["sequence_number"] = sequence
		if err := emitter.write(event.eventType, event.payload); err != nil {
			return err
		}
	}
	return emitter.done()
}

func buildResponseReplayEvents(response domain.Response, includeObfuscation bool) []responseReplayEvent {
	status := strings.TrimSpace(response.Status)
	if status == "" {
		status = "completed"
	}

	created := responseReplaySnapshot(response, "in_progress", false)
	events := []responseReplayEvent{
		{
			eventType: "response.created",
			payload: map[string]any{
				"type":     "response.created",
				"response": created,
			},
		},
		{
			eventType: "response.in_progress",
			payload: map[string]any{
				"type":     "response.in_progress",
				"response": created,
			},
		},
	}

	outputItems := responseReplayOutputItems(response)
	for outputIndex, outputItem := range outputItems {
		events = append(events, buildResponseReplayOutputItemEvents(response.ID, outputIndex, outputItem, includeObfuscation)...)
	}

	finalEventType := "response.completed"
	if status != "completed" {
		finalEventType = fmt.Sprintf("response.%s", status)
	}
	events = append(events, responseReplayEvent{
		eventType: finalEventType,
		payload: map[string]any{
			"type":     finalEventType,
			"response": response,
		},
	})

	return events
}

func responseReplaySnapshot(response domain.Response, status string, completed bool) domain.Response {
	snapshot := response
	snapshot.Status = status
	if !completed {
		snapshot.CompletedAt = nil
		snapshot.Output = []domain.Item{}
		snapshot.OutputText = ""
	}
	return snapshot
}

func responseReplayOutputItems(response domain.Response) []domain.Item {
	if len(response.Output) > 0 {
		return append([]domain.Item(nil), response.Output...)
	}
	if strings.TrimSpace(response.OutputText) == "" {
		return nil
	}
	return []domain.Item{domain.NewOutputTextMessage(response.OutputText)}
}

func replayItemPayload(item domain.Item) map[string]any {
	payload := item.Map()
	if id := strings.TrimSpace(item.ID()); id != "" {
		payload["id"] = id
	}
	return payload
}

func buildResponseReplayOutputItemEvents(responseID string, outputIndex int, item domain.Item, includeObfuscation bool) []responseReplayEvent {
	addedItem := replayItemPayload(item)
	itemType := strings.TrimSpace(item.Type)
	switch itemType {
	case "message":
		addedItem["content"] = []any{}
	default:
		if isSyntheticReplayOutputItemType(itemType) {
			if isToolStreamItemType(itemType) {
				ensureCompletedToolItemID(addedItem, responseID, outputIndex)
			}
			addedItem = inProgressOutputItemSnapshot(addedItem)
		}
	}
	if itemType == "message" || isSyntheticReplayOutputItemType(itemType) || strings.TrimSpace(item.Status()) != "" {
		addedItem["status"] = "in_progress"
	}

	events := []responseReplayEvent{
		{
			eventType: "response.output_item.added",
			payload: map[string]any{
				"type":         "response.output_item.added",
				"output_index": outputIndex,
				"item":         addedItem,
			},
		},
	}
	itemID := strings.TrimSpace(asStringValue(addedItem["id"]))

	switch itemType {
	case "message":
		itemID := strings.TrimSpace(item.ID())
		for contentIndex, part := range responseReplayItemParts(item) {
			partType := strings.TrimSpace(asStringValue(part["type"]))
			switch partType {
			case "output_text":
				text := asStringValue(part["text"])
				addedPart := cloneReplayMap(part)
				addedPart["text"] = ""
				if _, ok := addedPart["annotations"]; !ok {
					addedPart["annotations"] = []any{}
				}
				events = append(events, responseReplayEvent{
					eventType: "response.content_part.added",
					payload: map[string]any{
						"type":          "response.content_part.added",
						"item_id":       itemID,
						"output_index":  outputIndex,
						"content_index": contentIndex,
						"part":          addedPart,
					},
				})
				if text != "" {
					events = append(events,
						responseReplayTextDeltaEvent(responseID, itemID, outputIndex, contentIndex, text, includeObfuscation),
						responseReplayEvent{
							eventType: "response.output_text.done",
							payload: map[string]any{
								"type":          "response.output_text.done",
								"response_id":   responseID,
								"item_id":       itemID,
								"output_index":  outputIndex,
								"content_index": contentIndex,
								"text":          text,
							},
						},
					)
				}
				events = append(events, responseReplayEvent{
					eventType: "response.content_part.done",
					payload: map[string]any{
						"type":          "response.content_part.done",
						"item_id":       itemID,
						"output_index":  outputIndex,
						"content_index": contentIndex,
						"part":          cloneReplayMap(part),
					},
				})
			default:
				events = append(events,
					responseReplayEvent{
						eventType: "response.content_part.added",
						payload: map[string]any{
							"type":          "response.content_part.added",
							"item_id":       itemID,
							"output_index":  outputIndex,
							"content_index": contentIndex,
							"part":          cloneReplayMap(part),
						},
					},
					responseReplayEvent{
						eventType: "response.content_part.done",
						payload: map[string]any{
							"type":          "response.content_part.done",
							"item_id":       itemID,
							"output_index":  outputIndex,
							"content_index": contentIndex,
							"part":          cloneReplayMap(part),
						},
					},
				)
			}
		}
	case "reasoning":
		itemID := strings.TrimSpace(item.ID())
		for contentIndex, part := range responseReplayItemParts(item) {
			if strings.TrimSpace(asStringValue(part["type"])) != "reasoning_text" {
				continue
			}
			text := asStringValue(part["text"])
			if text == "" {
				continue
			}
			events = append(events,
				responseReplayReasoningDeltaEvent(responseID, itemID, outputIndex, contentIndex, text, includeObfuscation),
				responseReplayEvent{
					eventType: "response.reasoning_text.done",
					payload: map[string]any{
						"type":          "response.reasoning_text.done",
						"item_id":       itemID,
						"output_index":  outputIndex,
						"content_index": contentIndex,
						"text":          text,
					},
				},
			)
		}
	case "function_call", "custom_tool_call", "mcp_call", "mcp_tool_call":
		deltaEvent, doneEvent, valueKey := toolStreamEventShape(itemType)
		progressEvent := toolStreamProgressEventType(itemType)
		failedEvent := toolStreamFailureEventType(itemType)
		doneItem := replayItemPayload(item)
		ensureCompletedToolItemID(doneItem, responseID, outputIndex)
		value := strings.TrimSpace(asStringValue(doneItem[valueKey]))
		if value != "" {
			events = append(events, responseReplayTextDeltaEvent(responseID, itemID, outputIndex, 0, value, includeObfuscation))
			events[len(events)-1].eventType = deltaEvent
			events[len(events)-1].payload["type"] = deltaEvent
			delete(events[len(events)-1].payload, "content_index")
			if includeObfuscation {
				events[len(events)-1].payload["obfuscation"] = strings.Repeat("x", utf8.RuneCountInString(value))
			}
		}

		donePayload := map[string]any{
			"type":         doneEvent,
			"response_id":  responseID,
			"item_id":      itemID,
			"output_index": outputIndex,
			"item":         doneItem,
		}
		if value != "" {
			donePayload[valueKey] = value
		}
		events = append(events, responseReplayEvent{
			eventType: doneEvent,
			payload:   donePayload,
		})
		if progressEvent != "" {
			events = append(events, responseReplayEvent{
				eventType: progressEvent,
				payload: map[string]any{
					"type":         progressEvent,
					"response_id":  responseID,
					"item_id":      itemID,
					"output_index": outputIndex,
				},
			})
		}
		if failedEvent != "" && isFailedToolStreamItem(doneItem) {
			failedPayload := map[string]any{
				"type":         failedEvent,
				"response_id":  responseID,
				"item_id":      itemID,
				"output_index": outputIndex,
			}
			if errPayload, ok := doneItem["error"]; ok && errPayload != nil {
				failedPayload["error"] = errPayload
			}
			events = append(events, responseReplayEvent{
				eventType: failedEvent,
				payload:   failedPayload,
			})
		}
	}

	if hostedEventTypes := hostedToolReplayEventTypes(itemType, replayItemPayload(item)); len(hostedEventTypes) > 0 {
		for _, hostedEventType := range hostedEventTypes {
			events = append(events, responseReplayEvent{
				eventType: hostedEventType,
				payload:   hostedToolReplayEventPayload(hostedEventType, itemID, outputIndex),
			})
		}
	}

	doneItemPayload := replayItemPayload(item)
	if isToolStreamItemType(itemType) {
		ensureCompletedToolItemID(doneItemPayload, responseID, outputIndex)
	}

	events = append(events, responseReplayEvent{
		eventType: "response.output_item.done",
		payload: map[string]any{
			"type":         "response.output_item.done",
			"output_index": outputIndex,
			"item":         doneItemPayload,
		},
	})
	return events
}

func responseReplayItemParts(item domain.Item) []map[string]any {
	payload := item.Map()
	rawParts, ok := payload["content"].([]any)
	if !ok || len(rawParts) == 0 {
		return nil
	}
	parts := make([]map[string]any, 0, len(rawParts))
	for _, rawPart := range rawParts {
		part, ok := rawPart.(map[string]any)
		if !ok {
			continue
		}
		parts = append(parts, cloneReplayMap(part))
	}
	return parts
}

func cloneReplayMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func asStringValue(value any) string {
	text, _ := value.(string)
	return text
}

func responseReplayTextDeltaEvent(responseID, itemID string, outputIndex, contentIndex int, text string, includeObfuscation bool) responseReplayEvent {
	payload := map[string]any{
		"type":          "response.output_text.delta",
		"response_id":   responseID,
		"item_id":       itemID,
		"output_index":  outputIndex,
		"content_index": contentIndex,
		"delta":         text,
	}
	if includeObfuscation {
		payload["obfuscation"] = strings.Repeat("x", utf8.RuneCountInString(text))
	}
	return responseReplayEvent{
		eventType: "response.output_text.delta",
		payload:   payload,
	}
}

func responseReplayReasoningDeltaEvent(_ string, itemID string, outputIndex, contentIndex int, text string, _ bool) responseReplayEvent {
	payload := map[string]any{
		"type":          "response.reasoning_text.delta",
		"item_id":       itemID,
		"output_index":  outputIndex,
		"content_index": contentIndex,
		"delta":         text,
	}
	return responseReplayEvent{
		eventType: "response.reasoning_text.delta",
		payload:   payload,
	}
}
