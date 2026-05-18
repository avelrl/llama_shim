package modelcert

import (
	"fmt"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"llama_shim/internal/config"
)

type RenderConfigOptions struct {
	Model       ModelEntry
	BaseConfig  config.Config
	ArtifactDir string
	Port        int
}

func RenderShimConfig(opts RenderConfigOptions) ([]byte, error) {
	model := opts.Model
	if opts.Port <= 0 {
		return nil, fmt.Errorf("port must be positive")
	}
	if strings.TrimSpace(opts.ArtifactDir) == "" {
		return nil, fmt.Errorf("artifact dir is required")
	}
	_, providerModel, _ := strings.Cut(model.Model, "/")
	metadata := findCodexMetadata(opts.BaseConfig, model.Model)
	if metadata.Model == "" {
		metadata = defaultCodexMetadata(model)
	}

	root := map[string]any{
		"shim": map[string]any{
			"addr":          fmt.Sprintf("127.0.0.1:%d", opts.Port),
			"read_timeout":  "15s",
			"write_timeout": "180s",
			"idle_timeout":  "60s",
			"auth": map[string]any{
				"mode":          config.ShimAuthModeDisabled,
				"bearer_tokens": []string{},
			},
			"metrics": map[string]any{
				"enabled": true,
				"path":    "/metrics",
			},
			"debug_traces": map[string]any{
				"enabled":     true,
				"max_entries": 4096,
			},
			"evidence": map[string]any{
				"enabled":     true,
				"root":        opts.ArtifactDir,
				"max_entries": 50,
				"stale_after": "168h",
			},
		},
		"ui": map[string]any{
			"enabled": false,
		},
		"storage": map[string]any{
			"backend": "sqlite",
		},
		"sqlite": map[string]any{
			"path": filepath.ToSlash(filepath.Join(opts.ArtifactDir, "shim", "shim.db")),
		},
		"log": map[string]any{
			"level":     "debug",
			"file_path": filepath.ToSlash(filepath.Join(opts.ArtifactDir, "shim", "shim.log")),
		},
		"llama": map[string]any{
			"base_url":                model.Provider.BaseURL,
			"readiness_bearer_token":  "",
			"timeout":                 "180s",
			"max_concurrent_requests": 4,
			"max_queue_wait":          "0s",
			"providers": []map[string]any{
				{
					"id":               model.Provider.ID,
					"base_url":         model.Provider.BaseURL,
					"bearer_token_env": model.Provider.BearerTokenEnv,
					"models": []map[string]any{
						{
							"model":          providerModel,
							"upstream_model": model.Provider.UpstreamModel,
						},
					},
				},
			},
		},
		"chat_completions": map[string]any{
			"default_store_when_omitted": true,
			"upstream_compatibility": map[string]any{
				"models": chatCompatibilityMaps(opts.BaseConfig.ChatCompletionsUpstreamCompatibility),
			},
		},
		"responses": map[string]any{
			"mode":               config.ResponsesModePreferUpstream,
			"upstream_transport": config.ResponsesUpstreamTransportChatCompletions,
			"websocket": map[string]any{
				"enabled": true,
			},
			"custom_tools": map[string]any{
				"mode": "auto",
			},
			"constrained_decoding": map[string]any{
				"backend": config.ResponsesConstrainedDecodingBackendShimValidateRepair,
			},
			"upstream_tool_compatibility": map[string]any{
				"models": upstreamToolCompatibilityMaps(opts.BaseConfig.ResponsesUpstreamToolCompatibility),
			},
			"codex": map[string]any{
				"enable_compatibility": true,
				"upstream_input_compatibility": map[string]any{
					"models": codexInputCompatibilityMaps(opts.BaseConfig.ResponsesCodexUpstreamInputCompatibility),
				},
				"model_metadata": map[string]any{
					"models": []map[string]any{codexMetadataMap(metadata)},
				},
			},
			"compaction": map[string]any{
				"backend": "heuristic",
			},
			"memory": map[string]any{
				"backend": "disabled",
				"inject":  false,
			},
			"web_search": map[string]any{
				"backend": "disabled",
			},
			"image_generation": map[string]any{
				"backend": "disabled",
			},
			"computer": map[string]any{
				"backend": "disabled",
			},
		},
	}
	raw, err := yaml.Marshal(root)
	if err != nil {
		return nil, err
	}
	return raw, nil
}

func defaultCodexMetadata(model ModelEntry) config.ResponsesCodexModelMetadata {
	contextWindow := model.Codex.ContextWindow
	if contextWindow <= 0 {
		contextWindow = 32768
	}
	return config.ResponsesCodexModelMetadata{
		Model:                         model.Model,
		DisplayName:                   model.Model,
		Description:                   "Model certification candidate routed through llama_shim.",
		ContextWindow:                 contextWindow,
		MaxContextWindow:              contextWindow,
		EffectiveContextWindowPercent: 80,
		DefaultReasoningLevel:         "medium",
		SupportedReasoningLevels:      []string{"low", "medium", "high"},
		DefaultReasoningSummary:       "none",
		ShellType:                     defaultString(model.Codex.ShellType, "shell_command"),
		ApplyPatchToolType:            defaultString(model.Codex.ApplyPatchToolType, "freeform"),
		WebSearchToolType:             "text",
		InputModalities:               []string{"text"},
		Visibility:                    "list",
		SupportedInAPI:                boolPtr(true),
		TruncationPolicy:              config.ResponsesCodexTruncationPolicy{Mode: "bytes", Limit: 10000},
	}
}

