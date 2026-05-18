package httpapi

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"time"

	"llama_shim/internal/backendcap"
	"llama_shim/internal/compactor"
	"llama_shim/internal/config"
	"llama_shim/internal/imagegen"
	"llama_shim/internal/memory"
	"llama_shim/internal/plugincontract"
	"llama_shim/internal/retrieval"
	"llama_shim/internal/storage"
	"llama_shim/internal/websearch"
)

type capabilityManifest struct {
	Object   string                  `json:"object"`
	Ready    bool                    `json:"ready"`
	Surfaces capabilitySurfaceSet    `json:"surfaces"`
	Runtime  capabilityRuntimeConfig `json:"runtime"`
	Tools    capabilityToolSet       `json:"tools"`
	Backends backendcap.Registry     `json:"backends"`
	Plugins  plugincontract.Registry `json:"plugins"`
	Probes   capabilityProbeSet      `json:"probes"`
}

type capabilitySurfaceSet struct {
	Responses       capabilityResponsesSurface   `json:"responses"`
	Conversations   capabilityConversationsRoute `json:"conversations"`
	ChatCompletions capabilityChatSurface        `json:"chat_completions"`
	Files           capabilitySimpleRoute        `json:"files"`
	VectorStores    capabilitySimpleRoute        `json:"vector_stores"`
	Containers      capabilityContainersSurface  `json:"containers"`
}

type capabilityResponsesSurface struct {
	Enabled        bool                         `json:"enabled"`
	Stateful       bool                         `json:"stateful"`
	Retrieve       bool                         `json:"retrieve"`
	Delete         bool                         `json:"delete"`
	Cancel         bool                         `json:"cancel"`
	InputItems     bool                         `json:"input_items"`
	CreateStream   bool                         `json:"create_stream"`
	RetrieveStream bool                         `json:"retrieve_stream"`
	InputTokens    bool                         `json:"input_tokens"`
	Compact        bool                         `json:"compact"`
	WebSocket      capabilityResponsesWebSocket `json:"websocket"`
	Mode           string                       `json:"mode"`
	Transport      string                       `json:"transport"`
}

type capabilityResponsesWebSocket struct {
	Enabled      bool   `json:"enabled"`
	Support      string `json:"support"`
	Endpoint     string `json:"endpoint"`
	Sequential   bool   `json:"sequential"`
	Multiplexing bool   `json:"multiplexing"`
}

type capabilityConversationsRoute struct {
	Enabled  bool `json:"enabled"`
	Create   bool `json:"create"`
	Retrieve bool `json:"retrieve"`
	Items    bool `json:"items"`
}

type capabilityChatSurface struct {
	Enabled                 bool `json:"enabled"`
	Stored                  bool `json:"stored"`
	DefaultStoreWhenOmitted bool `json:"default_store_when_omitted"`
}

type capabilitySimpleRoute struct {
	Enabled bool `json:"enabled"`
}

type capabilityContainersSurface struct {
	Enabled bool `json:"enabled"`
	Create  bool `json:"create"`
	Files   bool `json:"files"`
}

type capabilityRuntimeConfig struct {
	ResponsesMode                        string                              `json:"responses_mode"`
	ResponsesUpstreamTransport           string                              `json:"responses_upstream_transport"`
	CustomToolsMode                      string                              `json:"custom_tools_mode"`
	ChatCompletionsUpstreamCompatibility int                                 `json:"chat_completions_upstream_compatibility_rules"`
	UpstreamToolCompatibilityRules       int                                 `json:"upstream_tool_compatibility_rules"`
	Compaction                           capabilityCompactionConfig          `json:"compaction"`
	Memory                               capabilityMemoryConfig              `json:"memory"`
	ConstrainedDecoding                  capabilityConstrainedDecodingConfig `json:"constrained_decoding"`
	Codex                                capabilityCodexConfig               `json:"codex"`
	UpstreamProviderRouting              capabilityUpstreamProviderRouting   `json:"upstream_provider_routing"`
	Persistence                          capabilityPersistenceInfo           `json:"persistence"`
	Retrieval                            capabilityRetrievalConfig           `json:"retrieval"`
	Ops                                  capabilityOpsConfig                 `json:"ops"`
}

type capabilityUpstreamProviderRouting struct {
	Enabled       bool                                  `json:"enabled"`
	ProviderCount int                                   `json:"provider_count"`
	ModelCount    int                                   `json:"model_count"`
	Providers     []capabilityUpstreamProviderRoutingID `json:"providers"`
}

type capabilityUpstreamProviderRoutingID struct {
	ID         string   `json:"id"`
	PluginID   string   `json:"plugin_id,omitempty"`
	ModelCount int      `json:"model_count"`
	Models     []string `json:"models"`
}

type capabilityCompactionConfig struct {
	Enabled         bool              `json:"enabled"`
	Support         string            `json:"support"`
	Backend         string            `json:"backend"`
	CapabilityClass string            `json:"capability_class"`
	ModelConfigured bool              `json:"model_configured"`
	RetainedItems   int               `json:"retained_items,omitempty"`
	MaxInputChars   int               `json:"max_input_chars,omitempty"`
	Routing         capabilityRouting `json:"routing"`
}

type capabilityMemoryConfig struct {
	Enabled           bool              `json:"enabled"`
	Support           string            `json:"support"`
	Backend           string            `json:"backend"`
	CapabilityClass   string            `json:"capability_class"`
	InjectByDefault   bool              `json:"inject_by_default"`
	MaxNotes          int               `json:"max_notes"`
	MaxNoteBytes      int64             `json:"max_note_bytes"`
	MaxContextBytes   int64             `json:"max_context_bytes"`
	MetadataNamespace string            `json:"metadata_namespace"`
	Routing           capabilityRouting `json:"routing"`
}

