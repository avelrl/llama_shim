package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"llama_shim/internal/config"
)

type codexConfigOptions struct {
	Model                     string
	Provider                  string
	ProviderName              string
	BaseURL                   string
	APIKeyEnv                 string
	ReasoningEffort           string
	ReasoningSummary          string
	Verbosity                 string
	SupportsWebSockets        bool
	RequestMaxRetries         int
	StreamMaxRetries          int
	StreamIdleTimeoutMS       int
	WebSocketConnectTimeoutMS int
	ModelContextWindow        int
	ModelMaxOutputTokens      int
	ApplyPatchFreeform        bool
	UnifiedExec               bool
}

type codexDoctorReport struct {
	Object        string             `json:"object"`
	Status        string             `json:"status"`
	Model         string             `json:"model"`
	Provider      string             `json:"provider"`
	BaseURL       string             `json:"base_url"`
	APIKeyEnv     string             `json:"api_key_env"`
	APIKeyPresent bool               `json:"api_key_present"`
	CodexBin      string             `json:"codex_bin"`
	CodexVersion  string             `json:"codex_version,omitempty"`
	ArtifactDir   string             `json:"artifact_dir,omitempty"`
	Checks        []codexDoctorCheck `json:"checks"`
	Commands      map[string]string  `json:"commands"`
}

type codexDoctorCheck struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	HTTPStatus int    `json:"http_status,omitempty"`
	Detail     string `json:"detail,omitempty"`
	Path       string `json:"path,omitempty"`
}

func runCodex(cfg config.ShimctlConfig, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		printCodexUsage(stderr)
		return errors.New("codex requires a command")
	}
	switch args[0] {
	case "config":
		return runCodexConfig(cfg, args[1:], stdout, stderr)
	case "doctor":
		return runCodexDoctor(cfg, args[1:], stdout, stderr)
	default:
		printCodexUsage(stderr)
		return fmt.Errorf("unknown codex command %q", args[0])
	}
}

func runCodexConfig(cfg config.ShimctlConfig, args []string, stdout, stderr io.Writer) error {
	opts := defaultCodexConfigOptions(cfg)
	fs := flag.NewFlagSet("codex config", flag.ContinueOnError)
	fs.SetOutput(stderr)
	bindCodexConfigFlags(fs, &opts)
	outPath := fs.String("out", "", "write config.toml to this path instead of stdout")
	codexHome := fs.String("codex-home", "", "write config.toml under this CODEX_HOME directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := validateCodexConfigOptions(opts); err != nil {
		return err
	}
	if strings.TrimSpace(*outPath) != "" && strings.TrimSpace(*codexHome) != "" {
		return errors.New("codex config cannot combine -out and -codex-home")
	}

	toml := renderCodexConfigTOML(opts)
	switch {
	case strings.TrimSpace(*codexHome) != "":
		path := filepath.Join(*codexHome, "config.toml")
		if err := writeCodexConfigFile(path, toml); err != nil {
			return err
		}
		_, err := fmt.Fprintf(stdout, "codex config written: %s\n", path)
		return err
	case strings.TrimSpace(*outPath) != "":
		if err := writeCodexConfigFile(*outPath, toml); err != nil {
			return err
		}
		_, err := fmt.Fprintf(stdout, "codex config written: %s\n", *outPath)
		return err
	default:
		_, err := io.WriteString(stdout, toml)
		return err
	}
}

