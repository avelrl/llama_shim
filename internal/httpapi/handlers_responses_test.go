package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"llama_shim/internal/config"
	"llama_shim/internal/domain"
	"llama_shim/internal/llama"
)

func TestBuildUpstreamInputItemsPreservesRawItems(t *testing.T) {
	items := []domain.Item{
		domain.NewInputTextMessage("system", "You are a test assistant."),
		domain.NewInputTextMessage("user", "Remember: code=777. Reply OK."),
		domain.NewInputTextMessage("user", "What is the code? Reply with just the number."),
	}

	got := buildUpstreamInputItems(items)

	require.Len(t, got, 3)

	first, err := domain.NewItem(got[0])
	require.NoError(t, err)
	second, err := domain.NewItem(got[1])
	require.NoError(t, err)
	third, err := domain.NewItem(got[2])
	require.NoError(t, err)

	require.Equal(t, "system", first.Role)
	require.Equal(t, "user", second.Role)
	require.Equal(t, "user", third.Role)
	require.Equal(t, "You are a test assistant.", domain.MessageText(first))
	require.Equal(t, "Remember: code=777. Reply OK.", domain.MessageText(second))
	require.Equal(t, "What is the code? Reply with just the number.", domain.MessageText(third))
}

func TestPrepareShadowStoreKeepsMixedInputItems(t *testing.T) {
	request := CreateResponseRequest{
		Model: "test-model",
		Input: json.RawMessage(`[
			{"type":"function_call","call_id":"call_1","name":"add","arguments":"{\"a\":1,\"b\":2}"},
			{"type":"function_call_output","call_id":"call_1","output":"{\"result\":3}"}
		]`),
	}

	prepared, input, ok := prepareShadowStore(context.Background(), nil, request, `{"model":"test-model"}`)

	require.True(t, ok)
	require.Equal(t, "test-model", input.Model)
	require.Len(t, prepared.NormalizedInput, 2)
	require.Equal(t, "function_call", prepared.NormalizedInput[0].Type)
	require.Equal(t, "function_call_output", prepared.NormalizedInput[1].Type)
}

func TestPrepareShadowStorePreservesStateFields(t *testing.T) {
	request := CreateResponseRequest{
		Model:              "test-model",
		PreviousResponseID: "resp_prev",
		Conversation:       "conv_1",
		Input:              json.RawMessage(`"hello"`),
	}

	_, input, ok := prepareShadowStore(context.Background(), nil, request, `{"model":"test-model"}`)

	require.True(t, ok)
	require.Equal(t, "resp_prev", input.PreviousResponseID)
	require.Equal(t, "conv_1", input.ConversationID)
}

func TestParseCreateResponseStreamOptionsRequiresStream(t *testing.T) {
	_, err := parseCreateResponseStreamOptions(nil, json.RawMessage(`{"include_obfuscation":false}`))

	var validationErr *domain.ValidationError
	require.ErrorAs(t, err, &validationErr)
	require.Equal(t, "stream_options", validationErr.Param)
}

func TestParseCreateResponseStreamOptionsRejectsUnsupportedField(t *testing.T) {
	stream := true
	_, err := parseCreateResponseStreamOptions(&stream, json.RawMessage(`{"include_usage":true}`))

	var validationErr *domain.ValidationError
	require.ErrorAs(t, err, &validationErr)
	require.Equal(t, "stream_options", validationErr.Param)
	require.Contains(t, validationErr.Message, "unsupported stream_options field")
}

func TestDecodeCreateResponseRequestBodyAcceptsContextManagementCompactionPolicy(t *testing.T) {
	request, rawFields, _, err := decodeCreateResponseRequestBody([]byte(`{
		"model":"test-model",
		"input":"hello",
		"context_management":[{"type":"compaction","compact_threshold":200000}]
	}`), false)
	require.NoError(t, err)
	require.JSONEq(t, `[{"type":"compaction","compact_threshold":200000}]`, string(request.ContextManagement))
	require.Contains(t, rawFields, "context_management")
}

func TestDecodeCreateResponseRequestBodyRejectsInvalidContextManagementShape(t *testing.T) {
	_, _, _, err := decodeCreateResponseRequestBody([]byte(`{
		"model":"test-model",
		"input":"hello",
		"context_management":{"type":"compaction","compact_threshold":200000}
	}`), false)

	var validationErr *domain.ValidationError
	require.ErrorAs(t, err, &validationErr)
	require.Equal(t, "context_management", validationErr.Param)
	require.Contains(t, validationErr.Message, "must be an array")
}

func TestDecodeCreateResponseRequestBodyRejectsInvalidContextManagementCompactionPolicy(t *testing.T) {
	_, _, _, err := decodeCreateResponseRequestBody([]byte(`{
		"model":"test-model",
		"input":"hello",
		"context_management":[{"type":"compaction","compact_threshold":0}]
	}`), false)

	var validationErr *domain.ValidationError
	require.ErrorAs(t, err, &validationErr)
	require.Equal(t, "context_management", validationErr.Param)
	require.Contains(t, validationErr.Message, "compact_threshold > 0")
}

