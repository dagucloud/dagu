// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package http

import (
	"context"
	"encoding/json"
	"io"
	"maps"
	nethttp "net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/executor/registry"
	"github.com/dagucloud/dagu/v2/internal/ir"
	daguruntime "github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHTTPExecutor_SkipTLSVerify(t *testing.T) {
	t.Run("HTTPSRequestWithSelfSignedCertificate", func(t *testing.T) {
		// Create a test server with a self-signed certificate
		server := httptest.NewTLSServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
			w.WriteHeader(nethttp.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"message": "success",
			})
		}))
		defer server.Close()

		// Test with skip_tls_verify = true (should succeed)
		step := ir.Step{
			Commands: []ir.CommandEntry{{Command: "GET", Args: []string{server.URL + "/test"}}},
			ExecutorConfig: ir.ExecutorConfig{
				Type: "http",
				Config: map[string]any{
					"skip_tls_verify": true,
					"silent":          true,
					"format":          "json",
				},
			},
		}

		executor, err := newHTTP(context.Background(), step)
		require.NoError(t, err)

		httpExec, ok := executor.(*http)
		require.True(t, ok)
		httpExec.SetStdout(&testWriter{})
		httpExec.SetStderr(&testWriter{})

		err = httpExec.Run(context.Background())
		assert.NoError(t, err)
	})

	t.Run("HTTPSRequestWithoutSkipTLSVerify", func(t *testing.T) {
		// Create a test server with a self-signed certificate
		server := httptest.NewTLSServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
			w.WriteHeader(nethttp.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"message": "success",
			})
		}))
		defer server.Close()

		// Test with skip_tls_verify = false (should fail due to certificate verification)
		step := ir.Step{
			Commands: []ir.CommandEntry{{Command: "GET", Args: []string{server.URL + "/test"}}},
			ExecutorConfig: ir.ExecutorConfig{
				Type: "http",
				Config: map[string]any{
					"skip_tls_verify": false,
					"silent":          true,
				},
			},
		}

		executor, err := newHTTP(context.Background(), step)
		require.NoError(t, err)

		httpExec, ok := executor.(*http)
		require.True(t, ok)
		httpExec.SetStdout(&testWriter{})
		httpExec.SetStderr(&testWriter{})

		err = httpExec.Run(context.Background())
		// Should fail with certificate verification error
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "certificate")
	})

	t.Run("ConfigParsingWithSkipTLSVerify", func(t *testing.T) {
		step := ir.Step{
			Commands: []ir.CommandEntry{{Command: "GET", Args: []string{"https://example.com"}}},
			Script: `{
				"skip_tls_verify": true,
				"timeout": 30,
				"headers": {"Authorization": "Bearer token"}
			}`,
		}

		executor, err := newHTTP(context.Background(), step)
		require.NoError(t, err)

		httpExec, ok := executor.(*http)
		require.True(t, ok)
		assert.True(t, httpExec.cfg.SkipTLSVerify)
		assert.Equal(t, 30, httpExec.cfg.Timeout)
		assert.Equal(t, "Bearer token", httpExec.cfg.Headers["Authorization"])
	})
}