func runCodexDoctor(cfg config.ShimctlConfig, args []string, stdout, stderr io.Writer) error {
	opts := defaultCodexConfigOptions(cfg)
	codexBinDefault := envStringFirst("CODEX_BIN")
	if codexBinDefault == "" {
		codexBinDefault = "codex"
	}

	fs := flag.NewFlagSet("codex doctor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	bindCodexConfigFlags(fs, &opts)
	codexBin := fs.String("codex-bin", codexBinDefault, "Codex CLI binary to inspect")
	outDir := fs.String("out", "", "artifact directory for generated config and probe report")
	timeout := fs.Duration("timeout", 20*time.Second, "per HTTP probe timeout")
	skipDirectResponse := fs.Bool("skip-direct-response", false, "skip POST /v1/responses direct smoke")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := validateCodexConfigOptions(opts); err != nil {
		return err
	}
	if *timeout <= 0 {
		return errors.New("codex doctor requires -timeout > 0")
	}
	if strings.TrimSpace(*outDir) == "" {
		*outDir = filepath.Join(".tmp", "shimctl-codex", slugifyCodexValue(opts.Model)+"_"+time.Now().UTC().Format("20060102T150405Z"))
	}
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		return fmt.Errorf("create codex doctor artifact dir: %w", err)
	}

	report := codexDoctorReport{
		Object:        "shimctl.codex.doctor",
		Status:        "passed",
		Model:         opts.Model,
		Provider:      opts.Provider,
		BaseURL:       opts.BaseURL,
		APIKeyEnv:     opts.APIKeyEnv,
		APIKeyPresent: strings.TrimSpace(os.Getenv(opts.APIKeyEnv)) != "",
		CodexBin:      *codexBin,
		ArtifactDir:   *outDir,
		Commands:      codexDoctorCommands(opts),
	}
	configPath := filepath.Join(*outDir, "codex-home", "config.toml")
	if err := writeCodexConfigFile(configPath, renderCodexConfigTOML(opts)); err != nil {
		return err
	}
	if err := writeCodexEnvFile(filepath.Join(*outDir, "env.sh"), opts); err != nil {
		return err
	}

	report.addCheck(checkCodexBinary(context.Background(), *codexBin))
	if len(report.Checks) > 0 {
		report.CodexVersion = report.Checks[len(report.Checks)-1].Detail
	}
	if report.APIKeyPresent {
		report.addCheck(codexDoctorCheck{Name: "api_key_env", Status: "passed", Detail: opts.APIKeyEnv + " is present"})
	} else {
		report.addCheck(codexDoctorCheck{Name: "api_key_env", Status: "failed", Detail: opts.APIKeyEnv + " is empty or unset"})
	}

	client := &http.Client{Timeout: *timeout}
	shimBaseURL := shimBaseURLFromCodexBaseURL(opts.BaseURL)
	authToken := strings.TrimSpace(os.Getenv(opts.APIKeyEnv))
	report.addCheck(runCodexGETProbe(client, shimBaseURL+"/healthz", authToken, "healthz"))
	report.addCheck(runCodexGETProbe(client, shimBaseURL+"/readyz", authToken, "readyz"))
	capabilitiesCheck, capabilitiesPayload := runCodexJSONGETProbe(client, shimBaseURL+"/debug/capabilities", authToken, "capabilities")
	report.addCheck(capabilitiesCheck)
	report.addCheck(checkCodexCapabilities(capabilitiesPayload, opts.Model))
	modelsCheck, modelsPayload := runCodexJSONGETProbe(client, shimBaseURL+"/v1/models", authToken, "models")
	report.addCheck(modelsCheck)
	report.addCheck(checkCodexModelCatalog(modelsPayload, opts.Model))
	if !*skipDirectResponse {
		report.addCheck(runCodexResponsesSmoke(client, shimBaseURL+"/v1/responses", authToken, opts.Model))
	}

	if report.hasFailures() {
		report.Status = "failed"
	}
	if err := writeCodexDoctorReportFiles(*outDir, report); err != nil {
		return err
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		return fmt.Errorf("encode codex doctor report: %w", err)
	}
	if report.Status != "passed" {
		return fmt.Errorf("codex doctor failed; artifacts: %s", *outDir)
	}
	return nil
}