func TestDecodeCreateResponseRequestBodyRejectsUnsupportedContextManagementType(t *testing.T) {
	_, _, _, err := decodeCreateResponseRequestBody([]byte(`{
		"model":"test-model",
		"input":"hello",
		"context_management":[{"type":"summary","compact_threshold":200000}]
	}`), false)

	var validationErr *domain.ValidationError
	require.ErrorAs(t, err, &validationErr)
	require.Equal(t, "context_management", validationErr.Param)
	require.Contains(t, validationErr.Message, `unsupported context_management type "summary"`)
}

func TestShouldFallbackLocalState(t *testing.T) {
	require.True(t, shouldFallbackLocalState(config.ResponsesModePreferUpstream, &llama.UpstreamError{
		StatusCode: 500,
		Message:    "backend failed",
	}))
	require.False(t, shouldFallbackLocalState(config.ResponsesModePreferLocal, &llama.UpstreamError{
		StatusCode: 500,
		Message:    "backend failed",
	}))
	require.True(t, shouldFallbackLocalState(config.ResponsesModePreferLocal, domain.ErrUnsupportedShape))
	require.False(t, shouldFallbackLocalState(config.ResponsesModeLocalOnly, domain.ErrUnsupportedShape))
	require.False(t, shouldFallbackLocalState(config.ResponsesModePreferLocal, domain.NewValidationError("input", "input is required")))
}

func TestSelectResponsesCreateRoute(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mode    string
		profile responsesCreateRouteInputs
		want    responsesCreateRoute
	}{
		{
			name: "prefer upstream proxies standalone hosted requests",
			mode: config.ResponsesModePreferUpstream,
			profile: responsesCreateRouteInputs{
				LocalWebSearch: true,
			},
			want: responsesCreateRouteProxy,
		},
		{
			name: "prefer local runs local file search",
			mode: config.ResponsesModePreferLocal,
			profile: responsesCreateRouteInputs{
				LocalFileSearch: true,
			},
			want: responsesCreateRouteLocalFileSearch,
		},
		{
			name: "local only routes file search parser errors into local parser",
			mode: config.ResponsesModeLocalOnly,
			profile: responsesCreateRouteInputs{
				LocalFileSearchRequested: true,
			},
			want: responsesCreateRouteLocalFileSearch,
		},
		{
			name: "prefer local runs local tool search",
			mode: config.ResponsesModePreferLocal,
			profile: responsesCreateRouteInputs{
				LocalToolSearch: true,
			},
			want: responsesCreateRouteLocalToolSearch,
		},
		{
			name: "prefer local runs local mcp subset",
			mode: config.ResponsesModePreferLocal,
			profile: responsesCreateRouteInputs{
				LocalMCP: true,
			},
			want: responsesCreateRouteLocalMCP,
		},
		{
			name: "local only returns explicit web search disabled route",
			mode: config.ResponsesModeLocalOnly,
			profile: responsesCreateRouteInputs{
				LocalWebSearchRequested: true,
			},
			want: responsesCreateRouteLocalWebSearchDisabled,
		},
		{
			name: "local only routes web search parser errors when runtime is enabled",
			mode: config.ResponsesModeLocalOnly,
			profile: responsesCreateRouteInputs{
				LocalWebSearchRequested:      true,
				LocalWebSearchRuntimeEnabled: true,
			},
			want: responsesCreateRouteLocalWebSearch,
		},
		{
			name: "local only returns explicit image generation disabled route",
			mode: config.ResponsesModeLocalOnly,
			profile: responsesCreateRouteInputs{
				LocalImageGenerationRequested: true,
			},
			want: responsesCreateRouteLocalImageGenerationDisabled,
		},
		{
			name: "local only routes image generation parser errors when runtime is enabled",
			mode: config.ResponsesModeLocalOnly,
			profile: responsesCreateRouteInputs{
				LocalImageGenerationRequested:      true,
				LocalImageGenerationRuntimeEnabled: true,
			},
			want: responsesCreateRouteLocalImageGeneration,
		},
		{
			name: "local only returns explicit computer disabled route",
			mode: config.ResponsesModeLocalOnly,
			profile: responsesCreateRouteInputs{
				LocalComputerRequested: true,
			},
			want: responsesCreateRouteLocalComputerDisabled,
		},
		{
			name: "local only routes computer parser errors when runtime is enabled",
			mode: config.ResponsesModeLocalOnly,
			profile: responsesCreateRouteInputs{
				LocalComputerRequested:      true,
				LocalComputerRuntimeEnabled: true,
			},
			want: responsesCreateRouteLocalComputer,
		},
		{
			name: "local only returns explicit code interpreter disabled route",
			mode: config.ResponsesModeLocalOnly,
			profile: responsesCreateRouteInputs{
				LocalCodeInterpreterRequested: true,
			},
			want: responsesCreateRouteLocalCodeInterpreterDisabled,
		},
		{
			name: "local only routes code interpreter parser errors when runtime is enabled",
			mode: config.ResponsesModeLocalOnly,
			profile: responsesCreateRouteInputs{
				LocalCodeInterpreterRequested:      true,
				LocalCodeInterpreterRuntimeEnabled: true,
			},
			want: responsesCreateRouteLocalCodeInterpreter,
		},
		{
			name: "local only routes unsupported hosted tool search requests into local parser",
			mode: config.ResponsesModeLocalOnly,
			profile: responsesCreateRouteInputs{
				LocalToolSearchRequested: true,
			},
			want: responsesCreateRouteLocalToolSearch,
		},
		{
			name: "local only routes connector mcp requests into local parser",
			mode: config.ResponsesModeLocalOnly,
			profile: responsesCreateRouteInputs{
				LocalMCPRequested: true,
			},
			want: responsesCreateRouteLocalMCP,
		},
		{
			name: "local only rejects unsupported local state fields",
			mode: config.ResponsesModeLocalOnly,
			profile: responsesCreateRouteInputs{
				HasLocalState: true,
			},
			want: responsesCreateRouteLocalOnlyUnsupported,
		},
		{
			name: "prefer local reuses local state via upstream when no local subset matches",
			mode: config.ResponsesModePreferLocal,
			profile: responsesCreateRouteInputs{
				HasLocalState: true,
			},
			want: responsesCreateRouteLocalStateViaUpstream,
		},
		{
			name: "local only rejects unsupported standalone requests",
			mode: config.ResponsesModeLocalOnly,
			want: responsesCreateRouteLocalOnlyUnsupported,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, selectResponsesCreateRoute(tt.mode, tt.profile))
		})
	}
}

