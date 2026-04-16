package httpapi

import (
	"fmt"
	"net/url"
	"strings"
)

func validateLocalMCPServerURL(rawURL string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, fmt.Errorf("mcp.server_url must be a valid URL")
	}
	switch strings.ToLower(strings.TrimSpace(parsed.Scheme)) {
	case "http", "https":
	default:
		return nil, fmt.Errorf("mcp.server_url supports only http and https URLs")
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if host == "" {
		return nil, fmt.Errorf("mcp.server_url must include a host")
	}
	if parsed.User != nil {
		return nil, fmt.Errorf("mcp.server_url must not include userinfo")
	}
	return parsed, nil
}

func resolveLocalMCPLegacyEndpoint(streamURL string, endpoint string) (string, error) {
	base, err := validateLocalMCPServerURL(streamURL)
	if err != nil {
		return "", fmt.Errorf("parse MCP server URL: %w", err)
	}
	parsedEndpoint, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil {
		return "", fmt.Errorf("resolve MCP message endpoint: %w", err)
	}
	if parsedEndpoint.IsAbs() || strings.TrimSpace(parsedEndpoint.Host) != "" {
		return "", fmt.Errorf("MCP SSE endpoint event must be a relative URL")
	}
	resolved, err := base.Parse(parsedEndpoint.String())
	if err != nil {
		return "", fmt.Errorf("resolve MCP message endpoint: %w", err)
	}
	return resolved.String(), nil
}