func TestHTTPExecutor_StandardFeatures(t *testing.T) {
	t.Run("GETRequestWithHeadersAndQueryParams", func(t *testing.T) {
		server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
			assert.Equal(t, "GET", r.Method)
			assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
			assert.Equal(t, "value1", r.URL.Query().Get("param1"))

			w.WriteHeader(nethttp.StatusOK)
			_, _ = w.Write([]byte("test response"))
		}))
		defer server.Close()

		step := ir.Step{
			Commands: []ir.CommandEntry{{Command: "GET", Args: []string{server.URL}}},
			ExecutorConfig: ir.ExecutorConfig{
				Type: "http",
				Config: map[string]any{
					"headers": map[string]string{
						"Authorization": "Bearer test-token",
					},
					"query": map[string]string{
						"param1": "value1",
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
		assert.Equal(t, "test response", out.String())
	})
}

func TestHTTPExecutor_MultipartUpload(t *testing.T) {
	t.Parallel()

	type receivedRequest struct {
		contentType string
		fileName    string
		fileBody    string
		description string
		err         error
	}

	received := make(chan receivedRequest, 1)
	server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		result := receivedRequest{contentType: r.Header.Get("Content-Type")}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			result.err = err
			received <- result
			nethttp.Error(w, err.Error(), nethttp.StatusBadRequest)
			return
		}

		file, header, err := r.FormFile("document")
		if err != nil {
			result.err = err
			received <- result
			nethttp.Error(w, err.Error(), nethttp.StatusBadRequest)
			return
		}
		defer func() { _ = file.Close() }()

		body, err := io.ReadAll(file)
		if err != nil {
			result.err = err
			received <- result
			nethttp.Error(w, err.Error(), nethttp.StatusInternalServerError)
			return
		}

		result.fileName = header.Filename
		result.fileBody = string(body)
		result.description = r.FormValue("description")
		received <- result
		w.WriteHeader(nethttp.StatusOK)
		_, _ = w.Write([]byte("uploaded"))
	}))
	defer server.Close()

	filePath := filepath.Join(t.TempDir(), "report.txt")
	require.NoError(t, os.WriteFile(filePath, []byte("report contents"), 0o600))

	step := ir.Step{
		Commands: []ir.CommandEntry{{Command: "POST", Args: []string{server.URL}}},
		ExecutorConfig: ir.ExecutorConfig{
			Type: "http",
			Config: map[string]any{
				"files":  map[string]string{"document": filePath},
				"form":   map[string]string{"description": "nightly report"},
				"silent": true,
			},
		},
	}

	exec, err := newHTTP(context.Background(), step)
	require.NoError(t, err)

	out := &testWriter{}
	httpExec, ok := exec.(*http)
	require.True(t, ok)
	httpExec.SetStdout(out)
	httpExec.SetStderr(&testWriter{})

	require.NoError(t, httpExec.Run(context.Background()))
	result := <-received
	require.NoError(t, result.err)
	assert.Contains(t, result.contentType, "multipart/form-data; boundary=")
	assert.Equal(t, "report.txt", result.fileName)
	assert.Equal(t, "report contents", result.fileBody)
	assert.Equal(t, "nightly report", result.description)
	assert.Equal(t, "uploaded", out.String())
}

func TestHTTPExecutor_MultipartUploadResolvesRelativeFilePath(t *testing.T) {
	t.Parallel()

	received := make(chan string, 1)
	server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		file, _, err := r.FormFile("payload")
		if err != nil {
			nethttp.Error(w, err.Error(), nethttp.StatusBadRequest)
			return
		}
		defer func() { _ = file.Close() }()

		body, err := io.ReadAll(file)
		if err != nil {
			nethttp.Error(w, err.Error(), nethttp.StatusInternalServerError)
			return
		}
		received <- string(body)
		w.WriteHeader(nethttp.StatusNoContent)
	}))
	defer server.Close()

	workingDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(workingDir, "inputs"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(workingDir, "inputs", "payload.txt"), []byte("payload contents"), 0o600))

	ctx := daguruntime.WithEnv(context.Background(), daguruntime.Env{WorkingDir: workingDir})
	step := ir.Step{
		Commands: []ir.CommandEntry{{Command: "POST", Args: []string{server.URL}}},
		ExecutorConfig: ir.ExecutorConfig{
			Type: "http",
			Config: map[string]any{
				"files":  map[string]string{"payload": filepath.Join("inputs", "payload.txt")},
				"silent": true,
			},
		},
	}

	exec, err := newHTTP(ctx, step)
	require.NoError(t, err)

	httpExec, ok := exec.(*http)
	require.True(t, ok)
	httpExec.SetStdout(&testWriter{})
	httpExec.SetStderr(&testWriter{})

	require.NoError(t, httpExec.Run(context.Background()))
	assert.Equal(t, "payload contents", <-received)
}

func TestHTTPExecutor_MultipartUploadRejectsBody(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		multipart map[string]any
	}{
		{
			name:      "File",
			multipart: map[string]any{"files": map[string]string{"document": "report.txt"}},
		},
		{
			name:      "FormField",
			multipart: map[string]any{"form": map[string]string{"description": "report"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			config := map[string]any{"body": `{"name":"report"}`}
			maps.Copy(config, tt.multipart)

			_, err := newHTTP(context.Background(), ir.Step{
				Commands: []ir.CommandEntry{{Command: "POST", Args: []string{"https://example.com/upload"}}},
				ExecutorConfig: ir.ExecutorConfig{
					Type:   "http",
					Config: config,
				},
			})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "body cannot be combined with form or files")
		})
	}
}

