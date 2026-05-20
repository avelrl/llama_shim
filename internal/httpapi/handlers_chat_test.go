package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"llama_shim/internal/storage/sqlite"
)

func TestSanitizeChatCompletionJSONBodyStripsNonOpenAIFields(t *testing.T) {
	body := []byte(`{
		"id":"chatcmpl_test",
		"provider_specific_fields":{"trace_id":"abc"},
		"choices":[
			{
				"index":0,
				"message":{
					"role":"assistant",
					"content":"OK",
					"reasoning_content":"hidden",
					"provider_specific_fields":{"raw":true}
				}
			}
		]
	}`)

	sanitized, err := sanitizeChatCompletionJSONBody(body)
	require.NoError(t, err)
	require.JSONEq(t, `{
		"id":"chatcmpl_test",
		"choices":[
			{
				"index":0,
				"message":{
					"role":"assistant",
					"content":"OK"
				}
			}
		]
	}`, string(sanitized))
}

func TestSanitizeChatCompletionSSELineStripsNonOpenAIFields(t *testing.T) {
	line := "data: {\"choices\":[{\"delta\":{\"content\":\"OK\",\"reasoning_content\":\"hidden\"},\"provider_specific_fields\":{\"trace\":true}}]}\n"

	sanitized, err := sanitizeChatCompletionSSELine(line)
	require.NoError(t, err)
	require.Equal(t, "data: {\"choices\":[{\"delta\":{\"content\":\"OK\"}}]}\n", sanitized)
}

func TestSanitizeChatCompletionSSELineSuppressesReasoningOnlyDelta(t *testing.T) {
	line := "data: {\"id\":\"chatcmpl_test\",\"choices\":[{\"index\":0,\"delta\":{\"content\":null,\"reasoning_content\":\"hidden\"}}]}\n"

	sanitized, err := sanitizeChatCompletionSSELine(line)
	require.NoError(t, err)
	require.Empty(t, sanitized)
}

func TestSanitizeChatCompletionSSELineSuppressesNoOpDelta(t *testing.T) {
	line := "data: {\"id\":\"chatcmpl_test\",\"choices\":[{\"index\":0,\"delta\":{},\"stop_reason\":200012,\"token_ids\":[1,2]}]}\n"

	sanitized, err := sanitizeChatCompletionSSELine(line)
	require.NoError(t, err)
	require.Empty(t, sanitized)
}

func TestSanitizeChatCompletionSSELineKeepsTerminalAndUsefulDeltas(t *testing.T) {
	tests := []string{
		"data: {\"choices\":[{\"delta\":{\"content\":\"OK\",\"reasoning_content\":\"hidden\"}}]}\n",
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\"}]}}]}\n",
		"data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"\"}}]}\n",
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n",
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}\n",
		"data: [DONE]\n",
	}

	for _, line := range tests {
		sanitized, err := sanitizeChatCompletionSSELine(line)
		require.NoError(t, err)
		require.NotEmpty(t, sanitized)
	}
}

func TestSanitizeChatCompletionJSONToWriterStripsNestedNonOpenAIFields(t *testing.T) {
	body := `{"id":"chatcmpl_ok","provider_specific_fields":{"trace":true},"choices":[{"message":{"content":"OK","reasoning_content":"hidden","tool_calls":[{"function":{"arguments":"{}","provider_specific_fields":{"trace":true}}}]}}]}`
	var out bytes.Buffer

	err := sanitizeChatCompletionJSONToWriter(&out, strings.NewReader(body))
	require.NoError(t, err)
	require.JSONEq(t, `{"id":"chatcmpl_ok","choices":[{"message":{"content":"OK","tool_calls":[{"function":{"arguments":"{}"}}]}}]}`, out.String())
}

