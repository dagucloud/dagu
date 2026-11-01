// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCachePathsUsesWorkerLocalToolsDir(t *testing.T) {
	t.Parallel()

	paths, err := CachePaths("/var/cache/dagu/tools", "linux/amd64", "abc123")

	require.NoError(t, err)
	assert.Equal(t, filepath.Join("/var/cache/dagu/tools", "aqua", "root"), paths.RootDir)
	assert.Equal(t, filepath.Join("/var/cache/dagu/tools", "aqua", "locks"), paths.LockDir)
	assert.Equal(t, filepath.Join("/var/cache/dagu/tools", "aqua", "envs", "linux-amd64", "abc123"), paths.EnvDir)
	assert.Equal(t, filepath.Join(paths.EnvDir, "bin"), paths.BinDir)
	assert.Equal(t, filepath.Join(paths.EnvDir, "aqua.yaml"), paths.ConfigFile)
	assert.Equal(t, filepath.Join(paths.EnvDir, "aqua-checksums.json"), paths.ChecksumFile)
	assert.Equal(t, filepath.Join(paths.EnvDir, "manifest.json"), paths.ManifestFile)
}

func TestCachePathsSanitizesPlatformPathSegment(t *testing.T) {
	t.Parallel()

	paths, err := CachePaths("/var/cache/dagu/tools", "linux/amd64:ci worker\\x", "abc123")

	require.NoError(t, err)
	assert.Equal(t, filepath.Join("/var/cache/dagu/tools", "aqua", "envs", "linux-amd64-ci-worker-x", "abc123"), paths.EnvDir)
}

func TestToolsetHashChangesWithPlatform(t *testing.T) {
	t.Parallel()

	cfg := &ir.ToolConfig{
		Provider: "aqua",
		Packages: []ir.ToolPackage{{
			Name:     "jq",
			Package:  "jqlang/jq",
			Version:  "jq-1.7.1",
			Commands: []string{"jq"},
		}},
	}

	linuxHash, snapshot, err := ToolsetHash(cfg, "linux/amd64")
	require.NoError(t, err)
	require.Nil(t, snapshot)
	windowsHash, _, err := ToolsetHash(cfg, "windows/amd64")
	require.NoError(t, err)

	assert.NotEmpty(t, linuxHash)
	assert.NotEqual(t, linuxHash, windowsHash)
}

func TestToolsetHashLocalRegistry(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "registry.yaml")
	cfg := &ir.ToolConfig{Registry: &ir.ToolRegistry{Type: "local", Path: path}}
	require.NoError(t, os.WriteFile(path, []byte("first"), 0o600))
	first, snapshot, err := ToolsetHash(cfg, "linux/amd64")
	require.NoError(t, err)
	require.Equal(t, "first", string(snapshot))
	again, _, err := ToolsetHash(cfg, "linux/amd64")
	require.NoError(t, err)
	require.Equal(t, first, again)
	require.NoError(t, os.WriteFile(path, []byte("changed"), 0o600))
	changed, _, err := ToolsetHash(cfg, "linux/amd64")
	require.NoError(t, err)
	require.NotEqual(t, first, changed)
	require.Equal(t, "first", string(snapshot))
	require.NoError(t, os.Remove(path))
	_, _, err = ToolsetHash(cfg, "linux/amd64")
	require.Error(t, err)
}

// The documented limit must reject oversized files instead of hashing a prefix.
func TestToolsetHashLocalRegistryLimit(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		size      int64
		wantError bool
	}{
		{"at limit", 16 << 20, false}, {"over limit", (16 << 20) + 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "registry.yaml")
			file, err := os.Create(path)
			require.NoError(t, err)
			require.NoError(t, file.Truncate(tc.size))
			require.NoError(t, file.Close())
			cfg := &ir.ToolConfig{Registry: &ir.ToolRegistry{Type: "local", Path: path}}
			hash, snapshot, err := ToolsetHash(cfg, "linux/amd64")
			if tc.wantError {
				require.ErrorContains(t, err, "exceeds")
				require.Empty(t, hash)
				require.Nil(t, snapshot)
			} else {
				require.NoError(t, err)
				require.NotEmpty(t, hash)
				require.Len(t, snapshot, int(tc.size))
			}
		})
	}
}

