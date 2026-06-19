// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package docker_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/internal/core"
	dockerruntime "github.com/dagucloud/dagu/internal/runtime/builtin/docker"
	"github.com/moby/moby/api/types/mount"
	"github.com/stretchr/testify/require"
)

func TestLoadConfigExpandsHostEnvInVolumeSource(t *testing.T) {
	codexHome := filepath.Join(t.TempDir(), ".codex")
	t.Setenv("DAGU_TEST_CODEX_HOME", codexHome)

	cfg, err := dockerruntime.LoadConfig("", core.Container{
		Image:   "dagu-codex-runner:local",
		Volumes: []string{"${DAGU_TEST_CODEX_HOME}:/codex-home:ro"},
	}, nil)

	require.NoError(t, err)
	require.Equal(t, []string{codexHome + ":/codex-home:ro"}, cfg.Host.Binds)
}

func TestLoadConfigFailsClearlyForMissingHostEnvInVolumeSource(t *testing.T) {
	_, err := dockerruntime.LoadConfig("", core.Container{
		Image:   "dagu-codex-runner:local",
		Volumes: []string{"${DAGU_TEST_MISSING_CODEX_HOME}:/codex-home"},
	}, nil)

	require.Error(t, err)
	require.Contains(t, err.Error(), "container.volumes[0]")
	require.Contains(t, err.Error(), "DAGU_TEST_MISSING_CODEX_HOME")
}

func TestLoadConfigPreservesExpandedNamedVolume(t *testing.T) {
	t.Setenv("DAGU_TEST_VOLUME_NAME", "dagu-cache")

	cfg, err := dockerruntime.LoadConfig("", core.Container{
		Image:   "alpine",
		Volumes: []string{"${DAGU_TEST_VOLUME_NAME}:/cache"},
	}, nil)

	require.NoError(t, err)
	require.Empty(t, cfg.Host.Binds)
	require.Equal(t, []mount.Mount{{
		Type:   mount.TypeVolume,
		Source: "dagu-cache",
		Target: "/cache",
	}}, cfg.Host.Mounts)
}

func TestLoadConfigResolvesExpandedRelativeBindSourceFromWorkDir(t *testing.T) {
	workDir := t.TempDir()
	t.Setenv("DAGU_TEST_REL_CODEX_HOME", "./.codex")

	cfg, err := dockerruntime.LoadConfig(workDir, core.Container{
		Image:   "dagu-codex-runner:local",
		Volumes: []string{"${DAGU_TEST_REL_CODEX_HOME}:/codex-home"},
	}, nil)

	require.NoError(t, err)
	require.Len(t, cfg.Host.Binds, 1)
	require.True(t, strings.HasPrefix(cfg.Host.Binds[0], filepath.Join(workDir, ".codex")+":/codex-home:"))
}

func TestLoadConfigFromMapWithWorkDirExpandsShortcutVolumeSource(t *testing.T) {
	codexHome := filepath.Join(t.TempDir(), ".codex")
	t.Setenv("DAGU_TEST_CODEX_HOME", codexHome)

	cfg, err := dockerruntime.LoadConfigFromMapWithWorkDir("", map[string]any{
		"image":   "dagu-codex-runner:local",
		"volumes": []string{"${DAGU_TEST_CODEX_HOME}:/codex-home:ro"},
	}, nil)

	require.NoError(t, err)
	require.Equal(t, []string{codexHome + ":/codex-home:ro"}, cfg.Host.Binds)
}

func TestLoadConfigFromMapWithWorkDirResolvesShortcutRelativeSource(t *testing.T) {
	workDir := t.TempDir()
	t.Setenv("DAGU_TEST_REL_CODEX_HOME", "./.codex")

	cfg, err := dockerruntime.LoadConfigFromMapWithWorkDir(workDir, map[string]any{
		"image":   "dagu-codex-runner:local",
		"volumes": []string{"${DAGU_TEST_REL_CODEX_HOME}:/codex-home"},
	}, nil)

	require.NoError(t, err)
	require.Equal(t, []string{filepath.Join(workDir, ".codex") + ":/codex-home:rw"}, cfg.Host.Binds)
}

func TestLoadConfigFromMapWithWorkDirPreservesDotRelativeSourceComponents(t *testing.T) {
	workDir := t.TempDir()

	tests := []struct {
		name     string
		source   string
		expected string
	}{
		{
			name:     "DotPrefixedDirectory",
			source:   ".codex",
			expected: filepath.Join(workDir, ".codex"),
		},
		{
			name:     "ParentRelativeDirectory",
			source:   "../data",
			expected: filepath.Clean(filepath.Join(workDir, "../data")),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("DAGU_TEST_REL_SOURCE", tt.source)

			cfg, err := dockerruntime.LoadConfigFromMapWithWorkDir(workDir, map[string]any{
				"image":   "alpine",
				"volumes": []string{"${DAGU_TEST_REL_SOURCE}:/data"},
			}, nil)

			require.NoError(t, err)
			require.Equal(t, []string{tt.expected + ":/data:rw"}, cfg.Host.Binds)
		})
	}
}

func TestLoadConfigFromMapWithWorkDirFailsClearlyForMissingShortcutVolumeEnv(t *testing.T) {
	_, err := dockerruntime.LoadConfigFromMapWithWorkDir("", map[string]any{
		"image":   "dagu-codex-runner:local",
		"volumes": []string{"${DAGU_TEST_MISSING_CODEX_HOME}:/codex-home"},
	}, nil)

	require.Error(t, err)
	require.Contains(t, err.Error(), "executor.config.volumes[0]")
	require.Contains(t, err.Error(), "DAGU_TEST_MISSING_CODEX_HOME")
}
