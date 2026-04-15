package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"llama_shim/internal/domain"
)

type responseStreamEmitter struct {
	w                  http.ResponseWriter
	flusher            http.Flusher
	seq                int
	includeObfuscation bool
}

func newResponseStreamEmitter(w http.ResponseWriter, includeObfuscation bool) (*responseStreamEmitter, error) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, fmt.Errorf("response writer does not support streaming")
	}

	headers := w.Header()
	headers.Set("Content-Type", "text/event-stream")
	headers.Set("Cache-Control", "no-cache")
	headers.Set("Connection", "keep-alive")
	headers.Set("X-Accel-Buffering", "no")
	disableWriteDeadline(w)

	return &responseStreamEmitter{
		w:                  w,
		flusher:            flusher,
		includeObfuscation: includeObfuscation,
	}, nil
}

func (e *responseStreamEmitter) responseCreated(response domain.Response) error {
	return e.write("response.created", map[string]any{
		"type":            "response.created",
		"sequence_number": e.nextSequence(),
		"response":        response,
	})
}

func (e *responseStreamEmitter) responseInProgress(response domain.Response) error {
	return e.write("response.in_progress", map[string]any{
		"type":            "response.in_progress",
		"sequence_number": e.nextSequence(),
		"response":        response,
	})
}

func (e *responseStreamEmitter) outputItemAdded(itemID string) error {
	return e.outputItemAddedMessage(itemID, 0)
}

func (e *responseStreamEmitter) outputItemAddedMessage(itemID string, outputIndex int) error {
	return e.outputItemAddedAt(outputIndex, map[string]any{
		"id":      itemID,
		"type":    "message",
		"status":  "in_progress",
		"role":    "assistant",
		"content": []any{},
	})
}

func (e *responseStreamEmitter) outputItemAddedAt(outputIndex int, item map[string]any) error {
	return e.write("response.output_item.added", map[string]any{
		"type":            "response.output_item.added",
		"sequence_number": e.nextSequence(),
		"output_index":    outputIndex,
		"item":            item,
	})
}

func (e *responseStreamEmitter) contentPartAdded(itemID string) error {
	return e.contentPartAddedAt(itemID, 0)
}

func (e *responseStreamEmitter) contentPartAddedAt(itemID string, outputIndex int) error {
	return e.write("response.content_part.added", map[string]any{
		"type":            "response.content_part.added",
		"sequence_number": e.nextSequence(),
		"item_id":         itemID,
		"output_index":    outputIndex,
		"content_index":   0,
		"part": map[string]any{
			"type":        "output_text",
			"text":        "",
			"annotations": []any{},
		},
	})
}

func (e *responseStreamEmitter) outputTextDelta(responseID, itemID, delta string) error {
	return e.outputTextDeltaAt(responseID, itemID, 0, delta)
}

func (e *responseStreamEmitter) outputTextDeltaAt(responseID, itemID string, outputIndex int, delta string) error {
	payload := map[string]any{
		"type":            "response.output_text.delta",
		"sequence_number": e.nextSequence(),
		"response_id":     responseID,
		"item_id":         itemID,
		"output_index":    outputIndex,
		"content_index":   0,
		"delta":           delta,
	}
	if e.includeObfuscation {
		payload["obfuscation"] = strings.Repeat("x", utf8.RuneCountInString(delta))
	}
	return e.write("response.output_text.delta", payload)
}

func (e *responseStreamEmitter) outputTextDone(responseID, itemID, text string) error {
	return e.outputTextDoneAt(responseID, itemID, 0, text)
}

func (e *responseStreamEmitter) outputTextDoneAt(responseID, itemID string, outputIndex int, text string) error {
	return e.write("response.output_text.done", map[string]any{
		"type":            "response.output_text.done",
		"sequence_number": e.nextSequence(),
		"response_id":     responseID,
		"item_id":         itemID,
		"output_index":    outputIndex,
		"content_index":   0,
		"text":            text,
	})
}

func (e *responseStreamEmitter) contentPartDone(itemID, text string) error {
	return e.contentPartDoneAt(itemID, 0, text)
}

func (e *responseStreamEmitter) contentPartDoneAt(itemID string, outputIndex int, text string) error {
	return e.write("response.content_part.done", map[string]any{
		"type":            "response.content_part.done",
		"sequence_number": e.nextSequence(),
		"item_id":         itemID,
		"output_index":    outputIndex,
		"content_index":   0,
		"part": map[string]any{
			"type":        "output_text",
			"text":        text,
			"annotations": []any{},
		},
	})
}

func (e *responseStreamEmitter) outputItemDone(itemID string, item domain.Item) error {
	return e.outputItemDoneAt(itemID, item, 0)
}

func (e *responseStreamEmitter) outputItemDoneAt(itemID string, item domain.Item, outputIndex int) error {
	payload := item.Map()
	payload["id"] = itemID
	return e.write("response.output_item.done", map[string]any{
		"type":            "response.output_item.done",
		"sequence_number": e.nextSequence(),
		"output_index":    outputIndex,
		"item":            payload,
	})
}

func (e *responseStreamEmitter) responseCompleted(response domain.Response) error {
	return e.write("response.completed", map[string]any{
		"type":            "response.completed",
		"sequence_number": e.nextSequence(),
		"response":        response,
	})
}

func (e *responseStreamEmitter) error(payload apiError) error {
	return e.write("error", map[string]any{
		"type":            "error",
		"sequence_number": e.nextSequence(),
		"error":           payload,
	})
}

func (e *responseStreamEmitter) done() error {
	if _, err := fmt.Fprint(e.w, "data: [DONE]\n\n"); err != nil {
		return err
	}
	e.flusher.Flush()
	return nil
}

func (e *responseStreamEmitter) write(eventType string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	if _, err := fmt.Fprintf(e.w, "event: %s\n", eventType); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(e.w, "data: %s\n\n", body); err != nil {
		return err
	}
	e.flusher.Flush()
	return nil
}

func (e *responseStreamEmitter) nextSequence() int {
	e.seq++
	return e.seq
}

func disableWriteDeadline(w http.ResponseWriter) {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
}
