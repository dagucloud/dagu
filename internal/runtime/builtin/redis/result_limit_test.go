// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package redis

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A result whose output would exceed max_result_size fails and writes
// nothing, so a step never leaves a truncated result behind.
func TestWriteResultMaxResultSize(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		format  string
		limit   int64
		result  any
		wantErr bool
	}{
		{"JSONWithin", "json", 64, "small", false},
		{"JSONOver", "json", 16, strings.Repeat("x", 32), true},
		{"CSVOver", "csv", 16, []any{strings.Repeat("a", 10), strings.Repeat("b", 10)}, true},
		{"Unlimited", "raw", 0, strings.Repeat("x", 1024), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var stdout bytes.Buffer
			e := &redisExecutor{
				cfg:    &Config{OutputFormat: tc.format, MaxResultSize: tc.limit},
				stdout: &stdout,
			}
			err := e.writeResult(tc.result)
			if !tc.wantErr {
				require.NoError(t, err)
				assert.NotEmpty(t, stdout.String())
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), "max_result_size")
			assert.Empty(t, stdout.String())
		})
	}
}

// A result over max_result_size is reported as a failed command in the
// metrics, matching the step's failure.
func TestExecuteWithMetricsResultTooLarge(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	e := &redisExecutor{
		cfg:    &Config{OutputFormat: "json", MaxResultSize: 8},
		stdout: &stdout,
		stderr: &stderr,
	}
	err := e.executeWithMetrics(context.Background(), "GET", "k", func(context.Context) (any, error) {
		return strings.Repeat("x", 32), nil
	})
	require.Error(t, err)

	var metrics ExecutionMetrics
	require.NoError(t, json.Unmarshal(stderr.Bytes(), &metrics))
	assert.Equal(t, "error", metrics.Status)
	assert.Contains(t, metrics.Error, "max_result_size")
	assert.Empty(t, stdout.String())
}

// The 10 MiB default applies when max_result_size is unset, and an explicit
// 0 lifts it rather than falling back to the default.
func TestMaxResultSizeFromConfig(t *testing.T) {
	t.Parallel()

	result := strings.Repeat("x", 11<<20)
	for _, tc := range []struct {
		name    string
		config  map[string]any
		wantErr bool
	}{
		{"Default", map[string]any{"command": "PING", "output_format": "raw"}, true},
		{"Zero", map[string]any{"command": "PING", "output_format": "raw", "max_result_size": 0}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg, err := ParseConfig(context.Background(), tc.config)
			require.NoError(t, err)
			var stdout bytes.Buffer
			e := &redisExecutor{cfg: cfg, stdout: &stdout}
			err = e.writeResult(result)
			if tc.wantErr {
				require.Error(t, err)
				assert.Empty(t, stdout.String())
				return
			}
			require.NoError(t, err)
			assert.Greater(t, stdout.Len(), len(result))
		})
	}
}
