package httpapi

import (
	"encoding/json"
	"fmt"
	"strings"

	"llama_shim/internal/config"
	"llama_shim/internal/domain"
	"llama_shim/internal/imagegen"
	"llama_shim/internal/websearch"
)

const (
	responseToolDispositionLocalExecute        = "local_execute"
	responseToolDispositionUpstreamPassthrough = "upstream_passthrough"
	responseToolDispositionProxyOnly           = "proxy_only"
	responseToolDispositionChatProjection      = "chat_projection"
	responseToolDispositionFunctionRepair      = "function_repair"
	responseToolDispositionClientRoundTrip     = "client_round_trip"
	responseToolDispositionAcceptNoop          = "accept_noop"
	responseToolDispositionReject              = "reject"
)

type responseToolClassification struct {
	Index           int
	Type            string
	Family          string
	Disposition     string
	CapabilityClass string
	Backend         string
	LocalSupported  bool
	RuntimeEnabled  bool
	Reason          string
}

type responseToolClassifications []responseToolClassification

type responseToolClassifierConfig struct {
	RawFields               map[string]json.RawMessage
	WebSearchProvider       websearch.Provider
	ImageGenerationProvider imagegen.Provider
	LocalComputer           LocalComputerRuntimeConfig
	LocalCodeInterpreter    LocalCodeInterpreterRuntimeConfig
	HasLocalMCPState        bool
}

func classifyResponseTools(cfg responseToolClassifierConfig) responseToolClassifications {
	rawFields := cfg.RawFields
	tools := decodeToolList(rawFields)
	if len(tools) == 0 {
		return nil
	}

	localToolLoopSupported := supportsLocalToolLoop(rawFields)
	localFileSearchSupported := supportsLocalFileSearch(rawFields)
	localWebSearchSupported := supportsLocalWebSearch(rawFields, cfg.WebSearchProvider)
	localImageGenerationSupported := supportsLocalImageGeneration(rawFields, cfg.ImageGenerationProvider)
	localComputerSupported := supportsLocalComputer(rawFields, cfg.LocalComputer)
	localMCPSupported := supportsLocalMCP(rawFields) || (cfg.HasLocalMCPState && !hasConnectorMCPTools(rawFields))
	localToolSearchRequested := hasLocalToolSearchRequest(rawFields)
	localToolSearchSupported := supportsLocalToolSearch(rawFields)
	localCodeInterpreterSupported := supportsLocalCodeInterpreter(rawFields, cfg.LocalCodeInterpreter)

	out := make(responseToolClassifications, 0, len(tools))
	for index, tool := range tools {
		classification := responseToolClassification{
			Index:  index,
			Type:   strings.ToLower(strings.TrimSpace(asString(tool["type"]))),
			Family: strings.ToLower(strings.TrimSpace(asString(tool["type"]))),
		}

		switch toolType := classification.Type; {
		case toolType == "":
			classification.Family = "unknown"
			classification.Disposition = responseToolDispositionReject
			classification.CapabilityClass = "unsupported"
			classification.Reason = "tools[].type is required"
		case localToolSearchRequested && toolType == "function" && toolBoolField(tool, "defer_loading"):
			classification.Family = "tool_search_deferred_function"
			classification.Disposition = responseToolDispositionLocalExecute
			classification.CapabilityClass = "local_subset"
			classification.Backend = "local_tool_search"
			classification.LocalSupported = localToolSearchSupported
			classification.RuntimeEnabled = true
		case localToolSearchRequested && toolType == "namespace":
			classification.Family = "tool_search_namespace"
			classification.Disposition = responseToolDispositionLocalExecute
			classification.CapabilityClass = "local_subset"
			classification.Backend = "local_tool_search"
			classification.LocalSupported = localToolSearchSupported
			classification.RuntimeEnabled = true
		case toolType == "function":
			classification.Disposition = responseToolDispositionChatProjection
			classification.CapabilityClass = "chat_projection"
			classification.Backend = "chat_completions_tool_loop"
			classification.LocalSupported = localToolLoopSupported
			classification.RuntimeEnabled = true
		case isCustomToolType(toolType):
			classification.Family = "custom"
			classification.Disposition = responseToolDispositionChatProjection
			classification.CapabilityClass = "chat_projection"
			if detectCustomToolFormatType(tool) == "grammar" {
				classification.Disposition = responseToolDispositionFunctionRepair
				classification.CapabilityClass = "repair_or_validate"
			}
			classification.Backend = "chat_completions_tool_loop"
			classification.LocalSupported = localToolLoopSupported
			classification.RuntimeEnabled = true
		case isLocalBuiltinToolType(toolType):
			classification.Disposition = responseToolDispositionLocalExecute
			classification.CapabilityClass = "local_subset"
			classification.Backend = "chat_completions_tool_loop"
			classification.LocalSupported = localToolLoopSupported
			classification.RuntimeEnabled = true
		case isDisabledWebSearchTool(tool):
			classification.Family = "web_search"
			classification.Disposition = responseToolDispositionAcceptNoop
			classification.CapabilityClass = "local_subset"
			classification.LocalSupported = localToolLoopSupported
			classification.RuntimeEnabled = true
			classification.Reason = "web_search.external_web_access=false is a shim-local compatibility no-op inside the local tool loop"
		case toolType == "file_search":
			classification.Disposition = responseToolDispositionLocalExecute
			classification.CapabilityClass = "local_subset"
			classification.LocalSupported = localFileSearchSupported
			classification.RuntimeEnabled = true
		case toolType == "web_search" || toolType == "web_search_preview":
			classification.Family = "web_search"
			classification.Disposition = responseToolDispositionLocalExecute
			classification.CapabilityClass = "local_subset"
			classification.Backend = "responses.web_search"
			classification.LocalSupported = localWebSearchSupported
			classification.RuntimeEnabled = cfg.WebSearchProvider != nil
		case toolType == "image_generation":
			classification.Disposition = responseToolDispositionLocalExecute
			classification.CapabilityClass = "local_subset"
			classification.Backend = "responses.image_generation"
			classification.LocalSupported = localImageGenerationSupported
			classification.RuntimeEnabled = cfg.ImageGenerationProvider != nil
		case toolType == "computer":
			classification.Disposition = responseToolDispositionLocalExecute
			classification.CapabilityClass = "local_subset"
			classification.Backend = "responses.computer"
			classification.LocalSupported = localComputerSupported
			classification.RuntimeEnabled = cfg.LocalComputer.Enabled()
		case toolType == "code_interpreter":
			classification.Disposition = responseToolDispositionLocalExecute
			classification.CapabilityClass = "local_subset"
			classification.Backend = "responses.code_interpreter"
			classification.LocalSupported = localCodeInterpreterSupported
			classification.RuntimeEnabled = cfg.LocalCodeInterpreter.Enabled()
		case toolType == "mcp":
			classification.Family = classifyMCPToolFamily(tool)
			classification.RuntimeEnabled = true
			if classification.Family == "mcp_connector_id" {
				classification.Disposition = responseToolDispositionProxyOnly
				classification.CapabilityClass = "proxy_only"
				classification.Reason = "mcp.connector_id is an upstream connector surface; connectors remain upstream-only"
			} else {
				classification.Disposition = responseToolDispositionLocalExecute
				classification.CapabilityClass = "local_subset"
				classification.Backend = "remote_mcp_server_url"
				classification.LocalSupported = localMCPSupported
			}
		case toolType == "tool_search":
			classification.Family = "tool_search"
			if strings.EqualFold(strings.TrimSpace(asString(tool["execution"])), "client") {
				classification.Disposition = responseToolDispositionClientRoundTrip
				classification.CapabilityClass = "proxy_only"
				classification.Reason = "client-executed tool_search is a client round-trip surface; client execution remains proxy-only"
			} else {
				classification.Disposition = responseToolDispositionLocalExecute
				classification.CapabilityClass = "local_subset"
				classification.Backend = "local_tool_search"
				classification.LocalSupported = localToolSearchSupported
				classification.RuntimeEnabled = true
			}
		case toolType == "computer_use":
			classification.Family = "computer"
			classification.Disposition = responseToolDispositionUpstreamPassthrough
			classification.CapabilityClass = "proxy_only"
			classification.Reason = "computer_use is not part of the shim-local computer tool subset"
		default:
			classification.Family = "unknown"
			classification.Disposition = responseToolDispositionUpstreamPassthrough
			classification.CapabilityClass = "unsupported"
			classification.Reason = "tool type is not implemented by shim-local routing"
		}

		out = append(out, classification)
	}
	return out
}

