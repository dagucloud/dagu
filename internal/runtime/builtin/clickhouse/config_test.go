// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package clickhouse

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/executor/registry"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseConfig(t *testing.T) {
	t.Parallel()

	t.Run("Defaults", func(t *testing.T) {
		t.Parallel()
		cfg, err := ParseConfig(map[string]any{"url": "http://localhost:8123"})
		require.NoError(t, err)
		assert.Equal(t, 60, cfg.Timeout)
		assert.Equal(t, "jsonl", cfg.OutputFormat)
		assert.False(t, cfg.Headers)
		assert.False(t, cfg.TLSSkipVerify)
	})

	t.Run("AllOptions", func(t *testing.T) {
		t.Parallel()
		cfg, err := ParseConfig(map[string]any{
			"url":             "https://ch.example.com:8443/proxy/",
			"user":            "etl",
			"password":        "pw",
			"database":        "analytics",
			"params":          map[string]any{"day": "2024-01-02"},
			"settings":        map[string]any{"readonly": 1, "max_execution_time": 30},
			"output_format":   "csv",
			"headers":         true,
			"timeout":         120,
			"tls_skip_verify": true,
		})
		require.NoError(t, err)
		assert.Equal(t, "etl", cfg.User)
		assert.Equal(t, "analytics", cfg.Database)
		assert.Equal(t, 120, cfg.Timeout)
		assert.True(t, cfg.TLSSkipVerify)
	})

	t.Run("ZeroTimeoutDisablesIt", func(t *testing.T) {
		t.Parallel()
		cfg, err := ParseConfig(map[string]any{"url": "http://h", "timeout": 0})
		require.NoError(t, err)
		assert.Zero(t, cfg.Timeout)
	})

	for _, tt := range []struct {
		name    string
		cfg     map[string]any
		message string
	}{
		{"MissingURL", map[string]any{}, "url is required"},
		{"BlankURL", map[string]any{"url": "  "}, "url is required"},
		{"BadScheme", map[string]any{"url": "tcp://localhost:9000"}, "scheme must be http or https"},
		{"NoScheme", map[string]any{"url": "localhost:8123"}, "scheme must be http or https"},
		{"NoHost", map[string]any{"url": "http://"}, "host is required"},
		{"EmbeddedCredentials", map[string]any{"url": "http://user:secret@localhost:8123"}, "credentials must not be embedded"},
		{"EmbeddedUser", map[string]any{"url": "http://user@localhost:8123"}, "credentials must not be embedded"},
		{"QueryString", map[string]any{"url": "http://localhost:8123/?database=x"}, "query string is not allowed"},
		{"Fragment", map[string]any{"url": "http://localhost:8123/#x"}, "fragment is not allowed"},
		{"BadOutputFormat", map[string]any{"url": "http://h", "output_format": "tsv"}, "invalid output_format"},
		{"NegativeTimeout", map[string]any{"url": "http://h", "timeout": -1}, "timeout must be non-negative"},
		{"UnknownOption", map[string]any{"url": "http://h", "read_only": true}, "read_only"},
		{"BadParamName", map[string]any{"url": "http://h", "params": map[string]any{"a-b": 1}}, "invalid params name"},
		{"BadParamValue", map[string]any{"url": "http://h", "params": map[string]any{"a": struct{}{}}}, "unsupported value type"},
		{"BadSettingName", map[string]any{"url": "http://h", "settings": map[string]any{"a=b": 1}}, "invalid settings name"},
		{"BadSettingValue", map[string]any{"url": "http://h", "settings": map[string]any{"a": []any{1}}}, "unsupported value type"},
		{"ReservedQueryID", map[string]any{"url": "http://h", "settings": map[string]any{"query_id": "x"}}, "not allowed"},
		{"ReservedDefaultFormat", map[string]any{"url": "http://h", "settings": map[string]any{"default_format": "CSV"}}, "use output_format"},
		{"ReservedSession", map[string]any{"url": "http://h", "settings": map[string]any{"session_id": "x"}}, "not allowed"},
		{"ReservedSessionCheck", map[string]any{"url": "http://h", "settings": map[string]any{"session_check": 1}}, "not allowed"},
		{"ReservedSessionTimeout", map[string]any{"url": "http://h", "settings": map[string]any{"session_timeout": 1}}, "not allowed"},
		{"ReservedCompress", map[string]any{"url": "http://h", "settings": map[string]any{"compress": 1}}, "not allowed"},
		{"ReservedDecompress", map[string]any{"url": "http://h", "settings": map[string]any{"decompress": 1}}, "not allowed"},
		{"ReservedInBandException", map[string]any{"url": "http://h", "settings": map[string]any{"http_write_exception_in_output_format": 1}}, "not allowed"},
		{"QueryOptionIsNotAnOption", map[string]any{"url": "http://h", "query": "SELECT 1"}, "query"},
		{"ReservedParam", map[string]any{"url": "http://h", "settings": map[string]any{"param_x": 1}}, "use params"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseConfig(tt.cfg)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.message)
		})
	}

	t.Run("ErrorDoesNotEchoCredentials", func(t *testing.T) {
		t.Parallel()
		_, err := ParseConfig(map[string]any{"url": "http://user:topsecret@local host:8123"})
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "topsecret")
	})
}

