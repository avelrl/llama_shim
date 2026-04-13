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

func TestWriteCompletedResponseAsSSEReplaysOutputTextAnnotations(t *testing.T) {
	recorder := httptest.NewRecorder()

	err := writeCompletedResponseAsSSE(context.Background(), nil, recorder, []byte(`{
		"id":"resp_test_annotations",
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
		"output_text":"See artifact report.txt",
		"output":[
			{
				"id":"msg_test",
				"type":"message",
				"role":"assistant",
				"status":"completed",
				"content":[
					{"type":"output_text","text":"See artifact report.txt","annotations":[{"type":"container_file_citation","container_id":"cntr_test","file_id":"cfile_test","filename":"report.txt","start_index":13,"end_index":23}]}
				]
			}
		]
	}`), customToolTransportPlan{}, true)
	require.NoError(t, err)

	body := recorder.Body.String()
	require.Contains(t, body, "event: response.output_text.annotation.added\n")
	require.Contains(t, body, `"annotation_index":0`)
	require.Contains(t, body, `"file_id":"cfile_test"`)
	require.Contains(t, body, `"annotations":[]`)
}

func TestShouldIgnoreStreamProxyError(t *testing.T) {
	require.True(t, shouldIgnoreStreamProxyError(context.Canceled))
	require.False(t, shouldIgnoreStreamProxyError(io.EOF))
	require.False(t, shouldIgnoreStreamProxyError(nil))
}

func TestNormalizeCompletedToolCallEventSynthesizesMCPReplayEvents(t *testing.T) {
	proxy := newResponseStreamEventProxy(context.Background(), nil, customToolTransportPlan{}, nil)

	before, eventType, payload := proxy.normalizeCompletedToolCallEvent("response.completed", map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id":     "resp_proxy_mcp",
			"object": "response",
			"model":  "test-model",
			"output": []any{
				map[string]any{
					"type":         "mcp_call",
					"name":         "lookup_orders",
					"server_label": "shopify",
					"arguments":    `{"status":"open"}`,
					"output":       `{"count":3}`,
					"status":       "completed",
				},
			},
			"output_text": "",
		},
	})

	require.Equal(t, "response.completed", eventType)
	require.Len(t, before, 6)
	require.Equal(t, "response.created", before[0].eventType)
	require.Equal(t, "response.output_item.added", before[1].eventType)
	require.Equal(t, "response.mcp_call_arguments.delta", before[2].eventType)
	require.Equal(t, "response.mcp_call_arguments.done", before[3].eventType)
	require.Equal(t, "response.mcp_call.in_progress", before[4].eventType)
	require.Equal(t, "response.output_item.done", before[5].eventType)

	addedItem, ok := before[1].payload["item"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "mcp_call", addedItem["type"])
	require.Equal(t, "", asString(addedItem["arguments"]))
	_, hasOutput := addedItem["output"]
	require.False(t, hasOutput)

	doneItem, ok := before[3].payload["item"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, `{"count":3}`, asString(doneItem["output"]))
	require.Equal(t, "item_proxy_mcp_0", asString(doneItem["id"]))

	inProgress := before[4].payload
	require.Equal(t, "item_proxy_mcp_0", asString(inProgress["item_id"]))
	require.Equal(t, "resp_proxy_mcp", asString(inProgress["response_id"]))

	completedResponse, ok := payload["response"].(map[string]any)
	require.True(t, ok)
	output, ok := completedResponse["output"].([]any)
	require.True(t, ok)
	require.Len(t, output, 1)
	finalItem, ok := output[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "item_proxy_mcp_0", asString(finalItem["id"]))
}

