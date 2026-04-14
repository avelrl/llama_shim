package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"llama_shim/internal/domain"
	"llama_shim/internal/llama"
)

type toolChoiceContractMode string

const (
	toolChoiceContractRequiredAny           toolChoiceContractMode = "required_any"
	toolChoiceContractRequiredNamedFunction toolChoiceContractMode = "required_named_function"
	toolChoiceContractRequiredNamedCustom   toolChoiceContractMode = "required_named_custom"
)

type toolChoiceContract struct {
	Mode      toolChoiceContractMode
	Name      string
	Namespace string
}

type toolChoiceIncompatibleBackendError struct {
	Message string
}

func (e *toolChoiceIncompatibleBackendError) Error() string {
	return strings.TrimSpace(e.Message)
}

func (c toolChoiceContract) Active() bool {
	return c.Mode != ""
}

func deriveToolChoiceContract(raw json.RawMessage, upstreamChoice any) toolChoiceContract {
	var literal string
	if err := json.Unmarshal(raw, &literal); err == nil {
		if strings.EqualFold(strings.TrimSpace(literal), "required") {
			return toolChoiceContract{Mode: toolChoiceContractRequiredAny}
		}
		if upstreamLiteral, ok := upstreamChoice.(string); ok && strings.EqualFold(strings.TrimSpace(upstreamLiteral), "required") {
			return toolChoiceContract{Mode: toolChoiceContractRequiredAny}
		}
		return toolChoiceContract{}
	}

	var choice map[string]any
	if err := json.Unmarshal(raw, &choice); err != nil {
		return toolChoiceContract{}
	}

	switch strings.ToLower(strings.TrimSpace(asString(choice["type"]))) {
	case "allowed_tools":
		mode := strings.ToLower(strings.TrimSpace(asString(choice["mode"])))
		if mode == "required" {
			return toolChoiceContract{Mode: toolChoiceContractRequiredAny}
		}
		if upstreamLiteral, ok := upstreamChoice.(string); ok && strings.EqualFold(strings.TrimSpace(upstreamLiteral), "required") {
			return toolChoiceContract{Mode: toolChoiceContractRequiredAny}
		}
		return toolChoiceContract{}
	case "function":
		name := strings.TrimSpace(asString(choice["name"]))
		if name == "" {
			return toolChoiceContract{}
		}
		return toolChoiceContract{
			Mode: toolChoiceContractRequiredNamedFunction,
			Name: name,
		}
	case "custom", "custom_tool":
		name, namespace := customToolIdentity(choice)
		if name == "" {
			return toolChoiceContract{}
		}
		return toolChoiceContract{
			Mode:      toolChoiceContractRequiredNamedCustom,
			Name:      name,
			Namespace: namespace,
		}
	default:
		return toolChoiceContract{}
	}
}

func rewriteToolChoiceToAuto(body []byte) ([]byte, error) {
	fields, err := decodeRawFields(body)
	if err != nil {
		return nil, err
	}
	fields["tool_choice"] = json.RawMessage(`"auto"`)
	return json.Marshal(fields)
}

func rewriteToolChoiceRetryBody(body []byte) ([]byte, error) {
	fields, err := decodeRawFields(body)
	if err != nil {
		return nil, err
	}
	fields["tool_choice"] = json.RawMessage(`"auto"`)
	fields["stream"] = json.RawMessage(`false`)
	return json.Marshal(fields)
}

func shouldRetryToolChoiceWithAutoError(err error, plan customToolTransportPlan) bool {
	var upstreamErr *llama.UpstreamError
	if !errors.As(err, &upstreamErr) {
		return false
	}
	return shouldRetryToolChoiceWithAutoBody(upstreamErr.StatusCode, []byte(upstreamErr.Message), plan)
}

