package httpapi

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"llama_shim/internal/domain"
	"llama_shim/internal/service"
)

func TestParseLocalComputerPlanNormalizesKnownActionType(t *testing.T) {
	plan, err := parseLocalComputerPlan(`{"decision":"computer_call","actions":[{"type":" CLICK ","button":"left","x":10,"y":20}]}`)
	require.NoError(t, err)
	require.Len(t, plan.Actions, 1)
	require.Equal(t, "click", plan.Actions[0]["type"])
}

func TestParseLocalComputerPlanExtractsFencedJSONObject(t *testing.T) {
	plan, err := parseLocalComputerPlan("I will request a screenshot.\n```json\n{\"decision\":\"computer_call\",\"actions\":[{\"type\":\"screenshot\"}]}\n```")
	require.NoError(t, err)
	require.Len(t, plan.Actions, 1)
	require.Equal(t, "screenshot", plan.Actions[0]["type"])
}

func TestParseLocalComputerPlanExtractsJSONObjectAfterReasoningText(t *testing.T) {
	plan, err := parseLocalComputerPlan("The next safe step is to inspect the screen. {\"decision\":\"computer_call\",\"actions\":[{\"type\":\"screenshot\"}]} Done.")
	require.NoError(t, err)
	require.Len(t, plan.Actions, 1)
	require.Equal(t, "screenshot", plan.Actions[0]["type"])
}

func TestParseLocalComputerPlanRejectsUnknownActionType(t *testing.T) {
	_, err := parseLocalComputerPlan(`{"decision":"computer_call","actions":[{"type":"read_file","path":"README.md"}]}`)
	require.Error(t, err)
}

func TestParseLocalComputerPlanNormalizesActionAlias(t *testing.T) {
	plan, err := parseLocalComputerPlan(`{"decision":"computer_call","actions":[{"action":"screenshot","action_input":null}]}`)
	require.NoError(t, err)
	require.Equal(t, []map[string]any{{"type": "screenshot"}}, plan.Actions)
}

func TestParseLocalComputerPlanNormalizesActionTypeAlias(t *testing.T) {
	plan, err := parseLocalComputerPlan(`{"decision":"computer_call","actions":[{"action_type":"screenshot"}]}`)
	require.NoError(t, err)
	require.Equal(t, []map[string]any{{"type": "screenshot"}}, plan.Actions)
}

func TestParseLocalComputerPlanMergesActionInput(t *testing.T) {
	plan, err := parseLocalComputerPlan(`{"decision":"computer_call","actions":[{"action":"click","action_input":{"x":636,"y":343,"button":"left"}},{"action":"type","action_input":"penguin"}]}`)
	require.NoError(t, err)
	require.Equal(t, "click", plan.Actions[0]["type"])
	require.Equal(t, float64(636), plan.Actions[0]["x"])
	require.Equal(t, float64(343), plan.Actions[0]["y"])
	require.Equal(t, "left", plan.Actions[0]["button"])
	require.Equal(t, map[string]any{"type": "type", "text": "penguin"}, plan.Actions[1])
}

func TestParseLocalComputerPlanNormalizesTopLevelActionArgs(t *testing.T) {
	plan, err := parseLocalComputerPlan(`{"action":"click","args":{"element":"search input","coordinate":[660,510]}}`)
	require.NoError(t, err)
	require.Equal(t, "computer_call", plan.Decision)
	require.Equal(t, []map[string]any{{
		"type": "click",
		"x":    float64(660),
		"y":    float64(510),
	}}, plan.Actions)
}

func TestParseLocalComputerPlanNormalizesTypeTextAlias(t *testing.T) {
	plan, err := parseLocalComputerPlan(`{"action":"type","args":{"value":"penguin"}}`)
	require.NoError(t, err)
	require.Equal(t, []map[string]any{{"type": "type", "text": "penguin"}}, plan.Actions)
}

func TestParseLocalComputerPlanAcceptsFixtureActionFamily(t *testing.T) {
	plan, err := parseLocalComputerPlan(`{"decision":"computer_call","actions":[{"type":"scroll","scroll_y":520},{"type":"keypress","key":"Enter"},{"type":"drag","path":[{"x":142,"y":642},{"x":400,"y":646}]}]}`)
	require.NoError(t, err)
	require.Equal(t, "scroll", plan.Actions[0]["type"])
	require.Equal(t, float64(520), plan.Actions[0]["scroll_y"])
	require.Equal(t, "keypress", plan.Actions[1]["type"])
	require.Equal(t, "Enter", plan.Actions[1]["key"])
	require.Equal(t, "drag", plan.Actions[2]["type"])
	require.Len(t, plan.Actions[2]["path"], 2)
}

