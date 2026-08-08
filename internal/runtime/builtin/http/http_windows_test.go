// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package http

import (
	"context"
	"encoding/json"
	"fmt"
	nethttp "net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHTTPExecutor_WindowsSpecific tests the HTTP executor specifically on Windows
func TestHTTPExecutor_WindowsSpecific(t *testing.T) {
	// Ensure we're actually running on Windows
	require.Equal(t, "windows", runtime.GOOS, "This test should only run on Windows")

	t.Run("BasicGETRequestOnWindows", func(t *testing.T) {
		server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
			assert.Equal(t, "GET", r.Method)
			w.WriteHeader(nethttp.StatusOK)
			_, _ = w.Write([]byte("Hello from Windows test server"))
		}))
		defer server.Close()

		step := ir.Step{
			Command: "GET",
			Args:    []string{server.URL},
			ExecutorConfig: ir.ExecutorConfig{
				Type: "http",
				Config: map[string]any{
					"silent": true,
				},
			},
		}

		executor, err := newHTTP(context.Background(), step)
		require.NoError(t, err)

		out := &testWriter{}
		httpExec, ok := executor.(*http)
		require.True(t, ok)
		httpExec.SetStdout(out)
		httpExec.SetStderr(&testWriter{})

		err = httpExec.Run(context.Background())
		assert.NoError(t, err)
		assert.Equal(t, "Hello from Windows test server", out.String())
	})

	t.Run("POSTRequestWithJSONBodyOnWindows", func(t *testing.T) {
		var receivedBody map[string]interface{}
		server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
			assert.Equal(t, "POST", r.Method)
			assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

			err := json.NewDecoder(r.Body).Decode(&receivedBody)
			require.NoError(t, err)

			w.WriteHeader(nethttp.StatusCreated)
			response := map[string]interface{}{
				"received": receivedBody,
				"status":   "created",
			}
			_ = json.NewEncoder(w).Encode(response)
		}))
		defer server.Close()

		requestBody := map[string]interface{}{
			"name":    "test",
			"value":   123,
			"windows": true,
		}
		bodyBytes, _ := json.Marshal(requestBody)

		step := ir.Step{
			Command: "POST",
			Args:    []string{server.URL},
			ExecutorConfig: ir.ExecutorConfig{
				Type: "http",
				Config: map[string]any{
					"headers": map[string]string{
						"Content-Type": "application/json",
					},
					"body":   string(bodyBytes),
					"silent": false, // Don't suppress status info for JSON POST test
					"json":   true,
				},
			},
		}

		executor, err := newHTTP(context.Background(), step)
		require.NoError(t, err)

		out := &testWriter{}
		httpExec, ok := executor.(*http)
		require.True(t, ok)
		httpExec.SetStdout(out)
		httpExec.SetStderr(&testWriter{})

		err = httpExec.Run(context.Background())
		assert.NoError(t, err)

		// Verify the received body on server side
		assert.Equal(t, "test", receivedBody["name"])
		assert.Equal(t, float64(123), receivedBody["value"]) // JSON numbers become float64
		assert.Equal(t, true, receivedBody["windows"])

		// Verify JSON response format
		var response httpJSONResult
		err = json.Unmarshal([]byte(out.String()), &response)
		assert.NoError(t, err)
		assert.Equal(t, 201, response.StatusCode)
		assert.NotNil(t, response.Body)
	})

	t.Run("HeadersAndQueryParametersOnWindows", func(t *testing.T) {
		server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
			// Verify custom headers
			assert.Equal(t, "Bearer windows-token", r.Header.Get("Authorization"))
			assert.Equal(t, "Dagu-Windows/1.0", r.Header.Get("User-Agent"))

			// Verify query parameters
			assert.Equal(t, "windows", r.URL.Query().Get("os"))
			assert.Equal(t, "test", r.URL.Query().Get("environment"))

			w.WriteHeader(nethttp.StatusOK)
			_, _ = w.Write([]byte("Headers and params received"))
		}))
		defer server.Close()

		step := ir.Step{
			Command: "GET",
			Args:    []string{server.URL},
			ExecutorConfig: ir.ExecutorConfig{
				Type: "http",
				Config: map[string]any{
					"headers": map[string]string{
						"Authorization": "Bearer windows-token",
						"User-Agent":    "Dagu-Windows/1.0",
					},
					"query": map[string]string{
						"os":          "windows",
						"environment": "test",
					},
					"silent": true,
				},
			},
		}

		executor, err := newHTTP(context.Background(), step)
		require.NoError(t, err)

		out := &testWriter{}
		httpExec, ok := executor.(*http)
		require.True(t, ok)
		httpExec.SetStdout(out)
		httpExec.SetStderr(&testWriter{})

		err = httpExec.Run(context.Background())
		assert.NoError(t, err)
		assert.Equal(t, "Headers and params received", out.String())
	})

	t.Run("TimeoutHandlingOnWindows", func(t *testing.T) {
		server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
			// Simulate slow response (longer than timeout)
			time.Sleep(3 * time.Second)
			w.WriteHeader(nethttp.StatusOK)
			_, _ = w.Write([]byte("This should timeout"))
		}))
		defer server.Close()

		step := ir.Step{
			Command: "GET",
			Args:    []string{server.URL},
			ExecutorConfig: ir.ExecutorConfig{
				Type: "http",
				Config: map[string]any{
					"timeout": 1, // 1 second timeout
					"silent":  true,
				},
			},
		}

		executor, err := newHTTP(context.Background(), step)
		require.NoError(t, err)

		httpExec, ok := executor.(*http)
		require.True(t, ok)
		httpExec.SetStdout(&testWriter{})
		httpExec.SetStderr(&testWriter{})

		start := time.Now()
		err = httpExec.Run(context.Background())
		duration := time.Since(start)

		// Should timeout and return an error
		assert.Error(t, err)
		assert.Contains(t, strings.ToLower(err.Error()), "timeout")
		// Should complete within reasonable time (not wait for full 3 seconds)
		assert.Less(t, duration, 2*time.Second)
	})

	t.Run("ErrorStatusCodesOnWindows", func(t *testing.T) {
		testCases := []struct {
			statusCode  int
			expectError bool
		}{
			{200, false},
			{201, false},
			{299, false},
			{400, true},
			{401, true},
			{404, true},
			{500, true},
		}

		for _, tc := range testCases {
			t.Run(fmt.Sprintf("Status_%d", tc.statusCode), func(t *testing.T) {
				server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
					w.WriteHeader(tc.statusCode)
					_, _ = w.Write([]byte(fmt.Sprintf("Status: %d", tc.statusCode)))
				}))
				defer server.Close()

				step := ir.Step{
					Command: "GET",
					Args:    []string{server.URL},
					ExecutorConfig: ir.ExecutorConfig{
						Type: "http",
						Config: map[string]any{
							"silent": false, // Don't suppress output for error cases
						},
					},
				}

				executor, err := newHTTP(context.Background(), step)
				require.NoError(t, err)

				out := &testWriter{}
				httpExec, ok := executor.(*http)
				require.True(t, ok)
				httpExec.SetStdout(out)
				httpExec.SetStderr(&testWriter{})

				err = httpExec.Run(context.Background())

				if tc.expectError {
					assert.Error(t, err)
					assert.Contains(t, err.Error(), "http status code not 2xx")
				} else {
					assert.NoError(t, err)
				}

				// Output should contain status information
				output := out.String()
				assert.Contains(t, output, fmt.Sprintf("%d", tc.statusCode))
			})
		}
	})

	t.Run("HTTPSWithSelfSignedCertificateOnWindows", func(t *testing.T) {
		server := httptest.NewTLSServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
			w.WriteHeader(nethttp.StatusOK)
			_, _ = w.Write([]byte("HTTPS on Windows works"))
		}))
		defer server.Close()

		// Test with skip_tls_verify = true (should work)
		step := ir.Step{
			Command: "GET",
			Args:    []string{server.URL},
			ExecutorConfig: ir.ExecutorConfig{
				Type: "http",
				Config: map[string]any{
					"skip_tls_verify": true,
					"silent":          true,
				},
			},
		}

		executor, err := newHTTP(context.Background(), step)
		require.NoError(t, err)

		out := &testWriter{}
		httpExec, ok := executor.(*http)
		require.True(t, ok)
		httpExec.SetStdout(out)
		httpExec.SetStderr(&testWriter{})

		err = httpExec.Run(context.Background())
		assert.NoError(t, err)
		assert.Equal(t, "HTTPS on Windows works", out.String())
	})

	t.Run("ProcessCancellationOnWindows", func(t *testing.T) {
		server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
			// Simulate long-running request
			time.Sleep(5 * time.Second)
			w.WriteHeader(nethttp.StatusOK)
			_, _ = w.Write([]byte("Should not reach here"))
		}))
		defer server.Close()

		step := ir.Step{
			Command: "GET",
			Args:    []string{server.URL},
			ExecutorConfig: ir.ExecutorConfig{
				Type: "http",
				Config: map[string]any{
					"silent": true,
				},
			},
		}

		executor, err := newHTTP(context.Background(), step)
		require.NoError(t, err)

		httpExec, ok := executor.(*http)
		require.True(t, ok)
		httpExec.SetStdout(&testWriter{})
		httpExec.SetStderr(&testWriter{})

		// Start the request in a goroutine
		done := make(chan error, 1)
		go func() {
			done <- httpExec.Run(context.Background())
		}()

		// Wait a bit, then kill the process
		time.Sleep(100 * time.Millisecond)
		err = httpExec.Kill(nil) // Signal doesn't matter for HTTP executor
		assert.NoError(t, err)

		// Should complete quickly due to cancellation
		select {
		case runErr := <-done:
			assert.Error(t, runErr)
			assert.Contains(t, strings.ToLower(runErr.Error()), "cancel")
		case <-time.After(2 * time.Second):
			t.Fatal("Request should have been cancelled but didn't complete in time")
		}
	})
}

