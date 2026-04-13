package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	Addr                                           string
	SQLitePath                                     string
	LlamaBaseURL                                   string
	LlamaTimeout                                   time.Duration
	ReadTimeout                                    time.Duration
	WriteTimeout                                   time.Duration
	IdleTimeout                                    time.Duration
	LogLevel                                       slog.Level
	LogFilePath                                    string
	ResponsesMode                                  string
	ResponsesCustomToolsMode                       string
	ResponsesCodexEnableCompatibility              bool
	ResponsesCodexForceToolChoiceRequired          bool
	ResponsesCodeInterpreterBackend                string
	ResponsesCodeInterpreterPythonBinary           string
	ResponsesCodeInterpreterDockerBinary           string
	ResponsesCodeInterpreterDockerImage            string
	ResponsesCodeInterpreterDockerMemory           string
	ResponsesCodeInterpreterDockerCPU              string
	ResponsesCodeInterpreterDockerPids             int
	ResponsesCodeInterpreterTimeout                time.Duration
	ResponsesCodeInterpreterInputFileURLPolicy     string
	ResponsesCodeInterpreterInputFileURLAllowHosts []string
	ResponsesCodeInterpreterCleanupInterval        time.Duration
	ConfigFile                                     string
}

const (
	ResponsesModePreferLocal                                       = "prefer_local"
	ResponsesModePreferUpstream                                    = "prefer_upstream"
	ResponsesModeLocalOnly                                         = "local_only"
	ResponsesCodeInterpreterBackendDisabled                        = "disabled"
	ResponsesCodeInterpreterBackendUnsafeHost                      = "unsafe_host"
	ResponsesCodeInterpreterBackendDocker                          = "docker"
	ResponsesCodeInterpreterInputFileURLPolicyDisabled             = "disabled"
	ResponsesCodeInterpreterInputFileURLPolicyAllowlist            = "allowlist"
	ResponsesCodeInterpreterInputFileURLPolicyUnsafeAllowHTTPHTTPS = "unsafe_allow_http_https"
)

