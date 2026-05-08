package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"llama_shim/internal/config"
)

var defaultProviderDoctorMatrixModels = []string{
	"deepseek/deepseek-v4-pro",
	"xiaomi/mimo-v2.5-pro",
	"svgun/kimi-k2.6",
	"svgun/qwen-3.6",
}

type providerDoctorOptions struct {
	ArtifactDir         string
	StrictEnv           bool
	RequireMatrix       bool
	StrictCodexMetadata bool
	MatrixModels        []string
	GeneratedAt         time.Time
}

type providerDoctorReport struct {
	Object             string                         `json:"object"`
	Status             string                         `json:"status"`
	GeneratedAt        string                         `json:"generated_at"`
	ConfigFile         string                         `json:"config_file,omitempty"`
	ArtifactDir        string                         `json:"artifact_dir,omitempty"`
	Options            providerDoctorReportOptions    `json:"options"`
	Summary            providerDoctorSummary          `json:"summary"`
	Providers          []providerDoctorProviderReport `json:"providers"`
	Models             []providerDoctorModelReport    `json:"models"`
	CompatibilityRules []providerDoctorRuleReport     `json:"compatibility_rules"`
	Issues             []providerDoctorIssue          `json:"issues"`
}

type providerDoctorReportOptions struct {
	StrictEnv           bool     `json:"strict_env"`
	RequireMatrix       bool     `json:"require_matrix"`
	StrictCodexMetadata bool     `json:"strict_codex_metadata"`
	MatrixModels        []string `json:"matrix_models"`
}

type providerDoctorSummary struct {
	ProviderCount int `json:"provider_count"`
	ModelCount    int `json:"model_count"`
	RuleCount     int `json:"rule_count"`
	Errors        int `json:"errors"`
	Warnings      int `json:"warnings"`
	Info          int `json:"info"`
}

type providerDoctorProviderReport struct {
	ID                 string `json:"id"`
	BaseURL            string `json:"base_url"`
	BaseURLLocal       bool   `json:"base_url_local"`
	BearerTokenEnv     string `json:"bearer_token_env,omitempty"`
	BearerTokenPresent bool   `json:"bearer_token_present"`
	ModelCount         int    `json:"model_count"`
}

type providerDoctorModelReport struct {
	PublicModel      string `json:"public_model"`
	ProviderID       string `json:"provider_id"`
	ProviderModel    string `json:"provider_model"`
	UpstreamModel    string `json:"upstream_model"`
	MatrixModel      bool   `json:"matrix_model"`
	HasCodexMetadata bool   `json:"has_codex_metadata"`
}

type providerDoctorRuleReport struct {
	RuleSet    string `json:"rule_set"`
	Model      string `json:"model"`
	TargetKind string `json:"target_kind"`
	Detail     string `json:"detail,omitempty"`
}

type providerDoctorIssue struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Message  string `json:"message"`
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	RuleSet  string `json:"rule_set,omitempty"`
}

type providerDoctorCatalog struct {
	aliases         map[string]string
	upstreams       map[string]struct{}
	metadata        map[string]config.ResponsesCodexModelMetadata
	matrix          map[string]struct{}
	duplicateMetas  map[string]int
	configuredModel []providerDoctorModelReport
}

func runProvider(cfg config.ShimctlConfig, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		printProviderUsage(stderr)
		return errors.New("provider requires a command")
	}
	switch args[0] {
	case "doctor":
		return runProviderDoctor(cfg, args[1:], stdout, stderr)
	default:
		printProviderUsage(stderr)
		return fmt.Errorf("unknown provider command %q", args[0])
	}
}

