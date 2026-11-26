// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDAGPathOwner(t *testing.T) {
	t.Parallel()

	// The projects and library directories nest in the DAGs directory, so
	// the innermost DAG root decides.
	root := t.TempDir()
	dags := filepath.Join(root, "dags")
	cfg := &config.Config{
		ProjectsEnabled: true,
		Paths: config.PathsConfig{
			DAGsDir:     dags,
			ProjectsDir: filepath.Join(dags, "projects"),
			AltDAGsDir:  filepath.Join(dags, "library"),
		},
	}
	for _, dir := range []string{"projects/intraday/etl", "projects/stray", "library", "ops"} {
		require.NoError(t, os.MkdirAll(filepath.Join(dags, dir), 0o750))
	}
	names := []string{"default", "intraday", "day-ahead"}

	tests := []struct {
		path    string
		owner   string
		library bool
	}{
		{path: dags, owner: "default"},
		{path: filepath.Join(dags, "ops", "cleanup.yaml"), owner: "default"},
		{path: filepath.Join(dags, "projects", "intraday"), owner: "intraday"},
		{path: filepath.Join(dags, "projects", "intraday", "etl", "forecast.yaml"), owner: "intraday"},
		{path: filepath.Join(dags, "projects", "day-ahead", "auction.yaml"), owner: "day-ahead"},
		{path: filepath.Join(dags, "projects", "stray", "x.yaml")},
		{path: filepath.Join(dags, "projects")},
		{path: filepath.Join(dags, "library", "abc.yaml"), library: true},
		{path: filepath.Join(root, "elsewhere.yaml")},
	}
	for _, tt := range tests {
		owner, library := cfg.DAGPathOwner(tt.path, names)
		assert.Equal(t, tt.owner, owner, tt.path)
		assert.Equal(t, tt.library, library, tt.path)
	}
}

func TestDAGPathOwnerFollowsLinks(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	projects := filepath.Join(root, "projects")
	require.NoError(t, os.MkdirAll(filepath.Join(projects, "intraday"), 0o750))
	link := filepath.Join(root, "link")
	if err := os.Symlink(filepath.Join(projects, "intraday"), link); err != nil {
		t.Skipf("symbolic links unavailable: %v", err)
	}
	cfg := &config.Config{
		ProjectsEnabled: true,
		Paths:           config.PathsConfig{DAGsDir: filepath.Join(root, "dags"), ProjectsDir: projects},
	}

	owner, _ := cfg.DAGPathOwner(filepath.Join(link, "forecast.yaml"), []string{"intraday"})
	assert.Equal(t, "intraday", owner)
}
