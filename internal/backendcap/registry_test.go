package backendcap

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewRegistryNormalizesAndSortsComponents(t *testing.T) {
	registry := NewRegistry(
		Component{
			ID:              " tool.web_search ",
			Category:        " tool_runtime ",
			Kind:            " searxng ",
			ConfigNamespace: " responses.web_search ",
			CapabilityClass: ClassLocalSubset,
			PluginID:        " provider.searxng ",
			PluginVersion:   " v1 ",
			Enabled:         true,
			Ready:           true,
			Tools:           []string{"web_search", "web_search", " "},
			ModelIDs:        []string{"qwen/coder", "qwen/coder"},
			WireModes:       []string{" responses_over_chat ", "chat_completions"},
		},
		Component{
			ID:              "storage.primary",
			Category:        "storage",
			Kind:            "sqlite",
			ConfigNamespace: "storage",
			CapabilityClass: ClassLocalSubset,
			Enabled:         true,
			Ready:           true,
		},
	)

	require.Equal(t, SchemaVersion, registry.SchemaVersion)
	require.Empty(t, registry.Issues)
	require.Len(t, registry.Components, 2)
	require.Equal(t, "storage.primary", registry.Components[0].ID)
	require.Equal(t, "tool.web_search", registry.Components[1].ID)
	require.Equal(t, "provider.searxng", registry.Components[1].PluginID)
	require.Equal(t, "v1", registry.Components[1].PluginVersion)
	require.Equal(t, []string{"web_search"}, registry.Components[1].Tools)
	require.Equal(t, []string{"qwen/coder"}, registry.Components[1].ModelIDs)
	require.Equal(t, []string{"chat_completions", "responses_over_chat"}, registry.Components[1].WireModes)
}

func TestValidateReportsContradictions(t *testing.T) {
	issues := Validate([]Component{
		{
			ID:              "dup",
			Category:        "storage",
			Kind:            "sqlite",
			ConfigNamespace: "storage",
			CapabilityClass: ClassLocalSubset,
			Enabled:         true,
		},
		{
			ID:              "dup",
			Category:        "storage",
			Kind:            "sqlite",
			ConfigNamespace: "storage",
			CapabilityClass: "magic",
			Ready:           true,
		},
		{
			ID:              "disabled.ready",
			Category:        "tool_runtime",
			Kind:            "fixture",
			ConfigNamespace: "responses.image_generation",
			CapabilityClass: ClassLocalSubset,
			Enabled:         false,
			Ready:           true,
		},
	})

	require.True(t, HasErrors(issues))
	require.Contains(t, issues, Issue{Severity: IssueError, Component: "dup", Message: `duplicate backend capability component "dup"`})
	require.Contains(t, issues, Issue{Severity: IssueError, Component: "dup", Message: `unknown backend capability class "magic"`})
	require.Contains(t, issues, Issue{Severity: IssueWarn, Component: "disabled.ready", Message: "disabled backend capability component should not report ready=true"})
}
