package codexeval

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	DiagnosisOK                    = "ok"
	DiagnosisRetryDependent        = "retry_dependent"
	DiagnosisControlFailed         = "control_failed"
	DiagnosisCandidateTransport    = "candidate_transport"
	DiagnosisCandidateToolContract = "candidate_tool_contract"
	DiagnosisCandidateModel        = "candidate_model_behavior"
	DiagnosisCandidateHarness      = "candidate_harness"
	DiagnosisCandidateMissingTask  = "candidate_missing_task"
	DiagnosisCandidateOnlyTask     = "candidate_only_task"
)

type CompareReport struct {
	GeneratedAt string             `json:"generated_at"`
	Control     CompareRunRef      `json:"control"`
	Candidates  []CandidateCompare `json:"candidates"`
	Counts      map[string]int     `json:"counts"`
}

type CompareRunRef struct {
	RunID     string `json:"run_id"`
	Source    string `json:"source"`
	Model     string `json:"model"`
	Suite     string `json:"suite"`
	Scope     string `json:"scope"`
	StartedAt string `json:"started_at,omitempty"`
	Status    string `json:"status"`
	Passed    int    `json:"passed"`
	Total     int    `json:"total"`
}

type CandidateCompare struct {
	Run      CompareRunRef  `json:"run"`
	Counts   map[string]int `json:"counts"`
	Tasks    []TaskCompare  `json:"tasks"`
	Coverage []TaskCompare  `json:"coverage,omitempty"`
}

type TaskCompare struct {
	ID                string `json:"id"`
	ControlStatus     string `json:"control_status,omitempty"`
	ControlBucket     string `json:"control_bucket,omitempty"`
	CandidateStatus   string `json:"candidate_status,omitempty"`
	CandidateBucket   string `json:"candidate_bucket,omitempty"`
	CandidateAttempts int    `json:"candidate_attempts,omitempty"`
	RetryDependent    bool   `json:"retry_dependent,omitempty"`
	Diagnosis         string `json:"diagnosis"`
}

func BuildCompareReport(controlPath string, candidatePaths []string) (CompareReport, error) {
	control, err := loadSingleCompareSummary(controlPath)
	if err != nil {
		return CompareReport{}, fmt.Errorf("load control summary: %w", err)
	}
	candidates, err := loadCandidateCompareSummaries(candidatePaths)
	if err != nil {
		return CompareReport{}, err
	}
	if len(candidates) == 0 {
		return CompareReport{}, fmt.Errorf("at least one candidate summary is required")
	}

	report := CompareReport{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Control:     compareRunRef(controlPath, control),
		Counts:      map[string]int{},
	}
	for _, candidate := range candidates {
		comparison := compareCandidate(control, candidate.source, candidate.summary)
		report.Candidates = append(report.Candidates, comparison)
		for diagnosis, count := range comparison.Counts {
			report.Counts[diagnosis] += count
		}
	}
	return report, nil
}

func RenderCompareMarkdown(controlPath string, candidatePaths []string) (string, error) {
	report, err := BuildCompareReport(controlPath, candidatePaths)
	if err != nil {
		return "", err
	}
	return RenderCompareReportMarkdown(report), nil
}

