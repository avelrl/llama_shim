package modelcert

import (
	"fmt"
	"path/filepath"
	"strings"
)

func RenderSummaryMarkdown(summary Summary) string {
	var b strings.Builder
	b.WriteString("# V4 Model Certification Report\n\n")
	b.WriteString("- Status: `" + summary.Status + "`\n")
	b.WriteString("- Run ID: `" + summary.RunID + "`\n")
	b.WriteString("- Generated: `" + summary.GeneratedAt + "`\n")
	b.WriteString("- Artifacts: `" + filepath.ToSlash(summary.OutDir) + "`\n\n")
	b.WriteString("| Model | Verdict | Tester | Codex | Owner | Artifacts |\n")
	b.WriteString("| --- | --- | --- | --- | --- | --- |\n")
	for _, model := range summary.Models {
		b.WriteString(fmt.Sprintf("| `%s` | `%s` | `%s` | `%s` | `%s` | `%s` |\n",
			model.Model,
			model.Verdict,
			model.Tester.Status,
			model.Codex.Status,
			defaultString(model.PossibleOwner, "-"),
			filepath.ToSlash(model.ArtifactDir),
		))
	}
	if len(summary.FixCandidates) > 0 {
		b.WriteString("\n## Fix Candidates\n\n")
		b.WriteString(RenderFixCandidatesMarkdown(summary.FixCandidates))
	}
	return b.String()
}

func RenderModelMarkdown(model ModelSummary) string {
	var b strings.Builder
	b.WriteString("# Model Certification\n\n")
	b.WriteString("- Model: `" + model.Model + "`\n")
	b.WriteString("- Verdict: `" + model.Verdict + "`\n")
	b.WriteString("- Provider: `" + model.ProviderID + "`\n")
	b.WriteString("- Provider model: `" + model.ProviderModel + "`\n")
	b.WriteString("- Upstream model: `" + model.UpstreamModel + "`\n")
	b.WriteString("- Shim base URL: `" + model.ShimBaseURL + "`\n")
	b.WriteString("- Artifact dir: `" + filepath.ToSlash(model.ArtifactDir) + "`\n")
	if model.PossibleOwner != "" {
		b.WriteString("- Possible owner: `" + model.PossibleOwner + "`\n")
	}
	b.WriteString("\n## Stages\n\n")
	b.WriteString(fmt.Sprintf("- Health: `%d`\n", model.HealthStatus))
	b.WriteString(fmt.Sprintf("- Readyz: `%d`\n", model.ReadyzStatus))
	b.WriteString(fmt.Sprintf("- Capabilities: `%d`\n", model.CapabilitiesStatus))
	b.WriteString(fmt.Sprintf("- Tester: `%s`", model.Tester.Status))
	if model.Tester.ExitCode != 0 {
		b.WriteString(fmt.Sprintf(" exit `%d`", model.Tester.ExitCode))
	}
	if model.Tester.Error != "" {
		b.WriteString(" - " + Redact(model.Tester.Error))
	}
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("- Codex: `%s`", model.Codex.Status))
	if model.Codex.ExitCode != 0 {
		b.WriteString(fmt.Sprintf(" exit `%d`", model.Codex.ExitCode))
	}
	if model.Codex.Error != "" {
		b.WriteString(" - " + Redact(model.Codex.Error))
	}
	b.WriteString("\n\n")
	if len(model.Signals) > 0 {
		b.WriteString("## Signals\n\n")
		for _, signal := range model.Signals {
			b.WriteString("- `" + signal + "`\n")
		}
	}
	return b.String()
}

func RenderFixCandidatesMarkdown(candidates []FixCandidate) string {
	var b strings.Builder
	if len(candidates) == 0 {
		b.WriteString("No fix candidates.\n")
		return b.String()
	}
	b.WriteString("| Model | Stage | Owner | Category | Confidence | Description |\n")
	b.WriteString("| --- | --- | --- | --- | --- | --- |\n")
	for _, candidate := range candidates {
		b.WriteString(fmt.Sprintf("| `%s` | `%s` | `%s` | `%s` | `%s` | %s |\n",
			candidate.Model,
			candidate.Stage,
			candidate.Owner,
			candidate.Category,
			candidate.Confidence,
			escapeMarkdownTable(candidate.Description),
		))
	}
	return b.String()
}

func escapeMarkdownTable(value string) string {
	value = strings.ReplaceAll(value, "|", "\\|")
	value = strings.ReplaceAll(value, "\n", " ")
	return value
}
