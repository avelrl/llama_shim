package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	defaultEvidenceRoot           = ".tmp"
	defaultEvidenceMaxEntries     = 50
	defaultEvidenceStaleAfter     = 7 * 24 * time.Hour
	evidenceSummaryMaxBytes       = 1 << 20
	evidenceObjectList            = "shim.evidence_list"
	evidenceObjectDetail          = "shim.evidence"
	evidenceRedactionPolicy       = "summary_json_only_no_raw_logs_no_request_bodies_no_headers"
	evidenceTimestampLayout       = "20060102T150405Z"
	evidenceGeneratedAtLayout     = time.RFC3339
	evidenceGeneratedAtNanoLayout = time.RFC3339Nano
)

type EvidenceConfig struct {
	Enabled    bool
	Root       string
	MaxEntries int
	StaleAfter time.Duration
}

func normalizeEvidenceConfig(cfg EvidenceConfig) EvidenceConfig {
	if strings.TrimSpace(cfg.Root) == "" {
		cfg.Root = defaultEvidenceRoot
	}
	cfg.Root = filepath.Clean(strings.TrimSpace(cfg.Root))
	if cfg.MaxEntries <= 0 {
		cfg.MaxEntries = defaultEvidenceMaxEntries
	}
	if cfg.StaleAfter < 0 {
		cfg.StaleAfter = 0
	}
	if cfg.StaleAfter == 0 {
		cfg.StaleAfter = defaultEvidenceStaleAfter
	}
	return cfg
}

type EvidenceRegistry struct {
	config EvidenceConfig
}

type EvidenceList struct {
	Object            string               `json:"object"`
	GeneratedAt       string               `json:"generated_at"`
	Root              string               `json:"root"`
	MaxEntries        int                  `json:"max_entries"`
	StaleAfterSeconds int64                `json:"stale_after_seconds"`
	RedactionPolicy   string               `json:"redaction_policy"`
	Sources           []EvidenceSourceInfo `json:"sources"`
	LatestByKind      []EvidenceRecord     `json:"latest_by_kind"`
	Data              []EvidenceRecord     `json:"data"`
	Errors            []EvidenceScanError  `json:"errors,omitempty"`
}

type EvidenceSourceInfo struct {
	Kind  string `json:"kind"`
	Title string `json:"title"`
	Glob  string `json:"glob"`
}

type EvidenceRecord struct {
	ID            string         `json:"id"`
	Kind          string         `json:"kind"`
	Title         string         `json:"title"`
	Status        string         `json:"status"`
	Verdict       string         `json:"verdict,omitempty"`
	Model         string         `json:"model,omitempty"`
	ArtifactDir   string         `json:"artifact_dir"`
	SummaryPath   string         `json:"summary_path"`
	SummaryMDPath string         `json:"summary_md_path,omitempty"`
	GeneratedAt   string         `json:"generated_at"`
	ModifiedAt    string         `json:"modified_at"`
	AgeSeconds    int64          `json:"age_seconds"`
	Stale         bool           `json:"stale"`
	WarningCount  int            `json:"warning_count,omitempty"`
	FailureCount  int            `json:"failure_count,omitempty"`
	Metrics       map[string]any `json:"metrics,omitempty"`
	Error         string         `json:"error,omitempty"`

	sortTime time.Time
}

type EvidenceDetail struct {
	Object          string         `json:"object"`
	RedactionPolicy string         `json:"redaction_policy"`
	Evidence        EvidenceRecord `json:"evidence"`
	Summary         map[string]any `json:"summary,omitempty"`
}

type EvidenceScanError struct {
	Kind    string `json:"kind,omitempty"`
	Path    string `json:"path,omitempty"`
	Message string `json:"message"`
}

type evidenceSource struct {
	kind  string
	title string
	glob  string
}

var evidenceSources = []evidenceSource{
	{kind: "v4_preflight_smoke", title: "V4 preflight smoke", glob: "v4-preflight-smoke/*/summary.json"},
	{kind: "v4_provider_config_doctor", title: "V4 provider config doctor", glob: "v4-provider-config-doctor/*/summary.json"},
	{kind: "v4_provider_matrix_smoke", title: "V4 provider matrix smoke", glob: "v4-provider-matrix-smoke/*/summary.json"},
	{kind: "v4_provider_matrix_curation", title: "V4 provider matrix curation", glob: "v4-provider-matrix-curation/*/summary.json"},
	{kind: "upstream_provider_routing_smoke", title: "Upstream provider routing smoke", glob: "upstream-provider-routing-smoke/*/summary.json"},
	{kind: "codex_eval_auto", title: "Codex eval auto", glob: "codex-eval-auto/*/summary.json"},
	{kind: "codex_eval_curation", title: "Codex eval curation", glob: "codex-eval-curation/*/summary.json"},
	{kind: "v3_computer_browser_harness", title: "V3 computer browser harness", glob: "v3-computer-browser-harness-runs/*/summary.json"},
}