func TestHTTPExecutor_MultipartSchemaRejectsNonStringValues(t *testing.T) {
	t.Parallel()

	for _, field := range []string{"form", "files"} {
		t.Run(field, func(t *testing.T) {
			t.Parallel()

			err := registry.ValidateExecutorConfig("http", map[string]any{
				field: map[string]any{"document": 123},
			})
			require.Error(t, err)
		})
	}
}

// A misspelled `with` key must fail validation instead of silently dropping
// the headers (#3440). The closed schema also suggests the closest valid key.
func TestHTTPExecutor_RejectsUnknownWithKey(t *testing.T) {
	t.Parallel()

	err := registry.ValidateExecutorConfig("http", map[string]any{
		"method": "GET", "url": "https://example.com", "header": map[string]any{"X-Test": "1"},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), `did you mean "headers" instead of "header"?`)

	require.NoError(t, registry.ValidateExecutorConfig("http", map[string]any{
		"method": "GET", "url": "https://example.com", "headers": map[string]any{"X-Test": "1"},
	}))
}

// status_exit_codes keys are a status code (300-599) or class (3xx-5xx), and
// values are exit codes (0-255), so dagu validate rejects anything else.
func TestHTTPExecutor_StatusExitCodesSchema(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		codes   map[string]any
		wantErr bool
	}{
		{name: "code and class", codes: map[string]any{"404": 0, "5xx": 3}},
		{name: "code out of range", codes: map[string]any{"600": 1}, wantErr: true},
		{name: "success code", codes: map[string]any{"204": 1}, wantErr: true},
		{name: "success class", codes: map[string]any{"2xx": 1}, wantErr: true},
		{name: "informational class", codes: map[string]any{"1xx": 1}, wantErr: true},
		{name: "unknown class", codes: map[string]any{"6xx": 1}, wantErr: true},
		{name: "partial class", codes: map[string]any{"40x": 1}, wantErr: true},
		{name: "exit code too high", codes: map[string]any{"404": 256}, wantErr: true},
		{name: "negative exit code", codes: map[string]any{"404": -1}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := registry.ValidateExecutorConfig("http", map[string]any{
				"method": "GET", "url": "https://example.com", "status_exit_codes": tc.codes,
			})
			if tc.wantErr {
				require.ErrorContains(t, err, "status_exit_codes")
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestHTTPExecutor_MissingMethodOrURLReturnsConfigError(t *testing.T) {
	t.Run("MissingURL", func(t *testing.T) {
		step := ir.Step{
			Commands: []ir.CommandEntry{{Command: "GET"}},
			ExecutorConfig: ir.ExecutorConfig{
				Type: "http",
			},
		}

		_, err := newHTTP(context.Background(), step)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "url is required")
	})

	t.Run("MissingMethod", func(t *testing.T) {
		step := ir.Step{
			ExecutorConfig: ir.ExecutorConfig{
				Type: "http",
				Config: map[string]any{
					"url": "https://example.com",
				},
			},
		}

		_, err := newHTTP(context.Background(), step)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "method is required")
	})
}

// TestHTTPExecutor_CrossPlatform tests behavior across different platforms
func TestHTTPExecutor_CrossPlatform(t *testing.T) {
	t.Run("BehaviorConsistencyAcrossPlatforms", func(t *testing.T) {
		// Test data that should behave the same on all platforms
		testCases := []struct {
			name         string
			method       string
			config       map[string]any
			expectedBody string
		}{
			{
				name:   "SimpleGET",
				method: "GET",
				config: map[string]any{"silent": true},
			},
			{
				name:         "POSTWithJSON",
				method:       "POST",
				expectedBody: `{"test": "data"}`,
				config: map[string]any{
					"headers": map[string]string{"Content-Type": "application/json"},
					"body":    `{"test": "data"}`,
					"silent":  true,
				},
			},
			{
				name:   "GETWithCustomHeaders",
				method: "GET",
				config: map[string]any{
					"headers": map[string]string{
						"User-Agent":    "Dagu-Test/1.0",
						"Accept":        "application/json",
						"Custom-Header": "cross-platform-test",
					},
					"silent": true,
				},
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
					body, err := io.ReadAll(r.Body)
					if err != nil {
						nethttp.Error(w, err.Error(), nethttp.StatusInternalServerError)
						return
					}

					// Echo back some request info
					response := map[string]any{
						"body":     string(body),
						"method":   r.Method,
						"platform": runtime.GOOS,
						"headers":  r.Header,
						"url":      r.URL.String(),
					}

					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(nethttp.StatusOK)
					_ = json.NewEncoder(w).Encode(response)
				}))
				defer server.Close()

				step := ir.Step{
					Commands: []ir.CommandEntry{{Command: tc.method, Args: []string{server.URL}}},
					ExecutorConfig: ir.ExecutorConfig{
						Type:   "http",
						Config: tc.config,
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

				// Parse response to verify it contains expected data
				var response map[string]any
				err = json.Unmarshal([]byte(out.String()), &response)
				assert.NoError(t, err)
				assert.Equal(t, tc.expectedBody, response["body"])
				assert.Equal(t, tc.method, response["method"])
				assert.Equal(t, runtime.GOOS, response["platform"])

				// Log platform-specific information for comparison
				t.Logf("Platform: %s, Method: %s, Response length: %d",
					runtime.GOOS, tc.method, len(out.String()))
			})
		}
	})

	t.Run("JSONResponseFormattingConsistency", func(t *testing.T) {
		server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Test-Header", "cross-platform")
			w.WriteHeader(nethttp.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"message":  "JSON response test",
				"platform": runtime.GOOS,
			})
		}))
		defer server.Close()

		step := ir.Step{
			Commands: []ir.CommandEntry{{Command: "GET", Args: []string{server.URL}}},
			ExecutorConfig: ir.ExecutorConfig{
				Type: "http",
				Config: map[string]any{
					"format": "json",
					"silent": false, // Don't suppress headers for this test
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

		// Verify JSON response structure is consistent
		var jsonResponse httpJSONResult
		err = json.Unmarshal([]byte(out.String()), &jsonResponse)
		assert.NoError(t, err)
		assert.Equal(t, 200, jsonResponse.StatusCode)
		assert.NotEmpty(t, jsonResponse.Headers)
		assert.Contains(t, jsonResponse.Headers, "Content-Type")
		assert.Contains(t, jsonResponse.Headers, "X-Test-Header")

		// Verify response body structure
		bodyMap, ok := jsonResponse.Body.(map[string]any)
		assert.True(t, ok)
		assert.Equal(t, "JSON response test", bodyMap["message"])
		assert.Equal(t, runtime.GOOS, bodyMap["platform"])

		t.Logf("Platform: %s, JSON response verified", runtime.GOOS)
	})

	t.Run("ErrorHandlingConsistency", func(t *testing.T) {
		server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
			w.WriteHeader(nethttp.StatusInternalServerError)
			_, _ = w.Write([]byte("Server error for cross-platform test"))
		}))
		defer server.Close()

		step := ir.Step{
			Commands: []ir.CommandEntry{{Command: "GET", Args: []string{server.URL}}},
			ExecutorConfig: ir.ExecutorConfig{
				Type: "http",
				Config: map[string]any{
					"silent": false,
				},
			},
		}

		executor, err := newHTTP(context.Background(), step)
		require.NoError(t, err)

		out := &testWriter{}
		stderr := &testWriter{}
		httpExec, ok := executor.(*http)
		require.True(t, ok)
		httpExec.SetStdout(out)
		httpExec.SetStderr(stderr)

		err = httpExec.Run(context.Background())

		// Error handling should be consistent across platforms
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "http status code not 2xx")
		assert.Contains(t, err.Error(), "500")

		// Output should contain status information
		output := out.String()
		assert.Contains(t, output, "500")
		assert.Contains(t, output, "Internal Server Error")

		t.Logf("Platform: %s, Error handling verified", runtime.GOOS)
	})

	t.Run("LegacyJSONBooleanCompatibility", func(t *testing.T) {
		server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(nethttp.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "legacy"})
		}))
		defer server.Close()

		step := ir.Step{
			Commands: []ir.CommandEntry{{Command: "GET", Args: []string{server.URL}}},
			ExecutorConfig: ir.ExecutorConfig{
				Type: "http",
				Config: map[string]any{
					"json": true,
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

		var jsonResponse httpJSONResult
		err = json.Unmarshal([]byte(out.String()), &jsonResponse)
		assert.NoError(t, err)
		assert.Equal(t, 200, jsonResponse.StatusCode)
	})
}

