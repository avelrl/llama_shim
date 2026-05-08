package plugincontract

import (
	"fmt"
	"sort"
	"strings"

	"llama_shim/internal/backendcap"
)

const (
	SchemaVersion = "v4.plugin_contracts.v1"

	IssueError = "error"
	IssueWarn  = "warn"
)

type Registry struct {
	SchemaVersion string       `json:"schema_version"`
	Plugins       []Descriptor `json:"plugins"`
	Issues        []Issue      `json:"issues,omitempty"`
}

type Descriptor struct {
	ID                    string       `json:"id"`
	Version               string       `json:"version"`
	Kind                  string       `json:"kind"`
	DisplayName           string       `json:"display_name,omitempty"`
	ConfigNamespace       string       `json:"config_namespace"`
	RequiredSecrets       []string     `json:"required_secrets,omitempty"`
	ReadinessProbe        string       `json:"readiness_probe,omitempty"`
	CapabilityComponentID string       `json:"capability_component_id"`
	PublicSurfaces        []string     `json:"public_surfaces,omitempty"`
	BackendProjections    []Projection `json:"backend_projections,omitempty"`
	Limits                []string     `json:"limits,omitempty"`
	Timeouts              []string     `json:"timeouts,omitempty"`
	ErrorClasses          []string     `json:"error_classes,omitempty"`
	CIFixtureSafe         bool         `json:"ci_fixture_safe"`
	ProductionIntended    bool         `json:"production_intended"`
}

type Projection struct {
	Class        string `json:"class"`
	SourceFormat string `json:"source_format"`
	TargetFormat string `json:"target_format"`
}

type Issue struct {
	Severity string `json:"severity"`
	Plugin   string `json:"plugin,omitempty"`
	Message  string `json:"message"`
}

type CapabilityPlugin interface {
	Descriptor() Descriptor
	CapabilityComponent() backendcap.Component
}

func NewRegistry(descriptors ...Descriptor) Registry {
	out := Registry{
		SchemaVersion: SchemaVersion,
		Plugins:       make([]Descriptor, 0, len(descriptors)),
	}
	for _, descriptor := range descriptors {
		out.Plugins = append(out.Plugins, normalizeDescriptor(descriptor))
	}
	sort.Slice(out.Plugins, func(i, j int) bool {
		return out.Plugins[i].ID < out.Plugins[j].ID
	})
	out.Issues = Validate(out.Plugins)
	return out
}

func NewRegistryFromPlugins(plugins ...CapabilityPlugin) Registry {
	descriptors := make([]Descriptor, 0, len(plugins))
	for _, plugin := range plugins {
		if plugin == nil {
			continue
		}
		descriptors = append(descriptors, plugin.Descriptor())
	}
	return NewRegistry(descriptors...)
}

func NewRegistryFromPluginsForComponents(components []backendcap.Component, plugins ...CapabilityPlugin) Registry {
	registry := NewRegistryFromPlugins(plugins...)
	registry.Issues = append(registry.Issues, ValidateComponentLinks(registry.Plugins, components)...)
	return registry
}

func ComponentsFromPlugins(plugins ...CapabilityPlugin) []backendcap.Component {
	components := make([]backendcap.Component, 0, len(plugins))
	for _, plugin := range plugins {
		if plugin == nil {
			continue
		}
		component := plugin.CapabilityComponent()
		descriptor := normalizeDescriptor(plugin.Descriptor())
		if component.PluginID == "" {
			component.PluginID = descriptor.ID
		}
		if component.PluginVersion == "" {
			component.PluginVersion = descriptor.Version
		}
		if component.PluginContractVersion == "" {
			component.PluginContractVersion = SchemaVersion
		}
		components = append(components, component)
	}
	return components
}

func ValidateComponentLinks(descriptors []Descriptor, components []backendcap.Component) []Issue {
	issues := make([]Issue, 0)
	componentByID := make(map[string]backendcap.Component, len(components))
	pluginByID := make(map[string]Descriptor, len(descriptors))
	for _, component := range components {
		id := strings.TrimSpace(component.ID)
		if id == "" {
			continue
		}
		componentByID[id] = component
	}
	for _, descriptor := range descriptors {
		descriptor = normalizeDescriptor(descriptor)
		if descriptor.ID == "" || descriptor.CapabilityComponentID == "" {
			continue
		}
		pluginByID[descriptor.ID] = descriptor
		component, ok := componentByID[descriptor.CapabilityComponentID]
		if !ok {
			issues = append(issues, Issue{
				Severity: IssueError,
				Plugin:   descriptor.ID,
				Message:  fmt.Sprintf("plugin capability_component_id %q does not exist", descriptor.CapabilityComponentID),
			})
			continue
		}
		if component.PluginID != "" && strings.TrimSpace(component.PluginID) != descriptor.ID {
			issues = append(issues, Issue{
				Severity: IssueError,
				Plugin:   descriptor.ID,
				Message:  fmt.Sprintf("backend component %q plugin_id %q does not match plugin id", component.ID, component.PluginID),
			})
		}
		if component.PluginVersion != "" && strings.TrimSpace(component.PluginVersion) != descriptor.Version {
			issues = append(issues, Issue{
				Severity: IssueError,
				Plugin:   descriptor.ID,
				Message:  fmt.Sprintf("backend component %q plugin_version %q does not match plugin version", component.ID, component.PluginVersion),
			})
		}
		if component.PluginContractVersion != "" && strings.TrimSpace(component.PluginContractVersion) != SchemaVersion {
			issues = append(issues, Issue{
				Severity: IssueError,
				Plugin:   descriptor.ID,
				Message:  fmt.Sprintf("backend component %q plugin_contract_version %q does not match schema version", component.ID, component.PluginContractVersion),
			})
		}
	}
	for _, component := range components {
		pluginID := strings.TrimSpace(component.PluginID)
		if pluginID == "" {
			continue
		}
		if _, ok := pluginByID[pluginID]; !ok {
			issues = append(issues, Issue{
				Severity: IssueError,
				Plugin:   pluginID,
				Message:  fmt.Sprintf("backend component %q references missing plugin", component.ID),
			})
		}
	}
	return issues
}