func TestSupportsLocalShimStateAcceptsContextManagementCompactionPolicy(t *testing.T) {
	rawFields := map[string]json.RawMessage{
		"model":              json.RawMessage(`"test-model"`),
		"input":              json.RawMessage(`"hello"`),
		"context_management": json.RawMessage(`[{"type":"compaction","compact_threshold":200000}]`),
	}

	require.True(t, supportsLocalShimState(rawFields))
}

func TestBuildResponsesCreateRouteInputsSuppressesLocalToolRoutesWhenContextManagementRequested(t *testing.T) {
	inputs := buildResponsesCreateRouteInputs(
		false,
		map[string]json.RawMessage{
			"model":              json.RawMessage(`"test-model"`),
			"input":              json.RawMessage(`"hello"`),
			"context_management": json.RawMessage(`[{"type":"compaction","compact_threshold":200000}]`),
			"tools":              json.RawMessage(`[{"type":"web_search"}]`),
		},
		nil,
		nil,
		LocalComputerRuntimeConfig{},
		LocalCodeInterpreterRuntimeConfig{},
		false,
	)

	require.False(t, inputs.LocalWebSearchRequested)
	require.False(t, inputs.LocalWebSearch)
	require.False(t, inputs.LocalSupported)
}

func TestShouldRetryLocalStateWithDirectProxyBody(t *testing.T) {
	request := CreateResponseRequest{PreviousResponseID: "resp_prev"}

	require.True(t, shouldRetryLocalStateWithDirectProxyBody(http.StatusBadRequest, []byte(`{
		"error":{
			"type":"invalid_request_error",
			"message":"637 validation errors:\n  {'type': 'string_type', 'loc': ('body', 'input', 'str'), 'msg': 'Input should be a valid string'}"
		}
	}`), request))
	require.False(t, shouldRetryLocalStateWithDirectProxyBody(http.StatusBadRequest, []byte(`{
		"error":{"type":"invalid_request_error","message":"tool type custom not supported"}
	}`), request))
	require.False(t, shouldRetryLocalStateWithDirectProxyBody(http.StatusBadRequest, []byte(`{
		"error":{"type":"invalid_request_error","message":"Input should be a valid string"}
	}`), CreateResponseRequest{}))
}

func TestShouldRetryResponsesInputAsStringBody(t *testing.T) {
	requestBody := []byte(`{"input":[{"type":"message","role":"user","content":"backend rejects structured input arrays"}]}`)

	require.True(t, shouldRetryResponsesInputAsStringBody(http.StatusBadRequest, []byte(`{
		"error":{"type":"invalid_request_error","message":"426 validation errors:\n  {'type': 'string_type', 'loc': ('body', 'input', 'str'), 'msg': 'Input should be a valid string'}"}
	}`), requestBody))
	require.True(t, shouldRetryResponsesInputAsStringBody(http.StatusBadRequest, []byte(`{
		"error":{"type":"invalid_request_error","message":"Field required: 'input': {'type': 'message'}"}
	}`), requestBody))
	require.False(t, shouldRetryResponsesInputAsStringBody(http.StatusBadRequest, []byte(`{
		"error":{"type":"invalid_request_error","message":"Input should be a valid string"}
	}`), []byte(`{"input":"hello"}`)))
}

func TestRewriteResponsesInputAsStringBody(t *testing.T) {
	body, err := rewriteResponsesInputAsStringBody([]byte(`{
		"model":"test-model",
		"input":[
			{"type":"message","role":"developer","content":[{"type":"input_text","text":"You are helpful."}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"Call add."}]},
			{"type":"function_call_output","call_id":"call_1","output":"{\"result\":3}"}
		]
	}`))
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(body, &payload))
	input, ok := payload["input"].(string)
	require.True(t, ok)
	require.Contains(t, input, "DEVELOPER:")
	require.Contains(t, input, "USER:")
	require.Contains(t, input, "FUNCTION CALL OUTPUT (call_1):")
	require.Contains(t, input, `{"result":3}`)
}

