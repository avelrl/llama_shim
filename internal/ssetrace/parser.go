package ssetrace

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

type Event struct {
	Event       string `json:"event,omitempty"`
	ID          string `json:"id,omitempty"`
	RetryMillis *int   `json:"retry_millis,omitempty"`
	Data        string `json:"data"`
	JSON        any    `json:"json,omitempty"`
}

type Stream struct {
	EventCount int     `json:"event_count"`
	Done       bool    `json:"done"`
	Events     []Event `json:"events"`
}

type CaptureFixture struct {
	CapturedAt      string              `json:"captured_at"`
	Label           string              `json:"label,omitempty"`
	Method          string              `json:"method"`
	URL             string              `json:"url"`
	StatusCode      int                 `json:"status_code"`
	ContentType     string              `json:"content_type,omitempty"`
	Request         any                 `json:"request,omitempty"`
	ResponseHeaders map[string][]string `json:"response_headers,omitempty"`
	Stream          Stream              `json:"stream"`
}

func Parse(body []byte) (Stream, error) {
	return ParseReader(bytes.NewReader(body))
}

func ParseReader(r io.Reader) (Stream, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 16<<20)

	var (
		stream     Stream
		eventName  string
		eventID    string
		retryValue *int
		dataLines  []string
	)

	flush := func() error {
		if len(dataLines) == 0 && eventName == "" && eventID == "" && retryValue == nil {
			return nil
		}

		event := Event{
			Event:       eventName,
			ID:          eventID,
			RetryMillis: retryValue,
			Data:        strings.Join(dataLines, "\n"),
		}
		if event.Data == "[DONE]" {
			stream.Done = true
		} else if json.Valid([]byte(event.Data)) {
			if err := json.Unmarshal([]byte(event.Data), &event.JSON); err != nil {
				return fmt.Errorf("decode SSE event JSON: %w", err)
			}
		}
		stream.Events = append(stream.Events, event)

		eventName = ""
		eventID = ""
		retryValue = nil
		dataLines = nil
		return nil
	}

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if err := flush(); err != nil {
				return Stream{}, err
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}

		field := line
		value := ""
		if idx := strings.IndexByte(line, ':'); idx >= 0 {
			field = line[:idx]
			value = line[idx+1:]
			value = strings.TrimPrefix(value, " ")
		}

		switch field {
		case "event":
			eventName = value
		case "data":
			dataLines = append(dataLines, value)
		case "id":
			eventID = value
		case "retry":
			millis, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return Stream{}, fmt.Errorf("parse SSE retry value %q: %w", value, err)
			}
			retryValue = &millis
		}
	}
	if err := scanner.Err(); err != nil {
		return Stream{}, err
	}
	if err := flush(); err != nil {
		return Stream{}, err
	}

	stream.EventCount = len(stream.Events)
	return stream, nil
}
