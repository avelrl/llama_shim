package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"llama_shim/internal/ssetrace"
)

type headerFlags []string

func (h *headerFlags) String() string {
	return strings.Join(*h, ", ")
}

func (h *headerFlags) Set(value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("header must not be empty")
	}
	*h = append(*h, value)
	return nil
}

func main() {
	var headers headerFlags

	method := flag.String("method", http.MethodPost, "HTTP method")
	baseURL := flag.String("base-url", defaultString(os.Getenv("OPENAI_BASE_URL"), "https://api.openai.com"), "base URL for the upstream API")
	path := flag.String("path", "/v1/responses", "path relative to base-url, or a full URL")
	requestFile := flag.String("request-file", "", "path to JSON request body, or - to read from stdin")
	apiKeyEnv := flag.String("api-key-env", "OPENAI_API_KEY", "environment variable that contains the API key")
	rawOut := flag.String("raw-out", "", "path to write the raw SSE body")
	fixtureOut := flag.String("fixture-out", "", "path to write the parsed fixture JSON")
	label := flag.String("label", "", "optional label stored in the fixture metadata")
	timeout := flag.Duration("timeout", 90*time.Second, "HTTP client timeout")
	flag.Var(&headers, "header", "extra HTTP header in Name=Value form; may be passed multiple times")
	flag.Parse()

	if *rawOut == "" && *fixtureOut == "" {
		exitf("at least one of -raw-out or -fixture-out is required")
	}

	requestBody, err := loadRequestBody(*requestFile)
	if err != nil {
		exitf("load request body: %v", err)
	}
	if requestBody == nil && strings.EqualFold(strings.TrimSpace(*method), http.MethodPost) {
		exitf("-request-file is required for POST captures")
	}

	apiKey := strings.TrimSpace(os.Getenv(*apiKeyEnv))
	if apiKey == "" {
		exitf("environment variable %s is not set", *apiKeyEnv)
	}

	url := buildURL(*baseURL, *path)
	req, err := http.NewRequest(strings.ToUpper(strings.TrimSpace(*method)), url, bytes.NewReader(requestBody))
	if err != nil {
		exitf("build request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Accept", "text/event-stream")
	if len(requestBody) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, header := range headers {
		name, value, ok := strings.Cut(header, "=")
		if !ok {
			exitf("invalid -header %q, expected Name=Value", header)
		}
		req.Header.Set(strings.TrimSpace(name), strings.TrimSpace(value))
	}

	client := &http.Client{Timeout: *timeout}
	resp, err := client.Do(req)
	if err != nil {
		exitf("do request: %v", err)
	}
	defer resp.Body.Close()

	rawBody, err := io.ReadAll(resp.Body)
	if err != nil {
		exitf("read response body: %v", err)
	}

	if *rawOut != "" {
		if err := writeFile(*rawOut, rawBody); err != nil {
			exitf("write raw SSE file: %v", err)
		}
	}

	stream, err := ssetrace.Parse(rawBody)
	if err != nil {
		exitf("parse SSE body: %v", err)
	}

	if *fixtureOut != "" {
		fixture := ssetrace.CaptureFixture{
			CapturedAt:      time.Now().UTC().Format(time.RFC3339),
			Label:           strings.TrimSpace(*label),
			Method:          req.Method,
			URL:             url,
			StatusCode:      resp.StatusCode,
			ContentType:     resp.Header.Get("Content-Type"),
			Request:         decodeJSONOrString(requestBody),
			ResponseHeaders: cloneHeaders(resp.Header),
			Stream:          stream,
		}

		body, err := json.MarshalIndent(fixture, "", "  ")
		if err != nil {
			exitf("marshal fixture: %v", err)
		}
		body = append(body, '\n')
		if err := writeFile(*fixtureOut, body); err != nil {
			exitf("write fixture file: %v", err)
		}
	}

	fmt.Fprintf(os.Stdout, "captured %d SSE events", stream.EventCount)
	if stream.Done {
		fmt.Fprint(os.Stdout, " (+ [DONE])")
	}
	fmt.Fprintf(os.Stdout, " from %s %s with HTTP %d\n", req.Method, url, resp.StatusCode)
	if *rawOut != "" {
		fmt.Fprintf(os.Stdout, "raw: %s\n", *rawOut)
	}
	if *fixtureOut != "" {
		fmt.Fprintf(os.Stdout, "fixture: %s\n", *fixtureOut)
	}
	if resp.StatusCode >= http.StatusBadRequest {
		os.Exit(2)
	}
}

func buildURL(baseURL, path string) string {
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return path
	}
	return strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(path, "/")
}

func loadRequestBody(path string) ([]byte, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	if path == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(path)
}

func decodeJSONOrString(body []byte) any {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return nil
	}
	var decoded any
	if json.Unmarshal(trimmed, &decoded) == nil {
		return decoded
	}
	return string(trimmed)
}

func cloneHeaders(header http.Header) map[string][]string {
	if len(header) == 0 {
		return nil
	}
	allowed := map[string]struct{}{
		"Alt-Svc":                        {},
		"Cf-Cache-Status":                {},
		"Content-Type":                   {},
		"Date":                           {},
		"Openai-Processing-Ms":           {},
		"Openai-Version":                 {},
		"Server":                         {},
		"X-Content-Type-Options":         {},
		"X-Ratelimit-Limit-Requests":     {},
		"X-Ratelimit-Limit-Tokens":       {},
		"X-Ratelimit-Remaining-Requests": {},
		"X-Ratelimit-Remaining-Tokens":   {},
		"X-Ratelimit-Reset-Requests":     {},
		"X-Ratelimit-Reset-Tokens":       {},
	}
	cloned := make(map[string][]string, len(allowed))
	for key, values := range header {
		if _, ok := allowed[http.CanonicalHeaderKey(key)]; !ok {
			continue
		}
		cloned[key] = append([]string(nil), values...)
	}
	if len(cloned) == 0 {
		return nil
	}
	return cloned
}

func writeFile(path string, body []byte) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, body, 0o644)
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func exitf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