func TestSanitizeChatCompletionJSONBodyWithStructuredProfileUnwrapsMarkdownFence(t *testing.T) {
	body := []byte("{\n" +
		"  \"id\":\"chatcmpl_structured\",\n" +
		"  \"choices\":[\n" +
		"    {\n" +
		"      \"index\":0,\n" +
		"      \"message\":{\n" +
		"        \"role\":\"assistant\",\n" +
		"        \"content\":\"```json\\n{\\n  \\\"status\\\": \\\"ok\\\",\\n  \\\"value\\\": 42\\n}\\n```\"\n" +
		"      },\n" +
		"      \"finish_reason\":\"stop\"\n" +
		"    }\n" +
		"  ]\n" +
		"}")

	sanitized, err := sanitizeChatCompletionJSONBodyWithProfile(body, chatCompletionSanitizationProfile{NormalizeStructuredJSON: true})
	require.NoError(t, err)
	require.JSONEq(t, `{
		"id":"chatcmpl_structured",
		"choices":[
			{
				"index":0,
				"message":{
					"role":"assistant",
					"content":"{\n  \"status\": \"ok\",\n  \"value\": 42\n}"
				},
				"finish_reason":"stop"
			}
		]
	}`, string(sanitized))
}

func TestSanitizeChatCompletionSSELineWithStructuredProfileUnwrapsMarkdownFenceInDelta(t *testing.T) {
	line := "data: {\"choices\":[{\"delta\":{\"content\":\"```json\\n{\\n  \\\"status\\\": \\\"ok\\\",\\n  \\\"value\\\": 42\\n}\\n```\"}}]}\n"

	sanitized, err := sanitizeChatCompletionSSELineWithProfile(line, chatCompletionSanitizationProfile{NormalizeStructuredJSON: true})
	require.NoError(t, err)
	require.Equal(t, "data: {\"choices\":[{\"delta\":{\"content\":\"{\\n  \\\"status\\\": \\\"ok\\\",\\n  \\\"value\\\": 42\\n}\"}}]}\n", sanitized)
}

func TestSanitizeChatCompletionStructuredOutputPreservesToolCalls(t *testing.T) {
	profile := chatCompletionSanitizationProfile{NormalizeStructuredJSON: true}
	body := []byte("{\n" +
		"  \"id\":\"chatcmpl_tool_structured\",\n" +
		"  \"choices\":[\n" +
		"    {\n" +
		"      \"index\":0,\n" +
		"      \"message\":{\n" +
		"        \"role\":\"assistant\",\n" +
		"        \"content\":\"```json\\n{\\\"status\\\":\\\"ok\\\",\\\"value\\\":42}\\n```\",\n" +
		"        \"tool_calls\":[\n" +
		"          {\n" +
		"            \"id\":\"call_1\",\n" +
		"            \"type\":\"function\",\n" +
		"            \"function\":{\n" +
		"              \"name\":\"write_file\",\n" +
		"              \"arguments\":\"```json\\n{\\\"path\\\":\\\"result.json\\\",\\\"content\\\":\\\"ok\\\"}\\n```\"\n" +
		"            }\n" +
		"          }\n" +
		"        ]\n" +
		"      },\n" +
		"      \"finish_reason\":\"tool_calls\"\n" +
		"    }\n" +
		"  ]\n" +
		"}")

	sanitized, err := sanitizeChatCompletionJSONBodyWithProfile(body, profile)
	require.NoError(t, err)
	require.JSONEq(t, "{"+
		`"id":"chatcmpl_tool_structured",`+
		`"choices":[{`+
		`"index":0,`+
		`"message":{`+
		`"role":"assistant",`+
		`"content":"{\"status\":\"ok\",\"value\":42}",`+
		`"tool_calls":[{`+
		`"id":"call_1",`+
		`"type":"function",`+
		`"function":{`+
		`"name":"write_file",`+
		`"arguments":"`+"```json\\n{\\\"path\\\":\\\"result.json\\\",\\\"content\\\":\\\"ok\\\"}\\n```"+`"`+
		`}`+
		`}]`+
		`},`+
		`"finish_reason":"tool_calls"`+
		`}]`+
		"}", string(sanitized))
}