func classifyMCPToolFamily(tool map[string]any) string {
	if strings.TrimSpace(asString(tool["connector_id"])) != "" {
		return "mcp_connector_id"
	}
	return "mcp_server_url"
}

func (classifications responseToolClassifications) validateForResponsesMode(mode string) error {
	if mode != config.ResponsesModeLocalOnly {
		return nil
	}
	for _, classification := range classifications {
		switch classification.Disposition {
		case responseToolDispositionProxyOnly, responseToolDispositionUpstreamPassthrough, responseToolDispositionClientRoundTrip, responseToolDispositionReject:
			return classification.localOnlyValidationError()
		}
	}
	return nil
}

func (classification responseToolClassification) localOnlyValidationError() error {
	toolType := classification.Type
	if toolType == "" {
		toolType = "unknown"
	}
	message := fmt.Sprintf(
		"tool %q is classified as %s and is not supported when responses.mode=local_only",
		toolType,
		classification.Disposition,
	)
	if classification.Family != "" && classification.Family != toolType {
		message = fmt.Sprintf(
			"tool %q (%s) is classified as %s and is not supported when responses.mode=local_only",
			toolType,
			classification.Family,
			classification.Disposition,
		)
	}
	if strings.TrimSpace(classification.Reason) != "" {
		message += ": " + strings.TrimSpace(classification.Reason)
	}
	return domain.NewValidationError("tools", message)
}