func TestNormalizeTextStreamEventSynthesizesAnnotationReplayFromCompletedResponse(t *testing.T) {
	proxy := newResponseStreamEventProxy(context.Background(), nil, customToolTransportPlan{}, nil)

	before, eventType, payload := proxy.normalizeTextStreamEvent("response.completed", map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id":          "resp_proxy_annotations",
			"object":      "response",
			"model":       "test-model",
			"output_text": "Created report.txt.\n\nGenerated files:\n- report.txt",
			"output": []any{
				map[string]any{
					"id":     "msg_proxy_annotations",
					"type":   "message",
					"role":   "assistant",
					"status": "completed",
					"content": []any{
						map[string]any{
							"type": "output_text",
							"text": "Created report.txt.\n\nGenerated files:\n- report.txt",
							"annotations": []any{
								map[string]any{
									"type":         "container_file_citation",
									"container_id": "cntr_test",
									"file_id":      "cfile_test",
									"filename":     "report.txt",
									"start_index":  39,
									"end_index":    49,
								},
							},
						},
					},
				},
			},
		},
	})

	require.Equal(t, "response.completed", eventType)
	require.Len(t, before, 5)
	require.Equal(t, "response.created", before[0].eventType)
	require.Equal(t, "response.output_item.added", before[1].eventType)
	require.Equal(t, "response.output_text.annotation.added", before[2].eventType)
	require.Equal(t, "response.output_text.done", before[3].eventType)
	require.Equal(t, "response.output_item.done", before[4].eventType)

	addedItem, ok := before[1].payload["item"].(map[string]any)
	require.True(t, ok)
	content, ok := addedItem["content"].([]map[string]any)
	require.True(t, ok)
	require.Len(t, content, 1)
	require.Equal(t, []any{}, content[0]["annotations"])

	doneItem, ok := before[4].payload["item"].(map[string]any)
	require.True(t, ok)
	doneContent, ok := doneItem["content"].([]map[string]any)
	require.True(t, ok)
	require.Len(t, doneContent, 1)
	doneAnnotations, ok := doneContent[0]["annotations"].([]any)
	require.True(t, ok)
	require.Len(t, doneAnnotations, 1)

	completedResponse, ok := payload["response"].(map[string]any)
	require.True(t, ok)
	output, ok := completedResponse["output"].([]map[string]any)
	require.True(t, ok)
	require.Len(t, output, 1)
	finalContent, ok := output[0]["content"].([]map[string]any)
	require.True(t, ok)
	require.Len(t, finalContent, 1)
	finalAnnotations, ok := finalContent[0]["annotations"].([]any)
	require.True(t, ok)
	require.Len(t, finalAnnotations, 1)
}

func TestNormalizeCompletedToolCallEventSynthesizesFailedMCPReplayEvents(t *testing.T) {
	proxy := newResponseStreamEventProxy(context.Background(), nil, customToolTransportPlan{}, nil)

	before, _, _ := proxy.normalizeCompletedToolCallEvent("response.completed", map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id":     "resp_proxy_mcp_failed",
			"object": "response",
			"model":  "test-model",
			"output": []any{
				map[string]any{
					"id":           "mcp_failed",
					"type":         "mcp_call",
					"name":         "lookup_orders",
					"server_label": "shopify",
					"arguments":    `{"status":"open"}`,
					"error": map[string]any{
						"type":    "tool_execution_error",
						"message": "remote MCP unavailable",
					},
					"status": "failed",
				},
			},
			"output_text": "",
		},
	})

	require.Len(t, before, 7)
	require.Equal(t, "response.mcp_call.failed", before[5].eventType)
	require.Equal(t, "response.output_item.done", before[6].eventType)

	failed := before[5].payload
	require.Equal(t, "mcp_failed", asString(failed["item_id"]))
	errPayload, ok := failed["error"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "tool_execution_error", asString(errPayload["type"]))
	require.Equal(t, "remote MCP unavailable", asString(errPayload["message"]))
}

func TestNormalizeCompletedToolCallEventSynthesizesHostedAddedDoneReplay(t *testing.T) {
	proxy := newResponseStreamEventProxy(context.Background(), nil, customToolTransportPlan{}, nil)

	before, eventType, payload := proxy.normalizeCompletedToolCallEvent("response.completed", map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id":     "resp_proxy_web_search",
			"object": "response",
			"model":  "test-model",
			"output": []any{
				map[string]any{
					"id":     "ws_test",
					"type":   "web_search_call",
					"status": "completed",
					"action": map[string]any{
						"type":  "search",
						"query": "latest weather in Paris",
					},
				},
			},
			"output_text": "",
		},
	})

	require.Equal(t, "response.completed", eventType)
	require.Len(t, before, 6)
	require.Equal(t, "response.created", before[0].eventType)
	require.Equal(t, "response.output_item.added", before[1].eventType)
	require.Equal(t, "response.web_search_call.in_progress", before[2].eventType)
	require.Equal(t, "response.web_search_call.searching", before[3].eventType)
	require.Equal(t, "response.web_search_call.completed", before[4].eventType)
	require.Equal(t, "response.output_item.done", before[5].eventType)

	addedItem, ok := before[1].payload["item"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "web_search_call", addedItem["type"])
	require.Equal(t, "in_progress", asString(addedItem["status"]))
	_, hasAction := addedItem["action"]
	require.False(t, hasAction)

	inProgress := before[2].payload
	require.Equal(t, "ws_test", asString(inProgress["item_id"]))

	searching := before[3].payload
	require.Equal(t, "ws_test", asString(searching["item_id"]))

	completed := before[4].payload
	require.Equal(t, "ws_test", asString(completed["item_id"]))

	doneItem, ok := before[5].payload["item"].(map[string]any)
	require.True(t, ok)
	action, ok := doneItem["action"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "search", asString(action["type"]))

	completedResponse, ok := payload["response"].(map[string]any)
	require.True(t, ok)
	output, ok := completedResponse["output"].([]any)
	require.True(t, ok)
	require.Len(t, output, 1)
	finalItem, ok := output[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "ws_test", asString(finalItem["id"]))
}