func RenderCompareReportMarkdown(report CompareReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Codex Eval Compare\n\n")
	fmt.Fprintf(&b, "- Generated: `%s`\n", report.GeneratedAt)
	fmt.Fprintf(&b, "- Control: `%s` `%s` `%s` `%s` `%d/%d`\n\n",
		report.Control.RunID,
		report.Control.Model,
		report.Control.Suite,
		report.Control.Scope,
		report.Control.Passed,
		report.Control.Total,
	)

	fmt.Fprintf(&b, "## Candidate Overview\n\n")
	fmt.Fprintf(&b, "| Run | Model | Suite | Scope | Result | Diagnoses |\n")
	fmt.Fprintf(&b, "| --- | --- | --- | --- | ---: | --- |\n")
	for _, candidate := range report.Candidates {
		fmt.Fprintf(
			&b,
			"| `%s` | `%s` | `%s` | `%s` | %d/%d | %s |\n",
			markdownCell(candidate.Run.RunID),
			markdownCell(candidate.Run.Model),
			markdownCell(candidate.Run.Suite),
			markdownCell(candidate.Run.Scope),
			candidate.Run.Passed,
			candidate.Run.Total,
			diagnosisCountsText(candidate.Counts),
		)
	}

	for _, candidate := range report.Candidates {
		fmt.Fprintf(&b, "\n## `%s`\n\n", candidate.Run.RunID)
		fmt.Fprintf(&b, "### Comparable Tasks\n\n")
		fmt.Fprintf(&b, "| Task | Control | Candidate | Diagnosis | Candidate bucket | Attempts |\n")
		fmt.Fprintf(&b, "| --- | --- | --- | --- | --- | ---: |\n")
		for _, task := range candidate.Tasks {
			fmt.Fprintf(
				&b,
				"| `%s` | `%s` | `%s` | `%s` | `%s` | %d |\n",
				markdownCell(task.ID),
				markdownCell(statusWithBucket(task.ControlStatus, task.ControlBucket)),
				markdownCell(statusWithBucket(task.CandidateStatus, task.CandidateBucket)),
				markdownCell(task.Diagnosis),
				markdownCell(task.CandidateBucket),
				task.CandidateAttempts,
			)
		}
		if len(candidate.Coverage) > 0 {
			fmt.Fprintf(&b, "\n### Coverage Differences\n\n")
			fmt.Fprintf(&b, "| Task | Control | Candidate | Note |\n")
			fmt.Fprintf(&b, "| --- | --- | --- | --- |\n")
			for _, task := range candidate.Coverage {
				fmt.Fprintf(
					&b,
					"| `%s` | `%s` | `%s` | `%s` |\n",
					markdownCell(task.ID),
					markdownCell(statusWithBucket(task.ControlStatus, task.ControlBucket)),
					markdownCell(statusWithBucket(task.CandidateStatus, task.CandidateBucket)),
					markdownCell(task.Diagnosis),
				)
			}
		}
	}
	return b.String()
}

type loadedCompareSummary struct {
	source  string
	summary Summary
}

func loadSingleCompareSummary(path string) (Summary, error) {
	summaries, err := loadMatrixSummariesFromPath(path)
	if err != nil {
		return Summary{}, err
	}
	if len(summaries) != 1 {
		return Summary{}, fmt.Errorf("%s resolved to %d summaries; pass one run directory or summary.json", path, len(summaries))
	}
	return summaries[0], nil
}

func loadCandidateCompareSummaries(paths []string) ([]loadedCompareSummary, error) {
	var loaded []loadedCompareSummary
	for _, path := range paths {
		summaries, err := loadMatrixSummariesFromPath(path)
		if err != nil {
			return nil, fmt.Errorf("load candidate summary %s: %w", path, err)
		}
		for _, summary := range summaries {
			loaded = append(loaded, loadedCompareSummary{
				source:  path,
				summary: summary,
			})
		}
	}
	sort.Slice(loaded, func(i, j int) bool {
		if loaded[i].summary.StartedAt != loaded[j].summary.StartedAt {
			return loaded[i].summary.StartedAt < loaded[j].summary.StartedAt
		}
		return loaded[i].summary.RunID < loaded[j].summary.RunID
	})
	return loaded, nil
}

func compareCandidate(control Summary, source string, candidate Summary) CandidateCompare {
	controlTasks := taskResultByID(control.Tasks)
	candidateTasks := taskResultByID(candidate.Tasks)
	ids := compareTaskIDs(control.Tasks, candidate.Tasks)
	comparison := CandidateCompare{
		Run:    compareRunRef(source, candidate),
		Counts: map[string]int{},
	}
	for _, id := range ids {
		controlTask := controlTasks[id]
		candidateTask := candidateTasks[id]
		task := compareTask(controlTask, candidateTask)
		if diagnosis := compareCountDiagnosis(controlTask, candidateTask, task); diagnosis != "" {
			comparison.Counts[diagnosis]++
		}
		if controlTask == nil || candidateTask == nil {
			comparison.Coverage = append(comparison.Coverage, task)
			continue
		}
		comparison.Tasks = append(comparison.Tasks, task)
	}
	return comparison
}

func compareCountDiagnosis(controlTask, candidateTask *TaskResult, task TaskCompare) string {
	if candidateTask == nil {
		return ""
	}
	if controlTask != nil {
		return task.Diagnosis
	}
	if candidateTask.Status == StatusPassed && task.RetryDependent {
		return DiagnosisRetryDependent
	}
	if candidateTask.Status == StatusPassed {
		return DiagnosisOK
	}
	return candidateDiagnosis(*candidateTask)
}

