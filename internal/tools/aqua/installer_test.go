// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package aqua

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	aquaconfig "github.com/aquaproj/aqua/v2/pkg/config/aqua"
	aquaregistryconfig "github.com/aquaproj/aqua/v2/pkg/config/registry"
	aquaruntime "github.com/aquaproj/aqua/v2/pkg/runtime"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInferPackageCommandsUsesRegistryFiles(t *testing.T) {
	t.Parallel()

	commands, err := inferPackageCommands(
		slog.New(slog.DiscardHandler),
		&aquaconfig.Package{
			Name:    "example/tool",
			Version: "v1.0.0",
		},
		&aquaregistryconfig.PackageInfo{
			Name: "example/tool",
			Type: "github_release",
			Files: []*aquaregistryconfig.File{
				{Name: "tool"},
				{Name: "toolctl"},
			},
			SupportedEnvs: aquaregistryconfig.SupportedEnvs{"linux/amd64"},
		},
		&aquaruntime.Runtime{GOOS: "linux", GOARCH: "amd64"},
	)

	require.NoError(t, err)
	assert.Equal(t, []string{"tool", "toolctl"}, commands)
}

func TestInferPackageCommandsUsesDefaultCommandName(t *testing.T) {
	t.Parallel()

	commands, err := inferPackageCommands(
		slog.New(slog.DiscardHandler),
		&aquaconfig.Package{
			Name:    "google/pprof",
			Version: "d04f2422c8a17569c14e84da0fae252d9529826b",
		},
		&aquaregistryconfig.PackageInfo{
			Name:      "google/pprof",
			Type:      "go_install",
			RepoOwner: "google",
			RepoName:  "pprof",
			Path:      "github.com/google/pprof",
		},
		&aquaruntime.Runtime{GOOS: "linux", GOARCH: "amd64"},
	)

	require.NoError(t, err)
	assert.Equal(t, []string{"pprof"}, commands)
}

func TestInferPackageCommandsRejectsUnsafeRegistryFileName(t *testing.T) {
	t.Parallel()

	_, err := inferPackageCommands(
		slog.New(slog.DiscardHandler),
		&aquaconfig.Package{
			Name:    "example/tool",
			Version: "v1.0.0",
		},
		&aquaregistryconfig.PackageInfo{
			Name: "example/tool",
			Type: "github_release",
			Files: []*aquaregistryconfig.File{
				{Name: "tool;rm"},
			},
		},
		&aquaruntime.Runtime{GOOS: "linux", GOARCH: "amd64"},
	)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "specify commands explicitly")
}

func TestPackageCommandsRejectsUnsafeExplicitCommandName(t *testing.T) {
	t.Parallel()

	installer := New()
	_, err := installer.packageCommands(
		t.Context(),
		&core.ToolConfig{
			Packages: []core.ToolPackage{{
				Package:  "jqlang/jq",
				Version:  "jq-1.7.1",
				Commands: []string{"../jq"},
			}},
		},
		nil,
		tools.CacheLayout{},
		nil,
	)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be executable names")
}

func TestReadyManifestReturnsManifestWhenCommandsExist(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	paths := testCacheLayout(dir)
	require.NoError(t, os.MkdirAll(paths.BinDir, 0o750))
	commandPath := filepath.Join(paths.BinDir, "jq")
	require.NoError(t, os.WriteFile(commandPath, []byte("#!/bin/sh\n"), 0o700))

	manifest := &tools.Manifest{
		Provider:     providerAqua,
		Platform:     "linux/amd64",
		Hash:         "hash",
		RootDir:      paths.RootDir,
		EnvDir:       paths.EnvDir,
		BinDir:       paths.BinDir,
		Config:       paths.ConfigFile,
		ManifestFile: paths.ManifestFile,
		Commands: map[string]tools.Command{
			"jq": {
				Name:    "jq",
				Path:    commandPath,
				Package: "jqlang/jq",
				Version: "jq-1.7.1",
			},
		},
	}
	require.NoError(t, writeManifest(paths.ManifestFile, manifest))

	got, err := readyManifest(paths, "linux/amd64", "hash")

	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, commandPath, got.Commands["jq"].Path)
}

func TestReadyManifestMissesWhenCommandIsMissing(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	paths := testCacheLayout(dir)
	require.NoError(t, os.MkdirAll(paths.BinDir, 0o750))
	manifest := &tools.Manifest{
		Provider:     providerAqua,
		Platform:     "linux/amd64",
		Hash:         "hash",
		RootDir:      paths.RootDir,
		EnvDir:       paths.EnvDir,
		BinDir:       paths.BinDir,
		Config:       paths.ConfigFile,
		ManifestFile: paths.ManifestFile,
		Commands: map[string]tools.Command{
			"jq": {
				Name: "jq",
				Path: filepath.Join(paths.BinDir, "jq"),
			},
		},
	}
	require.NoError(t, writeManifest(paths.ManifestFile, manifest))

	got, err := readyManifest(paths, "linux/amd64", "hash")

	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestRegistryCacheReadyRequiresValidJSONCache(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	registryPath := filepath.Join(dir, "registry.yaml")
	require.NoError(t, os.WriteFile(registryPath+".json", []byte("{"), 0o600))
	assert.False(t, registryCacheReady(registryPath))

	require.NoError(t, os.WriteFile(registryPath+".json", []byte("{}"), 0o600))
	assert.True(t, registryCacheReady(registryPath))
}

func testCacheLayout(dir string) tools.CacheLayout {
	return tools.CacheLayout{
		RootDir:      filepath.Join(dir, "root"),
		LockDir:      filepath.Join(dir, "locks"),
		EnvDir:       filepath.Join(dir, "env"),
		BinDir:       filepath.Join(dir, "env", "bin"),
		ConfigFile:   filepath.Join(dir, "env", "aqua.yaml"),
		ChecksumFile: filepath.Join(dir, "env", "aqua-checksums.json"),
		ManifestFile: filepath.Join(dir, "env", "manifest.json"),
	}
}