type capabilityConstrainedDecodingConfig struct {
	Enabled         bool                                      `json:"enabled"`
	Support         string                                    `json:"support"`
	Runtime         string                                    `json:"runtime"`
	Backend         string                                    `json:"backend"`
	CapabilityClass string                                    `json:"capability_class"`
	NativeAvailable bool                                      `json:"native_available"`
	NativeBackend   string                                    `json:"native_backend"`
	NativeFormats   []string                                  `json:"native_formats"`
	Validation      string                                    `json:"validation"`
	Repair          string                                    `json:"repair"`
	CustomTools     capabilityConstrainedCustomToolsConfig    `json:"custom_tools"`
	Structured      capabilityConstrainedStructuredJSONConfig `json:"structured_outputs"`
	Routing         capabilityRouting                         `json:"routing"`
}

type capabilityConstrainedCustomToolsConfig struct {
	Enabled                   bool     `json:"enabled"`
	Formats                   []string `json:"formats"`
	LarkSubset                bool     `json:"lark_subset"`
	MaxGrammarDefinitionBytes int64    `json:"max_grammar_definition_bytes"`
	MaxCompiledPatternBytes   int64    `json:"max_compiled_pattern_bytes"`
}

type capabilityConstrainedStructuredJSONConfig struct {
	Enabled bool     `json:"enabled"`
	Formats []string `json:"formats"`
	Support string   `json:"support"`
}

type capabilityCodexConfig struct {
	CompatibilityEnabled            bool `json:"compatibility_enabled"`
	UpstreamInputCompatibilityRules int  `json:"upstream_input_compatibility_rules"`
	ModelMetadataModels             int  `json:"model_metadata_models"`
}

type capabilityPersistenceInfo struct {
	Backend              string `json:"backend"`
	ResponseStore        string `json:"response_store"`
	ConversationStore    string `json:"conversation_store"`
	ChatCompletionStore  string `json:"chat_completion_store"`
	FileStore            string `json:"file_store"`
	VectorStore          string `json:"vector_store"`
	CodeInterpreterStore string `json:"code_interpreter_store"`
	MemoryStore          string `json:"memory_store"`
	ExpectedDurable      bool   `json:"expected_durable"`
}

type capabilityRetrievalConfig struct {
	StorageBackend  string                       `json:"storage_backend"`
	IndexBackend    string                       `json:"index_backend"`
	EmbedderBackend string                       `json:"embedder_backend"`
	SemanticSearch  bool                         `json:"semantic_search"`
	HybridSearch    bool                         `json:"hybrid_search"`
	LocalRerank     bool                         `json:"local_rerank"`
	LazyRepair      bool                         `json:"lazy_repair"`
	ANNIndex        *capabilityRetrievalANNIndex `json:"ann_index,omitempty"`
}

type capabilityRetrievalANNIndex struct {
	Enabled    bool   `json:"enabled"`
	Method     string `json:"method"`
	Metric     string `json:"metric"`
	Dimensions int    `json:"dimensions"`
}

type capabilityOpsConfig struct {
	AuthMode             string                         `json:"auth_mode"`
	RateLimit            capabilityRateLimit            `json:"rate_limit"`
	Metrics              capabilityMetrics              `json:"metrics"`
	DebugTraces          capabilityDebugTraces          `json:"debug_traces"`
	Evidence             capabilityEvidence             `json:"evidence"`
	OperatorUI           capabilityOperatorUI           `json:"operator_ui"`
	BackendFailurePolicy []capabilityBackendFailureRule `json:"backend_failure_policy"`
	HealthPublic         bool                           `json:"health_public"`
	ReadyzPublic         bool                           `json:"readyz_public"`
}

type capabilityRateLimit struct {
	Enabled           bool `json:"enabled"`
	RequestsPerMinute int  `json:"requests_per_minute"`
	Burst             int  `json:"burst"`
}

type capabilityMetrics struct {
	Enabled bool   `json:"enabled"`
	Path    string `json:"path"`
}

type capabilityDebugTraces struct {
	Enabled        bool     `json:"enabled"`
	MaxEntries     int      `json:"max_entries"`
	ListEndpoint   string   `json:"list_endpoint"`
	DetailEndpoint string   `json:"detail_endpoint"`
	Redaction      string   `json:"redaction"`
	Captures       []string `json:"captures"`
}

type capabilityEvidence struct {
	Enabled           bool   `json:"enabled"`
	Root              string `json:"root"`
	MaxEntries        int    `json:"max_entries"`
	StaleAfterSeconds int64  `json:"stale_after_seconds"`
	ListEndpoint      string `json:"list_endpoint"`
	DetailEndpoint    string `json:"detail_endpoint"`
	Redaction         string `json:"redaction"`
	Support           string `json:"support"`
}

type capabilityOperatorUI struct {
	Enabled            bool   `json:"enabled"`
	BasePath           string `json:"base_path"`
	PublicStaticAssets bool   `json:"public_static_assets"`
	Support            string `json:"support"`
}

type capabilityBackendFailureRule struct {
	Class               string `json:"class"`
	Retryable           bool   `json:"retryable"`
	Cooldown            bool   `json:"cooldown"`
	CooldownHintSeconds int    `json:"cooldown_hint_seconds,omitempty"`
	FallbackAllowed     bool   `json:"fallback_allowed"`
	ClientStatus        int    `json:"client_status"`
	ClientType          string `json:"client_type"`
	ClientCode          string `json:"client_code,omitempty"`
}

type capabilityToolSet struct {
	FileSearch       capabilityTool `json:"file_search"`
	WebSearch        capabilityTool `json:"web_search"`
	ImageGeneration  capabilityTool `json:"image_generation"`
	Computer         capabilityTool `json:"computer"`
	CodeInterpreter  capabilityTool `json:"code_interpreter"`
	Shell            capabilityTool `json:"shell"`
	ApplyPatch       capabilityTool `json:"apply_patch"`
	MCPServerURL     capabilityTool `json:"mcp_server_url"`
	MCPConnectorID   capabilityTool `json:"mcp_connector_id"`
	ToolSearchHosted capabilityTool `json:"tool_search_hosted"`
	ToolSearchClient capabilityTool `json:"tool_search_client"`
}

