// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	persisfile "github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/project"
	"github.com/dagucloud/dagu/v2/internal/queue"
)

// libraryConfig is projectStoresConfig with a library holding etl.yaml.
func libraryConfig(t *testing.T, projectsOn bool) (*config.Config, string) {
	t.Helper()
	cfg := projectStoresConfig(t, projectsOn)
	cfg.Paths.AltDAGsDir = filepath.Join(filepath.Dir(cfg.Paths.DAGsDir), "library")
	return cfg, writeDAGFile(t, filepath.Join(cfg.Paths.AltDAGsDir, sampleDAG+".yaml"))
}

// With projects on, a project records the runs, queue items, and DAG state
// of the library DAG etl in its library, so its own DAG etl reads none of
// them. Each project's library is in its state directory.
func TestLibraryStoresKeepRunsApart(t *testing.T) {
	ctx := context.Background()
	cfg, libraryDAG := libraryConfig(t, true)
	server := persisfile.NewServerStores(cfg)

	for _, name := range []string{project.DefaultName, "weather"} {
		t.Run(name, func(t *testing.T) {
			stores := openProject(t, cfg, server, name)
			library, err := stores.RunStores(ctx, libraryDAG)
			require.NoError(t, err)
			require.True(t, library.InLibrary())
			runs, procs := stores.LibraryRuns()
			require.Same(t, library.DAGRunRepository, runs)
			require.Same(t, library.ProcRepository, procs)
			ownDAG := filepath.Join(stores.Paths.DAGsDir, sampleDAG+".yaml")
			own, err := stores.RunStores(ctx, ownDAG)
			require.NoError(t, err)
			require.Same(t, stores, own)
			// A library run that enqueues a project DAG records it in the
			// project.
			own, err = library.RunStores(ctx, ownDAG)
			require.NoError(t, err)
			require.Same(t, stores, own)
			assert.True(t, library.InLibrary())
			assert.False(t, stores.InLibrary())

			writeRunState(t, library, "library")
			writeRunState(t, stores, "project")
			assertRunState(t, readersOf(ctx, library), "library")
			assertRunState(t, readersOf(ctx, stores), "project")

			state := filepath.Join(cfg.Paths.DataDir, "projects", name, "library")
			assert.Equal(t, state, library.Paths.StateDir)
			assert.Equal(t, filepath.Join(state, "logs"), library.Paths.LogDir)
			for _, dir := range []string{"runs", "state/dag-state", "state/queue"} {
				assert.DirExists(t, filepath.Join(state, filepath.FromSlash(dir)))
			}
		})
	}
}

// With a path set apart, a project's library keeps its runs and logs under
// that path in .projects/<name>.library/.
func TestLibraryStoresSetApart(t *testing.T) {
	ctx := context.Background()
	cfg, libraryDAG := libraryConfig(t, true)
	cfg.Paths.Explicit = config.ExplicitPaths{LogDir: true, DAGRunsDir: true}
	stores := openProject(t, cfg, persisfile.NewServerStores(cfg), "weather")
	library, err := stores.RunStores(ctx, libraryDAG)
	require.NoError(t, err)

	writeRun(t, library.DAGRunRepository, ir.DAGRunStatus{Params: "library"})
	assert.DirExists(t, filepath.Join(cfg.Paths.DAGRunsDir, ".projects", "weather.library", sampleDAG))
	assert.Equal(t, filepath.Join(cfg.Paths.LogDir, ".projects", "weather.library"), library.Paths.LogDir)
}

