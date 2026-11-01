// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec

import (
	"context"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWatch(t *testing.T) {
	t.Parallel()

	t.Run("folder, pattern and interval", func(t *testing.T) {
		t.Parallel()
		dag, err := LoadYAML(context.Background(), []byte(`
watch:
  path: ~/Downloads/invoices
  pattern: "*.pdf"
  interval: 30s
steps:
  - id: read
    run: echo "${WATCH_FILE}" "${WATCH_NAME}"
`))
		require.NoError(t, err)
		assert.Equal(t, &ir.WatchConfig{Path: "~/Downloads/invoices", Pattern: "*.pdf", Interval: 30 * time.Second}, dag.Watch)
		assert.Empty(t, dag.Schedule, "a watched DAG needs no schedule")
	})

	t.Run("defaults", func(t *testing.T) {
		t.Parallel()
		dag, err := LoadYAML(context.Background(), []byte(`
watch:
  path: /srv/inbox
  pattern: "*"
steps:
  - run: echo hi
`))
		require.NoError(t, err)
		assert.Equal(t, &ir.WatchConfig{Path: "/srv/inbox", Interval: ir.DefaultWatchInterval}, dag.Watch, "* matches every file, as no pattern does")
	})

	for _, tc := range []struct {
		name, watch, want string
	}{
		{"no path", "watch:\n  pattern: '*.pdf'\n", "the folder to watch is required"},
		{"bad pattern", "watch:\n  path: /in\n  pattern: '[a'\n", "syntax error in pattern"},
		{"bad interval", "watch:\n  path: /in\n  interval: soon\n", "interval must be a duration such as 30s"},
		{"short interval", "watch:\n  path: /in\n  interval: 200ms\n", "interval must be at least 1s"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := LoadYAML(context.Background(), []byte(tc.watch+"steps:\n  - run: echo hi\n"))
			require.ErrorContains(t, err, tc.want)
		})
	}
}
