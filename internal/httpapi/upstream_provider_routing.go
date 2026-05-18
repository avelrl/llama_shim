package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"

	"llama_shim/internal/config"
	"llama_shim/internal/domain"
	"llama_shim/internal/llama"
)

type upstreamProviderResolver struct {
	providersByID map[string]upstreamProviderConfig
	models        []upstreamProviderPublicModel
}

type upstreamProviderConfig struct {
	ID            string
	PluginID      string
	PluginVersion string
	BaseURL       string
	BearerToken   string
	modelsByID    map[string]upstreamProviderModelConfig
}

type upstreamProviderModelConfig struct {
	PublicModel   string
	UpstreamModel string
}

type upstreamProviderPublicModel struct {
	ID            string
	Provider      string
	UpstreamModel string
}

func newUpstreamProviderResolver(providers []config.LlamaProvider) *upstreamProviderResolver {
	if len(providers) == 0 {
		return nil
	}
	resolver := &upstreamProviderResolver{
		providersByID: make(map[string]upstreamProviderConfig, len(providers)),
	}
	for _, provider := range providers {
		providerID := strings.TrimSpace(provider.ID)
		if providerID == "" {
			continue
		}
		entry := upstreamProviderConfig{
			ID:            providerID,
			PluginID:      upstreamProviderPluginID(providerID),
			PluginVersion: modelProviderPluginVersion,
			BaseURL:       strings.TrimRight(strings.TrimSpace(provider.BaseURL), "/"),
			BearerToken:   strings.TrimSpace(provider.BearerToken),
			modelsByID:    make(map[string]upstreamProviderModelConfig, len(provider.Models)),
		}
		for _, model := range provider.Models {
			publicSuffix := strings.TrimSpace(model.Model)
			if publicSuffix == "" {
				continue
			}
			upstreamModel := strings.TrimSpace(model.UpstreamModel)
			if upstreamModel == "" {
				upstreamModel = publicSuffix
			}
			entry.modelsByID[publicSuffix] = upstreamProviderModelConfig{
				PublicModel:   publicSuffix,
				UpstreamModel: upstreamModel,
			}
			resolver.models = append(resolver.models, upstreamProviderPublicModel{
				ID:            providerID + "/" + publicSuffix,
				Provider:      providerID,
				UpstreamModel: upstreamModel,
			})
		}
		resolver.providersByID[providerID] = entry
	}
	sort.Slice(resolver.models, func(i, j int) bool {
		return resolver.models[i].ID < resolver.models[j].ID
	})
	if len(resolver.providersByID) == 0 {
		return nil
	}
	return resolver
}

func (r *upstreamProviderResolver) Enabled() bool {
	return r != nil && len(r.providersByID) > 0
}

func (r *upstreamProviderResolver) Resolve(publicModel string) (llama.UpstreamRoute, error) {
	if !r.Enabled() {
		return llama.UpstreamRoute{}, nil
	}
	publicModel = strings.TrimSpace(publicModel)
	providerID, modelSuffix, ok := strings.Cut(publicModel, "/")
	if !ok || strings.TrimSpace(providerID) == "" || strings.TrimSpace(modelSuffix) == "" {
		return llama.UpstreamRoute{}, domain.NewValidationError("model", "model must use configured provider/model form")
	}
	provider, ok := r.providersByID[strings.TrimSpace(providerID)]
	if !ok {
		return llama.UpstreamRoute{}, domain.NewValidationError("model", fmt.Sprintf("unknown model provider %q", strings.TrimSpace(providerID)))
	}
	model, ok := provider.modelsByID[strings.TrimSpace(modelSuffix)]
	if !ok {
		return llama.UpstreamRoute{}, domain.NewValidationError("model", fmt.Sprintf("unknown model %q for provider %q", strings.TrimSpace(modelSuffix), provider.ID))
	}
	return llama.UpstreamRoute{
		ProviderID:    provider.ID,
		PublicModel:   provider.ID + "/" + model.PublicModel,
		UpstreamModel: model.UpstreamModel,
		PluginID:      provider.PluginID,
		PluginVersion: provider.PluginVersion,
		BaseURL:       provider.BaseURL,
		BearerToken:   provider.BearerToken,
	}, nil
}