func TestHTTPExecutor_CmdWithArgsExpansion(t *testing.T) {
	t.Run("MethodFromCmdWithArgs", func(t *testing.T) {
		// Simulates the post-expansion state where CmdWithArgs is expanded
		// but Command still contains the unexpanded variable (issue #1722).
		server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
			assert.Equal(t, "GET", r.Method)
			w.WriteHeader(nethttp.StatusOK)
			_, _ = w.Write([]byte("ok"))
		}))
		defer server.Close()

		step := ir.Step{
			Commands: []ir.CommandEntry{{
				Command:     "${METHOD}",          // unexpanded
				Args:        []string{server.URL}, // expanded
				CmdWithArgs: "GET " + server.URL,  // expanded (as evaluateCommandArgs does)
			}},
			ExecutorConfig: ir.ExecutorConfig{
				Type:   "http",
				Config: map[string]any{"silent": true},
			},
		}

		exec, err := newHTTP(context.Background(), step)
		require.NoError(t, err)

		httpExec, ok := exec.(*http)
		require.True(t, ok)
		assert.Equal(t, "GET", httpExec.method)
		assert.Equal(t, server.URL, httpExec.url)

		httpExec.SetStdout(&testWriter{})
		httpExec.SetStderr(&testWriter{})

		err = httpExec.Run(context.Background())
		assert.NoError(t, err)
	})

	t.Run("FallbackToCommandWhenCmdWithArgsEmpty", func(t *testing.T) {
		// When CmdWithArgs is empty, fall back to Command/Args fields.
		server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
			assert.Equal(t, "POST", r.Method)
			w.WriteHeader(nethttp.StatusOK)
			_, _ = w.Write([]byte("ok"))
		}))
		defer server.Close()

		step := ir.Step{
			Commands: []ir.CommandEntry{{
				Command: "POST",
				Args:    []string{server.URL},
			}},
			ExecutorConfig: ir.ExecutorConfig{
				Type:   "http",
				Config: map[string]any{"silent": true},
			},
		}

		exec, err := newHTTP(context.Background(), step)
		require.NoError(t, err)

		httpExec, ok := exec.(*http)
		require.True(t, ok)
		assert.Equal(t, "POST", httpExec.method)
		assert.Equal(t, server.URL, httpExec.url)

		httpExec.SetStdout(&testWriter{})
		httpExec.SetStderr(&testWriter{})

		err = httpExec.Run(context.Background())
		assert.NoError(t, err)
	})

	t.Run("FallbackToLegacyStepFields", func(t *testing.T) {
		server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
			assert.Equal(t, "GET", r.Method)
			w.WriteHeader(nethttp.StatusOK)
			_, _ = w.Write([]byte("ok"))
		}))
		defer server.Close()

		step := ir.Step{
			Command: "GET",
			Args:    []string{server.URL},
			ExecutorConfig: ir.ExecutorConfig{
				Type:   "http",
				Config: map[string]any{"silent": true},
			},
		}

		exec, err := newHTTP(context.Background(), step)
		require.NoError(t, err)

		httpExec, ok := exec.(*http)
		require.True(t, ok)
		assert.Equal(t, "GET", httpExec.method)
		assert.Equal(t, server.URL, httpExec.url)

		httpExec.SetStdout(&testWriter{})
		httpExec.SetStderr(&testWriter{})

		err = httpExec.Run(context.Background())
		assert.NoError(t, err)
	})
}

