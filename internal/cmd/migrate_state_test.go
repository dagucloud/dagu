// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/persis/file/migration"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Before migration, a v2 run on a worker gets a cancel request that the
// coordinator acts on when the worker next connects. It has no local
// process, so migration does not wait for it.
func TestV2RunStopperCancelsRunsOnWorkers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cfg := v2StopperConfig(t)
	attempt := writeRunningV2Run(t, cfg)
	addRunOnWorker(t, cfg)

	live, err := newV2RunStopper(cfg).stop(ctx)

	require.NoError(t, err)
	assert.Empty(t, live)
	requireAborting(t, attempt)
}

// A stop request that fails, here because the run's record is not readable
// yet, is made again on the next call.
func TestV2RunStopperRetriesFailedStop(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cfg := v2StopperConfig(t)
	addRunOnWorker(t, cfg)
	stopper := newV2RunStopper(cfg)

	_, err := stopper.stop(ctx)
	require.NoError(t, err)
	attempt := writeRunningV2Run(t, cfg)
	_, err = stopper.stop(ctx)

	require.NoError(t, err)
	requireAborting(t, attempt)
}

// A process with projects off warns on a data directory that migration
// moved to projects, and stays quiet on one it did not.
func TestWarnMigratedWithProjectsOff(t *testing.T) {
	t.Parallel()
	cfg := v2StopperConfig(t)
	var out bytes.Buffer
	ctx := logger.WithFixedLogger(context.Background(), logger.NewLogger(logger.WithWriter(&out)))

	warnMigrated(ctx, cfg)
	assert.Empty(t, out.String())

	require.NoError(t, migration.New(cfg, file.NewServiceRegistry(cfg), noLiveRuns).Run(ctx))
	cfg.ProjectsEnabled = false
	warnMigrated(ctx, cfg)
	assert.Contains(t, out.String(), "migrated to projects")
}

// noLiveRuns stops no run: the test's data directory has none.
func noLiveRuns(context.Context) ([]string, error) { return nil, nil }

func v2StopperConfig(t *testing.T) *config.Config {
	t.Helper()
	data := filepath.Join(t.TempDir(), "data")
	cfg := &config.Config{ProjectsEnabled: true}
	cfg.Paths = config.PathsConfig{
		DataDir:     data,
		DAGRunsDir:  filepath.Join(data, "dag-runs"),
		ProcDir:     filepath.Join(data, "proc"),
		LogDir:      filepath.Join(data, "logs"),
		ArtifactDir: filepath.Join(data, "artifacts"),
	}
	return cfg
}

// writeRunningV2Run records the run etl:run-1 as running in v2's
// directories.
func writeRunningV2Run(t *testing.T, cfg *config.Config) dagrun.Attempt {
	t.Helper()
	ctx := context.Background()
	dag := &ir.DAG{Name: "etl", Steps: []ir.Step{{Name: "step", Command: "echo hi"}}}
	attempt, err := file.NewDAGRunRepository(cfg).CreateAttempt(ctx, dag, time.Now(), "run-1", persis.DAGRunCreateAttemptOptions{})
	require.NoError(t, err)
	require.NoError(t, attempt.Open(ctx))
	require.NoError(t, attempt.Write(ctx, ir.DAGRunStatus{Name: "etl", DAGRunID: "run-1", AttemptID: attempt.ID(), Status: ir.Running}))
	require.NoError(t, attempt.Close(ctx))
	return attempt
}

// addRunOnWorker records etl:run-1 as running on a worker.
func addRunOnWorker(t *testing.T, cfg *config.Config) {
	t.Helper()
	require.NoError(t, file.NewServerStores(cfg).ActiveDistributedRunStore.Upsert(context.Background(), dispatch.ActiveDistributedRun{
		AttemptKey: "etl:run-1",
		DAGRun:     ir.NewDAGRunRef("etl", "run-1"),
		WorkerID:   "worker-1",
		Status:     ir.Running,
		UpdatedAt:  time.Now().Unix(),
	}))
}

func requireAborting(t *testing.T, attempt dagrun.Attempt) {
	t.Helper()
	aborting, err := attempt.IsAborting(context.Background())
	require.NoError(t, err)
	require.True(t, aborting, "the run has a cancel request")
}
