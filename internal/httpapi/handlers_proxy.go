package httpapi

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"llama_shim/internal/llama"
)

type proxyHandler struct {
	logger *slog.Logger
	client *llama.Client
}

func newProxyHandler(logger *slog.Logger, client *llama.Client) *proxyHandler {
	return &proxyHandler{
		logger: logger,
		client: client,
	}
}

func (h *proxyHandler) forward(w http.ResponseWriter, r *http.Request) {
	h.forwardRequest(w, r)
}

func (h *proxyHandler) forwardWithBody(w http.ResponseWriter, r *http.Request, body []byte) {
	cloned := r.Clone(r.Context())
	cloned.Body = io.NopCloser(bytes.NewReader(body))
	cloned.ContentLength = int64(len(body))
	cloned.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(body)), nil
	}
	h.forwardRequest(w, cloned)
}

func (h *proxyHandler) forwardRequest(w http.ResponseWriter, r *http.Request) {
	// All non-shim routes pass through unchanged so the shim can coexist
	// with any OpenAI-compatible backend surface area we do not own.
	if r.Header.Get("X-Request-Id") == "" {
		r.Header.Set("X-Request-Id", RequestIDFromContext(r.Context()))
	}
	response, err := h.client.Proxy(r.Context(), r)
	if err != nil {
		status, payload := MapError(r.Context(), h.logger, err)
		WriteJSON(w, status, apiErrorPayload{Error: payload})
		return
	}
	defer response.Body.Close()

	copyResponseHeaders(w.Header(), response.Header)
	isSSE := strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), "text/event-stream")
	if isSSE {
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Accel-Buffering", "no")
		disableWriteDeadline(w)
	}
	w.WriteHeader(response.StatusCode)

	if !isSSE {
		_, _ = io.Copy(w, response.Body)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		return
	}
	flusher.Flush()

	buf := make([]byte, 1024)
	for {
		n, err := response.Body.Read(buf)
		if n > 0 {
			if _, writeErr := w.Write(buf[:n]); writeErr != nil {
				return
			}
			flusher.Flush()
		}
		if err != nil {
			return
		}
	}
}

func copyResponseHeaders(dst, src http.Header) {
	for key, values := range src {
		if shouldSkipHeader(key) {
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func shouldSkipHeader(key string) bool {
	switch http.CanonicalHeaderKey(key) {
	case "Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Te", "Trailer", "Transfer-Encoding", "Upgrade":
		return true
	default:
		return false
	}
}