func compareTask(controlTask, candidateTask *TaskResult) TaskCompare {
	result := TaskCompare{Diagnosis: DiagnosisOK}
	if controlTask != nil {
		result.ID = controlTask.ID
		result.ControlStatus = controlTask.Status
		result.ControlBucket = controlTask.FailureBucket
	}
	if candidateTask != nil {
		result.ID = candidateTask.ID
		result.CandidateStatus = candidateTask.Status
		result.CandidateBucket = candidateTask.FailureBucket
		result.CandidateAttempts = len(candidateTask.Attempts)
		result.RetryDependent = retryDependent(*candidateTask)
	}

	switch {
	case controlTask == nil:
		result.Diagnosis = DiagnosisCandidateOnlyTask
	case candidateTask == nil:
		result.Diagnosis = DiagnosisCandidateMissingTask
	case controlTask.Status != StatusPassed:
		result.Diagnosis = DiagnosisControlFailed
	case candidateTask.Status == StatusPassed && result.RetryDependent:
		result.Diagnosis = DiagnosisRetryDependent
	case candidateTask.Status == StatusPassed:
		result.Diagnosis = DiagnosisOK
	default:
		result.Diagnosis = candidateDiagnosis(*candidateTask)
	}
	return result
}

func candidateDiagnosis(task TaskResult) string {
	switch task.FailureBucket {
	case BucketShimAuth, BucketShimTransport, BucketUpstreamHTTP, BucketUpstreamStream, BucketTimeout:
		return DiagnosisCandidateTransport
	case BucketRawToolMarkup, BucketModelBadToolArgs, BucketCodexToolMissing, BucketCodexToolExec:
		return DiagnosisCandidateToolContract
	case BucketModelNoTool, BucketCheckerDiff, BucketCheckerTests:
		return DiagnosisCandidateModel
	case BucketCodexConfig, BucketHarnessBug:
		return DiagnosisCandidateHarness
	}
	switch task.Status {
	case StatusFailedTransport, StatusFailedTimeout:
		return DiagnosisCandidateTransport
	case StatusFailedRawTool, StatusFailedNoToolEvent:
		return DiagnosisCandidateToolContract
	case StatusFailedSetup:
		return DiagnosisCandidateHarness
	default:
		return DiagnosisCandidateModel
	}
}

func compareRunRef(source string, summary Summary) CompareRunRef {
	passed := summary.Counts[StatusPassed]
	total := len(summary.Tasks)
	status := StatusPassed
	if passed != total {
		status = "failed"
	}
	return CompareRunRef{
		RunID:     summary.RunID,
		Source:    source,
		Model:     summary.Environment.Model,
		Suite:     summary.Environment.Suite,
		StartedAt: summary.StartedAt,
		Status:    status,
		Passed:    passed,
		Total:     total,
		Scope:     SuiteScope(summary.Environment.Suite),
	}
}

func taskResultByID(tasks []TaskResult) map[string]*TaskResult {
	byID := make(map[string]*TaskResult, len(tasks))
	for i := range tasks {
		if tasks[i].ID != "" {
			byID[tasks[i].ID] = &tasks[i]
		}
	}
	return byID
}

func compareTaskIDs(controlTasks, candidateTasks []TaskResult) []string {
	seen := map[string]bool{}
	ids := make([]string, 0, len(controlTasks)+len(candidateTasks))
	for _, task := range controlTasks {
		if task.ID == "" || seen[task.ID] {
			continue
		}
		seen[task.ID] = true
		ids = append(ids, task.ID)
	}
	var extra []string
	for _, task := range candidateTasks {
		if task.ID == "" || seen[task.ID] {
			continue
		}
		seen[task.ID] = true
		extra = append(extra, task.ID)
	}
	sort.Strings(extra)
	return append(ids, extra...)
}

func retryDependent(task TaskResult) bool {
	if task.Status != StatusPassed || len(task.Attempts) < 2 {
		return false
	}
	sawFailure := false
	for _, attempt := range task.Attempts {
		if attempt.Status != StatusPassed {
			sawFailure = true
			continue
		}
		if attempt.Status == StatusPassed {
			return sawFailure
		}
	}
	return false
}

func diagnosisCountsText(counts map[string]int) string {
	if len(counts) == 0 {
		return "none"
	}
	keys := sortedKeys(counts)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("`%s`: %d", key, counts[key]))
	}
	return strings.Join(parts, ", ")
}

func statusWithBucket(status, bucket string) string {
	if status == "" {
		return ""
	}
	if bucket == "" {
		return status
	}
	return status + " / " + bucket
}

func markdownCell(value string) string {
	value = strings.ReplaceAll(value, "|", "\\|")
	value = strings.ReplaceAll(value, "\n", " ")
	return value
}
