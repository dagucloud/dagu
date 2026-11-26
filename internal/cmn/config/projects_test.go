// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package config_test

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func projectsTestConfig(t *testing.T, enabled bool) *config.Config {
	t.Helper()
	root := t.TempDir()
	return &config.Config{
		ProjectsEnabled: enabled,
		Paths: config.PathsConfig{
			DAGsDir:               filepath.Join(root, "dags"),
			ProjectsDir:           filepath.Join(root, "projects"),
			DataDir:               filepath.Join(root, "data"),
			LogDir:                filepath.Join(root, "logs"),
			ArtifactDir:           filepath.Join(root, "data", "artifacts"),
			DAGRunsDir:            filepath.Join(root, "data", "dag-runs"),
			DAGRunWorkDir:         filepath.Join(root, "data", "dag-run-work"),
			EventStoreDir:         filepath.Join(root, "logs", "admin", "events"),
			DAGStateDir:           filepath.Join(root, "data", "dag-state"),
			QueueDir:              filepath.Join(root, "queue"),
			ProcDir:               filepath.Join(root, "data", "proc"),
			SuspendFlagsDir:       filepath.Join(root, "data", "suspend"),
			SuspendFlagsDirLegacy: filepath.Join(root, "suspend"),
			ViewsDir:              filepath.Join(root, "data", "views"),
		},
	}
}

func TestProjectPaths(t *testing.T) {
	t.Parallel()

	t.Run("OffDefaultIsV2", func(t *testing.T) {
		t.Parallel()
		cfg := projectsTestConfig(t, false)

		paths, err := cfg.ProjectPaths("default")
		require.NoError(t, err)
		assert.Equal(t, config.ProjectPaths{
			DAGsDir:               cfg.Paths.DAGsDir,
			StateDir:              cfg.Paths.DataDir,
			LogDir:                cfg.Paths.LogDir,
			ArtifactDir:           cfg.Paths.ArtifactDir,
			DAGRunsDir:            cfg.Paths.DAGRunsDir,
			DAGRunWorkDir:         cfg.Paths.DAGRunWorkDir,
			EventStoreDir:         cfg.Paths.EventStoreDir,
			SettingsDir:           cfg.Paths.DataDir,
			OtherStateDir:         cfg.Paths.DataDir,
			DAGStateDir:           cfg.Paths.DAGStateDir,
			QueueDir:              cfg.Paths.QueueDir,
			ProcDir:               cfg.Paths.ProcDir,
			SuspendFlagsDir:       cfg.Paths.SuspendFlagsDir,
			LegacySuspendFlagsDir: cfg.Paths.SuspendFlagsDirLegacy,
			ViewsDir:              cfg.Paths.ViewsDir,
		}, paths)
	})

	t.Run("OffRefusesOtherProjects", func(t *testing.T) {
		t.Parallel()
		_, err := projectsTestConfig(t, false).ProjectPaths("weather")
		require.Error(t, err)
	})

	t.Run("OnDefault", func(t *testing.T) {
		t.Parallel()
		cfg := projectsTestConfig(t, true)
		state := filepath.Join(cfg.Paths.DataDir, "projects", "default")

		// The registry spells the default project "default"; another case
		// still resolves to that one spelling on disk.
		// paths.queue_dir and the other paths settings of project stores
		// only tell migration where v2 kept their data.
		for _, name := range []string{"default", "Default"} {
			paths, err := cfg.ProjectPaths(name)
			require.NoError(t, err)
			assert.Equal(t, config.ProjectPaths{
				DAGsDir:         cfg.Paths.DAGsDir,
				StateDir:        state,
				LogDir:          filepath.Join(state, "logs"),
				ArtifactDir:     filepath.Join(state, "artifacts"),
				DAGRunsDir:      filepath.Join(state, "runs"),
				DAGRunWorkDir:   filepath.Join(state, "state", "dag-run-work"),
				EventStoreDir:   filepath.Join(state, "state", "events"),
				SettingsDir:     filepath.Join(state, "settings"),
				OtherStateDir:   filepath.Join(state, "state"),
				DAGStateDir:     filepath.Join(state, "state", "dag-state"),
				QueueDir:        filepath.Join(state, "state", "queue"),
				ProcDir:         filepath.Join(state, "state", "proc"),
				SuspendFlagsDir: filepath.Join(state, "state", "suspend"),
				ViewsDir:        filepath.Join(state, "state", "views"),
				RecordDirs:      []string{state},
			}, paths)
		}
	})

	t.Run("OnProject", func(t *testing.T) {
		t.Parallel()
		cfg := projectsTestConfig(t, true)
		state := filepath.Join(cfg.Paths.DataDir, "projects", "weather")

		paths, err := cfg.ProjectPaths("weather")
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(cfg.Paths.ProjectsDir, "weather"), paths.DAGsDir)
		assert.Equal(t, state, paths.StateDir)
		assert.Equal(t, filepath.Join(state, "runs"), paths.DAGRunsDir)
		assert.Equal(t, []string{state}, paths.RecordDirs)
	})

	t.Run("OnSetApartPaths", func(t *testing.T) {
		t.Parallel()
		cfg := projectsTestConfig(t, true)
		cfg.Paths.Explicit = config.ExplicitPaths{
			LogDir:        true,
			ArtifactDir:   true,
			DAGRunsDir:    true,
			DAGRunWorkDir: true,
		}
		state := filepath.Join(cfg.Paths.DataDir, "projects", "weather")
		logs := filepath.Join(cfg.Paths.LogDir, ".projects", "weather")
		artifacts := filepath.Join(cfg.Paths.ArtifactDir, ".projects", "weather")
		runs := filepath.Join(cfg.Paths.DAGRunsDir, ".projects", "weather")
		work := filepath.Join(cfg.Paths.DAGRunWorkDir, ".projects", "weather")

		paths, err := cfg.ProjectPaths("weather")
		require.NoError(t, err)
		assert.Equal(t, logs, paths.LogDir)
		assert.Equal(t, artifacts, paths.ArtifactDir)
		assert.Equal(t, runs, paths.DAGRunsDir)
		assert.Equal(t, work, paths.DAGRunWorkDir)
		assert.Equal(t, []string{state, logs, artifacts, runs, work}, paths.RecordDirs)
	})

	// paths.event_store_dir only tells migration where v2 kept run events,
	// so each project keeps its own in its state directory.
	t.Run("OnEventStoreIgnoresPath", func(t *testing.T) {
		t.Parallel()
		cfg := projectsTestConfig(t, true)
		cfg.Paths.EventStoreDir = filepath.Join(t.TempDir(), "events")

		paths, err := cfg.ProjectPaths("weather")
		require.NoError(t, err)
		state := filepath.Join(cfg.Paths.DataDir, "projects", "weather")
		assert.Equal(t, filepath.Join(state, "state", "events"), paths.EventStoreDir)
		assert.Equal(t, []string{state}, paths.RecordDirs)
	})

	t.Run("OnSharedSetApartRoot", func(t *testing.T) {
		t.Parallel()
		cfg := projectsTestConfig(t, true)
		cfg.Paths.ArtifactDir = cfg.Paths.LogDir
		cfg.Paths.Explicit = config.ExplicitPaths{LogDir: true, ArtifactDir: true}

		paths, err := cfg.ProjectPaths("weather")
		require.NoError(t, err)
		assert.Equal(t, []string{
			filepath.Join(cfg.Paths.DataDir, "projects", "weather"),
			filepath.Join(cfg.Paths.LogDir, ".projects", "weather"),
		}, paths.RecordDirs)
	})

	t.Run("OnRefusesPathNames", func(t *testing.T) {
		t.Parallel()
		cfg := projectsTestConfig(t, true)
		for _, name := range []string{"", ".", "..", "a/b", `a\b`, "c:"} {
			_, err := cfg.ProjectPaths(name)
			assert.Error(t, err, "name %q", name)
		}
	})
}