func TestRemapCustomToolsPayloadRewritesCustomToolsAndSpecificToolChoice(t *testing.T) {
	rawFields := map[string]json.RawMessage{
		"model":       json.RawMessage(`"test-model"`),
		"tool_choice": json.RawMessage(`{"type":"custom","name":"code_exec"}`),
		"tools": json.RawMessage(`[
			{"type":"custom","name":"code_exec","description":"Executes arbitrary Python code"}
		]`),
	}

	body, plan, err := remapCustomToolsPayload(rawFields, "bridge", false, false)

	require.NoError(t, err)
	require.True(t, plan.BridgeActive())

	var payload map[string]any
	require.NoError(t, json.Unmarshal(body, &payload))

	tools, ok := payload["tools"].([]any)
	require.True(t, ok)
	require.Len(t, tools, 1)

	tool, ok := tools[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "function", tool["type"])
	require.Equal(t, "code_exec", tool["name"])
	require.Equal(t, "Executes arbitrary Python code", tool["description"])

	parameters, ok := tool["parameters"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "object", parameters["type"])
	require.Equal(t, false, parameters["additionalProperties"])

	properties, ok := parameters["properties"].(map[string]any)
	require.True(t, ok)
	inputProp, ok := properties["input"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "string", inputProp["type"])

	toolChoice, ok := payload["tool_choice"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "function", toolChoice["type"])
	require.Equal(t, "code_exec", toolChoice["name"])
}

func TestRemapCustomToolsPayloadRejectsGrammarCustomToolsInBridgeMode(t *testing.T) {
	rawFields := map[string]json.RawMessage{
		"tools": json.RawMessage(`[
			{"type":"custom","name":"code_exec","grammar":{"syntax":"lark","definition":"start: /.+/"}}
		]`),
	}

	_, _, err := remapCustomToolsPayload(rawFields, "bridge", false, false)

	var validationErr *domain.ValidationError
	require.ErrorAs(t, err, &validationErr)
	require.Equal(t, "tools", validationErr.Param)
	require.Contains(t, validationErr.Message, "custom tool format is not supported in bridge mode")
}

func TestRemapCustomToolsPayloadAcceptsCustomToolAlias(t *testing.T) {
	rawFields := map[string]json.RawMessage{
		"tool_choice": json.RawMessage(`{"type":"custom_tool","name":"code_exec"}`),
		"tools": json.RawMessage(`[
			{"type":"custom_tool","name":"code_exec","description":"Executes arbitrary Python code"}
		]`),
	}

	body, plan, err := remapCustomToolsPayload(rawFields, "bridge", false, false)

	require.NoError(t, err)
	require.True(t, plan.BridgeActive())

	var payload map[string]any
	require.NoError(t, json.Unmarshal(body, &payload))

	tools, ok := payload["tools"].([]any)
	require.True(t, ok)
	require.Len(t, tools, 1)
	tool, ok := tools[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "function", tool["type"])
	require.Equal(t, "code_exec", tool["name"])
	parameters, ok := tool["parameters"].(map[string]any)
	require.True(t, ok)
	properties, ok := parameters["properties"].(map[string]any)
	require.True(t, ok)
	inputProp, ok := properties["input"].(map[string]any)
	require.True(t, ok)
	require.Contains(t, inputProp["description"], "Escape any inner double quotes")

	toolChoice, ok := payload["tool_choice"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "function", toolChoice["type"])
	require.Equal(t, "code_exec", toolChoice["name"])
	require.Contains(t, payload["instructions"], customToolBridgeHintPrefix)
	require.Contains(t, payload["instructions"], "Available bridged custom tools: code_exec.")
	require.Equal(t, toolChoiceContractRequiredNamedCustom, plan.ToolChoiceContract.Mode)
	require.Equal(t, "code_exec", plan.ToolChoiceContract.Name)
}

func TestRemapCustomToolsPayloadDropsDisabledWebSearchTool(t *testing.T) {
	rawFields := map[string]json.RawMessage{
		"tool_choice": json.RawMessage(`"auto"`),
		"tools": json.RawMessage(`[
			{"type":"function","name":"exec_command","parameters":{"type":"object","properties":{"cmd":{"type":"string"}},"required":["cmd"]}},
			{"type":"web_search","external_web_access":false}
		]`),
	}

	body, plan, err := remapCustomToolsPayload(rawFields, "bridge", false, false)

	require.NoError(t, err)
	require.False(t, plan.BridgeActive())
	require.Equal(t, []string{"web_search"}, plan.DroppedBuiltinTools)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(body, &payload))

	tools, ok := payload["tools"].([]any)
	require.True(t, ok)
	require.Len(t, tools, 1)
	tool, ok := tools[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "function", tool["type"])
	require.Equal(t, "exec_command", tool["name"])
}

func TestRemapCustomToolsPayloadPreservesSupportedWebSearchBuiltIn(t *testing.T) {
	rawFields := map[string]json.RawMessage{
		"tool_choice": json.RawMessage(`{"type":"web_search"}`),
		"tools": json.RawMessage(`[
			{"type":"web_search","external_web_access":true}
		]`),
	}

	body, plan, err := remapCustomToolsPayload(rawFields, "bridge", false, false)

	require.NoError(t, err)
	require.False(t, plan.BridgeActive())

	var payload map[string]any
	require.NoError(t, json.Unmarshal(body, &payload))
	tools, ok := payload["tools"].([]any)
	require.True(t, ok)
	require.Len(t, tools, 1)
	tool, ok := tools[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "web_search", tool["type"])
	require.Equal(t, true, tool["external_web_access"])
	toolChoice, ok := payload["tool_choice"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "web_search", toolChoice["type"])
}

