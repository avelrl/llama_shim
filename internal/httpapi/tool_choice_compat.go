package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
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
