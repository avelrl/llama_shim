package telemetry

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
)

const (
	ProtocolOTLPHTTP = "otlp_http"
	ProtocolOTLPGRPC = "otlp_grpc"
)

type Config struct {
	Enabled               bool
	Protocol              string
	Endpoint              string
	ServiceName           string
	ServiceVersion        string
	DeploymentEnvironment string
	Headers               map[string]string
	SampleRatio           float64
	BatchTimeout          time.Duration
	ExportTimeout         time.Duration
}

type ShutdownFunc func(context.Context) error

func Start(ctx context.Context, cfg Config) (ShutdownFunc, error) {
	if !cfg.Enabled {
		return func(context.Context) error { return nil }, nil
	}
	cfg = normalizeConfig(cfg)

	exporter, err := newTraceExporter(ctx, cfg)
	if err != nil {
		return nil, err
	}
	res, err := newResource(cfg)
	if err != nil {
		return nil, err
	}
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter,
			sdktrace.WithBatchTimeout(cfg.BatchTimeout),
			sdktrace.WithExportTimeout(cfg.ExportTimeout),
		),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(cfg.SampleRatio))),
	)
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	return provider.Shutdown, nil
}

func normalizeConfig(cfg Config) Config {
	cfg.Protocol = normalizeProtocol(cfg.Protocol)
	cfg.Endpoint = strings.TrimSpace(cfg.Endpoint)
	cfg.ServiceName = strings.TrimSpace(cfg.ServiceName)
	if cfg.ServiceName == "" {
		cfg.ServiceName = "llama_shim"
	}
	cfg.ServiceVersion = strings.TrimSpace(cfg.ServiceVersion)
	cfg.DeploymentEnvironment = strings.TrimSpace(cfg.DeploymentEnvironment)
	if cfg.SampleRatio < 0 {
		cfg.SampleRatio = 0
	}
	if cfg.SampleRatio > 1 {
		cfg.SampleRatio = 1
	}
	if cfg.BatchTimeout <= 0 {
		cfg.BatchTimeout = 5 * time.Second
	}
	if cfg.ExportTimeout <= 0 {
		cfg.ExportTimeout = 5 * time.Second
	}
	return cfg
}

func normalizeProtocol(protocol string) string {
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case "", ProtocolOTLPHTTP, "http/protobuf":
		return ProtocolOTLPHTTP
	case ProtocolOTLPGRPC, "grpc":
		return ProtocolOTLPGRPC
	default:
		return strings.ToLower(strings.TrimSpace(protocol))
	}
}

func newTraceExporter(ctx context.Context, cfg Config) (sdktrace.SpanExporter, error) {
	if err := validateEndpointURL(cfg.Endpoint); err != nil {
		return nil, err
	}
	switch cfg.Protocol {
	case ProtocolOTLPHTTP:
		opts := []otlptracehttp.Option{
			otlptracehttp.WithTimeout(cfg.ExportTimeout),
		}
		if cfg.Endpoint != "" {
			opts = append(opts, otlptracehttp.WithEndpointURL(cfg.Endpoint))
		}
		if len(cfg.Headers) > 0 {
			opts = append(opts, otlptracehttp.WithHeaders(cfg.Headers))
		}
		return otlptracehttp.New(ctx, opts...)
	case ProtocolOTLPGRPC:
		opts := []otlptracegrpc.Option{
			otlptracegrpc.WithTimeout(cfg.ExportTimeout),
		}
		if cfg.Endpoint != "" {
			opts = append(opts, otlptracegrpc.WithEndpointURL(cfg.Endpoint))
		}
		if len(cfg.Headers) > 0 {
			opts = append(opts, otlptracegrpc.WithHeaders(cfg.Headers))
		}
		return otlptracegrpc.New(ctx, opts...)
	default:
		return nil, fmt.Errorf("unsupported telemetry protocol %q", cfg.Protocol)
	}
}

func validateEndpointURL(endpoint string) error {
	if endpoint == "" {
		return nil
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("parse telemetry endpoint %q: %w", endpoint, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("telemetry endpoint %q must use http or https", endpoint)
	}
	if parsed.Host == "" {
		return fmt.Errorf("telemetry endpoint %q must include host", endpoint)
	}
	return nil
}

func newResource(cfg Config) (*resource.Resource, error) {
	attrs := []attribute.KeyValue{
		semconv.ServiceName(cfg.ServiceName),
	}
	if cfg.ServiceVersion != "" {
		attrs = append(attrs, semconv.ServiceVersion(cfg.ServiceVersion))
	}
	if cfg.DeploymentEnvironment != "" {
		attrs = append(attrs, attribute.String("deployment.environment.name", cfg.DeploymentEnvironment))
	}
	return resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(semconv.SchemaURL, attrs...),
	)
}
