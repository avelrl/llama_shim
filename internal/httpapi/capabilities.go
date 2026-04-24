package httpapi

import (
	"context"
	"net/http"
	"strings"

	"llama_shim/internal/config"
	"llama_shim/internal/retrieval"
	"llama_shim/internal/websearch"
)

type capabilityManifest struct {
	Object   string                  `json:"object"`
	Ready    bool                    `json:"ready"`
	Surfaces capabilitySurfaceSet    `json:"surfaces"`
	Runtime  capabilityRuntimeConfig `json:"runtime"`
	Tools    capabilityToolSet       `json:"tools"`
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
	ResponsesMode   string                    `json:"responses_mode"`
	CustomToolsMode string                    `json:"custom_tools_mode"`
	Codex           capabilityCodexConfig     `json:"codex"`
	Persistence     capabilityPersistenceInfo `json:"persistence"`
	Retrieval       capabilityRetrievalConfig `json:"retrieval"`
	Ops             capabilityOpsConfig       `json:"ops"`
}

type capabilityCodexConfig struct {
	CompatibilityEnabled    bool `json:"compatibility_enabled"`
	ForceToolChoiceRequired bool `json:"force_tool_choice_required"`
}

type capabilityPersistenceInfo struct {
	Backend         string `json:"backend"`
	ExpectedDurable bool   `json:"expected_durable"`
}

type capabilityRetrievalConfig struct {
	IndexBackend string `json:"index_backend"`
}

type capabilityOpsConfig struct {
	AuthMode     string              `json:"auth_mode"`
	RateLimit    capabilityRateLimit `json:"rate_limit"`
	Metrics      capabilityMetrics   `json:"metrics"`
	HealthPublic bool                `json:"health_public"`
	ReadyzPublic bool                `json:"readyz_public"`
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
	Support string            `json:"support"`
	Backend string            `json:"backend,omitempty"`
	Enabled bool              `json:"enabled"`
	Routing capabilityRouting `json:"routing"`
}

type capabilityRouting struct {
	PreferLocal    string `json:"prefer_local"`
	PreferUpstream string `json:"prefer_upstream"`
	LocalOnly      string `json:"local_only"`
}

type capabilityProbeSet struct {
	SQLite                 capabilityProbe `json:"sqlite"`
	Llama                  capabilityProbe `json:"llama"`
	RetrievalEmbedder      capabilityProbe `json:"retrieval_embedder"`
	WebSearchBackend       capabilityProbe `json:"web_search_backend"`
	ImageGenerationBackend capabilityProbe `json:"image_generation_backend"`
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

	return capabilityManifest{
		Object: "shim.capabilities",
		Ready:  probes.ready(),
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
				Mode: deps.ResponsesMode,
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
			ResponsesMode:   deps.ResponsesMode,
			CustomToolsMode: deps.ResponsesCustomToolsMode,
			Codex: capabilityCodexConfig{
				CompatibilityEnabled:    deps.ResponsesCodexEnableCompatibility,
				ForceToolChoiceRequired: deps.ResponsesCodexForceToolChoiceRequired,
			},
			Persistence: capabilityPersistenceInfo{
				Backend:         "sqlite",
				ExpectedDurable: deps.Store != nil,
			},
			Retrieval: capabilityRetrievalConfig{
				IndexBackend: deps.RetrievalIndexBackend,
			},
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
				HealthPublic: true,
				ReadyzPublic: true,
			},
		},
		Tools: capabilityToolSet{
			FileSearch: capabilityTool{
				Support: "local_subset",
				Enabled: true,
				Routing: capabilityRouting{
					PreferLocal:    "local_subset",
					PreferUpstream: "proxy_first",
					LocalOnly:      "local_subset_or_validation_error",
				},
			},
			WebSearch: capabilityTool{
				Support: "local_subset_when_configured",
				Backend: normalizedCapabilityBackend(deps.ResponsesWebSearchBackend, deps.WebSearchProvider != nil, "configured"),
				Enabled: deps.WebSearchProvider != nil,
				Routing: capabilityRouting{
					PreferLocal:    "local_subset_or_upstream_fallback",
					PreferUpstream: "proxy_first",
					LocalOnly:      "local_subset_or_explicit_local_only_error",
				},
			},
			ImageGeneration: capabilityTool{
				Support: "local_subset_when_configured",
				Backend: normalizedCapabilityBackend(deps.ResponsesImageGenerationBackend, deps.ImageGenerationProvider != nil, "configured"),
				Enabled: deps.ImageGenerationProvider != nil,
				Routing: capabilityRouting{
					PreferLocal:    "local_subset_or_upstream_fallback",
					PreferUpstream: "proxy_first",
					LocalOnly:      "local_subset_or_explicit_disabled_runtime_error",
				},
			},
			Computer: capabilityTool{
				Support: "local_subset_when_configured",
				Backend: normalizedCapabilityBackend(deps.LocalComputer.Backend, deps.LocalComputer.Enabled(), "configured"),
				Enabled: deps.LocalComputer.Enabled(),
				Routing: capabilityRouting{
					PreferLocal:    "local_subset",
					PreferUpstream: "proxy_first",
					LocalOnly:      "local_subset_or_explicit_disabled_runtime_error",
				},
			},
			CodeInterpreter: capabilityTool{
				Support: "local_subset_when_configured",
				Backend: localCodeInterpreterCapabilityBackend(deps.LocalCodeInterpreter),
				Enabled: deps.LocalCodeInterpreter.Enabled(),
				Routing: capabilityRouting{
					PreferLocal:    "local_subset",
					PreferUpstream: "proxy_first",
					LocalOnly:      "local_subset_or_explicit_disabled_runtime_error",
				},
			},
			Shell: capabilityTool{
				Support: "native_local_subset",
				Backend: "chat_completions_tool_loop",
				Enabled: true,
				Routing: capabilityRouting{
					PreferLocal:    "local_subset_or_validation_error",
					PreferUpstream: "proxy_first",
					LocalOnly:      "local_subset_or_validation_error",
				},
			},
			ApplyPatch: capabilityTool{
				Support: "native_local_subset",
				Backend: "chat_completions_tool_loop",
				Enabled: true,
				Routing: capabilityRouting{
					PreferLocal:    "local_subset",
					PreferUpstream: "proxy_first",
					LocalOnly:      "local_subset",
				},
			},
			MCPServerURL: capabilityTool{
				Support: "local_subset",
				Enabled: true,
				Routing: capabilityRouting{
					PreferLocal:    "local_subset",
					PreferUpstream: "proxy_first",
					LocalOnly:      "local_subset_or_explicit_validation_error",
				},
			},
			MCPConnectorID: capabilityTool{
				Support: "proxy_only",
				Enabled: true,
				Routing: capabilityRouting{
					PreferLocal:    "proxy_only_bridge",
					PreferUpstream: "proxy_only_bridge",
					LocalOnly:      "reject_with_mcp_validation_error",
				},
			},
			ToolSearchHosted: capabilityTool{
				Support: "local_subset",
				Enabled: true,
				Routing: capabilityRouting{
					PreferLocal:    "local_subset",
					PreferUpstream: "proxy_first",
					LocalOnly:      "local_subset",
				},
			},
			ToolSearchClient: capabilityTool{
				Support: "proxy_only",
				Enabled: true,
				Routing: capabilityRouting{
					PreferLocal:    "proxy_only",
					PreferUpstream: "proxy_only",
					LocalOnly:      "reject_with_tool_search_validation_error",
				},
			},
		},
		Probes: probes,
	}
}

