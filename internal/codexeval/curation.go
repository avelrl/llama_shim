package codexeval

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	CurationKindAuto    = "auto"
	CurationKindCompare = "compare"
	CurationKindRun     = "run"

	CurationStatusOK        = "ok"
	CurationStatusAttention = "attention"
	CurationStatusNoData    = "no_data"
)

type CurationOptions struct {
	Paths        []string
	Limit        int
	Model        string
	Since        string
	ModelAliases map[string]string
}

type CurationReport struct {
	GeneratedAt     string                  `json:"generated_at"`
	Status          string                  `json:"status"`
	Inputs          []string                `json:"inputs"`
	Filters         CurationFilters         `json:"filters"`
	ProfileResults  []CurationProfileResult `json:"profile_results"`
	ModelSummaries  []CurationModelSummary  `json:"model_summaries"`
	TaskTrends      []CurationTaskTrend     `json:"task_trends"`
	DiagnosisCounts map[string]int          `json:"diagnosis_counts"`
	Recommendations []string                `json:"recommendations"`
	LoadWarnings    []string                `json:"load_warnings,omitempty"`
}

type CurationFilters struct {
	Limit        int               `json:"limit,omitempty"`
	Model        string            `json:"model,omitempty"`
	Since        string            `json:"since,omitempty"`
	ModelAliases map[string]string `json:"model_aliases,omitempty"`
}

type CurationProfileResult struct {
	Kind           string            `json:"kind"`
	Source         string            `json:"source"`
	Profile        string            `json:"profile"`
	RunID          string            `json:"run_id"`
	Model          string            `json:"model"`
	PublicModel    string            `json:"public_model,omitempty"`
	CanonicalModel string            `json:"canonical_model,omitempty"`
	Suite          string            `json:"suite"`
	StartedAt      string            `json:"started_at,omitempty"`
	Status         string            `json:"status"`
	StrictClean    bool              `json:"strict_clean"`
	Passed         int               `json:"passed"`
	Total          int               `json:"total"`
	RetryDependent int               `json:"retry_dependent"`
	FailedTasks    []AutoTaskFinding `json:"failed_tasks,omitempty"`
	RetryTasks     []AutoTaskFinding `json:"retry_tasks,omitempty"`
	Counts         map[string]int    `json:"counts"`
	Interpretation string            `json:"interpretation"`
	MatrixEligible bool              `json:"matrix_eligible"`
	Notes          []string          `json:"notes,omitempty"`
}

type CurationModelSummary struct {
	Model                string         `json:"model"`
	PublicModel          string         `json:"public_model,omitempty"`
	CanonicalModel       string         `json:"canonical_model,omitempty"`
	RawModels            []string       `json:"raw_models,omitempty"`
	Profile              string         `json:"profile"`
	Runs                 int            `json:"runs"`
	StrictCleanRuns      int            `json:"strict_clean_runs"`
	RetryPassedRuns      int            `json:"retry_passed_runs"`
	FailedRuns           int            `json:"failed_runs"`
	LatestSource         string         `json:"latest_source,omitempty"`
	LatestRunID          string         `json:"latest_run_id,omitempty"`
	LatestStartedAt      string         `json:"latest_started_at,omitempty"`
	LatestStatus         string         `json:"latest_status,omitempty"`
	LatestInterpretation string         `json:"latest_interpretation,omitempty"`
	Passed               int            `json:"passed"`
	Total                int            `json:"total"`
	DiagnosisCounts      map[string]int `json:"diagnosis_counts,omitempty"`
	UnstableTasks        []string       `json:"unstable_tasks,omitempty"`
}

type CurationTaskTrend struct {
	Model          string         `json:"model"`
	PublicModel    string         `json:"public_model,omitempty"`
	CanonicalModel string         `json:"canonical_model,omitempty"`
	RawModels      []string       `json:"raw_models,omitempty"`
	Profile        string         `json:"profile"`
	Task           string         `json:"task"`
	Failed         int            `json:"failed"`
	RetryDependent int            `json:"retry_dependent"`
	Diagnoses      map[string]int `json:"diagnoses,omitempty"`
	LastStatus     string         `json:"last_status,omitempty"`
	LastDiagnosis  string         `json:"last_diagnosis,omitempty"`
	LastSource     string         `json:"last_source,omitempty"`
	LastStartedAt  string         `json:"last_started_at,omitempty"`
}

type curationLoaded struct {
	kind    string
	source  string
	loaded  string
	when    string
	auto    *AutoReport
	compare *CompareReport
	summary *Summary
}

