// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetupDoesNotMutatePerTestProcessEnv(t *testing.T) {
	t.Setenv("DAGU_HOME", "/original/dagu-home")
	t.Setenv("DAGU_CONFIG", "/original/config.yaml")
	t.Setenv("DAGU_EXECUTABLE", "/original/dagu")
	t.Setenv("SHELL", "/original/shell")

	helper := Setup(t)

	assert.Equal(t, "/original/dagu-home", os.Getenv("DAGU_HOME"))
	assert.Equal(t, "/original/config.yaml", os.Getenv("DAGU_CONFIG"))
	assertExecutableEnvValue(t, "/original/dagu", os.Getenv("DAGU_EXECUTABLE"))
	assert.Equal(t, "/original/shell", os.Getenv("SHELL"))

	assert.Contains(t, helper.ChildEnv, "DAGU_HOME="+helper.tmpDir)
	assert.Contains(t, helper.ChildEnv, "DAGU_CONFIG="+helper.Config.Paths.ConfigFileUsed)
	assert.Contains(t, helper.ChildEnv, "DAGU_EXECUTABLE="+helper.Config.Paths.Executable)
	assert.Contains(t, helper.ChildEnv, "SHELL="+helper.Config.Core.DefaultShell)
	assert.Contains(t, helper.ChildEnv, "DEBUG=true")
	assert.Contains(t, helper.ChildEnv, "CI=true")
	assert.Contains(t, helper.ChildEnv, "TZ=UTC")
}

func TestTestShellPath_PrefersNativeWindowsShells(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Skipping Windows-specific test")
	}

	chosen := strings.ToLower(filepath.Base(testShellPath(t)))
	assert.Contains(t, []string{"powershell.exe", "pwsh.exe", "cmd.exe"}, chosen)
}

func assertExecutableEnvValue(t *testing.T, expectedInput, actual string) {
	t.Helper()

	if runtime.GOOS != "windows" {
		assert.Equal(t, expectedInput, actual)
		return
	}

	if after, ok := strings.CutPrefix(expectedInput, "/"); ok {
		normalized := filepath.FromSlash(after)
		assert.Regexp(t, `^[A-Za-z]:\\.+`, actual)
		assert.True(t, strings.HasSuffix(strings.ToLower(actual), strings.ToLower(`\`+normalized)), "unexpected executable env value %q", actual)
		return
	}

	assert.Equal(t, filepath.FromSlash(expectedInput), actual)
}

// Directories that ended test processes left behind are removed; recent ones,
// which a running process may still use, and other files are kept.
func TestRemoveStaleDirs(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	t.Setenv("TMP", tmp)
	t.Setenv("TEMP", tmp)
	old := time.Now().Add(-staleDirAge - time.Hour)
	dir := func(name string, modified time.Time) string {
		path := filepath.Join(tmp, name)
		require.NoError(t, os.MkdirAll(filepath.Join(path, "logs"), 0o750))
		require.NoError(t, os.Chtimes(path, modified, modified))
		return path
	}
	staleBin := dir("dagu-test-bin-123", old)
	staleHome := dir("dagu-test-4a555b30-ea32-4b6b-adf6-a9640f9a4150954405819", old)
	freshBin := dir("dagu-test-bin-456", time.Now())
	other := dir("dagu-test-cache", old)

	removeStaleDirs()

	assert.NoDirExists(t, staleBin)
	assert.NoDirExists(t, staleHome)
	assert.DirExists(t, freshBin)
	assert.DirExists(t, other)
}

// The executable a test process built is removed once its tests finish.
func TestRemoveBuiltExecutable(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "dagu-test-bin-123")
	require.NoError(t, os.MkdirAll(dir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "dagu"), nil, 0o600))
	prev := builtExecutableDir
	builtExecutableDir = dir
	t.Cleanup(func() { builtExecutableDir = prev })

	removeBuiltExecutable()

	assert.NoDirExists(t, dir)
}
