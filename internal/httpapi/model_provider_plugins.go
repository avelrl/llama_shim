package httpapi

import (
	"strings"

	"llama_shim/internal/backendcap"
	"llama_shim/internal/config"
	"llama_shim/internal/plugincontract"
	"llama_shim/internal/upstreamcompat"
)

const modelProviderPluginVersion = "v1"

type modelProviderPlugin struct {
	descriptor plugincontract.Descriptor
	component  backendcap.Component
}

func (p modelProviderPlugin) Descriptor() plugincontract.Descriptor {
	return p.descriptor
}

func (p modelProviderPlugin) CapabilityComponent() backendcap.Component {
	return p.component
}

func modelProviderPlugins(deps RouterDeps, llamaProbe capabilityProbe, responsesTransport string) []plugincontract.CapabilityPlugin {
	wireModes := []string{"chat_completions", "raw_proxy"}
	if responsesTransport == config.ResponsesUpstreamTransportChatCompletions {
		wireModes = append(wireModes, "responses_over_chat")
	} else {
		wireModes = append(wireModes, "responses_native")
	}
	if deps.ResponsesWebSocketEnabled {
		wireModes = append(wireModes, "websocket_responses")
	}
	class := modelBackendRegistryClass(responsesTransport)
	publicSurfaces := []string{
		"models.list",
		"responses.create",
		"responses.input_tokens",
		"responses.compact",
		"chat_completions.create",
	}
	projections := modelProviderBackendProjections(responsesTransport)
	errorClasses := []string{
		string(backendFailureAuthFailure),
		string(backendFailurePermissionFailure),
		string(backendFailureQuotaExhausted),
		string(backendFailureRateLimitRetryable),
		string(backendFailureModelUnavailable),
		string(backendFailureUnsupportedToolOrParam),
		string(backendFailureTransportTimeout),
		string(backendFailureMalformedBackendResponse),
		string(backendFailureTransportError),
	}
	timeouts := []string{"readyzProviderTimeout", "modelsUpstreamTimeout", "upstream_client_timeout"}

	if len(deps.LlamaProviders) == 0 {
		descriptor := plugincontract.Descriptor{
			ID:                    "model.llama",
			Version:               modelProviderPluginVersion,
			Kind:                  "model_backend",
			DisplayName:           "Default Llama backend",
			ConfigNamespace:       "llama",
			ReadinessProbe:        "llama",
			CapabilityComponentID: "model.llama",
			PublicSurfaces:        publicSurfaces,
			BackendProjections:    projections,
			RequestCleanupHooks:   upstreamcompat.RequestCleanupHookNamesForChatRules(deps.ChatCompletionsUpstreamCompatibility),
			Timeouts:              timeouts,
			ErrorClasses:          errorClasses,
			CIFixtureSafe:         true,
			ProductionIntended:    true,
		}
		component := backendcap.Component{
			ID:              "model.llama",
			Category:        "model_backend",
			Kind:            "openai_compatible",
			ConfigNamespace: "llama",
			CapabilityClass: class,
			Enabled:         deps.LlamaClient != nil,
			Ready:           deps.LlamaClient != nil && probeReady(llamaProbe),
			ReadinessProbe:  "llama",
			Auth:            "single_upstream_config",
			WireModes:       wireModes,
			PublicSurfaces:  publicSurfaces,
			RoutingModes:    standardRoutingModes(),
			Evidence:        []string{"docs/v3-upstream-provider-routing.md", "docs/v4-preflight.md"},
		}
		return []plugincontract.CapabilityPlugin{modelProviderPlugin{descriptor: descriptor, component: component}}
	}

	plugins := make([]plugincontract.CapabilityPlugin, 0, len(deps.LlamaProviders))
	for _, provider := range deps.LlamaProviders {
		providerID := strings.TrimSpace(provider.ID)
		if providerID == "" {
			continue
		}
		models := make([]string, 0, len(provider.Models))
		upstreamModels := make([]string, 0, len(provider.Models))
		for _, model := range provider.Models {
			publicModel := strings.TrimSpace(model.Model)
			if publicModel == "" {
				continue
			}
			models = append(models, providerID+"/"+publicModel)
			upstreamModel := strings.TrimSpace(model.UpstreamModel)
			if upstreamModel == "" {
				upstreamModel = publicModel
			}
			upstreamModels = append(upstreamModels, upstreamModel)
		}
		requestCleanupHooks := []string{upstreamcompat.RequestCleanupHookProviderRewriteModelAlias}
		if strings.TrimSpace(provider.BearerToken) != "" || strings.TrimSpace(provider.BearerTokenEnv) != "" {
			requestCleanupHooks = append(requestCleanupHooks, upstreamcompat.RequestCleanupHookProviderOverrideAuthorizationHeader)
		}
		requestCleanupHooks = append(requestCleanupHooks, upstreamcompat.RequestCleanupHookNamesForChatModels(deps.ChatCompletionsUpstreamCompatibility, upstreamModels...)...)
		pluginID := upstreamProviderPluginID(providerID)
		descriptor := plugincontract.Descriptor{
			ID:                    pluginID,
			Version:               modelProviderPluginVersion,
			Kind:                  "model_provider",
			DisplayName:           providerID,
			ConfigNamespace:       "llama.providers." + providerID,
			RequiredSecrets:       providerSecretRefs(provider),
			ReadinessProbe:        "llama",
			CapabilityComponentID: "provider." + providerID,
			PublicSurfaces:        publicSurfaces,
			BackendProjections:    projections,
			RequestCleanupHooks:   upstreamcompat.RequestCleanupHookFieldNames(requestCleanupHooks),
			Timeouts:              timeouts,
			ErrorClasses:          errorClasses,
			CIFixtureSafe:         false,
			ProductionIntended:    true,
		}
		component := backendcap.Component{
			ID:              "provider." + providerID,
			Category:        "model_provider",
			Kind:            "openai_compatible",
			DisplayName:     providerID,
			ConfigNamespace: "llama.providers." + providerID,
			CapabilityClass: class,
			Enabled:         true,
			Ready:           probeReady(llamaProbe),
			ReadinessProbe:  "llama",
			Auth:            providerAuthSummary(provider),
			SecretRefs:      providerSecretRefs(provider),
			StateOwnership:  "stateless_backend_with_shim_state",
			WireModes:       wireModes,
			PublicSurfaces:  publicSurfaces,
			ModelIDs:        models,
			RoutingModes:    standardRoutingModes(),
			Evidence:        []string{"docs/v3-upstream-provider-routing.md", "docs/v4-preflight.md"},
		}
		plugins = append(plugins, modelProviderPlugin{descriptor: descriptor, component: component})
	}
	return plugins
}

func modelProviderBackendProjections(responsesTransport string) []plugincontract.Projection {
	projections := []plugincontract.Projection{
		{Class: "native", SourceFormat: "chat_completions", TargetFormat: "chat_completions"},
		{Class: "proxy_only", SourceFormat: "models.list", TargetFormat: "models.list"},
	}
	if responsesTransport == config.ResponsesUpstreamTransportChatCompletions {
		projections = append(projections, plugincontract.Projection{
			Class:        "chat_projection",
			SourceFormat: "responses",
			TargetFormat: "chat_completions",
		})
	} else {
		projections = append(projections, plugincontract.Projection{
			Class:        "native",
			SourceFormat: "responses",
			TargetFormat: "responses",
		})
	}
	return projections
}

func upstreamProviderPluginID(providerID string) string {
	providerID = strings.TrimSpace(providerID)
	if providerID == "" {
		return ""
	}
	return "provider." + providerID
}
