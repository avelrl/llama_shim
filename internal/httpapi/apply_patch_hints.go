package httpapi

import "strings"

const applyPatchRawInputFormatHint = "For apply_patch raw input, return a real apply_patch patch: start with `*** Begin Patch` and end with `*** End Patch`; for existing-file edits, use `*** Update File: path` followed by one or more `@@` hunks with removed lines prefixed by `-`, added lines prefixed by `+`, and unchanged context lines prefixed by a single space. Do not use unified-diff range headers such as `@@ -1,4 +1,4 @@`. Do not place a whole replacement file directly after `*** Update File`; do not include merge-conflict markers or markdown fences."

func applyPatchRawInputHintForDescriptor(descriptor customToolDescriptor) string {
	if !strings.EqualFold(strings.TrimSpace(descriptor.Name), "apply_patch") {
		return ""
	}
	return applyPatchRawInputFormatHint
}
