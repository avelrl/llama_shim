package httpapi

import (
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
