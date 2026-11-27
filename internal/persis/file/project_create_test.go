// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file_test

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	persisfile "github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/persis/file/projectrecord"
	"github.com/dagucloud/dagu/v2/internal/persis/file/projectregistry"
	"github.com/dagucloud/dagu/v2/internal/project"
)

// TestCreateProject proves Spec 102 "Create": a project gets its DAG
// directory and a record of its ID in its state directory, and each refusal
// leaves the registry as it was.
func TestCreateProject(t *testing.T) {
	ctx := context.Background()
	cfg := projectStoresConfig(t, true)
	registry := projectregistry.New(cfg.Paths.DataDir)
	create := func(name string) (project.Project, error) {
		return persisfile.CreateProject(ctx, cfg, registry, project.CreateInput{Name: name})
	}

	created, err := create("weather")
	require.NoError(t, err)
	assert.DirExists(t, filepath.Join(cfg.Paths.ProjectsDir, "weather"))
	record, err := projectrecord.Read(filepath.Join(cfg.Paths.DataDir, "projects", "weather"))
	require.NoError(t, err)
	assert.Equal(t, projectrecord.Record{ID: created.ID, Name: "weather"}, record)

	_, err = create("Weather")
	require.ErrorIs(t, err, project.ErrNameTaken)
	_, err = create("default")
	require.ErrorIs(t, err, project.ErrInvalidName)

	require.NoError(t, projectrecord.Write(filepath.Join(cfg.Paths.DataDir, "projects", "intraday"),
		projectrecord.Record{ID: "an-older-id", Name: "intraday"}))
	_, err = create("intraday")
	require.ErrorIs(t, err, persisfile.ErrStateDirTaken)
	assert.NoDirExists(t, filepath.Join(cfg.Paths.ProjectsDir, "intraday"))

	cfg.Paths.CalendarsDir = filepath.Join(cfg.Paths.ProjectsDir, "calendars")
	_, err = create("calendars")
	require.ErrorContains(t, err, "paths.calendars_dir")

	projects, err := registry.List(ctx)
	require.NoError(t, err)
	require.Len(t, projects, 1, "only the first create registered a project")
}

// A create that cannot record the project in a later state directory removes
// the records it wrote, so a retry succeeds once the cause is gone.
func TestCreateProjectRollsBackRecords(t *testing.T) {
	ctx := context.Background()
	cfg := projectStoresConfig(t, true)
	cfg.Paths.Explicit.LogDir = true
	registry := projectregistry.New(cfg.Paths.DataDir)
	blocker := filepath.Join(cfg.Paths.LogDir, ".projects")
	require.NoError(t, os.MkdirAll(cfg.Paths.LogDir, 0o750))
	require.NoError(t, os.WriteFile(blocker, nil, 0o600))

	_, err := persisfile.CreateProject(ctx, cfg, registry, project.CreateInput{Name: "ops"})
	require.Error(t, err)
	_, err = projectrecord.Read(filepath.Join(cfg.Paths.DataDir, "projects", "ops"))
	require.ErrorIs(t, err, fs.ErrNotExist)
	projects, err := registry.List(ctx)
	require.NoError(t, err)
	require.Empty(t, projects)

	require.NoError(t, os.Remove(blocker))
	_, err = persisfile.CreateProject(ctx, cfg, registry, project.CreateInput{Name: "ops"})
	require.NoError(t, err)
}