func TestNormalizeCompletedToolCallEventSynthesizesHostedOpenPageReplay(t *testing.T) {
	proxy := newResponseStreamEventProxy(context.Background(), nil, customToolTransportPlan{}, nil)

	before, eventType, payload := proxy.normalizeCompletedToolCallEvent("response.completed", map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id":     "resp_proxy_web_search_open_page",
			"object": "response",
			"model":  "test-model",
			"output": []any{
				map[string]any{
					"id":     "ws_open_page_test",
					"type":   "web_search_call",
					"status": "completed",
					"action": map[string]any{
						"type": "open_page",
						"url":  "https://example.com/story",
					},
				},
			},
			"output_text": "",
		},
	})

	require.Equal(t, "response.completed", eventType)
	require.Len(t, before, 6)
	require.Equal(t, "response.created", before[0].eventType)
	require.Equal(t, "response.output_item.added", before[1].eventType)
	require.Equal(t, "response.web_search_call.in_progress", before[2].eventType)
	require.Equal(t, "response.web_search_call.searching", before[3].eventType)
	require.Equal(t, "response.web_search_call.completed", before[4].eventType)
	require.Equal(t, "response.output_item.done", before[5].eventType)

	addedItem, ok := before[1].payload["item"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "web_search_call", addedItem["type"])
	require.Equal(t, "in_progress", asString(addedItem["status"]))
	_, hasAction := addedItem["action"]
	require.False(t, hasAction)

	doneItem, ok := before[5].payload["item"].(map[string]any)
	require.True(t, ok)
	action, ok := doneItem["action"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "open_page", asString(action["type"]))
	require.Equal(t, "https://example.com/story", asString(action["url"]))

	completedResponse, ok := payload["response"].(map[string]any)
	require.True(t, ok)
	output, ok := completedResponse["output"].([]any)
	require.True(t, ok)
	require.Len(t, output, 1)
	finalItem, ok := output[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "ws_open_page_test", asString(finalItem["id"]))
}

func TestNormalizeCompletedToolCallEventSynthesizesHostedFileSearchAddedDoneReplay(t *testing.T) {
	proxy := newResponseStreamEventProxy(context.Background(), nil, customToolTransportPlan{}, nil)

	before, eventType, payload := proxy.normalizeCompletedToolCallEvent("response.completed", map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id":     "resp_proxy_file_search",
			"object": "response",
			"model":  "test-model",
			"output": []any{
				map[string]any{
					"id":      "fs_test",
					"type":    "file_search_call",
					"status":  "completed",
					"queries": []any{"find onboarding handbook"},
					"search_results": []any{
						map[string]any{
							"file_id":  "file_123",
							"filename": "notes.txt",
							"score":    0.91,
						},
					},
				},
			},
			"output_text": "",
		},
	})

	require.Equal(t, "response.completed", eventType)
	require.Len(t, before, 6)
	require.Equal(t, "response.created", before[0].eventType)
	require.Equal(t, "response.output_item.added", before[1].eventType)
	require.Equal(t, "response.file_search_call.in_progress", before[2].eventType)
	require.Equal(t, "response.file_search_call.searching", before[3].eventType)
	require.Equal(t, "response.file_search_call.completed", before[4].eventType)
	require.Equal(t, "response.output_item.done", before[5].eventType)

	addedItem, ok := before[1].payload["item"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "file_search_call", addedItem["type"])
	require.Equal(t, "in_progress", asString(addedItem["status"]))
	queries, ok := addedItem["queries"].([]any)
	require.True(t, ok)
	require.Empty(t, queries)
	_, hasResults := addedItem["results"]
	require.False(t, hasResults)
	_, hasSearchResults := addedItem["search_results"]
	require.False(t, hasSearchResults)

	doneItem, ok := before[5].payload["item"].(map[string]any)
	require.True(t, ok)
	doneQueries, ok := doneItem["queries"].([]any)
	require.True(t, ok)
	require.Len(t, doneQueries, 1)
	searchResults, ok := doneItem["search_results"].([]any)
	require.True(t, ok)
	require.Len(t, searchResults, 1)

	completedResponse, ok := payload["response"].(map[string]any)
	require.True(t, ok)
	output, ok := completedResponse["output"].([]any)
	require.True(t, ok)
	require.Len(t, output, 1)
	finalItem, ok := output[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "fs_test", asString(finalItem["id"]))
}