func Load(configPath string) (Config, error) {
	v := viper.New()
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	setDefaults(v)

	if err := readConfigFile(v, resolveConfigPath(configPath)); err != nil {
		return Config{}, err
	}

	cfg := Config{
		Addr:                                           strings.TrimSpace(v.GetString("shim.addr")),
		SQLitePath:                                     strings.TrimSpace(v.GetString("sqlite.path")),
		LlamaBaseURL:                                   strings.TrimRight(strings.TrimSpace(v.GetString("llama.base_url")), "/"),
		ConfigFile:                                     v.ConfigFileUsed(),
		LogLevel:                                       slog.LevelInfo,
		LogFilePath:                                    strings.TrimSpace(v.GetString("log.file_path")),
		ResponsesMode:                                  strings.ToLower(strings.TrimSpace(v.GetString("responses.mode"))),
		ResponsesCustomToolsMode:                       strings.ToLower(strings.TrimSpace(v.GetString("responses.custom_tools.mode"))),
		ResponsesCodexEnableCompatibility:              v.GetBool("responses.codex.enable_compatibility"),
		ResponsesCodexForceToolChoiceRequired:          v.GetBool("responses.codex.force_tool_choice_required"),
		ResponsesCodeInterpreterBackend:                strings.ToLower(strings.TrimSpace(v.GetString("responses.code_interpreter.backend"))),
		ResponsesCodeInterpreterPythonBinary:           strings.TrimSpace(v.GetString("responses.code_interpreter.python_binary")),
		ResponsesCodeInterpreterDockerBinary:           strings.TrimSpace(v.GetString("responses.code_interpreter.docker.binary")),
		ResponsesCodeInterpreterDockerImage:            strings.TrimSpace(v.GetString("responses.code_interpreter.docker.image")),
		ResponsesCodeInterpreterDockerMemory:           strings.TrimSpace(v.GetString("responses.code_interpreter.docker.memory_limit")),
		ResponsesCodeInterpreterDockerCPU:              strings.TrimSpace(v.GetString("responses.code_interpreter.docker.cpu_limit")),
		ResponsesCodeInterpreterInputFileURLPolicy:     strings.ToLower(strings.TrimSpace(v.GetString("responses.code_interpreter.input_file_url_policy"))),
		ResponsesCodeInterpreterInputFileURLAllowHosts: parseStringList(v, "responses.code_interpreter.input_file_url_allow_hosts"),
	}
	if cfg.ResponsesCodeInterpreterBackend == "" {
		if v.GetBool("responses.code_interpreter.enable_unsafe_host_executor") {
			cfg.ResponsesCodeInterpreterBackend = ResponsesCodeInterpreterBackendUnsafeHost
		} else {
			cfg.ResponsesCodeInterpreterBackend = ResponsesCodeInterpreterBackendDisabled
		}
	}

	if err := parseDuration(v.GetString("llama.timeout"), &cfg.LlamaTimeout); err != nil {
		return Config{}, fmt.Errorf("parse llama.timeout: %w", err)
	}
	if err := parseDuration(v.GetString("shim.read_timeout"), &cfg.ReadTimeout); err != nil {
		return Config{}, fmt.Errorf("parse shim.read_timeout: %w", err)
	}
	if err := parseDuration(v.GetString("shim.write_timeout"), &cfg.WriteTimeout); err != nil {
		return Config{}, fmt.Errorf("parse shim.write_timeout: %w", err)
	}
	if err := parseDuration(v.GetString("shim.idle_timeout"), &cfg.IdleTimeout); err != nil {
		return Config{}, fmt.Errorf("parse shim.idle_timeout: %w", err)
	}
	if err := parseLogLevel(v.GetString("log.level"), &cfg.LogLevel); err != nil {
		return Config{}, fmt.Errorf("parse log.level: %w", err)
	}
	if err := parseResponsesMode(cfg.ResponsesMode); err != nil {
		return Config{}, fmt.Errorf("parse responses.mode: %w", err)
	}
	if err := parseCustomToolsMode(cfg.ResponsesCustomToolsMode); err != nil {
		return Config{}, fmt.Errorf("parse responses.custom_tools.mode: %w", err)
	}
	if err := parseCodeInterpreterBackend(cfg.ResponsesCodeInterpreterBackend); err != nil {
		return Config{}, fmt.Errorf("parse responses.code_interpreter.backend: %w", err)
	}
	if err := parseCodeInterpreterInputFileURLPolicy(cfg.ResponsesCodeInterpreterInputFileURLPolicy); err != nil {
		return Config{}, fmt.Errorf("parse responses.code_interpreter.input_file_url_policy: %w", err)
	}
	if err := parseDuration(v.GetString("responses.code_interpreter.execution_timeout"), &cfg.ResponsesCodeInterpreterTimeout); err != nil {
		return Config{}, fmt.Errorf("parse responses.code_interpreter.execution_timeout: %w", err)
	}
	if err := parseDuration(v.GetString("responses.code_interpreter.cleanup_interval"), &cfg.ResponsesCodeInterpreterCleanupInterval); err != nil {
		return Config{}, fmt.Errorf("parse responses.code_interpreter.cleanup_interval: %w", err)
	}
	pidsLimit, err := parsePositiveInt(v.GetString("responses.code_interpreter.docker.pids_limit"))
	if err != nil {
		return Config{}, fmt.Errorf("parse responses.code_interpreter.docker.pids_limit: %w", err)
	}
	cfg.ResponsesCodeInterpreterDockerPids = pidsLimit
	if cfg.ResponsesCodeInterpreterPythonBinary == "" {
		return Config{}, fmt.Errorf("parse responses.code_interpreter.python_binary: %w", strconv.ErrSyntax)
	}
	if cfg.ResponsesCodeInterpreterDockerBinary == "" {
		return Config{}, fmt.Errorf("parse responses.code_interpreter.docker.binary: %w", strconv.ErrSyntax)
	}
	if cfg.ResponsesCodeInterpreterDockerImage == "" {
		return Config{}, fmt.Errorf("parse responses.code_interpreter.docker.image: %w", strconv.ErrSyntax)
	}
	if cfg.ResponsesCodeInterpreterDockerMemory == "" {
		return Config{}, fmt.Errorf("parse responses.code_interpreter.docker.memory_limit: %w", strconv.ErrSyntax)
	}
	if cfg.ResponsesCodeInterpreterDockerCPU == "" {
		return Config{}, fmt.Errorf("parse responses.code_interpreter.docker.cpu_limit: %w", strconv.ErrSyntax)
	}

	return cfg, nil
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("shim.addr", ":8080")
	v.SetDefault("shim.read_timeout", "15s")
	v.SetDefault("shim.write_timeout", "90s")
	v.SetDefault("shim.idle_timeout", "60s")
	v.SetDefault("sqlite.path", "./data/shim.db")
	v.SetDefault("llama.base_url", "http://127.0.0.1:8081")
	v.SetDefault("llama.timeout", "60s")
	v.SetDefault("log.level", "info")
	v.SetDefault("log.file_path", "")
	v.SetDefault("responses.mode", ResponsesModePreferLocal)
	v.SetDefault("responses.custom_tools.mode", "auto")
	v.SetDefault("responses.codex.enable_compatibility", true)
	v.SetDefault("responses.codex.force_tool_choice_required", true)
	v.SetDefault("responses.code_interpreter.backend", "")
	v.SetDefault("responses.code_interpreter.enable_unsafe_host_executor", false)
	v.SetDefault("responses.code_interpreter.python_binary", "python3")
	v.SetDefault("responses.code_interpreter.execution_timeout", "20s")
	v.SetDefault("responses.code_interpreter.docker.binary", "docker")
	v.SetDefault("responses.code_interpreter.docker.image", "python:3.12-slim")
	v.SetDefault("responses.code_interpreter.docker.memory_limit", "1g")
	v.SetDefault("responses.code_interpreter.docker.cpu_limit", "0.5")
	v.SetDefault("responses.code_interpreter.docker.pids_limit", "64")
	v.SetDefault("responses.code_interpreter.input_file_url_policy", ResponsesCodeInterpreterInputFileURLPolicyDisabled)
	v.SetDefault("responses.code_interpreter.input_file_url_allow_hosts", []string{})
	v.SetDefault("responses.code_interpreter.cleanup_interval", "1m")
}

