// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package cmdutil

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetShellCommand_WithDAGUDefaultShell(t *testing.T) {
	// Save original env var
	originalShell := os.Getenv("DAGU_DEFAULT_SHELL")
	defer func() {
		if originalShell != "" {
			_ = os.Setenv("DAGU_DEFAULT_SHELL", originalShell)
		} else {
			_ = os.Unsetenv("DAGU_DEFAULT_SHELL")
		}
	}()

	// Test with DAGU_DEFAULT_SHELL set
	testShell := "/usr/local/bin/fish"
	_ = os.Setenv("DAGU_DEFAULT_SHELL", testShell)

	result := GetShellCommand("")
	assert.Equal(t, testShell, result)
}

func TestGetShellCommand_UnixDefaults(t *testing.T) {
	// Save original env var
	originalShell := os.Getenv("SHELL")
	originalDAGUShell := os.Getenv("DAGU_DEFAULT_SHELL")
	defer func() {
		if originalShell != "" {
			_ = os.Setenv("SHELL", originalShell)
		} else {
			_ = os.Unsetenv("SHELL")
		}
		if originalDAGUShell != "" {
			_ = os.Setenv("DAGU_DEFAULT_SHELL", originalDAGUShell)
		} else {
			_ = os.Unsetenv("DAGU_DEFAULT_SHELL")
		}
	}()

	// Clear env vars to test fallback
	_ = os.Unsetenv("SHELL")
	_ = os.Unsetenv("DAGU_DEFAULT_SHELL")

	result := GetShellCommand("")
	// Should find sh on Unix systems
	assert.NotEmpty(t, result)
	assert.Contains(t, result, "sh")
}

func TestIsExecutableFileInEnv_UnixMatchesExecutableBit(t *testing.T) {
	dir := t.TempDir()
	tool := filepath.Join(dir, "tool")
	require.NoError(t, os.WriteFile(tool, []byte("#!/bin/sh\n"), 0o755))
	plain := filepath.Join(dir, "plain")
	require.NoError(t, os.WriteFile(plain, []byte("x"), 0o644))

	assert.True(t, IsExecutableFileInEnv(tool, nil))
	assert.False(t, IsExecutableFileInEnv(plain, nil))
	assert.False(t, IsExecutableFileInEnv(filepath.Join(dir, "missing"), nil))
}

func TestLookPathInEnvDir_RelativePATHEntry(t *testing.T) {
	base := t.TempDir()
	bin := filepath.Join(base, "bin")
	require.NoError(t, os.MkdirAll(bin, 0o755))
	tool := filepath.Join(bin, "dagu-test-relpath")
	require.NoError(t, os.WriteFile(tool, []byte("#!/bin/sh\nexit 0\n"), 0o755))

	// A relative PATH entry resolves against dir, like the started process.
	resolved, err := LookPathInEnvDir("dagu-test-relpath", []string{"PATH=./bin"}, base)
	require.NoError(t, err)
	assert.Equal(t, tool, resolved)

	// Without a base dir the same entry resolves from the process directory.
	_, err = LookPathInEnv("dagu-test-relpath", []string{"PATH=./bin"})
	assert.Error(t, err)
}
