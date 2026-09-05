// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package spec045_http_request holds black-box conformance tests for
// Spec 045: HTTP Request Action.
package spec045_http_request_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

// startHTTPServer starts an httptest server running handler and registers
// its cleanup, returning its base URL.
func startHTTPServer(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server.URL
}

// stdoutLogPattern matches the per-step captured-stdout log path dagu start
// prints in its tree render, e.g. "└─stdout: /path/to/step.<ts>.<run>.out".
var stdoutLogPattern = regexp.MustCompile(`stdout: (.+)`)

// stepStdout reads the exact bytes a step wrote to stdout, by locating its
// captured-output log file from dagu start's own tree render and reading it
// directly. The tree render re-wraps long lines with its own indentation,
// which would corrupt a strict JSON parse or an exact-equality check, so
// assertions on precise step output read this file instead of
// result.Stdout().
func stepStdout(t *testing.T, daguStartOutput string) string {
	t.Helper()

	match := stdoutLogPattern.FindStringSubmatch(daguStartOutput)
	require.Lenf(t, match, 2, "expected a stdout log path in output:\n%s", daguStartOutput)
	path := strings.TrimSpace(match[1])
	data, err := os.ReadFile(path) // #nosec G304 -- path comes from the harness's own trusted output.
	require.NoError(t, err)
	return string(data)
}

