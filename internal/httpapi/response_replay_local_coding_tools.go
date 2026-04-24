package httpapi

import (
	"strings"
	"unicode/utf8"
)

type responseReplayProfile struct {
	includeShellToolFamily      bool
	includeApplyPatchToolFamily bool
}

var (
	completedResponseReplayProfile = responseReplayProfile{
		includeShellToolFamily:      true,
		includeApplyPatchToolFamily: true,
	}
	retrieveResponseReplayProfile = responseReplayProfile{
		includeApplyPatchToolFamily: true,
	}
)

func forEachLocalCodingToolReplayEvent(item map[string]any, itemID string, outputIndex int, includeObfuscation bool, profile responseReplayProfile, visit func(hostedToolReplayEventSpec) error) error {
	switch strings.TrimSpace(asString(item["type"])) {
	case localBuiltinShellCallType:
		if !profile.includeShellToolFamily {
			return nil
		}
		action, _ := item["action"].(map[string]any)
		rawCommands, _ := action["commands"].([]any)
		for commandIndex, rawCommand := range rawCommands {
			command := strings.TrimSpace(asString(rawCommand))
			if err := visit(hostedToolReplayEventSpec{
				eventType: "response.shell_call_command.added",
				payload: map[string]any{
					"type":          "response.shell_call_command.added",
					"command":       "",
					"command_index": commandIndex,
					"output_index":  outputIndex,
				},
			}); err != nil {
				return err
			}
			if command != "" {
				deltaPayload := map[string]any{
					"type":          "response.shell_call_command.delta",
					"command_index": commandIndex,
					"delta":         command,
					"output_index":  outputIndex,
				}
				if includeObfuscation {
					deltaPayload["obfuscation"] = strings.Repeat("x", utf8.RuneCountInString(command))
				}
				if err := visit(hostedToolReplayEventSpec{
					eventType: "response.shell_call_command.delta",
					payload:   deltaPayload,
				}); err != nil {
					return err
				}
			}
			if err := visit(hostedToolReplayEventSpec{
				eventType: "response.shell_call_command.done",
				payload: map[string]any{
					"type":          "response.shell_call_command.done",
					"command":       command,
					"command_index": commandIndex,
					"output_index":  outputIndex,
				},
			}); err != nil {
				return err
			}
		}
	case localBuiltinApplyPatchCallType:
		if !profile.includeApplyPatchToolFamily {
			return nil
		}
		operation, _ := item["operation"].(map[string]any)
		diff := asString(operation["diff"])
		if diff != "" {
			deltaPayload := map[string]any{
				"type":         "response.apply_patch_call_operation_diff.delta",
				"item_id":      itemID,
				"output_index": outputIndex,
				"delta":        diff,
			}
			if includeObfuscation {
				deltaPayload["obfuscation"] = strings.Repeat("x", utf8.RuneCountInString(diff))
			}
			if err := visit(hostedToolReplayEventSpec{
				eventType: "response.apply_patch_call_operation_diff.delta",
				payload:   deltaPayload,
			}); err != nil {
				return err
			}
		}
		if err := visit(hostedToolReplayEventSpec{
			eventType: "response.apply_patch_call_operation_diff.done",
			payload: map[string]any{
				"type":         "response.apply_patch_call_operation_diff.done",
				"item_id":      itemID,
				"output_index": outputIndex,
				"diff":         diff,
			},
		}); err != nil {
			return err
		}
	}
	return nil
}