// A silent success prints only the body; status_code and headers stay in
// the output when silent is off or the response is not a success. A JSON
// null body stays as "body": null, and with.output replaces the body.
func TestHTTPExecutor_JSONOutputFields(t *testing.T) {
	okBody := map[string]any{"ok": true}
	for _, tc := range []struct {
		name     string
		status   int
		response string
		silent   bool
		output   bool
		keys     []string
		body     any
	}{
		{"SilentSuccess", nethttp.StatusAccepted, `{"ok":true}`, true, false, []string{"body"}, okBody},
		{"Success", nethttp.StatusAccepted, `{"ok":true}`, false, false, []string{"body", "headers", "status_code"}, okBody},
		{"SilentError", nethttp.StatusServiceUnavailable, `{"ok":true}`, true, false, []string{"body", "headers", "status_code"}, okBody},
		{"SilentNullBody", nethttp.StatusOK, `null`, true, false, []string{"body"}, nil},
		{"NullBody", nethttp.StatusOK, `null`, false, false, []string{"body", "headers", "status_code"}, nil},
		{"SilentNullBodyToFile", nethttp.StatusOK, `null`, true, true, []string{"output"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.response))
			}))
			defer server.Close()

			cfg := map[string]any{
				"format": "json",
				"silent": tc.silent,
			}
			if tc.output {
				cfg["output"] = filepath.Join(t.TempDir(), "response.json")
			}
			step := ir.Step{
				Commands: []ir.CommandEntry{{Command: "POST", Args: []string{server.URL}}},
				ExecutorConfig: ir.ExecutorConfig{
					Type:   "http",
					Config: cfg,
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
			if tc.status >= 300 {
				require.ErrorIs(t, err, errHTTPStatusCode)
			} else {
				require.NoError(t, err)
			}

			var result map[string]any
			require.NoError(t, json.Unmarshal(out.data, &result))
			assert.ElementsMatch(t, tc.keys, slices.Collect(maps.Keys(result)))
			assert.Equal(t, tc.body, result["body"])
			if _, ok := result["status_code"]; ok {
				assert.InDelta(t, tc.status, result["status_code"], 0)
			}
		})
	}
}

