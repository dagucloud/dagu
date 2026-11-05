// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package telemetry

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/url"
	"os"
	"strings"

	cmnvalue "github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	// TracerName is the name of the tracer
	TracerName = "github.com/dagucloud/dagu/v2"
)

// Tracer wraps OpenTelemetry tracer with DAG-specific configuration
type Tracer struct {
	tracer   trace.Tracer
	provider *sdktrace.TracerProvider
	config   *ir.OTelConfig
}

// NewTracer creates a new OpenTelemetry tracer for a DAG.
// The vars parameter is used to evaluate environment variables and secrets in the OTel config.
func NewTracer(ctx context.Context, dag *ir.DAG, vars map[string]string) (*Tracer, error) {
	if dag.OTel == nil || !dag.OTel.Enabled {
		return &Tracer{tracer: otel.Tracer(TracerName)}, nil
	}

	scope := cmnvalue.GetEnvScope(ctx)
	if len(vars) > 0 {
		if scope == nil {
			scope = cmnvalue.NewEnvScope(nil, false)
		}
		scope = scope.WithEntries(vars, cmnvalue.EnvSourceStepEnv)
	}
	resolver := cmnvalue.NewResolver(cmnvalue.StaticScope{}, cmnvalue.RuntimeScope{Env: scope})
	cfgAny, err := resolver.Object(ctx, *dag.OTel, cmnvalue.HostConfigObjectField("otel"))
	if err != nil {
		return nil, fmt.Errorf("failed to evaluate OTel config: %w", err)
	}
	cfg, ok := cfgAny.(ir.OTelConfig)
	if !ok {
		return nil, fmt.Errorf("failed to evaluate OTel config: expected ir.OTelConfig, got %T", cfgAny)
	}

	// Resource values have always seen the process env and ${DAG_NAME}, which
	// the evaluation above does not provide, so they are evaluated again with
	// those added. DAG env and secrets from vars win over the process env.
	cfg.Resource, err = resolveResource(ctx, dag, scope)
	if err != nil {
		return nil, fmt.Errorf("failed to evaluate OTel resource: %w", err)
	}

	exporter, err := createExporter(ctx, &cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create OTel exporter: %w", err)
	}

	res, err := createResource(&cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create OTel resource: %w", err)
	}

	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)

	otel.SetTracerProvider(provider)

	return &Tracer{
		tracer:   otel.Tracer(TracerName),
		provider: provider,
		config:   dag.OTel,
	}, nil
}

// createExporter creates an OTLP exporter based on the endpoint
func createExporter(ctx context.Context, config *ir.OTelConfig) (sdktrace.SpanExporter, error) {
	if config.Endpoint == "" {
		return nil, fmt.Errorf("OTel endpoint is required")
	}
	if isHTTPEndpoint(config.Endpoint) {
		return createHTTPExporter(ctx, config)
	}
	return createGRPCExporter(ctx, config)
}

// isHTTPEndpoint reports whether the endpoint selects the OTLP/HTTP exporter.
func isHTTPEndpoint(endpoint string) bool {
	return strings.HasSuffix(endpoint, "/v1/traces")
}

// createHTTPExporter creates an OTLP HTTP exporter. The endpoint is either a
// URL (http://host:4318/v1/traces) or host:port followed by the path
// (host:4318/v1/traces). For a URL, the scheme selects HTTP or HTTPS.
func createHTTPExporter(ctx context.Context, config *ir.OTelConfig) (sdktrace.SpanExporter, error) {
	opts := []otlptracehttp.Option{
		otlptracehttp.WithHeaders(config.Headers),
	}

	insecure := config.Insecure
	if strings.Contains(config.Endpoint, "://") {
		u, err := url.Parse(config.Endpoint)
		if err != nil {
			return nil, fmt.Errorf("invalid OTel endpoint %q: %w", config.Endpoint, err)
		}
		switch u.Scheme {
		case "http":
			insecure = true
		case "https":
			insecure = false
		default:
			return nil, fmt.Errorf("invalid OTel endpoint %q: scheme must be http or https", config.Endpoint)
		}
		opts = append(opts, otlptracehttp.WithEndpointURL(config.Endpoint))
	} else {
		hostPort, path, _ := strings.Cut(config.Endpoint, "/")
		opts = append(opts,
			otlptracehttp.WithEndpoint(hostPort),
			otlptracehttp.WithURLPath("/"+path),
		)
	}

	if config.Timeout > 0 {
		opts = append(opts, otlptracehttp.WithTimeout(config.Timeout))
	}

	if insecure {
		opts = append(opts, otlptracehttp.WithInsecure())
	} else {
		opts = append(opts, otlptracehttp.WithTLSClientConfig(&tls.Config{
			MinVersion: tls.VersionTLS12,
		}))
	}

	client := otlptracehttp.NewClient(opts...)
	return otlptrace.New(ctx, client)
}