func TestRemapCustomToolsPayloadForcesRequiredToolChoiceForCodexAuto(t *testing.T) {
	rawFields := map[string]json.RawMessage{
		"instructions": json.RawMessage(`"You are a coding agent running in the Codex CLI, a terminal-based coding assistant."`),
		"tool_choice":  json.RawMessage(`"auto"`),
		"tools": json.RawMessage(`[
			{"type":"function","name":"exec_command","parameters":{"type":"object","properties":{"cmd":{"type":"string"}},"required":["cmd"]}}
		]`),
	}

	body, plan, err := remapCustomToolsPayload(rawFields, "bridge", true, true)

	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(body, &payload))
	require.Equal(t, "required", payload["tool_choice"])
	require.Equal(t, toolChoiceContractRequiredAny, plan.ToolChoiceContract.Mode)
}

func TestRemapCustomToolsPayloadKeepsAutoToolChoiceForNonCodexRequest(t *testing.T) {
	rawFields := map[string]json.RawMessage{
		"instructions": json.RawMessage(`"You are a normal assistant."`),
		"tool_choice":  json.RawMessage(`"auto"`),
		"tools": json.RawMessage(`[
			{"type":"function","name":"exec_command","parameters":{"type":"object","properties":{"cmd":{"type":"string"}},"required":["cmd"]}}
		]`),
	}

	body, _, err := remapCustomToolsPayload(rawFields, "bridge", true, true)

	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(body, &payload))
	require.Equal(t, "auto", payload["tool_choice"])
}

func TestRemapCustomToolsPayloadCapturesRequiredToolChoiceContract(t *testing.T) {
	rawFields := map[string]json.RawMessage{
		"tool_choice": json.RawMessage(`"required"`),
		"tools": json.RawMessage(`[
			{"type":"function","name":"add","parameters":{"type":"object","properties":{"a":{"type":"number"},"b":{"type":"number"}},"required":["a","b"]}}
		]`),
	}

	_, plan, err := remapCustomToolsPayload(rawFields, "bridge", false, false)

	require.NoError(t, err)
	require.Equal(t, toolChoiceContractRequiredAny, plan.ToolChoiceContract.Mode)
}

func TestShouldRetryToolChoiceWithAutoBody(t *testing.T) {
	plan := customToolTransportPlan{
		ToolChoiceContract: toolChoiceContract{Mode: toolChoiceContractRequiredAny},
	}

	require.True(t, shouldRetryToolChoiceWithAutoBody(http.StatusNotImplemented, []byte(`{"error":{"message":"Only 'auto' tool_choice is supported in response API with Harmony"}}`), plan))
	require.False(t, shouldRetryToolChoiceWithAutoBody(http.StatusNotImplemented, []byte(`{"error":{"message":"different error"}}`), plan))
}

func TestShouldRetryToolChoiceWithRequiredBody(t *testing.T) {
	plan := customToolTransportPlan{
		ToolChoiceContract: toolChoiceContract{Mode: toolChoiceContractRequiredNamedFunction, Name: "add"},
	}

	require.True(t, shouldRetryToolChoiceWithRequiredBody(http.StatusBadRequest, []byte(`{"error":{"message":"Invalid tool choice, tool_choice={'name': 'add', 'type': 'function'}"}}`), plan))
	require.False(t, shouldRetryToolChoiceWithRequiredBody(http.StatusBadRequest, []byte(`{"error":{"message":"Only 'auto' tool_choice is supported"}}`), plan))
}

func TestProxyCreateWithShadowStoreRetriesNamedToolChoiceRejection(t *testing.T) {
	requestBodies := make([]map[string]any, 0, 2)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/v1/responses", r.URL.Path)

		var payload map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		requestBodies = append(requestBodies, payload)

		if len(requestBodies) == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]any{
					"message": "Invalid tool choice, tool_choice={'name': 'add', 'type': 'function'}",
				},
			}))
			return
		}

		require.Equal(t, "required", payload["tool_choice"])
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"id":          "resp_1",
			"object":      "response",
			"model":       "test-model",
			"output_text": "",
			"output": []map[string]any{
				{
					"id":        "fc_1",
					"type":      "function_call",
					"call_id":   "call_1",
					"name":      "add",
					"arguments": `{"a":40,"b":2}`,
					"status":    "completed",
				},
			},
		}))
	}))
	defer upstream.Close()

	handler := &responseHandler{
		logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		proxy:           newProxyHandler(nil, llama.NewClient(upstream.URL, time.Second), nil, ServiceLimits{}, false),
		customToolsMode: "bridge",
	}

	rawFields := map[string]json.RawMessage{
		"model":               json.RawMessage(`"test-model"`),
		"input":               json.RawMessage(`"Call add with a=40 and b=2. Do not answer yourself."`),
		"parallel_tool_calls": json.RawMessage(`false`),
		"tool_choice":         json.RawMessage(`{"name":"add","type":"function"}`),
		"tools": json.RawMessage(`[
			{"type":"function","name":"add","description":"Adds two integers and returns the sum.","parameters":{"type":"object","properties":{"a":{"type":"integer"},"b":{"type":"integer"}},"required":["a","b"],"additionalProperties":false}}
		]`),
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	rec := httptest.NewRecorder()

	handler.proxyCreateWithShadowStore(rec, req, CreateResponseRequest{}, nil, "", rawFields)

	resp := rec.Result()
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, requestBodies, 2)
	require.Equal(t, "function", requestBodies[0]["tool_choice"].(map[string]any)["type"])
	require.Equal(t, "required", requestBodies[1]["tool_choice"])

	var payload map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&payload))
	output := payload["output"].([]any)
	require.Len(t, output, 1)
	toolCall := output[0].(map[string]any)
	require.Equal(t, "function_call", toolCall["type"])
	require.Equal(t, "add", toolCall["name"])
}

