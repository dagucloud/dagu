// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package telemetry

import (
	"context"
	"crypto/tls"
	"fmt"
	"os"

	cmnvalue "github.com/dagucloud/dagu/internal/cmn/value"
	"github.com/dagucloud/dagu/internal/core"
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
	TracerName = "github.com/dagucloud/dagu"
)

// Tracer wraps OpenTelemetry tracer with DAG-specific configuration
type Tracer struct {
	tracer   trace.Tracer
	provider *sdktrace.TracerProvider
	config   *core.OTelConfig
}

// NewTracer creates a new OpenTelemetry tracer for a DAG.
// The vars parameter is used to evaluate environment variables and secrets in the OTel config.
func NewTracer(ctx context.Context, dag *core.DAG, vars map[string]string) (*Tracer, error) {
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
	cfg, ok := cfgAny.(core.OTelConfig)
	if !ok {
		return nil, fmt.Errorf("failed to evaluate OTel config: expected core.OTelConfig, got %T", cfgAny)
	}

	exporter, err := createExporter(ctx, &cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create OTel exporter: %w", err)
	}

	res, err := createResource(dag)
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
func createExporter(ctx context.Context, config *core.OTelConfig) (sdktrace.SpanExporter, error) {
	endpoint := config.Endpoint
	if endpoint == "" {
		return nil, fmt.Errorf("OTel endpoint is required")
	}

	// Determine if it's HTTP or gRPC based on the endpoint
	isHTTP := len(endpoint) > 4 && endpoint[len(endpoint)-4:] == "/v1/traces"

	if isHTTP {
		return createHTTPExporter(ctx, config)
	}
	return createGRPCExporter(ctx, config)
}

// createHTTPExporter creates an OTLP HTTP exporter
func createHTTPExporter(ctx context.Context, config *core.OTelConfig) (sdktrace.SpanExporter, error) {
	opts := []otlptracehttp.Option{
		otlptracehttp.WithEndpoint(config.Endpoint),
		otlptracehttp.WithHeaders(config.Headers),
	}

	if config.Timeout > 0 {
		opts = append(opts, otlptracehttp.WithTimeout(config.Timeout))
	}

	if config.Insecure {
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
func createGRPCExporter(ctx context.Context, config *core.OTelConfig) (sdktrace.SpanExporter, error) {
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

// createResource creates the OpenTelemetry resource for the DAG
func createResource(dag *core.DAG) (*resource.Resource, error) {
	attrs := []attribute.KeyValue{
		semconv.ServiceName("dagu"),
	}

	// Add custom resource attributes from config
	if dag.OTel != nil && dag.OTel.Resource != nil {
		// DAG variables for expansion (e.g., ${DAG_NAME})
		dagVars := map[string]string{
			"DAG_NAME": dag.Name,
		}

		for key, val := range dag.OTel.Resource {
			switch v := val.(type) {
			case string:
				// Expand environment variables using os.Expand with DAG vars
				// Check DAG vars first, then fall back to real environment
				expanded := os.Expand(v, func(name string) string {
					if val, ok := dagVars[name]; ok {
						return val
					}
					return os.Getenv(name)
				})
				attrs = append(attrs, attribute.String(key, expanded))
			case int:
				attrs = append(attrs, attribute.Int(key, v))
			case int64:
				attrs = append(attrs, attribute.Int64(key, int64(v)))
			case float64:
				attrs = append(attrs, attribute.Float64(key, v))
			case bool:
				attrs = append(attrs, attribute.Bool(key, v))
			}
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
