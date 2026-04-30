package httpapi

import "strings"

const (
	applyPatchBeginMarker = "*** Begin Patch"
	applyPatchEndMarker   = "*** End Patch"
)

func repairConstrainedCustomToolInput(descriptor customToolDescriptor, input string) (string, bool) {
	if !strings.EqualFold(strings.TrimSpace(descriptor.Name), localBuiltinApplyPatchToolType) {
		return "", false
	}
	repaired, changedEnvelope := repairApplyPatchRepeatedEnvelopes(input)
	if !changedEnvelope {
		repaired = input
	}
	changed := changedEnvelope
	if next, ok := repairApplyPatchEmptyHunkHeaders(repaired); ok {
		repaired = next
		changed = true
	}
	if next, ok := repairApplyPatchUnprefixedContextLines(repaired); ok {
		repaired = next
		changed = true
	}
	return repaired, changed
}

func repairConstrainedCustomToolCallPayload(item map[string]any, bridge customToolBridge) (map[string]any, bool) {
	if strings.TrimSpace(asString(item["type"])) != "custom_tool_call" {
		return nil, false
	}
	descriptor, ok := bridge.ByCanonicalIdentity(asString(item["name"]), asString(item["namespace"]))
	if !ok || descriptor.Constraint == nil {
		return nil, false
	}
	repaired, ok := repairConstrainedCustomToolInput(descriptor, extractCustomToolInput(item["input"]))
	if !ok {
		return nil, false
	}
	rewritten := cloneAnyMap(item)
	rewritten["input"] = repaired
	return rewritten, true
}

func repairApplyPatchRepeatedEnvelopes(input string) (string, bool) {
	trimmed := strings.TrimSpace(input)
	if strings.Count(trimmed, applyPatchBeginMarker) < 2 {
		return "", false
	}

	lines := strings.Split(trimmed, "\n")
	body := make([]string, 0, len(lines))
	inEnvelope := false
	envelopes := 0

	for _, line := range lines {
		switch line {
		case applyPatchBeginMarker:
			if inEnvelope {
				return "", false
			}
			inEnvelope = true
			envelopes++
		case applyPatchEndMarker:
			if !inEnvelope {
				return "", false
			}
			inEnvelope = false
		default:
			if !inEnvelope {
				if strings.TrimSpace(line) == "" {
					continue
				}
				return "", false
			}
			body = append(body, line)
		}
	}
	if inEnvelope || envelopes < 2 || len(body) == 0 {
		return "", false
	}

	return applyPatchBeginMarker + "\n" + strings.Join(body, "\n") + "\n" + applyPatchEndMarker + "\n", true
}

func repairApplyPatchEmptyHunkHeaders(input string) (string, bool) {
	lines := strings.Split(input, "\n")
	changed := false
	for index, line := range lines {
		if line == "@@ " {
			lines[index] = "@@"
			changed = true
		}
	}
	if !changed {
		return "", false
	}
	return strings.Join(lines, "\n"), true
}

func repairApplyPatchUnprefixedContextLines(input string) (string, bool) {
	lines := strings.Split(input, "\n")
	changed := false
	inUpdateHunk := false
	for index, line := range lines {
		switch {
		case strings.HasPrefix(line, "*** "):
			inUpdateHunk = false
			continue
		case strings.HasPrefix(line, "@@"):
			inUpdateHunk = true
			continue
		case !inUpdateHunk:
			continue
		case line == "":
			lines[index] = " "
			changed = true
			continue
		}

		switch line[0] {
		case '+', '-', ' ':
			continue
		default:
			lines[index] = " " + line
			changed = true
		}
	}
	if !changed {
		return "", false
	}
	return strings.Join(lines, "\n"), true
}