type curationJSONKind struct {
	Profiles    json.RawMessage `json:"profiles"`
	Control     json.RawMessage `json:"control"`
	Candidates  json.RawMessage `json:"candidates"`
	Tasks       json.RawMessage `json:"tasks"`
	Environment json.RawMessage `json:"environment"`
}

func BuildCurationReport(options CurationOptions) (CurationReport, error) {
	since, err := parseCurationSince(options.Since)
	if err != nil {
		return CurationReport{}, err
	}
	var loaded []curationLoaded
	var warnings []string
	if len(options.Paths) > 0 {
		loaded, warnings, err = discoverCurationInputs(options.Paths)
		if err != nil {
			return CurationReport{}, err
		}
	}
	results := expandCurationInputs(loaded)
	modelAliases := normalizeCurationModelAliases(options.ModelAliases)
	results = applyCurationModelAliases(results, modelAliases)
	results = filterCurationResults(results, options.Model, since)
	sort.SliceStable(results, func(i, j int) bool {
		return curationSortTime(results[i]) > curationSortTime(results[j])
	})
	if options.Limit > 0 && len(results) > options.Limit {
		results = results[:options.Limit]
	}
	for i := range results {
		results[i].Interpretation, results[i].MatrixEligible, results[i].Notes = curationInterpretation(results[i])
	}
	modelSummaries := buildCurationModelSummaries(results)
	taskTrends := buildCurationTaskTrends(results)
	report := CurationReport{
		GeneratedAt:     time.Now().UTC().Format(time.RFC3339),
		Status:          CurationStatusOK,
		Inputs:          append([]string(nil), options.Paths...),
		Filters:         CurationFilters{Limit: options.Limit, Model: strings.TrimSpace(options.Model), Since: strings.TrimSpace(options.Since), ModelAliases: modelAliases},
		ProfileResults:  results,
		ModelSummaries:  modelSummaries,
		TaskTrends:      taskTrends,
		DiagnosisCounts: curationDiagnosisCounts(results),
		LoadWarnings:    warnings,
	}
	report.Recommendations = buildCurationRecommendations(report)
	if len(results) == 0 {
		report.Status = CurationStatusNoData
	} else if curationNeedsAttention(results) {
		report.Status = CurationStatusAttention
	}
	return report, nil
}