func bindCodexConfigFlags(fs *flag.FlagSet, opts *codexConfigOptions) {
	fs.StringVar(&opts.Model, "model", opts.Model, "Codex model id sent to the shim")
	fs.StringVar(&opts.Provider, "provider", opts.Provider, "Codex model provider id")
	fs.StringVar(&opts.ProviderName, "provider-name", opts.ProviderName, "human-readable Codex provider name")
	fs.StringVar(&opts.BaseURL, "base-url", opts.BaseURL, "Codex provider base URL, normally the shim /v1 URL")
	fs.StringVar(&opts.APIKeyEnv, "api-key-env", opts.APIKeyEnv, "environment variable Codex uses for shim bearer auth")
	fs.StringVar(&opts.ReasoningEffort, "reasoning-effort", opts.ReasoningEffort, "Codex reasoning effort")
	fs.StringVar(&opts.ReasoningSummary, "reasoning-summary", opts.ReasoningSummary, "Codex reasoning summary mode")
	fs.StringVar(&opts.Verbosity, "verbosity", opts.Verbosity, "optional Codex model verbosity")
	fs.BoolVar(&opts.SupportsWebSockets, "supports-websockets", opts.SupportsWebSockets, "enable Codex Responses WebSocket transport for this provider")
	fs.IntVar(&opts.RequestMaxRetries, "request-max-retries", opts.RequestMaxRetries, "Codex provider HTTP retry count")
	fs.IntVar(&opts.StreamMaxRetries, "stream-max-retries", opts.StreamMaxRetries, "Codex provider stream retry count")
	fs.IntVar(&opts.StreamIdleTimeoutMS, "stream-idle-timeout-ms", opts.StreamIdleTimeoutMS, "Codex provider stream idle timeout in milliseconds")
	fs.IntVar(&opts.WebSocketConnectTimeoutMS, "websocket-connect-timeout-ms", opts.WebSocketConnectTimeoutMS, "Codex provider WebSocket connect timeout in milliseconds")
	fs.IntVar(&opts.ModelContextWindow, "model-context-window", opts.ModelContextWindow, "optional model context window tokens; 0 omits it")
	fs.IntVar(&opts.ModelMaxOutputTokens, "model-max-output-tokens", opts.ModelMaxOutputTokens, "optional model max output tokens; 0 omits it")
	fs.BoolVar(&opts.ApplyPatchFreeform, "apply-patch-freeform", opts.ApplyPatchFreeform, "enable Codex apply_patch freeform feature")
	fs.BoolVar(&opts.UnifiedExec, "unified-exec", opts.UnifiedExec, "enable Codex unified exec feature")
}

func defaultCodexConfigOptions(cfg config.ShimctlConfig) codexConfigOptions {
	shimBaseURL := envStringFirst("SHIM_BASE_URL")
	if shimBaseURL == "" {
		shimBaseURL = "http://127.0.0.1:8080"
	}
	baseURL := envStringFirst("CODEX_BASE_URL")
	if baseURL == "" {
		baseURL = strings.TrimRight(shimBaseURL, "/") + "/v1"
	}
	model := envStringFirst("CODEX_MODEL", "MODEL")
	if model == "" {
		model = strings.TrimSpace(cfg.ProbeModel)
	}
	if model == "" {
		model = "devstack-model"
	}
	apiKeyEnv := envStringFirst("CODEX_API_KEY_ENV")
	if apiKeyEnv == "" {
		apiKeyEnv = "GW_API_KEY"
	}
	provider := envStringFirst("CODEX_PROVIDER")
	if provider == "" {
		provider = "gateway-shim"
	}
	reasoningEffort := envStringFirst("CODEX_EVAL_REASONING_EFFORT")
	if reasoningEffort == "" {
		reasoningEffort = "minimal"
	}
	reasoningSummary := envStringFirst("CODEX_EVAL_REASONING_SUMMARY")
	if reasoningSummary == "" {
		reasoningSummary = "none"
	}
	return codexConfigOptions{
		Model:                     model,
		Provider:                  provider,
		ProviderName:              "llama-shim gateway",
		BaseURL:                   strings.TrimRight(baseURL, "/"),
		APIKeyEnv:                 apiKeyEnv,
		ReasoningEffort:           reasoningEffort,
		ReasoningSummary:          reasoningSummary,
		SupportsWebSockets:        envBool("CODEX_EVAL_WEBSOCKETS", false),
		RequestMaxRetries:         envInt("CODEX_EVAL_REQUEST_MAX_RETRIES", 1),
		StreamMaxRetries:          envInt("CODEX_EVAL_STREAM_MAX_RETRIES", 0),
		StreamIdleTimeoutMS:       envInt("CODEX_EVAL_STREAM_IDLE_TIMEOUT_MS", 180000),
		WebSocketConnectTimeoutMS: 15000,
		ApplyPatchFreeform:        envBool("CODEX_EVAL_APPLY_PATCH_FREEFORM", true),
		UnifiedExec:               envBool("CODEX_EVAL_UNIFIED_EXEC", true),
	}
}

