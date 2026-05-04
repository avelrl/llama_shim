package codexeval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	AutoStrictNone     = "none"
	AutoStrictBaseline = "baseline"
	AutoStrictAll      = "all"
)

type AutoReport struct {
	GeneratedAt string              `json:"generated_at"`
	Status      string              `json:"status"`
	Profiles    []AutoProfileReport `json:"profiles"`
	Counts      map[string]int      `json:"counts"`
}

type AutoProfileReport struct {
	Name           string            `json:"name"`
	Source         string            `json:"source"`
	ComparePath    string            `json:"compare_path,omitempty"`
	SummaryPath    string            `json:"summary_path,omitempty"`
	ShimLogPath    string            `json:"shim_log_path,omitempty"`
	ShimDiagPath   string            `json:"shim_diagnostics_path,omitempty"`
	Status         string            `json:"status"`
	StrictClean    bool              `json:"strict_clean"`
	RetryDependent int               `json:"retry_dependent"`
	Counts         map[string]int    `json:"counts"`
	Control        CompareRunRef     `json:"control"`
	Candidates     []CompareRunRef   `json:"candidates"`
	RetryTasks     []AutoTaskFinding `json:"retry_tasks,omitempty"`
	FailedTasks    []AutoTaskFinding `json:"failed_tasks,omitempty"`
	Coverage       []AutoTaskFinding `json:"coverage,omitempty"`
}

type AutoTaskFinding struct {
	Profile   string `json:"profile,omitempty"`
	Candidate string `json:"candidate,omitempty"`
	Task      string `json:"task"`
	Diagnosis string `json:"diagnosis"`
	Status    string `json:"status,omitempty"`
	Bucket    string `json:"bucket,omitempty"`
	Attempts  int    `json:"attempts,omitempty"`
}

type autoCoverageCount struct {
	Profile   string
	Candidate string
	Count     int
}

func BuildAutoReport(paths []string) (AutoReport, error) {
	if len(paths) == 0 {
		return AutoReport{}, fmt.Errorf("at least one loop directory or compare summary path is required")
	}
	report := AutoReport{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Status:      StatusPassed,
		Counts:      map[string]int{},
	}
	for _, path := range paths {
		profile, err := loadAutoProfile(path)
		if err != nil {
			return AutoReport{}, err
		}
		report.Profiles = append(report.Profiles, profile)
		for diagnosis, count := range profile.Counts {
			report.Counts[diagnosis] += count
		}
		if profile.Status != StatusPassed {
			report.Status = "failed"
		}
	}
	sort.SliceStable(report.Profiles, func(i, j int) bool {
		return autoProfileSortKey(report.Profiles[i].Name) < autoProfileSortKey(report.Profiles[j].Name)
	})
	return report, nil
}

func RenderAutoReportMarkdown(report AutoReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Codex Eval Auto Report\n\n")
	fmt.Fprintf(&b, "- Generated: `%s`\n", report.GeneratedAt)
	fmt.Fprintf(&b, "- Status: `%s`\n", report.Status)
	fmt.Fprintf(&b, "- Profiles: `%d`\n\n", len(report.Profiles))

	fmt.Fprintf(&b, "## Overview\n\n")
	fmt.Fprintf(&b, "| Profile | Status | Control | Candidates | Diagnoses | Notes |\n")
	fmt.Fprintf(&b, "| --- | --- | --- | --- | --- | --- |\n")
	for _, profile := range report.Profiles {
		fmt.Fprintf(
			&b,
			"| `%s` | `%s` | %s | %s | %s | %s |\n",
			markdownCell(profile.Name),
			markdownCell(profile.Status),
			autoRunRefCell(profile.Control),
			autoCandidateRefsCell(profile.Candidates),
			diagnosisCountsText(profile.Counts),
			markdownCell(autoProfileNotes(profile)),
		)
	}

	fmt.Fprintf(&b, "\n## Artifacts\n\n")
	fmt.Fprintf(&b, "| Profile | Loop | Compare | JSON | Shim log | Shim diagnostics |\n")
	fmt.Fprintf(&b, "| --- | --- | --- | --- | --- | --- |\n")
	for _, profile := range report.Profiles {
		fmt.Fprintf(
			&b,
			"| `%s` | `%s` | `%s` | `%s` | `%s` | `%s` |\n",
			markdownCell(profile.Name),
			markdownCell(profile.Source),
			markdownCell(profile.ComparePath),
			markdownCell(profile.SummaryPath),
			markdownCell(profile.ShimLogPath),
			markdownCell(profile.ShimDiagPath),
		)
	}

	if findings := autoFailedFindings(report); len(findings) > 0 {
		fmt.Fprintf(&b, "\n## Failed Tasks\n\n")
		renderAutoFindingsTable(&b, findings)
	}
	if findings := autoRetryFindings(report); len(findings) > 0 {
		fmt.Fprintf(&b, "\n## Retry-Dependent Tasks\n\n")
		renderAutoFindingsTable(&b, findings)
	}
	if findings := autoCoverageFindings(report); len(findings) > 0 {
		fmt.Fprintf(&b, "\n## Coverage Differences\n\n")
		fmt.Fprintf(&b, "Coverage differences are expected when control and candidate suites are intentionally different. Use each profile's `compare.md` for the task-level list.\n\n")
		fmt.Fprintf(&b, "| Profile | Candidate | Count |\n")
		fmt.Fprintf(&b, "| --- | --- | ---: |\n")
		for _, count := range autoCoverageCounts(findings) {
			fmt.Fprintf(&b, "| `%s` | `%s` | %d |\n", markdownCell(count.Profile), markdownCell(count.Candidate), count.Count)
		}
	}
	return b.String()
}

