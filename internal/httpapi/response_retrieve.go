package httpapi

import (
	"encoding/json"
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

func writeResponseReplayAsSSE(w http.ResponseWriter, response domain.Response, artifacts []domain.ResponseReplayArtifact, startingAfter int, includeObfuscation bool) error {
	emitter, err := newResponseStreamEmitter(w, false)
	if err != nil {
		return err
	}

	sequence := 0
	if err := forEachResponseReplayEvent(response, artifacts, includeObfuscation, func(event responseReplayEvent) error {
		sequence++
		if sequence <= startingAfter {
			return nil
		}
		event.payload["sequence_number"] = sequence
		return emitter.write(event.eventType, event.payload)
	}); err != nil {
		return err
	}
	return emitter.done()
}

func forEachResponseReplayEvent(response domain.Response, artifacts []domain.ResponseReplayArtifact, includeObfuscation bool, visit func(responseReplayEvent) error) error {
	status := strings.TrimSpace(response.Status)
	if status == "" {
		status = "completed"
	}

	created := responseReplaySnapshot(response, "in_progress", false)
	if err := visit(responseReplayEvent{
		eventType: "response.created",
		payload: map[string]any{
			"type":     "response.created",
			"response": created,
		},
	}); err != nil {
		return err
	}
	if err := visit(responseReplayEvent{
		eventType: "response.in_progress",
		payload: map[string]any{
			"type":     "response.in_progress",
			"response": created,
		},
	}); err != nil {
		return err
	}

	outputItems := responseReplayOutputItems(response)
	for outputIndex, outputItem := range outputItems {
		if err := forEachResponseReplayOutputItemEvent(response.ID, outputIndex, outputItem, artifacts, includeObfuscation, visit); err != nil {
			return err
		}
	}

	finalEventType := "response.completed"
	if status != "completed" {
		finalEventType = fmt.Sprintf("response.%s", status)
	}
	if err := visit(responseReplayEvent{
		eventType: finalEventType,
		payload: map[string]any{
			"type":     finalEventType,
			"response": response,
		},
	}); err != nil {
		return err
	}
	return nil
}

func responseReplaySnapshot(response domain.Response, status string, completed bool) domain.Response {
	snapshot := response
	snapshot.Status = status
	if !completed {
		snapshot.CompletedAt = nil
		snapshot.Error = nil
		snapshot.IncompleteDetails = nil
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

func forEachResponseReplayOutputItemEvent(responseID string, outputIndex int, item domain.Item, artifacts []domain.ResponseReplayArtifact, includeObfuscation bool, visit func(responseReplayEvent) error) error {
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
	if itemType == "message" || (!isSyntheticReplayOutputItemType(itemType) && strings.TrimSpace(item.Status()) != "") {
		addedItem["status"] = "in_progress"
	}

	if err := visit(responseReplayEvent{
		eventType: "response.output_item.added",
		payload: map[string]any{
			"type":         "response.output_item.added",
			"output_index": outputIndex,
			"item":         addedItem,
		},
	}); err != nil {
		return err
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
				annotations := responseReplayPartAnnotations(part)
				addedPart := cloneReplayMap(part)
				addedPart["text"] = ""
				addedPart["annotations"] = []any{}
				if err := visit(responseReplayEvent{
					eventType: "response.content_part.added",
					payload: map[string]any{
						"type":          "response.content_part.added",
						"item_id":       itemID,
						"output_index":  outputIndex,
						"content_index": contentIndex,
						"part":          addedPart,
					},
				}); err != nil {
					return err
				}
				if text != "" {
					if err := visit(responseReplayTextDeltaEvent(responseID, itemID, outputIndex, contentIndex, text, includeObfuscation)); err != nil {
						return err
					}
				}
				if err := forEachResponseReplayTextAnnotationEvent(itemID, outputIndex, contentIndex, annotations, visit); err != nil {
					return err
				}
				if err := visit(responseReplayEvent{
					eventType: "response.output_text.done",
					payload: map[string]any{
						"type":          "response.output_text.done",
						"response_id":   responseID,
						"item_id":       itemID,
						"output_index":  outputIndex,
						"content_index": contentIndex,
						"text":          text,
					},
				}); err != nil {
					return err
				}
				if err := visit(responseReplayEvent{
					eventType: "response.content_part.done",
					payload: map[string]any{
						"type":          "response.content_part.done",
						"item_id":       itemID,
						"output_index":  outputIndex,
						"content_index": contentIndex,
						"part":          cloneReplayMap(part),
					},
				}); err != nil {
					return err
				}
			default:
				if err := visit(responseReplayEvent{
					eventType: "response.content_part.added",
					payload: map[string]any{
						"type":          "response.content_part.added",
						"item_id":       itemID,
						"output_index":  outputIndex,
						"content_index": contentIndex,
						"part":          cloneReplayMap(part),
					},
				}); err != nil {
					return err
				}
				if err := visit(responseReplayEvent{
					eventType: "response.content_part.done",
					payload: map[string]any{
						"type":          "response.content_part.done",
						"item_id":       itemID,
						"output_index":  outputIndex,
						"content_index": contentIndex,
						"part":          cloneReplayMap(part),
					},
				}); err != nil {
					return err
				}
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
			if err := visit(responseReplayReasoningDeltaEvent(responseID, itemID, outputIndex, contentIndex, text, includeObfuscation)); err != nil {
				return err
			}
			if err := visit(responseReplayEvent{
				eventType: "response.reasoning_text.done",
				payload: map[string]any{
					"type":          "response.reasoning_text.done",
					"item_id":       itemID,
					"output_index":  outputIndex,
					"content_index": contentIndex,
					"text":          text,
				},
			}); err != nil {
				return err
			}
		}
	case "function_call", "custom_tool_call", "mcp_call", "mcp_tool_call":
		deltaEvent, doneEvent, valueKey := toolStreamEventShape(itemType)
		progressEvent := toolStreamProgressEventType(itemType)
		failedEvent := toolStreamFailureEventType(itemType)
		doneItem := replayItemPayload(item)
		ensureCompletedToolItemID(doneItem, responseID, outputIndex)
		value := strings.TrimSpace(asStringValue(doneItem[valueKey]))
		if value != "" {
			if err := visit(responseReplayToolDeltaEvent(deltaEvent, responseID, itemID, outputIndex, value, includeObfuscation)); err != nil {
				return err
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
		if err := visit(responseReplayEvent{
			eventType: doneEvent,
			payload:   donePayload,
		}); err != nil {
			return err
		}
		if progressEvent != "" {
			if err := visit(responseReplayEvent{
				eventType: progressEvent,
				payload: map[string]any{
					"type":         progressEvent,
					"response_id":  responseID,
					"item_id":      itemID,
					"output_index": outputIndex,
				},
			}); err != nil {
				return err
			}
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
			if err := visit(responseReplayEvent{
				eventType: failedEvent,
				payload:   failedPayload,
			}); err != nil {
				return err
			}
		}
	}

	if err := forEachCodeInterpreterReplayEvent(replayItemPayload(item), itemID, outputIndex, includeObfuscation, func(replaySpec hostedToolReplayEventSpec) error {
		return visit(responseReplayEvent{
			eventType: replaySpec.eventType,
			payload:   replaySpec.payload,
		})
	}); err != nil {
		return err
	}

	if hostedEventTypes := hostedToolReplayEventTypes(itemType, replayItemPayload(item)); len(hostedEventTypes) > 0 {
		for _, hostedEventType := range hostedEventTypes {
			if err := visit(responseReplayEvent{
				eventType: hostedEventType,
				payload:   hostedToolReplayEventPayload(hostedEventType, itemID, outputIndex),
			}); err != nil {
				return err
			}
		}
	}
	if err := forEachResponseReplayArtifactEventForItem(outputIndex, itemID, artifacts, visit); err != nil {
		return err
	}

	doneItemPayload := replayItemPayload(item)
	if isToolStreamItemType(itemType) {
		ensureCompletedToolItemID(doneItemPayload, responseID, outputIndex)
	}

	if err := visit(responseReplayEvent{
		eventType: "response.output_item.done",
		payload: map[string]any{
			"type":         "response.output_item.done",
			"output_index": outputIndex,
			"item":         doneItemPayload,
		},
	}); err != nil {
		return err
	}
	return nil
}

func forEachResponseReplayArtifactEventForItem(outputIndex int, itemID string, artifacts []domain.ResponseReplayArtifact, visit func(responseReplayEvent) error) error {
	if len(artifacts) == 0 || strings.TrimSpace(itemID) == "" {
		return nil
	}

	for _, artifact := range artifacts {
		if strings.TrimSpace(artifact.EventType) == "" || strings.TrimSpace(artifact.PayloadJSON) == "" {
			continue
		}

		var payload map[string]any
		if err := json.Unmarshal([]byte(artifact.PayloadJSON), &payload); err != nil {
			continue
		}
		if strings.TrimSpace(asStringValue(payload["item_id"])) != itemID {
			continue
		}
		artifactOutputIndex, ok := intAttr(payload["output_index"])
		if !ok || artifactOutputIndex != outputIndex {
			continue
		}
		payload["type"] = strings.TrimSpace(artifact.EventType)
		if err := visit(responseReplayEvent{
			eventType: strings.TrimSpace(artifact.EventType),
			payload:   payload,
		}); err != nil {
			return err
		}
	}

	return nil
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

func responseReplayPartAnnotations(part map[string]any) []any {
	rawAnnotations, ok := part["annotations"].([]any)
	if !ok || len(rawAnnotations) == 0 {
		return nil
	}
	annotations := make([]any, 0, len(rawAnnotations))
	for _, annotation := range rawAnnotations {
		if annotation == nil {
			continue
		}
		annotations = append(annotations, annotation)
	}
	return annotations
}

func forEachResponseReplayTextAnnotationEvent(itemID string, outputIndex, contentIndex int, annotations []any, visit func(responseReplayEvent) error) error {
	if len(annotations) == 0 {
		return nil
	}
	for annotationIndex, annotation := range annotations {
		if err := visit(responseReplayEvent{
			eventType: "response.output_text.annotation.added",
			payload: map[string]any{
				"type":             "response.output_text.annotation.added",
				"item_id":          itemID,
				"output_index":     outputIndex,
				"content_index":    contentIndex,
				"annotation_index": annotationIndex,
				"annotation":       annotation,
			},
		}); err != nil {
			return err
		}
	}
	return nil
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

func responseReplayToolDeltaEvent(eventType, responseID, itemID string, outputIndex int, value string, includeObfuscation bool) responseReplayEvent {
	payload := map[string]any{
		"type":         eventType,
		"response_id":  responseID,
		"item_id":      itemID,
		"output_index": outputIndex,
		"delta":        value,
	}
	if includeObfuscation {
		payload["obfuscation"] = strings.Repeat("x", utf8.RuneCountInString(value))
	}
	return responseReplayEvent{
		eventType: eventType,
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