func RenderCurationReportMarkdown(report CurationReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Codex Eval Curation Report\n\n")
	fmt.Fprintf(&b, "- Generated: `%s`\n", report.GeneratedAt)
	fmt.Fprintf(&b, "- Status: `%s`\n", report.Status)
	fmt.Fprintf(&b, "- Profile results: `%d`\n", len(report.ProfileResults))
	if report.Filters.Model != "" {
		fmt.Fprintf(&b, "- Model filter: `%s`\n", markdownCell(report.Filters.Model))
	}
	if report.Filters.Since != "" {
		fmt.Fprintf(&b, "- Since: `%s`\n", markdownCell(report.Filters.Since))
	}
	if report.Filters.Limit > 0 {
		fmt.Fprintf(&b, "- Limit: `%d`\n", report.Filters.Limit)
	}
	fmt.Fprintf(&b, "\n")

	if len(report.Recommendations) > 0 {
		fmt.Fprintf(&b, "## Interpretation\n\n")
		for _, recommendation := range report.Recommendations {
			fmt.Fprintf(&b, "- %s\n", recommendation)
		}
		fmt.Fprintf(&b, "\n")
	}

	fmt.Fprintf(&b, "## Latest Profile Results\n\n")
	if len(report.ProfileResults) == 0 {
		fmt.Fprintf(&b, "No eval artifacts matched the selected inputs.\n\n")
	} else {
		fmt.Fprintf(&b, "| When | Kind | Profile | Model | Suite | Result | Retry | Interpretation | Source |\n")
		fmt.Fprintf(&b, "| --- | --- | --- | --- | --- | ---: | ---: | --- | --- |\n")
		for _, result := range report.ProfileResults {
			fmt.Fprintf(
				&b,
				"| `%s` | `%s` | `%s` | `%s` | `%s` | %d/%d | %d | `%s` | `%s` |\n",
				markdownCell(curationShortTime(result.StartedAt)),
				markdownCell(result.Kind),
				markdownCell(result.Profile),
				markdownCell(curationDisplayModel(result)),
				markdownCell(result.Suite),
				result.Passed,
				result.Total,
				result.RetryDependent,
				markdownCell(result.Interpretation),
				markdownCell(result.Source),
			)
		}
		fmt.Fprintf(&b, "\n")
	}

	if len(report.ModelSummaries) > 0 {
		fmt.Fprintf(&b, "## Model And Profile Trends\n\n")
		fmt.Fprintf(&b, "| Model | Profile | Runs | Strict | Retry-pass | Failed | Latest | Diagnoses | Unstable tasks |\n")
		fmt.Fprintf(&b, "| --- | --- | ---: | ---: | ---: | ---: | --- | --- | --- |\n")
		for _, summary := range report.ModelSummaries {
			fmt.Fprintf(
				&b,
				"| `%s` | `%s` | %d | %d | %d | %d | `%s` | %s | %s |\n",
				markdownCell(summary.Model),
				markdownCell(summary.Profile),
				summary.Runs,
				summary.StrictCleanRuns,
				summary.RetryPassedRuns,
				summary.FailedRuns,
				markdownCell(summary.LatestInterpretation),
				diagnosisCountsText(summary.DiagnosisCounts),
				markdownListCell(summary.UnstableTasks),
			)
		}
		fmt.Fprintf(&b, "\n")
	}

	if len(report.TaskTrends) > 0 {
		fmt.Fprintf(&b, "## Failed Or Retry-Dependent Tasks\n\n")
		fmt.Fprintf(&b, "| Model | Profile | Task | Failed | Retry | Last | Diagnoses |\n")
		fmt.Fprintf(&b, "| --- | --- | --- | ---: | ---: | --- | --- |\n")
		for _, trend := range report.TaskTrends {
			fmt.Fprintf(
				&b,
				"| `%s` | `%s` | `%s` | %d | %d | `%s` | %s |\n",
				markdownCell(trend.Model),
				markdownCell(trend.Profile),
				markdownCell(trend.Task),
				trend.Failed,
				trend.RetryDependent,
				markdownCell(trend.LastDiagnosis),
				diagnosisCountsText(trend.Diagnoses),
			)
		}
		fmt.Fprintf(&b, "\n")
	}

	if len(report.LoadWarnings) > 0 {
		fmt.Fprintf(&b, "## Load Warnings\n\n")
		for _, warning := range report.LoadWarnings {
			fmt.Fprintf(&b, "- `%s`\n", markdownCell(warning))
		}
		fmt.Fprintf(&b, "\n")
	}

	fmt.Fprintf(&b, "## Matrix Transfer Notes\n\n")
	fmt.Fprintf(&b, "- Generated files own counts; the model matrix owns interpretation.\n")
	fmt.Fprintf(&b, "- `public_model` and `canonical_model` fields are shim-owned curation metadata used to align raw upstream model ids with configured provider/model aliases.\n")
	fmt.Fprintf(&b, "- Promote `baseline` only when the latest baseline is strict-clean, or explicitly note retry-dependent tasks.\n")
	fmt.Fprintf(&b, "- Treat `expanded` and `bench-lite` as diagnostic/stability evidence unless the matrix scope is deliberately changed.\n")
	fmt.Fprintf(&b, "- Inspect shim log diagnostics before classifying transport or raw-tool failures as model behavior.\n")
	return b.String()
}

func discoverCurationInputs(paths []string) ([]curationLoaded, []string, error) {
	var loaded []curationLoaded
	var warnings []string
	seen := map[string]bool{}
	for _, input := range paths {
		clean := filepath.Clean(input)
		info, err := os.Stat(clean)
		if err != nil {
			return nil, nil, fmt.Errorf("stat %s: %w", clean, err)
		}
		if !info.IsDir() {
			item, err := loadCurationSummaryFile(clean)
			if err != nil {
				return nil, nil, err
			}
			if !seen[item.loaded] {
				loaded = append(loaded, item)
				seen[item.loaded] = true
			}
			continue
		}
		err = filepath.WalkDir(clean, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				warnings = append(warnings, walkErr.Error())
				return nil
			}
			if !entry.IsDir() {
				return nil
			}
			if curationSkipDir(entry.Name()) && path != clean {
				return filepath.SkipDir
			}
			summaryPath := filepath.Join(path, "summary.json")
			if _, err := os.Stat(summaryPath); err != nil {
				return nil
			}
			item, err := loadCurationSummaryFile(summaryPath)
			if err != nil {
				warnings = append(warnings, err.Error())
				return filepath.SkipDir
			}
			if !seen[item.loaded] {
				loaded = append(loaded, item)
				seen[item.loaded] = true
			}
			return filepath.SkipDir
		})
		if err != nil {
			return nil, nil, err
		}
	}
	sort.SliceStable(loaded, func(i, j int) bool {
		if loaded[i].when != loaded[j].when {
			return loaded[i].when > loaded[j].when
		}
		return loaded[i].loaded > loaded[j].loaded
	})
	return loaded, warnings, nil
}