func runProviderDoctor(cfg config.ShimctlConfig, args []string, stdout, stderr io.Writer) error {
	opts := providerDoctorOptions{
		MatrixModels: append([]string(nil), defaultProviderDoctorMatrixModels...),
		GeneratedAt:  time.Now().UTC(),
	}
	fs := flag.NewFlagSet("provider doctor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	outDir := fs.String("out", "", "artifact directory for provider config doctor report")
	matrixModels := fs.String("matrix-models", strings.Join(opts.MatrixModels, " "), "comma or space separated provider/model aliases expected by the operator matrix")
	fs.BoolVar(&opts.StrictEnv, "strict-env", false, "treat missing configured provider token env values as errors")
	fs.BoolVar(&opts.RequireMatrix, "require-matrix", false, "treat missing operator-matrix provider aliases as errors")
	fs.BoolVar(&opts.StrictCodexMetadata, "strict-codex-metadata", false, "treat missing or incomplete Codex metadata for matrix aliases as errors")
	if err := fs.Parse(args); err != nil {
		return err
	}
	opts.MatrixModels = splitProviderDoctorModels(*matrixModels)
	if len(opts.MatrixModels) == 0 {
		return errors.New("provider doctor requires at least one -matrix-models entry")
	}
	if strings.TrimSpace(*outDir) == "" {
		*outDir = filepath.Join(".tmp", "v4-provider-config-doctor", opts.GeneratedAt.Format("20060102T150405Z"))
	}
	opts.ArtifactDir = *outDir

	report := diagnoseProviderConfig(cfg, opts)
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		return fmt.Errorf("create provider doctor artifact dir: %w", err)
	}
	if err := writeProviderDoctorReportFiles(*outDir, report); err != nil {
		return err
	}

	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		return fmt.Errorf("encode provider doctor report: %w", err)
	}
	if report.Status != "passed" {
		return fmt.Errorf("provider doctor failed; artifacts: %s", *outDir)
	}
	return nil
}

func diagnoseProviderConfig(cfg config.ShimctlConfig, opts providerDoctorOptions) providerDoctorReport {
	if opts.GeneratedAt.IsZero() {
		opts.GeneratedAt = time.Now().UTC()
	}
	if len(opts.MatrixModels) == 0 {
		opts.MatrixModels = append([]string(nil), defaultProviderDoctorMatrixModels...)
	}
	report := providerDoctorReport{
		Object:      "shimctl.provider.doctor",
		Status:      "passed",
		GeneratedAt: opts.GeneratedAt.Format(time.RFC3339Nano),
		ConfigFile:  cfg.ConfigFile,
		ArtifactDir: opts.ArtifactDir,
		Options: providerDoctorReportOptions{
			StrictEnv:           opts.StrictEnv,
			RequireMatrix:       opts.RequireMatrix,
			StrictCodexMetadata: opts.StrictCodexMetadata,
			MatrixModels:        append([]string(nil), opts.MatrixModels...),
		},
		Providers:          []providerDoctorProviderReport{},
		Models:             []providerDoctorModelReport{},
		CompatibilityRules: []providerDoctorRuleReport{},
		Issues:             []providerDoctorIssue{},
	}
	catalog := buildProviderDoctorCatalog(cfg, opts.MatrixModels)
	report.Summary.ProviderCount = len(cfg.LlamaProviders)
	report.Summary.ModelCount = len(catalog.configuredModel)
	report.Models = append(report.Models, catalog.configuredModel...)

	if len(cfg.LlamaProviders) == 0 {
		report.addIssue(providerDoctorIssue{
			Severity: "warning",
			Code:     "provider_routing_not_configured",
			Message:  "llama.providers is empty; provider/model aliases are unavailable until configured",
		})
	}

	for _, provider := range cfg.LlamaProviders {
		validURL, localURL := validateProviderBaseURL(provider.BaseURL)
		report.Providers = append(report.Providers, providerDoctorProviderReport{
			ID:                 provider.ID,
			BaseURL:            provider.BaseURL,
			BaseURLLocal:       localURL,
			BearerTokenEnv:     provider.BearerTokenEnv,
			BearerTokenPresent: strings.TrimSpace(provider.BearerToken) != "",
			ModelCount:         len(provider.Models),
		})
		if !validURL {
			report.addIssue(providerDoctorIssue{
				Severity: "error",
				Code:     "provider_base_url_invalid",
				Provider: provider.ID,
				Message:  fmt.Sprintf("provider %q base_url must be an absolute URL with scheme and host", provider.ID),
			})
		}
		if provider.BearerTokenEnv == "" {
			severity := "info"
			if !localURL {
				severity = "warning"
			}
			if opts.StrictEnv && !localURL {
				severity = "error"
			}
			report.addIssue(providerDoctorIssue{
				Severity: severity,
				Code:     "provider_token_env_missing",
				Provider: provider.ID,
				Message:  fmt.Sprintf("provider %q does not configure bearer_token_env; this is expected only for local unauthenticated providers", provider.ID),
			})
		} else if opts.StrictEnv && strings.TrimSpace(provider.BearerToken) == "" {
			report.addIssue(providerDoctorIssue{
				Severity: "error",
				Code:     "provider_token_env_unset",
				Provider: provider.ID,
				Message:  fmt.Sprintf("provider %q bearer_token_env %q is configured but empty in the current environment", provider.ID, provider.BearerTokenEnv),
			})
		}
	}

	for _, model := range opts.MatrixModels {
		if _, ok := catalog.aliases[model]; !ok {
			severity := "warning"
			if opts.RequireMatrix {
				severity = "error"
			}
			report.addIssue(providerDoctorIssue{
				Severity: severity,
				Code:     "matrix_model_missing",
				Model:    model,
				Message:  fmt.Sprintf("operator matrix model %q is not configured in llama.providers", model),
			})
		}
	}

	checkCodexMetadata(cfg, opts, catalog, &report)
	checkProviderCompatibilityRules(cfg, catalog, &report)

	sort.SliceStable(report.Issues, func(i, j int) bool {
		if providerDoctorSeverityRank(report.Issues[i].Severity) != providerDoctorSeverityRank(report.Issues[j].Severity) {
			return providerDoctorSeverityRank(report.Issues[i].Severity) < providerDoctorSeverityRank(report.Issues[j].Severity)
		}
		if report.Issues[i].Code != report.Issues[j].Code {
			return report.Issues[i].Code < report.Issues[j].Code
		}
		return report.Issues[i].Message < report.Issues[j].Message
	})
	if report.Summary.Errors > 0 {
		report.Status = "failed"
	}
	return report
}