func (r *upstreamProviderResolver) ContextForModel(ctx context.Context, publicModel string) (context.Context, *llama.UpstreamRoute, error) {
	if !r.Enabled() {
		return ctx, nil, nil
	}
	route, err := r.Resolve(publicModel)
	if err != nil {
		return ctx, nil, err
	}
	routedCtx := llama.ContextWithUpstreamRoute(ctx, route)
	return routedCtx, &route, nil
}

func (h *proxyHandler) routeContextForModel(ctx context.Context, publicModel string) (context.Context, *llama.UpstreamRoute, error) {
	if h == nil || h.upstreamProviderResolver == nil {
		return ctx, nil, nil
	}
	routedCtx, route, err := h.upstreamProviderResolver.ContextForModel(ctx, publicModel)
	if err == nil && route != nil {
		RecordDebugTraceUpstreamRoute(routedCtx, *route)
	}
	return routedCtx, route, err
}

func (r *upstreamProviderResolver) WriteModels(ctx context.Context, w http.ResponseWriter, client *llama.Client) {
	catalogs, err := r.LiveProviderModelCatalogs(ctx, client)
	if err != nil {
		WriteError(w, http.StatusServiceUnavailable, "service_unavailable", "upstream provider model catalog is not ready", "")
		return
	}
	data := make([]map[string]any, 0, len(r.models))
	for _, model := range r.models {
		catalog := catalogs[model.Provider]
		if _, ok := catalog[model.UpstreamModel]; !ok {
			continue
		}
		data = append(data, map[string]any{
			"id":       model.ID,
			"object":   "model",
			"created":  0,
			"owned_by": "provider:" + model.Provider,
		})
	}
	if len(data) == 0 {
		WriteError(w, http.StatusServiceUnavailable, "service_unavailable", "upstream provider model catalog is not ready", "")
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"object": "list",
		"data":   data,
	})
}

func (r *upstreamProviderResolver) CheckReady(ctx context.Context, client *llama.Client) error {
	return providerReadinessAggregateError(r.ProviderReadiness(ctx, client))
}

func (r *upstreamProviderResolver) ProviderReadiness(ctx context.Context, client *llama.Client) map[string]error {
	if !r.Enabled() || client == nil {
		return nil
	}
	providerIDs := r.providerIDs()

	type result struct {
		providerID string
		err        error
	}
	results := make(chan result, len(providerIDs))
	var wg sync.WaitGroup
	for _, providerID := range providerIDs {
		provider := r.providersByID[providerID]
		wg.Add(1)
		go func(provider upstreamProviderConfig) {
			defer wg.Done()
			route := llama.UpstreamRoute{
				ProviderID:    provider.ID,
				PluginID:      provider.PluginID,
				PluginVersion: provider.PluginVersion,
				BaseURL:       provider.BaseURL,
				BearerToken:   provider.BearerToken,
			}
			models, err := client.ListModels(llama.ContextWithUpstreamRoute(ctx, route))
			if err == nil {
				err = providerModelCatalogReadyError(provider, models)
			}
			if err != nil {
				err = fmt.Errorf("provider %q is not ready: %w", provider.ID, err)
			}
			results <- result{providerID: provider.ID, err: err}
		}(provider)
	}
	wg.Wait()
	close(results)

	readiness := make(map[string]error, len(providerIDs))
	for result := range results {
		readiness[result.providerID] = result.err
	}
	return readiness
}

func (r *upstreamProviderResolver) providerIDs() []string {
	if !r.Enabled() {
		return nil
	}
	providerIDs := make([]string, 0, len(r.providersByID))
	for providerID := range r.providersByID {
		providerIDs = append(providerIDs, providerID)
	}
	sort.Strings(providerIDs)
	return providerIDs
}