func AutoReportShouldFail(report AutoReport, strict string) bool {
	switch normalizeAutoStrict(strict) {
	case AutoStrictAll:
		if report.Status != StatusPassed {
			return true
		}
		for _, profile := range report.Profiles {
			if profile.Status != StatusPassed {
				return true
			}
		}
		return false
	case AutoStrictBaseline:
		for _, profile := range report.Profiles {
			if profile.Name == "baseline" {
				return profile.Status != StatusPassed
			}
		}
		return report.Status != StatusPassed
	default:
		return false
	}
}

func normalizeAutoStrict(strict string) string {
	switch strings.ToLower(strings.TrimSpace(strict)) {
	case "", "0", "false", "no", "off", AutoStrictNone:
		return AutoStrictNone
	case "1", "true", "yes", "on", AutoStrictAll:
		return AutoStrictAll
	case AutoStrictBaseline:
		return AutoStrictBaseline
	default:
		return AutoStrictBaseline
	}
}

func loadAutoProfile(path string) (AutoProfileReport, error) {
	reportPath, source, err := autoReportPath(path)
	if err != nil {
		return AutoProfileReport{}, err
	}
	raw, err := os.ReadFile(reportPath)
	if err != nil {
		return AutoProfileReport{}, fmt.Errorf("read %s: %w", reportPath, err)
	}
	var compare CompareReport
	if err := json.Unmarshal(raw, &compare); err != nil {
		return AutoProfileReport{}, fmt.Errorf("parse %s: %w", reportPath, err)
	}
	profile := AutoProfileReport{
		Name:           autoProfileName(source),
		Source:         source,
		ComparePath:    filepath.Join(source, "compare.md"),
		SummaryPath:    reportPath,
		ShimLogPath:    autoOptionalPath(source, "shim.log.slice"),
		ShimDiagPath:   autoOptionalPath(source, "shim-log-diagnostics.md"),
		Status:         StatusPassed,
		StrictClean:    true,
		RetryDependent: compare.Counts[DiagnosisRetryDependent],
		Counts:         compare.Counts,
		Control:        compare.Control,
	}
	if compare.Control.Passed != compare.Control.Total {
		profile.Status = "failed"
		profile.StrictClean = false
	}
	for _, candidate := range compare.Candidates {
		profile.Candidates = append(profile.Candidates, candidate.Run)
		if candidate.Run.Passed != candidate.Run.Total {
			profile.Status = "failed"
			profile.StrictClean = false
		}
		for _, task := range candidate.Tasks {
			finding := AutoTaskFinding{
				Profile:   profile.Name,
				Candidate: candidate.Run.RunID,
				Task:      task.ID,
				Diagnosis: task.Diagnosis,
				Status:    task.CandidateStatus,
				Bucket:    task.CandidateBucket,
				Attempts:  task.CandidateAttempts,
			}
			switch task.Diagnosis {
			case DiagnosisOK:
			case DiagnosisRetryDependent:
				profile.StrictClean = false
				profile.RetryTasks = append(profile.RetryTasks, finding)
			default:
				profile.Status = "failed"
				profile.StrictClean = false
				profile.FailedTasks = append(profile.FailedTasks, finding)
			}
		}
		for _, task := range candidate.Coverage {
			finding := AutoTaskFinding{
				Profile:   profile.Name,
				Candidate: candidate.Run.RunID,
				Task:      task.ID,
				Diagnosis: task.Diagnosis,
				Status:    task.CandidateStatus,
				Bucket:    task.CandidateBucket,
				Attempts:  task.CandidateAttempts,
			}
			switch task.Diagnosis {
			case DiagnosisOK, DiagnosisRetryDependent, DiagnosisCandidateOnlyTask, DiagnosisCandidateMissingTask:
				profile.Coverage = append(profile.Coverage, finding)
			default:
				profile.Status = "failed"
				profile.StrictClean = false
				profile.FailedTasks = append(profile.FailedTasks, finding)
			}
			if task.Diagnosis == DiagnosisRetryDependent || (task.CandidateStatus == StatusPassed && task.CandidateAttempts > 1) {
				profile.StrictClean = false
				finding.Diagnosis = DiagnosisRetryDependent
				profile.RetryTasks = append(profile.RetryTasks, finding)
			}
		}
	}
	if profile.RetryDependent > 0 {
		profile.StrictClean = false
	}
	return profile, nil
}

