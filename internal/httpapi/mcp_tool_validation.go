package httpapi

import (
	"encoding/json"
	"strings"

	"llama_shim/internal/domain"
)

var supportedConnectorIDs = map[string]struct{}{
	"connector_dropbox":        {},
	"connector_gmail":          {},
	"connector_googlecalendar": {},
	"connector_googledrive":    {},
	"connector_microsoftteams": {},
	"connector_outlookcalendar": {},
	"connector_outlookemail":   {},
	"connector_sharepoint":     {},
}

func hasMCPToolDefinitions(rawFields map[string]json.RawMessage) bool {
	for _, tool := range decodeToolList(rawFields) {
		if strings.TrimSpace(asString(tool["type"])) == "mcp" {
			return true
		}
	}
	return false
}

func validateMCPToolDefinitions(rawFields map[string]json.RawMessage) error {
	tools := decodeToolList(rawFields)
	if len(tools) == 0 {
		return nil
	}

	seenLabels := make(map[string]struct{}, len(tools))
	for _, tool := range tools {
		if strings.TrimSpace(asString(tool["type"])) != "mcp" {
			continue
		}

		serverLabel := strings.TrimSpace(asString(tool["server_label"]))
		if serverLabel == "" {
			return domain.NewValidationError("tools", "mcp.server_label is required")
		}
		if _, ok := seenLabels[serverLabel]; ok {
			return domain.NewValidationError("tools", "duplicate mcp.server_label values are not supported")
		}
		seenLabels[serverLabel] = struct{}{}

		serverURL := strings.TrimSpace(asString(tool["server_url"]))
		connectorID := strings.TrimSpace(asString(tool["connector_id"]))
		switch {
		case serverURL != "" && connectorID != "":
			return domain.NewValidationError("tools", "mcp tools must set exactly one of server_url or connector_id")
		case serverURL == "" && connectorID == "":
			return domain.NewValidationError("tools", "mcp tools must set exactly one of server_url or connector_id")
		}

		authorization := strings.TrimSpace(asString(tool["authorization"]))
		if connectorID != "" {
			if _, ok := supportedConnectorIDs[connectorID]; !ok {
				return domain.NewValidationError("tools", "invalid mcp.connector_id")
			}
		}

		if err := validateMCPToolHeaders(tool["headers"], connectorID != "", authorization != ""); err != nil {
			return err
		}
	}

	return nil
}

func validateMCPToolHeaders(value any, connector bool, hasAuthorization bool) error {
	if value == nil {
		return nil
	}
	headers, ok := value.(map[string]any)
	if !ok {
		return domain.NewValidationError("tools", "mcp.headers must be an object of string values")
	}
	for key, entry := range headers {
		if _, ok := entry.(string); !ok {
			return domain.NewValidationError("tools", "mcp.headers must be an object of string values")
		}
		if !strings.EqualFold(strings.TrimSpace(key), "Authorization") {
			continue
		}
		switch {
		case connector:
			return domain.NewValidationError("tools", "connectors must not send headers.Authorization")
		case hasAuthorization:
			return domain.NewValidationError("tools", "mcp tools must not send both authorization and headers.Authorization")
		}
	}
	return nil
}