type capabilityTool struct {
	Support     string            `json:"support"`
	Backend     string            `json:"backend,omitempty"`
	Enabled     bool              `json:"enabled"`
	Disposition string            `json:"disposition"`
	Routing     capabilityRouting `json:"routing"`
}

type capabilityRouting struct {
	PreferLocal    string `json:"prefer_local"`
	PreferUpstream string `json:"prefer_upstream"`
	LocalOnly      string `json:"local_only"`
}

type capabilityProbeSet struct {
	Storage                capabilityProbe            `json:"storage"`
	SQLite                 capabilityProbe            `json:"sqlite"`
	Postgres               capabilityProbe            `json:"postgres"`
	Llama                  capabilityProbe            `json:"llama"`
	Providers              map[string]capabilityProbe `json:"providers,omitempty"`
	RetrievalEmbedder      capabilityProbe            `json:"retrieval_embedder"`
	WebSearchBackend       capabilityProbe            `json:"web_search_backend"`
	ImageGenerationBackend capabilityProbe            `json:"image_generation_backend"`
	ComputerRuntime        capabilityProbe            `json:"computer_runtime"`
}

type capabilityProbe struct {
	Enabled bool   `json:"enabled"`
	Checked bool   `json:"checked"`
	Ready   bool   `json:"ready"`
	Error   string `json:"error,omitempty"`
}