func buildProviderDoctorCatalog(cfg config.ShimctlConfig, matrixModels []string) providerDoctorCatalog {
	catalog := providerDoctorCatalog{
		aliases:        make(map[string]string),
		upstreams:      make(map[string]struct{}),
		metadata:       make(map[string]config.ResponsesCodexModelMetadata),
		matrix:         make(map[string]struct{}),
		duplicateMetas: make(map[string]int),
	}
	for _, model := range matrixModels {
		if model = strings.TrimSpace(model); model != "" {
			catalog.matrix[model] = struct{}{}
		}
	}
	seenMetadata := make(map[string]int, len(cfg.ResponsesCodexModelMetadata))
	for _, metadata := range cfg.ResponsesCodexModelMetadata {
		model := strings.TrimSpace(metadata.Model)
		if model == "" {
			continue
		}
		seenMetadata[model]++
		catalog.metadata[model] = metadata
	}
	for model, count := range seenMetadata {
		if count > 1 {
			catalog.duplicateMetas[model] = count
		}
	}
	for _, provider := range cfg.LlamaProviders {
		for _, model := range provider.Models {
			publicModel := provider.ID + "/" + model.Model
			upstreamModel := strings.TrimSpace(model.UpstreamModel)
			if upstreamModel == "" {
				upstreamModel = model.Model
			}
			catalog.aliases[publicModel] = upstreamModel
			catalog.upstreams[upstreamModel] = struct{}{}
			_, inMatrix := catalog.matrix[publicModel]
			_, hasMetadata := catalog.metadata[publicModel]
			catalog.configuredModel = append(catalog.configuredModel, providerDoctorModelReport{
				PublicModel:      publicModel,
				ProviderID:       provider.ID,
				ProviderModel:    model.Model,
				UpstreamModel:    upstreamModel,
				MatrixModel:      inMatrix,
				HasCodexMetadata: hasMetadata,
			})
		}
	}
	sort.Slice(catalog.configuredModel, func(i, j int) bool {
		return catalog.configuredModel[i].PublicModel < catalog.configuredModel[j].PublicModel
	})
	return catalog
}