func providerReadinessAggregateError(readiness map[string]error) error {
	if len(readiness) == 0 {
		return nil
	}
	providerIDs := make([]string, 0, len(readiness))
	for providerID := range readiness {
		providerIDs = append(providerIDs, providerID)
	}
	sort.Strings(providerIDs)
	var firstErr error
	for _, providerID := range providerIDs {
		err := readiness[providerID]
		if err == nil {
			return nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func providerModelCatalogReadyError(provider upstreamProviderConfig, models []string) error {
	if len(provider.modelsByID) == 0 {
		return nil
	}
	catalog := make(map[string]struct{}, len(models))
	for _, model := range models {
		model = strings.TrimSpace(model)
		if model != "" {
			catalog[model] = struct{}{}
		}
	}
	configured := make([]string, 0, len(provider.modelsByID))
	for _, model := range provider.modelsByID {
		upstreamModel := strings.TrimSpace(model.UpstreamModel)
		if upstreamModel == "" {
			continue
		}
		configured = append(configured, upstreamModel)
		if _, ok := catalog[upstreamModel]; ok {
			return nil
		}
	}
	sort.Strings(configured)
	return fmt.Errorf("model catalog does not contain any configured upstream models: %s", strings.Join(configured, ", "))
}

func (r *upstreamProviderResolver) LiveProviderModelCatalogs(ctx context.Context, client *llama.Client) (map[string]map[string]struct{}, error) {
	if !r.Enabled() || client == nil {
		return nil, fmt.Errorf("upstream provider routing is not enabled")
	}
	providerIDs := r.providerIDs()

	type result struct {
		providerID string
		models     []string
		err        error
	}
	results := make(chan result, len(providerIDs))
	var wg sync.WaitGroup
	for _, providerID := range providerIDs {
		provider := r.providersByID[providerID]
		wg.Add(1)
		go func(provider upstreamProviderConfig) {
			defer wg.Done()
			route := llama.UpstreamRoute{
				ProviderID:    provider.ID,
				PluginID:      provider.PluginID,
				PluginVersion: provider.PluginVersion,
				BaseURL:       provider.BaseURL,
				BearerToken:   provider.BearerToken,
			}
			models, err := client.ListModels(llama.ContextWithUpstreamRoute(ctx, route))
			if err != nil {
				err = fmt.Errorf("provider %q model catalog is not ready: %w", provider.ID, err)
			}
			results <- result{providerID: provider.ID, models: models, err: err}
		}(provider)
	}
	wg.Wait()
	close(results)

	catalogs := make(map[string]map[string]struct{}, len(providerIDs))
	var firstErr error
	for result := range results {
		if result.err != nil {
			if firstErr == nil {
				firstErr = result.err
			}
			continue
		}
		catalog := make(map[string]struct{}, len(result.models))
		for _, model := range result.models {
			catalog[model] = struct{}{}
		}
		catalogs[result.providerID] = catalog
	}
	if len(catalogs) == 0 && firstErr != nil {
		return nil, firstErr
	}
	return catalogs, nil
}

func rewriteRawFieldsModel(fields map[string]json.RawMessage, model string) (map[string]json.RawMessage, error) {
	if strings.TrimSpace(model) == "" {
		return fields, nil
	}
	rewritten := make(map[string]json.RawMessage, len(fields))
	for key, value := range fields {
		rewritten[key] = append(json.RawMessage(nil), value...)
	}
	rawModel, err := json.Marshal(model)
	if err != nil {
		return nil, err
	}
	rewritten["model"] = rawModel
	return rewritten, nil
}

func upstreamRawFieldsForContext(ctx context.Context, fields map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	route, ok := llama.UpstreamRouteFromContext(ctx)
	if !ok || strings.TrimSpace(route.UpstreamModel) == "" {
		return fields, nil
	}
	return rewriteRawFieldsModel(fields, route.UpstreamModel)
}

func restoreRoutedModelInResponseBody(ctx context.Context, body []byte) []byte {
	route, ok := llama.UpstreamRouteFromContext(ctx)
	if !ok || strings.TrimSpace(route.PublicModel) == "" || len(strings.TrimSpace(string(body))) == 0 {
		return body
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		return body
	}
	rawModel, err := json.Marshal(route.PublicModel)
	if err != nil {
		return body
	}
	payload["model"] = rawModel
	rewritten, err := json.Marshal(payload)
	if err != nil {
		return body
	}
	return rewritten
}

func restoreRoutedModelInResponse(ctx context.Context, response domain.Response) domain.Response {
	route, ok := llama.UpstreamRouteFromContext(ctx)
	if ok && strings.TrimSpace(route.PublicModel) != "" {
		response.Model = route.PublicModel
	}
	return response
}
