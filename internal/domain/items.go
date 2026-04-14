package domain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

const storedItemEnvelopeKey = "__llama_shim_item"

type TextPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type ItemMeta struct {
	Transport        string            `json:"transport,omitempty"`
	SyntheticName    string            `json:"synthetic_name,omitempty"`
	CanonicalType    string            `json:"canonical_type,omitempty"`
	ToolName         string            `json:"tool_name,omitempty"`
	ToolNamespace    string            `json:"tool_namespace,omitempty"`
	MCPServerURL     string            `json:"mcp_server_url,omitempty"`
	MCPConnectorID   string            `json:"mcp_connector_id,omitempty"`
	MCPAuthorization string            `json:"mcp_authorization,omitempty"`
	MCPApproval      string            `json:"mcp_approval,omitempty"`
	MCPTransport     string            `json:"mcp_transport,omitempty"`
	MCPToolNames     []string          `json:"mcp_tool_names,omitempty"`
	MCPHeaders       map[string]string `json:"mcp_headers,omitempty"`
}

type Item struct {
	Raw     json.RawMessage `json:"-"`
	Meta    *ItemMeta       `json:"-"`
	Type    string          `json:"type,omitempty"`
	Role    string          `json:"role,omitempty"`
	Content []TextPart      `json:"content,omitempty"`
}

type MessageItem = Item

type storedItemEnvelope struct {
	Item json.RawMessage `json:"item"`
	Meta *ItemMeta       `json:"meta,omitempty"`
}

type ToolCallReference struct {
	Type      string
	Name      string
	Namespace string
	Meta      *ItemMeta
}

func (i Item) MarshalJSON() ([]byte, error) {
	if raw := bytes.TrimSpace(i.Raw); len(raw) > 0 {
		return append([]byte(nil), raw...), nil
	}

	payload := map[string]any{}
	if strings.TrimSpace(i.Type) != "" {
		payload["type"] = i.Type
	}
	if strings.TrimSpace(i.Role) != "" {
		payload["role"] = i.Role
	}
	if len(i.Content) > 0 {
		payload["content"] = i.Content
	}
	if len(payload) == 0 {
		return []byte("null"), nil
	}
	return json.Marshal(payload)
}

func (i *Item) UnmarshalJSON(data []byte) error {
	raw, err := compactRawJSON(data)
	if err != nil {
		return err
	}

	*i = Item{
		Raw: raw,
	}
	type basePayload struct {
		Type    string          `json:"type"`
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	var payload basePayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil
	}

	i.Type = strings.TrimSpace(payload.Type)
	if i.Type == "" && looksLikeMessageItem(payload.Content, payload.Role) {
		i.Type = "message"
	}
	i.Role = strings.TrimSpace(payload.Role)
	i.Content = extractTextParts(payload.Content)
	return nil
}

func (i Item) ID() string {
	return i.StringField("id")
}

func (i Item) CallID() string {
	return i.StringField("call_id")
}

func (i Item) Name() string {
	return i.StringField("name")
}

func (i Item) Namespace() string {
	return i.StringField("namespace")
}

func (i Item) Status() string {
	return i.StringField("status")
}

func (i Item) Phase() string {
	return i.StringField("phase")
}

func (i Item) Arguments() string {
	return i.StringField("arguments")
}

func (i Item) Input() string {
	return i.StringField("input")
}

func (i Item) OutputRaw() json.RawMessage {
	return i.RawField("output")
}