func validateCodexConfigOptions(opts codexConfigOptions) error {
	if strings.TrimSpace(opts.Model) == "" {
		return errors.New("codex config requires -model")
	}
	if strings.TrimSpace(opts.Provider) == "" {
		return errors.New("codex config requires -provider")
	}
	if strings.TrimSpace(opts.BaseURL) == "" {
		return errors.New("codex config requires -base-url")
	}
	if strings.TrimSpace(opts.APIKeyEnv) == "" {
		return errors.New("codex config requires -api-key-env")
	}
	if opts.RequestMaxRetries < 0 || opts.StreamMaxRetries < 0 || opts.StreamIdleTimeoutMS < 0 || opts.WebSocketConnectTimeoutMS < 0 {
		return errors.New("codex config retry and timeout values must be non-negative")
	}
	if opts.ModelContextWindow < 0 || opts.ModelMaxOutputTokens < 0 {
		return errors.New("codex config model token values must be non-negative")
	}
	return nil
}

func renderCodexConfigTOML(opts codexConfigOptions) string {
	var b strings.Builder
	fmt.Fprintf(&b, "model = %s\n", tomlString(opts.Model))
	if opts.ModelContextWindow > 0 {
		fmt.Fprintf(&b, "model_context_window = %d\n", opts.ModelContextWindow)
	}
	if opts.ModelMaxOutputTokens > 0 {
		fmt.Fprintf(&b, "model_max_output_tokens = %d\n", opts.ModelMaxOutputTokens)
	}
	fmt.Fprintf(&b, "model_provider = %s\n", tomlString(opts.Provider))
	fmt.Fprintf(&b, "model_reasoning_effort = %s\n", tomlString(opts.ReasoningEffort))
	fmt.Fprintf(&b, "model_reasoning_summary = %s\n", tomlString(opts.ReasoningSummary))
	if strings.TrimSpace(opts.Verbosity) != "" {
		fmt.Fprintf(&b, "model_verbosity = %s\n", tomlString(opts.Verbosity))
	}
	b.WriteString(`approval_policy = "never"
sandbox_mode = "workspace-write"
web_search = "disabled"

[history]
persistence = "none"

[features]
apps = false
memories = false
multi_agent = false
`)
	fmt.Fprintf(&b, "apply_patch_freeform = %t\n", opts.ApplyPatchFreeform)
	fmt.Fprintf(&b, "unified_exec = %t\n", opts.UnifiedExec)
	b.WriteString(`
[apps._default]
enabled = false
default_tools_enabled = false

`)
	fmt.Fprintf(&b, "[model_providers.%s]\n", opts.Provider)
	fmt.Fprintf(&b, "name = %s\n", tomlString(opts.ProviderName))
	fmt.Fprintf(&b, "base_url = %s\n", tomlString(strings.TrimRight(opts.BaseURL, "/")))
	fmt.Fprintf(&b, "env_key = %s\n", tomlString(opts.APIKeyEnv))
	b.WriteString(`wire_api = "responses"
`)
	fmt.Fprintf(&b, "supports_websockets = %t\n", opts.SupportsWebSockets)
	fmt.Fprintf(&b, "request_max_retries = %d\n", opts.RequestMaxRetries)
	fmt.Fprintf(&b, "stream_max_retries = %d\n", opts.StreamMaxRetries)
	fmt.Fprintf(&b, "stream_idle_timeout_ms = %d\n", opts.StreamIdleTimeoutMS)
	fmt.Fprintf(&b, "websocket_connect_timeout_ms = %d\n", opts.WebSocketConnectTimeoutMS)
	return b.String()
}