func Validate(descriptors []Descriptor) []Issue {
	issues := make([]Issue, 0)
	seen := make(map[string]struct{}, len(descriptors))
	for _, descriptor := range descriptors {
		id := strings.TrimSpace(descriptor.ID)
		switch {
		case id == "":
			issues = append(issues, Issue{Severity: IssueError, Message: "plugin id is required"})
			continue
		case strings.ContainsAny(id, " \t\r\n"):
			issues = append(issues, Issue{Severity: IssueError, Plugin: id, Message: "plugin id must not contain whitespace"})
		}
		if _, ok := seen[id]; ok {
			issues = append(issues, Issue{Severity: IssueError, Plugin: id, Message: fmt.Sprintf("duplicate plugin %q", id)})
		}
		seen[id] = struct{}{}
		if strings.TrimSpace(descriptor.Version) == "" {
			issues = append(issues, Issue{Severity: IssueError, Plugin: id, Message: "plugin version is required"})
		}
		if strings.TrimSpace(descriptor.Kind) == "" {
			issues = append(issues, Issue{Severity: IssueError, Plugin: id, Message: "plugin kind is required"})
		}
		if strings.TrimSpace(descriptor.ConfigNamespace) == "" {
			issues = append(issues, Issue{Severity: IssueError, Plugin: id, Message: "plugin config_namespace is required"})
		}
		if strings.TrimSpace(descriptor.CapabilityComponentID) == "" {
			issues = append(issues, Issue{Severity: IssueError, Plugin: id, Message: "plugin capability_component_id is required"})
		}
		if len(descriptor.BackendProjections) == 0 {
			issues = append(issues, Issue{Severity: IssueWarn, Plugin: id, Message: "plugin declares no backend projections"})
		}
	}
	return issues
}

func HasErrors(issues []Issue) bool {
	for _, issue := range issues {
		if issue.Severity == IssueError {
			return true
		}
	}
	return false
}

func normalizeDescriptor(descriptor Descriptor) Descriptor {
	descriptor.ID = strings.TrimSpace(descriptor.ID)
	descriptor.Version = strings.TrimSpace(descriptor.Version)
	descriptor.Kind = strings.TrimSpace(descriptor.Kind)
	descriptor.DisplayName = strings.TrimSpace(descriptor.DisplayName)
	descriptor.ConfigNamespace = strings.TrimSpace(descriptor.ConfigNamespace)
	descriptor.ReadinessProbe = strings.TrimSpace(descriptor.ReadinessProbe)
	descriptor.CapabilityComponentID = strings.TrimSpace(descriptor.CapabilityComponentID)
	descriptor.RequiredSecrets = normalizeStringList(descriptor.RequiredSecrets)
	descriptor.PublicSurfaces = normalizeStringList(descriptor.PublicSurfaces)
	descriptor.Limits = normalizeStringList(descriptor.Limits)
	descriptor.Timeouts = normalizeStringList(descriptor.Timeouts)
	descriptor.ErrorClasses = normalizeStringList(descriptor.ErrorClasses)
	descriptor.BackendProjections = normalizeProjections(descriptor.BackendProjections)
	return descriptor
}

func normalizeStringList(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func normalizeProjections(values []Projection) []Projection {
	if len(values) == 0 {
		return nil
	}
	out := make([]Projection, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value.Class = strings.TrimSpace(value.Class)
		value.SourceFormat = strings.TrimSpace(value.SourceFormat)
		value.TargetFormat = strings.TrimSpace(value.TargetFormat)
		if value.Class == "" || value.SourceFormat == "" || value.TargetFormat == "" {
			continue
		}
		key := value.Class + "\x00" + value.SourceFormat + "\x00" + value.TargetFormat
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Class != out[j].Class {
			return out[i].Class < out[j].Class
		}
		if out[i].SourceFormat != out[j].SourceFormat {
			return out[i].SourceFormat < out[j].SourceFormat
		}
		return out[i].TargetFormat < out[j].TargetFormat
	})
	return out
}