var evidenceTimestampPattern = regexp.MustCompile(`\d{8}T\d{6}Z`)

func NewEvidenceRegistry(cfg EvidenceConfig) *EvidenceRegistry {
	cfg = normalizeEvidenceConfig(cfg)
	if !cfg.Enabled {
		return nil
	}
	return &EvidenceRegistry{config: cfg}
}

func (r *EvidenceRegistry) List(now time.Time) EvidenceList {
	if now.IsZero() {
		now = time.Now()
	}
	records, scanErrors := r.scan(now)
	latest := latestEvidenceByKind(records)
	return EvidenceList{
		Object:            evidenceObjectList,
		GeneratedAt:       now.UTC().Format(time.RFC3339Nano),
		Root:              slashPath(r.config.Root),
		MaxEntries:        r.config.MaxEntries,
		StaleAfterSeconds: int64(r.config.StaleAfter.Seconds()),
		RedactionPolicy:   evidenceRedactionPolicy,
		Sources:           evidenceSourceInfos(),
		LatestByKind:      latest,
		Data:              records,
		Errors:            scanErrors,
	}
}

func (r *EvidenceRegistry) Detail(id string, now time.Time) (EvidenceDetail, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		return EvidenceDetail{}, false
	}
	list := r.List(now)
	for _, record := range list.Data {
		if record.ID != id {
			continue
		}
		summary, err := readEvidenceSummary(record.SummaryPath)
		if err != nil {
			record.Error = err.Error()
		}
		return EvidenceDetail{
			Object:          evidenceObjectDetail,
			RedactionPolicy: evidenceRedactionPolicy,
			Evidence:        record,
			Summary:         summary,
		}, true
	}
	return EvidenceDetail{}, false
}

func (r *EvidenceRegistry) scan(now time.Time) ([]EvidenceRecord, []EvidenceScanError) {
	if r == nil {
		return nil, nil
	}
	var records []EvidenceRecord
	var scanErrors []EvidenceScanError
	for _, source := range evidenceSources {
		pattern := filepath.Join(r.config.Root, filepath.FromSlash(source.glob))
		matches, err := filepath.Glob(pattern)
		if err != nil {
			scanErrors = append(scanErrors, EvidenceScanError{
				Kind:    source.kind,
				Path:    slashPath(pattern),
				Message: err.Error(),
			})
			continue
		}
		for _, summaryPath := range matches {
			record := buildEvidenceRecord(source, summaryPath, now, r.config.StaleAfter)
			records = append(records, record)
		}
	}
	sort.SliceStable(records, func(i, j int) bool {
		if records[i].sortTime.Equal(records[j].sortTime) {
			return records[i].ID < records[j].ID
		}
		return records[i].sortTime.After(records[j].sortTime)
	})
	if len(records) > r.config.MaxEntries {
		records = records[:r.config.MaxEntries]
	}
	return records, scanErrors
}

func buildEvidenceRecord(source evidenceSource, summaryPath string, now time.Time, staleAfter time.Duration) EvidenceRecord {
	summaryPath = filepath.Clean(summaryPath)
	artifactDir := filepath.Dir(summaryPath)
	stat, statErr := os.Stat(summaryPath)
	modifiedAt := time.Time{}
	if statErr == nil {
		modifiedAt = stat.ModTime()
	}
	summary, readErr := readEvidenceSummary(summaryPath)

	generatedAt := evidenceGeneratedAt(summary, summaryPath, modifiedAt)
	sortTime := generatedAt
	if sortTime.IsZero() {
		sortTime = modifiedAt
	}
	if sortTime.IsZero() {
		sortTime = now
	}
	status := evidenceString(summary, "status")
	if status == "" {
		if readErr != nil || statErr != nil {
			status = "unreadable"
		} else {
			status = "unknown"
		}
	}
	record := EvidenceRecord{
		ID:            evidenceID(source.kind, artifactDir),
		Kind:          source.kind,
		Title:         evidenceTitle(source.title, artifactDir),
		Status:        status,
		Verdict:       evidenceString(summary, "verdict"),
		Model:         evidenceModel(summary, artifactDir),
		ArtifactDir:   slashPath(artifactDir),
		SummaryPath:   slashPath(summaryPath),
		SummaryMDPath: evidenceSummaryMarkdownPath(summaryPath),
		GeneratedAt:   formatEvidenceTime(generatedAt),
		ModifiedAt:    formatEvidenceTime(modifiedAt),
		AgeSeconds:    maxInt64(0, int64(now.Sub(sortTime).Seconds())),
		Stale:         staleAfter > 0 && now.Sub(sortTime) > staleAfter,
		WarningCount:  lenEvidenceArray(summary, "warnings"),
		FailureCount:  lenEvidenceArray(summary, "failures"),
		Metrics:       evidenceMetrics(summary),
		sortTime:      sortTime,
	}
	switch {
	case statErr != nil:
		record.Error = statErr.Error()
	case readErr != nil:
		record.Error = readErr.Error()
	}
	return record
}