func buildCapabilityManifest(ctx context.Context, deps RouterDeps) capabilityManifest {
	authConfig, err := normalizeStaticBearerAuthConfig(deps.Auth)
	if err != nil {
		authConfig = StaticBearerAuthConfig{Mode: config.ShimAuthModeDisabled}
	}
	rateLimitConfig, err := normalizeRateLimitConfig(deps.RateLimit)
	if err != nil {
		rateLimitConfig = RateLimitConfig{}
	}
	metricsConfig := normalizeMetricsConfig(deps.MetricsConfig)
	probes := collectCapabilityProbes(ctx, deps)
	responsesTransport := normalizeResponsesUpstreamTransport(deps.ResponsesUpstreamTransport)
	backends := capabilityBackendRegistry(deps, probes)
	plugins := capabilityPluginRegistry(deps, probes, responsesTransport, backends.Components)

	return capabilityManifest{
		Object: "shim.capabilities",
		Ready:  probes.ready() && !backendcap.HasErrors(backends.Issues) && !plugincontract.HasErrors(plugins.Issues),
		Surfaces: capabilitySurfaceSet{
			Responses: capabilityResponsesSurface{
				Enabled:        true,
				Stateful:       true,
				Retrieve:       true,
				Delete:         true,
				Cancel:         true,
				InputItems:     true,
				CreateStream:   true,
				RetrieveStream: true,
				InputTokens:    true,
				Compact:        true,
				WebSocket: capabilityResponsesWebSocket{
					Enabled:      deps.ResponsesWebSocketEnabled,
					Support:      "local_subset",
					Endpoint:     "/v1/responses",
					Sequential:   true,
					Multiplexing: false,
				},
				Mode:      deps.ResponsesMode,
				Transport: responsesTransport,
			},
			Conversations: capabilityConversationsRoute{
				Enabled:  true,
				Create:   true,
				Retrieve: true,
				Items:    true,
			},
			ChatCompletions: capabilityChatSurface{
				Enabled:                 true,
				Stored:                  true,
				DefaultStoreWhenOmitted: deps.ChatCompletionsStoreWhenOmitted,
			},
			Files:        capabilitySimpleRoute{Enabled: true},
			VectorStores: capabilitySimpleRoute{Enabled: true},
			Containers: capabilityContainersSurface{
				Enabled: true,
				Create:  deps.LocalCodeInterpreter.Enabled(),
				Files:   true,
			},
		},
		Runtime: capabilityRuntimeConfig{
			ResponsesMode:                        deps.ResponsesMode,
			ResponsesUpstreamTransport:           responsesTransport,
			CustomToolsMode:                      deps.ResponsesCustomToolsMode,
			ChatCompletionsUpstreamCompatibility: len(deps.ChatCompletionsUpstreamCompatibility),
			UpstreamToolCompatibilityRules:       len(deps.ResponsesUpstreamToolCompatibility),
			Compaction:                           compactionCapability(deps),
			Memory:                               memoryCapability(deps),
			ConstrainedDecoding:                  constrainedDecodingCapability(deps),
			Codex: capabilityCodexConfig{
				CompatibilityEnabled:            deps.ResponsesCodexEnableCompatibility,
				UpstreamInputCompatibilityRules: len(deps.ResponsesCodexUpstreamInputCompatibility),
				ModelMetadataModels:             len(deps.ResponsesCodexModelMetadata),
			},
			UpstreamProviderRouting: upstreamProviderRoutingCapability(deps),
			Persistence:             capabilityPersistence(deps),
			Retrieval:               capabilityRetrieval(deps),
			Ops: capabilityOpsConfig{
				AuthMode: normalizedCapabilityAuthMode(authConfig.Mode),
				RateLimit: capabilityRateLimit{
					Enabled:           rateLimitConfig.Enabled,
					RequestsPerMinute: rateLimitConfig.RequestsPerMinute,
					Burst:             rateLimitConfig.Burst,
				},
				Metrics: capabilityMetrics{
					Enabled: metricsConfig.Enabled,
					Path:    metricsConfig.Path,
				},
				DebugTraces:          debugTraceCapability(deps.DebugTrace),
				Evidence:             evidenceCapability(deps.Evidence),
				OperatorUI:           operatorUICapability(deps.UI),
				BackendFailurePolicy: capabilityBackendFailurePolicy(),
				HealthPublic:         true,
				ReadyzPublic:         true,
			},
		},
		Tools: capabilityToolSet{
			FileSearch: capabilityTool{
				Support:     "local_subset",
				Enabled:     true,
				Disposition: "local_execute",
				Routing: capabilityRouting{
					PreferLocal:    "local_subset",
					PreferUpstream: "proxy_first",
					LocalOnly:      "local_subset_or_validation_error",
				},
			},
			WebSearch: capabilityTool{
				Support:     "local_subset_when_configured",
				Backend:     normalizedCapabilityBackend(deps.ResponsesWebSearchBackend, deps.WebSearchProvider != nil, "configured"),
				Enabled:     deps.WebSearchProvider != nil,
				Disposition: "local_execute",
				Routing: capabilityRouting{
					PreferLocal:    "local_subset_or_upstream_fallback",
					PreferUpstream: "proxy_first",
					LocalOnly:      "local_subset_or_explicit_local_only_error",
				},
			},
			ImageGeneration: capabilityTool{
				Support:     "local_subset_when_configured",
				Backend:     normalizedCapabilityBackend(deps.ResponsesImageGenerationBackend, deps.ImageGenerationProvider != nil, "configured"),
				Enabled:     deps.ImageGenerationProvider != nil,
				Disposition: "local_execute",
				Routing: capabilityRouting{
					PreferLocal:    "local_subset_or_upstream_fallback",
					PreferUpstream: "proxy_first",
					LocalOnly:      "local_subset_or_explicit_disabled_runtime_error",
				},
			},
			Computer: capabilityTool{
				Support:     "local_subset_when_configured",
				Backend:     normalizedCapabilityBackend(deps.LocalComputer.Backend, deps.LocalComputer.Enabled(), "configured"),
				Enabled:     deps.LocalComputer.Enabled(),
				Disposition: "local_execute",
				Routing: capabilityRouting{
					PreferLocal:    "local_subset",
					PreferUpstream: "proxy_first",
					LocalOnly:      "local_subset_or_explicit_disabled_runtime_error",
				},
			},
			CodeInterpreter: capabilityTool{
				Support:     "local_subset_when_configured",
				Backend:     localCodeInterpreterCapabilityBackend(deps.LocalCodeInterpreter),
				Enabled:     deps.LocalCodeInterpreter.Enabled(),
				Disposition: "local_execute",
				Routing: capabilityRouting{
					PreferLocal:    "local_subset",
					PreferUpstream: "proxy_first",
					LocalOnly:      "local_subset_or_explicit_disabled_runtime_error",
				},
			},
			Shell: capabilityTool{
				Support:     "native_local_subset",
				Backend:     "chat_completions_tool_loop",
				Enabled:     true,
				Disposition: "local_execute",
				Routing: capabilityRouting{
					PreferLocal:    "local_subset_or_validation_error",
					PreferUpstream: "proxy_first",
					LocalOnly:      "local_subset_or_validation_error",
				},
			},
			ApplyPatch: capabilityTool{
				Support:     "native_local_subset",
				Backend:     "chat_completions_tool_loop",
				Enabled:     true,
				Disposition: "local_execute",
				Routing: capabilityRouting{
					PreferLocal:    "local_subset",
					PreferUpstream: "proxy_first",
					LocalOnly:      "local_subset",
				},
			},
			MCPServerURL: capabilityTool{
				Support:     "local_subset",
				Enabled:     true,
				Disposition: "local_execute",
				Routing: capabilityRouting{
					PreferLocal:    "local_subset",
					PreferUpstream: "proxy_first",
					LocalOnly:      "local_subset_or_explicit_validation_error",
				},
			},
			MCPConnectorID: capabilityTool{
				Support:     "proxy_only",
				Enabled:     true,
				Disposition: "proxy_only",
				Routing: capabilityRouting{
					PreferLocal:    "proxy_only_bridge",
					PreferUpstream: "proxy_only_bridge",
					LocalOnly:      "reject_with_mcp_validation_error",
				},
			},
			ToolSearchHosted: capabilityTool{
				Support:     "local_subset",
				Enabled:     true,
				Disposition: "local_execute",
				Routing: capabilityRouting{
					PreferLocal:    "local_subset",
					PreferUpstream: "proxy_first",
					LocalOnly:      "local_subset",
				},
			},
			ToolSearchClient: capabilityTool{
				Support:     "proxy_only",
				Enabled:     true,
				Disposition: "client_round_trip",
				Routing: capabilityRouting{
					PreferLocal:    "proxy_only",
					PreferUpstream: "proxy_only",
					LocalOnly:      "reject_with_tool_search_validation_error",
				},
			},
		},
		Backends: backends,
		Plugins:  plugins,
		Probes:   probes,
	}
}

func compactionCapability(deps RouterDeps) capabilityCompactionConfig {
	backend := strings.ToLower(strings.TrimSpace(deps.ResponsesCompactionBackend))
	if backend == "" {
		backend = compactor.BackendHeuristic
	}
	retainedItems := deps.ResponsesCompactionRetainedItems
	maxInputChars := deps.ResponsesCompactionMaxInputRunes
	if backend == compactor.BackendHeuristic {
		retainedItems = 0
		maxInputChars = 0
	}
	return capabilityCompactionConfig{
		Enabled:         true,
		Support:         "local_subset",
		Backend:         backend,
		CapabilityClass: backend,
		ModelConfigured: strings.TrimSpace(deps.ResponsesCompactionModel) != "",
		RetainedItems:   retainedItems,
		MaxInputChars:   maxInputChars,
		Routing: capabilityRouting{
			PreferLocal:    "local_subset",
			PreferUpstream: "proxy_first_or_local_state",
			LocalOnly:      "local_subset",
		},
	}
}