type testWriter struct {
	data []byte
}

func (tw *testWriter) Write(p []byte) (n int, err error) {
	tw.data = append(tw.data, p...)
	return len(p), nil
}

func (tw *testWriter) String() string {
	return string(tw.data)
}

func TestHTTPExecutor_StatusExitCodes(t *testing.T) {
	t.Parallel()

	// wantExit 0 means the step succeeds.
	for _, tc := range []struct {
		name     string
		status   int
		mapCfg   map[string]any
		wantExit int
	}{
		{"ExactCodeMapsToZero", 409, map[string]any{"status_exit_codes": map[string]any{"409": 0}}, 0},
		{"ExactCodeMapsToNonZero", 404, map[string]any{"status_exit_codes": map[string]any{"404": 23}}, 23},
		{"ClassMapsToRetryable", 503, map[string]any{"status_exit_codes": map[string]any{"5xx": 75}}, 75},
		{"ExactWinsOverClass", 404, map[string]any{"status_exit_codes": map[string]any{"404": 0, "4xx": 66}}, 0},
		{"UnmappedKeepsDefault", 400, map[string]any{"status_exit_codes": map[string]any{"404": 0, "5xx": 75}}, 1},
		{"NoMapKeepsDefault", 500, nil, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
				w.WriteHeader(tc.status)
			}))
			defer server.Close()

			cfg := map[string]any{"url": server.URL}
			if tc.mapCfg != nil {
				maps.Copy(cfg, tc.mapCfg)
			}
			step := ir.Step{
				Commands:       []ir.CommandEntry{{Command: "GET"}},
				ExecutorConfig: ir.ExecutorConfig{Type: "http", Config: cfg},
			}

			executor, err := newHTTP(context.Background(), step)
			require.NoError(t, err)
			httpExec, ok := executor.(*http)
			require.True(t, ok)
			httpExec.SetStdout(&testWriter{})
			httpExec.SetStderr(&testWriter{})

			err = httpExec.Run(context.Background())
			if tc.wantExit == 0 {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, errHTTPStatusCode)
			assert.Equal(t, tc.wantExit, httpExec.ExitCode())
		})
	}
}

