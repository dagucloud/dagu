// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	nethttp "net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/core"
	_ "github.com/dagucloud/dagu/v2/internal/runtime/builtin/http"
	"github.com/dagucloud/dagu/v2/internal/runtime/executor"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHTTPExecutorFileOutputWritesResponseBody(t *testing.T) {
	t.Parallel()

	body := []byte{0, 1, 2, 'o', 'k'}
	server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		w.WriteHeader(nethttp.StatusOK)
		_, _ = w.Write(body)
	}))
	defer server.Close()

	output := filepath.Join(t.TempDir(), "downloads", "payload.bin")
	exec, err := executor.NewExecutor(context.Background(), httpStep(server.URL, map[string]any{
		"output": output,
		"silent": true,
	}))
	require.NoError(t, err)

	var stdout bytes.Buffer
	exec.SetStdout(&stdout)
	exec.SetStderr(&bytes.Buffer{})

	require.NoError(t, exec.Run(context.Background()))

	got, err := os.ReadFile(output)
	require.NoError(t, err)
	assert.Equal(t, body, got)
	assert.Empty(t, stdout.String())
}

func TestHTTPExecutorFileOutputWithJSONWritesMetadata(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cfg  map[string]any
	}{
		{
			name: "FormatJSON",
			cfg:  map[string]any{"format": "json"},
		},
		{
			name: "LegacyJSONBoolean",
			cfg:  map[string]any{"json": true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
				w.WriteHeader(nethttp.StatusOK)
				_, _ = w.Write([]byte(`{"ok":true}`))
			}))
			defer server.Close()

			output := filepath.Join(t.TempDir(), "payload.json")
			tt.cfg["output"] = output

			exec, err := executor.NewExecutor(context.Background(), httpStep(server.URL, tt.cfg))
			require.NoError(t, err)

			var stdout bytes.Buffer
			exec.SetStdout(&stdout)
			exec.SetStderr(&bytes.Buffer{})

			require.NoError(t, exec.Run(context.Background()))

			got, err := os.ReadFile(output)
			require.NoError(t, err)
			assert.JSONEq(t, `{"ok":true}`, string(got))

			var metadata map[string]any
			require.NoError(t, json.Unmarshal(stdout.Bytes(), &metadata))
			assert.Equal(t, output, metadata["output"])
			assert.NotContains(t, metadata, "body")
		})
	}
}

func TestHTTPExecutorFileOutputKeepsExistingFileOnHTTPError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		w.WriteHeader(nethttp.StatusNotFound)
		_, _ = w.Write([]byte("missing"))
	}))
	defer server.Close()

	output := filepath.Join(t.TempDir(), "payload.bin")
	require.NoError(t, os.WriteFile(output, []byte("old"), 0o600))

	exec, err := executor.NewExecutor(context.Background(), httpStep(server.URL, map[string]any{
		"output": output,
		"silent": true,
	}))
	require.NoError(t, err)

	var stdout bytes.Buffer
	exec.SetStdout(&stdout)
	exec.SetStderr(&bytes.Buffer{})

	err = exec.Run(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "http status code not 2xx")
	assert.Contains(t, stdout.String(), "missing")

	got, err := os.ReadFile(output)
	require.NoError(t, err)
	assert.Equal(t, "old", string(got))
}

func TestHTTPExecutorFileOutputWithJSONKeepsHTTPStatusOnError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		w.WriteHeader(nethttp.StatusInternalServerError)
		_, _ = w.Write([]byte("server failed"))
	}))
	defer server.Close()

	output := filepath.Join(t.TempDir(), "payload.bin")
	require.NoError(t, os.WriteFile(output, []byte("old"), 0o600))

	exec, err := executor.NewExecutor(context.Background(), httpStep(server.URL, map[string]any{
		"output": output,
		"format": "json",
		"silent": true,
	}))
	require.NoError(t, err)

	var stdout bytes.Buffer
	exec.SetStdout(&stdout)
	exec.SetStderr(&bytes.Buffer{})

	err = exec.Run(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "http status code not 2xx")

	got, err := os.ReadFile(output)
	require.NoError(t, err)
	assert.Equal(t, "old", string(got))

	var metadata map[string]any
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &metadata))
	assert.Equal(t, float64(nethttp.StatusInternalServerError), metadata["status_code"])
	assert.Equal(t, "server failed", metadata["body"])
}

func httpStep(url string, cfg map[string]any) core.Step {
	return core.Step{
		Commands: []core.CommandEntry{{Command: "GET", Args: []string{url}}},
		ExecutorConfig: core.ExecutorConfig{
			Type:   "http",
			Config: cfg,
		},
	}
}
