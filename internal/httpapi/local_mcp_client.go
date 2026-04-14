package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	localMCPProtocolVersion       = "2024-11-05"
	localMCPTransportLegacySSE    = "legacy_sse"
	localMCPTransportStreamable   = "streamable_http"
	localMCPSessionIDHeader       = "Mcp-Session-Id"
	localMCPProtocolVersionHeader = "MCP-Protocol-Version"
)

type localMCPClient struct {
	httpClient *http.Client
}

type localMCPTool struct {
	Name        string
	Description string
	Annotations any
	InputSchema any
}

type localMCPToolCallResult struct {
	Content []map[string]any
	IsError bool
}

type localMCPSession interface {
	Initialize(context.Context) error
	Request(context.Context, string, any) (json.RawMessage, error)
	Notify(context.Context, string, any) error
	Close() error
	Transport() string
}

type localMCPLegacySession struct {
	httpClient *http.Client
	config     localMCPServerConfig
	streamURL  string
	postURL    string
	body       io.ReadCloser
	reader     *bufio.Reader
	nextID     int64
}

type localMCPStreamableHTTPSession struct {
	httpClient *http.Client
	config     localMCPServerConfig
	endpoint   string
	sessionID  string
	nextID     int64
}

func newLocalMCPClient() *localMCPClient {
	return &localMCPClient{
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
}

func (c *localMCPClient) ListTools(ctx context.Context, config localMCPServerConfig) ([]localMCPTool, string, error) {
	session, err := c.openInitializedSession(ctx, config)
	if err != nil {
		return nil, "", err
	}
	defer session.Close()

	var (
		cursor string
		tools  []localMCPTool
	)
	for {
		payload := map[string]any{}
		if strings.TrimSpace(cursor) != "" {
			payload["cursor"] = cursor
		}
		result, err := session.Request(ctx, "tools/list", payload)
		if err != nil {
			return nil, "", err
		}

		pageTools, nextCursor, err := decodeLocalMCPToolsList(result)
		if err != nil {
			return nil, "", err
		}
		tools = append(tools, pageTools...)
		if strings.TrimSpace(nextCursor) == "" {
			return tools, session.Transport(), nil
		}
		cursor = nextCursor
	}
}

func (c *localMCPClient) CallTool(ctx context.Context, config localMCPServerConfig, name string, arguments json.RawMessage) (localMCPToolCallResult, error) {
	session, err := c.openInitializedSession(ctx, config)
	if err != nil {
		return localMCPToolCallResult{}, err
	}
	defer session.Close()

	params := map[string]any{
		"name": name,
	}
	if trimmed := bytes.TrimSpace(arguments); len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null")) {
		var decoded any
		if err := json.Unmarshal(trimmed, &decoded); err != nil {
			return localMCPToolCallResult{}, fmt.Errorf("decode MCP tool arguments: %w", err)
		}
		params["arguments"] = decoded
	}

	result, err := session.Request(ctx, "tools/call", params)
	if err != nil {
		return localMCPToolCallResult{}, err
	}
	return decodeLocalMCPToolCallResult(result)
}

func (c *localMCPClient) openInitializedSession(ctx context.Context, config localMCPServerConfig) (localMCPSession, error) {
	openers := []func(context.Context, localMCPServerConfig) (localMCPSession, error){}
	switch strings.TrimSpace(config.Transport) {
	case localMCPTransportLegacySSE:
		openers = append(openers, c.openLegacySession, c.openStreamableHTTPSession)
	case localMCPTransportStreamable:
		openers = append(openers, c.openStreamableHTTPSession, c.openLegacySession)
	default:
		if looksLikeLegacyMCPServerURL(config.ServerURL) {
			openers = append(openers, c.openLegacySession, c.openStreamableHTTPSession)
		} else {
			openers = append(openers, c.openStreamableHTTPSession, c.openLegacySession)
		}
	}

	var lastErr error
	for _, open := range openers {
		session, err := open(ctx, config)
		if err != nil {
			lastErr = err
			continue
		}
		if err := session.Initialize(ctx); err != nil {
			lastErr = err
			_ = session.Close()
			continue
		}
		return session, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("open MCP session failed")
	}
	return nil, lastErr
}

func (c *localMCPClient) openLegacySession(ctx context.Context, config localMCPServerConfig) (localMCPSession, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, config.ServerURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create MCP SSE request: %w", err)
	}
	localMCPApplyRequestHeaders(req, config, "text/event-stream", "")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("open MCP SSE stream: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return nil, fmt.Errorf("open MCP SSE stream returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if !strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return nil, fmt.Errorf("open MCP SSE stream returned unsupported content type %q: %s", resp.Header.Get("Content-Type"), strings.TrimSpace(string(body)))
	}

	session := &localMCPLegacySession{
		httpClient: c.httpClient,
		config:     config,
		streamURL:  config.ServerURL,
		body:       resp.Body,
		reader:     bufio.NewReader(resp.Body),
	}
	postEndpoint, err := session.readEndpoint()
	if err != nil {
		resp.Body.Close()
		return nil, err
	}
	session.postURL = postEndpoint
	return session, nil
}

