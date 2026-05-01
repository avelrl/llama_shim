package codexeval

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"

	"github.com/coder/websocket"
)

const requestShapeArtifactName = "request-shapes.json"
const requestShapeMaxBodyBytes = 1 << 20

type requestShapeArtifact struct {
	Version         int                    `json:"version"`
	UpstreamBaseURL string                 `json:"upstream_base_url"`
	Requests        []CapturedRequestShape `json:"requests"`
}

type CapturedRequestShape struct {
	Transport                 string            `json:"transport"`
	Method                    string            `json:"method"`
	Path                      string            `json:"path"`
	Query                     string            `json:"query,omitempty"`
	Headers                   map[string]string `json:"headers,omitempty"`
	BodyFields                []string          `json:"body_fields,omitempty"`
	BodyInvalidJSON           string            `json:"body_invalid_json,omitempty"`
	BodyTruncated             bool              `json:"body_truncated,omitempty"`
	Error                     string            `json:"error,omitempty"`
	Type                      string            `json:"type,omitempty"`
	Model                     string            `json:"model,omitempty"`
	Stream                    *bool             `json:"stream,omitempty"`
	Store                     *bool             `json:"store,omitempty"`
	Generate                  *bool             `json:"generate,omitempty"`
	ToolChoicePresent         bool              `json:"tool_choice_present,omitempty"`
	ParallelToolCalls         *bool             `json:"parallel_tool_calls,omitempty"`
	PreviousResponseID        string            `json:"previous_response_id,omitempty"`
	PreviousResponseIDPresent bool              `json:"previous_response_id_present,omitempty"`
	ToolNames                 []string          `json:"tool_names,omitempty"`
	ToolTypes                 []string          `json:"tool_types,omitempty"`
	InputItemTypes            []string          `json:"input_item_types,omitempty"`
	InputItemCount            int               `json:"input_item_count,omitempty"`
}

type requestShapeCapture struct {
	upstreamBaseURL string
	upstream        *url.URL
	artifactPath    string
	server          *httptest.Server
	client          *http.Client
	mu              sync.Mutex
	requests        []CapturedRequestShape
}

func startRequestShapeCapture(upstreamBaseURL, artifactPath string) (*requestShapeCapture, error) {
	upstream, err := url.Parse(strings.TrimRight(upstreamBaseURL, "/"))
	if err != nil {
		return nil, fmt.Errorf("parse upstream base URL: %w", err)
	}
	if upstream.Scheme != "http" && upstream.Scheme != "https" {
		return nil, fmt.Errorf("unsupported upstream base URL scheme %q", upstream.Scheme)
	}
	capture := &requestShapeCapture{
		upstreamBaseURL: upstream.String(),
		upstream:        upstream,
		artifactPath:    artifactPath,
		client:          &http.Client{},
	}
	capture.server = httptest.NewServer(http.HandlerFunc(capture.handle))
	return capture, nil
}

func (capture *requestShapeCapture) ProviderBaseURL() string {
	proxy, _ := url.Parse(capture.server.URL)
	proxy.Path = strings.TrimRight(capture.upstream.Path, "/")
	return strings.TrimRight(proxy.String(), "/")
}

func (capture *requestShapeCapture) Close() error {
	capture.server.Close()
	return nil
}

func (capture *requestShapeCapture) WriteArtifact() error {
	capture.mu.Lock()
	requests := append([]CapturedRequestShape(nil), capture.requests...)
	capture.mu.Unlock()
	return writeJSON(capture.artifactPath, requestShapeArtifact{
		Version:         1,
		UpstreamBaseURL: capture.upstreamBaseURL,
		Requests:        requests,
	})
}

func (capture *requestShapeCapture) handle(w http.ResponseWriter, r *http.Request) {
	if isWebSocketUpgrade(r) {
		capture.handleWebSocket(w, r)
		return
	}
	capture.handleHTTP(w, r)
}