func TestBuildLocalComputerPlannerBodyForcesJSONMode(t *testing.T) {
	item, err := domain.NewItem([]byte(`{"type":"message","role":"user","content":"Use the computer."}`))
	require.NoError(t, err)
	prepared := service.PreparedResponseContext{
		NormalizedInput: []domain.Item{item},
		ContextItems:    []domain.Item{item},
	}

	bodyBytes, err := buildLocalComputerPlannerBody("test-model", nil, prepared, true)
	require.NoError(t, err)

	var body map[string]any
	require.NoError(t, json.Unmarshal(bodyBytes, &body))
	require.Equal(t, "test-model", body["model"])
	require.Equal(t, float64(0), body["temperature"])
	require.Equal(t, map[string]any{"type": "json_object"}, body["response_format"])

	messages, ok := body["messages"].([]any)
	require.True(t, ok)
	require.Len(t, messages, 2)
	systemMessage, ok := messages[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "system", systemMessage["role"])
	require.Contains(t, systemMessage["content"], "Return JSON only")
}

func TestBuildLocalComputerPlannerBodyKeepsGenerationOverridesExceptResponseFormat(t *testing.T) {
	item, err := domain.NewItem([]byte(`{"type":"message","role":"user","content":"Use the computer."}`))
	require.NoError(t, err)
	prepared := service.PreparedResponseContext{
		NormalizedInput: []domain.Item{item},
		ContextItems:    []domain.Item{item},
	}
	options := map[string]json.RawMessage{
		"temperature":       json.RawMessage(`0.25`),
		"max_output_tokens": json.RawMessage(`77`),
		"response_format":   json.RawMessage(`{"type":"json_schema","json_schema":{"name":"external","schema":{"type":"object"}}}`),
	}

	bodyBytes, err := buildLocalComputerPlannerBody("test-model", options, prepared, false)
	require.NoError(t, err)

	var body map[string]any
	require.NoError(t, json.Unmarshal(bodyBytes, &body))
	require.Equal(t, float64(0.25), body["temperature"])
	require.Equal(t, float64(77), body["max_tokens"])
	require.Equal(t, map[string]any{"type": "json_object"}, body["response_format"])
	require.NotContains(t, body, "max_output_tokens")
}

func TestProjectLocalComputerCallOutputDefaultsScreenshotDetailAuto(t *testing.T) {
	item, err := domain.NewItem([]byte(`{
		"type": "computer_call_output",
		"call_id": "call_test",
		"output": {
			"type": "computer_screenshot",
			"image_url": "data:image/png;base64,ZmFrZQ=="
		}
	}`))
	require.NoError(t, err)

	message, err := projectLocalComputerCallOutputItem(item)
	require.NoError(t, err)

	parts, ok := message["content"].([]map[string]any)
	require.True(t, ok)
	require.Len(t, parts, 2)
	imageURL, ok := parts[1]["image_url"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "data:image/png;base64,ZmFrZQ==", imageURL["url"])
	require.Equal(t, "auto", imageURL["detail"])
}

func TestProjectLocalComputerCallOutputPreservesScreenshotDetail(t *testing.T) {
	item, err := domain.NewItem([]byte(`{
		"type": "computer_call_output",
		"call_id": "call_test",
		"output": {
			"type": "computer_screenshot",
			"image_url": "data:image/png;base64,ZmFrZQ==",
			"detail": "high"
		}
	}`))
	require.NoError(t, err)

	message, err := projectLocalComputerCallOutputItem(item)
	require.NoError(t, err)

	parts, ok := message["content"].([]map[string]any)
	require.True(t, ok)
	require.Len(t, parts, 2)
	imageURL, ok := parts[1]["image_url"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "high", imageURL["detail"])
}

func TestProjectLocalComputerCallOutputMapsOriginalScreenshotDetailToHigh(t *testing.T) {
	item, err := domain.NewItem([]byte(`{
		"type": "computer_call_output",
		"call_id": "call_test",
		"output": {
			"type": "computer_screenshot",
			"image_url": "data:image/png;base64,ZmFrZQ==",
			"detail": "original"
		}
	}`))
	require.NoError(t, err)

	message, err := projectLocalComputerCallOutputItem(item)
	require.NoError(t, err)

	parts, ok := message["content"].([]map[string]any)
	require.True(t, ok)
	require.Len(t, parts, 2)
	imageURL, ok := parts[1]["image_url"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "data:image/png;base64,ZmFrZQ==", imageURL["url"])
	require.Equal(t, "high", imageURL["detail"])
}