// A step's DAG state with scope dag is the library's when the step's DAG is
// a library DAG, and with scope root_dag when the root run's DAG is. Global
// state is the project's whichever DAG writes it.
func TestLibraryStoresStateFor(t *testing.T) {
	ctx := context.Background()
	cfg, libraryDAG := libraryConfig(t, true)
	stores := openProject(t, cfg, persisfile.NewServerStores(cfg), "weather")
	projectDAG := writeDAGFile(t, filepath.Join(stores.Paths.DAGsDir, sampleDAG+".yaml"))
	library, err := stores.RunStores(ctx, libraryDAG)
	require.NoError(t, err)

	for _, tc := range []struct {
		name         string
		root         *persisfile.ProjectStores
		path         string
		dagInLib     bool
		rootDAGInLib bool
	}{
		{name: "ProjectCallsLibrary", root: stores, path: libraryDAG, dagInLib: true},
		{name: "LibraryCallsProject", root: library, path: projectDAG, rootDAGInLib: true},
		{name: "LibraryRoot", root: library, path: libraryDAG, dagInLib: true, rootDAGInLib: true},
		{name: "ProjectRoot", root: stores, path: projectDAG},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state, err := tc.root.StateStoreFor(ctx, tc.path)
			require.NoError(t, err)
			for scope, inLibrary := range map[dagrun.StateScope]bool{
				dagrun.StateScopeDAG:     tc.dagInLib,
				dagrun.StateScopeRootDAG: tc.rootDAGInLib,
				dagrun.StateScopeGlobal:  false,
			} {
				ref := dagrun.StateRef{Scope: scope, Namespace: sampleDAG, Key: tc.name}
				_, err := state.Put(ctx, ref, json.RawMessage(`1`), dagrun.StatePutOptions{})
				require.NoError(t, err)

				_, err = stores.StateStore.Get(ctx, ref)
				if inLibrary {
					assert.ErrorIs(t, err, dagrun.ErrStateNotFound, "scope %s", scope)
				} else {
					assert.NoError(t, err, "scope %s", scope)
				}
			}
		})
	}
}

// With projects off, a library DAG's run is the default project's, as on
// v2, and nothing is written for a library.
func TestLibraryStoresOff(t *testing.T) {
	ctx := context.Background()
	cfg, libraryDAG := libraryConfig(t, false)
	stores, err := persisfile.NewProjectStores(ctx, cfg, persisfile.NewServerStores(cfg),
		project.Project{Name: project.DefaultName}, persisfile.ProjectStoreOptions{})
	require.NoError(t, err)
	libraryRuns, libraryProcs := stores.LibraryRuns()
	require.Nil(t, libraryRuns)
	require.Nil(t, libraryProcs)

	runs, err := stores.RunStores(ctx, libraryDAG)
	require.NoError(t, err)
	assert.Same(t, stores, runs)
	state, err := stores.StateStoreFor(ctx, libraryDAG)
	require.NoError(t, err)
	assert.Equal(t, stores.StateStore, state)
	writeRunState(t, runs, "v2")
	assertRunState(t, readersOf(ctx, stores), "v2")
	assert.NoDirExists(t, filepath.Join(cfg.Paths.DataDir, "projects"))
}

// writeRunState writes a run, a queue item, and a DAG state entry with
// scope dag of the DAG etl, each carrying value.
func writeRunState(t *testing.T, stores *persisfile.ProjectStores, value string) {
	t.Helper()
	ctx := context.Background()
	writeRun(t, stores.DAGRunRepository, ir.DAGRunStatus{Params: value})
	require.NoError(t, stores.QueueStore.Enqueue(ctx, sampleDAG, queue.QueuePriorityLow, ir.NewDAGRunRef(sampleDAG, value)))
	raw, err := json.Marshal(value)
	require.NoError(t, err)
	_, err = stores.StateStore.Put(ctx, dagScopeState, raw, dagrun.StatePutOptions{})
	require.NoError(t, err)
}

var dagScopeState = dagrun.StateRef{Scope: dagrun.StateScopeDAG, Namespace: sampleDAG, Key: "cursor"}

// assertRunState fails unless what writeRunState wrote with value is all
// that readers read.
func assertRunState(t *testing.T, readers sampleReaders, value string) {
	t.Helper()
	ctx := context.Background()
	assert.Equal(t, value, readRun(t, readers.runs).Params)
	items, err := readers.queue.List(ctx, sampleDAG)
	require.NoError(t, err)
	require.Len(t, items, 1)
	ref, err := items[0].Data()
	require.NoError(t, err)
	assert.Equal(t, value, ref.ID)
	entry, err := readers.state.Get(ctx, dagScopeState)
	require.NoError(t, err)
	assert.JSONEq(t, `"`+value+`"`, string(entry.Value))
}