func shouldRetryToolChoiceWithAutoResponse(resp *http.Response, plan customToolTransportPlan) (bool, error) {
	if resp == nil || (resp.StatusCode >= 200 && resp.StatusCode < 300) {
		return false, nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, err
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	return shouldRetryToolChoiceWithAutoBody(resp.StatusCode, body, plan), nil
}

func shouldRetryToolChoiceWithAutoBody(status int, body []byte, plan customToolTransportPlan) bool {
	if !plan.ToolChoiceContract.Active() || status < 400 {
		return false
	}

	message := strings.ToLower(strings.TrimSpace(extractAPIErrorMessage(body)))
	if message == "" {
		message = strings.ToLower(strings.TrimSpace(string(body)))
	}

	return strings.Contains(message, "tool_choice") &&
		strings.Contains(message, "auto") &&
		strings.Contains(message, "supported") &&
		strings.Contains(message, "only")
}

func shouldRetryCustomToolsWithBridgeError(err error, plan customToolTransportPlan) bool {
	var upstreamErr *llama.UpstreamError
	if !errors.As(err, &upstreamErr) {
		return false
	}
	return shouldRetryCustomToolsWithBridgeBody(upstreamErr.StatusCode, []byte(upstreamErr.Message), plan)
}

func shouldRetryCustomToolsWithBridgeResponse(resp *http.Response, plan customToolTransportPlan) (bool, error) {
	if resp == nil || (resp.StatusCode >= 200 && resp.StatusCode < 300) {
		return false, nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, err
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	return shouldRetryCustomToolsWithBridgeBody(resp.StatusCode, body, plan), nil
}

func shouldRetryCustomToolsWithBridgeBody(status int, body []byte, plan customToolTransportPlan) bool {
	if status < 400 || plan.Mode != customToolsModePassthrough || !plan.BridgeFallbackSafe {
		return false
	}

	message := strings.ToLower(strings.TrimSpace(extractAPIErrorMessage(body)))
	if message == "" {
		message = strings.ToLower(strings.TrimSpace(string(body)))
	}

	return strings.Contains(message, "tool type custom not supported") ||
		strings.Contains(message, "'type' of tool must be 'function'") ||
		(strings.Contains(message, "tool") && strings.Contains(message, "custom") && strings.Contains(message, "not supported"))
}

func shouldRetryLocalStateWithDirectProxyError(err error, request CreateResponseRequest) bool {
	var upstreamErr *llama.UpstreamError
	if !errors.As(err, &upstreamErr) {
		return false
	}
	return shouldRetryLocalStateWithDirectProxyBody(upstreamErr.StatusCode, []byte(upstreamErr.Message), request)
}

func shouldRetryLocalStateWithDirectProxyResponse(resp *http.Response, request CreateResponseRequest) (bool, error) {
	if resp == nil || (resp.StatusCode >= 200 && resp.StatusCode < 300) {
		return false, nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, err
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	return shouldRetryLocalStateWithDirectProxyBody(resp.StatusCode, body, request), nil
}

func shouldRetryLocalStateWithDirectProxyBody(status int, body []byte, request CreateResponseRequest) bool {
	if status < 400 || request.PreviousResponseID == "" || request.Conversation != "" {
		return false
	}

	message := strings.ToLower(strings.TrimSpace(extractAPIErrorMessage(body)))
	if message == "" {
		message = strings.ToLower(strings.TrimSpace(string(body)))
	}

	return strings.Contains(message, "input should be a valid string") ||
		(strings.Contains(message, "validation errors") && strings.Contains(message, "body', 'input', 'str"))
}

func shouldRetryResponsesInputAsStringError(err error, requestBody []byte) bool {
	var upstreamErr *llama.UpstreamError
	if !errors.As(err, &upstreamErr) {
		return false
	}
	return shouldRetryResponsesInputAsStringBody(upstreamErr.StatusCode, []byte(upstreamErr.Message), requestBody)
}

func shouldRetryResponsesInputAsStringResponse(resp *http.Response, requestBody []byte) (bool, error) {
	if resp == nil || (resp.StatusCode >= 200 && resp.StatusCode < 300) {
		return false, nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, err
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	return shouldRetryResponsesInputAsStringBody(resp.StatusCode, body, requestBody), nil
}

func shouldRetryResponsesInputAsStringBody(status int, body []byte, requestBody []byte) bool {
	if status < 400 || !requestBodyHasStructuredInput(requestBody) {
		return false
	}

	message := strings.ToLower(strings.TrimSpace(extractAPIErrorMessage(body)))
	if message == "" {
		message = strings.ToLower(strings.TrimSpace(string(body)))
	}

	return strings.Contains(message, "input should be a valid string") ||
		(strings.Contains(message, "validation errors") && strings.Contains(message, "body', 'input', 'str")) ||
		(strings.Contains(message, "field required") && strings.Contains(message, "'input': {'type': 'message'"))
}

func requestBodyHasStructuredInput(body []byte) bool {
	fields, err := decodeRawFields(body)
	if err != nil {
		return false
	}
	rawInput, ok := fields["input"]
	if !ok {
		return false
	}
	trimmed := bytes.TrimSpace(rawInput)
	if len(trimmed) == 0 {
		return false
	}
	switch trimmed[0] {
	case '[', '{':
		return true
	default:
		return false
	}
}

func rewriteResponsesInputAsStringBody(body []byte) ([]byte, error) {
	fields, err := decodeRawFields(body)
	if err != nil {
		return nil, err
	}

	rawInput, ok := fields["input"]
	if !ok {
		return nil, domain.NewValidationError("input", "input is required")
	}
	if !requestBodyHasStructuredInput(body) {
		return body, nil
	}

	stringified, err := stringifyResponsesInput(rawInput)
	if err != nil {
		return nil, err
	}
	fields["input"] = json.RawMessage(strconv.Quote(stringified))
	return json.Marshal(fields)
}

func stringifyResponsesInput(rawInput json.RawMessage) (string, error) {
	items, err := domain.NormalizeInput(rawInput)
	if err != nil {
		compact, compactErr := domain.CompactJSON(rawInput)
		if compactErr != nil {
			return "", err
		}
		return compact, nil
	}

	parts := make([]string, 0, len(items))
	for _, item := range items {
		switch item.Type {
		case "message":
			text := strings.TrimSpace(domain.MessageText(item))
			if text == "" {
				continue
			}
			role := strings.TrimSpace(item.Role)
			if role == "" {
				role = "user"
			}
			parts = append(parts, strings.ToUpper(role)+":\n"+text)
		case "function_call":
			header := "ASSISTANT FUNCTION CALL"
			if name := strings.TrimSpace(item.Name()); name != "" {
				header += " " + name
			}
			if callID := strings.TrimSpace(item.CallID()); callID != "" {
				header += " (" + callID + ")"
			}
			parts = append(parts, header+":\n"+strings.TrimSpace(item.Arguments()))
		case "custom_tool_call":
			header := "ASSISTANT CUSTOM TOOL CALL"
			if namespace := strings.TrimSpace(item.Namespace()); namespace != "" {
				header += " " + namespace + "."
			} else {
				header += " "
			}
			if name := strings.TrimSpace(item.Name()); name != "" {
				header += name
			}
			if callID := strings.TrimSpace(item.CallID()); callID != "" {
				header += " (" + callID + ")"
			}
			parts = append(parts, strings.TrimSpace(header)+":\n"+strings.TrimSpace(item.Input()))
		case "function_call_output", "custom_tool_call_output":
			header := strings.ToUpper(strings.ReplaceAll(item.Type, "_", " "))
			if callID := strings.TrimSpace(item.CallID()); callID != "" {
				header += " (" + callID + ")"
			}
			parts = append(parts, header+":\n"+stringifyResponsesItemOutput(item.OutputRaw()))
		default:
			raw, marshalErr := item.MarshalJSON()
			if marshalErr != nil {
				continue
			}
			compact, compactErr := domain.CompactJSON(raw)
			if compactErr != nil || compact == "" {
				continue
			}
			label := strings.ToUpper(strings.TrimSpace(item.Type))
			if label == "" {
				label = "ITEM"
			}
			parts = append(parts, label+":\n"+compact)
		}
	}

	if len(parts) == 0 {
		compact, err := domain.CompactJSON(rawInput)
		if err != nil {
			return "", domain.NewValidationError("input", "input must not be empty")
		}
		return compact, nil
	}
	return strings.Join(parts, "\n\n"), nil
}

func stringifyResponsesItemOutput(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return ""
	}
	if trimmed[0] == '"' {
		var text string
		if err := json.Unmarshal(trimmed, &text); err == nil {
			return text
		}
	}

	var parts []map[string]any
	if err := json.Unmarshal(trimmed, &parts); err == nil {
		var builder strings.Builder
		for _, part := range parts {
			text := strings.TrimSpace(asString(part["text"]))
			if text == "" {
				continue
			}
			if builder.Len() > 0 {
				builder.WriteString("\n")
			}
			builder.WriteString(text)
		}
		if builder.Len() > 0 {
			return builder.String()
		}
	}

	compact, err := domain.CompactJSON(trimmed)
	if err != nil {
		return string(trimmed)
	}
	return compact
}

func extractAPIErrorMessage(body []byte) string {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	errorPayload, ok := payload["error"].(map[string]any)
	if !ok {
		return ""
	}
	return strings.TrimSpace(asString(errorPayload["message"]))
}

func enforceToolChoiceContract(response domain.Response, contract toolChoiceContract) error {
	if !contract.Active() {
		return nil
	}

	for _, item := range response.Output {
		switch item.Type {
		case "", "reasoning":
			continue
		case "function_call", "custom_tool_call":
			if contractMatchesOutputItem(contract, item) {
				return nil
			}
			return &toolChoiceIncompatibleBackendError{
				Message: buildToolChoiceMismatchMessage(contract, item),
			}
		default:
			return &toolChoiceIncompatibleBackendError{
				Message: buildToolChoiceMissingMessage(contract),
			}
		}
	}

	if strings.TrimSpace(response.OutputText) != "" {
		return &toolChoiceIncompatibleBackendError{
			Message: buildToolChoiceMissingMessage(contract),
		}
	}

	return &toolChoiceIncompatibleBackendError{
		Message: buildToolChoiceMissingMessage(contract),
	}
}

func contractMatchesOutputItem(contract toolChoiceContract, item domain.Item) bool {
	switch contract.Mode {
	case toolChoiceContractRequiredAny:
		return item.Type == "function_call" || item.Type == "custom_tool_call"
	case toolChoiceContractRequiredNamedFunction:
		return item.Type == "function_call" && strings.EqualFold(strings.TrimSpace(item.Name()), contract.Name)
	case toolChoiceContractRequiredNamedCustom:
		if item.Type != "custom_tool_call" || !strings.EqualFold(strings.TrimSpace(item.Name()), contract.Name) {
			return false
		}
		if contract.Namespace == "" {
			return true
		}
		return strings.EqualFold(strings.TrimSpace(item.Namespace()), contract.Namespace)
	default:
		return false
	}
}

func buildToolChoiceMissingMessage(contract toolChoiceContract) string {
	switch contract.Mode {
	case toolChoiceContractRequiredAny:
		return "backend returned assistant output instead of the required tool call"
	case toolChoiceContractRequiredNamedFunction:
		return fmt.Sprintf("backend returned assistant output instead of the required function call %q", contract.Name)
	case toolChoiceContractRequiredNamedCustom:
		if contract.Namespace == "" {
			return fmt.Sprintf("backend returned assistant output instead of the required custom tool call %q", contract.Name)
		}
		return fmt.Sprintf("backend returned assistant output instead of the required custom tool call %q", contract.Namespace+"."+contract.Name)
	default:
		return "backend returned assistant output instead of the required tool call"
	}
}

func buildToolChoiceMismatchMessage(contract toolChoiceContract, item domain.Item) string {
	switch contract.Mode {
	case toolChoiceContractRequiredNamedFunction:
		return fmt.Sprintf("backend returned function call %q instead of the required function call %q", strings.TrimSpace(item.Name()), contract.Name)
	case toolChoiceContractRequiredNamedCustom:
		got := strings.TrimSpace(item.Name())
		if namespace := strings.TrimSpace(item.Namespace()); namespace != "" {
			got = namespace + "." + got
		}
		want := contract.Name
		if contract.Namespace != "" {
			want = contract.Namespace + "." + want
		}
		return fmt.Sprintf("backend returned custom tool call %q instead of the required custom tool call %q", got, want)
	default:
		return buildToolChoiceMissingMessage(contract)
	}
}