// A failed response need not carry JSON, so format: json keeps the mapped exit
// code when the error body is HTML or empty.
func TestHTTPExecutor_StatusExitCodesWithNonJSONBody(t *testing.T) {
	t.Parallel()

	for name, body := range map[string]string{"HTML": "<html>busy</html>", "Empty": ""} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
				w.WriteHeader(nethttp.StatusServiceUnavailable)
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()

			step := ir.Step{
				Commands: []ir.CommandEntry{{Command: "GET"}},
				ExecutorConfig: ir.ExecutorConfig{Type: "http", Config: map[string]any{
					"url":               server.URL,
					"format":            "json",
					"status_exit_codes": map[string]any{"5xx": 75},
				}},
			}

			executor, err := newHTTP(context.Background(), step)
			require.NoError(t, err)
			httpExec, ok := executor.(*http)
			require.True(t, ok)
			out := &testWriter{}
			httpExec.SetStdout(out)
			httpExec.SetStderr(&testWriter{})

			require.ErrorIs(t, httpExec.Run(context.Background()), errHTTPStatusCode)
			assert.Equal(t, 75, httpExec.ExitCode())
			var result map[string]any
			require.NoError(t, json.Unmarshal(out.data, &result))
			assert.InDelta(t, nethttp.StatusServiceUnavailable, result["status_code"], 0)
			assert.Equal(t, body, result["body"])
		})
	}
}

// A status mapped to 0 is a success, so the response is written as for a 2xx
// response and output receives the body.
func TestHTTPExecutor_StatusMappedToZeroWritesResponse(t *testing.T) {
	t.Parallel()

	const body = `{"error":"not here"}`
	for _, tc := range []struct {
		name       string
		format     string
		outputFile bool
	}{
		{name: "Text"},
		{name: "JSON", format: "json"},
		{name: "OutputFile", outputFile: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
				w.WriteHeader(nethttp.StatusNotFound)
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()

			output := filepath.Join(t.TempDir(), "response.out")
			cfg := map[string]any{
				"url":               server.URL,
				"format":            tc.format,
				"status_exit_codes": map[string]any{"404": 0},
			}
			if tc.outputFile {
				require.NoError(t, os.WriteFile(output, []byte("previous"), 0o600))
				cfg["output"] = output
			}
			step := ir.Step{
				Commands:       []ir.CommandEntry{{Command: "GET"}},
				ExecutorConfig: ir.ExecutorConfig{Type: "http", Config: cfg},
			}

			executor, err := newHTTP(context.Background(), step)
			require.NoError(t, err)
			out := &testWriter{}
			executor.SetStdout(out)
			executor.SetStderr(&testWriter{})

			require.NoError(t, executor.Run(context.Background()))
			if tc.outputFile {
				data, err := os.ReadFile(output)
				require.NoError(t, err)
				assert.Equal(t, body, string(data))
				return
			}
			assert.Contains(t, out.String(), "not here")
		})
	}
}

func TestHTTPExecutor_StatusExitCodesValidation(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		value  map[string]any
		script string
		ok     bool
	}{
		{name: "ValidExact", value: map[string]any{"status_exit_codes": map[string]any{"404": 0}}, ok: true},
		{name: "ValidClass", value: map[string]any{"status_exit_codes": map[string]any{"5xx": 75}}, ok: true},
		{name: "InvalidKey", value: map[string]any{"status_exit_codes": map[string]any{"999": 0}}},
		{name: "InvalidClass", value: map[string]any{"status_exit_codes": map[string]any{"9xx": 0}}},
		{name: "SuccessKey", value: map[string]any{"status_exit_codes": map[string]any{"200": 1}}},
		{name: "ValueOutOfRange", value: map[string]any{"status_exit_codes": map[string]any{"404": 256}}},
		{name: "ScriptInvalidKey", script: `{"status_exit_codes": {"999": 0}}`},
		{name: "ScriptValueOutOfRange", script: `{"status_exit_codes": {"404": 300}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			step := ir.Step{
				Commands:       []ir.CommandEntry{{Command: "GET", Args: []string{"https://example.com"}}},
				ExecutorConfig: ir.ExecutorConfig{Type: "http", Config: tc.value},
				Script:         tc.script,
			}
			_, err := newHTTP(context.Background(), step)
			if tc.ok {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}