func memoryCapability(deps RouterDeps) capabilityMemoryConfig {
	cfg, _ := memory.NormalizeConfig(memory.Config{
		Backend:           deps.ResponsesMemoryBackend,
		Inject:            deps.ResponsesMemoryInject,
		MaxNotes:          deps.ResponsesMemoryMaxNotes,
		MaxNoteBytes:      int(deps.ResponsesMemoryMaxNoteBytes),
		MaxContextBytes:   int(deps.ResponsesMemoryMaxContextBytes),
		MetadataNamespace: deps.ResponsesMemoryMetadataNamespace,
	})
	enabled := cfg.Enabled()
	class := "none"
	if enabled {
		class = "local_subset"
	}
	return capabilityMemoryConfig{
		Enabled:           enabled,
		Support:           "shim_owned_extension",
		Backend:           cfg.Backend,
		CapabilityClass:   class,
		InjectByDefault:   cfg.Inject,
		MaxNotes:          cfg.MaxNotes,
		MaxNoteBytes:      int64(cfg.MaxNoteBytes),
		MaxContextBytes:   int64(cfg.MaxContextBytes),
		MetadataNamespace: cfg.MetadataNamespace,
		Routing: capabilityRouting{
			PreferLocal:    "local_extension",
			PreferUpstream: "skipped_for_pure_proxy_or_shadow_store",
			LocalOnly:      "local_extension",
		},
	}
}

func upstreamProviderRoutingCapability(deps RouterDeps) capabilityUpstreamProviderRouting {
	if len(deps.LlamaProviders) == 0 {
		return capabilityUpstreamProviderRouting{}
	}
	providers := make([]capabilityUpstreamProviderRoutingID, 0, len(deps.LlamaProviders))
	modelCount := 0
	for _, provider := range deps.LlamaProviders {
		providerID := strings.TrimSpace(provider.ID)
		if providerID == "" {
			continue
		}
		models := make([]string, 0, len(provider.Models))
		for _, model := range provider.Models {
			suffix := strings.TrimSpace(model.Model)
			if suffix == "" {
				continue
			}
			models = append(models, providerID+"/"+suffix)
		}
		sort.Strings(models)
		modelCount += len(models)
		providers = append(providers, capabilityUpstreamProviderRoutingID{
			ID:         providerID,
			PluginID:   upstreamProviderPluginID(providerID),
			ModelCount: len(models),
			Models:     models,
		})
	}
	sort.Slice(providers, func(i, j int) bool {
		return providers[i].ID < providers[j].ID
	})
	return capabilityUpstreamProviderRouting{
		Enabled:       len(providers) > 0,
		ProviderCount: len(providers),
		ModelCount:    modelCount,
		Providers:     providers,
	}
}

func debugTraceCapability(cfg DebugTraceConfig) capabilityDebugTraces {
	cfg = normalizeDebugTraceConfig(cfg)
	return capabilityDebugTraces{
		Enabled:        cfg.Enabled,
		MaxEntries:     cfg.MaxEntries,
		ListEndpoint:   "/debug/traces",
		DetailEndpoint: "/debug/traces/{request_id}",
		Redaction:      "metadata_only_no_prompts_no_headers_no_file_contents",
		Captures: []string{
			"request_id",
			"route",
			"surface",
			"source_format",
			"model",
			"provider_route",
			"plugin_contract",
			"routing_mode",
			"selected_backend",
			"tool_classifier_decisions",
			"backend_failure_class",
			"fallback_decision",
			"rate_limit_decision",
			"final_status",
		},
	}
}

func evidenceCapability(cfg EvidenceConfig) capabilityEvidence {
	cfg = normalizeEvidenceConfig(cfg)
	return capabilityEvidence{
		Enabled:           cfg.Enabled,
		Root:              slashPath(cfg.Root),
		MaxEntries:        cfg.MaxEntries,
		StaleAfterSeconds: int64(cfg.StaleAfter.Seconds()),
		ListEndpoint:      "/debug/evidence",
		DetailEndpoint:    "/debug/evidence/{id}",
		Redaction:         evidenceRedactionPolicy,
		Support:           "read_only_summary_artifacts",
	}
}

func operatorUICapability(cfg UIConfig) capabilityOperatorUI {
	cfg = normalizeUIConfig(cfg)
	return capabilityOperatorUI{
		Enabled:            cfg.Enabled,
		BasePath:           cfg.BasePath,
		PublicStaticAssets: cfg.PublicStaticAssets,
		Support:            "shim_owned_read_only_operator_console",
	}
}

func capabilityBackendRegistry(deps RouterDeps, probes capabilityProbeSet) backendcap.Registry {
	responsesTransport := normalizeResponsesUpstreamTransport(deps.ResponsesUpstreamTransport)
	components := plugincontract.ComponentsFromPlugins(capabilityPlugins(deps, probes, responsesTransport)...)
	return backendcap.NewRegistry(components...)
}

func capabilityPluginRegistry(deps RouterDeps, probes capabilityProbeSet, responsesTransport string, components []backendcap.Component) plugincontract.Registry {
	return plugincontract.NewRegistryFromPluginsForComponents(components, capabilityPlugins(deps, probes, responsesTransport)...)
}

func capabilityPlugins(deps RouterDeps, probes capabilityProbeSet, responsesTransport string) []plugincontract.CapabilityPlugin {
	plugins := runtimeBackendPlugins(deps, probes)
	plugins = append(plugins, modelProviderPlugins(deps, probes, responsesTransport)...)
	return plugins
}

func normalizedCompactionBackend(backend string) string {
	backend = strings.ToLower(strings.TrimSpace(backend))
	if backend == "" {
		return compactor.BackendHeuristic
	}
	return backend
}

func constrainedDecodingRegistryClass(backend string) string {
	if _, ok := constrainedCustomToolBackendCapabilityFor(backend); ok {
		return backendcap.ClassNative
	}
	return backendcap.ClassRepairOrValidate
}