func TestShouldRetryCustomToolsWithBridgeBody(t *testing.T) {
	plan := customToolTransportPlan{Mode: customToolsModePassthrough, BridgeFallbackSafe: true}

	require.True(t, shouldRetryCustomToolsWithBridgeBody(http.StatusBadRequest, []byte(`{"error":{"message":"tool type custom not supported","type":"invalid_request_error"}}`), plan))
	require.True(t, shouldRetryCustomToolsWithBridgeBody(http.StatusBadRequest, []byte(`{"error":{"message":"'type' of tool must be 'function'","type":"invalid_request_error","param":"tools"}}`), plan))
	require.False(t, shouldRetryCustomToolsWithBridgeBody(http.StatusBadRequest, []byte(`{"error":{"message":"messages is required","type":"invalid_request_error"}}`), plan))
	require.False(t, shouldRetryCustomToolsWithBridgeBody(http.StatusBadRequest, []byte(`{"error":{"message":"tool type custom not supported","type":"invalid_request_error"}}`), customToolTransportPlan{Mode: customToolsModePassthrough}))
	require.False(t, shouldRetryCustomToolsWithBridgeBody(http.StatusBadRequest, []byte(`{"error":{"message":"tool type custom not supported","type":"invalid_request_error"}}`), customToolTransportPlan{Mode: customToolsModeBridge}))
}

func TestEnforceToolChoiceContractRejectsAssistantText(t *testing.T) {
	err := enforceToolChoiceContract(domain.Response{
		OutputText: "AUTO_FALLBACK_TEXT",
		Output:     []domain.Item{domain.NewOutputTextMessage("AUTO_FALLBACK_TEXT")},
	}, toolChoiceContract{Mode: toolChoiceContractRequiredAny})

	var incompatErr *toolChoiceIncompatibleBackendError
	require.ErrorAs(t, err, &incompatErr)
	require.Contains(t, incompatErr.Error(), "required tool call")
}

func TestEnforceToolChoiceContractAcceptsMatchingFunctionCall(t *testing.T) {
	item, err := domain.NewItem([]byte(`{"type":"function_call","call_id":"call_1","name":"add","arguments":"{\"a\":1,\"b\":2}"}`))
	require.NoError(t, err)

	err = enforceToolChoiceContract(domain.Response{
		Output: []domain.Item{item},
	}, toolChoiceContract{Mode: toolChoiceContractRequiredNamedFunction, Name: "add"})

	require.NoError(t, err)
}

func TestExtractCustomToolInputUnwrapsSingleStringProperty(t *testing.T) {
	require.Equal(t, `print("hello world")`, extractCustomToolInput(`{"code":"print(\"hello world\")"}`))
}

func TestExtractCustomToolInputUnwrapsDoubleEncodedSingleStringProperty(t *testing.T) {
	wrapped, err := json.Marshal(`{"code":"print(\"hello world\")"}`)
	require.NoError(t, err)
	require.Equal(t, `print("hello world")`, extractCustomToolInput(string(wrapped)))
}

func TestRemapCustomToolsPayloadAppendsCodexCompatibilityHint(t *testing.T) {
	rawFields := map[string]json.RawMessage{
		"instructions": json.RawMessage(`"You are a coding agent running in the Codex CLI, a terminal-based coding assistant."`),
		"tools": json.RawMessage(`[
			{"type":"function","name":"exec_command","description":"Runs a command in a PTY.","parameters":{"type":"object","properties":{"cmd":{"type":"string"}},"required":["cmd"]}},
			{"type":"function","name":"apply_patch","description":"Patch files.","parameters":{"type":"object"}}
		]`),
	}

	body, _, err := remapCustomToolsPayload(rawFields, "bridge", true, false)

	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(body, &payload))
	require.Contains(t, payload["instructions"], codexCompatibilityHint)

	tools, ok := payload["tools"].([]any)
	require.True(t, ok)
	require.Len(t, tools, 2)

	execCommand, ok := tools[0].(map[string]any)
	require.True(t, ok)
	require.Contains(t, execCommand["description"], "single shell string")
	require.Contains(t, execCommand["description"], "apply_patch tool directly")

	applyPatch, ok := tools[1].(map[string]any)
	require.True(t, ok)
	require.Contains(t, applyPatch["description"], "use this tool directly")
}

