package httpapi

import (
	"encoding/json"

	"llama_shim/internal/config"
	"llama_shim/internal/imagegen"
	"llama_shim/internal/websearch"
)

type responsesCreateRoute int

const (
	responsesCreateRouteProxy responsesCreateRoute = iota
	responsesCreateRouteLocalWebSearch
	responsesCreateRouteLocalImageGeneration
	responsesCreateRouteLocalFileSearch
	responsesCreateRouteLocalComputer
	responsesCreateRouteLocalMCP
	responsesCreateRouteLocalToolSearch
	responsesCreateRouteLocalCodeInterpreter
	responsesCreateRouteLocalToolLoop
	responsesCreateRouteLocalState
	responsesCreateRouteLocalStateViaUpstream
	responsesCreateRouteLocalOnlyUnsupported
	responsesCreateRouteLocalWebSearchDisabled
	responsesCreateRouteLocalImageGenerationDisabled
	responsesCreateRouteLocalComputerDisabled
	responsesCreateRouteLocalCodeInterpreterDisabled
)

type responsesCreateRouteInputs struct {
	HasLocalState                      bool
	LocalToolLoop                      bool
	LocalToolSearchRequested           bool
	LocalToolSearch                    bool
	LocalFileSearchRequested           bool
	LocalFileSearch                    bool
	LocalWebSearchRequested            bool
	LocalWebSearchRuntimeEnabled       bool
	LocalWebSearch                     bool
	LocalImageGenerationRequested      bool
	LocalImageGenerationRuntimeEnabled bool
	LocalImageGeneration               bool
	LocalComputerRequested             bool
	LocalComputerRuntimeEnabled        bool
	LocalComputer                      bool
	LocalMCPRequested                  bool
	LocalMCP                           bool
	LocalCodeInterpreterRequested      bool
	LocalCodeInterpreterRuntimeEnabled bool
	LocalCodeInterpreter               bool
	LocalSupported                     bool
}

func selectResponsesCreateRoute(responsesMode string, profile responsesCreateRouteInputs) responsesCreateRoute {
	if responsesMode == config.ResponsesModePreferUpstream && !profile.HasLocalState {
		return responsesCreateRouteProxy
	}

	switch {
	case profile.LocalWebSearch:
		return responsesCreateRouteLocalWebSearch
	case profile.LocalWebSearchRequested && responsesMode == config.ResponsesModeLocalOnly:
		if !profile.LocalWebSearchRuntimeEnabled {
			return responsesCreateRouteLocalWebSearchDisabled
		}
		return responsesCreateRouteLocalWebSearch
	case profile.LocalImageGeneration:
		return responsesCreateRouteLocalImageGeneration
	case profile.LocalImageGenerationRequested && responsesMode == config.ResponsesModeLocalOnly:
		if !profile.LocalImageGenerationRuntimeEnabled {
			return responsesCreateRouteLocalImageGenerationDisabled
		}
		return responsesCreateRouteLocalImageGeneration
	case profile.LocalFileSearch:
		return responsesCreateRouteLocalFileSearch
	case profile.LocalFileSearchRequested && responsesMode == config.ResponsesModeLocalOnly:
		return responsesCreateRouteLocalFileSearch
	case profile.LocalComputer:
		return responsesCreateRouteLocalComputer
	case profile.LocalComputerRequested && responsesMode == config.ResponsesModeLocalOnly:
		if !profile.LocalComputerRuntimeEnabled {
			return responsesCreateRouteLocalComputerDisabled
		}
		return responsesCreateRouteLocalComputer
	case profile.LocalMCP:
		return responsesCreateRouteLocalMCP
	case profile.LocalMCPRequested && responsesMode == config.ResponsesModeLocalOnly:
		return responsesCreateRouteLocalMCP
	case profile.LocalToolSearch:
		return responsesCreateRouteLocalToolSearch
	case profile.LocalToolSearchRequested && responsesMode == config.ResponsesModeLocalOnly:
		return responsesCreateRouteLocalToolSearch
	case profile.LocalCodeInterpreter:
		return responsesCreateRouteLocalCodeInterpreter
	case profile.LocalCodeInterpreterRequested && responsesMode == config.ResponsesModeLocalOnly:
		if !profile.LocalCodeInterpreterRuntimeEnabled {
			return responsesCreateRouteLocalCodeInterpreterDisabled
		}
		return responsesCreateRouteLocalCodeInterpreter
	case profile.LocalToolLoop:
		return responsesCreateRouteLocalToolLoop
	case profile.LocalSupported:
		return responsesCreateRouteLocalState
	case profile.HasLocalState && responsesMode == config.ResponsesModeLocalOnly:
		return responsesCreateRouteLocalOnlyUnsupported
	case profile.HasLocalState:
		return responsesCreateRouteLocalStateViaUpstream
	case responsesMode == config.ResponsesModeLocalOnly:
		return responsesCreateRouteLocalOnlyUnsupported
	default:
		return responsesCreateRouteProxy
	}
}