func TestValidateStep(t *testing.T) {
	t.Parallel()

	step := func(cfg map[string]any, command, script string) ir.Step {
		s := ir.Step{ExecutorConfig: ir.ExecutorConfig{Type: executorType, Config: cfg}, Script: script}
		if command != "" {
			s.Commands = []ir.CommandEntry{{Command: command, CmdWithArgs: command}}
		}
		return s
	}
	valid := map[string]any{"url": "http://localhost:8123"}

	t.Run("OtherExecutorIgnored", func(t *testing.T) {
		t.Parallel()
		assert.NoError(t, validateStep(ir.Step{ExecutorConfig: ir.ExecutorConfig{Type: "redis"}}))
	})
	t.Run("Command", func(t *testing.T) {
		t.Parallel()
		assert.NoError(t, validateStep(step(valid, "SELECT 1", "")))
	})
	t.Run("Script", func(t *testing.T) {
		t.Parallel()
		assert.NoError(t, validateStep(step(valid, "", "SELECT 1")))
	})
	t.Run("File", func(t *testing.T) {
		t.Parallel()
		assert.NoError(t, validateStep(step(map[string]any{"url": "http://h", "file": "q.sql"}, "", "")))
	})
	t.Run("NoQuery", func(t *testing.T) {
		t.Parallel()
		err := validateStep(step(valid, "", ""))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "query or file is required")
	})
	t.Run("QueryAndFile", func(t *testing.T) {
		t.Parallel()
		err := validateStep(step(map[string]any{"url": "http://h", "file": "q.sql"}, "SELECT 1", ""))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "mutually exclusive")
	})
	t.Run("MissingURL", func(t *testing.T) {
		t.Parallel()
		err := validateStep(step(map[string]any{}, "SELECT 1", ""))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "url is required")
	})
	t.Run("FileReference", func(t *testing.T) {
		t.Parallel()
		assert.NoError(t, validateStep(step(map[string]any{"url": "http://h", "file": "${SQL_FILE}"}, "", "")))
	})
	t.Run("FileReferenceWithQuery", func(t *testing.T) {
		t.Parallel()
		err := validateStep(step(map[string]any{"url": "http://h", "file": "${SQL_FILE}"}, "SELECT 1", ""))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "mutually exclusive")
	})
	t.Run("ReferencesAreNotChecked", func(t *testing.T) {
		t.Parallel()
		cfg := map[string]any{
			"url":           "${CLICKHOUSE_URL}",
			"timeout":       "${TIMEOUT}",
			"output_format": "${FORMAT}",
			"headers":       "${HEADERS}",
		}
		assert.NoError(t, validateStep(step(cfg, "SELECT 1", "")))
	})
	t.Run("OtherErrorsStillReported", func(t *testing.T) {
		t.Parallel()
		cfg := map[string]any{"url": "${CLICKHOUSE_URL}", "output_format": "xml"}
		err := validateStep(step(cfg, "SELECT 1", ""))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid output_format")
	})
}

func TestExecutorRegistered(t *testing.T) {
	t.Parallel()

	caps := registry.ExecutorCapabilitiesFor(executorType)
	assert.True(t, caps.Command)
	assert.True(t, caps.Script)
	assert.NoError(t, registry.ValidateExecutorConfig(executorType, map[string]any{
		"url": "http://localhost:8123", "timeout": "${T}", "settings": map[string]any{"readonly": 1},
	}))
	assert.Error(t, registry.ValidateExecutorConfig(executorType, map[string]any{"url": 5}))
}
