package config

import (
	"fmt"
	"strings"
	"time"

	"llama_shim/internal/retrieval"

	"github.com/spf13/viper"
)

type ShimctlConfig struct {
	SQLitePath                     string
	LlamaBaseURL                   string
	LlamaTimeout                   time.Duration
	LlamaMaxConcurrentRequests     int
	LlamaMaxQueueWait              time.Duration
	ProbeCount                     int
	ProbeRequestTimeout            time.Duration
	ProbeBearerToken               string
	ProbeModel                     string
	LlamaHTTPMaxIdleConns          int
	LlamaHTTPMaxIdleConnsPerHost   int
	LlamaHTTPMaxConnsPerHost       int
	LlamaHTTPIdleConnTimeout       time.Duration
	LlamaHTTPDialTimeout           time.Duration
	LlamaHTTPKeepAlive             time.Duration
	LlamaHTTPTLSHandshakeTimeout   time.Duration
	LlamaHTTPExpectContinueTimeout time.Duration
	RetrievalIndexBackend          string
	RetrievalEmbedderBackend       string
	RetrievalEmbedderBaseURL       string
	RetrievalEmbedderModel         string
	ConfigFile                     string
}

func LoadShimctl(configPath string) (ShimctlConfig, error) {
	if err := loadDotEnv(resolveDotEnvPath()); err != nil {
		return ShimctlConfig{}, err
	}

	v := viper.New()
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()
	if err := v.BindEnv("probe.bearer_token", "SHIMCTL_PROBE_BEARER_TOKEN", "LLAMA_STARTUP_CALIBRATION_TOKEN"); err != nil {
		return ShimctlConfig{}, fmt.Errorf("bind probe.bearer_token env: %w", err)
	}
	if err := v.BindEnv("probe.model", "SHIMCTL_PROBE_MODEL", "LLAMA_STARTUP_CALIBRATION_MODEL"); err != nil {
		return ShimctlConfig{}, fmt.Errorf("bind probe.model env: %w", err)
	}
	setShimctlDefaults(v)

	if err := readConfigFileNamed(v, resolveConfigPath(configPath), "config"); err != nil {
		return ShimctlConfig{}, err
	}

	cfg := ShimctlConfig{
		SQLitePath:               strings.TrimSpace(v.GetString("sqlite.path")),
		LlamaBaseURL:             strings.TrimRight(strings.TrimSpace(v.GetString("llama.base_url")), "/"),
		ProbeBearerToken:         strings.TrimSpace(v.GetString("probe.bearer_token")),
		ProbeModel:               strings.TrimSpace(v.GetString("probe.model")),
		RetrievalIndexBackend:    strings.TrimSpace(v.GetString("retrieval.index.backend")),
		RetrievalEmbedderBackend: strings.TrimSpace(v.GetString("retrieval.embedder.backend")),
		RetrievalEmbedderBaseURL: strings.TrimSpace(v.GetString("retrieval.embedder.base_url")),
		RetrievalEmbedderModel:   strings.TrimSpace(v.GetString("retrieval.embedder.model")),
		ConfigFile:               v.ConfigFileUsed(),
	}

	if err := parseDuration(v.GetString("llama.timeout"), &cfg.LlamaTimeout); err != nil {
		return ShimctlConfig{}, fmt.Errorf("parse llama.timeout: %w", err)
	}
	llamaMaxConcurrentRequests, err := parseNonNegativeInt(v.GetString("llama.max_concurrent_requests"))
	if err != nil {
		return ShimctlConfig{}, fmt.Errorf("parse llama.max_concurrent_requests: %w", err)
	}
	cfg.LlamaMaxConcurrentRequests = llamaMaxConcurrentRequests
	if err := parseDuration(v.GetString("llama.max_queue_wait"), &cfg.LlamaMaxQueueWait); err != nil {
		return ShimctlConfig{}, fmt.Errorf("parse llama.max_queue_wait: %w", err)
	}
	probeCount, err := parsePositiveInt(v.GetString("probe.count"))
	if err != nil {
		return ShimctlConfig{}, fmt.Errorf("parse probe.count: %w", err)
	}
	cfg.ProbeCount = probeCount
	if err := parseDuration(v.GetString("probe.request_timeout"), &cfg.ProbeRequestTimeout); err != nil {
		return ShimctlConfig{}, fmt.Errorf("parse probe.request_timeout: %w", err)
	}
	llamaHTTPMaxIdleConns, err := parsePositiveInt(v.GetString("llama.http.max_idle_conns"))
	if err != nil {
		return ShimctlConfig{}, fmt.Errorf("parse llama.http.max_idle_conns: %w", err)
	}
	cfg.LlamaHTTPMaxIdleConns = llamaHTTPMaxIdleConns
	llamaHTTPMaxIdleConnsPerHost, err := parsePositiveInt(v.GetString("llama.http.max_idle_conns_per_host"))
	if err != nil {
		return ShimctlConfig{}, fmt.Errorf("parse llama.http.max_idle_conns_per_host: %w", err)
	}
	cfg.LlamaHTTPMaxIdleConnsPerHost = llamaHTTPMaxIdleConnsPerHost
	llamaHTTPMaxConnsPerHost, err := parsePositiveInt(v.GetString("llama.http.max_conns_per_host"))
	if err != nil {
		return ShimctlConfig{}, fmt.Errorf("parse llama.http.max_conns_per_host: %w", err)
	}
	cfg.LlamaHTTPMaxConnsPerHost = llamaHTTPMaxConnsPerHost
	if err := parseDuration(v.GetString("llama.http.idle_conn_timeout"), &cfg.LlamaHTTPIdleConnTimeout); err != nil {
		return ShimctlConfig{}, fmt.Errorf("parse llama.http.idle_conn_timeout: %w", err)
	}
	if err := parseDuration(v.GetString("llama.http.dial_timeout"), &cfg.LlamaHTTPDialTimeout); err != nil {
		return ShimctlConfig{}, fmt.Errorf("parse llama.http.dial_timeout: %w", err)
	}
	if err := parseDuration(v.GetString("llama.http.keep_alive"), &cfg.LlamaHTTPKeepAlive); err != nil {
		return ShimctlConfig{}, fmt.Errorf("parse llama.http.keep_alive: %w", err)
	}
	if err := parseDuration(v.GetString("llama.http.tls_handshake_timeout"), &cfg.LlamaHTTPTLSHandshakeTimeout); err != nil {
		return ShimctlConfig{}, fmt.Errorf("parse llama.http.tls_handshake_timeout: %w", err)
	}
	if err := parseDuration(v.GetString("llama.http.expect_continue_timeout"), &cfg.LlamaHTTPExpectContinueTimeout); err != nil {
		return ShimctlConfig{}, fmt.Errorf("parse llama.http.expect_continue_timeout: %w", err)
	}

	normalizedRetrieval, err := retrieval.NormalizeConfig(retrieval.Config{
		IndexBackend: cfg.RetrievalIndexBackend,
		Embedder: retrieval.EmbedderConfig{
			Backend: cfg.RetrievalEmbedderBackend,
			BaseURL: cfg.RetrievalEmbedderBaseURL,
			Model:   cfg.RetrievalEmbedderModel,
		},
	})
	if err != nil {
		return ShimctlConfig{}, fmt.Errorf("parse retrieval config: %w", err)
	}
	cfg.RetrievalIndexBackend = normalizedRetrieval.IndexBackend
	cfg.RetrievalEmbedderBackend = normalizedRetrieval.Embedder.Backend
	cfg.RetrievalEmbedderBaseURL = normalizedRetrieval.Embedder.BaseURL
	cfg.RetrievalEmbedderModel = normalizedRetrieval.Embedder.Model

	return cfg, nil
}