func TestNormalizeCompletedToolCallEventSynthesizesHostedFindInPageReplay(t *testing.T) {
	proxy := newResponseStreamEventProxy(context.Background(), nil, customToolTransportPlan{}, nil)

	before, eventType, payload := proxy.normalizeCompletedToolCallEvent("response.completed", map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id":     "resp_proxy_web_search_find_in_page",
			"object": "response",
			"model":  "test-model",
			"output": []any{
				map[string]any{
					"id":     "ws_find_in_page_test",
					"type":   "web_search_call",
					"status": "completed",
					"action": map[string]any{
						"type":    "find_in_page",
						"url":     "https://example.com/story",
						"pattern": "Supported in reasoning models",
					},
				},
			},
			"output_text": "",
		},
	})

	require.Equal(t, "response.completed", eventType)
	require.Len(t, before, 6)
	require.Equal(t, "response.created", before[0].eventType)
	require.Equal(t, "response.output_item.added", before[1].eventType)
	require.Equal(t, "response.web_search_call.in_progress", before[2].eventType)
	require.Equal(t, "response.web_search_call.searching", before[3].eventType)
	require.Equal(t, "response.web_search_call.completed", before[4].eventType)
	require.Equal(t, "response.output_item.done", before[5].eventType)

	addedItem, ok := before[1].payload["item"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "web_search_call", addedItem["type"])
	require.Equal(t, "in_progress", asString(addedItem["status"]))
	_, hasAction := addedItem["action"]
	require.False(t, hasAction)

	doneItem, ok := before[5].payload["item"].(map[string]any)
	require.True(t, ok)
	action, ok := doneItem["action"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "find_in_page", asString(action["type"]))
	require.Equal(t, "https://example.com/story", asString(action["url"]))
	require.Equal(t, "Supported in reasoning models", asString(action["pattern"]))

	completedResponse, ok := payload["response"].(map[string]any)
	require.True(t, ok)
	output, ok := completedResponse["output"].([]any)
	require.True(t, ok)
	require.Len(t, output, 1)
	finalItem, ok := output[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "ws_find_in_page_test", asString(finalItem["id"]))
}

