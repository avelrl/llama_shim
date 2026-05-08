package httpapi

import (
	"strings"

	"llama_shim/internal/backendcap"
	"llama_shim/internal/compactor"
	"llama_shim/internal/config"
	"llama_shim/internal/imagegen"
	"llama_shim/internal/plugincontract"
	"llama_shim/internal/retrieval"
	"llama_shim/internal/storage"
	"llama_shim/internal/websearch"
)

func runtimeBackendPlugins(deps RouterDeps, probes capabilityProbeSet) []plugincontract.CapabilityPlugin {
	storageBackend := strings.ToLower(strings.TrimSpace(deps.StorageBackend))
	if storageBackend == "" && deps.Store != nil {
		storageBackend = storage.BackendSQLite
	}
	if storageBackend == "" {
		storageBackend = "none"
	}
	retrievalIndexBackend := strings.ToLower(strings.TrimSpace(deps.RetrievalIndexBackend))
	if retrievalIndexBackend == "" {
		retrievalIndexBackend = retrieval.IndexBackendLexical
	}
	retrievalEmbedderBackend := strings.ToLower(strings.TrimSpace(deps.RetrievalEmbedderBackend))
	if retrievalEmbedderBackend == "" {
		if deps.RetrievalEmbedder != nil {
			retrievalEmbedderBackend = "custom"
		} else {
			retrievalEmbedderBackend = retrieval.EmbedderBackendDisabled
		}
	}
	webSearchBackend := normalizedCapabilityBackend(deps.ResponsesWebSearchBackend, deps.WebSearchProvider != nil, websearch.BackendSearXNG)
	imageBackend := normalizedCapabilityBackend(deps.ResponsesImageGenerationBackend, deps.ImageGenerationProvider != nil, imagegen.BackendFixture)
	codeInterpreterBackend := localCodeInterpreterCapabilityBackend(deps.LocalCodeInterpreter)

	plugins := []plugincontract.CapabilityPlugin{
		newRegisteredCapabilityPlugin(backendcap.Component{
			ID:              "storage.primary",
			Category:        "storage",
			Kind:            "local_store",
			ConfigNamespace: "storage",
			Backend:         storageBackend,
			CapabilityClass: backendcap.ClassLocalSubset,
			Enabled:         true,
			Ready:           probeReady(probes.Storage),
			ReadinessProbe:  "storage",
			StateOwnership:  "shim_owned_store",
			PublicSurfaces: []string{
				"responses.retrieve",
				"responses.delete",
				"responses.input_items",
				"conversations",
				"chat_completions.stored",
				"files",
				"vector_stores",
				"containers",
			},
			Evidence: []string{
				"docs/v3-storage-retrieval-backends.md",
				"docs/v3-hard-delete-governance.md",
			},
		}, capabilityPluginOptions{
			CIFixtureSafe:      true,
			ProductionIntended: storageBackend != "none",
			Limits:             []string{"service_limits", "store_pagination_limits"},
		}),
		newRegisteredCapabilityPlugin(backendcap.Component{
			ID:              "retrieval.index",
			Category:        "retrieval_index",
			Kind:            "local_retrieval",
			ConfigNamespace: "retrieval.index",
			Backend:         retrievalIndexBackend,
			CapabilityClass: backendcap.ClassLocalSubset,
			Enabled:         true,
			Ready:           probeReady(probes.Storage) && probeReady(probes.RetrievalEmbedder),
			ReadinessProbe:  "storage,retrieval_embedder",
			StateOwnership:  "shim_owned_index",
			Tools:           []string{"file_search"},
			PublicSurfaces:  []string{"vector_stores.search", "responses.tools.file_search"},
			RoutingModes:    standardRoutingModes(),
			Evidence:        []string{"docs/v3-storage-retrieval-backends.md"},
		}, capabilityPluginOptions{
			CIFixtureSafe:      true,
			ProductionIntended: true,
			Limits:             []string{"retrieval_batch_limits", "vector_store_pagination_limits"},
		}),
		newRegisteredCapabilityPlugin(backendcap.Component{
			ID:              "retrieval.embedder",
			Category:        "embedder",
			Kind:            "retrieval_embedder",
			ConfigNamespace: "retrieval.embedder",
			Backend:         retrievalEmbedderBackend,
			CapabilityClass: backendcap.ClassLocalSubset,
			Enabled:         probes.RetrievalEmbedder.Enabled,
			Ready:           probes.RetrievalEmbedder.Enabled && probeReady(probes.RetrievalEmbedder),
			ReadinessProbe:  "retrieval_embedder",
			Tools:           []string{"file_search"},
			Evidence:        []string{"docs/v3-storage-retrieval-backends.md"},
		}, capabilityPluginOptions{
			CIFixtureSafe:      retrievalEmbedderBackend == retrieval.EmbedderBackendDisabled || retrievalEmbedderBackend == "custom",
			ProductionIntended: retrievalEmbedderBackend != retrieval.EmbedderBackendDisabled,
			Timeouts:           []string{"retrieval_embedder_timeout"},
		}),
		newRegisteredCapabilityPlugin(backendcap.Component{
			ID:              "runtime.compaction",
			Category:        "runtime",
			Kind:            "compaction",
			ConfigNamespace: "responses.compaction",
			Backend:         normalizedCompactionBackend(deps.ResponsesCompactionBackend),
			CapabilityClass: backendcap.ClassLocalSubset,
			Enabled:         true,
			Ready:           true,
			StateOwnership:  "shim_owned_opaque_state",
			PublicSurfaces:  []string{"responses.compact", "responses.context_management"},
			RoutingModes:    standardRoutingModes(),
			Evidence:        []string{"docs/v3-compaction.md"},
		}, capabilityPluginOptions{
			Kind:               "runtime",
			CIFixtureSafe:      normalizedCompactionBackend(deps.ResponsesCompactionBackend) == compactor.BackendHeuristic,
			ProductionIntended: true,
		}),
		newRegisteredCapabilityPlugin(backendcap.Component{
			ID:              "runtime.constrained_decoding",
			Category:        "runtime",
			Kind:            "constrained_decoding",
			ConfigNamespace: "responses.constrained_decoding",
			Backend:         normalizedCapabilityBackend(deps.ResponsesConstrainedDecodingBackend, true, config.ResponsesConstrainedDecodingBackendShimValidateRepair),
			CapabilityClass: constrainedDecodingRegistryClass(deps.ResponsesConstrainedDecodingBackend),
			Enabled:         true,
			Ready:           true,
			Tools:           []string{"custom", "structured_outputs"},
			RoutingModes:    standardRoutingModes(),
			Evidence:        []string{"docs/v3-constrained-decoding.md"},
		}, capabilityPluginOptions{
			Kind:               "runtime",
			CIFixtureSafe:      true,
			ProductionIntended: true,
			Limits:             []string{"custom_tool_grammar_definition_bytes", "custom_tool_compiled_pattern_bytes"},
		}),
		newRegisteredCapabilityPlugin(backendcap.Component{
			ID:              "transport.responses_websocket",
			Category:        "transport",
			Kind:            "responses_websocket",
			ConfigNamespace: "responses.websocket",
			CapabilityClass: backendcap.ClassLocalSubset,
			Enabled:         deps.ResponsesWebSocketEnabled,
			Ready:           deps.ResponsesWebSocketEnabled,
			WireModes:       []string{"websocket_responses"},
			PublicSurfaces:  []string{"responses.websocket.response_create"},
			Evidence:        []string{"docs/v3-websocket.md"},
		}, capabilityPluginOptions{
			CIFixtureSafe:      true,
			ProductionIntended: true,
			Timeouts:           []string{"websocket_idle_timeout"},
		}),
		newRegisteredCapabilityPlugin(backendcap.Component{
			ID:              "tool.web_search",
			Category:        "tool_runtime",
			Kind:            "web_search",
			ConfigNamespace: "responses.web_search",
			Backend:         webSearchBackend,
			CapabilityClass: backendcap.ClassLocalSubset,
			Enabled:         deps.WebSearchProvider != nil,
			Ready:           deps.WebSearchProvider != nil && probeReady(probes.WebSearchBackend),
			ReadinessProbe:  "web_search_backend",
			Tools:           []string{"web_search"},
			PublicSurfaces:  []string{"responses.tools.web_search"},
			RoutingModes:    standardRoutingModes(),
			Evidence:        []string{"docs/v3-local-runtimes.md"},
		}, capabilityPluginOptions{
			CIFixtureSafe:      webSearchBackend == websearch.BackendDisabled,
			ProductionIntended: webSearchBackend != websearch.BackendDisabled,
			Timeouts:           []string{"responses.web_search.timeout"},
			ErrorClasses:       localRuntimeErrorClasses(),
		}),
		newRegisteredCapabilityPlugin(backendcap.Component{
			ID:              "tool.image_generation",
			Category:        "tool_runtime",
			Kind:            "image_generation",
			ConfigNamespace: "responses.image_generation",
			Backend:         imageBackend,
			CapabilityClass: imageGenerationRegistryClass(deps.ResponsesImageGenerationBackend),
			Enabled:         deps.ImageGenerationProvider != nil,
			Ready:           deps.ImageGenerationProvider != nil && probeReady(probes.ImageGenerationBackend),
			ReadinessProbe:  "image_generation_backend",
			Tools:           []string{"image_generation"},
			PublicSurfaces:  []string{"responses.tools.image_generation"},
			RoutingModes:    standardRoutingModes(),
			Evidence:        []string{"docs/v3-image-backends.md"},
		}, capabilityPluginOptions{
			CIFixtureSafe:      imageBackend == imagegen.BackendFixture || imageBackend == imagegen.BackendDisabled,
			ProductionIntended: imageBackend != imagegen.BackendDisabled && imageBackend != imagegen.BackendFixture,
			Timeouts:           []string{"responses.image_generation.timeout", "responses.image_generation.comfyui.max_wait"},
			ErrorClasses:       localRuntimeErrorClasses(),
		}),
		newRegisteredCapabilityPlugin(backendcap.Component{
			ID:              "tool.computer",
			Category:        "tool_runtime",
			Kind:            "computer",
			ConfigNamespace: "responses.computer",
			Backend:         normalizedCapabilityBackend(deps.LocalComputer.Backend, deps.LocalComputer.Enabled(), LocalComputerBackendChatCompletions),
			CapabilityClass: backendcap.ClassLocalSubset,
			Enabled:         deps.LocalComputer.Enabled(),
			Ready:           deps.LocalComputer.Enabled() && probeReady(probes.ComputerRuntime),
			ReadinessProbe:  "computer_runtime",
			Tools:           []string{"computer"},
			PublicSurfaces:  []string{"responses.tools.computer"},
			RoutingModes:    standardRoutingModes(),
			Evidence:        []string{"docs/v3-local-runtimes.md", "docs/v3-computer-browser-harness.md"},
		}, capabilityPluginOptions{
			CIFixtureSafe:      true,
			ProductionIntended: true,
			Timeouts:           []string{"upstream_client_timeout", "computer_planner_timeout"},
			ErrorClasses:       localRuntimeErrorClasses(),
		}),
		newRegisteredCapabilityPlugin(backendcap.Component{
			ID:              "tool.code_interpreter",
			Category:        "tool_runtime",
			Kind:            "code_interpreter",
			ConfigNamespace: "responses.code_interpreter",
			Backend:         codeInterpreterBackend,
			CapabilityClass: backendcap.ClassLocalSubset,
			Enabled:         deps.LocalCodeInterpreter.Enabled(),
			Ready:           deps.LocalCodeInterpreter.Enabled(),
			Tools:           []string{"code_interpreter"},
			PublicSurfaces:  []string{"containers", "responses.tools.code_interpreter"},
			RoutingModes:    standardRoutingModes(),
			Evidence:        []string{"docs/v3-local-runtimes.md", "docs/engineering/runtime-hardening.md"},
		}, capabilityPluginOptions{
			CIFixtureSafe:      codeInterpreterBackend == config.ResponsesCodeInterpreterBackendDisabled,
			ProductionIntended: deps.LocalCodeInterpreter.Enabled(),
			Limits:             []string{"code_interpreter_generated_files", "code_interpreter_generated_file_bytes", "code_interpreter_generated_total_bytes", "code_interpreter_remote_input_file_bytes"},
			Timeouts:           []string{"responses.code_interpreter.execution_timeout", "responses.code_interpreter.cleanup_interval"},
			ErrorClasses:       localRuntimeErrorClasses(),
		}),
		newRegisteredCapabilityPlugin(backendcap.Component{
			ID:              "tool.shell",
			Category:        "tool_runtime",
			Kind:            "native_local_tool",
			ConfigNamespace: "responses.native_tools.shell",
			Backend:         "chat_completions_tool_loop",
			CapabilityClass: backendcap.ClassLocalSubset,
			Enabled:         true,
			Ready:           true,
			Tools:           []string{"shell"},
			PublicSurfaces:  []string{"responses.tools.shell"},
			RoutingModes:    standardRoutingModes(),
			Evidence:        []string{"docs/v3-coding-tools.md"},
		}, capabilityPluginOptions{
			Kind:               "tool_runtime",
			CIFixtureSafe:      true,
			ProductionIntended: true,
			Timeouts:           []string{"tool_loop_turn_timeout"},
			ErrorClasses:       localRuntimeErrorClasses(),
		}),
		newRegisteredCapabilityPlugin(backendcap.Component{
			ID:              "tool.apply_patch",
			Category:        "tool_runtime",
			Kind:            "native_local_tool",
			ConfigNamespace: "responses.native_tools.apply_patch",
			Backend:         "chat_completions_tool_loop",
			CapabilityClass: backendcap.ClassLocalSubset,
			Enabled:         true,
			Ready:           true,
			Tools:           []string{"apply_patch"},
			PublicSurfaces:  []string{"responses.tools.apply_patch"},
			RoutingModes:    standardRoutingModes(),
			Evidence:        []string{"docs/v3-coding-tools.md"},
		}, capabilityPluginOptions{
			Kind:               "tool_runtime",
			CIFixtureSafe:      true,
			ProductionIntended: true,
			Timeouts:           []string{"tool_loop_turn_timeout"},
			ErrorClasses:       localRuntimeErrorClasses(),
		}),
		newRegisteredCapabilityPlugin(backendcap.Component{
			ID:              "client_profile.codex",
			Category:        "client_profile",
			Kind:            "codex_cli",
			ConfigNamespace: "responses.codex",
			CapabilityClass: backendcap.ClassLocalSubset,
			Enabled:         deps.ResponsesCodexEnableCompatibility || len(deps.ResponsesCodexModelMetadata) > 0,
			Ready:           deps.ResponsesCodexEnableCompatibility || len(deps.ResponsesCodexModelMetadata) > 0,
			PublicSurfaces:  []string{"responses.create", "responses.websocket", "responses.compact"},
			Tools:           []string{"shell", "apply_patch"},
			WireModes:       []string{"responses_native", "websocket_responses"},
			Evidence:        []string{"docs/v3-codex-eval-harness.md", "docs/guides/codex-cli.md"},
		}, capabilityPluginOptions{
			Kind:               "client_profile",
			CIFixtureSafe:      true,
			ProductionIntended: true,
			Timeouts:           []string{"codex_eval_attempt_timeout"},
		}),
	}
	return plugins
}

func localRuntimeErrorClasses() []string {
	return []string{
		string(backendFailureBackendCapabilityMismatch),
		string(backendFailureLocalRuntimeUnavailable),
		string(backendFailureMalformedBackendResponse),
		string(backendFailureTransportTimeout),
		string(backendFailureTransportError),
	}
}
