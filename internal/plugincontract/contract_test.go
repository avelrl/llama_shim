package plugincontract

import (
	"testing"

	"github.com/stretchr/testify/require"

	"llama_shim/internal/backendcap"
)

type testPlugin struct {
	descriptor Descriptor
	component  backendcap.Component
}

func (p testPlugin) Descriptor() Descriptor {
	return p.descriptor
}

func (p testPlugin) CapabilityComponent() backendcap.Component {
	return p.component
}

func TestNewRegistryNormalizesAndSortsDescriptors(t *testing.T) {
	registry := NewRegistry(
		Descriptor{
			ID:                    " provider.qwen ",
			Version:               " v1 ",
			Kind:                  " model_provider ",
			ConfigNamespace:       " llama.providers.qwen ",
			CapabilityComponentID: " provider.qwen ",
			RequiredSecrets:       []string{" QWEN_API_KEY ", "QWEN_API_KEY", " "},
			PublicSurfaces:        []string{"responses.create", "chat_completions.create", "responses.create"},
			BackendProjections: []Projection{
				{Class: " chat_projection ", SourceFormat: " responses ", TargetFormat: " chat_completions "},
				{Class: "chat_projection", SourceFormat: "responses", TargetFormat: "chat_completions"},
			},
			CIFixtureSafe:      true,
			ProductionIntended: true,
		},
		Descriptor{
			ID:                    "model.llama",
			Version:               "v1",
			Kind:                  "model_backend",
			ConfigNamespace:       "llama",
			CapabilityComponentID: "model.llama",
			BackendProjections: []Projection{
				{Class: "native", SourceFormat: "responses", TargetFormat: "responses"},
			},
		},
	)

	require.Equal(t, SchemaVersion, registry.SchemaVersion)
	require.Empty(t, registry.Issues)
	require.Len(t, registry.Plugins, 2)
	require.Equal(t, "model.llama", registry.Plugins[0].ID)
	require.Equal(t, "provider.qwen", registry.Plugins[1].ID)
	require.Equal(t, []string{"QWEN_API_KEY"}, registry.Plugins[1].RequiredSecrets)
	require.Equal(t, []string{"chat_completions.create", "responses.create"}, registry.Plugins[1].PublicSurfaces)
	require.Equal(t, []Projection{{Class: "chat_projection", SourceFormat: "responses", TargetFormat: "chat_completions"}}, registry.Plugins[1].BackendProjections)
}

func TestValidateReportsContractIssues(t *testing.T) {
	issues := Validate([]Descriptor{
		{
			ID:                    "dup",
			Version:               "v1",
			Kind:                  "model_provider",
			ConfigNamespace:       "llama.providers.qwen",
			CapabilityComponentID: "provider.qwen",
			BackendProjections:    []Projection{{Class: "native", SourceFormat: "responses", TargetFormat: "responses"}},
		},
		{
			ID:                    "dup",
			CapabilityComponentID: "provider.qwen2",
		},
		{
			ID:                    "no.projection",
			Version:               "v1",
			Kind:                  "model_provider",
			ConfigNamespace:       "llama.providers.none",
			CapabilityComponentID: "provider.none",
		},
	})

	require.True(t, HasErrors(issues))
	require.Contains(t, issues, Issue{Severity: IssueError, Plugin: "dup", Message: `duplicate plugin "dup"`})
	require.Contains(t, issues, Issue{Severity: IssueError, Plugin: "dup", Message: "plugin version is required"})
	require.Contains(t, issues, Issue{Severity: IssueError, Plugin: "dup", Message: "plugin kind is required"})
	require.Contains(t, issues, Issue{Severity: IssueError, Plugin: "dup", Message: "plugin config_namespace is required"})
	require.Contains(t, issues, Issue{Severity: IssueWarn, Plugin: "no.projection", Message: "plugin declares no backend projections"})
}

