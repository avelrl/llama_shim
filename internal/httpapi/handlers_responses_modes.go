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
	responsesCreateRouteLocalImageGenerationDisabled
	responsesCreateRouteLocalComputerDisabled
	responsesCreateRouteLocalCodeInterpreterDisabled
)

type responsesCreateRouteInputs struct {
	HasLocalState                 bool
	LocalToolLoop                 bool
	LocalToolSearch               bool
	LocalFileSearch               bool
	LocalWebSearch                bool
	LocalImageGenerationRequested bool
	LocalImageGeneration          bool
	LocalComputerRequested        bool
	LocalComputer                 bool
	LocalMCP                      bool
	LocalCodeInterpreterRequested bool
	LocalCodeInterpreter          bool
	LocalSupported                bool
}

func selectResponsesCreateRoute(responsesMode string, profile responsesCreateRouteInputs) responsesCreateRoute {
	if responsesMode == config.ResponsesModePreferUpstream && !profile.HasLocalState {
		return responsesCreateRouteProxy
	}

	switch {
	case profile.LocalWebSearch:
		return responsesCreateRouteLocalWebSearch
	case profile.LocalImageGeneration:
		return responsesCreateRouteLocalImageGeneration
	case profile.LocalFileSearch:
		return responsesCreateRouteLocalFileSearch
	case profile.LocalComputer:
		return responsesCreateRouteLocalComputer
	case profile.LocalMCP:
		return responsesCreateRouteLocalMCP
	case profile.LocalToolSearch:
		return responsesCreateRouteLocalToolSearch
	case profile.LocalCodeInterpreter:
		return responsesCreateRouteLocalCodeInterpreter
	case profile.LocalToolLoop:
		return responsesCreateRouteLocalToolLoop
	case profile.LocalComputerRequested && responsesMode == config.ResponsesModeLocalOnly:
		return responsesCreateRouteLocalComputerDisabled
	case profile.LocalImageGenerationRequested && responsesMode == config.ResponsesModeLocalOnly:
		return responsesCreateRouteLocalImageGenerationDisabled
	case profile.LocalCodeInterpreterRequested && responsesMode == config.ResponsesModeLocalOnly:
		return responsesCreateRouteLocalCodeInterpreterDisabled
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
	localMCPSupported := supportsLocalMCP(rawFields)
	localMCPConnector := hasConnectorMCPTools(rawFields)
	localMCPUnsupported := hasUnsupportedLocalMCPTools(rawFields)

	return responsesCreateRouteInputs{
		HasLocalState:                 hasLocalState,
		LocalToolLoop:                 supportsLocalToolLoop(rawFields),
		LocalToolSearch:               supportsLocalToolSearch(rawFields),
		LocalFileSearch:               supportsLocalFileSearch(rawFields),
		LocalWebSearch:                supportsLocalWebSearch(rawFields, webSearchProvider),
		LocalImageGenerationRequested: isLocalImageGenerationToolRequest(rawFields),
		LocalImageGeneration:          supportsLocalImageGeneration(rawFields, imageGenerationProvider),
		LocalComputerRequested:        isLocalComputerToolRequest(rawFields),
		LocalComputer:                 supportsLocalComputer(rawFields, localComputer),
		LocalMCP:                      localMCPSupported || localMCPUnsupported || hasLocalMCPApprovalResponse(rawFields) || (hasLocalMCPState && !localMCPConnector),
		LocalCodeInterpreterRequested: isLocalCodeInterpreterToolRequest(rawFields),
		LocalCodeInterpreter:          supportsLocalCodeInterpreter(rawFields, localCodeInterpreter),
		LocalSupported:                supportsLocalShimState(rawFields),
	}
}
