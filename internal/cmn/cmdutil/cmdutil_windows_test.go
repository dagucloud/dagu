// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package cmdutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetShellCommand_WindowsPrefersNativeShellsOverSHELL(t *testing.T) {
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

	_ = os.Unsetenv("DAGU_DEFAULT_SHELL")
	_ = os.Setenv("SHELL", `/usr/bin/bash`)

	result := GetShellCommand("")
	base := strings.TrimSuffix(strings.ToLower(filepath.Base(result)), ".exe")

	assert.NotEmpty(t, result)
	assert.Contains(t, []string{"powershell", "pwsh", "cmd"}, base)
	assert.NotEqual(t, "bash", base)
}

func TestIsExecutableFileInEnv_PATHEXT(t *testing.T) {
	dir := t.TempDir()
	envs := []string{"PATHEXT=.COM;.EXE;.BAT;.CMD"}

	write := func(name string) string {
		p := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(p, []byte("x"), 0o644))
		return p
	}

	assert.False(t, IsExecutableFileInEnv(write("tool.txt"), envs),
		"a non-PATHEXT extension resolves at lookup but cannot launch")
	assert.True(t, IsExecutableFileInEnv(write("real.exe"), envs))
	assert.False(t, IsExecutableFileInEnv(write("bare"), envs),
		"extensionless files are never resolved by the starter")
	assert.False(t, IsExecutableFileInEnv(filepath.Join(dir, "missing.exe"), envs))

	// An extensionless name skips the bare file, like os/exec: npm installs a
	// POSIX shim named tool next to tool.cmd.
	write("shim")
	shimCmd := write("shim.cmd")
	assert.True(t, IsExecutableFileInEnv(filepath.Join(dir, "shim"), envs))
	resolved, err := LookPathInEnvDir("shim", append([]string{"PATH=" + dir}, envs...), "")
	require.NoError(t, err)
	assert.Equal(t, strings.ToLower(shimCmd), strings.ToLower(resolved))

	// A dotted name still gains PATHEXT suffixes: tool.v2 -> tool.v2.exe.
	write("dotted.v2.exe")
	assert.True(t, IsExecutableFileInEnv(filepath.Join(dir, "dotted.v2"), envs))

	// A custom PATHEXT controls which extensions count as launchable.
	write("plain.txt")
	assert.True(t, IsExecutableFileInEnv(filepath.Join(dir, "plain.txt"), []string{"PATHEXT=.TXT"}))

	assert.False(t, IsExecutableFileInEnv(filepath.Join(dir, "dotted.v2"), []string{"PATHEXT=.BAT"}),
		"only listed PATHEXT suffixes resolve")
}

func TestLookPathInEnvDir_RelativePATHEntry(t *testing.T) {
	base := t.TempDir()
	bin := filepath.Join(base, "bin")
	require.NoError(t, os.MkdirAll(bin, 0o755))
	tool := filepath.Join(bin, "mytool.exe")
	require.NoError(t, os.WriteFile(tool, []byte("x"), 0o644))

	// A relative PATH entry resolves against dir, like the started process.
	resolved, err := LookPathInEnvDir("mytool", []string{"PATH=bin;"}, base)
	require.NoError(t, err)
	// The resolved name carries the PATHEXT extension's case, like exec.LookPath.
	assert.Equal(t, strings.ToLower(tool), strings.ToLower(resolved))

	// A txt payload shadows nothing and cannot launch.
	require.NoError(t, os.WriteFile(filepath.Join(bin, "doc.txt"), []byte("x"), 0o644))
	_, err = LookPathInEnvDir("doc.txt", []string{"PATH=bin"}, base)
	assert.Error(t, err)
}
