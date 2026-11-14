// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmd"
	"github.com/dagucloud/dagu/v2/internal/cmn/replaycache"
	"github.com/dagucloud/dagu/v2/internal/cmn/workbook"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestXlsxCacheClear(t *testing.T) {
	t.Run("ClearsEveryStep", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)
		seedXlsxReplayCache(t, th, "quotes", "fields", "totals")

		out, err := runXlsxCacheClear(th, "quotes")
		require.NoError(t, err)
		assert.Contains(t, out, `Removed xlsx replay cache for step "fields" of DAG "quotes"`)
		assert.Contains(t, out, `Removed xlsx replay cache for step "totals" of DAG "quotes"`)
		assert.Empty(t, xlsxReplayCacheSteps(t, th, "quotes"))
	})

	t.Run("ClearsOneStep", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)
		seedXlsxReplayCache(t, th, "quotes", "fields", "totals")

		out, err := runXlsxCacheClear(th, "quotes", "--step", "fields")
		require.NoError(t, err)
		assert.Equal(t, "Removed xlsx replay cache for step \"fields\" of DAG \"quotes\"\n", out)
		assert.Equal(t, []string{"totals"}, xlsxReplayCacheSteps(t, th, "quotes"))
	})

	t.Run("ReportsNothingToClear", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)

		out, err := runXlsxCacheClear(th, "quotes")
		require.NoError(t, err)
		assert.Equal(t, "No xlsx replay cache for DAG \"quotes\"\n", out)

		out, err = runXlsxCacheClear(th, "quotes", "--step", "fields")
		require.NoError(t, err)
		assert.Equal(t, "No xlsx replay cache for step \"fields\" of DAG \"quotes\"\n", out)
	})

	// The cache is keyed by the DAG name, which a YAML path resolves to.
	t.Run("ResolvesDAGFile", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)
		dag := th.DAG(t, `name: explicit-quotes
steps:
  - name: "1"
    run: echo "hello"
`)
		seedXlsxReplayCache(t, th, "explicit-quotes", "fields")

		_, err := runXlsxCacheClear(th, dag.Location)
		require.NoError(t, err)
		assert.Empty(t, xlsxReplayCacheSteps(t, th, "explicit-quotes"))
	})
}

func runXlsxCacheClear(th test.Command, args ...string) (string, error) {
	return runCommand(th, cmd.Xlsx(), append([]string{"xlsx", "cache", "clear"}, args...)...)
}

func xlsxReplayCache(th test.Command) *replaycache.Store {
	return replaycache.New(filepath.Join(th.Config.Paths.DataDir, workbook.DataDirName))
}

func seedXlsxReplayCache(t *testing.T, th test.Command, dagName string, steps ...string) {
	t.Helper()
	cache := xlsxReplayCache(th)
	for _, step := range steps {
		path := cache.Path(dagName, step)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
		require.NoError(t, os.WriteFile(path, []byte("{}"), 0o600))
	}
}

func xlsxReplayCacheSteps(t *testing.T, th test.Command, dagName string) []string {
	t.Helper()
	steps, err := xlsxReplayCache(th).Steps(dagName)
	require.NoError(t, err)
	return steps
}