func TestSanitizeChatCompletionStructuredStreamPreservesToolCalls(t *testing.T) {
	line := "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"```json\\n{\\\"status\\\":\\\"ok\\\"}\\n```\",\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"write_file\",\"arguments\":\"```json\\n{\\\"path\\\":\\\"result.json\\\"}\\n```\"}}]}}]}\n"

	sanitized, err := sanitizeChatCompletionSSELineWithProfile(line, chatCompletionSanitizationProfile{NormalizeStructuredJSON: true})
	require.NoError(t, err)
	payload := decodeChatCompletionSSEPayload(t, sanitized)
	delta := payload["choices"].([]any)[0].(map[string]any)["delta"].(map[string]any)
	require.Equal(t, "{\"status\":\"ok\"}", delta["content"])
	toolCalls := delta["tool_calls"].([]any)
	function := toolCalls[0].(map[string]any)["function"].(map[string]any)
	require.Equal(t, "write_file", function["name"])
	require.Equal(t, "```json\n{\"path\":\"result.json\"}\n```", function["arguments"])
}

func TestChatCompletionStreamSanitizerConvertsPseudoFunctionToolMarkup(t *testing.T) {
	sanitizer := newChatCompletionStreamSanitizer(chatCompletionSanitizationProfile{RepairRawToolMarkup: true})
	line := "data: {\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"I'll inspect files.\\n\\n<function=bash>\\n<parameter=command>\\nfind . -name \\\"*.go\\\" -type f\\n</parameter>\\n<parameter=description>Find Go files</parameter>\\n</function>\\n</tool_call>\"}}]}\n"

	sanitized, err := sanitizer.SanitizeLine(line)
	require.NoError(t, err)

	payload := decodeChatCompletionSSEPayload(t, sanitized)
	choice := payload["choices"].([]any)[0].(map[string]any)
	delta := choice["delta"].(map[string]any)
	require.NotContains(t, delta, "content")
	toolCalls := delta["tool_calls"].([]any)
	require.Len(t, toolCalls, 1)
	toolCall := toolCalls[0].(map[string]any)
	require.Equal(t, "function", toolCall["type"])
	function := toolCall["function"].(map[string]any)
	require.Equal(t, "bash", function["name"])
	require.JSONEq(t, `{"command":"find . -name \"*.go\" -type f","description":"Find Go files"}`, function["arguments"].(string))

	done, err := sanitizer.SanitizeLine("data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n")
	require.NoError(t, err)
	donePayload := decodeChatCompletionSSEPayload(t, done)
	doneChoice := donePayload["choices"].([]any)[0].(map[string]any)
	require.Equal(t, "tool_calls", doneChoice["finish_reason"])
}

