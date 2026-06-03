// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package engine_test

import (
	"context"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/config"
	coreexec "github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/engine"
	"github.com/dagucloud/dagu/internal/persis/file"
	"github.com/dagucloud/dagu/internal/persis/store"
	"github.com/dagucloud/dagu/internal/persis/testutil"
	"github.com/dagucloud/dagu/internal/runtime/runstate/memstore"
	"github.com/stretchr/testify/require"
)

func TestRunYAMLUsesRunStateStoreWithoutDAGRunStore(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	runStateStore := memstore.New()
	eng, err := engine.New(ctx, engine.Options{
		HomeDir:            t.TempDir(),
		PersistenceFactory: memoryPersistenceFactory(runStateStore),
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, eng.Close(context.Background()))
	})

	run, err := eng.RunYAML(ctx, []byte(`
name: embedded-memory-state
steps:
  - name: produce
    command: echo memory-state
    output: RESULT
`), engine.RunOptions{RunID: "memory-run"})
	require.NoError(t, err)

	status, err := run.Wait(ctx)
	require.NoError(t, err)
	require.Equal(t, "succeeded", status.Status)
	require.Equal(t, "embedded-memory-state", status.Name)

	outputs, err := run.Outputs(ctx)
	require.NoError(t, err)
	require.Equal(t, map[string]string{"result": "memory-state"}, outputs)

	opened, err := runStateStore.OpenAttempt(ctx, coreexec.NewDAGRunRef("embedded-memory-state", "memory-run"))
	require.NoError(t, err)
	persisted, err := opened.ReadStatus(ctx)
	require.NoError(t, err)
	require.Equal(t, "succeeded", persisted.Status.String())
}

func TestRunYAMLUsesRunStateStoreWhenDAGRunStoreAlsoConfigured(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	runStateStore := memstore.New()
	eng, err := engine.New(ctx, engine.Options{
		HomeDir:            t.TempDir(),
		PersistenceFactory: hybridPersistenceFactory(runStateStore),
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, eng.Close(context.Background()))
	})

	run, err := eng.RunYAML(ctx, []byte(`
name: embedded-hybrid-state
steps:
  - name: produce
    command: echo hybrid-state
    output: RESULT
`), engine.RunOptions{RunID: "hybrid-run"})
	require.NoError(t, err)

	status, err := run.Wait(ctx)
	require.NoError(t, err)
	require.Equal(t, "succeeded", status.Status)
	require.Equal(t, "embedded-hybrid-state", status.Name)

	outputs, err := run.Outputs(ctx)
	require.NoError(t, err)
	require.Equal(t, map[string]string{"result": "hybrid-state"}, outputs)
}

func TestStatusAndOutputsFallBackToDAGRunStoreWhenRunStateMissing(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	homeDir := t.TempDir()
	historyEngine, err := engine.New(ctx, engine.Options{
		HomeDir:            homeDir,
		PersistenceFactory: dagRunPersistenceFactory(),
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, historyEngine.Close(context.Background()))
	})

	run, err := historyEngine.RunYAML(ctx, []byte(`
name: embedded-history-state
steps:
  - name: produce
    command: echo history-state
    output: RESULT
`), engine.RunOptions{RunID: "history-run"})
	require.NoError(t, err)

	status, err := run.Wait(ctx)
	require.NoError(t, err)
	require.Equal(t, "succeeded", status.Status)
	require.Equal(t, "embedded-history-state", status.Name)

	ref := run.Ref()
	runStateStore := memstore.New()
	hybridEngine, err := engine.New(ctx, engine.Options{
		HomeDir:            homeDir,
		PersistenceFactory: hybridPersistenceFactory(runStateStore),
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, hybridEngine.Close(context.Background()))
	})

	_, err = runStateStore.OpenAttempt(ctx, coreexec.NewDAGRunRef("embedded-history-state", "history-run"))
	require.ErrorIs(t, err, coreexec.ErrDAGRunIDNotFound)

	status, err = hybridEngine.Status(ctx, ref)
	require.NoError(t, err)
	require.Equal(t, "succeeded", status.Status)
	require.Equal(t, "embedded-history-state", status.Name)

	outputs, err := hybridEngine.Outputs(ctx, ref)
	require.NoError(t, err)
	require.Equal(t, map[string]string{"result": "history-state"}, outputs)

	require.NoError(t, hybridEngine.Stop(ctx, ref))
}

func TestRunYAMLRejectsDuplicateRunIDWithoutDAGRunStore(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	runStateStore := memstore.New()
	eng, err := engine.New(ctx, engine.Options{
		HomeDir:            t.TempDir(),
		PersistenceFactory: memoryPersistenceFactory(runStateStore),
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, eng.Close(context.Background()))
	})

	dagYAML := []byte(`
name: embedded-duplicate-state
steps:
  - name: done
    command: echo done
`)
	run, err := eng.RunYAML(ctx, dagYAML, engine.RunOptions{RunID: "duplicate-run"})
	require.NoError(t, err)
	_, err = run.Wait(ctx)
	require.NoError(t, err)

	secondRun, err := eng.RunYAML(ctx, dagYAML, engine.RunOptions{RunID: "duplicate-run"})
	require.ErrorIs(t, err, coreexec.ErrDAGRunAlreadyExists)
	require.Nil(t, secondRun)
}

func memoryPersistenceFactory(runStateStore *memstore.Store) engine.PersistenceFactory {
	return func(_ context.Context, cfg *config.Config) (engine.Persistence, error) {
		backend := testutil.NewMemoryBackend()
		dagStore, err := file.NewDAGStore(cfg, file.WithDAGSkipExamples(true))
		if err != nil {
			return engine.Persistence{}, err
		}
		persistence := engine.Persistence{
			DAGStore:        dagStore,
			ProcStore:       store.NewProcStore(backend.Collection("proc")),
			StateStore:      store.NewDAGStateStore(backend.Collection("dag_state")),
			ServiceRegistry: file.NewServiceRegistry(cfg),
			DAGStoreFactory: func(_ context.Context, cfg *config.Config, opts engine.DAGStoreFactoryOptions) (coreexec.DAGStore, error) {
				fileOpts := []file.DAGStoreOption{file.WithDAGSkipExamples(true)}
				if len(opts.SearchPaths) > 0 {
					fileOpts = append(fileOpts, file.WithDAGSearchPaths(opts.SearchPaths))
				}
				return file.NewDAGStore(cfg, fileOpts...)
			},
		}
		if runStateStore != nil {
			persistence.RunStateStore = runStateStore
		}
		return persistence, nil
	}
}

func dagRunPersistenceFactory() engine.PersistenceFactory {
	return func(ctx context.Context, cfg *config.Config) (engine.Persistence, error) {
		persistence, err := memoryPersistenceFactory(nil)(ctx, cfg)
		if err != nil {
			return engine.Persistence{}, err
		}
		persistence.DAGRunStore = file.NewDAGRunStore(cfg, file.WithDAGRunLatestStatusToday(false))
		return persistence, nil
	}
}

func hybridPersistenceFactory(runStateStore *memstore.Store) engine.PersistenceFactory {
	return func(ctx context.Context, cfg *config.Config) (engine.Persistence, error) {
		persistence, err := memoryPersistenceFactory(runStateStore)(ctx, cfg)
		if err != nil {
			return engine.Persistence{}, err
		}
		persistence.DAGRunStore = file.NewDAGRunStore(cfg, file.WithDAGRunLatestStatusToday(false))
		return persistence, nil
	}
}