func writeCodexConfigFile(path string, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create codex config dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return fmt.Errorf("write codex config %s: %w", path, err)
	}
	return nil
}

func writeCodexEnvFile(path string, opts codexConfigOptions) error {
	var b strings.Builder
	fmt.Fprintf(&b, "export SHIM_BASE_URL=%s\n", shellQuote(shimBaseURLFromCodexBaseURL(opts.BaseURL)))
	fmt.Fprintf(&b, "export CODEX_BASE_URL=%s\n", shellQuote(opts.BaseURL))
	fmt.Fprintf(&b, "export CODEX_PROVIDER=%s\n", shellQuote(opts.Provider))
	fmt.Fprintf(&b, "export CODEX_MODEL=%s\n", shellQuote(opts.Model))
	fmt.Fprintf(&b, "export CODEX_EVAL_MODELS=%s\n", shellQuote(opts.Model))
	fmt.Fprintf(&b, "export CODEX_API_KEY_ENV=%s\n", shellQuote(opts.APIKeyEnv))
	fmt.Fprintf(&b, "export CODEX_EVAL_ATTEMPTS=2\n")
	fmt.Fprintf(&b, "export CODEX_EVAL_NOTIFY=bell\n")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create codex env dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		return fmt.Errorf("write codex env file: %w", err)
	}
	return nil
}

func checkCodexBinary(ctx context.Context, codexBin string) codexDoctorCheck {
	if _, err := exec.LookPath(codexBin); err != nil {
		return codexDoctorCheck{Name: "codex_binary", Status: "failed", Detail: err.Error()}
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(probeCtx, codexBin, "--version")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return codexDoctorCheck{Name: "codex_binary", Status: "failed", Detail: strings.TrimSpace(out.String() + " " + err.Error())}
	}
	return codexDoctorCheck{Name: "codex_binary", Status: "passed", Detail: strings.TrimSpace(out.String())}
}

func runCodexGETProbe(client *http.Client, url string, token string, name string) codexDoctorCheck {
	check, _ := runCodexJSONGETProbe(client, url, token, name)
	return check
}

func runCodexJSONGETProbe(client *http.Client, url string, token string, name string) (codexDoctorCheck, map[string]any) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return codexDoctorCheck{Name: name, Status: "failed", Path: url, Detail: err.Error()}, nil
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return codexDoctorCheck{Name: name, Status: "failed", Path: url, Detail: err.Error()}, nil
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	status := "passed"
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		status = "failed"
	}
	check := codexDoctorCheck{Name: name, Status: status, HTTPStatus: resp.StatusCode, Path: url}
	if status != "passed" {
		check.Detail = trimForCodexDoctor(string(raw), 1024)
		return check, nil
	}
	var payload map[string]any
	if len(bytes.TrimSpace(raw)) > 0 && json.Unmarshal(raw, &payload) != nil {
		return codexDoctorCheck{Name: name, Status: "failed", HTTPStatus: resp.StatusCode, Path: url, Detail: "response is not JSON"}, nil
	}
	return check, payload
}