func (c *localMCPClient) openStreamableHTTPSession(_ context.Context, config localMCPServerConfig) (localMCPSession, error) {
	return &localMCPStreamableHTTPSession{
		httpClient: c.httpClient,
		config:     config,
		endpoint:   config.ServerURL,
	}, nil
}

func (s *localMCPLegacySession) Transport() string {
	return localMCPTransportLegacySSE
}

func (s *localMCPStreamableHTTPSession) Transport() string {
	return localMCPTransportStreamable
}

func (s *localMCPLegacySession) Close() error {
	if s == nil || s.body == nil {
		return nil
	}
	return s.body.Close()
}

func (s *localMCPStreamableHTTPSession) Close() error {
	return nil
}

func (s *localMCPLegacySession) Initialize(ctx context.Context) error {
	params := map[string]any{
		"protocolVersion": localMCPProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "llama_shim",
			"version": "local",
		},
	}
	result, err := s.Request(ctx, "initialize", params)
	if err != nil {
		return err
	}

	var payload struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := json.Unmarshal(result, &payload); err != nil {
		return fmt.Errorf("decode MCP initialize response: %w", err)
	}
	if strings.TrimSpace(payload.ProtocolVersion) == "" {
		return fmt.Errorf("decode MCP initialize response: missing protocolVersion")
	}
	return s.Notify(ctx, "notifications/initialized", nil)
}

func (s *localMCPStreamableHTTPSession) Initialize(ctx context.Context) error {
	params := map[string]any{
		"protocolVersion": localMCPProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "llama_shim",
			"version": "local",
		},
	}
	result, err := s.Request(ctx, "initialize", params)
	if err != nil {
		return err
	}

	var payload struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := json.Unmarshal(result, &payload); err != nil {
		return fmt.Errorf("decode MCP initialize response: %w", err)
	}
	if strings.TrimSpace(payload.ProtocolVersion) == "" {
		return fmt.Errorf("decode MCP initialize response: missing protocolVersion")
	}
	return s.Notify(ctx, "notifications/initialized", nil)
}

func (s *localMCPLegacySession) Request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	s.nextID++
	id := s.nextID
	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
	}
	if params != nil {
		payload["params"] = params
	}
	if err := s.post(ctx, payload); err != nil {
		return nil, err
	}

	for {
		eventType, data, err := s.readEvent()
		if err != nil {
			return nil, err
		}
		if eventType != "message" {
			continue
		}
		result, matched, err := decodeLocalMCPResponsePayload(data, id)
		if err != nil {
			continue
		}
		if !matched {
			continue
		}
		return result, nil
	}
}

func (s *localMCPStreamableHTTPSession) Request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	s.nextID++
	id := s.nextID
	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
	}
	if params != nil {
		payload["params"] = params
	}
	return s.postRequest(ctx, payload, id)
}

func (s *localMCPLegacySession) Notify(ctx context.Context, method string, params any) error {
	payload := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
	}
	if params != nil {
		payload["params"] = params
	}
	return s.post(ctx, payload)
}

func (s *localMCPStreamableHTTPSession) Notify(ctx context.Context, method string, params any) error {
	payload := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
	}
	if params != nil {
		payload["params"] = params
	}
	_, err := s.postRequest(ctx, payload, 0)
	return err
}