// createGRPCExporter creates an OTLP gRPC exporter
func createGRPCExporter(ctx context.Context, config *ir.OTelConfig) (sdktrace.SpanExporter, error) {
	opts := []otlptracegrpc.Option{
		otlptracegrpc.WithEndpoint(config.Endpoint),
		otlptracegrpc.WithHeaders(config.Headers),
	}

	if config.Timeout > 0 {
		opts = append(opts, otlptracegrpc.WithTimeout(config.Timeout))
	}

	if config.Insecure {
		opts = append(opts, otlptracegrpc.WithInsecure())
		opts = append(opts, otlptracegrpc.WithDialOption(grpc.WithTransportCredentials(insecure.NewCredentials())))
	} else {
		opts = append(opts, otlptracegrpc.WithDialOption(grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
			MinVersion: tls.VersionTLS12,
		}))))
	}

	client := otlptracegrpc.NewClient(opts...)
	return otlptrace.New(ctx, client)
}

// resolveResource evaluates dag.OTel.Resource against scope, plus the process
// env and DAG_NAME.
func resolveResource(ctx context.Context, dag *ir.DAG, scope *cmnvalue.EnvScope) (map[string]any, error) {
	if len(dag.OTel.Resource) == 0 {
		return nil, nil
	}
	processEnv := make(map[string]string)
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			processEnv[k] = v
		}
	}
	resScope := cmnvalue.NewEnvScope(nil, false).WithEntries(processEnv, cmnvalue.EnvSourceStepEnv)
	if scope != nil {
		resScope = resScope.WithEntries(scope.ToMap(), cmnvalue.EnvSourceStepEnv)
	}
	resScope = resScope.WithEntries(map[string]string{"DAG_NAME": dag.Name}, cmnvalue.EnvSourceStepEnv)

	resolver := cmnvalue.NewResolver(cmnvalue.StaticScope{}, cmnvalue.RuntimeScope{Env: resScope})
	resolved, err := resolver.Object(ctx, dag.OTel.Resource, cmnvalue.HostConfigObjectField("otel"))
	if err != nil {
		return nil, err
	}
	out, ok := resolved.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("expected map[string]any, got %T", resolved)
	}
	return out, nil
}

// createResource creates the OpenTelemetry resource from the evaluated config,
// so its string values are already resolved.
func createResource(config *ir.OTelConfig) (*resource.Resource, error) {
	attrs := []attribute.KeyValue{
		semconv.ServiceName("dagu"),
	}

	// Add custom resource attributes from config
	for key, val := range config.Resource {
		switch v := val.(type) {
		case string:
			attrs = append(attrs, attribute.String(key, v))
		case int:
			attrs = append(attrs, attribute.Int(key, v))
		case int64:
			attrs = append(attrs, attribute.Int64(key, v))
		case float64:
			attrs = append(attrs, attribute.Float64(key, v))
		case bool:
			attrs = append(attrs, attribute.Bool(key, v))
		}
	}

	return resource.NewWithAttributes(
		semconv.SchemaURL,
		attrs...,
	), nil
}

// Start starts a new span for the DAG execution
func (t *Tracer) Start(ctx context.Context, spanName string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	if t.tracer == nil {
		// Return a no-op span if tracer is not initialized
		return ctx, trace.SpanFromContext(ctx)
	}
	return t.tracer.Start(ctx, spanName, opts...)
}

// Shutdown shuts down the tracer provider
func (t *Tracer) Shutdown(ctx context.Context) error {
	if t.provider != nil {
		return t.provider.Shutdown(ctx)
	}
	return nil
}

// IsEnabled returns true if OTel is enabled
func (t *Tracer) IsEnabled() bool {
	return t.config != nil && t.config.Enabled
}