func buildResponsesCreateRouteInputs(
	hasLocalState bool,
	rawFields map[string]json.RawMessage,
	webSearchProvider websearch.Provider,
	imageGenerationProvider imagegen.Provider,
	localComputer LocalComputerRuntimeConfig,
	localCodeInterpreter LocalCodeInterpreterRuntimeConfig,
	hasLocalMCPState bool,
) responsesCreateRouteInputs {
	localContextManagement := hasLocalContextManagementRequest(rawFields)
	localMCPSupported := supportsLocalMCP(rawFields)
	localMCPConnector := hasConnectorMCPTools(rawFields)
	localMCPUnsupported := hasUnsupportedLocalMCPTools(rawFields)

	return responsesCreateRouteInputs{
		HasLocalState:                      hasLocalState,
		LocalToolLoop:                      !localContextManagement && supportsLocalToolLoop(rawFields),
		LocalToolSearchRequested:           !localContextManagement && hasLocalToolSearchRequest(rawFields),
		LocalToolSearch:                    !localContextManagement && supportsLocalToolSearch(rawFields),
		LocalFileSearchRequested:           !localContextManagement && isLocalFileSearchToolRequest(rawFields),
		LocalFileSearch:                    !localContextManagement && supportsLocalFileSearch(rawFields),
		LocalWebSearchRequested:            !localContextManagement && isLocalWebSearchToolRequest(rawFields),
		LocalWebSearchRuntimeEnabled:       webSearchProvider != nil,
		LocalWebSearch:                     !localContextManagement && supportsLocalWebSearch(rawFields, webSearchProvider),
		LocalImageGenerationRequested:      !localContextManagement && isLocalImageGenerationToolRequest(rawFields),
		LocalImageGenerationRuntimeEnabled: imageGenerationProvider != nil,
		LocalImageGeneration:               !localContextManagement && supportsLocalImageGeneration(rawFields, imageGenerationProvider),
		LocalComputerRequested:             !localContextManagement && isLocalComputerToolRequest(rawFields),
		LocalComputerRuntimeEnabled:        localComputer.Enabled(),
		LocalComputer:                      !localContextManagement && supportsLocalComputer(rawFields, localComputer),
		LocalMCPRequested:                  !localContextManagement && hasDeclaredMCPTools(rawFields),
		LocalMCP:                           !localContextManagement && (localMCPSupported || localMCPUnsupported || hasLocalMCPApprovalResponse(rawFields) || (hasLocalMCPState && !localMCPConnector)),
		LocalCodeInterpreterRequested:      !localContextManagement && isLocalCodeInterpreterToolRequest(rawFields),
		LocalCodeInterpreterRuntimeEnabled: localCodeInterpreter.Enabled(),
		LocalCodeInterpreter:               !localContextManagement && supportsLocalCodeInterpreter(rawFields, localCodeInterpreter),
		LocalSupported:                     supportsLocalShimState(rawFields),
	}
}