func TestHTTPRequestLive(t *testing.T) {
	t.Run("sends headers and query parameters, and prints status and body", func(t *testing.T) {
		t.Parallel()

		var gotHeader, gotQuery string
		url := startHTTPServer(t, func(w http.ResponseWriter, r *http.Request) {
			gotHeader = r.Header.Get("X-Test-Header")
			gotQuery = r.URL.Query().Get("q")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("get response body"))
		})

		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv([]string{"HTTP_URL=" + url}, "start", "get_request.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, "header-value", gotHeader)
		require.Equal(t, "query-value", gotQuery)
		out := stepStdout(t, result.Stdout())
		require.Contains(t, out, "200 OK")
		require.Contains(t, out, "get response body")
	})

	t.Run("sends the request body", func(t *testing.T) {
		t.Parallel()

		var gotBody string
		url := startHTTPServer(t, func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			gotBody = string(body)
			w.WriteHeader(http.StatusCreated)
		})

		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv([]string{"HTTP_URL=" + url}, "start", "post_body.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, "request body content", gotBody)
	})

	t.Run("a non-2xx response fails the step but still prints status and body", func(t *testing.T) {
		t.Parallel()

		url := startHTTPServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("server error body"))
		})

		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv([]string{"HTTP_URL=" + url}, "start", "non_2xx_status.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("http status code not 2xx: 500")
		out := stepStdout(t, result.Stdout())
		require.Contains(t, out, "500 Internal Server Error")
		require.Contains(t, out, "server error body")
	})

	t.Run("silent suppresses status and headers only on success", func(t *testing.T) {
		t.Parallel()

		url := startHTTPServer(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/fail" {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte("failure body"))
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("success body"))
		})
		env := []string{"HTTP_URL=" + url}

		dagu := harness.NewRunner(t)
		ok := dagu.RunWithEnv(env, "start", "silent_success.yaml")
		ok.ExpectExitCode(0)
		okOut := stepStdout(t, ok.Stdout())
		require.NotContains(t, okOut, "200 OK")
		require.Contains(t, okOut, "success body")

		dagu2 := harness.NewRunner(t)
		failed := dagu2.RunWithEnv(env, "start", "silent_failure.yaml")
		failed.ExpectNonZeroExitCode()
		failedOut := stepStdout(t, failed.Stdout())
		require.Contains(t, failedOut, "500 Internal Server Error")
		require.Contains(t, failedOut, "failure body")
	})

	t.Run("format json prints a structured status/headers/body result", func(t *testing.T) {
		t.Parallel()

		url := startHTTPServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"key":"value"}`))
		})

		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv([]string{"HTTP_URL=" + url}, "start", "json_format.yaml")
		result.ExpectExitCode(0)

		var parsed struct {
			StatusCode int            `json:"status_code"`
			Body       map[string]any `json:"body"`
		}
		require.NoError(t, json.Unmarshal([]byte(stepStdout(t, result.Stdout())), &parsed))
		require.Equal(t, http.StatusOK, parsed.StatusCode)
		require.Equal(t, "value", parsed.Body["key"])
	})

	t.Run("output writes the response body to a file instead of stdout", func(t *testing.T) {
		t.Parallel()

		url := startHTTPServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("file output content"))
		})

		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv([]string{"HTTP_URL=" + url}, "start", "output_file.yaml")
		result.ExpectExitCode(0)
		dagu.ExpectFileContent("response_body.out", "file output content")
	})

	t.Run("uploads multipart form fields and a file together", func(t *testing.T) {
		t.Parallel()

		var gotField, gotFileContent string
		url := startHTTPServer(t, func(w http.ResponseWriter, r *http.Request) {
			require.NoError(t, r.ParseMultipartForm(1<<20))
			gotField = r.FormValue("field1")
			file, _, err := r.FormFile("upload")
			require.NoError(t, err)
			defer file.Close()
			content, err := io.ReadAll(file)
			require.NoError(t, err)
			gotFileContent = string(content)
			w.WriteHeader(http.StatusOK)
		})

		dagu := harness.NewRunner(t)
		dagu.WriteFile("upload.txt", "uploaded file content")

		result := dagu.RunWithEnv([]string{"HTTP_URL=" + url}, "start", "multipart_upload.yaml")
		result.ExpectExitCode(0)
		require.Equal(t, "form-value", gotField)
		require.Equal(t, "uploaded file content", gotFileContent)
	})
}

// TestHTTPRequestTLS proves with.skip_tls_verify controls whether the
// request's TLS transport trusts the server's certificate, against a real
// TLS server with a self-signed certificate.
func TestHTTPRequestTLS(t *testing.T) {
	t.Parallel()

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("tls response body"))
	}))
	t.Cleanup(server.Close)
	env := []string{"HTTPS_URL=" + server.URL}

	t.Run("skip_tls_verify true accepts a self-signed certificate", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv(env, "start", "skip_tls_verify_true.yaml")
		result.ExpectExitCode(0)
		require.Contains(t, result.Stdout(), "tls response body")
	})

	t.Run("skip_tls_verify false rejects a self-signed certificate", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.RunWithEnv(env, "start", "skip_tls_verify_false.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("certificate")
	})
}

// TestHTTPRequestNoServer proves the failure modes that do not need a real
// HTTP server: with.method/with.url are validated at DAG-build time,
// with.body cannot be combined with with.form (a runtime error, since it is
// checked before any request is made), and a connection that fails outright
// is a runtime error.
func TestHTTPRequestNoServer(t *testing.T) {
	t.Parallel()

	t.Run("missing with.method fails validate", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("validate", "missing_method.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("with.method is required")
	})

	t.Run("missing with.url fails validate", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("validate", "missing_url.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("with.url is required")
	})

	t.Run("an empty with.method fails validate distinctly from a missing one", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("validate", "empty_method.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("with.method must be a non-empty string")
	})

	t.Run("with.body combined with with.form fails only when the step runs", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		validate := dagu.Run("validate", "body_form_conflict.yaml")
		validate.ExpectExitCode(0)

		result := dagu.Run("start", "body_form_conflict.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("body cannot be combined with form or files")
	})

	t.Run("nothing listening on the target port fails to connect", func(t *testing.T) {
		t.Parallel()

		dagu := harness.NewRunner(t)
		result := dagu.Run("start", "connection_refused.yaml")
		result.ExpectNonZeroExitCode()
		result.ExpectStderrContains("connection refused")
	})
}