func checkCodexCapabilities(payload map[string]any, model string) codexDoctorCheck {
	if len(payload) == 0 {
		return codexDoctorCheck{Name: "capabilities_shape", Status: "failed", Detail: "missing capabilities payload"}
	}
	if payload["object"] != "shim.capabilities" {
		return codexDoctorCheck{Name: "capabilities_shape", Status: "failed", Detail: "object is not shim.capabilities"}
	}
	if responsesSurface, ok := nestedMap(payload, "surfaces", "responses"); ok {
		if enabled, _ := responsesSurface["enabled"].(bool); !enabled {
			return codexDoctorCheck{Name: "capabilities_responses", Status: "failed", Detail: "Responses surface is disabled"}
		}
	} else {
		return codexDoctorCheck{Name: "capabilities_responses", Status: "failed", Detail: "missing surfaces.responses"}
	}
	if strings.Contains(model, "/") {
		models := capabilityProviderModels(payload)
		for _, candidate := range models {
			if candidate == model {
				return codexDoctorCheck{Name: "capabilities_provider_model", Status: "passed", Detail: model}
			}
		}
		return codexDoctorCheck{Name: "capabilities_provider_model", Status: "failed", Detail: "provider/model alias is not advertised in /debug/capabilities"}
	}
	return codexDoctorCheck{Name: "capabilities_provider_model", Status: "passed", Detail: "non provider/model model; route catalog check skipped"}
}

func checkCodexModelCatalog(payload map[string]any, model string) codexDoctorCheck {
	data, ok := payload["data"].([]any)
	if !ok {
		return codexDoctorCheck{Name: "models_catalog_shape", Status: "failed", Detail: "missing data array"}
	}
	for _, item := range data {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if entry["id"] == model {
			return codexDoctorCheck{Name: "models_catalog_model", Status: "passed", Detail: model}
		}
	}
	return codexDoctorCheck{Name: "models_catalog_model", Status: "failed", Detail: "model is not listed by /v1/models"}
}

func runCodexResponsesSmoke(client *http.Client, url string, token string, model string) codexDoctorCheck {
	body := fmt.Sprintf(`{"model":%s,"store":false,"input":"Reply with the exact token CODEX_DOCTOR_OK."}`, tomlString(model))
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		return codexDoctorCheck{Name: "responses_smoke", Status: "failed", Path: url, Detail: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return codexDoctorCheck{Name: "responses_smoke", Status: "failed", Path: url, Detail: err.Error()}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	status := "passed"
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		status = "failed"
	}
	check := codexDoctorCheck{Name: "responses_smoke", Status: status, HTTPStatus: resp.StatusCode, Path: url}
	if status != "passed" {
		check.Detail = trimForCodexDoctor(string(raw), 1024)
		return check
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return codexDoctorCheck{Name: "responses_smoke", Status: "failed", HTTPStatus: resp.StatusCode, Path: url, Detail: "response is not JSON"}
	}
	if payload["id"] == nil && payload["object"] != "response" {
		return codexDoctorCheck{Name: "responses_smoke", Status: "failed", HTTPStatus: resp.StatusCode, Path: url, Detail: "unexpected response shape"}
	}
	return check
}

func capabilityProviderModels(payload map[string]any) []string {
	routing, ok := nestedMap(payload, "upstream_provider_routing")
	if !ok {
		routing, _ = nestedMap(payload, "runtime", "upstream_provider_routing")
	}
	if len(routing) == 0 {
		return nil
	}
	providers, _ := routing["providers"].([]any)
	var models []string
	for _, rawProvider := range providers {
		provider, ok := rawProvider.(map[string]any)
		if !ok {
			continue
		}
		rawModels, _ := provider["models"].([]any)
		for _, rawModel := range rawModels {
			if model, ok := rawModel.(string); ok {
				models = append(models, model)
			}
		}
	}
	return models
}

func nestedMap(root map[string]any, path ...string) (map[string]any, bool) {
	current := root
	for _, key := range path {
		next, ok := current[key].(map[string]any)
		if !ok {
			return nil, false
		}
		current = next
	}
	return current, true
}

func (r *codexDoctorReport) addCheck(check codexDoctorCheck) {
	if strings.TrimSpace(check.Name) == "" {
		return
	}
	r.Checks = append(r.Checks, check)
}

func (r codexDoctorReport) hasFailures() bool {
	for _, check := range r.Checks {
		if check.Status == "failed" {
			return true
		}
	}
	return false
}