func imageGenerationRegistryClass(backend string) string {
	switch strings.ToLower(strings.TrimSpace(backend)) {
	case imagegen.BackendResponses:
		return backendcap.ClassProxyOnly
	case "":
		return backendcap.ClassLocalSubset
	default:
		return backendcap.ClassLocalSubset
	}
}

func modelBackendRegistryClass(responsesTransport string) string {
	if responsesTransport == config.ResponsesUpstreamTransportChatCompletions {
		return backendcap.ClassChatProjection
	}
	return backendcap.ClassNative
}

func providerAuthSummary(provider config.LlamaProvider) string {
	switch {
	case strings.TrimSpace(provider.BearerTokenEnv) != "":
		return "bearer_env"
	case strings.TrimSpace(provider.BearerToken) != "":
		return "bearer_configured"
	default:
		return "none"
	}
}

func providerSecretRefs(provider config.LlamaProvider) []string {
	if strings.TrimSpace(provider.BearerTokenEnv) == "" {
		return nil
	}
	return []string{provider.BearerTokenEnv}
}

func standardRoutingModes() []string {
	return []string{
		config.ResponsesModePreferLocal,
		config.ResponsesModePreferUpstream,
		config.ResponsesModeLocalOnly,
	}
}

func capabilityPersistence(deps RouterDeps) capabilityPersistenceInfo {
	backend := strings.ToLower(strings.TrimSpace(deps.StorageBackend))
	if backend == "" && deps.Store != nil {
		backend = storage.BackendSQLite
	}
	if backend == "" {
		backend = "none"
	}
	return capabilityPersistenceInfo{
		Backend:              backend,
		ResponseStore:        persistenceStoreBackend(backend, "response"),
		ConversationStore:    persistenceStoreBackend(backend, "conversation"),
		ChatCompletionStore:  persistenceStoreBackend(backend, "chat_completion"),
		FileStore:            persistenceStoreBackend(backend, "file"),
		VectorStore:          persistenceStoreBackend(backend, "vector"),
		CodeInterpreterStore: persistenceStoreBackend(backend, "code_interpreter"),
		MemoryStore:          persistenceStoreBackend(backend, "memory"),
		ExpectedDurable:      deps.Store != nil,
	}
}

func persistenceStoreBackend(backend, surface string) string {
	if backend != storage.BackendPostgres {
		return backend
	}
	switch surface {
	case "response", "conversation", "chat_completion", "file", "vector", "memory":
		return storage.BackendPostgres
	default:
		return storage.BackendSQLite + "_sidecar"
	}
}

func capabilityRetrieval(deps RouterDeps) capabilityRetrievalConfig {
	storageBackend := strings.ToLower(strings.TrimSpace(deps.StorageBackend))
	if storageBackend == "" && deps.Store != nil {
		storageBackend = storage.BackendSQLite
	}
	if storageBackend == "" {
		storageBackend = "none"
	}
	embedderBackend := strings.ToLower(strings.TrimSpace(deps.RetrievalEmbedderBackend))
	if embedderBackend == "" {
		if deps.RetrievalEmbedder != nil {
			embedderBackend = "custom"
		} else {
			embedderBackend = retrieval.EmbedderBackendDisabled
		}
	}
	indexCapabilities := retrieval.IndexCapabilitiesForConfig(retrieval.Config{
		IndexBackend: strings.ToLower(strings.TrimSpace(deps.RetrievalIndexBackend)),
	}, deps.RetrievalEmbedder != nil)
	if reporter, ok := deps.Store.(storage.RetrievalIndexReporter); ok {
		if reported := reporter.RetrievalIndexCapabilities(); reported.Backend != "" {
			indexCapabilities = reported
		}
	}
	out := capabilityRetrievalConfig{
		StorageBackend:  storageBackend,
		IndexBackend:    indexCapabilities.Backend,
		EmbedderBackend: embedderBackend,
		SemanticSearch:  indexCapabilities.SemanticSearch,
		HybridSearch:    indexCapabilities.HybridSearch,
		LocalRerank:     indexCapabilities.LocalRerank,
		LazyRepair:      indexCapabilities.LazyRepair,
	}
	if indexCapabilities.ANNIndex != nil && indexCapabilities.ANNIndex.Enabled {
		out.ANNIndex = &capabilityRetrievalANNIndex{
			Enabled:    true,
			Method:     indexCapabilities.ANNIndex.Method,
			Metric:     indexCapabilities.ANNIndex.Metric,
			Dimensions: indexCapabilities.ANNIndex.Dimensions,
		}
	}
	return out
}

func constrainedDecodingCapability(deps RouterDeps) capabilityConstrainedDecodingConfig {
	limits := normalizeServiceLimits(deps.ServiceLimits)
	capability := capabilityConstrainedDecodingConfig{
		Enabled:         true,
		Support:         "shim_validate_repair",
		Runtime:         "chat_completions_json_schema_hint",
		Backend:         normalizedCapabilityBackend("", deps.LlamaClient != nil, "configured_llama_chat_completions"),
		CapabilityClass: "none",
		NativeAvailable: false,
		NativeBackend:   "none",
		NativeFormats:   []string{},
		Validation:      "local_regex_validation",
		Repair:          "local_retry_when_invalid_or_timeout",
		CustomTools: capabilityConstrainedCustomToolsConfig{
			Enabled:                   true,
			Formats:                   []string{"grammar.regex", "grammar.lark_subset"},
			LarkSubset:                true,
			MaxGrammarDefinitionBytes: limits.CustomToolGrammarDefinitionBytes,
			MaxCompiledPatternBytes:   limits.CustomToolCompiledPatternBytes,
		},
		Structured: capabilityConstrainedStructuredJSONConfig{
			Enabled: true,
			Formats: []string{
				"text.format=json_object",
				"text.format=json_schema_subset",
				"chat.response_format=json_object",
				"chat.response_format=json_schema_subset",
			},
			Support: "local_validation_and_normalization",
		},
		Routing: capabilityRouting{
			PreferLocal:    "shim_validate_repair_or_upstream_fallback",
			PreferUpstream: "proxy_first",
			LocalOnly:      "shim_validate_repair_or_validation_error",
		},
	}
	if backendCapability, ok := constrainedCustomToolBackendCapabilityFor(deps.ResponsesConstrainedDecodingBackend); ok {
		capability.Support = backendCapability.Support
		capability.Runtime = backendCapability.Runtime
		capability.Backend = backendCapability.Backend
		capability.CapabilityClass = backendCapability.CapabilityClass
		capability.NativeAvailable = true
		capability.NativeBackend = backendCapability.NativeBackend
		capability.NativeFormats = append([]string(nil), backendCapability.NativeFormats...)
		capability.Validation = backendCapability.Validation
		capability.Repair = backendCapability.Repair
		capability.Routing = backendCapability.Routing
	}
	return capability
}

