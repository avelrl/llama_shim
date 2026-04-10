package httpapi

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResponseStreamEventProxyLogsOutputTextAndSummary(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	ctx := context.WithValue(context.Background(), requestIDKey, "req_test")
	proxy := newResponseStreamEventProxy(ctx, logger, customToolTransportPlan{}, nil)

	var out bytes.Buffer
	lines := []string{
		"event: response.created\n",
		"data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_test\",\"model\":\"google/gemma-4-26b-a4b:latest\",\"output_text\":\"\",\"output\":[]}}\n",
		"\n",
		"event: response.output_text.done\n",
		"data: {\"type\":\"response.output_text.done\",\"response_id\":\"resp_test\",\"item_id\":\"msg_test\",\"output_index\":0,\"content_index\":0,\"text\":\"You are a coding agent...\"}\n",
		"\n",
		"event: response.completed\n",
		"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_test\",\"model\":\"google/gemma-4-26b-a4b:latest\",\"output_text\":\"You are a coding agent...\",\"output\":[]}}\n",
		"\n",
	}

	for _, line := range lines {
		require.NoError(t, proxy.WriteLine(&out, line))
	}
	require.NoError(t, proxy.Flush(io.Discard))

	output := logs.String()
	require.Contains(t, output, `"msg":"responses stream event"`)
	require.Contains(t, output, `"event_type":"response.output_text.done"`)
	require.Contains(t, output, `"text_preview":"You are a coding agent..."`)
	require.Contains(t, output, `"msg":"responses stream summary"`)
	require.Contains(t, output, `"output_text_preview":"You are a coding agent..."`)
	require.Contains(t, output, `"event_count":5`)
}

func TestResponseStreamEventProxyKeepsStreamedMessageIDOnCompleted(t *testing.T) {
	proxy := newResponseStreamEventProxy(context.Background(), nil, customToolTransportPlan{}, nil)

	var out bytes.Buffer
	lines := []string{
		"event: response.created\n",
		"data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_test\",\"model\":\"google/gemma-4-26b-a4b:latest\",\"output_text\":\"\",\"output\":[]}}\n",
		"\n",
		"event: response.output_text.done\n",
		"data: {\"type\":\"response.output_text.done\",\"response_id\":\"resp_test\",\"item_id\":\"msg_stream\",\"output_index\":0,\"content_index\":0,\"text\":\"hello\"}\n",
		"\n",
		"event: response.completed\n",
		"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_test\",\"model\":\"google/gemma-4-26b-a4b:latest\",\"output_text\":\"hello\",\"output\":[{\"id\":\"msg_completed\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"hello\"}]}]}}\n",
		"\n",
	}

	for _, line := range lines {
		require.NoError(t, proxy.WriteLine(&out, line))
	}
	require.NoError(t, proxy.Flush(io.Discard))

	output := out.String()
	require.Contains(t, output, `"item_id":"msg_stream"`)
	require.Contains(t, output, `"id":"msg_stream"`)
	require.NotContains(t, output, `"msg_completed"`)
	require.Equal(t, 1, strings.Count(output, "event: response.output_item.done\n"))
}

func TestLooksLikeSSEPayload(t *testing.T) {
	require.True(t, looksLikeSSEPayload("text/event-stream", nil))
	require.True(t, looksLikeSSEPayload("application/octet-stream", []byte("event: response.created\n")))
	require.True(t, looksLikeSSEPayload("application/octet-stream", []byte(" data: {\"ok\":true}\n")))
	require.False(t, looksLikeSSEPayload("application/json", []byte(`{"ok":true}`)))
}

func TestProxyResponsesStreamCanonicalizesErrorBody(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusBadRequest,
		Header: http.Header{
			"Content-Type": []string{"application/json"},
		},
		Body: io.NopCloser(strings.NewReader(`{"error":{"message":"messages is required","type":"invalid_request_error"}}`)),
	}

	recorder := httptest.NewRecorder()
	err := proxyResponsesStream(context.Background(), nil, recorder, resp, customToolTransportPlan{}, nil)
	require.NoError(t, err)
	require.JSONEq(t, `{"error":{"message":"messages is required","type":"invalid_request_error","param":null,"code":null}}`, recorder.Body.String())
}

func TestWriteCompletedResponseAsSSEReplaysCoreEventSequence(t *testing.T) {
	recorder := httptest.NewRecorder()

	err := writeCompletedResponseAsSSE(context.Background(), nil, recorder, []byte(`{
		"id":"resp_test",
		"object":"response",
		"created_at":1741900000,
		"status":"completed",
		"completed_at":1741900001,
		"model":"test-model",
		"background":false,
		"store":true,
		"text":{"format":{"type":"text"}},
		"usage":null,
		"metadata":{},
		"output_text":"OK",
		"output":[
			{
				"id":"msg_test",
				"type":"message",
				"role":"assistant",
				"status":"completed",
				"content":[
					{"type":"output_text","text":"OK","annotations":[]}
				]
			}
		]
	}`), customToolTransportPlan{}, true)
	require.NoError(t, err)

	body := recorder.Body.String()
	require.Contains(t, body, "event: response.created\n")
	require.Contains(t, body, "event: response.in_progress\n")
	require.Contains(t, body, "event: response.content_part.added\n")
	require.Contains(t, body, "event: response.output_text.delta\n")
	require.Contains(t, body, `"delta":"OK"`)
	require.Contains(t, body, `"obfuscation":"xx"`)
	require.Contains(t, body, "event: response.output_text.done\n")
	require.Contains(t, body, `"response_id":"resp_test"`)
	require.Contains(t, body, "event: response.content_part.done\n")
	require.Contains(t, body, "event: response.output_item.done\n")
	require.Contains(t, body, "event: response.completed\n")
	require.Contains(t, body, "data: [DONE]\n\n")
}

func TestShouldIgnoreStreamProxyError(t *testing.T) {
	require.True(t, shouldIgnoreStreamProxyError(context.Canceled))
	require.False(t, shouldIgnoreStreamProxyError(io.EOF))
	require.False(t, shouldIgnoreStreamProxyError(nil))
}