func checkCodexMetadata(cfg config.ShimctlConfig, opts providerDoctorOptions, catalog providerDoctorCatalog, report *providerDoctorReport) {
	for model, count := range catalog.duplicateMetas {
		report.addIssue(providerDoctorIssue{
			Severity: "error",
			Code:     "codex_metadata_duplicate",
			Model:    model,
			Message:  fmt.Sprintf("responses.codex.model_metadata contains %d entries for %q", count, model),
		})
	}
	for _, metadata := range cfg.ResponsesCodexModelMetadata {
		model := strings.TrimSpace(metadata.Model)
		if model == "" {
			continue
		}
		_, configuredAlias := catalog.aliases[model]
		if strings.Contains(model, "/") && !configuredAlias {
			report.addIssue(providerDoctorIssue{
				Severity: "warning",
				Code:     "codex_metadata_unknown_alias",
				Model:    model,
				Message:  fmt.Sprintf("Codex metadata model %q is not configured as a public provider/model alias", model),
			})
		}
		if _, inMatrix := catalog.matrix[model]; inMatrix {
			addCodexMetadataCompletenessIssues(metadata, opts.StrictCodexMetadata, report)
		}
	}
	if !(cfg.ResponsesCodexEnableCompatibility || len(cfg.ResponsesCodexModelMetadata) > 0 || opts.StrictCodexMetadata) {
		return
	}
	for _, model := range catalog.configuredModel {
		if model.HasCodexMetadata {
			continue
		}
		if !model.MatrixModel && !opts.StrictCodexMetadata {
			continue
		}
		severity := "warning"
		if opts.StrictCodexMetadata {
			severity = "error"
		}
		report.addIssue(providerDoctorIssue{
			Severity: severity,
			Code:     "codex_metadata_missing",
			Model:    model.PublicModel,
			Provider: model.ProviderID,
			Message:  fmt.Sprintf("public alias %q has no responses.codex.model_metadata entry", model.PublicModel),
		})
	}
}

func addCodexMetadataCompletenessIssues(metadata config.ResponsesCodexModelMetadata, strict bool, report *providerDoctorReport) {
	severity := "warning"
	if strict {
		severity = "error"
	}
	if metadata.ContextWindow <= 0 {
		report.addIssue(providerDoctorIssue{
			Severity: severity,
			Code:     "codex_metadata_context_window_missing",
			Model:    metadata.Model,
			Message:  fmt.Sprintf("Codex metadata for %q should set context_window for Codex config generation", metadata.Model),
		})
	}
	if metadata.MaxContextWindow <= 0 {
		report.addIssue(providerDoctorIssue{
			Severity: severity,
			Code:     "codex_metadata_max_context_window_missing",
			Model:    metadata.Model,
			Message:  fmt.Sprintf("Codex metadata for %q should set max_context_window for Codex config generation", metadata.Model),
		})
	}
	if strings.TrimSpace(metadata.ShellType) == "" || metadata.ShellType == "disabled" {
		report.addIssue(providerDoctorIssue{
			Severity: severity,
			Code:     "codex_metadata_shell_tool_missing",
			Model:    metadata.Model,
			Message:  fmt.Sprintf("Codex metadata for %q should declare a shell tool type for Codex task profiles", metadata.Model),
		})
	}
	if strings.TrimSpace(metadata.ApplyPatchToolType) == "" {
		report.addIssue(providerDoctorIssue{
			Severity: severity,
			Code:     "codex_metadata_apply_patch_missing",
			Model:    metadata.Model,
			Message:  fmt.Sprintf("Codex metadata for %q should declare apply_patch_tool_type for patch profiles", metadata.Model),
		})
	}
}

