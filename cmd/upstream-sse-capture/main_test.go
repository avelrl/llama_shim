package main

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCloneHeadersRedactsSensitiveHeaders(t *testing.T) {
	headers := http.Header{
		"Content-Type":         []string{"text/event-stream; charset=utf-8"},
		"Openai-Processing-Ms": []string{"718"},
		"Set-Cookie":           []string{"secret-cookie"},
		"Openai-Project":       []string{"proj_secret"},
		"Openai-Organization":  []string{"org_secret"},
		"X-Request-Id":         []string{"req_secret"},
	}

	cloned := cloneHeaders(headers)

	require.Equal(t, []string{"text/event-stream; charset=utf-8"}, cloned["Content-Type"])
	require.Equal(t, []string{"718"}, cloned["Openai-Processing-Ms"])
	require.NotContains(t, cloned, "Set-Cookie")
	require.NotContains(t, cloned, "Openai-Project")
	require.NotContains(t, cloned, "Openai-Organization")
	require.NotContains(t, cloned, "X-Request-Id")
}

func TestExpandTemplateEnvReplacesPresentVariables(t *testing.T) {
	t.Setenv("OPENAI_VECTOR_STORE_ID", "vs_123")

	expanded, err := expandTemplateEnv([]byte(`{"vector_store_ids":["${OPENAI_VECTOR_STORE_ID}"]}`))
	require.NoError(t, err)
	require.JSONEq(t, `{"vector_store_ids":["vs_123"]}`, string(expanded))
}

func TestExpandTemplateEnvReturnsErrorForMissingVariables(t *testing.T) {
	expanded, err := expandTemplateEnv([]byte(`{"vector_store_ids":["${OPENAI_VECTOR_STORE_ID_MISSING_FOR_TEST}"]}`))
	require.Error(t, err)
	require.Nil(t, expanded)
	require.Contains(t, err.Error(), "OPENAI_VECTOR_STORE_ID_MISSING_FOR_TEST")
}
