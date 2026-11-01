// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package aqua

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateCommandShimUsesEnvLocalBin(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	target := filepath.Join(dir, "real-tool")
	require.NoError(t, os.WriteFile(target, []byte("#!/bin/sh\nexit 0\n"), 0o755))

	shim, err := createCommandShim(filepath.Join(dir, "bin"), "tool", target, "linux/amd64")

	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "bin", "tool"), shim)
	require.FileExists(t, shim)
}

func TestCreateCommandShimKeepsMatchingSymlink(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("symlink behavior is platform-specific")
	}

	dir := t.TempDir()
	target := filepath.Join(dir, "real-tool")
	require.NoError(t, os.WriteFile(target, []byte("#!/bin/sh\nexit 0\n"), 0o755))
	binDir := filepath.Join(dir, "bin")
	require.NoError(t, os.MkdirAll(binDir, 0o750))
	shimPath := filepath.Join(binDir, "tool")
	require.NoError(t, os.Symlink(target, shimPath))

	shim, err := createCommandShim(binDir, "tool", target, "linux/amd64")

	require.NoError(t, err)
	assert.Equal(t, shimPath, shim)
	info, err := os.Lstat(shimPath)
	require.NoError(t, err)
	assert.NotZero(t, info.Mode()&os.ModeSymlink)
	linkTarget, err := os.Readlink(shimPath)
	require.NoError(t, err)
	assert.Equal(t, target, linkTarget)
}

func TestCreateCommandShimResolvesSymlinkedTarget(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("symlink behavior is platform-specific")
	}

	dir := t.TempDir()
	pkgBin := filepath.Join(dir, "pkg", "bin")
	realScript := filepath.Join(dir, "pkg", "lib", "npm-cli.js")
	require.NoError(t, os.MkdirAll(pkgBin, 0o750))
	require.NoError(t, os.MkdirAll(filepath.Dir(realScript), 0o750))
	require.NoError(t, os.WriteFile(realScript, []byte("#!/bin/sh\nexit 0\n"), 0o755))
	target := filepath.Join(pkgBin, "npm")
	require.NoError(t, os.Symlink("../lib/npm-cli.js", target))
	binDir := filepath.Join(dir, "bin")
	require.NoError(t, os.MkdirAll(binDir, 0o750))
	// A dangling shim left by an earlier version must be replaced.
	require.NoError(t, os.Symlink("../lib/npm-cli.js", filepath.Join(binDir, "npm")))

	shim, err := createCommandShim(binDir, "npm", target, "linux/amd64")

	require.NoError(t, err)
	info, err := os.Stat(shim)
	require.NoError(t, err, "shim must not dangle")
	assert.NotZero(t, info.Mode()&0o111)
	linkTarget, err := os.Readlink(shim)
	require.NoError(t, err)
	assert.Equal(t, target, linkTarget)
}

func TestCommandShimNameAddsWindowsExecutableExtension(t *testing.T) {
	t.Parallel()

	shim := commandShimName("jq", `C:\tools\jq.exe`, "windows/amd64")

	assert.Equal(t, "jq.exe", shim)
}