func (capture *requestShapeCapture) handleHTTP(w http.ResponseWriter, r *http.Request) {
	body, truncated, err := readCaptureBody(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
		return
	}
	capture.record(capturedShapeFromBody("http", r.Method, r.URL, r.Header, body, truncated))

	target := capture.targetURL(r.URL)
	req, err := http.NewRequestWithContext(r.Context(), r.Method, target.String(), bytes.NewReader(body))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	req.Header = forwardHeaders(r.Header)
	req.ContentLength = int64(len(body))
	resp, err := capture.client.Do(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	copyResponseHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (capture *requestShapeCapture) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	capture.record(capturedShapeFromBody("websocket_handshake", http.MethodGet, r.URL, r.Header, nil, false))
	clientConn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		return
	}
	defer clientConn.Close(websocket.StatusNormalClosure, "")
	clientConn.SetReadLimit(requestShapeMaxBodyBytes)

	target := capture.targetURL(r.URL)
	target.Scheme = websocketScheme(target.Scheme)
	upstreamConn, _, err := websocket.Dial(r.Context(), target.String(), &websocket.DialOptions{
		HTTPHeader: forwardHeaders(r.Header),
	})
	if err != nil {
		capture.record(capturedShapeError("websocket_error", "DIAL", r.URL, r.Header, err))
		_ = clientConn.Close(websocket.StatusPolicyViolation, err.Error())
		return
	}
	defer upstreamConn.Close(websocket.StatusNormalClosure, "")
	upstreamConn.SetReadLimit(requestShapeMaxBodyBytes)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	errs := make(chan error, 2)
	go capture.pumpClientWebSocket(ctx, clientConn, upstreamConn, r, errs)
	go pumpWebSocket(ctx, upstreamConn, clientConn, errs)
	err = <-errs
	cancel()
	if err != nil && !isExpectedWebSocketClose(err) {
		capture.record(capturedShapeError("websocket_error", "PUMP", r.URL, r.Header, err))
		_ = clientConn.Close(websocket.StatusInternalError, err.Error())
		_ = upstreamConn.Close(websocket.StatusInternalError, err.Error())
	}
}

func (capture *requestShapeCapture) pumpClientWebSocket(ctx context.Context, src, dst *websocket.Conn, original *http.Request, errs chan<- error) {
	for {
		messageType, raw, err := src.Read(ctx)
		if err != nil {
			errs <- err
			return
		}
		if messageType == websocket.MessageText {
			capture.record(capturedShapeFromBody("websocket", "MESSAGE", original.URL, original.Header, raw, false))
		}
		if err := dst.Write(ctx, messageType, raw); err != nil {
			errs <- err
			return
		}
	}
}

func pumpWebSocket(ctx context.Context, src, dst *websocket.Conn, errs chan<- error) {
	for {
		messageType, raw, err := src.Read(ctx)
		if err != nil {
			errs <- err
			return
		}
		if err := dst.Write(ctx, messageType, raw); err != nil {
			errs <- err
			return
		}
	}
}

func (capture *requestShapeCapture) targetURL(incoming *url.URL) url.URL {
	target := *capture.upstream
	basePath := strings.TrimRight(capture.upstream.EscapedPath(), "/")
	incomingPath := incoming.EscapedPath()
	suffix := incomingPath
	if basePath != "" {
		if incomingPath == basePath {
			suffix = ""
		} else if strings.HasPrefix(incomingPath, basePath+"/") {
			suffix = strings.TrimPrefix(incomingPath, basePath)
		}
	}
	target.Path = joinURLPath(capture.upstream.EscapedPath(), suffix)
	target.RawQuery = incoming.RawQuery
	return target
}

func (capture *requestShapeCapture) record(shape CapturedRequestShape) {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	capture.requests = append(capture.requests, shape)
}

func capturedShapeFromBody(transport, method string, requestURL *url.URL, header http.Header, body []byte, truncated bool) CapturedRequestShape {
	shape := CapturedRequestShape{
		Transport: transport,
		Method:    method,
		Path:      requestURL.EscapedPath(),
		Query:     requestURL.RawQuery,
		Headers:   redactedHeaders(header),
	}
	if truncated {
		shape.BodyTruncated = true
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return shape
	}
	var object map[string]any
	if err := json.Unmarshal(body, &object); err != nil {
		shape.BodyInvalidJSON = err.Error()
		return shape
	}
	shape.BodyFields = sortedStringKeysAny(object)
	shape.Type = stringField(object, "type")
	shape.Model = stringField(object, "model")
	shape.Stream = boolField(object, "stream")
	shape.Store = boolField(object, "store")
	shape.Generate = boolField(object, "generate")
	shape.ParallelToolCalls = boolField(object, "parallel_tool_calls")
	_, shape.ToolChoicePresent = object["tool_choice"]
	if _, ok := object["previous_response_id"]; ok {
		shape.PreviousResponseIDPresent = true
		shape.PreviousResponseID = stringField(object, "previous_response_id")
	}
	shape.ToolNames, shape.ToolTypes = summarizeTools(object["tools"])
	shape.InputItemTypes, shape.InputItemCount = summarizeInput(object["input"])
	return shape
}

