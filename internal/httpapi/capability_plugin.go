package httpapi

import (
	"strings"

	"llama_shim/internal/backendcap"
	"llama_shim/internal/plugincontract"
)

const capabilityPluginVersion = "v1"

type registeredCapabilityPlugin struct {
	descriptor plugincontract.Descriptor
	component  backendcap.Component
}

func (p registeredCapabilityPlugin) Descriptor() plugincontract.Descriptor {
	return p.descriptor
}

func (p registeredCapabilityPlugin) CapabilityComponent() backendcap.Component {
	return p.component
}

type capabilityPluginOptions struct {
	ID                  string
	Kind                string
	DisplayName         string
	CIFixtureSafe       bool
	ProductionIntended  bool
	BackendProjections  []plugincontract.Projection
	RequestCleanupHooks []string
	Limits              []string
	Timeouts            []string
	ErrorClasses        []string
}

func newRegisteredCapabilityPlugin(component backendcap.Component, opts capabilityPluginOptions) plugincontract.CapabilityPlugin {
	pluginID := strings.TrimSpace(opts.ID)
	if pluginID == "" {
		pluginID = strings.TrimSpace(component.ID)
	}
	kind := strings.TrimSpace(opts.Kind)
	if kind == "" {
		kind = pluginKindForComponent(component)
	}
	displayName := strings.TrimSpace(opts.DisplayName)
	if displayName == "" {
		displayName = strings.TrimSpace(component.DisplayName)
	}
	descriptor := plugincontract.Descriptor{
		ID:                    pluginID,
		Version:               capabilityPluginVersion,
		Kind:                  kind,
		DisplayName:           displayName,
		ConfigNamespace:       component.ConfigNamespace,
		RequiredSecrets:       component.SecretRefs,
		ReadinessProbe:        component.ReadinessProbe,
		CapabilityComponentID: component.ID,
		PublicSurfaces:        component.PublicSurfaces,
		BackendProjections:    opts.BackendProjections,
		RequestCleanupHooks:   opts.RequestCleanupHooks,
		Limits:                opts.Limits,
		Timeouts:              opts.Timeouts,
		ErrorClasses:          opts.ErrorClasses,
		CIFixtureSafe:         opts.CIFixtureSafe,
		ProductionIntended:    opts.ProductionIntended,
	}
	if len(descriptor.BackendProjections) == 0 {
		descriptor.BackendProjections = componentBackendProjections(component)
	}
	return registeredCapabilityPlugin{descriptor: descriptor, component: component}
}

func pluginKindForComponent(component backendcap.Component) string {
	switch strings.TrimSpace(component.Category) {
	case "storage":
		return "storage_backend"
	case "retrieval_index":
		return "retrieval_index"
	case "embedder":
		return "retrieval_embedder"
	case "transport":
		return "transport"
	case "tool_runtime":
		return "tool_runtime"
	case "client_profile":
		return "client_profile"
	default:
		return strings.TrimSpace(component.Category)
	}
}

func componentBackendProjections(component backendcap.Component) []plugincontract.Projection {
	class := strings.TrimSpace(component.CapabilityClass)
	if class == "" {
		class = backendcap.ClassLocalSubset
	}
	target := strings.TrimSpace(component.Backend)
	if target == "" {
		target = strings.TrimSpace(component.Kind)
	}
	if target == "" {
		target = strings.TrimSpace(component.ID)
	}
	sources := append([]string(nil), component.PublicSurfaces...)
	if len(sources) == 0 {
		for _, tool := range component.Tools {
			tool = strings.TrimSpace(tool)
			if tool != "" {
				sources = append(sources, "responses.tools."+tool)
			}
		}
	}
	if len(sources) == 0 {
		if component.Category != "" {
			sources = append(sources, component.Category)
		} else {
			sources = append(sources, component.ID)
		}
	}
	projections := make([]plugincontract.Projection, 0, len(sources))
	for _, source := range sources {
		source = strings.TrimSpace(source)
		if source == "" {
			continue
		}
		projections = append(projections, plugincontract.Projection{
			Class:        class,
			SourceFormat: source,
			TargetFormat: target,
		})
	}
	return projections
}