func collectCapabilityProbes(ctx context.Context, deps RouterDeps) capabilityProbeSet {
	probes := capabilityProbeSet{
		SQLite: capabilityProbe{
			Enabled: true,
			Checked: true,
			Ready:   deps.Store != nil,
			Error:   probeErrorMessage(deps.Store == nil, "sqlite is not ready"),
		},
		Llama: capabilityProbe{
			Enabled: true,
			Checked: true,
			Ready:   deps.LlamaClient != nil,
			Error:   probeErrorMessage(deps.LlamaClient == nil, "llama backend is not ready"),
		},
	}

	if deps.Store != nil {
		if err := deps.Store.PingContext(ctx); err != nil {
			probes.SQLite.Ready = false
			probes.SQLite.Error = "sqlite is not ready"
		} else {
			probes.SQLite.Error = ""
		}
	}

	if deps.LlamaClient != nil {
		upstreamCtx, cancel := context.WithTimeout(ctx, readyzUpstreamTimeout)
		err := deps.LlamaClient.CheckReady(upstreamCtx)
		cancel()
		if err != nil {
			probes.Llama.Ready = false
			probes.Llama.Error = "llama backend is not ready"
		} else {
			probes.Llama.Error = ""
		}
	}

	probes.RetrievalEmbedder = capabilityProbe{
		Enabled: deps.RetrievalIndexBackend == retrieval.IndexBackendSQLiteVec,
	}
	if probes.RetrievalEmbedder.Enabled {
		if checker, ok := deps.RetrievalEmbedder.(retrieval.ReadyChecker); ok {
			retrievalCtx, cancel := context.WithTimeout(ctx, readyzUpstreamTimeout)
			err := checker.CheckReady(retrievalCtx)
			cancel()
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
		err := checker.CheckReady(webSearchCtx)
		cancel()
		probes.WebSearchBackend.Checked = true
		probes.WebSearchBackend.Ready = err == nil
		probes.WebSearchBackend.Error = probeErrorMessage(err != nil, "web search backend is not ready")
	}

	probes.ImageGenerationBackend = capabilityProbe{
		Enabled: deps.ImageGenerationProvider != nil,
	}
	if deps.ImageGenerationProvider != nil {
		imageCtx, cancel := context.WithTimeout(ctx, readyzUpstreamTimeout)
		err := deps.ImageGenerationProvider.CheckReady(imageCtx)
		cancel()
		probes.ImageGenerationBackend.Checked = true
		probes.ImageGenerationBackend.Ready = err == nil
		probes.ImageGenerationBackend.Error = probeErrorMessage(err != nil, "image generation backend is not ready")
	}

	return probes
}

func (p capabilityProbeSet) ready() bool {
	return probeReady(p.SQLite) &&
		probeReady(p.Llama) &&
		probeReady(p.RetrievalEmbedder) &&
		probeReady(p.WebSearchBackend) &&
		probeReady(p.ImageGenerationBackend)
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