func TestNormalizeCompletedToolCallEventSynthesizesHostedCodeInterpreterAddedDoneReplay(t *testing.T) {
	proxy := newResponseStreamEventProxy(context.Background(), nil, customToolTransportPlan{}, nil)

	before, eventType, payload := proxy.normalizeCompletedToolCallEvent("response.completed", map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id":     "resp_proxy_code_interpreter",
			"object": "response",
			"model":  "test-model",
			"output": []any{
				map[string]any{
					"id":           "ci_test",
					"type":         "code_interpreter_call",
					"status":       "completed",
					"container_id": "cntr_123",
					"code":         "print(\"result=2.0\")",
					"outputs": []any{
						map[string]any{
							"type": "logs",
							"logs": "result=2.0\n",
						},
					},
				},
			},
			"output_text": "",
		},
	})

	require.Equal(t, "response.completed", eventType)
	require.Len(t, before, 8)
	require.Equal(t, "response.created", before[0].eventType)
	require.Equal(t, "response.output_item.added", before[1].eventType)
	require.Equal(t, "response.code_interpreter_call.in_progress", before[2].eventType)
	require.Equal(t, "response.code_interpreter_call_code.delta", before[3].eventType)
	require.Equal(t, "response.code_interpreter_call_code.done", before[4].eventType)
	require.Equal(t, "response.code_interpreter_call.interpreting", before[5].eventType)
	require.Equal(t, "response.code_interpreter_call.completed", before[6].eventType)
	require.Equal(t, "response.output_item.done", before[7].eventType)

	addedItem, ok := before[1].payload["item"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "code_interpreter_call", addedItem["type"])
	require.Equal(t, "cntr_123", asString(addedItem["container_id"]))
	require.Equal(t, "in_progress", asString(addedItem["status"]))
	require.Equal(t, "", asString(addedItem["code"]))
	addedOutputs, ok := addedItem["outputs"].([]any)
	require.True(t, ok)
	require.Empty(t, addedOutputs)

	deltaPayload := before[3].payload
	require.Equal(t, "ci_test", asString(deltaPayload["item_id"]))
	require.Equal(t, "print(\"result=2.0\")", asString(deltaPayload["delta"]))
	_, hasObfuscation := deltaPayload["obfuscation"]
	require.False(t, hasObfuscation)

	doneCodePayload := before[4].payload
	require.Equal(t, "ci_test", asString(doneCodePayload["item_id"]))
	require.Equal(t, "print(\"result=2.0\")", asString(doneCodePayload["code"]))

	doneItem, ok := before[7].payload["item"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "print(\"result=2.0\")", asString(doneItem["code"]))
	outputs, ok := doneItem["outputs"].([]any)
	require.True(t, ok)
	require.Len(t, outputs, 1)

	completedResponse, ok := payload["response"].(map[string]any)
	require.True(t, ok)
	output, ok := completedResponse["output"].([]any)
	require.True(t, ok)
	require.Len(t, output, 1)
	finalItem, ok := output[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "ci_test", asString(finalItem["id"]))
}

func TestNormalizeCompletedToolCallEventSynthesizesHostedCodeInterpreterNilOutputsReplay(t *testing.T) {
	proxy := newResponseStreamEventProxy(context.Background(), nil, customToolTransportPlan{}, nil)

	before, eventType, payload := proxy.normalizeCompletedToolCallEvent("response.completed", map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id":     "resp_proxy_code_interpreter_nil_outputs",
			"object": "response",
			"model":  "test-model",
			"output": []any{
				map[string]any{
					"id":           "ci_nil_outputs_test",
					"type":         "code_interpreter_call",
					"status":       "completed",
					"container_id": "cntr_456",
					"code":         "print(\"result=2.0\")",
					"outputs":      nil,
				},
			},
			"output_text": "",
		},
	})

	require.Equal(t, "response.completed", eventType)
	require.Len(t, before, 8)
	require.Equal(t, "response.output_item.added", before[1].eventType)
	require.Equal(t, "response.code_interpreter_call.in_progress", before[2].eventType)
	require.Equal(t, "response.code_interpreter_call_code.delta", before[3].eventType)
	require.Equal(t, "response.code_interpreter_call_code.done", before[4].eventType)
	require.Equal(t, "response.code_interpreter_call.interpreting", before[5].eventType)
	require.Equal(t, "response.code_interpreter_call.completed", before[6].eventType)
	require.Equal(t, "response.output_item.done", before[7].eventType)

	addedItem, ok := before[1].payload["item"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "", asString(addedItem["code"]))
	outputs, hasOutputs := addedItem["outputs"]
	require.True(t, hasOutputs)
	require.Nil(t, outputs)

	doneItem, ok := before[7].payload["item"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "print(\"result=2.0\")", asString(doneItem["code"]))
	outputs, hasOutputs = doneItem["outputs"]
	require.True(t, hasOutputs)
	require.Nil(t, outputs)

	completedResponse, ok := payload["response"].(map[string]any)
	require.True(t, ok)
	output, ok := completedResponse["output"].([]any)
	require.True(t, ok)
	require.Len(t, output, 1)
	finalItem, ok := output[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "ci_nil_outputs_test", asString(finalItem["id"]))
}