func collectCapabilityProbes(ctx context.Context, deps RouterDeps) capabilityProbeSet {
	storageBackend := strings.ToLower(strings.TrimSpace(deps.StorageBackend))
	if storageBackend == "" {
		storageBackend = storage.BackendSQLite
	}
	probes := capabilityProbeSet{
		Storage: capabilityProbe{
			Enabled: true,
			Checked: true,
			Ready:   deps.Store != nil,
			Error:   probeErrorMessage(deps.Store == nil, "storage backend is not ready"),
		},
		SQLite: capabilityProbe{
			Enabled: storageBackend == storage.BackendSQLite,
			Checked: storageBackend == storage.BackendSQLite,
			Ready:   deps.Store != nil,
			Error:   probeErrorMessage(deps.Store == nil && storageBackend == storage.BackendSQLite, "sqlite is not ready"),
		},
		Postgres: capabilityProbe{
			Enabled: storageBackend == storage.BackendPostgres,
			Checked: storageBackend == storage.BackendPostgres,
			Ready:   deps.Store != nil,
			Error:   probeErrorMessage(deps.Store == nil && storageBackend == storage.BackendPostgres, "postgres is not ready"),
		},
		Llama: capabilityProbe{
			Enabled: true,
			Checked: true,
			Ready:   deps.LlamaClient != nil,
			Error:   probeErrorMessage(deps.LlamaClient == nil, "llama backend is not ready"),
		},
	}

	if deps.Store != nil {
		probeStart := time.Now()
		if err := deps.Store.PingContext(ctx); err != nil {
			observeReadinessProbe(deps.Metrics, "capabilities", "storage", probeStart, err)
			probes.Storage.Ready = false
			probes.Storage.Error = "storage backend is not ready"
			if probes.SQLite.Enabled {
				probes.SQLite.Ready = false
				probes.SQLite.Error = "sqlite is not ready"
			}
			if probes.Postgres.Enabled {
				probes.Postgres.Ready = false
				probes.Postgres.Error = "postgres is not ready"
			}
		} else {
			observeReadinessProbe(deps.Metrics, "capabilities", "storage", probeStart, nil)
			probes.Storage.Error = ""
			if probes.SQLite.Enabled {
				probes.SQLite.Error = ""
			}
			if probes.Postgres.Enabled {
				probes.Postgres.Error = ""
			}
		}
	} else {
		observeReadinessProbeOutcome(deps.Metrics, "capabilities", "storage", "unready")
	}

	if deps.LlamaClient != nil {
		upstreamTimeout := readyzUpstreamTimeout
		resolver := newUpstreamProviderResolver(deps.LlamaProviders)
		if resolver.Enabled() {
			upstreamTimeout = readyzProviderTimeout
		}
		upstreamCtx, cancel := context.WithTimeout(ctx, upstreamTimeout)
		probeStart := time.Now()
		var err error
		if resolver.Enabled() {
			readiness := resolver.ProviderReadiness(upstreamCtx, deps.LlamaClient)
			probes.Providers = providerCapabilityProbes(deps.LlamaProviders, readiness)
			err = providerReadinessAggregateError(readiness)
		} else {
			err = deps.LlamaClient.CheckReadyWithBearerToken(upstreamCtx, deps.LlamaReadinessBearerToken)
		}
		cancel()
		if err != nil {
			observeReadinessProbe(deps.Metrics, "capabilities", "llama", probeStart, err)
			probes.Llama.Ready = false
			probes.Llama.Error = "llama backend is not ready"
		} else {
			observeReadinessProbe(deps.Metrics, "capabilities", "llama", probeStart, nil)
			probes.Llama.Error = ""
		}
	} else {
		observeReadinessProbeOutcome(deps.Metrics, "capabilities", "llama", "unready")
	}

	probes.ComputerRuntime = capabilityProbe{
		Enabled: deps.LocalComputer.Enabled(),
		Checked: deps.LocalComputer.Enabled(),
		Ready:   !deps.LocalComputer.Enabled() || probes.Llama.Ready,
	}
	if probes.ComputerRuntime.Enabled {
		switch {
		case deps.LlamaClient == nil:
			probes.ComputerRuntime.Ready = false
			probes.ComputerRuntime.Error = "computer planner backend is not configured"
		case !probes.Llama.Ready:
			probes.ComputerRuntime.Error = "computer planner backend is not ready"
		default:
			probes.ComputerRuntime.Error = ""
		}
	}

	probes.RetrievalEmbedder = capabilityProbe{
		Enabled: deps.RetrievalIndexBackend == retrieval.IndexBackendSQLiteVec || deps.RetrievalIndexBackend == retrieval.IndexBackendPGVector,
	}
	if probes.RetrievalEmbedder.Enabled {
		if checker, ok := deps.RetrievalEmbedder.(retrieval.ReadyChecker); ok {
			retrievalCtx, cancel := context.WithTimeout(ctx, readyzUpstreamTimeout)
			probeStart := time.Now()
			err := checker.CheckReady(retrievalCtx)
			cancel()
			observeReadinessProbe(deps.Metrics, "capabilities", "retrieval_embedder", probeStart, err)
			probes.RetrievalEmbedder.Checked = true
			probes.RetrievalEmbedder.Ready = err == nil
			probes.RetrievalEmbedder.Error = probeErrorMessage(err != nil, "retrieval embedder is not ready")
		} else {
			probes.RetrievalEmbedder.Ready = true
		}
	}

	probes.WebSearchBackend = capabilityProbe{
		Enabled: deps.WebSearchProvider != nil,
		Ready:   deps.WebSearchProvider != nil,
	}
	if checker, ok := deps.WebSearchProvider.(websearch.ReadyChecker); ok {
		webSearchCtx, cancel := context.WithTimeout(ctx, readyzUpstreamTimeout)
		probeStart := time.Now()
		err := checker.CheckReady(webSearchCtx)
		cancel()
		observeReadinessProbe(deps.Metrics, "capabilities", "web_search_backend", probeStart, err)
		probes.WebSearchBackend.Checked = true
		probes.WebSearchBackend.Ready = err == nil
		probes.WebSearchBackend.Error = probeErrorMessage(err != nil, "web search backend is not ready")
	}

	probes.ImageGenerationBackend = capabilityProbe{
		Enabled: deps.ImageGenerationProvider != nil,
	}
	if deps.ImageGenerationProvider != nil {
		imageCtx, cancel := context.WithTimeout(ctx, readyzUpstreamTimeout)
		probeStart := time.Now()
		err := deps.ImageGenerationProvider.CheckReady(imageCtx)
		cancel()
		observeReadinessProbe(deps.Metrics, "capabilities", "image_generation_backend", probeStart, err)
		probes.ImageGenerationBackend.Checked = true
		probes.ImageGenerationBackend.Ready = err == nil
		probes.ImageGenerationBackend.Error = probeErrorMessage(err != nil, "image generation backend is not ready")
	}

	return probes
}