func (i Item) RawField(name string) json.RawMessage {
	if len(i.Raw) == 0 {
		return nil
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(i.Raw, &payload); err != nil {
		return nil
	}
	return payload[name]
}

func (i Item) StringField(name string) string {
	raw := i.RawField(name)
	if len(raw) == 0 {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	return strings.TrimSpace(value)
}

func (i Item) HasNonTextMessageContent() bool {
	if i.Type != "message" {
		return false
	}
	raw := bytes.TrimSpace(i.RawField("content"))
	if len(raw) == 0 || raw[0] == '"' {
		return false
	}

	var parts []map[string]any
	if err := json.Unmarshal(raw, &parts); err != nil {
		return true
	}
	for _, part := range parts {
		partType := strings.TrimSpace(asString(part["type"]))
		switch partType {
		case "", "text", "input_text", "output_text":
		default:
			return true
		}
	}
	return false
}

func (i Item) Map() map[string]any {
	if len(i.Raw) == 0 {
		return map[string]any{}
	}
	var payload map[string]any
	if err := json.Unmarshal(i.Raw, &payload); err != nil {
		return map[string]any{}
	}
	return payload
}

func (i Item) WithMap(payload map[string]any) (Item, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return Item{}, err
	}
	item, err := NewItem(raw)
	if err != nil {
		return Item{}, err
	}
	item.Meta = cloneItemMeta(i.Meta)
	return item, nil
}

func (i Item) WithField(name string, value any) (Item, error) {
	payload := i.Map()
	payload[name] = value
	return i.WithMap(payload)
}

func (i Item) WithID(id string) (Item, error) {
	if strings.TrimSpace(i.ID()) != "" {
		return i, nil
	}
	return i.WithField("id", id)
}

func (i Item) WithMeta(meta ItemMeta) Item {
	out := i
	out.Meta = cloneItemMeta(&meta)
	return out
}

func NewItem(raw []byte) (Item, error) {
	var item Item
	if err := item.UnmarshalJSON(raw); err != nil {
		return Item{}, err
	}
	return item, nil
}

func MarshalStoredItems(items []Item) ([]byte, error) {
	rawItems := make([]json.RawMessage, 0, len(items))
	for _, item := range items {
		raw, err := MarshalStoredItem(item)
		if err != nil {
			return nil, err
		}
		rawItems = append(rawItems, raw)
	}
	return json.Marshal(rawItems)
}

func UnmarshalStoredItems(data []byte) ([]Item, error) {
	var rawItems []json.RawMessage
	if err := json.Unmarshal(data, &rawItems); err != nil {
		return nil, err
	}

	items := make([]Item, 0, len(rawItems))
	for _, raw := range rawItems {
		item, err := UnmarshalStoredItem(raw)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func MarshalStoredItem(item Item) (json.RawMessage, error) {
	raw, err := item.MarshalJSON()
	if err != nil {
		return nil, err
	}
	if item.Meta == nil {
		return raw, nil
	}

	envelope, err := json.Marshal(map[string]any{
		storedItemEnvelopeKey: storedItemEnvelope{
			Item: raw,
			Meta: cloneItemMeta(item.Meta),
		},
	})
	if err != nil {
		return nil, err
	}
	return envelope, nil
}

func UnmarshalStoredItem(data []byte) (Item, error) {
	raw := bytes.TrimSpace(data)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return Item{}, nil
	}

	var wrapper map[string]json.RawMessage
	if err := json.Unmarshal(raw, &wrapper); err == nil {
		if rawEnvelope, ok := wrapper[storedItemEnvelopeKey]; ok {
			var envelope storedItemEnvelope
			if err := json.Unmarshal(rawEnvelope, &envelope); err != nil {
				return Item{}, fmt.Errorf("decode item envelope: %w", err)
			}
			item, err := NewItem(envelope.Item)
			if err != nil {
				return Item{}, err
			}
			item.Meta = cloneItemMeta(envelope.Meta)
			return item, nil
		}
	}

	return NewItem(raw)
}

func compactRawJSON(data []byte) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, nil
	}

	var buf bytes.Buffer
	if err := json.Compact(&buf, trimmed); err != nil {
		return nil, err
	}
	return append(json.RawMessage(nil), buf.Bytes()...), nil
}

