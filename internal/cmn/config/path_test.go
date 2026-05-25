// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolver(t *testing.T) {
	t.Parallel()
	t.Run("AppHomeDirectory", func(t *testing.T) {
		t.Parallel()
		tmpDir := fileutil.MustTempDir("test")
		defer os.RemoveAll(tmpDir)

		_ = os.Setenv("TEST_APP_HOME", filepath.Join(tmpDir, config.AppSlug))
		paths, err := config.ResolvePaths("TEST_APP_HOME", filepath.Join(tmpDir, ".dagu"), config.XDGConfig{})
		require.NoError(t, err)

		assert.Equal(t, config.Paths{
			ConfigDir:       filepath.Join(tmpDir, config.AppSlug),
			DAGsDir:         filepath.Join(tmpDir, config.AppSlug, "dags"),
			SuspendFlagsDir: filepath.Join(tmpDir, config.AppSlug, "suspend"),
			DataDir:         filepath.Join(tmpDir, config.AppSlug, "data"),
			LogsDir:         filepath.Join(tmpDir, config.AppSlug, "logs"),
			ArtifactsDir:    filepath.Join(tmpDir, config.AppSlug, "data", "artifacts"),
			DAGStateDir:     filepath.Join(tmpDir, config.AppSlug, "data", "dag-state"),
			AdminLogsDir:    filepath.Join(tmpDir, config.AppSlug, "logs/admin"),
			EventStoreDir:   filepath.Join(tmpDir, config.AppSlug, "logs/admin/events"),
			BaseConfigFile:  filepath.Join(tmpDir, config.AppSlug, "base.yaml"),
		}, paths)
	})
	t.Run("AppHomeDirectoryRelativePath", func(t *testing.T) {
		t.Parallel()

		wd, err := os.Getwd()
		require.NoError(t, err)

		// Set a relative path
		_ = os.Setenv("TEST_APP_HOME_REL", "tmp")
		defer os.Unsetenv("TEST_APP_HOME_REL")
		paths, err := config.ResolvePaths("TEST_APP_HOME_REL", "", config.XDGConfig{})
		require.NoError(t, err)

		// Should be converted to absolute path
		expectedAbsPath := filepath.Join(wd, "tmp")
		assert.Equal(t, expectedAbsPath, paths.ConfigDir)
	})
	t.Run("UnifiedHomeDirectory", func(t *testing.T) {
		t.Parallel()
		tmpDir := fileutil.MustTempDir("test")
		defer os.RemoveAll(tmpDir)

		legacyPath := filepath.Join(tmpDir, "."+config.AppSlug)
		err := os.MkdirAll(legacyPath, os.ModePerm)
		require.NoError(t, err)

		paths, err := config.ResolvePaths("UNSET_APP_HOME", legacyPath, config.XDGConfig{})
		require.NoError(t, err)

		assert.Equal(t, config.Paths{
			ConfigDir:       legacyPath,
			DAGsDir:         filepath.Join(legacyPath, "dags"),
			SuspendFlagsDir: filepath.Join(legacyPath, "suspend"),
			DataDir:         filepath.Join(legacyPath, "data"),
			LogsDir:         filepath.Join(legacyPath, "logs"),
			ArtifactsDir:    filepath.Join(legacyPath, "data", "artifacts"),
			DAGStateDir:     filepath.Join(legacyPath, "data", "dag-state"),
			AdminLogsDir:    filepath.Join(legacyPath, "logs", "admin"),
			EventStoreDir:   filepath.Join(legacyPath, "logs", "admin", "events"),
			BaseConfigFile:  filepath.Join(legacyPath, "base.yaml"),
			Notices:         []string{config.ExistingHomeDirNoticePrefix + legacyPath + "."},
		}, paths)
	})
	t.Run("XDGCONFIGHOME", func(t *testing.T) {
		t.Parallel()
		// Use temp directories for cross-platform compatibility
		tmpDir := fileutil.MustTempDir("test")
		defer os.RemoveAll(tmpDir)

		dataHome := filepath.Join(tmpDir, "share")
		configHome := filepath.Join(tmpDir, "config")

		paths, err := config.ResolvePaths("UNSET_APP_HOME", ".test", config.XDGConfig{
			DataHome:   dataHome,
			ConfigHome: configHome,
		})
		require.NoError(t, err)
		assert.Equal(t, config.Paths{
			ConfigDir:       filepath.Join(configHome, config.AppSlug),
			DAGsDir:         filepath.Join(configHome, config.AppSlug, "dags"),
			SuspendFlagsDir: filepath.Join(dataHome, config.AppSlug, "suspend"),
			DataDir:         filepath.Join(dataHome, config.AppSlug, "data"),
			LogsDir:         filepath.Join(dataHome, config.AppSlug, "logs"),
			ArtifactsDir:    filepath.Join(dataHome, config.AppSlug, "data", "artifacts"),
			DAGStateDir:     filepath.Join(dataHome, config.AppSlug, "data", "dag-state"),
			AdminLogsDir:    filepath.Join(dataHome, config.AppSlug, "logs", "admin"),
			EventStoreDir:   filepath.Join(dataHome, config.AppSlug, "logs", "admin", "events"),
			BaseConfigFile:  filepath.Join(configHome, config.AppSlug, "base.yaml"),
		}, paths)
	})
}
