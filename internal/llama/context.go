package llama

import (
	"context"
	"net/http"
)

type contextKey string

const forwardHeadersKey contextKey = "llama_forward_headers"

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