func curationSkipDir(name string) bool {
	switch name {
	case "codex-home", "workspace", "before", "after", ".git", "node_modules":
		return true
	default:
		return false
	}
}

func loadCurationSummaryFile(path string) (curationLoaded, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return curationLoaded{}, fmt.Errorf("read %s: %w", path, err)
	}
	var kind curationJSONKind
	if err := json.Unmarshal(raw, &kind); err != nil {
		return curationLoaded{}, fmt.Errorf("parse %s: %w", path, err)
	}
	source := filepath.Dir(path)
	switch {
	case len(kind.Profiles) > 0:
		var report AutoReport
		if err := json.Unmarshal(raw, &report); err != nil {
			return curationLoaded{}, fmt.Errorf("parse auto report %s: %w", path, err)
		}
		return curationLoaded{kind: CurationKindAuto, source: source, loaded: path, when: report.GeneratedAt, auto: &report}, nil
	case len(kind.Control) > 0 && len(kind.Candidates) > 0:
		var report CompareReport
		if err := json.Unmarshal(raw, &report); err != nil {
			return curationLoaded{}, fmt.Errorf("parse compare report %s: %w", path, err)
		}
		return curationLoaded{kind: CurationKindCompare, source: source, loaded: path, when: report.GeneratedAt, compare: &report}, nil
	case len(kind.Tasks) > 0 && len(kind.Environment) > 0:
		var summary Summary
		if err := json.Unmarshal(raw, &summary); err != nil {
			return curationLoaded{}, fmt.Errorf("parse eval summary %s: %w", path, err)
		}
		if strings.TrimSpace(summary.RunID) == "" {
			return curationLoaded{}, fmt.Errorf("%s missing run_id", path)
		}
		return curationLoaded{kind: CurationKindRun, source: source, loaded: path, when: summary.StartedAt, summary: &summary}, nil
	default:
		return curationLoaded{}, fmt.Errorf("%s is not a Codex eval summary, compare report, or auto report", path)
	}
}

func expandCurationInputs(loaded []curationLoaded) []CurationProfileResult {
	var results []CurationProfileResult
	for _, input := range loaded {
		switch input.kind {
		case CurationKindAuto:
			results = append(results, expandCurationAuto(input)...)
		case CurationKindCompare:
			results = append(results, expandCurationCompare(input)...)
		case CurationKindRun:
			results = append(results, curationResultFromSummary(input.source, *input.summary))
		}
	}
	return results
}

func expandCurationAuto(input curationLoaded) []CurationProfileResult {
	var results []CurationProfileResult
	for _, profile := range input.auto.Profiles {
		if len(profile.Candidates) == 0 {
			results = append(results, CurationProfileResult{
				Kind:           CurationKindAuto,
				Source:         profile.Source,
				Profile:        profile.Name,
				Status:         profile.Status,
				StrictClean:    profile.StrictClean,
				RetryDependent: profile.RetryDependent,
				Counts:         copyIntMap(profile.Counts),
				FailedTasks:    append([]AutoTaskFinding(nil), profile.FailedTasks...),
				RetryTasks:     append([]AutoTaskFinding(nil), profile.RetryTasks...),
			})
			continue
		}
		for _, candidate := range profile.Candidates {
			result := CurationProfileResult{
				Kind:           CurationKindAuto,
				Source:         profile.Source,
				Profile:        profile.Name,
				RunID:          candidate.RunID,
				Model:          candidate.Model,
				Suite:          candidate.Suite,
				StartedAt:      candidate.StartedAt,
				Status:         profile.Status,
				StrictClean:    profile.StrictClean,
				Passed:         candidate.Passed,
				Total:          candidate.Total,
				RetryDependent: profile.RetryDependent,
				Counts:         copyIntMap(profile.Counts),
				FailedTasks:    curationFindingsForCandidate(profile.FailedTasks, candidate.RunID),
				RetryTasks:     curationFindingsForCandidate(profile.RetryTasks, candidate.RunID),
			}
			results = append(results, result)
		}
	}
	return results
}

