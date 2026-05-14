// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package aqua

import (
	"os"
	"path/filepath"
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

func TestCommandShimNameAddsWindowsExecutableExtension(t *testing.T) {
	t.Parallel()

	shim := commandShimName("jq", `C:\tools\jq.exe`, "windows/amd64")

	assert.Equal(t, "jq.exe", shim)
}
