// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec045_http_request_test

import (
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

func TestHTTPRequest(t *testing.T) {
	t.Parallel()

	requests := make(chan *http.Request, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.Clone(r.Context())
		w.Header().Set("X-Response", "response-value")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"key":"value"}`))
	}))
	t.Cleanup(server.Close)

	dagu := harness.NewRunner(t)
	result := dagu.RunWithEnv([]string{"HTTP_URL=" + server.URL}, "start", "json_format.yaml")
	result.ExpectExitCode(0)
	select {
	case request := <-requests:
		require.Equal(t, http.MethodGet, request.Method)
		require.Equal(t, "header-value", request.Header.Get("X-Test-Header"))
		require.Equal(t, "query-value", request.URL.Query().Get("q"))
	default:
		t.Fatal("HTTP action succeeded without a request")
	}
	data, err := os.ReadFile(dagu.ProjectPath("response.json"))
	require.NoError(t, err)
	var response struct {
		Status  int               `json:"status_code"`
		Headers http.Header       `json:"headers"`
		Body    map[string]string `json:"body"`
	}
	require.NoError(t, json.Unmarshal(data, &response))
	require.Equal(t, http.StatusOK, response.Status)
	require.Equal(t, "response-value", response.Headers.Get("X-Response"))
	require.Equal(t, "value", response.Body["key"])
}

func TestHTTPSilentJSON(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		status int
		keys   []string
	}{
		{"success", http.StatusAccepted, []string{"body"}},
		{"failure", http.StatusServiceUnavailable, []string{"body", "headers", "status_code"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"id":"j1"}`))
			}))
			t.Cleanup(server.Close)

			dagu := harness.NewRunner(t)
			result := dagu.RunWithEnv([]string{"HTTP_URL=" + server.URL}, "start", "silent_json_format.yaml")
			if tc.status < 300 {
				result.ExpectExitCode(0)
			} else {
				result.ExpectNonZeroExitCode()
			}
			data, err := os.ReadFile(dagu.ProjectPath("response.json"))
			require.NoError(t, err)
			var response map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(data, &response))
			require.ElementsMatch(t, tc.keys, slices.Collect(maps.Keys(response)))
			require.JSONEq(t, `{"id":"j1"}`, string(response["body"]))
		})
	}
}

func TestHTTPErrorOutput(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("service failed"))
	}))
	t.Cleanup(server.Close)

	dagu := harness.NewRunner(t)
	dagu.WriteFile("response_body.out", "existing content")
	result := dagu.RunWithEnv([]string{"HTTP_URL=" + server.URL}, "start", "output_file.yaml")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains("http status code not 2xx")
	dagu.ExpectFileContent("response_body.out", "existing content")
	dagu.ExpectFileContains("failure.out", "service failed")
}

// A mapped status is the step exit code, so retry_policy and continue_on act
// on it, and a status mapped to 0 still writes the response body.
func TestHTTPStatusExitCodes(t *testing.T) {
	t.Parallel()

	var flakyRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/missing":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("not here"))
		case "/flaky":
			if flakyRequests.Add(1) == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
			}
		case "/gone":
			w.WriteHeader(http.StatusGone)
		}
	}))
	t.Cleanup(server.Close)

	dagu := harness.NewRunner(t)
	dagu.RunWithEnv([]string{"HTTP_URL=" + server.URL}, "start", "status_exit_codes.yaml")
	dagu.ExpectFileContains("tolerated.out", "not here")
	require.Equal(t, int32(2), flakyRequests.Load())
	dagu.ExpectFileContains("after.out", "done")
}

func TestHTTPTLS(t *testing.T) {
	t.Parallel()

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("tls response"))
	}))
	t.Cleanup(server.Close)
	for _, tc := range []struct {
		file    string
		success bool
	}{
		{"skip_tls_verify_true.yaml", true},
		{"skip_tls_verify_false.yaml", false},
	} {
		t.Run(tc.file, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.RunWithEnv([]string{"HTTPS_URL=" + server.URL}, "start", tc.file)
			if tc.success {
				result.ExpectExitCode(0)
			} else {
				result.ExpectNonZeroExitCode()
				result.ExpectStderrContains("certificate")
			}
		})
	}
}

func TestHTTPConfig(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ file, field string }{
		{"missing_method.yaml", "with.method"},
		{"missing_url.yaml", "with.url"},
		{"unknown_field.yaml", `unexpected additional properties ["header"]; did you mean "headers" instead of "header"?`},
		{"status_exit_codes_invalid.yaml", "status_exit_codes"},
	} {
		t.Run(tc.file, func(t *testing.T) {
			t.Parallel()

			dagu := harness.NewRunner(t)
			result := dagu.Run("validate", tc.file)
			result.ExpectNonZeroExitCode()
			result.ExpectStderrContains(tc.field)
		})
	}
}
