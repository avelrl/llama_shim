package telemetry

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestNormalizeConfigDefaults(t *testing.T) {
	cfg := normalizeConfig(Config{
		SampleRatio:   2,
		BatchTimeout:  -time.Second,
		ExportTimeout: -time.Second,
	})

	if cfg.Protocol != ProtocolOTLPHTTP {
		t.Fatalf("protocol = %q, want %q", cfg.Protocol, ProtocolOTLPHTTP)
	}
	if cfg.ServiceName != "llama_shim" {
		t.Fatalf("service name = %q, want llama_shim", cfg.ServiceName)
	}
	if cfg.SampleRatio != 1 {
		t.Fatalf("sample ratio = %v, want 1", cfg.SampleRatio)
	}
	if cfg.BatchTimeout != 5*time.Second {
		t.Fatalf("batch timeout = %s, want 5s", cfg.BatchTimeout)
	}
	if cfg.ExportTimeout != 5*time.Second {
		t.Fatalf("export timeout = %s, want 5s", cfg.ExportTimeout)
	}
}

func TestStartDisabledIsNoop(t *testing.T) {
	shutdown, err := Start(context.Background(), Config{Enabled: false})
	if err != nil {
		t.Fatalf("Start disabled returned error: %v", err)
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("disabled shutdown returned error: %v", err)
	}
}

func TestStartRejectsUnsupportedProtocol(t *testing.T) {
	_, err := Start(context.Background(), Config{
		Enabled:  true,
		Protocol: "zipkin",
	})
	if err == nil {
		t.Fatal("Start unsupported protocol returned nil error")
	}
	if !strings.Contains(err.Error(), "unsupported telemetry protocol") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStartRejectsInvalidEndpoint(t *testing.T) {
	_, err := Start(context.Background(), Config{
		Enabled:  true,
		Protocol: ProtocolOTLPHTTP,
		Endpoint: "127.0.0.1:4318/v1/traces",
	})
	if err == nil {
		t.Fatal("Start invalid endpoint returned nil error")
	}
	if !strings.Contains(err.Error(), "parse telemetry endpoint") {
		t.Fatalf("unexpected error: %v", err)
	}
}
