// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package config_test

import (
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckProjectDAGsDir(t *testing.T) {
	t.Parallel()

	projects := filepath.Join(t.TempDir(), "projects")
	cfg := &config.Config{Paths: config.PathsConfig{
		ProjectsDir:    projects,
		DAGsDir:        filepath.Join(projects, "dags"),
		AltDAGsDir:     filepath.Join(projects, "shared", "library"),
		ConfigFileUsed: filepath.Join(projects, "etc", "config.yaml"),
	}}

	require.NoError(t, cfg.CheckProjectDAGsDir(filepath.Join(projects, "weather")))
	for dir, protected := range map[string]string{
		"dags":   "paths.dags_dir",
		"shared": "paths.alt_dags_dir",
		"etc":    "config.yaml",
	} {
		err := cfg.CheckProjectDAGsDir(filepath.Join(projects, dir))
		require.Error(t, err, dir)
		assert.Contains(t, err.Error(), protected, dir)
	}
}
