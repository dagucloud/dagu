// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package telemetry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewTracer(t *testing.T) {
	t.Run("DisabledOTel", func(t *testing.T) {
		dag := &ir.DAG{
			Name: "test-dag",
			OTel: nil, // OTel not configured
		}

		tracer, err := NewTracer(context.Background(), dag, nil)
		require.NoError(t, err)
		assert.NotNil(t, tracer)
		assert.False(t, tracer.IsEnabled())
		assert.Nil(t, tracer.provider)
	})

	t.Run("OTelDisabledExplicitly", func(t *testing.T) {
		dag := &ir.DAG{
			Name: "test-dag",
			OTel: &ir.OTelConfig{
				Enabled:  false,
				Endpoint: "localhost:4317",
			},
		}

		tracer, err := NewTracer(context.Background(), dag, nil)
		require.NoError(t, err)
		assert.NotNil(t, tracer)
		assert.False(t, tracer.IsEnabled())
		assert.Nil(t, tracer.provider)
	})

	t.Run("MissingEndpoint", func(t *testing.T) {
		dag := &ir.DAG{
			Name: "test-dag",
			OTel: &ir.OTelConfig{
				Enabled: true,
				// Missing endpoint
			},
		}

		tracer, err := NewTracer(context.Background(), dag, nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "endpoint is required")
		assert.Nil(t, tracer)
	})

	t.Run("GRPCEndpointDetection", func(t *testing.T) {
		dag := &ir.DAG{
			Name: "test-dag",
			OTel: &ir.OTelConfig{
				Enabled:  true,
				Endpoint: "localhost:4317",
			},
		}

		// This will try to connect to a non-existent endpoint, but that's fine for testing
		tracer, err := NewTracer(context.Background(), dag, nil)
		require.NoError(t, err)
		assert.NotNil(t, tracer)
		assert.True(t, tracer.IsEnabled())
		assert.NotNil(t, tracer.provider)

		// Clean up
		err = tracer.Shutdown(context.Background())
		assert.NoError(t, err)
	})

	t.Run("HTTPEndpointDetection", func(t *testing.T) {
		dag := &ir.DAG{
			Name: "test-dag",
			OTel: &ir.OTelConfig{
				Enabled:  true,
				Endpoint: "localhost:4318/v1/traces",
			},
		}

		// This will try to connect to a non-existent endpoint, but that's fine for testing
		tracer, err := NewTracer(context.Background(), dag, nil)
		require.NoError(t, err)
		assert.NotNil(t, tracer)
		assert.True(t, tracer.IsEnabled())
		assert.NotNil(t, tracer.provider)

		// Clean up
		err = tracer.Shutdown(context.Background())
		assert.NoError(t, err)
	})

	t.Run("WithHeadersAndTimeout", func(t *testing.T) {
		dag := &ir.DAG{
			Name: "test-dag",
			OTel: &ir.OTelConfig{
				Enabled:  true,
				Endpoint: "localhost:4317",
				Headers: map[string]string{
					"Authorization": "Bearer token",
				},
				Timeout:  10 * time.Second,
				Insecure: true,
			},
		}

		tracer, err := NewTracer(context.Background(), dag, nil)
		require.NoError(t, err)
		assert.NotNil(t, tracer)
		assert.True(t, tracer.IsEnabled())

		// Clean up
		err = tracer.Shutdown(context.Background())
		assert.NoError(t, err)
	})

	t.Run("WithResourceAttributes", func(t *testing.T) {
		dag := &ir.DAG{
			Name: "test-dag",
			OTel: &ir.OTelConfig{
				Enabled:  true,
				Endpoint: "localhost:4317",
				Resource: map[string]any{
					"service.name":           "dagu-test",
					"service.version":        "1.0.0",
					"deployment.environment": "test",
					"custom.int":             42,
					"custom.float":           3.14,
					"custom.bool":            true,
				},
			},
		}

		tracer, err := NewTracer(context.Background(), dag, nil)
		require.NoError(t, err)
		assert.NotNil(t, tracer)
		assert.True(t, tracer.IsEnabled())

		// Clean up
		err = tracer.Shutdown(context.Background())
		assert.NoError(t, err)
	})
}

func TestIsHTTPEndpoint(t *testing.T) {
	tests := []struct {
		endpoint string
		want     bool
	}{
		{"localhost:4317", false},
		{"http://localhost:4317", false},
		{"localhost:4318/v1/traces", true},
		{"http://collector:4318/v1/traces", true},
		{"https://collector.example.com/v1/traces", true},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, isHTTPEndpoint(tt.endpoint), tt.endpoint)
	}
}

// An endpoint that ends with /v1/traces must reach an OTLP/HTTP receiver,
// with or without the URL scheme.
func TestHTTPExporter(t *testing.T) {
	tests := []struct {
		name     string
		endpoint func(serverURL string) string
	}{
		{
			name:     "URL",
			endpoint: func(serverURL string) string { return serverURL + "/v1/traces" },
		},
		{
			name: "HostPortPath",
			endpoint: func(serverURL string) string {
				return strings.TrimPrefix(serverURL, "http://") + "/v1/traces"
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requests := make(chan string, 16)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests <- r.Method + " " + r.URL.Path
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()

			dag := &ir.DAG{
				Name: "test-dag",
				OTel: &ir.OTelConfig{
					Enabled:  true,
					Endpoint: tt.endpoint(server.URL),
					Insecure: true,
					Timeout:  2 * time.Second,
				},
			}

			tracer, err := NewTracer(context.Background(), dag, nil)
			require.NoError(t, err)

			_, span := tracer.Start(context.Background(), "test-span")
			span.End()
			require.NoError(t, tracer.Shutdown(context.Background()))

			select {
			case got := <-requests:
				assert.Equal(t, "POST /v1/traces", got)
			default:
				t.Fatal("the OTLP/HTTP receiver got no request")
			}
		})
	}
}

func TestHTTPExporterInvalidScheme(t *testing.T) {
	dag := &ir.DAG{
		Name: "test-dag",
		OTel: &ir.OTelConfig{
			Enabled:  true,
			Endpoint: "ftp://collector:4318/v1/traces",
		},
	}

	tracer, err := NewTracer(context.Background(), dag, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "scheme must be http or https")
	assert.Nil(t, tracer)
}

func TestTracerStart(t *testing.T) {
	t.Run("WithEnabledTracer", func(t *testing.T) {
		dag := &ir.DAG{
			Name: "test-dag",
			OTel: &ir.OTelConfig{
				Enabled:  true,
				Endpoint: "localhost:4317",
			},
		}

		tracer, err := NewTracer(context.Background(), dag, nil)
		require.NoError(t, err)
		defer func() {
			_ = tracer.Shutdown(context.Background())
		}()

		ctx, span := tracer.Start(context.Background(), "test-span")
		assert.NotNil(t, ctx)
		assert.NotNil(t, span)

		// Span should be valid
		assert.True(t, span.SpanContext().IsValid())
		span.End()
	})

	t.Run("WithDisabledTracer", func(t *testing.T) {
		dag := &ir.DAG{
			Name: "test-dag",
			OTel: nil,
		}

		tracer, err := NewTracer(context.Background(), dag, nil)
		require.NoError(t, err)

		ctx, span := tracer.Start(context.Background(), "test-span")
		assert.NotNil(t, ctx)
		assert.NotNil(t, span)

		// Should return a no-op span
		assert.False(t, span.SpanContext().IsValid())
		span.End()
	})
}