func readEvidenceSummary(path string) (map[string]any, error) {
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, err
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, evidenceSummaryMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > evidenceSummaryMaxBytes {
		return nil, fmt.Errorf("summary JSON exceeds %d bytes", evidenceSummaryMaxBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var summary map[string]any
	if err := decoder.Decode(&summary); err != nil {
		return nil, err
	}
	return summary, nil
}

func evidenceListHandler(registry *EvidenceRegistry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			WriteError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed", "")
			return
		}
		if registry == nil {
			WriteError(w, http.StatusNotFound, "not_found_error", "operational evidence is disabled", "")
			return
		}
		WriteJSON(w, http.StatusOK, registry.List(time.Now()))
	}
}

func evidenceDetailHandler(registry *EvidenceRegistry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			WriteError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed", "")
			return
		}
		if registry == nil {
			WriteError(w, http.StatusNotFound, "not_found_error", "operational evidence is disabled", "")
			return
		}
		id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/debug/evidence/"), "/")
		decodedID, err := url.PathUnescape(id)
		if err != nil {
			WriteError(w, http.StatusBadRequest, "invalid_request_error", "invalid evidence id", "id")
			return
		}
		detail, ok := registry.Detail(decodedID, time.Now())
		if !ok {
			WriteError(w, http.StatusNotFound, "not_found_error", "operational evidence not found", "id")
			return
		}
		WriteJSON(w, http.StatusOK, detail)
	}
}

func evidenceSourceInfos() []EvidenceSourceInfo {
	out := make([]EvidenceSourceInfo, 0, len(evidenceSources))
	for _, source := range evidenceSources {
		out = append(out, EvidenceSourceInfo{Kind: source.kind, Title: source.title, Glob: source.glob})
	}
	return out
}

func latestEvidenceByKind(records []EvidenceRecord) []EvidenceRecord {
	seen := make(map[string]bool, len(evidenceSources))
	out := make([]EvidenceRecord, 0, len(evidenceSources))
	for _, record := range records {
		if seen[record.Kind] {
			continue
		}
		seen[record.Kind] = true
		out = append(out, record)
	}
	return out
}

func evidenceGeneratedAt(summary map[string]any, summaryPath string, modifiedAt time.Time) time.Time {
	if generated := parseEvidenceTime(evidenceString(summary, "generated_at")); !generated.IsZero() {
		return generated
	}
	if started := parseEvidenceTime(evidenceString(summary, "started_at")); !started.IsZero() {
		return started
	}
	if parsed := evidenceTimestampFromPath(summaryPath); !parsed.IsZero() {
		return parsed
	}
	return modifiedAt
}

func parseEvidenceTime(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	if parsed, err := time.Parse(evidenceGeneratedAtNanoLayout, value); err == nil {
		return parsed
	}
	if parsed, err := time.Parse(evidenceGeneratedAtLayout, value); err == nil {
		return parsed
	}
	return time.Time{}
}

func evidenceTimestampFromPath(path string) time.Time {
	match := evidenceTimestampPattern.FindString(filepath.ToSlash(path))
	if match == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(evidenceTimestampLayout, match)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func evidenceString(summary map[string]any, key string) string {
	if summary == nil {
		return ""
	}
	value, ok := summary[key]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return typed.String()
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

func evidenceModel(summary map[string]any, artifactDir string) string {
	for _, key := range []string{"model", "provider_model", "public_model"} {
		if value := evidenceString(summary, key); value != "" {
			return value
		}
	}
	base := filepath.Base(artifactDir)
	if before, _, ok := strings.Cut(base, "_full_"); ok {
		return before
	}
	return ""
}

func evidenceMetrics(summary map[string]any) map[string]any {
	if summary == nil {
		return nil
	}
	metrics := make(map[string]any)
	for _, key := range []string{"totals", "counts", "statuses", "settings"} {
		if value, ok := summary[key]; ok && value != nil {
			metrics[key] = value
		}
	}
	if len(metrics) == 0 {
		return nil
	}
	return metrics
}

func lenEvidenceArray(summary map[string]any, key string) int {
	if summary == nil {
		return 0
	}
	items, ok := summary[key].([]any)
	if !ok {
		return 0
	}
	return len(items)
}

func evidenceSummaryMarkdownPath(summaryPath string) string {
	path := strings.TrimSuffix(summaryPath, ".json") + ".md"
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	return slashPath(path)
}

func evidenceTitle(prefix string, artifactDir string) string {
	base := strings.TrimSpace(filepath.Base(artifactDir))
	if base == "" || base == "." {
		return prefix
	}
	return prefix + " " + base
}

func evidenceID(kind string, artifactDir string) string {
	return kind + ":" + filepath.Base(filepath.Clean(artifactDir))
}

func slashPath(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	return filepath.ToSlash(filepath.Clean(path))
}

func formatEvidenceTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
