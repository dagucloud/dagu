// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package redis

import (
	"bytes"
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