func providerCapabilityProbes(providers []config.LlamaProvider, readiness map[string]error) map[string]capabilityProbe {
	if len(providers) == 0 {
		return nil
	}
	providerIDs := make([]string, 0, len(providers))
	seen := make(map[string]struct{}, len(providers))
	for _, provider := range providers {
		providerID := strings.TrimSpace(provider.ID)
		if providerID == "" {
			continue
		}
		if _, ok := seen[providerID]; ok {
			continue
		}
		seen[providerID] = struct{}{}
		providerIDs = append(providerIDs, providerID)
	}
	sort.Strings(providerIDs)
	if len(providerIDs) == 0 {
		return nil
	}
	out := make(map[string]capabilityProbe, len(providerIDs))
	for _, providerID := range providerIDs {
		err, checked := readiness[providerID]
		probe := capabilityProbe{
			Enabled: true,
			Checked: checked,
			Ready:   checked && err == nil,
		}
		if checked && err != nil {
			probe.Error = "provider backend is not ready"
		}
		out[providerID] = probe
	}
	return out
}

func (p capabilityProbeSet) ready() bool {
	return probeReady(p.Storage) &&
		probeReady(p.SQLite) &&
		probeReady(p.Postgres) &&
		probeReady(p.Llama) &&
		probeReady(p.RetrievalEmbedder) &&
		probeReady(p.WebSearchBackend) &&
		probeReady(p.ImageGenerationBackend) &&
		probeReady(p.ComputerRuntime)
}

func probeReady(probe capabilityProbe) bool {
	if !probe.Enabled || !probe.Checked {
		return true
	}
	return probe.Ready
}

func probeErrorMessage(include bool, message string) string {
	if !include {
		return ""
	}
	return message
}

func capabilityBackendFailurePolicy() []capabilityBackendFailureRule {
	classes := []backendFailureClass{
		backendFailureAuthFailure,
		backendFailurePermissionFailure,
		backendFailureQuotaExhausted,
		backendFailureRateLimitRetryable,
		backendFailureModelUnavailable,
		backendFailureUnsupportedToolOrParam,
		backendFailureTransportTimeout,
		backendFailureStreamIdleTimeout,
		backendFailureMalformedBackendResponse,
		backendFailureBackendCapabilityMismatch,
		backendFailureLocalRuntimeUnavailable,
		backendFailureTransportError,
		backendFailureUpstreamServerError,
	}
	out := make([]capabilityBackendFailureRule, 0, len(classes))
	for _, class := range classes {
		decision := backendFailureDecisionFor(class)
		out = append(out, capabilityBackendFailureRule{
			Class:               string(decision.Class),
			Retryable:           decision.Retryable,
			Cooldown:            decision.Cooldown,
			CooldownHintSeconds: decision.CooldownHintSeconds,
			FallbackAllowed:     decision.FallbackAllowed,
			ClientStatus:        decision.ClientStatus,
			ClientType:          decision.ClientType,
			ClientCode:          decision.ClientCode,
		})
	}
	return out
}

func normalizedCapabilityAuthMode(mode string) string {
	if strings.TrimSpace(mode) == "" {
		return config.ShimAuthModeDisabled
	}
	return mode
}

func normalizedCapabilityBackend(backend string, enabled bool, fallback string) string {
	normalized := strings.TrimSpace(backend)
	switch {
	case normalized != "":
		return normalized
	case enabled:
		return fallback
	default:
		return "disabled"
	}
}

func localCodeInterpreterCapabilityBackend(runtime LocalCodeInterpreterRuntimeConfig) string {
	if runtime.Backend == nil {
		return config.ResponsesCodeInterpreterBackendDisabled
	}
	if kind := strings.TrimSpace(runtime.Backend.Kind()); kind != "" {
		return kind
	}
	return "configured"
}

func capabilityHandler(deps RouterDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			WriteError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed", "")
			return
		}
		WriteJSON(w, http.StatusOK, buildCapabilityManifest(r.Context(), deps))
	}
}