func expandCurationCompare(input curationLoaded) []CurationProfileResult {
	var results []CurationProfileResult
	for _, candidate := range input.compare.Candidates {
		profile := curationProfileName(input.source, candidate.Run.Suite)
		failed, retry := curationFindingsFromTaskCompares(profile, candidate)
		status := StatusPassed
		strictClean := true
		if input.compare.Control.Passed != input.compare.Control.Total || candidate.Run.Passed != candidate.Run.Total {
			status = "failed"
			strictClean = false
		}
		retryCount := candidate.Counts[DiagnosisRetryDependent]
		if retryCount > 0 {
			strictClean = false
		}
		results = append(results, CurationProfileResult{
			Kind:           CurationKindCompare,
			Source:         input.source,
			Profile:        profile,
			RunID:          candidate.Run.RunID,
			Model:          candidate.Run.Model,
			Suite:          candidate.Run.Suite,
			StartedAt:      candidate.Run.StartedAt,
			Status:         status,
			StrictClean:    strictClean,
			Passed:         candidate.Run.Passed,
			Total:          candidate.Run.Total,
			RetryDependent: retryCount,
			FailedTasks:    failed,
			RetryTasks:     retry,
			Counts:         copyIntMap(candidate.Counts),
		})
	}
	return results
}

func curationResultFromSummary(source string, summary Summary) CurationProfileResult {
	var failed []AutoTaskFinding
	var retry []AutoTaskFinding
	counts := map[string]int{}
	profile := curationProfileName(source, summary.Environment.Suite)
	for _, task := range summary.Tasks {
		if retryDependent(task) {
			finding := AutoTaskFinding{
				Profile:   profile,
				Candidate: summary.RunID,
				Task:      task.ID,
				Diagnosis: DiagnosisRetryDependent,
				Status:    task.Status,
				Bucket:    task.FailureBucket,
				Attempts:  len(task.Attempts),
			}
			retry = append(retry, finding)
			counts[DiagnosisRetryDependent]++
			continue
		}
		if task.Status != StatusPassed {
			diagnosis := candidateDiagnosis(task)
			failed = append(failed, AutoTaskFinding{
				Profile:   profile,
				Candidate: summary.RunID,
				Task:      task.ID,
				Diagnosis: diagnosis,
				Status:    task.Status,
				Bucket:    task.FailureBucket,
				Attempts:  len(task.Attempts),
			})
			counts[diagnosis]++
			continue
		}
		counts[DiagnosisOK]++
	}
	status := StatusPassed
	strictClean := len(failed) == 0 && len(retry) == 0 && summary.Counts[StatusPassed] == len(summary.Tasks)
	if summary.Counts[StatusPassed] != len(summary.Tasks) {
		status = "failed"
	}
	return CurationProfileResult{
		Kind:           CurationKindRun,
		Source:         source,
		Profile:        profile,
		RunID:          summary.RunID,
		Model:          summary.Environment.Model,
		Suite:          summary.Environment.Suite,
		StartedAt:      summary.StartedAt,
		Status:         status,
		StrictClean:    strictClean,
		Passed:         summary.Counts[StatusPassed],
		Total:          len(summary.Tasks),
		RetryDependent: len(retry),
		FailedTasks:    failed,
		RetryTasks:     retry,
		Counts:         counts,
	}
}

func curationProfileName(source, suite string) string {
	base := filepath.Base(filepath.Clean(source))
	switch base {
	case "baseline", "expanded", "bench-lite":
		return base
	}
	switch suite {
	case "codex-real-upstream":
		return "baseline"
	case "codex-real-upstream-expanded":
		return "expanded"
	case "codex-bench-lite":
		return "bench-lite"
	default:
		if strings.TrimSpace(suite) != "" {
			return suite
		}
		return base
	}
}

func curationFindingsFromTaskCompares(profile string, candidate CandidateCompare) (failed, retry []AutoTaskFinding) {
	for _, task := range append(append([]TaskCompare{}, candidate.Tasks...), candidate.Coverage...) {
		finding := AutoTaskFinding{
			Profile:   profile,
			Candidate: candidate.Run.RunID,
			Task:      task.ID,
			Diagnosis: task.Diagnosis,
			Status:    task.CandidateStatus,
			Bucket:    task.CandidateBucket,
			Attempts:  task.CandidateAttempts,
		}
		switch task.Diagnosis {
		case DiagnosisOK, DiagnosisCandidateOnlyTask, DiagnosisCandidateMissingTask:
			continue
		case DiagnosisRetryDependent:
			retry = append(retry, finding)
		default:
			failed = append(failed, finding)
		}
	}
	return failed, retry
}

func curationFindingsForCandidate(findings []AutoTaskFinding, runID string) []AutoTaskFinding {
	var result []AutoTaskFinding
	for _, finding := range findings {
		if finding.Candidate == runID || finding.Candidate == "" {
			result = append(result, finding)
		}
	}
	return result
}