func resolveConfigPath(configPath string) string {
	if strings.TrimSpace(configPath) != "" {
		return configPath
	}
	return strings.TrimSpace(os.Getenv("SHIM_CONFIG"))
}

func readConfigFile(v *viper.Viper, configPath string) error {
	if configPath != "" {
		v.SetConfigFile(configPath)
		if err := v.ReadInConfig(); err != nil {
			return fmt.Errorf("read config file %q: %w", configPath, err)
		}
		return nil
	}

	v.SetConfigName("config")
	v.AddConfigPath(".")
	if err := v.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if errors.As(err, &notFound) {
			return nil
		}
		return fmt.Errorf("read config file: %w", err)
	}
	return nil
}

func parseDuration(value string, dst *time.Duration) error {
	parsed, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil {
		return err
	}
	*dst = parsed
	return nil
}

func parseLogLevel(value string, dst *slog.Level) error {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "debug":
		*dst = slog.LevelDebug
	case "info":
		*dst = slog.LevelInfo
	case "warn", "warning":
		*dst = slog.LevelWarn
	case "error":
		*dst = slog.LevelError
	default:
		if n, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
			*dst = slog.Level(n)
			return nil
		}
		return strconv.ErrSyntax
	}

	return nil
}

func parseCustomToolsMode(value string) error {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "bridge", "passthrough", "auto":
		return nil
	default:
		return strconv.ErrSyntax
	}
}

func parseResponsesMode(value string) error {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", ResponsesModePreferLocal, ResponsesModePreferUpstream, ResponsesModeLocalOnly:
		return nil
	default:
		return strconv.ErrSyntax
	}
}

func parseCodeInterpreterBackend(value string) error {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case ResponsesCodeInterpreterBackendDisabled, ResponsesCodeInterpreterBackendUnsafeHost, ResponsesCodeInterpreterBackendDocker:
		return nil
	default:
		return strconv.ErrSyntax
	}
}

func parseCodeInterpreterInputFileURLPolicy(value string) error {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case ResponsesCodeInterpreterInputFileURLPolicyDisabled,
		ResponsesCodeInterpreterInputFileURLPolicyAllowlist,
		ResponsesCodeInterpreterInputFileURLPolicyUnsafeAllowHTTPHTTPS:
		return nil
	default:
		return strconv.ErrSyntax
	}
}

func parsePositiveInt(value string) (int, error) {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0, err
	}
	if parsed <= 0 {
		return 0, strconv.ErrSyntax
	}
	return parsed, nil
}

func parseStringList(v *viper.Viper, key string) []string {
	values := v.GetStringSlice(key)
	if len(values) == 0 {
		if raw := strings.TrimSpace(v.GetString(key)); raw != "" {
			values = strings.Split(raw, ",")
		}
	} else if len(values) == 1 && strings.Contains(values[0], ",") {
		values = strings.Split(values[0], ",")
	}

	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		normalized := strings.ToLower(trimmed)
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}