func capturedShapeError(transport, method string, requestURL *url.URL, header http.Header, err error) CapturedRequestShape {
	shape := capturedShapeFromBody(transport, method, requestURL, header, nil, false)
	if err != nil {
		shape.Error = err.Error()
	}
	return shape
}

func readCaptureBody(body io.ReadCloser) ([]byte, bool, error) {
	defer body.Close()
	raw, err := io.ReadAll(io.LimitReader(body, requestShapeMaxBodyBytes+1))
	if err != nil {
		return nil, false, err
	}
	if len(raw) > requestShapeMaxBodyBytes {
		return raw[:requestShapeMaxBodyBytes], true, fmt.Errorf("request body exceeds eval capture limit")
	}
	return raw, false, nil
}

func summarizeTools(value any) ([]string, []string) {
	values, ok := value.([]any)
	if !ok {
		return nil, nil
	}
	names := make([]string, 0, len(values))
	types := make([]string, 0, len(values))
	for _, item := range values {
		object, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if typ := stringField(object, "type"); typ != "" {
			types = append(types, typ)
		}
		if name := stringField(object, "name"); name != "" {
			names = append(names, name)
			continue
		}
		if fn, ok := object["function"].(map[string]any); ok {
			if name := stringField(fn, "name"); name != "" {
				names = append(names, name)
			}
		}
	}
	return sortedUnique(names), sortedUnique(types)
}

func summarizeInput(value any) ([]string, int) {
	items, ok := value.([]any)
	if !ok {
		if value == nil {
			return nil, 0
		}
		return nil, 1
	}
	types := make([]string, 0, len(items))
	for _, item := range items {
		object, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if typ := stringField(object, "type"); typ != "" {
			types = append(types, typ)
		}
	}
	return sortedUnique(types), len(items)
}

func redactedHeaders(header http.Header) map[string]string {
	result := make(map[string]string, len(header))
	for key, values := range header {
		normalized := strings.ToLower(key)
		if shouldRedactHeader(normalized) {
			result[normalized] = "[REDACTED]"
			continue
		}
		result[normalized] = strings.Join(values, ",")
	}
	return result
}

func shouldRedactHeader(key string) bool {
	for _, marker := range []string{"authorization", "api-key", "token", "secret", "cookie"} {
		if strings.Contains(key, marker) {
			return true
		}
	}
	return false
}

func forwardHeaders(header http.Header) http.Header {
	result := make(http.Header, len(header))
	for key, values := range header {
		if isHopByHopHeader(key) {
			continue
		}
		for _, value := range values {
			result.Add(key, value)
		}
	}
	return result
}

func isHopByHopHeader(key string) bool {
	switch strings.ToLower(key) {
	case "connection", "keep-alive", "proxy-authenticate", "proxy-authorization", "te", "trailer", "transfer-encoding", "upgrade":
		return true
	}
	return strings.HasPrefix(strings.ToLower(key), "sec-websocket-")
}

func copyResponseHeaders(dst, src http.Header) {
	for key, values := range src {
		if isHopByHopHeader(key) {
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func stringField(object map[string]any, key string) string {
	value, _ := object[key].(string)
	return value
}

func boolField(object map[string]any, key string) *bool {
	value, ok := object[key].(bool)
	if !ok {
		return nil
	}
	return &value
}

func sortedStringKeysAny(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedUnique(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func joinURLPath(base, suffix string) string {
	base = strings.TrimRight(base, "/")
	suffix = strings.TrimLeft(suffix, "/")
	if base == "" {
		return "/" + suffix
	}
	if suffix == "" {
		return base
	}
	return base + "/" + suffix
}

func isWebSocketUpgrade(r *http.Request) bool {
	return strings.EqualFold(strings.TrimSpace(r.Header.Get("Upgrade")), "websocket")
}

func websocketScheme(scheme string) string {
	if scheme == "https" {
		return "wss"
	}
	return "ws"
}

func isExpectedWebSocketClose(err error) bool {
	if err == nil {
		return true
	}
	status := websocket.CloseStatus(err)
	return status == websocket.StatusNormalClosure ||
		status == websocket.StatusGoingAway ||
		status == websocket.StatusNoStatusRcvd ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, io.EOF)
}