func TestRemapCustomToolsPayloadSkipsCodexCompatibilityWhenDisabled(t *testing.T) {
	rawFields := map[string]json.RawMessage{
		"instructions": json.RawMessage(`"You are a coding agent running in the Codex CLI, a terminal-based coding assistant."`),
		"tool_choice":  json.RawMessage(`"auto"`),
		"tools": json.RawMessage(`[
			{"type":"function","name":"exec_command","parameters":{"type":"object","properties":{"cmd":{"type":"string"}},"required":["cmd"]}}
		]`),
	}

	body, _, err := remapCustomToolsPayload(rawFields, "bridge", false, true)

	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(body, &payload))
	require.Equal(t, "required", payload["tool_choice"])
	require.NotContains(t, payload["instructions"], codexCompatibilityHint)
}

func TestRemapCustomToolsPayloadKeepsAutoWithoutCompatAndWithoutForce(t *testing.T) {
	rawFields := map[string]json.RawMessage{
		"instructions": json.RawMessage(`"You are a coding agent running in the Codex CLI, a terminal-based coding assistant."`),
		"tool_choice":  json.RawMessage(`"auto"`),
		"tools": json.RawMessage(`[
			{"type":"function","name":"exec_command","parameters":{"type":"object","properties":{"cmd":{"type":"string"}},"required":["cmd"]}}
		]`),
	}

	body, _, err := remapCustomToolsPayload(rawFields, "bridge", false, false)

	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(body, &payload))
	require.Equal(t, "auto", payload["tool_choice"])
	require.NotContains(t, payload["instructions"], codexCompatibilityHint)
}

func TestNormalizeUpstreamResponseBodyLeavesExecCommandUntouched(t *testing.T) {
	raw := []byte(`{
		"id":"upstream_resp_1",
		"object":"response",
		"model":"test-model",
		"output_text":"",
		"output":[
			{
				"id":"fc_1",
				"type":"function_call",
				"call_id":"call_1",
				"name":"exec_command",
				"arguments":"{\"cmd\":\"cd /tmp/snake_test && go test ./game -v 2>&1\",\"sandbox_permissions\":\"require_escalated\",\"justification\":\"Need approval to run tests\"}",
				"status":"completed"
			}
		]
	}`)

	body, err := normalizeUpstreamResponseBody(raw, customToolTransportPlan{})

	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(body, &payload))

	output := payload["output"].([]any)
	item := output[0].(map[string]any)
	require.Contains(t, item["arguments"].(string), `"sandbox_permissions":"require_escalated"`)
	require.Contains(t, item["arguments"].(string), `"justification":"Need approval to run tests"`)
}

func TestNormalizeUpstreamResponseBodyDoesNotSynthesizeAssistantMessage(t *testing.T) {
	raw := []byte(`{
		"id":"upstream_resp_1",
		"object":"response",
		"model":"test-model",
		"output_text":"",
		"output":[
			{
				"id":"rs_1",
				"type":"reasoning",
				"status":"completed",
				"content":[{"type":"reasoning_text","text":"All tasks are complete. Let me provide a summary to the user."}]
			},
			{
				"id":"fc_1",
				"type":"function_call",
				"call_id":"call_1",
				"name":"update_plan",
				"arguments":"{\"plan\":[{\"status\":\"completed\",\"step\":\"done\"}]}",
				"status":"completed"
			}
		]
	}`)

	body, err := normalizeUpstreamResponseBody(raw, customToolTransportPlan{})

	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(body, &payload))
	require.Equal(t, "", payload["output_text"])

	output := payload["output"].([]any)
	require.Len(t, output, 2)
	require.Equal(t, "reasoning", output[0].(map[string]any)["type"])
	require.Equal(t, "function_call", output[1].(map[string]any)["type"])
}

func TestRemapCustomToolResponseBodyRestoresOnlyCustomTools(t *testing.T) {
	raw := []byte(`{
		"id":"upstream_resp_1",
		"object":"response",
		"output_text":"",
		"output":[
			{
				"id":"fc_1",
				"type":"function_call",
				"call_id":"call_1",
				"name":"code_exec",
				"arguments":"{\"input\":\"print(\\\"hello world\\\")\"}",
				"status":"completed"
			},
			{
				"type":"function_call",
				"call_id":"call_2",
				"name":"add",
				"arguments":"{\"a\":1,\"b\":2}",
				"status":"completed"
			}
		]
	}`)

	body, err := remapCustomToolResponseBody(raw, customToolTransportPlan{
		Mode: customToolsModeBridge,
		Bridge: customToolBridge{
			ByModelName: map[string]customToolDescriptor{
				"code_exec": {
					Name:          "code_exec",
					SyntheticName: "shim_custom_89d627846840f47ebaffff0e3d467aeb500def4d",
				},
			},
			BySynthetic: map[string]customToolDescriptor{
				"shim_custom_89d627846840f47ebaffff0e3d467aeb500def4d": {
					Name:          "code_exec",
					SyntheticName: "shim_custom_89d627846840f47ebaffff0e3d467aeb500def4d",
				},
			},
			ByCanonical: map[string]customToolDescriptor{
				canonicalCustomToolKey("", "code_exec"): {
					Name:          "code_exec",
					SyntheticName: "shim_custom_89d627846840f47ebaffff0e3d467aeb500def4d",
				},
			},
		},
	})

	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(body, &payload))

	output, ok := payload["output"].([]any)
	require.True(t, ok)
	require.Len(t, output, 2)

	customCall, ok := output[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "custom_tool_call", customCall["type"])
	require.Equal(t, "fc_1", customCall["id"])
	require.Equal(t, "call_1", customCall["call_id"])
	require.Equal(t, "code_exec", customCall["name"])
	require.Equal(t, `print("hello world")`, customCall["input"])
	require.Equal(t, "completed", customCall["status"])

	functionCall, ok := output[1].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "function_call", functionCall["type"])
	require.Equal(t, "add", functionCall["name"])
}