func setShimctlDefaults(v *viper.Viper) {
	v.SetDefault("sqlite.path", "./data/shim.db")
	v.SetDefault("llama.base_url", "http://127.0.0.1:8081")
	v.SetDefault("llama.timeout", "60s")
	v.SetDefault("llama.max_concurrent_requests", "4")
	v.SetDefault("llama.max_queue_wait", "0s")
	v.SetDefault("llama.http.max_idle_conns", "32")
	v.SetDefault("llama.http.max_idle_conns_per_host", "16")
	v.SetDefault("llama.http.max_conns_per_host", "8")
	v.SetDefault("llama.http.idle_conn_timeout", "90s")
	v.SetDefault("llama.http.dial_timeout", "10s")
	v.SetDefault("llama.http.keep_alive", "30s")
	v.SetDefault("llama.http.tls_handshake_timeout", "10s")
	v.SetDefault("llama.http.expect_continue_timeout", "1s")
	v.SetDefault("probe.count", "3")
	v.SetDefault("probe.request_timeout", "8s")
	v.SetDefault("probe.bearer_token", "")
	v.SetDefault("probe.model", "")
	v.SetDefault("retrieval.index.backend", retrieval.IndexBackendLexical)
	v.SetDefault("retrieval.embedder.backend", retrieval.EmbedderBackendDisabled)
	v.SetDefault("retrieval.embedder.base_url", "")
	v.SetDefault("retrieval.embedder.model", "")
}