func TestChatCompletionStreamSanitizerConvertsChatCMPLToolMarkup(t *testing.T) {
	sanitizer := newChatCompletionStreamSanitizer(chatCompletionSanitizationProfile{RepairRawToolMarkup: true})
	line := "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"<chatcmpl-tool>{\\\"name\\\":\\\"exec_command\\\",\\\"arguments\\\":{\\\"cmd\\\":\\\"cat README.md\\\"}}</chatcmpl-tool>\"}}]}\n"

	sanitized, err := sanitizer.SanitizeLine(line)
	require.NoError(t, err)

	payload := decodeChatCompletionSSEPayload(t, sanitized)
	delta := payload["choices"].([]any)[0].(map[string]any)["delta"].(map[string]any)
	require.NotContains(t, delta, "content")
	toolCalls := delta["tool_calls"].([]any)
	require.Len(t, toolCalls, 1)
	function := toolCalls[0].(map[string]any)["function"].(map[string]any)
	require.Equal(t, "exec_command", function["name"])
	require.JSONEq(t, `{"cmd":"cat README.md"}`, function["arguments"].(string))
}

func TestChatCompletionStreamSanitizerLeavesAmbiguousToolMarkupBufferedUntilTerminal(t *testing.T) {
	sanitizer := newChatCompletionStreamSanitizer(chatCompletionSanitizationProfile{RepairRawToolMarkup: true})
	first, err := sanitizer.SanitizeLine("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"<tools>{\\\"call\\\":\\\"cat\\\"}</tools>\"}}]}\n")
	require.NoError(t, err)
	require.Empty(t, first)

	done, err := sanitizer.SanitizeLine("data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n")
	require.NoError(t, err)
	payload := decodeChatCompletionSSEPayload(t, done)
	choice := payload["choices"].([]any)[0].(map[string]any)
	delta := choice["delta"].(map[string]any)
	require.Equal(t, "<tools>{\"call\":\"cat\"}</tools>", delta["content"])
	require.Equal(t, "stop", choice["finish_reason"])
}

func TestChatCompletionStreamSanitizerRecordsPseudoFunctionToolMarkupTrace(t *testing.T) {
	store := NewDebugTraceStore(2)
	req, err := http.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	require.NoError(t, err)
	ctx := RequestContextWithID(context.Background(), "req_chat_stream_tool")
	ctx = store.Begin(ctx, req, time.Unix(4, 0))

	sanitizer := newChatCompletionStreamSanitizer(chatCompletionSanitizationProfile{RepairRawToolMarkup: true})
	sanitizer.ctx = ctx
	_, err = sanitizer.SanitizeLine("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"<function=bash><parameter=command>pwd</parameter></function>\"}}]}\n")
	require.NoError(t, err)

	trace, ok := store.Get("req_chat_stream_tool")
	require.True(t, ok)
	require.Contains(t, trace.Transforms, DebugTraceTransform{
		Stage:    "chat_compatibility",
		Class:    "chat_completions",
		Hook:     ChatCompatibilityTransformStreamPseudoToolConversion,
		Fields:   []string{"choices.delta.content", "choices.delta.tool_calls", "choices.finish_reason"},
		Decision: "applied",
	})
}

func TestRecordChatCompatibilityProfileRecordsEnabledTransforms(t *testing.T) {
	store := NewDebugTraceStore(2)
	req, err := http.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	require.NoError(t, err)
	ctx := RequestContextWithID(context.Background(), "req_chat_profile")
	ctx = store.Begin(ctx, req, time.Unix(5, 0))

	recordChatCompatibilityProfile(ctx,
		chatCompletionSanitizationProfile{NormalizeStructuredJSON: true, RepairRawToolMarkup: true},
		chatToolCompatRequest{RepairRawToolMarkup: true},
		nil,
	)

	trace, ok := store.Get("req_chat_profile")
	require.True(t, ok)
	require.Contains(t, trace.Transforms, DebugTraceTransform{
		Stage:    "chat_compatibility",
		Class:    "chat_completions",
		Hook:     ChatCompatibilityTransformStructuredJSONNormalize,
		Fields:   []string{"choices.delta.content", "choices.message.content"},
		Decision: "enabled",
	})
	require.Contains(t, trace.Transforms, DebugTraceTransform{
		Stage:    "chat_compatibility",
		Class:    "chat_completions",
		Hook:     ChatCompatibilityTransformRawToolMarkupRepair,
		Fields:   []string{"choices.delta.content", "choices.message.content", "messages"},
		Decision: "enabled",
	})
}

func TestRecordChatToolCompatibilityRetryTransforms(t *testing.T) {
	store := NewDebugTraceStore(2)
	req, err := http.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	require.NoError(t, err)
	ctx := RequestContextWithID(context.Background(), "req_chat_retry")
	ctx = store.Begin(ctx, req, time.Unix(6, 0))

	recordChatToolCompatibilityRetryTransforms(ctx, chatToolCompatRequest{
		Contract: toolChoiceContract{Mode: toolChoiceContractRequiredAny},
	}, true)

	trace, ok := store.Get("req_chat_retry")
	require.True(t, ok)
	require.Contains(t, trace.Transforms, DebugTraceTransform{
		Stage:    "chat_compatibility",
		Class:    "chat_completions",
		Hook:     ChatCompatibilityTransformToolChoiceRetry,
		Fields:   []string{"tool_choice"},
		Decision: "applied",
	})
	require.Contains(t, trace.Transforms, DebugTraceTransform{
		Stage:    "chat_compatibility",
		Class:    "chat_completions",
		Hook:     ChatCompatibilityTransformRawToolMarkupRepair,
		Fields:   []string{"choices.message.content", "messages"},
		Decision: "applied",
	})
	require.Contains(t, trace.Transforms, DebugTraceTransform{
		Stage:    "chat_compatibility",
		Class:    "chat_completions",
		Hook:     ChatCompatibilityTransformMinimumRetryTokens,
		Fields:   []string{"max_completion_tokens", "max_tokens"},
		Decision: "applied",
	})
}

func TestChatCompletionStreamSanitizerBuffersSplitPseudoFunctionToolMarkup(t *testing.T) {
	sanitizer := newChatCompletionStreamSanitizer(chatCompletionSanitizationProfile{RepairRawToolMarkup: true})

	first, err := sanitizer.SanitizeLine("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"<function=bash><parameter=command>\"}}]}\n")
	require.NoError(t, err)
	require.Empty(t, first)

	second, err := sanitizer.SanitizeLine("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"pwd</parameter></function>\"}}]}\n")
	require.NoError(t, err)
	payload := decodeChatCompletionSSEPayload(t, second)
	delta := payload["choices"].([]any)[0].(map[string]any)["delta"].(map[string]any)
	toolCalls := delta["tool_calls"].([]any)
	function := toolCalls[0].(map[string]any)["function"].(map[string]any)
	require.Equal(t, "bash", function["name"])
	require.JSONEq(t, `{"command":"pwd"}`, function["arguments"].(string))
}

func TestChatCompletionStreamSanitizerLeavesPseudoFunctionTextWithoutToolsProfile(t *testing.T) {
	line := "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"<function=bash><parameter=command>pwd</parameter></function>\"}}]}\n"

	sanitized, err := sanitizeChatCompletionSSELine(line)
	require.NoError(t, err)
	payload := decodeChatCompletionSSEPayload(t, sanitized)
	choice := payload["choices"].([]any)[0].(map[string]any)
	delta := choice["delta"].(map[string]any)
	require.Equal(t, "<function=bash><parameter=command>pwd</parameter></function>", delta["content"])
	require.NotContains(t, delta, "tool_calls")
}

func TestValidateChatToolCallContractAcceptsNamedFunctionChoice(t *testing.T) {
	err := validateChatToolCallContract([]byte(`{
		"choices":[{
			"message":{
				"role":"assistant",
				"tool_calls":[{
					"type":"function",
					"function":{"name":"add","arguments":"{\"a\":1,\"b\":2}"}
				}]
			},
			"finish_reason":"tool_calls"
		}]
	}`), toolChoiceContract{Mode: toolChoiceContractRequiredNamedFunction, Name: "add"}, true)

	require.NoError(t, err)
}

func TestValidateChatToolCallContractRejectsTruncatedArguments(t *testing.T) {
	err := validateChatToolCallContract([]byte(`{
		"choices":[{
			"message":{
				"role":"assistant",
				"tool_calls":[{
					"type":"function",
					"function":{"name":"add","arguments":"{\"a\":"}
				}]
			},
			"finish_reason":"length"
		}]
	}`), toolChoiceContract{Mode: toolChoiceContractRequiredAny}, true)

	var incompatErr *toolChoiceIncompatibleBackendError
	require.ErrorAs(t, err, &incompatErr)
	require.Contains(t, incompatErr.Error(), "truncated tool call arguments")
}

func TestValidateChatToolCallContractRejectsRawToolMarkupContent(t *testing.T) {
	err := validateChatToolCallContract([]byte(`{
		"choices":[{
			"message":{
				"role":"assistant",
				"content":"I'll use a tool.\n\n<function=list_files>\n</function>\n</tool_call>"
			},
			"finish_reason":"stop"
		}]
	}`), toolChoiceContract{}, true)

	var markupErr *rawToolCallMarkupError
	require.ErrorAs(t, err, &markupErr)
	require.Contains(t, markupErr.Content, "<function=list_files>")
}

func decodeChatCompletionSSEPayload(t *testing.T, line string) map[string]any {
	t.Helper()
	require.NotEmpty(t, line)
	require.True(t, strings.HasPrefix(line, "data: "))
	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &payload))
	return payload
}