func TestProjectBaseConfig(t *testing.T) {
	t.Parallel()

	for _, projectsOn := range []bool{false, true} {
		cfg := projectsTestConfig(t, projectsOn)
		cfg.Paths.BaseConfig = filepath.Join(filepath.Dir(cfg.Paths.DAGsDir), "base.yaml")
		for _, name := range []string{"", "default", "Default"} {
			base, err := cfg.ProjectBaseConfig(name)
			require.NoError(t, err)
			assert.Equal(t, cfg.Paths.BaseConfig, base, "name %q", name)
		}
		base, err := cfg.ProjectBaseConfig("weather")
		if !projectsOn {
			assert.Error(t, err)
			assert.Equal(t, filepath.Join(cfg.Paths.DAGsDir, "workspaces"), cfg.WorkspaceBaseConfigDir())
			continue
		}
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(cfg.Paths.ProjectsDir, "weather", "base.yaml"), base)
		assert.Empty(t, cfg.WorkspaceBaseConfigDir())
	}
}

func TestDiscoverySkipRoots(t *testing.T) {
	t.Parallel()

	t.Run("Off", func(t *testing.T) {
		t.Parallel()
		cfg := projectsTestConfig(t, false)
		cfg.Paths.AltDAGsDir = filepath.Join(cfg.Paths.DAGsDir, "library")
		assert.Empty(t, cfg.DiscoverySkipRoots())
	})

	t.Run("On", func(t *testing.T) {
		t.Parallel()
		cfg := projectsTestConfig(t, true)
		assert.Equal(t, []string{cfg.Paths.DAGsDir, cfg.Paths.ProjectsDir}, cfg.DiscoverySkipRoots())

		cfg.Paths.AltDAGsDir = filepath.Join(cfg.Paths.DAGsDir, "library")
		assert.Equal(t, []string{cfg.Paths.DAGsDir, cfg.Paths.ProjectsDir, cfg.Paths.AltDAGsDir}, cfg.DiscoverySkipRoots())
	})
}

func TestProjectWarnings(t *testing.T) {
	t.Parallel()

	v2Paths := config.ExplicitPaths{QueueDir: true, SuspendFlagsDir: true, ProcDir: true}

	t.Run("Off", func(t *testing.T) {
		t.Parallel()
		cfg := projectsTestConfig(t, false)
		cfg.Paths.Explicit = v2Paths
		assert.Empty(t, cfg.ProjectWarnings())
	})

	t.Run("SameFileSystem", func(t *testing.T) {
		t.Parallel()
		assert.Empty(t, projectsTestConfig(t, true).ProjectWarnings())
	})

	t.Run("V2StorePaths", func(t *testing.T) {
		t.Parallel()
		cfg := projectsTestConfig(t, true)
		cfg.Paths.Explicit = v2Paths

		warnings := cfg.ProjectWarnings()
		require.Len(t, warnings, 3)
		assert.Contains(t, warnings[0], "paths.queue_dir")
		assert.Contains(t, warnings[1], "paths.suspend_flags_dir")
		assert.Contains(t, warnings[2], "paths.proc_dir")
	})

	t.Run("DifferentFileSystems", func(t *testing.T) {
		t.Parallel()
		if runtime.GOOS == "windows" {
			t.Skip("no second volume to rely on")
		}
		cfg := projectsTestConfig(t, true)
		// /dev is its own file system on Linux and macOS.
		cfg.Paths.DAGsDir = "/dev"

		warnings := cfg.ProjectWarnings()
		require.Len(t, warnings, 1)
		assert.Contains(t, warnings[0], "different file systems")
	})
}
