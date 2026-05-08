package httpapi

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"llama_shim/internal/config"
)

func TestClassifyResponseToolsDispositions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		body        string
		wantFamily  string
		wantType    string
		wantDisp    string
		wantClass   string
		wantBackend string
	}{
		{
			name: "function uses chat projection",
			body: `{
				"model":"test-model",
				"input":"call a tool",
				"tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}]
			}`,
			wantFamily:  "function",
			wantType:    "function",
			wantDisp:    responseToolDispositionChatProjection,
			wantClass:   "chat_projection",
			wantBackend: "chat_completions_tool_loop",
		},
		{
			name: "constrained custom uses repair disposition",
			body: `{
				"model":"test-model",
				"input":"call a tool",
				"tools":[{"type":"custom","name":"emit","format":{"type":"grammar","syntax":"regex","definition":"[a-z]+"}}]
			}`,
			wantFamily:  "custom",
			wantType:    "custom",
			wantDisp:    responseToolDispositionFunctionRepair,
			wantClass:   "repair_or_validate",
			wantBackend: "chat_completions_tool_loop",
		},
		{
			name: "mcp server_url is local execute",
			body: `{
				"model":"test-model",
				"input":"roll dice",
				"tools":[{"type":"mcp","server_label":"dice","server_url":"https://example.com/mcp"}]
			}`,
			wantFamily:  "mcp_server_url",
			wantType:    "mcp",
			wantDisp:    responseToolDispositionLocalExecute,
			wantClass:   "local_subset",
			wantBackend: "remote_mcp_server_url",
		},
		{
			name: "file search is local execute",
			body: `{
				"model":"test-model",
				"input":"search docs",
				"tools":[{"type":"file_search","vector_store_ids":["vs_test"]}]
			}`,
			wantFamily: "file_search",
			wantType:   "file_search",
			wantDisp:   responseToolDispositionLocalExecute,
			wantClass:  "local_subset",
		},
		{
			name: "web search is local execute",
			body: `{
				"model":"test-model",
				"input":"search web",
				"tools":[{"type":"web_search"}]
			}`,
			wantFamily:  "web_search",
			wantType:    "web_search",
			wantDisp:    responseToolDispositionLocalExecute,
			wantClass:   "local_subset",
			wantBackend: "responses.web_search",
		},
		{
			name: "image generation is local execute",
			body: `{
				"model":"test-model",
				"input":"draw",
				"tools":[{"type":"image_generation"}]
			}`,
			wantFamily:  "image_generation",
			wantType:    "image_generation",
			wantDisp:    responseToolDispositionLocalExecute,
			wantClass:   "local_subset",
			wantBackend: "responses.image_generation",
		},
		{
			name: "computer is local execute",
			body: `{
				"model":"test-model",
				"input":"use computer",
				"tools":[{"type":"computer"}]
			}`,
			wantFamily:  "computer",
			wantType:    "computer",
			wantDisp:    responseToolDispositionLocalExecute,
			wantClass:   "local_subset",
			wantBackend: "responses.computer",
		},
		{
			name: "code interpreter is local execute",
			body: `{
				"model":"test-model",
				"input":"run python",
				"tools":[{"type":"code_interpreter","container":{"type":"auto"}}]
			}`,
			wantFamily:  "code_interpreter",
			wantType:    "code_interpreter",
			wantDisp:    responseToolDispositionLocalExecute,
			wantClass:   "local_subset",
			wantBackend: "responses.code_interpreter",
		},
		{
			name: "shell is local execute through tool loop",
			body: `{
				"model":"test-model",
				"input":"list files",
				"tools":[{"type":"shell","environment":{"type":"local"}}]
			}`,
			wantFamily:  "shell",
			wantType:    "shell",
			wantDisp:    responseToolDispositionLocalExecute,
			wantClass:   "local_subset",
			wantBackend: "chat_completions_tool_loop",
		},
		{
			name: "apply_patch is local execute through tool loop",
			body: `{
				"model":"test-model",
				"input":"patch file",
				"tools":[{"type":"apply_patch"}]
			}`,
			wantFamily:  "apply_patch",
			wantType:    "apply_patch",
			wantDisp:    responseToolDispositionLocalExecute,
			wantClass:   "local_subset",
			wantBackend: "chat_completions_tool_loop",
		},
		{
			name: "mcp connector stays proxy only",
			body: `{
				"model":"test-model",
				"input":"read mail",
				"tools":[{"type":"mcp","server_label":"gmail","connector_id":"connector_gmail"}]
			}`,
			wantFamily: "mcp_connector_id",
			wantType:   "mcp",
			wantDisp:   responseToolDispositionProxyOnly,
			wantClass:  "proxy_only",
		},
		{
			name: "client tool search stays client round trip",
			body: `{
				"model":"test-model",
				"input":"find a tool",
				"tools":[{"type":"tool_search","execution":"client"}]
			}`,
			wantFamily: "tool_search",
			wantType:   "tool_search",
			wantDisp:   responseToolDispositionClientRoundTrip,
			wantClass:  "proxy_only",
		},
		{
			name: "unknown tools are upstream passthrough candidates",
			body: `{
				"model":"test-model",
				"input":"use future tool",
				"tools":[{"type":"future_tool"}]
			}`,
			wantFamily: "unknown",
			wantType:   "future_tool",
			wantDisp:   responseToolDispositionUpstreamPassthrough,
			wantClass:  "unsupported",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			classifications := classifyResponseTools(responseToolClassifierConfig{
				RawFields: mustResponseToolClassifierRawFields(t, tt.body),
			})

			require.Len(t, classifications, 1)
			got := classifications[0]
			require.Equal(t, tt.wantFamily, got.Family)
			require.Equal(t, tt.wantType, got.Type)
			require.Equal(t, tt.wantDisp, got.Disposition)
			require.Equal(t, tt.wantClass, got.CapabilityClass)
			require.Equal(t, tt.wantBackend, got.Backend)
		})
	}
}

func TestResponseToolClassifierLocalOnlyValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		body         string
		mode         string
		wantErr      bool
		wantContains []string
	}{
		{
			name: "proxy-only mcp connector rejects in local_only",
			body: `{
				"model":"test-model",
				"input":"read mail",
				"tools":[{"type":"mcp","server_label":"gmail","connector_id":"connector_gmail"}]
			}`,
			mode:         config.ResponsesModeLocalOnly,
			wantErr:      true,
			wantContains: []string{"mcp.connector_id", responseToolDispositionProxyOnly, "local_only"},
		},
		{
			name: "proxy-only mcp connector does not reject in prefer_upstream",
			body: `{
				"model":"test-model",
				"input":"read mail",
				"tools":[{"type":"mcp","server_label":"gmail","connector_id":"connector_gmail"}]
			}`,
			mode: config.ResponsesModePreferUpstream,
		},
		{
			name: "client tool_search rejects in local_only",
			body: `{
				"model":"test-model",
				"input":"find a tool",
				"tools":[{"type":"tool_search","execution":"client"}]
			}`,
			mode:         config.ResponsesModeLocalOnly,
			wantErr:      true,
			wantContains: []string{"tool_search", responseToolDispositionClientRoundTrip, "client round-trip"},
		},
		{
			name: "unknown tool rejects in local_only",
			body: `{
				"model":"test-model",
				"input":"use future tool",
				"tools":[{"type":"future_tool"}]
			}`,
			mode:         config.ResponsesModeLocalOnly,
			wantErr:      true,
			wantContains: []string{"future_tool", responseToolDispositionUpstreamPassthrough, "not implemented"},
		},
		{
			name: "local function tools pass local_only classifier",
			body: `{
				"model":"test-model",
				"input":"call a tool",
				"tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}]
			}`,
			mode: config.ResponsesModeLocalOnly,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			classifications := classifyResponseTools(responseToolClassifierConfig{
				RawFields: mustResponseToolClassifierRawFields(t, tt.body),
			})
			err := classifications.validateForResponsesMode(tt.mode)
			if !tt.wantErr {
				require.NoError(t, err)
				return
			}

			require.Error(t, err)
			for _, part := range tt.wantContains {
				require.Truef(t, strings.Contains(err.Error(), part), "expected %q to contain %q", err.Error(), part)
			}
		})
	}
}

func mustResponseToolClassifierRawFields(t *testing.T, body string) map[string]json.RawMessage {
	t.Helper()

	var rawFields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(body), &rawFields))
	return rawFields
}