func TestRewriteChatToolCallRetryBodyAddsRawMarkupRepairPrompt(t *testing.T) {
	rawBody := []byte(`{
		"model":"test-model",
		"messages":[{"role":"user","content":"Use the tool."}],
		"tools":[{"type":"function","function":{"name":"list_files","parameters":{"type":"object"}}}],
		"tool_choice":"auto"
	}`)

	rewritten, err := rewriteChatToolCallRetryBody(rawBody, toolChoiceContract{}, true)
	require.NoError(t, err)

	var payload struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		MaxTokens int64 `json:"max_tokens"`
	}
	require.NoError(t, json.Unmarshal(rewritten, &payload))
	require.Len(t, payload.Messages, 2)
	require.Equal(t, "system", payload.Messages[0].Role)
	require.Contains(t, payload.Messages[0].Content, "printed internal tool-call markup")
	require.Equal(t, "user", payload.Messages[1].Role)
	require.Equal(t, int64(256), payload.MaxTokens)
}

func TestLimitedBodyCaptureBufferMarksOverflowWithoutFailingWrites(t *testing.T) {
	capture := newLimitedBodyCaptureBuffer(4)

	n, err := capture.Write([]byte("abcdef"))
	require.NoError(t, err)
	require.Equal(t, 6, n)
	require.True(t, capture.overflowed)
	require.Equal(t, "abcd", string(capture.Bytes()))
}