func normalizeCurationModelAliases(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	aliases := make(map[string]string, len(values))
	keys := make([]string, 0, len(values))
	for raw, canonical := range values {
		raw = strings.TrimSpace(raw)
		canonical = strings.TrimSpace(canonical)
		if raw == "" || canonical == "" {
			continue
		}
		if _, exists := aliases[raw]; !exists {
			aliases[raw] = canonical
			keys = append(keys, raw)
		}
	}
	if len(aliases) == 0 {
		return nil
	}
	sort.Strings(keys)
	ordered := make(map[string]string, len(aliases))
	for _, key := range keys {
		ordered[key] = aliases[key]
	}
	return ordered
}

func applyCurationModelAliases(results []CurationProfileResult, aliases map[string]string) []CurationProfileResult {
	for i := range results {
		canonical := curationCanonicalModelFor(results[i].Model, aliases)
		if canonical == "" {
			continue
		}
		results[i].CanonicalModel = canonical
		if strings.Contains(canonical, "/") {
			results[i].PublicModel = canonical
		}
	}
	return results
}

func curationCanonicalModelFor(model string, aliases map[string]string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return ""
	}
	if canonical := strings.TrimSpace(aliases[model]); canonical != "" {
		return canonical
	}
	if strings.Contains(model, "/") {
		return model
	}
	return ""
}

func curationModelKey(result CurationProfileResult) string {
	if result.CanonicalModel != "" {
		return result.CanonicalModel
	}
	return result.Model
}

func curationPublicModelFor(result CurationProfileResult) string {
	if result.PublicModel != "" {
		return result.PublicModel
	}
	if strings.Contains(result.CanonicalModel, "/") {
		return result.CanonicalModel
	}
	if strings.Contains(result.Model, "/") {
		return result.Model
	}
	return ""
}

func curationDisplayModel(result CurationProfileResult) string {
	canonical := curationModelKey(result)
	if canonical == "" || canonical == result.Model {
		return result.Model
	}
	return result.Model + " -> " + canonical
}

func filterCurationResults(results []CurationProfileResult, model string, since *time.Time) []CurationProfileResult {
	var filtered []CurationProfileResult
	model = strings.TrimSpace(model)
	for _, result := range results {
		if model != "" && result.Model != model && result.CanonicalModel != model && result.PublicModel != model {
			continue
		}
		if since != nil {
			parsed, ok := parseCurationTime(result.StartedAt)
			if ok && parsed.Before(*since) {
				continue
			}
		}
		filtered = append(filtered, result)
	}
	return filtered
}

func parseCurationSince(value string) (*time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02", "20060102"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			utc := parsed.UTC()
			return &utc, nil
		}
	}
	return nil, fmt.Errorf("invalid --since value %q; use RFC3339 or YYYY-MM-DD", value)
}

func parseCurationTime(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02", "20060102T150405Z"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC(), true
		}
	}
	return time.Time{}, false
}

func curationSortTime(result CurationProfileResult) string {
	if result.StartedAt != "" {
		return result.StartedAt
	}
	return result.Source
}

func curationShortTime(value string) string {
	parsed, ok := parseCurationTime(value)
	if !ok {
		return value
	}
	return parsed.Format("2006-01-02 15:04")
}

func curationInterpretation(result CurationProfileResult) (string, bool, []string) {
	var notes []string
	if result.StrictClean {
		notes = append(notes, "strict-clean")
	}
	if result.RetryDependent > 0 {
		notes = append(notes, fmt.Sprintf("retry-dependent=%d", result.RetryDependent))
	}
	if len(result.FailedTasks) > 0 {
		notes = append(notes, fmt.Sprintf("failed=%d", len(result.FailedTasks)))
	}
	if result.Profile == "baseline" {
		switch {
		case result.Status == StatusPassed && result.StrictClean:
			return "promote_baseline_after_log_spot_check", true, notes
		case result.Status == StatusPassed:
			return "record_retry_dependent_baseline", true, notes
		default:
			return "do_not_promote_baseline", false, notes
		}
	}
	switch {
	case result.Status == StatusPassed && result.StrictClean:
		return "diagnostic_strict_green", false, notes
	case result.Status == StatusPassed:
		return "diagnostic_retry_dependent_green", false, notes
	default:
		return "diagnostic_attention", false, notes
	}
}

func curationNeedsAttention(results []CurationProfileResult) bool {
	for _, result := range results {
		if result.Status != StatusPassed || result.RetryDependent > 0 || len(result.FailedTasks) > 0 {
			return true
		}
	}
	return false
}

func curationDiagnosisCounts(results []CurationProfileResult) map[string]int {
	counts := map[string]int{}
	for _, result := range results {
		for diagnosis, count := range result.Counts {
			counts[diagnosis] += count
		}
	}
	return counts
}