func TestNormalizeCompletedToolCallEventSynthesizesComputerCallAddedDoneReplay(t *testing.T) {
	proxy := newResponseStreamEventProxy(context.Background(), nil, customToolTransportPlan{}, nil)

	before, eventType, payload := proxy.normalizeCompletedToolCallEvent("response.completed", map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id":     "resp_proxy_computer_call",
			"object": "response",
			"model":  "test-model",
			"output": []any{
				map[string]any{
					"id":      "cu_test",
					"type":    "computer_call",
					"status":  "completed",
					"call_id": "call_test",
					"actions": []any{
						map[string]any{
							"type":   "click",
							"button": "left",
							"x":      636,
							"y":      343,
						},
						map[string]any{
							"type": "type",
							"text": "penguin",
						},
					},
				},
			},
			"output_text": "",
		},
	})

	require.Equal(t, "response.completed", eventType)
	require.Len(t, before, 3)
	require.Equal(t, "response.created", before[0].eventType)
	require.Equal(t, "response.output_item.added", before[1].eventType)
	require.Equal(t, "response.output_item.done", before[2].eventType)

	addedItem, ok := before[1].payload["item"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "computer_call", addedItem["type"])
	require.Equal(t, "call_test", asString(addedItem["call_id"]))
	require.Equal(t, "in_progress", asString(addedItem["status"]))
	_, hasActions := addedItem["actions"]
	require.False(t, hasActions)

	doneItem, ok := before[2].payload["item"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "completed", asString(doneItem["status"]))
	actions, ok := doneItem["actions"].([]any)
	require.True(t, ok)
	require.Len(t, actions, 2)

	completedResponse, ok := payload["response"].(map[string]any)
	require.True(t, ok)
	output, ok := completedResponse["output"].([]any)
	require.True(t, ok)
	require.Len(t, output, 1)
	finalItem, ok := output[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "cu_test", asString(finalItem["id"]))
}

func TestNormalizeCompletedToolCallEventSynthesizesImageGenerationCallReplaySubset(t *testing.T) {
	proxy := newResponseStreamEventProxy(context.Background(), nil, customToolTransportPlan{}, nil)

	before, eventType, payload := proxy.normalizeCompletedToolCallEvent("response.completed", map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id":     "resp_proxy_image_generation_call",
			"object": "response",
			"model":  "test-model",
			"output": []any{
				map[string]any{
					"id":             "ig_test",
					"type":           "image_generation_call",
					"status":         "completed",
					"background":     "opaque",
					"output_format":  "jpeg",
					"quality":        "low",
					"size":           "1024x1024",
					"result":         "/9j/4AAQSkZJRgABAQAAAQABAAD...",
					"revised_prompt": "A tiny orange cat curled up in a teacup.",
					"action":         "generate",
				},
			},
			"output_text": "",
		},
	})

	require.Equal(t, "response.completed", eventType)
	require.Len(t, before, 6)
	require.Equal(t, "response.created", before[0].eventType)
	require.Equal(t, "response.output_item.added", before[1].eventType)
	require.Equal(t, "response.image_generation_call.in_progress", before[2].eventType)
	require.Equal(t, "response.image_generation_call.generating", before[3].eventType)
	require.Equal(t, "response.image_generation_call.completed", before[4].eventType)
	require.Equal(t, "response.output_item.done", before[5].eventType)

	addedItem, ok := before[1].payload["item"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "image_generation_call", addedItem["type"])
	require.Equal(t, "ig_test", asString(addedItem["id"]))
	require.Equal(t, "in_progress", asString(addedItem["status"]))
	_, hasBackground := addedItem["background"]
	require.False(t, hasBackground)
	_, hasOutputFormat := addedItem["output_format"]
	require.False(t, hasOutputFormat)
	_, hasQuality := addedItem["quality"]
	require.False(t, hasQuality)
	_, hasSize := addedItem["size"]
	require.False(t, hasSize)
	_, hasResult := addedItem["result"]
	require.False(t, hasResult)
	_, hasRevisedPrompt := addedItem["revised_prompt"]
	require.False(t, hasRevisedPrompt)
	_, hasAction := addedItem["action"]
	require.False(t, hasAction)

	doneItem, ok := before[5].payload["item"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "completed", asString(doneItem["status"]))
	require.Equal(t, "/9j/4AAQSkZJRgABAQAAAQABAAD...", asString(doneItem["result"]))
	require.Equal(t, "A tiny orange cat curled up in a teacup.", asString(doneItem["revised_prompt"]))
	require.Equal(t, "generate", asString(doneItem["action"]))

	completedResponse, ok := payload["response"].(map[string]any)
	require.True(t, ok)
	output, ok := completedResponse["output"].([]any)
	require.True(t, ok)
	require.Len(t, output, 1)
	finalItem, ok := output[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "ig_test", asString(finalItem["id"]))
}

