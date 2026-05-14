// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCachePathsUsesWorkerLocalDataDir(t *testing.T) {
	t.Parallel()

	paths, err := CachePaths("/var/lib/dagu/data", "linux/amd64", "abc123")

	require.NoError(t, err)
	assert.Equal(t, filepath.Join("/var/lib/dagu/data", "tools", "aqua", "root"), paths.RootDir)
	assert.Equal(t, filepath.Join("/var/lib/dagu/data", "tools", "aqua", "envs", "linux-amd64", "abc123"), paths.EnvDir)
	assert.Equal(t, filepath.Join(paths.EnvDir, "bin"), paths.BinDir)
	assert.Equal(t, filepath.Join(paths.EnvDir, "aqua.yaml"), paths.ConfigFile)
	assert.Equal(t, filepath.Join(paths.EnvDir, "aqua-checksums.json"), paths.ChecksumFile)
	assert.Equal(t, filepath.Join(paths.EnvDir, "manifest.json"), paths.ManifestFile)
}

func TestCachePathsSanitizesPlatformPathSegment(t *testing.T) {
	t.Parallel()

	paths, err := CachePaths("/var/lib/dagu/data", "linux/amd64:ci worker\\x", "abc123")

	require.NoError(t, err)
	assert.Equal(t, filepath.Join("/var/lib/dagu/data", "tools", "aqua", "envs", "linux-amd64-ci-worker-x", "abc123"), paths.EnvDir)
}

func TestToolsetHashChangesWithPlatform(t *testing.T) {
	t.Parallel()

	cfg := &core.ToolConfig{
		Provider: "aqua",
		Packages: []core.ToolPackage{{
			Name:     "jq",
			Package:  "jqlang/jq",
			Version:  "jq-1.7.1",
			Commands: []string{"jq"},
		}},
	}

	linuxHash, err := ToolsetHash(cfg, "linux/amd64")
	require.NoError(t, err)
	windowsHash, err := ToolsetHash(cfg, "windows/amd64")
	require.NoError(t, err)

	assert.NotEmpty(t, linuxHash)
	assert.NotEqual(t, linuxHash, windowsHash)
}

func TestEnvVarsExposeAquaToolset(t *testing.T) {
	t.Parallel()

	envs := EnvVars(&Manifest{
		RootDir:      "/var/lib/dagu/data/tools/aqua/root",
		EnvDir:       "/var/lib/dagu/data/tools/aqua/envs/linux-amd64/hash",
		BinDir:       "/var/lib/dagu/data/tools/aqua/envs/linux-amd64/hash/bin",
		Config:       "/var/lib/dagu/data/tools/aqua/envs/linux-amd64/hash/aqua.yaml",
		Checksum:     "/var/lib/dagu/data/tools/aqua/envs/linux-amd64/hash/aqua-checksums.json",
		ManifestFile: "/var/lib/dagu/data/tools/aqua/envs/linux-amd64/hash/manifest.json",
	}, "/usr/bin")

	assert.Contains(t, envs, "AQUA_ROOT_DIR=/var/lib/dagu/data/tools/aqua/root")
	assert.Contains(t, envs, "AQUA_CONFIG=/var/lib/dagu/data/tools/aqua/envs/linux-amd64/hash/aqua.yaml")
	assert.Contains(t, envs, "AQUA_DISABLE_LAZY_INSTALL=true")
	assert.Contains(t, envs, "AQUA_CHECKSUM=true")
	assert.Contains(t, envs, "AQUA_REQUIRE_CHECKSUM=true")
	assert.Contains(t, envs, "AQUA_ENFORCE_CHECKSUM=true")
	assert.Contains(t, envs, "AQUA_ENFORCE_REQUIRE_CHECKSUM=true")
	assert.Contains(t, envs, "DAGU_TOOLS_MANIFEST=/var/lib/dagu/data/tools/aqua/envs/linux-amd64/hash/manifest.json")
	assert.Contains(t, envs, "PATH=/var/lib/dagu/data/tools/aqua/envs/linux-amd64/hash/bin:/usr/bin")
}
