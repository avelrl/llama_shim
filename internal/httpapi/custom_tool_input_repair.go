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
	if next, ok := repairApplyPatchUnifiedDiffHunkHeaders(repaired); ok {
		repaired = next
		changed = true
	}
	if next, ok := repairApplyPatchUnprefixedWholeFileHunks(repaired); ok {
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

func repairApplyPatchUnifiedDiffHunkHeaders(input string) (string, bool) {
	lines := strings.Split(input, "\n")
	changed := false
	for index, line := range lines {
		rewritten, ok := rewriteUnifiedDiffHunkHeader(line)
		if !ok {
			continue
		}
		lines[index] = rewritten
		changed = true
	}
	if !changed {
		return "", false
	}
	return strings.Join(lines, "\n"), true
}

func rewriteUnifiedDiffHunkHeader(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "@@ ") {
		return "", false
	}
	fields := strings.Fields(strings.TrimPrefix(trimmed, "@@ "))
	if len(fields) < 3 || fields[2] != "@@" {
		return "", false
	}
	if !isUnifiedDiffRange(fields[0], '-') || !isUnifiedDiffRange(fields[1], '+') {
		return "", false
	}
	if len(fields) == 3 {
		return "@@", true
	}
	return "@@ " + strings.Join(fields[3:], " "), true
}

func isUnifiedDiffRange(value string, prefix byte) bool {
	if len(value) < 2 || value[0] != prefix {
		return false
	}
	seenComma := false
	for index := 1; index < len(value); index++ {
		ch := value[index]
		switch {
		case ch >= '0' && ch <= '9':
			continue
		case ch == ',' && !seenComma && index > 1 && index < len(value)-1:
			seenComma = true
		default:
			return false
		}
	}
	return true
}

func repairApplyPatchUnprefixedWholeFileHunks(input string) (string, bool) {
	lines := strings.Split(input, "\n")
	changed := false

	for index := 0; index < len(lines); index++ {
		line := lines[index]
		addFile := strings.HasPrefix(line, "*** Add File: ")
		updateFile := strings.HasPrefix(line, "*** Update File: ")
		if !addFile && !updateFile {
			continue
		}

		bodyStart := index + 1
		bodyEnd := bodyStart
		hasPatchSyntax := false
		for bodyEnd < len(lines) && !strings.HasPrefix(lines[bodyEnd], "*** ") {
			if strings.HasPrefix(lines[bodyEnd], "@@") {
				hasPatchSyntax = true
			}
			bodyEnd++
		}
		if bodyEnd == bodyStart || hasPatchSyntax {
			continue
		}
		if applyPatchWholeFileBodyHasAddPrefixes(lines[bodyStart:bodyEnd]) {
			continue
		}

		if updateFile {
			lines[index] = strings.Replace(line, "*** Update File: ", "*** Add File: ", 1)
		}
		for bodyIndex := bodyStart; bodyIndex < bodyEnd; bodyIndex++ {
			lines[bodyIndex] = "+" + lines[bodyIndex]
		}
		changed = true
		index = bodyEnd - 1
	}

	if !changed {
		return "", false
	}
	return strings.Join(lines, "\n"), true
}

func applyPatchWholeFileBodyHasAddPrefixes(lines []string) bool {
	for _, line := range lines {
		if strings.HasPrefix(line, "+") {
			return true
		}
	}
	return false
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