func writeCodexDoctorReportFiles(outDir string, report codexDoctorReport) error {
	if err := writeJSONFile(filepath.Join(outDir, "summary.json"), report); err != nil {
		return err
	}
	var md strings.Builder
	fmt.Fprintf(&md, "# Codex Config Doctor\n\n")
	fmt.Fprintf(&md, "- Status: `%s`\n", report.Status)
	fmt.Fprintf(&md, "- Model: `%s`\n", report.Model)
	fmt.Fprintf(&md, "- Provider: `%s`\n", report.Provider)
	fmt.Fprintf(&md, "- Base URL: `%s`\n", report.BaseURL)
	fmt.Fprintf(&md, "- API key env: `%s` present=%t\n", report.APIKeyEnv, report.APIKeyPresent)
	if report.CodexVersion != "" {
		fmt.Fprintf(&md, "- Codex version: `%s`\n", report.CodexVersion)
	}
	fmt.Fprintf(&md, "\n## Checks\n\n")
	for _, check := range report.Checks {
		fmt.Fprintf(&md, "- `%s`: `%s`", check.Name, check.Status)
		if check.HTTPStatus != 0 {
			fmt.Fprintf(&md, " HTTP %d", check.HTTPStatus)
		}
		if check.Detail != "" {
			fmt.Fprintf(&md, " - %s", check.Detail)
		}
		fmt.Fprintf(&md, "\n")
	}
	fmt.Fprintf(&md, "\n## Commands\n\n")
	keys := []string{"codex_exec", "codex_eval_auto", "codex_eval_curate"}
	for _, key := range keys {
		if value := report.Commands[key]; value != "" {
			fmt.Fprintf(&md, "```bash\n%s\n```\n\n", value)
		}
	}
	if err := os.WriteFile(filepath.Join(outDir, "summary.md"), []byte(md.String()), 0o644); err != nil {
		return fmt.Errorf("write codex doctor summary: %w", err)
	}
	return nil
}

func writeJSONFile(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create json output dir: %w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("create json output file: %w", err)
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return fmt.Errorf("encode json output file: %w", err)
	}
	return nil
}

func codexDoctorCommands(opts codexConfigOptions) map[string]string {
	codexHome := ".tmp/shimctl-codex/<run>/codex-home"
	return map[string]string{
		"codex_exec":        fmt.Sprintf("CODEX_HOME=%s %s=%s codex exec --ephemeral --json -m %s -c model_provider=%s 'Reply OK'", shellQuote(codexHome), opts.APIKeyEnv, shellQuote("<secret>"), shellQuote(opts.Model), shellQuote(opts.Provider)),
		"codex_eval_auto":   fmt.Sprintf("SHIM_BASE_URL=%s CODEX_EVAL_MODELS=%s CODEX_API_KEY_ENV=%s %s=%s make codex-eval-auto", shellQuote(shimBaseURLFromCodexBaseURL(opts.BaseURL)), shellQuote(opts.Model), shellQuote(opts.APIKeyEnv), opts.APIKeyEnv, shellQuote("<secret>")),
		"codex_eval_curate": "make codex-eval-curate",
	}
}

func shimBaseURLFromCodexBaseURL(baseURL string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if strings.HasSuffix(baseURL, "/v1") {
		return strings.TrimSuffix(baseURL, "/v1")
	}
	return baseURL
}

func envStringFirst(keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}

func envInt(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	var value int
	if _, err := fmt.Sscanf(raw, "%d", &value); err != nil {
		return fallback
	}
	return value
}

func envBool(key string, fallback bool) bool {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	switch raw {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func tomlString(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	value = strings.ReplaceAll(value, "\n", `\n`)
	value = strings.ReplaceAll(value, "\r", `\r`)
	value = strings.ReplaceAll(value, "\t", `\t`)
	return `"` + value + `"`
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func slugifyCodexValue(value string) string {
	value = strings.ToLower(value)
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-'
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "model"
	}
	return out
}

func trimForCodexDoctor(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "...(truncated)"
}

func printCodexUsage(w io.Writer) {
	_, _ = fmt.Fprintln(w, "usage: shimctl codex <config|doctor> [flags]")
}
