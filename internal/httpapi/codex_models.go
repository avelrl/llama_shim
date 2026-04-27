package httpapi

import (
	"net/http"
)

type CodexModelMetadata struct {
	Model                         string
	DisplayName                   string
	Description                   string
	ContextWindow                 int64
	MaxContextWindow              int64
	AutoCompactTokenLimit         int64
	EffectiveContextWindowPercent int64
	DefaultReasoningLevel         string
	SupportedReasoningLevels      []string
	SupportsReasoningSummaries    bool
	DefaultReasoningSummary       string
	ShellType                     string
	ApplyPatchToolType            string
	WebSearchToolType             string
	SupportsParallelToolCalls     bool
	SupportVerbosity              bool
	DefaultVerbosity              string
	SupportsImageDetailOriginal   bool
	SupportsSearchTool            bool
	InputModalities               []string
	Visibility                    string
	SupportedInAPI                *bool
	Priority                      int
	AdditionalSpeedTiers          []string
	ExperimentalSupportedTools    []string
	AvailabilityNuxMessage        string
	TruncationPolicyMode          string
	TruncationPolicyLimit         int64
	BaseInstructions              string
}

func writeCodexModels(w http.ResponseWriter, models []CodexModelMetadata) bool {
	if len(models) == 0 {
		return false
	}
	out := make([]map[string]any, 0, len(models))
	for _, model := range models {
		item := map[string]any{
			"slug":                             model.Model,
			"display_name":                     model.DisplayName,
			"description":                      model.Description,
			"default_reasoning_level":          codexStringDefault(model.DefaultReasoningLevel, "high"),
			"supported_reasoning_levels":       codexReasoningLevelPresets(model.SupportedReasoningLevels),
			"shell_type":                       codexStringDefault(model.ShellType, "shell_command"),
			"visibility":                       codexStringDefault(model.Visibility, "list"),
			"supported_in_api":                 codexBoolDefault(model.SupportedInAPI, true),
			"priority":                         model.Priority,
			"additional_speed_tiers":           codexStringList(model.AdditionalSpeedTiers),
			"availability_nux":                 optionalAvailabilityNux(model.AvailabilityNuxMessage),
			"upgrade":                          nil,
			"base_instructions":                model.BaseInstructions,
			"model_messages":                   nil,
			"supports_reasoning_summaries":     model.SupportsReasoningSummaries,
			"default_reasoning_summary":        codexStringDefault(model.DefaultReasoningSummary, "none"),
			"support_verbosity":                model.SupportVerbosity,
			"default_verbosity":                optionalCodexString(model.DefaultVerbosity),
			"apply_patch_tool_type":            optionalCodexString(model.ApplyPatchToolType),
			"web_search_tool_type":             codexStringDefault(model.WebSearchToolType, "text"),
			"truncation_policy":                codexTruncationPolicy(model.TruncationPolicyMode, model.TruncationPolicyLimit),
			"supports_parallel_tool_calls":     model.SupportsParallelToolCalls,
			"supports_image_detail_original":   model.SupportsImageDetailOriginal,
			"context_window":                   optionalPositiveInt64(model.ContextWindow),
			"max_context_window":               optionalPositiveInt64(model.MaxContextWindow),
			"auto_compact_token_limit":         optionalPositiveInt64(model.AutoCompactTokenLimit),
			"effective_context_window_percent": codexInt64Default(model.EffectiveContextWindowPercent, 95),
			"experimental_supported_tools":     codexStringList(model.ExperimentalSupportedTools),
			"input_modalities":                 codexInputModalities(model.InputModalities),
			"supports_search_tool":             model.SupportsSearchTool,
		}
		out = append(out, item)
	}
	WriteJSON(w, http.StatusOK, map[string]any{"models": out})
	return true
}

func codexReasoningLevelPresets(levels []string) []map[string]string {
	out := make([]map[string]string, 0, len(levels))
	for _, level := range levels {
		out = append(out, map[string]string{
			"effort":      level,
			"description": level,
		})
	}
	return out
}

func optionalCodexString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func codexStringDefault(value string, defaultValue string) string {
	if value == "" {
		return defaultValue
	}
	return value
}

func codexStringList(values []string) []string {
	if len(values) == 0 {
		return []string{}
	}
	return append([]string(nil), values...)
}

func codexInputModalities(values []string) []string {
	if len(values) == 0 {
		return []string{"text"}
	}
	return append([]string(nil), values...)
}

func optionalAvailabilityNux(message string) any {
	if message == "" {
		return nil
	}
	return map[string]string{"message": message}
}

func codexBoolDefault(value *bool, defaultValue bool) bool {
	if value == nil {
		return defaultValue
	}
	return *value
}

func codexInt64Default(value int64, defaultValue int64) int64 {
	if value == 0 {
		return defaultValue
	}
	return value
}

func codexTruncationPolicy(mode string, limit int64) map[string]any {
	if mode == "" {
		mode = "bytes"
	}
	if limit <= 0 {
		limit = 10000
	}
	return map[string]any{"mode": mode, "limit": limit}
}

func optionalPositiveInt64(value int64) any {
	if value <= 0 {
		return nil
	}
	return value
}