func TestToolsetHashLocalRegistryDirectory(t *testing.T) {
	t.Parallel()
	_, _, err := ToolsetHash(&ir.ToolConfig{Registry: &ir.ToolRegistry{Type: "local", Path: t.TempDir()}}, "linux/amd64")
	require.ErrorContains(t, err, "not a regular file")
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
	assert.Contains(t, envs, "PATH=/var/lib/dagu/data/tools/aqua/envs/linux-amd64/hash/bin"+string(os.PathListSeparator)+"/usr/bin")
}

func TestPrepareDAGInstallsDeclaredTools(t *testing.T) {
	t.Parallel()

	installer := &fakeInstaller{
		manifest: &Manifest{
			RootDir:      "/data/tools/aqua/root",
			EnvDir:       "/data/tools/aqua/envs/linux-amd64/hash",
			BinDir:       "/data/tools/aqua/envs/linux-amd64/hash/bin",
			Config:       "/data/tools/aqua/envs/linux-amd64/hash/aqua.yaml",
			ManifestFile: "/data/tools/aqua/envs/linux-amd64/hash/manifest.json",
		},
	}
	dag := &ir.DAG{
		Name:       "tool-dag",
		WorkingDir: "/work",
		Tools: &ir.ToolConfig{
			Provider: "aqua",
			Packages: []ir.ToolPackage{{
				Package: "jqlang/jq",
				Version: "jq-1.7.1",
			}},
		},
	}

	envs, err := PrepareDAG(context.Background(), dag, installer, InstallOptions{
		ToolsDir: "/data/tools",
		WorkDir:  "/work",
	}, "/usr/bin")

	require.NoError(t, err)
	require.Equal(t, 1, installer.calls)
	assert.Same(t, dag.Tools, installer.cfg)
	assert.Equal(t, InstallOptions{ToolsDir: "/data/tools", WorkDir: "/work"}, installer.opts)
	assert.Contains(t, envs, "PATH=/data/tools/aqua/envs/linux-amd64/hash/bin"+string(os.PathListSeparator)+"/usr/bin")
}

func TestPrepareDAGLogsToolPreparationStatus(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	ctx := logger.WithFixedLogger(context.Background(), logger.NewLogger(
		logger.WithQuiet(),
		logger.WithFormat("text"),
		logger.WithWriter(&output),
	))
	installer := &fakeInstaller{
		manifest: &Manifest{
			RootDir:      "/data/tools/aqua/root",
			EnvDir:       "/data/tools/aqua/envs/linux-amd64/hash",
			BinDir:       "/data/tools/aqua/envs/linux-amd64/hash/bin",
			Config:       "/data/tools/aqua/envs/linux-amd64/hash/aqua.yaml",
			ManifestFile: "/data/tools/aqua/envs/linux-amd64/hash/manifest.json",
			Commands: map[string]Command{
				"jq": {Name: "jq", Package: "jqlang/jq", Version: "jq-1.7.1"},
			},
		},
	}
	dag := &ir.DAG{
		Name: "tool-dag",
		Tools: &ir.ToolConfig{
			Provider: "aqua",
			Packages: []ir.ToolPackage{{
				Package: "jqlang/jq",
				Version: "jq-1.7.1",
			}},
		},
	}

	_, err := PrepareDAG(ctx, dag, installer, InstallOptions{}, "/usr/bin")

	require.NoError(t, err)
	assert.Contains(t, output.String(), "Preparing DAG tools")
	assert.Contains(t, output.String(), "DAG tools ready")
}

func TestPrepareDAGRejectsUnsupportedExecutor(t *testing.T) {
	t.Parallel()

	dag := &ir.DAG{
		Name: "tool-dag",
		Tools: &ir.ToolConfig{
			Provider: "aqua",
			Packages: []ir.ToolPackage{{Package: "jqlang/jq", Version: "jq-1.7.1"}},
		},
		Steps: []ir.Step{{
			Name:           "container-step",
			ExecutorConfig: ir.ExecutorConfig{Type: "docker"},
		}},
	}
	installer := &fakeInstaller{}

	envs, err := PrepareDAG(context.Background(), dag, installer, InstallOptions{}, "")

	require.Error(t, err)
	assert.Nil(t, envs)
	assert.Zero(t, installer.calls)
	assert.Contains(t, err.Error(), `tools are not supported with executor "docker"`)
}

type fakeInstaller struct {
	calls    int
	cfg      *ir.ToolConfig
	opts     InstallOptions
	manifest *Manifest
	err      error
}

func (f *fakeInstaller) Install(_ context.Context, cfg *ir.ToolConfig, opts InstallOptions) (*Manifest, error) {
	f.calls++
	f.cfg = cfg
	f.opts = opts
	return f.manifest, f.err
}