func TestNormalizeCompletedToolCallEventSynthesizesMCPApprovalRequestGenericReplay(t *testing.T) {
	proxy := newResponseStreamEventProxy(context.Background(), nil, customToolTransportPlan{}, nil)

	before, eventType, payload := proxy.normalizeCompletedToolCallEvent("response.completed", map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id":     "resp_proxy_mcp_approval_request",
			"object": "response",
			"model":  "test-model",
			"output": []any{
				map[string]any{
					"id":           "mcpr_test",
					"type":         "mcp_approval_request",
					"arguments":    "{\"diceRollExpression\":\"2d4 + 1\"}",
					"name":         "roll",
					"server_label": "dmcp",
				},
			},
			"output_text": "",
		},
	})

	require.Equal(t, "response.completed", eventType)
	require.Len(t, before, 3)
	require.Equal(t, "response.created", before[0].eventType)
	require.Equal(t, "response.output_item.added", before[1].eventType)
	require.Equal(t, "response.output_item.done", before[2].eventType)

	addedItem, ok := before[1].payload["item"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "mcp_approval_request", addedItem["type"])
	require.Equal(t, "mcpr_test", asString(addedItem["id"]))
	require.Equal(t, "{\"diceRollExpression\":\"2d4 + 1\"}", asString(addedItem["arguments"]))
	_, hasStatus := addedItem["status"]
	require.False(t, hasStatus)

	doneItem, ok := before[2].payload["item"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "mcp_approval_request", doneItem["type"])
	require.Equal(t, "{\"diceRollExpression\":\"2d4 + 1\"}", asString(doneItem["arguments"]))
	_, hasStatus = doneItem["status"]
	require.False(t, hasStatus)

	completedResponse, ok := payload["response"].(map[string]any)
	require.True(t, ok)
	output, ok := completedResponse["output"].([]any)
	require.True(t, ok)
	require.Len(t, output, 1)
	finalItem, ok := output[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "mcpr_test", asString(finalItem["id"]))
}

func TestNormalizeCompletedToolCallEventSynthesizesMCPListToolsGenericReplay(t *testing.T) {
	proxy := newResponseStreamEventProxy(context.Background(), nil, customToolTransportPlan{}, nil)

	before, eventType, payload := proxy.normalizeCompletedToolCallEvent("response.completed", map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id":     "resp_proxy_mcp_list_tools",
			"object": "response",
			"model":  "test-model",
			"output": []any{
				map[string]any{
					"id":           "mcpl_test",
					"type":         "mcp_list_tools",
					"server_label": "dmcp",
					"tools": []any{
						map[string]any{
							"name":        "roll",
							"description": "Given a string of text describing a dice roll...",
							"annotations": nil,
							"input_schema": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"diceRollExpression": map[string]any{
										"type": "string",
									},
								},
								"required":             []any{"diceRollExpression"},
								"additionalProperties": false,
							},
						},
					},
				},
			},
			"output_text": "",
		},
	})

	require.Equal(t, "response.completed", eventType)
	require.Len(t, before, 3)
	require.Equal(t, "response.created", before[0].eventType)
	require.Equal(t, "response.output_item.added", before[1].eventType)
	require.Equal(t, "response.output_item.done", before[2].eventType)

	addedItem, ok := before[1].payload["item"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "mcp_list_tools", addedItem["type"])
	require.Equal(t, "mcpl_test", asString(addedItem["id"]))
	require.Equal(t, "dmcp", asString(addedItem["server_label"]))
	tools, ok := addedItem["tools"].([]any)
	require.True(t, ok)
	require.Len(t, tools, 1)
	_, hasStatus := addedItem["status"]
	require.False(t, hasStatus)

	doneItem, ok := before[2].payload["item"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "mcp_list_tools", doneItem["type"])
	doneTools, ok := doneItem["tools"].([]any)
	require.True(t, ok)
	require.Len(t, doneTools, 1)
	_, hasStatus = doneItem["status"]
	require.False(t, hasStatus)

	completedResponse, ok := payload["response"].(map[string]any)
	require.True(t, ok)
	output, ok := completedResponse["output"].([]any)
	require.True(t, ok)
	require.Len(t, output, 1)
	finalItem, ok := output[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "mcpl_test", asString(finalItem["id"]))
}
