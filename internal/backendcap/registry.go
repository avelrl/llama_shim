package backendcap

import (
	"fmt"
	"sort"
	"strings"
)

const (
	SchemaVersion = "v4.backend_capabilities.v1"

	ClassNative           = "native"
	ClassLocalSubset      = "local_subset"
	ClassProxyOnly        = "proxy_only"
	ClassChatProjection   = "chat_projection"
	ClassRepairOrValidate = "repair_or_validate"
	ClassUnsupported      = "unsupported"

	IssueError = "error"
	IssueWarn  = "warn"
)

type Registry struct {
	SchemaVersion string      `json:"schema_version"`
	Components    []Component `json:"components"`
	Issues        []Issue     `json:"issues,omitempty"`
}

type Component struct {
	ID                    string   `json:"id"`
	Category              string   `json:"category"`
	Kind                  string   `json:"kind"`
	DisplayName           string   `json:"display_name,omitempty"`
	ConfigNamespace       string   `json:"config_namespace"`
	Backend               string   `json:"backend,omitempty"`
	CapabilityClass       string   `json:"capability_class"`
	Enabled               bool     `json:"enabled"`
	Ready                 bool     `json:"ready"`
	ReadinessProbe        string   `json:"readiness_probe,omitempty"`
	PluginID              string   `json:"plugin_id,omitempty"`
	PluginVersion         string   `json:"plugin_version,omitempty"`
	PluginContractVersion string   `json:"plugin_contract_version,omitempty"`
	Auth                  string   `json:"auth,omitempty"`
	SecretRefs            []string `json:"secret_refs,omitempty"`
	StateOwnership        string   `json:"state_ownership,omitempty"`
	WireModes             []string `json:"wire_modes,omitempty"`
	PublicSurfaces        []string `json:"public_surfaces,omitempty"`
	Tools                 []string `json:"tools,omitempty"`
	ModelIDs              []string `json:"model_ids,omitempty"`
	RoutingModes          []string `json:"routing_modes,omitempty"`
	Evidence              []string `json:"evidence,omitempty"`
	Notes                 []string `json:"notes,omitempty"`
}

type Issue struct {
	Severity  string `json:"severity"`
	Component string `json:"component,omitempty"`
	Message   string `json:"message"`
}

func NewRegistry(components ...Component) Registry {
	out := Registry{
		SchemaVersion: SchemaVersion,
		Components:    make([]Component, 0, len(components)),
	}
	for _, component := range components {
		component = normalizeComponent(component)
		out.Components = append(out.Components, component)
	}
	sort.Slice(out.Components, func(i, j int) bool {
		return out.Components[i].ID < out.Components[j].ID
	})
	out.Issues = Validate(out.Components)
	return out
}

func Validate(components []Component) []Issue {
	issues := make([]Issue, 0)
	seen := make(map[string]struct{}, len(components))
	for _, component := range components {
		id := strings.TrimSpace(component.ID)
		switch {
		case id == "":
			issues = append(issues, Issue{Severity: IssueError, Message: "backend capability component id is required"})
			continue
		case strings.ContainsAny(id, " \t\r\n"):
			issues = append(issues, Issue{Severity: IssueError, Component: id, Message: "backend capability component id must not contain whitespace"})
		}
		if _, ok := seen[id]; ok {
			issues = append(issues, Issue{Severity: IssueError, Component: id, Message: fmt.Sprintf("duplicate backend capability component %q", id)})
		}
		seen[id] = struct{}{}
		if strings.TrimSpace(component.Category) == "" {
			issues = append(issues, Issue{Severity: IssueError, Component: id, Message: "backend capability component category is required"})
		}
		if strings.TrimSpace(component.Kind) == "" {
			issues = append(issues, Issue{Severity: IssueError, Component: id, Message: "backend capability component kind is required"})
		}
		if strings.TrimSpace(component.ConfigNamespace) == "" {
			issues = append(issues, Issue{Severity: IssueError, Component: id, Message: "backend capability component config_namespace is required"})
		}
		if !knownCapabilityClass(component.CapabilityClass) {
			issues = append(issues, Issue{Severity: IssueError, Component: id, Message: fmt.Sprintf("unknown backend capability class %q", component.CapabilityClass)})
		}
		if !component.Enabled && component.Ready {
			issues = append(issues, Issue{Severity: IssueWarn, Component: id, Message: "disabled backend capability component should not report ready=true"})
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

func normalizeComponent(component Component) Component {
	component.ID = strings.TrimSpace(component.ID)
	component.Category = strings.TrimSpace(component.Category)
	component.Kind = strings.TrimSpace(component.Kind)
	component.DisplayName = strings.TrimSpace(component.DisplayName)
	component.ConfigNamespace = strings.TrimSpace(component.ConfigNamespace)
	component.Backend = strings.TrimSpace(component.Backend)
	component.CapabilityClass = strings.TrimSpace(component.CapabilityClass)
	component.ReadinessProbe = strings.TrimSpace(component.ReadinessProbe)
	component.PluginID = strings.TrimSpace(component.PluginID)
	component.PluginVersion = strings.TrimSpace(component.PluginVersion)
	component.PluginContractVersion = strings.TrimSpace(component.PluginContractVersion)
	component.Auth = strings.TrimSpace(component.Auth)
	component.StateOwnership = strings.TrimSpace(component.StateOwnership)
	component.SecretRefs = normalizeStringList(component.SecretRefs)
	component.WireModes = normalizeStringList(component.WireModes)
	component.PublicSurfaces = normalizeStringList(component.PublicSurfaces)
	component.Tools = normalizeStringList(component.Tools)
	component.ModelIDs = normalizeStringList(component.ModelIDs)
	component.RoutingModes = normalizeStringList(component.RoutingModes)
	component.Evidence = normalizeStringList(component.Evidence)
	component.Notes = normalizeStringList(component.Notes)
	return component
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

func knownCapabilityClass(value string) bool {
	switch strings.TrimSpace(value) {
	case ClassNative, ClassLocalSubset, ClassProxyOnly, ClassChatProjection, ClassRepairOrValidate, ClassUnsupported:
		return true
	default:
		return false
	}
}
