// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmd"
	"github.com/dagucloud/dagu/v2/internal/cmn/replaycache"
	"github.com/dagucloud/dagu/v2/internal/computerhost"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestComputerCacheClear(t *testing.T) {
	t.Parallel()

	th := test.SetupCommand(t)
	seedComputerReplayCache(t, th, "invoices", "post", "read")
	seedBrowserReplayCache(t, th, "invoices", "post")

	out, err := runCommand(th, cmd.Computer(), "computer", "cache", "clear", "invoices", "--step", "post")
	require.NoError(t, err)
	assert.Equal(t, "Removed computer replay cache for step \"post\" of DAG \"invoices\"\n", out)
	assert.Equal(t, []string{"read"}, computerReplayCacheSteps(t, th, "invoices"))
	assert.Equal(t, []string{"post"}, browserReplayCacheSteps(t, th, "invoices"), "browser steps keep their cache")
}

func computerReplayCache(th test.Command) *replaycache.Store {
	return replaycache.New(filepath.Join(th.Config.Paths.DataDir, computerhost.DataDirName))
}

func seedComputerReplayCache(t *testing.T, th test.Command, dagName string, steps ...string) {
	t.Helper()
	cache := computerReplayCache(th)
	for _, step := range steps {
		path := cache.Path(dagName, step)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
		require.NoError(t, os.WriteFile(path, []byte("{}"), 0o600))
	}
}

func computerReplayCacheSteps(t *testing.T, th test.Command, dagName string) []string {
	t.Helper()
	steps, err := computerReplayCache(th).Steps(dagName)
	require.NoError(t, err)
	return steps
}
