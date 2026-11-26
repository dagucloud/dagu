// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/persis"
	persisfile "github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/persis/file/projectregistry"
	"github.com/dagucloud/dagu/v2/internal/project"
)

const libraryTestDAG = "steps:\n  - run: echo ok\n"

func writeDAGFile(t *testing.T, path string) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(libraryTestDAG), 0o600))
	return path
}

// openProject registers the project called name, unless it is the default
// project, and opens its stores.
func openProject(t *testing.T, cfg *config.Config, server *persisfile.ServerStores, name string) *persisfile.ProjectStores {
	t.Helper()
	ctx := context.Background()
	if name != project.DefaultName {
		_, err := projectregistry.New(cfg.Paths.DataDir).Create(ctx, project.CreateInput{Name: name})
		require.NoError(t, err)
	}
	stores, err := persisfile.NewProjectStores(ctx, cfg, server, project.Project{Name: name}, persisfile.ProjectStoreOptions{})
	require.NoError(t, err)
	return stores
}

// With projects on, each project's DAG repository finds the library DAGs
// it lacks, and an explicit start of a library DAG is refused only in a
// project that has a DAG of the same name.
func TestProjectStoresLibraryDAGs(t *testing.T) {
	cfg := projectStoresConfig(t, true)
	cfg.Paths.AltDAGsDir = filepath.Join(filepath.Dir(cfg.Paths.DAGsDir), "library")
	server := persisfile.NewServerStores(cfg)
	shared := writeDAGFile(t, filepath.Join(cfg.Paths.AltDAGsDir, "ABC.yaml"))
	cleanup := writeDAGFile(t, filepath.Join(cfg.Paths.AltDAGsDir, "cleanup.yaml"))
	weather := openProject(t, cfg, server, "weather")
	writeDAGFile(t, filepath.Join(weather.Paths.DAGsDir, "ABC.yaml"))
	defaultProject := openProject(t, cfg, server, project.DefaultName)

	dag, err := weather.DAGRepository.GetMetadata(context.Background(), "cleanup")
	require.NoError(t, err)
	want, err := filepath.EvalSymlinks(cleanup)
	require.NoError(t, err)
	assert.Equal(t, want, dag.Location)

	err = weather.CheckStart(context.Background(), shared)
	assert.EqualError(t, err, `library DAG `+shared+` cannot start in project "weather", which has a DAG named ABC`)
	assert.NoError(t, weather.CheckStart(context.Background(), cleanup))
	assert.NoError(t, defaultProject.CheckStart(context.Background(), shared))

	// A shadowing DAG that does not load still refuses the start, and the
	// error says why.
	require.NoError(t, os.WriteFile(filepath.Join(weather.Paths.DAGsDir, "cleanup.yaml"), []byte("steps: [\n"), 0o600))
	err = weather.CheckStart(context.Background(), cleanup)
	assert.ErrorContains(t, err, `library DAG `+cleanup+` cannot start in project "weather": read its DAG named cleanup in `+weather.Paths.DAGsDir)
}

// A file symlink out of the DAG directory loads as before while projects
// are off, and never while they are on.
func TestProjectStoresExternalSymlinks(t *testing.T) {
	for _, projectsOn := range []bool{false, true} {
		cfg := projectStoresConfig(t, projectsOn)
		cfg.DAGDiscovery.Symlinks = true
		target := writeDAGFile(t, filepath.Join(t.TempDir(), "outside.yaml"))
		require.NoError(t, os.MkdirAll(cfg.Paths.DAGsDir, 0o750))
		if err := os.Symlink(target, filepath.Join(cfg.Paths.DAGsDir, "linked.yaml")); err != nil {
			t.Skipf("symlinks are unavailable: %v", err)
		}
		stores, err := persisfile.NewProjectStores(context.Background(), cfg, persisfile.NewServerStores(cfg),
			project.Project{Name: project.DefaultName}, persisfile.ProjectStoreOptions{})
		require.NoError(t, err)

		_, err = stores.DAGRepository.GetMetadata(context.Background(), "linked")
		if projectsOn {
			assert.ErrorIs(t, err, persis.ErrDAGNotFound)
			continue
		}
		assert.NoError(t, err)
		assert.NoError(t, stores.CheckStart(context.Background(), target))
	}
}

// With projects on, a DAG's base config is its project's alone: base.yaml in
// the DAG directory of a project other than the default one, which is no
// DAG, and paths.base_config for the default project, which a workspace
// label adds no layer to. With projects off, the label's workspace base
// config layers over paths.base_config as on v2.
func TestProjectStoresBaseConfig(t *testing.T) {
	for _, projectsOn := range []bool{true, false} {
		cfg := projectStoresConfig(t, projectsOn)
		cfg.Paths.BaseConfig = filepath.Join(filepath.Dir(cfg.Paths.DAGsDir), "base.yaml")
		writeBaseConfig(t, cfg.Paths.BaseConfig, "default")
		writeBaseConfig(t, filepath.Join(cfg.Paths.DAGsDir, "workspaces", "ops", "base.yaml"), "ops")
		require.NoError(t, os.WriteFile(filepath.Join(cfg.Paths.DAGsDir, "labelled.yaml"),
			[]byte("labels: [workspace=ops]\n"+libraryTestDAG), 0o600))
		server := persisfile.NewServerStores(cfg)

		labelled, err := openProject(t, cfg, server, project.DefaultName).DAGRepository.GetMetadata(context.Background(), "labelled")
		require.NoError(t, err)
		assert.Contains(t, labelled.Env, "FROM_DEFAULT=yes")
		if !projectsOn {
			assert.Contains(t, labelled.Env, "FROM_OPS=yes")
			continue
		}
		assert.NotContains(t, labelled.Env, "FROM_OPS=yes")

		weather := openProject(t, cfg, server, "weather")
		writeBaseConfig(t, filepath.Join(weather.Paths.DAGsDir, "base.yaml"), "weather")
		writeDAGFile(t, filepath.Join(weather.Paths.DAGsDir, "forecast.yaml"))
		forecast, err := weather.DAGRepository.GetMetadata(context.Background(), "forecast")
		require.NoError(t, err)
		assert.Equal(t, []string{"FROM_WEATHER=yes"}, forecast.Env)
		for _, target := range []string{"base", filepath.Join(weather.Paths.DAGsDir, "base.yaml")} {
			_, err = weather.DAGRepository.GetMetadata(context.Background(), target)
			assert.ErrorIs(t, err, persis.ErrDAGNotFound, target)
		}
		listed, _, err := weather.DAGRepository.List(context.Background(), persis.DAGListOptions{})
		require.NoError(t, err)
		require.Len(t, listed.Items, 1)
		assert.Equal(t, "forecast", listed.Items[0].ID)

		// A project without base.yaml has no base config.
		dayAhead := openProject(t, cfg, server, "day-ahead")
		writeDAGFile(t, filepath.Join(dayAhead.Paths.DAGsDir, "auction.yaml"))
		auction, err := dayAhead.DAGRepository.GetMetadata(context.Background(), "auction")
		require.NoError(t, err)
		assert.Empty(t, auction.BaseConfigData)
	}
}

// writeBaseConfig writes a base config that sets FROM_<SOURCE> to yes.
func writeBaseConfig(t *testing.T, path, source string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte("env:\n  - FROM_"+strings.ToUpper(source)+": \"yes\"\n"), 0o600))
}