func buildCurationModelSummaries(results []CurationProfileResult) []CurationModelSummary {
	type aggregate struct {
		summary CurationModelSummary
		latest  CurationProfileResult
		tasks   map[string]bool
		raw     map[string]bool
	}
	aggregates := map[string]*aggregate{}
	for _, result := range results {
		modelKey := curationModelKey(result)
		key := modelKey + "\x00" + result.Profile
		agg := aggregates[key]
		if agg == nil {
			agg = &aggregate{
				summary: CurationModelSummary{
					Model:           modelKey,
					PublicModel:     curationPublicModelFor(result),
					CanonicalModel:  result.CanonicalModel,
					Profile:         result.Profile,
					DiagnosisCounts: map[string]int{},
				},
				tasks: map[string]bool{},
				raw:   map[string]bool{},
			}
			aggregates[key] = agg
		}
		if result.Model != "" {
			agg.raw[result.Model] = true
		}
		if agg.summary.PublicModel == "" {
			agg.summary.PublicModel = curationPublicModelFor(result)
		}
		if agg.summary.CanonicalModel == "" {
			agg.summary.CanonicalModel = result.CanonicalModel
		}
		agg.summary.Runs++
		if result.StrictClean {
			agg.summary.StrictCleanRuns++
		}
		if result.Status == StatusPassed && result.RetryDependent > 0 {
			agg.summary.RetryPassedRuns++
		}
		if result.Status != StatusPassed {
			agg.summary.FailedRuns++
		}
		agg.summary.Passed += result.Passed
		agg.summary.Total += result.Total
		for diagnosis, count := range result.Counts {
			agg.summary.DiagnosisCounts[diagnosis] += count
		}
		for _, finding := range append(append([]AutoTaskFinding{}, result.FailedTasks...), result.RetryTasks...) {
			if finding.Task != "" {
				agg.tasks[finding.Task] = true
			}
		}
		if agg.latest.Source == "" || curationSortTime(result) > curationSortTime(agg.latest) {
			agg.latest = result
		}
	}
	keys := make([]string, 0, len(aggregates))
	for key := range aggregates {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	summaries := make([]CurationModelSummary, 0, len(keys))
	for _, key := range keys {
		agg := aggregates[key]
		taskIDs := make([]string, 0, len(agg.tasks))
		for taskID := range agg.tasks {
			taskIDs = append(taskIDs, taskID)
		}
		sort.Strings(taskIDs)
		rawModels := make([]string, 0, len(agg.raw))
		for model := range agg.raw {
			rawModels = append(rawModels, model)
		}
		sort.Strings(rawModels)
		if len(rawModels) > 0 {
			agg.summary.RawModels = rawModels
		}
		agg.summary.UnstableTasks = taskIDs
		agg.summary.LatestSource = agg.latest.Source
		agg.summary.LatestRunID = agg.latest.RunID
		agg.summary.LatestStartedAt = agg.latest.StartedAt
		agg.summary.LatestStatus = agg.latest.Status
		agg.summary.LatestInterpretation = agg.latest.Interpretation
		summaries = append(summaries, agg.summary)
	}
	sort.SliceStable(summaries, func(i, j int) bool {
		if summaries[i].Model != summaries[j].Model {
			return summaries[i].Model < summaries[j].Model
		}
		return autoProfileSortKey(summaries[i].Profile) < autoProfileSortKey(summaries[j].Profile)
	})
	return summaries
}

func buildCurationTaskTrends(results []CurationProfileResult) []CurationTaskTrend {
	type trendAggregate struct {
		trend CurationTaskTrend
		raw   map[string]bool
	}
	trends := map[string]*trendAggregate{}
	addFinding := func(result CurationProfileResult, finding AutoTaskFinding, retry bool) {
		if finding.Task == "" {
			return
		}
		modelKey := curationModelKey(result)
		key := modelKey + "\x00" + result.Profile + "\x00" + finding.Task
		agg := trends[key]
		if agg == nil {
			agg = &trendAggregate{
				trend: CurationTaskTrend{
					Model:          modelKey,
					PublicModel:    curationPublicModelFor(result),
					CanonicalModel: result.CanonicalModel,
					Profile:        result.Profile,
					Task:           finding.Task,
					Diagnoses:      map[string]int{},
				},
				raw: map[string]bool{},
			}
			trends[key] = agg
		}
		if result.Model != "" {
			agg.raw[result.Model] = true
		}
		trend := &agg.trend
		if retry {
			trend.RetryDependent++
		} else {
			trend.Failed++
		}
		if finding.Diagnosis != "" {
			trend.Diagnoses[finding.Diagnosis]++
		}
		if trend.LastSource == "" || curationSortTime(result) > trend.LastStartedAt {
			trend.LastStatus = finding.Status
			trend.LastDiagnosis = finding.Diagnosis
			trend.LastSource = result.Source
			trend.LastStartedAt = curationSortTime(result)
		}
	}
	for _, result := range results {
		for _, finding := range result.FailedTasks {
			addFinding(result, finding, false)
		}
		for _, finding := range result.RetryTasks {
			addFinding(result, finding, true)
		}
	}
	keys := make([]string, 0, len(trends))
	for key := range trends {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]CurationTaskTrend, 0, len(keys))
	for _, key := range keys {
		agg := trends[key]
		rawModels := make([]string, 0, len(agg.raw))
		for model := range agg.raw {
			rawModels = append(rawModels, model)
		}
		sort.Strings(rawModels)
		if len(rawModels) > 0 {
			agg.trend.RawModels = rawModels
		}
		result = append(result, agg.trend)
	}
	sort.SliceStable(result, func(i, j int) bool {
		left := result[i].Failed + result[i].RetryDependent
		right := result[j].Failed + result[j].RetryDependent
		if left != right {
			return left > right
		}
		if result[i].Model != result[j].Model {
			return result[i].Model < result[j].Model
		}
		if result[i].Profile != result[j].Profile {
			return autoProfileSortKey(result[i].Profile) < autoProfileSortKey(result[j].Profile)
		}
		return result[i].Task < result[j].Task
	})
	if len(result) > 20 {
		result = result[:20]
	}
	return result
}