func checkProviderCompatibilityRules(cfg config.ShimctlConfig, catalog providerDoctorCatalog, report *providerDoctorReport) {
	for _, rule := range cfg.ChatCompletionsUpstreamCompatibility {
		addCompatibilityRuleReport("chat_completions.upstream_compatibility", rule.Model, catalog, report)
	}
	for _, rule := range cfg.ResponsesUpstreamToolCompatibility {
		addCompatibilityRuleReport("responses.upstream_tool_compatibility", rule.Model, catalog, report)
	}
	for _, rule := range cfg.ResponsesCodexUpstreamInputCompatibility {
		addCompatibilityRuleReport("responses.codex.upstream_input_compatibility", rule.Model, catalog, report)
	}
}

func addCompatibilityRuleReport(ruleSet string, model string, catalog providerDoctorCatalog, report *providerDoctorReport) {
	targetKind, detail := classifyCompatibilityRuleTarget(model, catalog)
	report.CompatibilityRules = append(report.CompatibilityRules, providerDoctorRuleReport{
		RuleSet:    ruleSet,
		Model:      model,
		TargetKind: targetKind,
		Detail:     detail,
	})
	report.Summary.RuleCount++
	switch targetKind {
	case "public_alias":
		report.addIssue(providerDoctorIssue{
			Severity: "error",
			Code:     "compatibility_rule_uses_public_alias",
			Model:    model,
			RuleSet:  ruleSet,
			Message:  fmt.Sprintf("%s rule for %q targets the public provider/model alias; provider-routed cleanup rules must target the resolved upstream_model", ruleSet, model),
		})
	case "provider_alias_pattern":
		report.addIssue(providerDoctorIssue{
			Severity: "warning",
			Code:     "compatibility_rule_uses_provider_alias_pattern",
			Model:    model,
			RuleSet:  ruleSet,
			Message:  fmt.Sprintf("%s rule pattern %q contains a provider slash; provider-routed cleanup rules should match upstream_model patterns", ruleSet, model),
		})
	case "unmatched_exact":
		report.addIssue(providerDoctorIssue{
			Severity: "warning",
			Code:     "compatibility_rule_unmatched",
			Model:    model,
			RuleSet:  ruleSet,
			Message:  fmt.Sprintf("%s rule for %q does not match a configured upstream_model; it may be stale or intended for direct non-routed model ids", ruleSet, model),
		})
	}
}

func classifyCompatibilityRuleTarget(model string, catalog providerDoctorCatalog) (string, string) {
	model = strings.TrimSpace(model)
	if model == "" {
		return "empty", ""
	}
	if strings.ContainsAny(model, "*?") {
		if strings.Contains(model, "/") {
			return "provider_alias_pattern", "pattern contains provider/model separator"
		}
		return "upstream_model_pattern", "pattern is checked against resolved upstream_model values"
	}
	if upstream, ok := catalog.aliases[model]; ok {
		return "public_alias", fmt.Sprintf("public alias resolves to upstream_model %q", upstream)
	}
	if _, ok := catalog.upstreams[model]; ok {
		return "resolved_upstream_model", "matches a configured upstream_model"
	}
	return "unmatched_exact", "does not match a configured upstream_model"
}

func validateProviderBaseURL(value string) (bool, bool) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return false, false
	}
	host := strings.ToLower(parsed.Hostname())
	return true, host == "localhost" || host == "127.0.0.1" || host == "::1" || strings.HasSuffix(host, ".local")
}

func (r *providerDoctorReport) addIssue(issue providerDoctorIssue) {
	if issue.Severity == "" || issue.Code == "" || issue.Message == "" {
		return
	}
	r.Issues = append(r.Issues, issue)
	switch issue.Severity {
	case "error":
		r.Summary.Errors++
	case "warning":
		r.Summary.Warnings++
	default:
		r.Summary.Info++
	}
}

func providerDoctorSeverityRank(value string) int {
	switch value {
	case "error":
		return 0
	case "warning":
		return 1
	default:
		return 2
	}
}

