// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package sql

import (
	"testing"

	"github.com/dagucloud/dagu/v2/internal/executor/registry"
	"github.com/stretchr/testify/require"
)

// An unknown with key must fail validation instead of being silently dropped
// by the postgres and sqlite executors (#3440). The query text is folded into
// the step command by the action normalizer, so valid configs still validate.
func TestSQLRejectsUnknownWithKey(t *testing.T) {
	t.Parallel()

	for _, executorType := range []string{"postgres", "sqlite"} {
		t.Run(executorType, func(t *testing.T) {
			t.Parallel()

			err := registry.ValidateExecutorConfig(executorType, map[string]any{
				"dsn": "file:test.db", "output_formt": "json",
			})
			require.Error(t, err)
			require.Contains(t, err.Error(), `did you mean "output_format" instead of "output_formt"?`)

			require.NoError(t, registry.ValidateExecutorConfig(executorType, map[string]any{
				"dsn": "file:test.db", "output_format": "json", "params": []any{1, 2},
			}))
		})
	}
}