func buildCurationRecommendations(report CurationReport) []string {
	if len(report.ProfileResults) == 0 {
		return []string{"No matching Codex eval artifacts were found. Run `make codex-eval-auto` or pass explicit artifact roots."}
	}
	var recommendations []string
	latestBaseline := map[string]CurationProfileResult{}
	for _, result := range report.ProfileResults {
		if result.Profile != "baseline" {
			continue
		}
		model := curationModelKey(result)
		current, ok := latestBaseline[model]
		if !ok || curationSortTime(result) > curationSortTime(current) {
			latestBaseline[model] = result
		}
	}
	if len(latestBaseline) == 0 {
		recommendations = append(recommendations, "No `baseline` profile was found; do not update the model matrix baseline from this curation report.")
	} else {
		models := make([]string, 0, len(latestBaseline))
		for model := range latestBaseline {
			models = append(models, model)
		}
		sort.Strings(models)
		for _, model := range models {
			result := latestBaseline[model]
			switch result.Interpretation {
			case "promote_baseline_after_log_spot_check":
				recommendations = append(recommendations, fmt.Sprintf("`%s` latest baseline is strict-clean (%d/%d); it can be promoted after checking relevant shim diagnostics.", markdownCell(model), result.Passed, result.Total))
			case "record_retry_dependent_baseline":
				recommendations = append(recommendations, fmt.Sprintf("`%s` latest baseline passed with %d retry-dependent task(s); record it as retry-dependent, not strict-clean.", markdownCell(model), result.RetryDependent))
			default:
				recommendations = append(recommendations, fmt.Sprintf("`%s` latest baseline is not promotable; inspect failed tasks before changing the matrix.", markdownCell(model)))
			}
		}
	}
	if count := report.DiagnosisCounts[DiagnosisCandidateTransport]; count > 0 {
		recommendations = append(recommendations, fmt.Sprintf("Transport diagnoses present (%d); inspect shim log diagnostics before blaming model behavior.", count))
	}
	if count := report.DiagnosisCounts[DiagnosisCandidateToolContract]; count > 0 {
		recommendations = append(recommendations, fmt.Sprintf("Tool-contract diagnoses present (%d); check raw-markup/tool-use artifacts before adding shim repair logic.", count))
	}
	if count := report.DiagnosisCounts[DiagnosisRetryDependent]; count > 0 {
		recommendations = append(recommendations, fmt.Sprintf("Retry-dependent passes present (%d); keep them visible in matrix notes and trend summaries.", count))
	}
	return recommendations
}

func copyIntMap(values map[string]int) map[string]int {
	result := make(map[string]int, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func markdownListCell(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	limit := len(values)
	if limit > 8 {
		limit = 8
	}
	parts := make([]string, 0, limit+1)
	for _, value := range values[:limit] {
		parts = append(parts, "`"+markdownCell(value)+"`")
	}
	if len(values) > limit {
		parts = append(parts, fmt.Sprintf("+%d more", len(values)-limit))
	}
	return strings.Join(parts, "<br>")
}
