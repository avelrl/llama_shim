package llama

import (
	"context"
	"net/http"
)

type contextKey string

const forwardHeadersKey contextKey = "llama_forward_headers"
const upstreamRouteKey contextKey = "llama_upstream_route"

type UpstreamRoute struct {
	ProviderID    string
	PublicModel   string
	UpstreamModel string
	PluginID      string
	PluginVersion string
	BaseURL       string
	BearerToken   string
}

var forwardedRequestHeaders = []string{
	"Authorization",
	"Api-Key",
	"X-Api-Key",
	"X-Client-Request-Id",
	"OpenAI-Organization",
	"OpenAI-Project",
}

func ContextWithForwardHeaders(ctx context.Context, incoming http.Header) context.Context {
	headers := cloneForwardHeaders(incoming)
	if len(headers) == 0 {
		return ctx
	}
	return context.WithValue(ctx, forwardHeadersKey, headers)
}

func ContextWithUpstreamRoute(ctx context.Context, route UpstreamRoute) context.Context {
	if route.ProviderID == "" && route.BaseURL == "" && route.PublicModel == "" && route.UpstreamModel == "" && route.PluginID == "" {
		return ctx
	}
	return context.WithValue(ctx, upstreamRouteKey, route)
}

func UpstreamRouteFromContext(ctx context.Context) (UpstreamRoute, bool) {
	route, ok := ctx.Value(upstreamRouteKey).(UpstreamRoute)
	return route, ok
}

func applyContextHeaders(ctx context.Context, outgoing http.Header) {
	stored, _ := ctx.Value(forwardHeadersKey).(http.Header)
	for key, values := range stored {
		if outgoing.Get(key) != "" {
			continue
		}
		for _, value := range values {
			outgoing.Add(key, value)
		}
	}
}

func cloneForwardHeaders(incoming http.Header) http.Header {
	if len(incoming) == 0 {
		return nil
	}

	out := make(http.Header)
	for _, key := range forwardedRequestHeaders {
		values := incoming.Values(key)
		if len(values) == 0 {
			continue
		}
		out[key] = append([]string(nil), values...)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
