// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package aqua

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/tools"
	"github.com/stretchr/testify/require"
)

func TestInstallerInstallIntegration(t *testing.T) {
	if os.Getenv("DAGU_AQUA_INTEGRATION") != "1" {
		t.Skip("set DAGU_AQUA_INTEGRATION=1 to run aqua network integration test")
	}

	dataDir := t.TempDir()
	workDir := t.TempDir()
	manifest, err := New().Install(context.Background(), &core.ToolConfig{
		Provider: "aqua",
		Packages: []core.ToolPackage{{
			Name:    "jq",
			Package: "jqlang/jq",
			Version: "jq-1.7.1",
		}},
	}, tools.InstallOptions{
		DataDir: dataDir,
		WorkDir: workDir,
	})

	require.NoError(t, err)
	require.NotNil(t, manifest)
	require.FileExists(t, filepath.Join(manifest.EnvDir, "aqua.yaml"))
	require.FileExists(t, manifest.Checksum)
	require.FileExists(t, filepath.Join(manifest.EnvDir, "manifest.json"))
	require.NotEmpty(t, manifest.Commands["jq"].Path)
	require.Equal(t, filepath.Join(manifest.EnvDir, "bin"), manifest.BinDir)
	require.Equal(t, filepath.Join(manifest.BinDir, filepath.Base(manifest.Commands["jq"].Path)), manifest.Commands["jq"].Path)
	require.FileExists(t, manifest.Commands["jq"].Path)
}