func autoReportPath(path string) (reportPath, source string, err error) {
	clean := filepath.Clean(path)
	info, statErr := os.Stat(clean)
	if statErr != nil {
		return "", "", fmt.Errorf("stat %s: %w", clean, statErr)
	}
	if info.IsDir() {
		return filepath.Join(clean, "summary.json"), clean, nil
	}
	return clean, filepath.Dir(clean), nil
}

func autoProfileName(source string) string {
	name := filepath.Base(filepath.Clean(source))
	if strings.TrimSpace(name) == "" || name == "." {
		return "profile"
	}
	return name
}

func autoOptionalPath(source, name string) string {
	path := filepath.Join(source, name)
	if _, err := os.Stat(path); err == nil {
		return path
	}
	return ""
}

func autoProfileSortKey(name string) string {
	switch name {
	case "baseline":
		return "00-" + name
	case "expanded":
		return "01-" + name
	case "bench-lite":
		return "02-" + name
	default:
		return "99-" + name
	}
}

func autoProfileNotes(profile AutoProfileReport) string {
	var notes []string
	if profile.StrictClean {
		notes = append(notes, "strict-clean")
	}
	if profile.RetryDependent > 0 {
		notes = append(notes, fmt.Sprintf("retry-dependent: %d", profile.RetryDependent))
	}
	if len(profile.FailedTasks) > 0 {
		notes = append(notes, fmt.Sprintf("failed tasks: %d", len(profile.FailedTasks)))
	}
	if len(notes) == 0 {
		return "green"
	}
	return strings.Join(notes, "; ")
}

func autoRunRefCell(run CompareRunRef) string {
	return fmt.Sprintf("`%s` %d/%d", markdownCell(run.Suite), run.Passed, run.Total)
}

func autoCandidateRefsCell(candidates []CompareRunRef) string {
	if len(candidates) == 0 {
		return "none"
	}
	var parts []string
	for _, candidate := range candidates {
		parts = append(parts, fmt.Sprintf("`%s` %d/%d", markdownCell(candidate.Model), candidate.Passed, candidate.Total))
	}
	return strings.Join(parts, "<br>")
}

func autoFailedFindings(report AutoReport) []AutoTaskFinding {
	var findings []AutoTaskFinding
	for _, profile := range report.Profiles {
		findings = append(findings, profile.FailedTasks...)
	}
	return findings
}

func autoRetryFindings(report AutoReport) []AutoTaskFinding {
	var findings []AutoTaskFinding
	for _, profile := range report.Profiles {
		findings = append(findings, profile.RetryTasks...)
	}
	return findings
}

func autoCoverageFindings(report AutoReport) []AutoTaskFinding {
	var findings []AutoTaskFinding
	for _, profile := range report.Profiles {
		findings = append(findings, profile.Coverage...)
	}
	return findings
}

func autoCoverageCounts(findings []AutoTaskFinding) []autoCoverageCount {
	counts := map[string]autoCoverageCount{}
	for _, finding := range findings {
		key := finding.Profile + "\x00" + finding.Candidate
		count := counts[key]
		count.Profile = finding.Profile
		count.Candidate = finding.Candidate
		count.Count++
		counts[key] = count
	}
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]autoCoverageCount, 0, len(keys))
	for _, key := range keys {
		result = append(result, counts[key])
	}
	return result
}

func renderAutoFindingsTable(b *strings.Builder, findings []AutoTaskFinding) {
	fmt.Fprintf(b, "| Profile | Candidate | Task | Diagnosis | Status | Bucket | Attempts |\n")
	fmt.Fprintf(b, "| --- | --- | --- | --- | --- | --- | ---: |\n")
	for _, finding := range findings {
		fmt.Fprintf(
			b,
			"| `%s` | `%s` | `%s` | `%s` | `%s` | `%s` | %d |\n",
			markdownCell(finding.Profile),
			markdownCell(finding.Candidate),
			markdownCell(finding.Task),
			markdownCell(finding.Diagnosis),
			markdownCell(finding.Status),
			markdownCell(finding.Bucket),
			finding.Attempts,
		)
	}
}
