package httpapi

import (
	"bytes"
	"context"
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
	}`), toolChoiceContract{Mode: toolChoiceContractRequiredNamedFunction, Name: "add"})

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
	}`), toolChoiceContract{Mode: toolChoiceContractRequiredAny})

	var incompatErr *toolChoiceIncompatibleBackendError
	require.ErrorAs(t, err, &incompatErr)
	require.Contains(t, incompatErr.Error(), "truncated tool call arguments")
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
