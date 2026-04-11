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
