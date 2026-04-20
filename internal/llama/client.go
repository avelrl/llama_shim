package llama

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"llama_shim/internal/domain"
)

type Client struct {
	baseURL       string
	requestClient *http.Client
	streamClient  *http.Client
}

var hopByHopHeaders = map[string]struct{}{
	"Connection":          {},
	"Keep-Alive":          {},
	"Proxy-Authenticate":  {},
	"Proxy-Authorization": {},
	"Te":                  {},
	"Trailer":             {},
	"Transfer-Encoding":   {},
	"Upgrade":             {},
}

type UpstreamError struct {
	StatusCode int
	Message    string
}

func (e *UpstreamError) Error() string {
	return fmt.Sprintf("llama upstream returned %d: %s", e.StatusCode, e.Message)
}

type TimeoutError struct {
	Message string
}

func (e *TimeoutError) Error() string {
	if e.Message == "" {
		return "llama upstream timeout"
	}
	return e.Message
}

type InvalidResponseError struct {
	Message string
}

func (e *InvalidResponseError) Error() string {
	return e.Message
}

func NewClient(baseURL string, timeout time.Duration) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		requestClient: &http.Client{
			Timeout: timeout,
		},
		streamClient: &http.Client{},
	}
}

func (c *Client) CreateResponse(ctx context.Context, requestBody []byte) ([]byte, error) {
	endpoint, err := url.JoinPath(c.baseURL, "/v1/responses")
	if err != nil {
		return nil, fmt.Errorf("build llama url: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(requestBody))
	if err != nil {
		return nil, fmt.Errorf("create llama request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	applyContextHeaders(ctx, req.Header)

	resp, err := c.requestClient.Do(req)
	if err != nil {
		if mappedErr := mapTimeoutError(err); mappedErr != nil {
			return nil, mappedErr
		}
		return nil, fmt.Errorf("call llama: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("read llama response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &UpstreamError{
			StatusCode: resp.StatusCode,
			Message:    string(bytes.TrimSpace(body)),
		}
	}

	return body, nil
}

func (c *Client) CreateChatCompletion(ctx context.Context, requestBody []byte) ([]byte, error) {
	endpoint, err := url.JoinPath(c.baseURL, "/v1/chat/completions")
	if err != nil {
		return nil, fmt.Errorf("build llama url: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(requestBody))
	if err != nil {
		return nil, fmt.Errorf("create llama request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	applyContextHeaders(ctx, req.Header)

	resp, err := c.requestClient.Do(req)
	if err != nil {
		if mappedErr := mapTimeoutError(err); mappedErr != nil {
			return nil, mappedErr
		}
		return nil, fmt.Errorf("call llama: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("read llama response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &UpstreamError{
			StatusCode: resp.StatusCode,
			Message:    string(bytes.TrimSpace(body)),
		}
	}

	return body, nil
}

func (c *Client) CreateChatCompletionText(ctx context.Context, requestBody []byte) (string, error) {
	body, err := c.CreateChatCompletion(ctx, requestBody)
	if err != nil {
		return "", err
	}
	return extractAssistantText(body)
}

func (c *Client) CheckReady(ctx context.Context) error {
	if c == nil {
		return errors.New("llama client is nil")
	}

	endpoint, err := url.JoinPath(c.baseURL, "/v1/models")
	if err != nil {
		return fmt.Errorf("build llama url: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("create llama request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	applyContextHeaders(ctx, req.Header)

	resp, err := c.requestClient.Do(req)
	if err != nil {
		if mappedErr := mapTimeoutError(err); mappedErr != nil {
			return mappedErr
		}
		return fmt.Errorf("call llama: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read llama response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &UpstreamError{
			StatusCode: resp.StatusCode,
			Message:    string(bytes.TrimSpace(body)),
		}
	}

	var payload struct {
		Object string            `json:"object"`
		Data   []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return fmt.Errorf("decode llama response: %w", err)
	}
	if payload.Object != "" && payload.Object != "list" {
		return &InvalidResponseError{Message: "llama models response did not contain a list object"}
	}
	if payload.Data == nil {
		return &InvalidResponseError{Message: "llama models response did not contain data"}
	}

	return nil
}

func (c *Client) Generate(ctx context.Context, model string, items []domain.Item, options map[string]json.RawMessage) (string, error) {
	requestBody, err := c.buildChatCompletionRequest(model, items, false, options)
	if err != nil {
		return "", fmt.Errorf("marshal llama request: %w", err)
	}

	endpoint, err := url.JoinPath(c.baseURL, "/v1/chat/completions")
	if err != nil {
		return "", fmt.Errorf("build llama url: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(requestBody))
	if err != nil {
		return "", fmt.Errorf("create llama request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	applyContextHeaders(ctx, req.Header)

	resp, err := c.requestClient.Do(req)
	if err != nil {
		if mappedErr := mapTimeoutError(err); mappedErr != nil {
			return "", mappedErr
		}
		return "", fmt.Errorf("call llama: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("read llama response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", &UpstreamError{
			StatusCode: resp.StatusCode,
			Message:    string(bytes.TrimSpace(body)),
		}
	}

	return extractAssistantText(body)
}

func (c *Client) GenerateStream(ctx context.Context, model string, items []domain.Item, options map[string]json.RawMessage, onDelta func(string) error) error {
	requestBody, err := c.buildChatCompletionRequest(model, items, true, options)
	if err != nil {
		return fmt.Errorf("marshal llama request: %w", err)
	}

	endpoint, err := url.JoinPath(c.baseURL, "/v1/chat/completions")
	if err != nil {
		return fmt.Errorf("build llama url: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(requestBody))
	if err != nil {
		return fmt.Errorf("create llama request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	applyContextHeaders(ctx, req.Header)

	resp, err := c.streamClient.Do(req)
	if err != nil {
		if mappedErr := mapTimeoutError(err); mappedErr != nil {
			return mappedErr
		}
		return fmt.Errorf("call llama: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if readErr != nil {
			return fmt.Errorf("read llama error response: %w", readErr)
		}
		return &UpstreamError{
			StatusCode: resp.StatusCode,
			Message:    string(bytes.TrimSpace(body)),
		}
	}

	if !strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if err != nil {
			return fmt.Errorf("read llama response: %w", err)
		}
		text, err := extractAssistantText(body)
		if err != nil {
			return err
		}
		return onDelta(text)
	}

	return c.consumeChatCompletionStream(resp.Body, onDelta)
}

func (c *Client) Proxy(ctx context.Context, incoming *http.Request) (*http.Response, error) {
	endpoint, err := c.buildUpstreamURL(incoming.URL.Path, incoming.URL.RawQuery)
	if err != nil {
		return nil, fmt.Errorf("build llama url: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, incoming.Method, endpoint, incoming.Body)
	if err != nil {
		return nil, fmt.Errorf("create llama request: %w", err)
	}
	req.Header = cloneHeader(incoming.Header)
	removeHopByHopHeaders(req.Header)
	applyForwardedHeaders(req, incoming)
	applyContextHeaders(ctx, req.Header)

	resp, err := c.streamClient.Do(req)
	if err != nil {
		if mappedErr := mapTimeoutError(err); mappedErr != nil {
			return nil, mappedErr
		}
		return nil, fmt.Errorf("call llama: %w", err)
	}

	return resp, nil
}

func (c *Client) buildUpstreamURL(path, rawQuery string) (string, error) {
	endpoint, err := url.JoinPath(c.baseURL, path)
	if err != nil {
		return "", err
	}
	if rawQuery == "" {
		return endpoint, nil
	}
	return endpoint + "?" + rawQuery, nil
}

func cloneHeader(src http.Header) http.Header {
	dst := make(http.Header, len(src))
	for key, values := range src {
		copied := append([]string(nil), values...)
		dst[key] = copied
	}
	return dst
}

func removeHopByHopHeaders(header http.Header) {
	for key := range hopByHopHeaders {
		header.Del(key)
	}
}

func applyForwardedHeaders(outgoing *http.Request, incoming *http.Request) {
	if requestID := incoming.Header.Get("X-Request-Id"); requestID != "" {
		outgoing.Header.Set("X-Request-Id", requestID)
	}

	if host, _, err := net.SplitHostPort(incoming.RemoteAddr); err == nil && host != "" {
		if prior := outgoing.Header.Get("X-Forwarded-For"); prior != "" {
			outgoing.Header.Set("X-Forwarded-For", prior+", "+host)
		} else {
			outgoing.Header.Set("X-Forwarded-For", host)
		}
	}
	if incoming.Host != "" {
		outgoing.Header.Set("X-Forwarded-Host", incoming.Host)
	}
	if incoming.TLS != nil {
		outgoing.Header.Set("X-Forwarded-Proto", "https")
	} else {
		outgoing.Header.Set("X-Forwarded-Proto", "http")
	}
}

func (c *Client) buildChatCompletionRequest(model string, items []domain.Item, stream bool, options map[string]json.RawMessage) ([]byte, error) {
	messages, err := collapseChatMessages(items)
	if err != nil {
		return nil, err
	}
	payload := map[string]any{
		"model":    model,
		"messages": messages,
	}
	if stream {
		payload["stream"] = true
	}
	for key, raw := range options {
		targetKey := key
		if key == "max_output_tokens" {
			targetKey = "max_tokens"
		}
		payload[targetKey] = json.RawMessage(raw)
	}

	return json.Marshal(payload)
}

func collapseChatMessages(items []domain.Item) ([]ChatMessageDTO, error) {
	messages := make([]ChatMessageDTO, 0, len(items))
	for _, item := range items {
		if item.Type != "message" || item.HasNonTextMessageContent() {
			return nil, domain.ErrUnsupportedShape
		}

		role := item.Role
		if role == "developer" {
			role = "system"
		}
		switch role {
		case "system", "user", "assistant":
		default:
			return nil, domain.ErrUnsupportedShape
		}

		content := domain.MessageText(item)
		messages = append(messages, ChatMessageDTO{
			Role:    role,
			Content: content,
		})
	}
	return messages, nil
}

func (c *Client) consumeChatCompletionStream(body io.Reader, onDelta func(string) error) error {
	reader := bufio.NewReader(body)
	var (
		dataLines []string
		seenText  bool
	)

	flushEvent := func() error {
		if len(dataLines) == 0 {
			return nil
		}

		payload := strings.Join(dataLines, "\n")
		dataLines = nil

		if payload == "[DONE]" {
			return io.EOF
		}

		var chunk chatCompletionResponse
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			return fmt.Errorf("decode llama stream chunk: %w", err)
		}

		if len(chunk.Choices) == 0 {
			return nil
		}

		text := extractStreamChunkText(chunk.Choices[0])
		if text == "" {
			return nil
		}

		seenText = true
		return onDelta(text)
	}

	for {
		line, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("read llama stream: %w", err)
		}

		line = strings.TrimRight(line, "\r\n")
		switch {
		case line == "":
			if flushErr := flushEvent(); flushErr != nil {
				if errors.Is(flushErr, io.EOF) {
					if seenText {
						return nil
					}
					return &InvalidResponseError{Message: "llama stream ended without text"}
				}
				return flushErr
			}
		case strings.HasPrefix(line, ":"):
			// SSE comment/heartbeat.
		case strings.HasPrefix(line, "data:"):
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}

		if errors.Is(err, io.EOF) {
			break
		}
	}

	if err := flushEvent(); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if !seenText {
		return &InvalidResponseError{Message: "llama stream ended without text"}
	}
	return nil
}

func extractAssistantText(body []byte) (string, error) {
	var payload chatCompletionResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("decode llama response: %w", err)
	}
	if len(payload.Choices) == 0 {
		return "", &InvalidResponseError{Message: "llama response did not contain choices"}
	}

	text := extractMessageContent(payload.Choices[0].Message.Content)
	if strings.TrimSpace(text) == "" {
		return "", &InvalidResponseError{Message: "llama response content was empty"}
	}
	return text, nil
}

func extractStreamChunkText(choice chatCompletionChoice) string {
	if text := extractMessageContent(choice.Delta.Content); text != "" {
		return text
	}
	return extractMessageContent(choice.Message.Content)
}

func extractMessageContent(raw json.RawMessage) string {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return ""
	}

	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}

	var parts []struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err == nil {
		var builder strings.Builder
		for _, part := range parts {
			builder.WriteString(part.Text)
		}
		return builder.String()
	}

	return ""
}

func mapTimeoutError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return &TimeoutError{}
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Timeout() {
		return &TimeoutError{}
	}
	return nil
}