func chatCompatibilityMaps(rules []config.ChatCompletionsUpstreamCompatibilityRule) []map[string]any {
	out := make([]map[string]any, 0, len(rules))
	for _, rule := range rules {
		row := map[string]any{"model": rule.Model}
		if rule.RemapDeveloperRole {
			row["remap_developer_role"] = true
		}
		if rule.DefaultThinking != "" {
			row["default_thinking"] = rule.DefaultThinking
		}
		if rule.DefaultMaxTokens > 0 {
			row["default_max_tokens"] = rule.DefaultMaxTokens
		}
		if rule.JSONSchemaMode != "" {
			row["json_schema_mode"] = rule.JSONSchemaMode
		}
		if rule.EnsureToolParameterPropertyTypes {
			row["ensure_tool_parameter_property_types"] = true
		}
		if rule.SanitizeMoonshotToolSchema {
			row["sanitize_moonshot_tool_schema"] = true
		}
		if rule.OmitEmptyAssistantToolContent {
			row["omit_empty_assistant_tool_content"] = true
		}
		if rule.RetryInvalidToolArguments {
			row["retry_invalid_tool_arguments"] = true
		}
		if rule.InvalidToolArgumentsFallback != "" {
			row["invalid_tool_arguments_fallback"] = rule.InvalidToolArgumentsFallback
		}
		out = append(out, row)
	}
	return out
}

func upstreamToolCompatibilityMaps(rules []config.ResponsesUpstreamToolCompatibilityRule) []map[string]any {
	out := make([]map[string]any, 0, len(rules))
	for _, rule := range rules {
		out = append(out, map[string]any{
			"model":          rule.Model,
			"disabled_tools": append([]string(nil), rule.DisabledTools...),
		})
	}
	return out
}

func codexInputCompatibilityMaps(rules []config.ResponsesCodexUpstreamInputCompatibilityRule) []map[string]any {
	out := make([]map[string]any, 0, len(rules))
	for _, rule := range rules {
		out = append(out, map[string]any{
			"model": rule.Model,
			"mode":  rule.Mode,
		})
	}
	return out
}

func codexMetadataMap(metadata config.ResponsesCodexModelMetadata) map[string]any {
	row := map[string]any{
		"model":                            metadata.Model,
		"display_name":                     metadata.DisplayName,
		"description":                      metadata.Description,
		"context_window":                   metadata.ContextWindow,
		"max_context_window":               metadata.MaxContextWindow,
		"auto_compact_token_limit":         metadata.AutoCompactTokenLimit,
		"effective_context_window_percent": metadata.EffectiveContextWindowPercent,
		"default_reasoning_level":          metadata.DefaultReasoningLevel,
		"supported_reasoning_levels":       append([]string(nil), metadata.SupportedReasoningLevels...),
		"supports_reasoning_summaries":     metadata.SupportsReasoningSummaries,
		"default_reasoning_summary":        metadata.DefaultReasoningSummary,
		"shell_type":                       metadata.ShellType,
		"apply_patch_tool_type":            metadata.ApplyPatchToolType,
		"web_search_tool_type":             metadata.WebSearchToolType,
		"supports_parallel_tool_calls":     metadata.SupportsParallelToolCalls,
		"support_verbosity":                metadata.SupportVerbosity,
		"default_verbosity":                metadata.DefaultVerbosity,
		"supports_image_detail_original":   metadata.SupportsImageDetailOriginal,
		"supports_search_tool":             metadata.SupportsSearchTool,
		"input_modalities":                 append([]string(nil), metadata.InputModalities...),
		"visibility":                       metadata.Visibility,
		"additional_speed_tiers":           append([]string(nil), metadata.AdditionalSpeedTiers...),
		"experimental_supported_tools":     append([]string(nil), metadata.ExperimentalSupportedTools...),
		"availability_nux_message":         metadata.AvailabilityNuxMessage,
		"truncation_policy": map[string]any{
			"mode":  metadata.TruncationPolicy.Mode,
			"limit": metadata.TruncationPolicy.Limit,
		},
		"base_instructions": metadata.BaseInstructions,
	}
	if metadata.SupportedInAPI != nil {
		row["supported_in_api"] = *metadata.SupportedInAPI
	}
	if metadata.Priority != nil {
		row["priority"] = *metadata.Priority
	}
	return row
}

func boolPtr(value bool) *bool {
	return &value
}