func TestComponentsFromPluginsAnnotatesComponentWithPluginMetadata(t *testing.T) {
	components := ComponentsFromPlugins(testPlugin{
		descriptor: Descriptor{
			ID:                    "provider.qwen",
			Version:               "v1",
			Kind:                  "model_provider",
			ConfigNamespace:       "llama.providers.qwen",
			CapabilityComponentID: "provider.qwen",
			BackendProjections:    []Projection{{Class: "native", SourceFormat: "responses", TargetFormat: "responses"}},
		},
		component: backendcap.Component{
			ID:              "provider.qwen",
			Category:        "model_provider",
			Kind:            "openai_compatible",
			ConfigNamespace: "llama.providers.qwen",
			CapabilityClass: backendcap.ClassNative,
			Enabled:         true,
			Ready:           true,
		},
	})

	require.Len(t, components, 1)
	require.Equal(t, "provider.qwen", components[0].PluginID)
	require.Equal(t, "v1", components[0].PluginVersion)
	require.Equal(t, SchemaVersion, components[0].PluginContractVersion)
}

func TestValidateComponentLinksReportsMissingAndMismatchedComponents(t *testing.T) {
	issues := ValidateComponentLinks(
		[]Descriptor{
			{
				ID:                    "provider.qwen",
				Version:               "v1",
				Kind:                  "model_provider",
				ConfigNamespace:       "llama.providers.qwen",
				CapabilityComponentID: "provider.qwen",
				BackendProjections:    []Projection{{Class: "native", SourceFormat: "responses", TargetFormat: "responses"}},
			},
			{
				ID:                    "tool.web_search",
				Version:               "v1",
				Kind:                  "tool_runtime",
				ConfigNamespace:       "responses.web_search",
				CapabilityComponentID: "tool.web_search",
				BackendProjections:    []Projection{{Class: "local_subset", SourceFormat: "responses.tools.web_search", TargetFormat: "searxng"}},
			},
			{
				ID:                    "missing",
				Version:               "v1",
				Kind:                  "tool_runtime",
				ConfigNamespace:       "responses.missing",
				CapabilityComponentID: "tool.missing",
				BackendProjections:    []Projection{{Class: "local_subset", SourceFormat: "responses.tools.missing", TargetFormat: "missing"}},
			},
		},
		[]backendcap.Component{
			{
				ID:                    "provider.qwen",
				Category:              "model_provider",
				Kind:                  "openai_compatible",
				ConfigNamespace:       "llama.providers.qwen",
				CapabilityClass:       backendcap.ClassNative,
				Enabled:               true,
				Ready:                 true,
				PluginID:              "provider.other",
				PluginVersion:         "v2",
				PluginContractVersion: "bad.schema",
			},
			{
				ID:                    "tool.web_search",
				Category:              "tool_runtime",
				Kind:                  "web_search",
				ConfigNamespace:       "responses.web_search",
				CapabilityClass:       backendcap.ClassLocalSubset,
				Enabled:               true,
				Ready:                 true,
				PluginID:              "tool.web_search",
				PluginVersion:         "v1",
				PluginContractVersion: SchemaVersion,
			},
			{
				ID:              "tool.orphan",
				Category:        "tool_runtime",
				Kind:            "orphan",
				ConfigNamespace: "responses.orphan",
				CapabilityClass: backendcap.ClassLocalSubset,
				Enabled:         true,
				Ready:           true,
				PluginID:        "tool.orphan",
			},
		},
	)

	require.True(t, HasErrors(issues))
	require.Contains(t, issues, Issue{Severity: IssueError, Plugin: "provider.qwen", Message: `backend component "provider.qwen" plugin_id "provider.other" does not match plugin id`})
	require.Contains(t, issues, Issue{Severity: IssueError, Plugin: "provider.qwen", Message: `backend component "provider.qwen" plugin_version "v2" does not match plugin version`})
	require.Contains(t, issues, Issue{Severity: IssueError, Plugin: "provider.qwen", Message: `backend component "provider.qwen" plugin_contract_version "bad.schema" does not match schema version`})
	require.Contains(t, issues, Issue{Severity: IssueError, Plugin: "missing", Message: `plugin capability_component_id "tool.missing" does not exist`})
	require.Contains(t, issues, Issue{Severity: IssueError, Plugin: "tool.orphan", Message: `backend component "tool.orphan" references missing plugin`})
}
