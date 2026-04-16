package httpapi

import (
	"bufio"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseLocalMCPToolConfigsRejectsInvalidServerURL(t *testing.T) {
	tests := []struct {
		name      string
		serverURL string
		errNeedle string
	}{
		{name: "invalid scheme", serverURL: "file:///tmp/sse", errNeedle: "supports only http and https"},
		{name: "missing host", serverURL: "https:///sse", errNeedle: "must include a host"},
		{name: "userinfo", serverURL: "https://user:pass@example.com/sse", errNeedle: "must not include userinfo"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := map[string]json.RawMessage{
				"tools": json.RawMessage(`[{"type":"mcp","server_label":"srv","server_url":"` + tt.serverURL + `"}]`),
			}
			_, err := parseLocalMCPToolConfigs(raw)
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.errNeedle)
		})
	}
}

func TestParseLocalMCPToolConfigsAcceptsPublicServerURL(t *testing.T) {
	raw := map[string]json.RawMessage{
		"tools": json.RawMessage(`[{"type":"mcp","server_label":"srv","server_url":"https://example.com/sse"}]`),
	}

	configs, err := parseLocalMCPToolConfigs(raw)
	require.NoError(t, err)
	require.Len(t, configs, 1)
	require.Equal(t, "https://example.com/sse", configs[0].ServerURL)
}

func TestReadEndpointRejectsAbsoluteEndpointEvent(t *testing.T) {
	session := &localMCPLegacySession{
		streamURL: "https://example.com/sse",
		reader: bufio.NewReader(strings.NewReader(
			"event: endpoint\n" +
				"data: http://127.0.0.1:8080/mcp\n\n",
		)),
	}

	_, err := session.readEndpoint()
	require.Error(t, err)
	require.Contains(t, err.Error(), "relative URL")
}

func TestReadEndpointResolvesRelativeEndpointEvent(t *testing.T) {
	session := &localMCPLegacySession{
		streamURL: "https://example.com/sse",
		reader: bufio.NewReader(strings.NewReader(
			"event: endpoint\n" +
				"data: /mcp\n\n",
		)),
	}

	endpoint, err := session.readEndpoint()
	require.NoError(t, err)
	require.Equal(t, "https://example.com/mcp", endpoint)
}
