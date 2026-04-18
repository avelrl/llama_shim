package httpapi

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestChatCompletionStreamStoreCaptureRejectsOversizedAccumulation(t *testing.T) {
	capture := newChatCompletionStreamStoreCapture("req_test")
	capture.CaptureLine(`data: {"id":"chatcmpl_test","created":1712059200,"model":"test-model","choices":[{"index":0,"delta":{"content":"hello"}}]}`)

	large := strings.Repeat("a", maxChatCompletionStreamStoreCaptureBytes)
	capture.CaptureLine(fmt.Sprintf(`data: {"id":"chatcmpl_test","created":1712059200,"model":"test-model","choices":[{"index":0,"delta":{"content":%q}}]}`, large))
	capture.CaptureLine("data: [DONE]")

	_, err := capture.ReconstructedResponse([]byte(`{"model":"test-model"}`))
	require.Error(t, err)
	require.Contains(t, err.Error(), "exceeded")
}

func TestChatCompletionStreamStoreCaptureReconstructsWithinLimit(t *testing.T) {
	capture := newChatCompletionStreamStoreCapture("req_test")
	capture.CaptureLine(`data: {"id":"chatcmpl_test","created":1712059200,"model":"test-model","choices":[{"index":0,"delta":{"role":"assistant","content":"hello"}}]}`)
	capture.CaptureLine(`data: {"id":"chatcmpl_test","created":1712059200,"model":"test-model","choices":[{"index":0,"delta":{"function_call":{"name":"tool","arguments":"{\"k\":1}"}}}]}`)
	capture.CaptureLine("data: [DONE]")

	body, err := capture.ReconstructedResponse([]byte(`{"model":"test-model"}`))
	require.NoError(t, err)
	require.Contains(t, string(body), `"content":"hello"`)
	require.Contains(t, string(body), `"name":"tool"`)
}