func (s *localMCPLegacySession) post(ctx context.Context, payload map[string]any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.postURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create MCP POST request: %w", err)
	}
	localMCPApplyRequestHeaders(req, s.config, "application/json", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send MCP request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return fmt.Errorf("send MCP request returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	return nil
}

func (s *localMCPStreamableHTTPSession) postRequest(ctx context.Context, payload map[string]any, expectedID int64) (json.RawMessage, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create MCP streamable HTTP request: %w", err)
	}
	localMCPApplyRequestHeaders(req, s.config, "application/json, text/event-stream", "application/json")
	if strings.TrimSpace(s.sessionID) != "" {
		req.Header.Set(localMCPSessionIDHeader, s.sessionID)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send MCP streamable HTTP request: %w", err)
	}
	defer resp.Body.Close()
	if sessionID := strings.TrimSpace(resp.Header.Get(localMCPSessionIDHeader)); sessionID != "" {
		s.sessionID = sessionID
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return nil, fmt.Errorf("send MCP streamable HTTP request returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if expectedID == 0 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		return nil, nil
	}
	return decodeLocalMCPHTTPResponse(resp, expectedID)
}

func (s *localMCPLegacySession) readEndpoint() (string, error) {
	for {
		eventType, data, err := s.readEvent()
		if err != nil {
			return "", err
		}
		if eventType != "endpoint" {
			continue
		}
		endpoint := strings.TrimSpace(string(data))
		if endpoint == "" {
			return "", fmt.Errorf("MCP SSE stream returned empty endpoint event")
		}
		base, err := url.Parse(s.streamURL)
		if err != nil {
			return "", fmt.Errorf("parse MCP server URL: %w", err)
		}
		resolved, err := base.Parse(endpoint)
		if err != nil {
			return "", fmt.Errorf("resolve MCP message endpoint: %w", err)
		}
		return resolved.String(), nil
	}
}

func (s *localMCPLegacySession) readEvent() (string, []byte, error) {
	return readLocalMCPSSEEvent(s.reader)
}

func readLocalMCPSSEEvent(reader *bufio.Reader) (string, []byte, error) {
	var (
		eventType string
		dataLines []string
	)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return "", nil, fmt.Errorf("read MCP SSE event: %w", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if len(dataLines) == 0 {
				continue
			}
			return eventType, []byte(strings.Join(dataLines, "\n")), nil
		}
		switch {
		case strings.HasPrefix(line, "event:"):
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
}

func localMCPApplyRequestHeaders(req *http.Request, config localMCPServerConfig, accept string, contentType string) {
	if strings.TrimSpace(accept) != "" {
		req.Header.Set("Accept", accept)
	}
	if strings.TrimSpace(contentType) != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set(localMCPProtocolVersionHeader, localMCPProtocolVersion)
	for key, value := range config.Headers {
		req.Header.Set(key, value)
	}
	if token := localMCPAuthorizationHeader(config.Authorization); token != "" {
		req.Header.Set("Authorization", token)
	}
}

func localMCPAuthorizationHeader(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(token), "bearer ") {
		return token
	}
	return "Bearer " + token
}

func decodeLocalMCPHTTPResponse(resp *http.Response, expectedID int64) (json.RawMessage, error) {
	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.Contains(contentType, "text/event-stream") {
		reader := bufio.NewReader(resp.Body)
		for {
			_, data, err := readLocalMCPSSEEvent(reader)
			if err != nil {
				return nil, err
			}
			result, matched, err := decodeLocalMCPResponsePayload(data, expectedID)
			if err != nil {
				continue
			}
			if matched {
				return result, nil
			}
		}
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read MCP streamable HTTP response: %w", err)
	}
	result, matched, err := decodeLocalMCPResponsePayload(body, expectedID)
	if err != nil {
		return nil, err
	}
	if !matched {
		return nil, fmt.Errorf("decode MCP response: missing matching json-rpc id")
	}
	return result, nil
}

func decodeLocalMCPResponsePayload(data []byte, expectedID int64) (json.RawMessage, bool, error) {
	var response struct {
		ID     any             `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, false, err
	}
	if !localMCPJSONRPCIDMatches(response.ID, expectedID) {
		return nil, false, nil
	}
	if response.Error != nil {
		message := strings.TrimSpace(response.Error.Message)
		if message == "" {
			message = "unknown MCP error"
		}
		return nil, true, fmt.Errorf("MCP request failed: %s", message)
	}
	return response.Result, true, nil
}

func looksLikeLegacyMCPServerURL(serverURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(serverURL))
	if err != nil {
		return false
	}
	return strings.HasSuffix(strings.ToLower(parsed.Path), "/sse")
}

func localMCPJSONRPCIDMatches(value any, expected int64) bool {
	switch typed := value.(type) {
	case float64:
		return int64(typed) == expected
	case int64:
		return typed == expected
	case string:
		return strings.TrimSpace(typed) == fmt.Sprintf("%d", expected)
	default:
		return false
	}
}

func decodeLocalMCPToolsList(raw json.RawMessage) ([]localMCPTool, string, error) {
	var payload struct {
		Tools []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Annotations any    `json:"annotations"`
			InputSchema any    `json:"inputSchema"`
		} `json:"tools"`
		NextCursor string `json:"nextCursor"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, "", fmt.Errorf("decode MCP tools/list response: %w", err)
	}
	tools := make([]localMCPTool, 0, len(payload.Tools))
	for _, tool := range payload.Tools {
		name := strings.TrimSpace(tool.Name)
		if name == "" {
			continue
		}
		tools = append(tools, localMCPTool{
			Name:        name,
			Description: strings.TrimSpace(tool.Description),
			Annotations: tool.Annotations,
			InputSchema: tool.InputSchema,
		})
	}
	return tools, strings.TrimSpace(payload.NextCursor), nil
}

func decodeLocalMCPToolCallResult(raw json.RawMessage) (localMCPToolCallResult, error) {
	var payload struct {
		Content []map[string]any `json:"content"`
		IsError bool             `json:"isError"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return localMCPToolCallResult{}, fmt.Errorf("decode MCP tools/call response: %w", err)
	}
	return localMCPToolCallResult{
		Content: payload.Content,
		IsError: payload.IsError,
	}, nil
}