func extractTextParts(raw json.RawMessage) []TextPart {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}

	if trimmed[0] == '"' {
		var text string
		if err := json.Unmarshal(trimmed, &text); err != nil {
			return nil
		}
		return []TextPart{{Type: "text", Text: text}}
	}

	var parts []map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &parts); err != nil {
		return nil
	}

	textParts := make([]TextPart, 0, len(parts))
	for _, part := range parts {
		partType := "text"
		if rawType, ok := part["type"]; ok {
			var value string
			if err := json.Unmarshal(rawType, &value); err == nil && strings.TrimSpace(value) != "" {
				partType = strings.TrimSpace(value)
			}
		}

		var text string
		if rawText, ok := part["text"]; ok {
			if err := json.Unmarshal(rawText, &text); err != nil {
				continue
			}
		}
		if text == "" {
			continue
		}
		textParts = append(textParts, TextPart{
			Type: partType,
			Text: text,
		})
	}
	return textParts
}

func looksLikeMessageItem(content json.RawMessage, role string) bool {
	if strings.TrimSpace(role) != "" {
		return true
	}
	trimmed := bytes.TrimSpace(content)
	return len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null"))
}

func cloneItemMeta(meta *ItemMeta) *ItemMeta {
	if meta == nil {
		return nil
	}
	cloned := *meta
	if len(meta.MCPToolNames) > 0 {
		cloned.MCPToolNames = append([]string(nil), meta.MCPToolNames...)
	}
	if len(meta.MCPHeaders) > 0 {
		cloned.MCPHeaders = make(map[string]string, len(meta.MCPHeaders))
		for key, value := range meta.MCPHeaders {
			cloned.MCPHeaders[key] = value
		}
	}
	return &cloned
}

func ValidateLocalShimItems(items []Item) error {
	for _, item := range items {
		if item.Type != "message" || item.HasNonTextMessageContent() {
			return ErrUnsupportedShape
		}

		role := item.Role
		switch role {
		case "system", "developer", "user", "assistant":
		default:
			return ErrUnsupportedShape
		}
	}
	return nil
}

func CollectToolCallReferences(items []Item) map[string]ToolCallReference {
	refs := make(map[string]ToolCallReference)
	for _, item := range items {
		callID := strings.TrimSpace(item.CallID())
		if callID == "" {
			continue
		}
		switch item.Type {
		case "function_call", "custom_tool_call":
			refs[callID] = ToolCallReference{
				Type:      item.Type,
				Name:      item.Name(),
				Namespace: item.Namespace(),
				Meta:      cloneItemMeta(item.Meta),
			}
		}
	}
	return refs
}

func CanonicalizeToolOutputs(items []Item, refs map[string]ToolCallReference) ([]Item, error) {
	if len(items) == 0 || len(refs) == 0 {
		return items, nil
	}

	out := make([]Item, 0, len(items))
	for _, item := range items {
		if item.Type != "function_call_output" && item.Type != "custom_tool_call_output" {
			out = append(out, item)
			continue
		}

		ref, ok := refs[item.CallID()]
		if !ok || ref.Type != "custom_tool_call" {
			out = append(out, item)
			continue
		}

		if item.Type == "custom_tool_call_output" {
			if item.Meta == nil && ref.Meta != nil {
				item.Meta = cloneItemMeta(ref.Meta)
			}
			out = append(out, item)
			continue
		}

		payload := item.Map()
		payload["type"] = "custom_tool_call_output"
		normalized, err := item.WithMap(payload)
		if err != nil {
			return nil, err
		}
		if ref.Meta != nil {
			normalized.Meta = cloneItemMeta(ref.Meta)
			if normalized.Meta.CanonicalType == "" {
				normalized.Meta.CanonicalType = "custom_tool_call_output"
			}
		}
		out = append(out, normalized)
	}
	return out, nil
}

func EnsureItemIDs(items []Item) ([]Item, error) {
	if len(items) == 0 {
		return nil, nil
	}

	out := make([]Item, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.ID()) != "" {
			out = append(out, item)
			continue
		}
		itemID, err := NewPrefixedID("item")
		if err != nil {
			return nil, err
		}
		withID, err := item.WithID(itemID)
		if err != nil {
			return nil, err
		}
		out = append(out, withID)
	}
	return out, nil
}

func asString(value any) string {
	text, _ := value.(string)
	return text
}
