// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package aqua

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/tools"
	"github.com/stretchr/testify/require"
)

func TestInstallerInstallIntegration(t *testing.T) {
	if os.Getenv("DAGU_AQUA_INTEGRATION") != "1" {
		t.Skip("set DAGU_AQUA_INTEGRATION=1 to run aqua network integration test")
	}

	toolsDir := filepath.Join(t.TempDir(), "tools")
	workDir := t.TempDir()
	manifest, err := New().Install(context.Background(), &ir.ToolConfig{
		Provider: "aqua",
		Packages: []ir.ToolPackage{{
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

func TestInstallerDigestPinningIntegration(t *testing.T) {
	if os.Getenv("DAGU_AQUA_INTEGRATION") != "1" {
		t.Skip("set DAGU_AQUA_INTEGRATION=1 to run aqua network integration test")
	}

	jq := ir.ToolPackage{Name: "jq", Package: "jqlang/jq", Version: "jq-1.7.1"}

	// First install records the artifact checksum aqua verified.
	seedDir := filepath.Join(t.TempDir(), "tools")
	seedManifest, err := New().Install(context.Background(), &ir.ToolConfig{
		Packages: []ir.ToolPackage{jq},
	}, tools.InstallOptions{ToolsDir: seedDir, WorkDir: t.TempDir()})
	require.NoError(t, err)

	data, err := os.ReadFile(seedManifest.Checksum)
	require.NoError(t, err)
	var recorded checksumFileContent
	require.NoError(t, json.Unmarshal(data, &recorded))
	digest := ""
	for _, entry := range recorded.Checksums {
		if strings.Contains(entry.ID, "github.com/jqlang/jq/jq-1.7.1/") {
			digest = "sha256:" + strings.ToLower(entry.Checksum)
			break
		}
	}
	require.NotEmpty(t, digest, "expected a recorded jq artifact checksum")

	// A fresh tools dir with the recorded digest must verify and expose an
	// aqua root isolated under the toolset env.
	pinned := jq
	pinned.Digest = digest
	goodDir := filepath.Join(t.TempDir(), "tools")
	manifest, err := New().Install(context.Background(), &ir.ToolConfig{
		Packages: []ir.ToolPackage{pinned},
	}, tools.InstallOptions{ToolsDir: goodDir, WorkDir: t.TempDir()})
	require.NoError(t, err)
	require.FileExists(t, manifest.Commands["jq"].Path)
	require.Equal(t, filepath.Join(manifest.EnvDir, "root"), manifest.RootDir)

	// A wrong digest must fail with a mismatch naming both hashes.
	wrong := jq
	wrong.Digest = "sha256:" + strings.Repeat("0", 64)
	_, err = New().Install(context.Background(), &ir.ToolConfig{
		Packages: []ir.ToolPackage{wrong},
	}, tools.InstallOptions{ToolsDir: filepath.Join(t.TempDir(), "tools"), WorkDir: t.TempDir()})
	require.Error(t, err)
	require.Contains(t, err.Error(), "digest mismatch for jqlang/jq@jq-1.7.1")
}

func TestInstallerLatestRegistryIntegration(t *testing.T) {
	if os.Getenv("DAGU_AQUA_INTEGRATION") != "1" {
		t.Skip("set DAGU_AQUA_INTEGRATION=1 to run aqua network integration test")
	}

	// earendil-works/pi entered the aqua registry on 2026-05-11, after the
	// compiled-in bootstrap ref; installing it proves resolution runs against
	// the latest registry release rather than the bootstrap snapshot.
	manifest, err := New().Install(context.Background(), &ir.ToolConfig{
		Provider: "aqua",
		Packages: []ir.ToolPackage{{
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
