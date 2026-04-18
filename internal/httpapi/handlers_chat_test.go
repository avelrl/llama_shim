package httpapi

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
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

func TestLimitedBodyCaptureBufferMarksOverflowWithoutFailingWrites(t *testing.T) {
	capture := newLimitedBodyCaptureBuffer(4)

	n, err := capture.Write([]byte("abcdef"))
	require.NoError(t, err)
	require.Equal(t, 6, n)
	require.True(t, capture.overflowed)
	require.Equal(t, "abcd", string(capture.Bytes()))
}
