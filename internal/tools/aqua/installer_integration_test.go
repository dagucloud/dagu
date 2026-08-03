// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package aqua

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/core"
	"github.com/dagucloud/dagu/v2/internal/tools"
	"github.com/stretchr/testify/require"
)

func TestInstallerInstallIntegration(t *testing.T) {
	if os.Getenv("DAGU_AQUA_INTEGRATION") != "1" {
		t.Skip("set DAGU_AQUA_INTEGRATION=1 to run aqua network integration test")
	}

	toolsDir := filepath.Join(t.TempDir(), "tools")
	workDir := t.TempDir()
	manifest, err := New().Install(context.Background(), &core.ToolConfig{
		Provider: "aqua",
		Packages: []core.ToolPackage{{
			Name:    "jq",
			Package: "jqlang/jq",
			Version: "jq-1.7.1",
		}},
	}, tools.InstallOptions{
		ToolsDir: toolsDir,
		WorkDir:  workDir,
	})

	require.NoError(t, err)
	require.NotNil(t, manifest)
	require.FileExists(t, filepath.Join(manifest.EnvDir, "aqua.yaml"))
	require.FileExists(t, manifest.Checksum)
	require.FileExists(t, filepath.Join(manifest.EnvDir, "manifest.json"))
	require.NotEmpty(t, manifest.Commands["jq"].Path)
	require.Equal(t, filepath.Join(toolsDir, "aqua", "root"), manifest.RootDir)
	require.Equal(t, filepath.Join(manifest.EnvDir, "bin"), manifest.BinDir)
	require.Equal(t, filepath.Join(manifest.BinDir, filepath.Base(manifest.Commands["jq"].Path)), manifest.Commands["jq"].Path)
	require.FileExists(t, manifest.Commands["jq"].Path)
}

func TestInstallerFallbackToLatestRegistryIntegration(t *testing.T) {
	if os.Getenv("DAGU_AQUA_INTEGRATION") != "1" {
		t.Skip("set DAGU_AQUA_INTEGRATION=1 to run aqua network integration test")
	}

	installer := New()
	// A snapshot that predates the earendil-works/pi registry entry, so the
	// pinned attempt misses and resolution falls back to the latest release.
	installer.standardRegistryRef = "5e2f56743d66abe9dfc7c56d35086511b7dc92d8"

	manifest, err := installer.Install(context.Background(), &core.ToolConfig{
		Provider: "aqua",
		Packages: []core.ToolPackage{{
			Package: "earendil-works/pi",
			Version: "v0.83.0",
		}},
	}, tools.InstallOptions{
		ToolsDir: filepath.Join(t.TempDir(), "tools"),
		WorkDir:  t.TempDir(),
	})

	require.NoError(t, err)
	require.NotNil(t, manifest)
	require.NotEmpty(t, manifest.Commands["pi"].Path)
	require.FileExists(t, manifest.Commands["pi"].Path)
}