func TestShadowStoreChatCompletionBestEffortIgnoresCanceledRequestContext(t *testing.T) {
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "shim.db"))
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = store.Close()
	})

	handler := newProxyHandler(nil, nil, store, ServiceLimits{
		ChatCompletionsShadowStoreTimeout: time.Second,
	}, false, nil)
	requestCtx, cancel := context.WithCancel(context.Background())
	cancel()

	err = handler.shadowStoreChatCompletionBestEffort(requestCtx,
		[]byte(`{
			"model":"gpt-5.4",
			"store":true,
			"metadata":{"case":"client_cancel"},
			"messages":[{"role":"user","content":"Say OK"}]
		}`),
		[]byte(`{
			"id":"chatcmpl_context_cancel",
			"object":"chat.completion",
			"created":1777020000,
			"model":"gpt-5.4",
			"choices":[
				{
					"index":0,
					"message":{"role":"assistant","content":"OK"},
					"finish_reason":"stop",
					"logprobs":null
				}
			]
		}`),
	)
	require.NoError(t, err)

	stored, err := store.GetChatCompletion(context.Background(), "chatcmpl_context_cancel")
	require.NoError(t, err)
	require.Equal(t, "gpt-5.4", stored.Model)
	require.Equal(t, map[string]string{"case": "client_cancel"}, stored.Metadata)
	require.JSONEq(t, `{"model":"gpt-5.4","store":true,"metadata":{"case":"client_cancel"},"messages":[{"role":"user","content":"Say OK"}]}`, stored.RequestJSON)
	require.JSONEq(t, `{"id":"chatcmpl_context_cancel","object":"chat.completion","created":1777020000,"model":"gpt-5.4","metadata":{"case":"client_cancel"},"choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop","logprobs":null}]}`, stored.ResponseJSON)
}
