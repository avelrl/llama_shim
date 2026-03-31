package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"llama_shim/internal/domain"
)

type responseStreamEmitter struct {
	w       http.ResponseWriter
	flusher http.Flusher
	seq     int
}

type streamOutputItem struct {
	ID      string            `json:"id"`
	Type    string            `json:"type"`
	Role    string            `json:"role"`
	Content []domain.TextPart `json:"content"`
}

func newResponseStreamEmitter(w http.ResponseWriter) (*responseStreamEmitter, error) {
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
		w:       w,
		flusher: flusher,
	}, nil
}

func (e *responseStreamEmitter) responseCreated(response domain.Response) error {
	return e.write("response.created", map[string]any{
		"type":            "response.created",
		"sequence_number": e.nextSequence(),
		"response":        response,
	})
}

func (e *responseStreamEmitter) outputItemAdded(itemID string) error {
	return e.write("response.output_item.added", map[string]any{
		"type":            "response.output_item.added",
		"sequence_number": e.nextSequence(),
		"output_index":    0,
		"item": streamOutputItem{
			ID:      itemID,
			Type:    "message",
			Role:    "assistant",
			Content: []domain.TextPart{{Type: "output_text", Text: ""}},
		},
	})
}

func (e *responseStreamEmitter) outputTextDelta(responseID, itemID, delta string) error {
	return e.write("response.output_text.delta", map[string]any{
		"type":            "response.output_text.delta",
		"sequence_number": e.nextSequence(),
		"response_id":     responseID,
		"item_id":         itemID,
		"output_index":    0,
		"content_index":   0,
		"delta":           delta,
	})
}

func (e *responseStreamEmitter) outputTextDone(itemID, text string) error {
	return e.write("response.output_text.done", map[string]any{
		"type":            "response.output_text.done",
		"sequence_number": e.nextSequence(),
		"item_id":         itemID,
		"output_index":    0,
		"content_index":   0,
		"text":            text,
	})
}

func (e *responseStreamEmitter) outputItemDone(itemID, text string) error {
	return e.write("response.output_item.done", map[string]any{
		"type":            "response.output_item.done",
		"sequence_number": e.nextSequence(),
		"output_index":    0,
		"item": streamOutputItem{
			ID:      itemID,
			Type:    "message",
			Role:    "assistant",
			Content: []domain.TextPart{{Type: "output_text", Text: text}},
		},
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
