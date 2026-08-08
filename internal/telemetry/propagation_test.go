// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package telemetry

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

func TestTraceContextCarrier(t *testing.T) {
	t.Run("GetAndSet", func(t *testing.T) {
		carrier := NewTraceContextCarrier()

		// Test Set and Get
		carrier.Set("traceparent", "00-1234567890abcdef1234567890abcdef-1234567890abcdef-01")
		carrier.Set("tracestate", "vendor1=value1,vendor2=value2")

		assert.Equal(t, "00-1234567890abcdef1234567890abcdef-1234567890abcdef-01", carrier.Get("traceparent"))
		assert.Equal(t, "vendor1=value1,vendor2=value2", carrier.Get("tracestate"))
		assert.Equal(t, "", carrier.Get("nonexistent"))
	})

	t.Run("Keys", func(t *testing.T) {
		carrier := NewTraceContextCarrier()
		carrier.Set("traceparent", "value1")
		carrier.Set("tracestate", "value2")

		keys := carrier.Keys()
		assert.Len(t, keys, 2)
		// Keys should be uppercase after conversion
		assert.Contains(t, keys, "TRACEPARENT")
		assert.Contains(t, keys, "TRACESTATE")
	})

	t.Run("ToEnv", func(t *testing.T) {
		carrier := NewTraceContextCarrier()
		carrier.Set("TRACEPARENT", "00-1234567890abcdef1234567890abcdef-1234567890abcdef-01")
		carrier.Set("TRACESTATE", "vendor=value")

		env := carrier.ToEnv()
		assert.Len(t, env, 2)
		assert.Contains(t, env, "TRACEPARENT=00-1234567890abcdef1234567890abcdef-1234567890abcdef-01")
		assert.Contains(t, env, "TRACESTATE=vendor=value")
	})
}

func TestInitializePropagators(t *testing.T) {
	// Save current propagator
	oldProp := otel.GetTextMapPropagator()
	defer otel.SetTextMapPropagator(oldProp)

	InitializePropagators()

	// Check that the propagator is set
	prop := otel.GetTextMapPropagator()
	require.NotNil(t, prop)

	// The propagator should be a TraceContext propagator
	_, ok := prop.(propagation.TraceContext)
	assert.True(t, ok, "Expected TraceContext propagator")
}

func TestInjectTraceContext(t *testing.T) {
	// Set up a mock tracer provider
	InitializePropagators()

	// Create a context with a span
	tracer := noop.NewTracerProvider().Tracer("test")
	ctx, span := tracer.Start(context.Background(), "test-span")
	defer span.End()

	// Since we're using a noop tracer, manually set up a real span context
	// In real usage, this would be done by the OpenTelemetry SDK
	traceID, _ := trace.TraceIDFromHex("1234567890abcdef1234567890abcdef")
	spanID, _ := trace.SpanIDFromHex("1234567890abcdef")
	spanCtx := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
	})
	ctx = trace.ContextWithSpanContext(ctx, spanCtx)

	// Inject trace context
	envVars := InjectTraceContext(ctx)

	// Check that environment variables are created
	assert.NotEmpty(t, envVars)

	// Should contain TRACEPARENT (uppercase)
	found := false
	for _, env := range envVars {
		if len(env) > 11 && env[:11] == "TRACEPARENT" {
			found = true
			// Format: version-traceid-spanid-flags
			assert.Contains(t, env, "00-1234567890abcdef1234567890abcdef-1234567890abcdef-01")
			break
		}
	}
	assert.True(t, found, "Expected to find TRACEPARENT in environment variables")
}

func TestExtractTraceContext(t *testing.T) {
	InitializePropagators()

	t.Run("WithTraceContextInEnvironment", func(t *testing.T) {
		// Set environment variables
		require.NoError(t, os.Setenv("TRACEPARENT", "00-1234567890abcdef1234567890abcdef-1234567890abcdef-01"))
		require.NoError(t, os.Setenv("TRACESTATE", "vendor=value"))
		defer func() {
			_ = os.Unsetenv("TRACEPARENT")
			_ = os.Unsetenv("TRACESTATE")
		}()

		// Extract trace context
		ctx := ExtractTraceContext(context.Background())

		// Check that the context has span context
		spanCtx := trace.SpanContextFromContext(ctx)
		assert.True(t, spanCtx.IsValid())
		assert.Equal(t, "1234567890abcdef1234567890abcdef", spanCtx.TraceID().String())
		assert.Equal(t, "1234567890abcdef", spanCtx.SpanID().String())
		assert.True(t, spanCtx.IsSampled())
		assert.Equal(t, "vendor=value", spanCtx.TraceState().String())
	})

	t.Run("WithoutTraceContextInEnvironment", func(t *testing.T) {
		// Ensure no trace context in environment
		_ = os.Unsetenv("TRACEPARENT")
		_ = os.Unsetenv("TRACESTATE")

		// Extract trace context
		ctx := ExtractTraceContext(context.Background())

		// Check that the context has no span context
		spanCtx := trace.SpanContextFromContext(ctx)
		assert.False(t, spanCtx.IsValid())
	})
}