func TestRemapCustomToolsPayloadRejectsDuplicateBridgeNamesAcrossNamespaces(t *testing.T) {
	rawFields := map[string]json.RawMessage{
		"tools": json.RawMessage(`[
			{"type":"custom","namespace":"shell","name":"exec"},
			{"type":"custom","namespace":"python","name":"exec"}
		]`),
	}

	_, _, err := remapCustomToolsPayload(rawFields, "bridge", false, false)

	var validationErr *domain.ValidationError
	require.ErrorAs(t, err, &validationErr)
	require.Equal(t, "tools", validationErr.Param)
}

func TestRemapCustomToolResponseBodyRecoversPlaceholderMessageFromReasoning(t *testing.T) {
	raw, err := json.Marshal(map[string]any{
		"id":          "resp_152511",
		"object":      "response",
		"output_text": "",
		"output": []map[string]any{
			{
				"id":   "rs_resp_152511",
				"type": "reasoning",
				"summary": []map[string]any{
					{
						"type": "summary_text",
						"text": "The user wants me to use the `code_exec` tool to print \"hello world\" to the console.\n" +
							"I should not answer directly, but instead emit a tool call.\n\n" +
							"Plan:\n1. Formulate the Python code: `print(\"hello world\")`.\n" +
							"2. Format it as a JSON string for the `code_exec` tool's `input` parameter.\n" +
							"3. Call the `code_exec` tool.",
					},
				},
			},
			{
				"id":     "msg_903606",
				"type":   "message",
				"status": "completed",
				"role":   "assistant",
				"content": []map[string]any{
					{"type": "output_text", "text": "<|tool_response|><|tool_response|><|tool_response|>\n"},
				},
			},
		},
	})
	require.NoError(t, err)

	body, err := remapCustomToolResponseBody(raw, customToolTransportPlan{
		Mode: customToolsModeBridge,
		Bridge: customToolBridge{
			ByModelName: map[string]customToolDescriptor{
				"code_exec": {
					Name:          "code_exec",
					SyntheticName: syntheticCustomToolName("", "code_exec"),
				},
			},
			BySynthetic: map[string]customToolDescriptor{
				syntheticCustomToolName("", "code_exec"): {
					Name:          "code_exec",
					SyntheticName: syntheticCustomToolName("", "code_exec"),
				},
			},
			ByCanonical: map[string]customToolDescriptor{
				canonicalCustomToolKey("", "code_exec"): {
					Name:          "code_exec",
					SyntheticName: syntheticCustomToolName("", "code_exec"),
				},
			},
		},
	})

	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(body, &payload))
	output := payload["output"].([]any)
	require.Len(t, output, 2)

	recovered := output[1].(map[string]any)
	require.Equal(t, "custom_tool_call", recovered["type"])
	require.Equal(t, "code_exec", recovered["name"])
	require.Equal(t, `print("hello world")`, recovered["input"])
	require.Equal(t, "msg_903606", recovered["id"])
	require.Equal(t, "call_903606", recovered["call_id"])
}

func TestBuildUpstreamResponsesBodyReplaysBridgeCustomToolsWithoutCurrentTools(t *testing.T) {
	call, err := domain.NewItem([]byte(`{
		"id":"ctc_1",
		"type":"custom_tool_call",
		"call_id":"call_1",
		"name":"code_exec",
		"input":"print(\"hello world\")",
		"status":"completed"
	}`))
	require.NoError(t, err)
	call.Meta = &domain.ItemMeta{
		Transport:     "bridge",
		SyntheticName: syntheticCustomToolName("", "code_exec"),
		CanonicalType: "custom_tool_call",
		ToolName:      "code_exec",
	}

	output, err := domain.NewItem([]byte(`{
		"type":"custom_tool_call_output",
		"call_id":"call_1",
		"output":"tool says hi"
	}`))
	require.NoError(t, err)

	refs := domain.CollectToolCallReferences([]domain.Item{call})
	body, plan, err := buildUpstreamResponsesBody(
		map[string]json.RawMessage{
			"model": json.RawMessage(`"test-model"`),
		},
		[]domain.Item{call, output},
		[]domain.Item{output},
		refs,
		"bridge",
		false,
		false,
	)
	require.NoError(t, err)
	require.True(t, plan.BridgeActive())

	var payload map[string]any
	require.NoError(t, json.Unmarshal(body, &payload))

	input, ok := payload["input"].([]any)
	require.True(t, ok)
	require.Len(t, input, 2)

	callItem, ok := input[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "function_call", callItem["type"])
	require.Equal(t, "code_exec", callItem["name"])
	require.Equal(t, `{"input":"print(\"hello world\")"}`, callItem["arguments"])

	outputItem, ok := input[1].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "function_call_output", outputItem["type"])
	require.Equal(t, "call_1", outputItem["call_id"])
	require.Equal(t, "tool says hi", outputItem["output"])
}