func splitProviderDoctorModels(value string) []string {
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r' || r == '\t' || r == ' '
	})
	out := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if _, ok := seen[part]; ok {
			continue
		}
		seen[part] = struct{}{}
		out = append(out, part)
	}
	return out
}

func writeProviderDoctorReportFiles(outDir string, report providerDoctorReport) error {
	if err := writeJSONFile(filepath.Join(outDir, "summary.json"), report); err != nil {
		return err
	}
	var md strings.Builder
	fmt.Fprintf(&md, "# V4 Provider Config Doctor\n\n")
	fmt.Fprintf(&md, "- Status: `%s`\n", report.Status)
	if report.ConfigFile != "" {
		fmt.Fprintf(&md, "- Config: `%s`\n", report.ConfigFile)
	}
	fmt.Fprintf(&md, "- Providers: `%d`\n", report.Summary.ProviderCount)
	fmt.Fprintf(&md, "- Models: `%d`\n", report.Summary.ModelCount)
	fmt.Fprintf(&md, "- Compatibility rules: `%d`\n", report.Summary.RuleCount)
	fmt.Fprintf(&md, "- Issues: `%d` errors, `%d` warnings, `%d` info\n", report.Summary.Errors, report.Summary.Warnings, report.Summary.Info)

	fmt.Fprintf(&md, "\n## Providers\n\n")
	if len(report.Providers) == 0 {
		fmt.Fprintf(&md, "- none configured\n")
	} else {
		for _, provider := range report.Providers {
			token := "none"
			if provider.BearerTokenEnv != "" {
				token = fmt.Sprintf("%s present=%t", provider.BearerTokenEnv, provider.BearerTokenPresent)
			}
			fmt.Fprintf(&md, "- `%s`: `%s`, models=%d, token=%s\n", provider.ID, provider.BaseURL, provider.ModelCount, token)
		}
	}

	fmt.Fprintf(&md, "\n## Models\n\n")
	if len(report.Models) == 0 {
		fmt.Fprintf(&md, "- none configured\n")
	} else {
		for _, model := range report.Models {
			fmt.Fprintf(&md, "- `%s` -> `%s`", model.PublicModel, model.UpstreamModel)
			if model.MatrixModel {
				fmt.Fprintf(&md, " matrix=true")
			}
			if model.HasCodexMetadata {
				fmt.Fprintf(&md, " codex_metadata=true")
			}
			fmt.Fprintf(&md, "\n")
		}
	}

	fmt.Fprintf(&md, "\n## Compatibility Rules\n\n")
	if len(report.CompatibilityRules) == 0 {
		fmt.Fprintf(&md, "- none configured\n")
	} else {
		for _, rule := range report.CompatibilityRules {
			fmt.Fprintf(&md, "- `%s` model `%s`: `%s`", rule.RuleSet, rule.Model, rule.TargetKind)
			if rule.Detail != "" {
				fmt.Fprintf(&md, " - %s", rule.Detail)
			}
			fmt.Fprintf(&md, "\n")
		}
	}

	fmt.Fprintf(&md, "\n## Issues\n\n")
	if len(report.Issues) == 0 {
		fmt.Fprintf(&md, "- none\n")
	} else {
		for _, issue := range report.Issues {
			fmt.Fprintf(&md, "- `%s` `%s`", issue.Severity, issue.Code)
			if issue.Provider != "" {
				fmt.Fprintf(&md, " provider=`%s`", issue.Provider)
			}
			if issue.Model != "" {
				fmt.Fprintf(&md, " model=`%s`", issue.Model)
			}
			if issue.RuleSet != "" {
				fmt.Fprintf(&md, " rule_set=`%s`", issue.RuleSet)
			}
			fmt.Fprintf(&md, ": %s\n", issue.Message)
		}
	}
	if err := os.WriteFile(filepath.Join(outDir, "summary.md"), []byte(md.String()), 0o644); err != nil {
		return fmt.Errorf("write provider doctor summary: %w", err)
	}
	return nil
}

func printProviderUsage(w io.Writer) {
	_, _ = fmt.Fprintln(w, "usage: shimctl provider <doctor> [flags]")
}