// TestHTTPExecutor_WindowsPerformance measures basic performance metrics on Windows
func TestHTTPExecutor_WindowsPerformance(t *testing.T) {
	require.Equal(t, "windows", runtime.GOOS, "This test should only run on Windows")

	server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		w.WriteHeader(nethttp.StatusOK)
		_, _ = w.Write([]byte("Performance test response"))
	}))
	defer server.Close()

	step := ir.Step{
		Command: "GET",
		Args:    []string{server.URL},
		ExecutorConfig: ir.ExecutorConfig{
			Type: "http",
			Config: map[string]any{
				"silent": true,
			},
		},
	}

	// Measure execution time for multiple requests
	const numRequests = 10
	var totalDuration time.Duration

	for i := 0; i < numRequests; i++ {
		executor, err := newHTTP(context.Background(), step)
		require.NoError(t, err)

		httpExec, ok := executor.(*http)
		require.True(t, ok)
		httpExec.SetStdout(&testWriter{})
		httpExec.SetStderr(&testWriter{})

		start := time.Now()
		err = httpExec.Run(context.Background())
		duration := time.Since(start)

		require.NoError(t, err)
		totalDuration += duration
	}

	avgDuration := totalDuration / numRequests
	t.Logf("Windows HTTP executor average execution time: %v", avgDuration)

	// Performance should be reasonable (less than 1 second for local requests)
	assert.Less(t, avgDuration, 1*time.Second,
		"HTTP executor performance on Windows seems slow: %v average", avgDuration)
}
