// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/browserhost"
	"github.com/dagucloud/dagu/v2/internal/cmd"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBrowserCacheClear(t *testing.T) {
	t.Run("ClearsEveryStep", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)
		seedBrowserReplayCache(t, th, "billing", "login", "download")

		out, err := runBrowserCacheClear(th, "billing")
		require.NoError(t, err)
		assert.Contains(t, out, `Removed browser replay cache for step "download" of DAG "billing"`)
		assert.Contains(t, out, `Removed browser replay cache for step "login" of DAG "billing"`)
		assert.Empty(t, browserReplayCacheSteps(t, th, "billing"))
	})

	t.Run("ClearsOneStep", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)
		seedBrowserReplayCache(t, th, "billing", "login", "download")

		out, err := runBrowserCacheClear(th, "billing", "--step", "login")
		require.NoError(t, err)
		assert.Equal(t, "Removed browser replay cache for step \"login\" of DAG \"billing\"\n", out)
		assert.Equal(t, []string{"download"}, browserReplayCacheSteps(t, th, "billing"))
	})

	t.Run("ReportsNothingToClear", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)

		out, err := runBrowserCacheClear(th, "billing")
		require.NoError(t, err)
		assert.Equal(t, "No browser replay cache for DAG \"billing\"\n", out)

		out, err = runBrowserCacheClear(th, "billing", "--step", "login")
		require.NoError(t, err)
		assert.Equal(t, "No browser replay cache for step \"login\" of DAG \"billing\"\n", out)
	})

	// The cache is keyed by the DAG name, which a YAML path resolves to.
	t.Run("ResolvesDAGFile", func(t *testing.T) {
		t.Parallel()

		th := test.SetupCommand(t)
		dag := th.DAG(t, `name: explicit-billing
steps:
  - name: "1"
    run: echo "hello"
`)
		seedBrowserReplayCache(t, th, "explicit-billing", "login")

		_, err := runBrowserCacheClear(th, dag.Location)
		require.NoError(t, err)
		assert.Empty(t, browserReplayCacheSteps(t, th, "explicit-billing"))
	})
}

func runBrowserCacheClear(th test.Command, args ...string) (string, error) {
	return runCommand(th, cmd.Browser(), append([]string{"browser", "cache", "clear"}, args...)...)
}

func browserReplayCache(th test.Command) *browserhost.ReplayCache {
	return browserhost.NewReplayCache(filepath.Join(th.Config.Paths.DataDir, browserhost.DataDirName))
}

func seedBrowserReplayCache(t *testing.T, th test.Command, dagName string, steps ...string) {
	t.Helper()
	cache := browserReplayCache(th)
	for _, step := range steps {
		path := cache.Path(dagName, step)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
		require.NoError(t, os.WriteFile(path, []byte("{}"), 0o600))
	}
}

func browserReplayCacheSteps(t *testing.T, th test.Command, dagName string) []string {
	t.Helper()
	steps, err := browserReplayCache(th).Steps(dagName)
	require.NoError(t, err)
	return steps
}
