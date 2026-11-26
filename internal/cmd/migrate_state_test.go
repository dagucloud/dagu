// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Before migration, a v2 run on a worker gets a cancel request that the
// coordinator acts on when the worker next connects. It has no local
// process, so migration does not wait for it.
func TestV2RunStopperCancelsRunsOnWorkers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	data := filepath.Join(t.TempDir(), "data")
	cfg := &config.Config{ProjectsEnabled: true}
	cfg.Paths = config.PathsConfig{
		DataDir:     data,
		DAGRunsDir:  filepath.Join(data, "dag-runs"),
		ProcDir:     filepath.Join(data, "proc"),
		LogDir:      filepath.Join(data, "logs"),
		ArtifactDir: filepath.Join(data, "artifacts"),
	}
	dag := &ir.DAG{Name: "etl", Steps: []ir.Step{{Name: "step", Command: "echo hi"}}}
	attempt, err := file.NewDAGRunRepository(cfg).CreateAttempt(ctx, dag, time.Now(), "run-1", persis.DAGRunCreateAttemptOptions{})
	require.NoError(t, err)
	require.NoError(t, attempt.Open(ctx))
	require.NoError(t, attempt.Write(ctx, ir.DAGRunStatus{Name: "etl", DAGRunID: "run-1", AttemptID: attempt.ID(), Status: ir.Running}))
	require.NoError(t, attempt.Close(ctx))
	require.NoError(t, file.NewServerStores(cfg).ActiveDistributedRunStore.Upsert(ctx, dispatch.ActiveDistributedRun{
		AttemptKey: "etl:run-1:" + attempt.ID(),
		DAGRun:     ir.NewDAGRunRef("etl", "run-1"),
		AttemptID:  attempt.ID(),
		WorkerID:   "worker-1",
		Status:     ir.Running,
		UpdatedAt:  time.Now().Unix(),
	}))

	live, err := newV2RunStopper(cfg).stop(ctx)

	require.NoError(t, err)
	assert.Empty(t, live)
	aborting, err := attempt.IsAborting(ctx)
	require.NoError(t, err)
	assert.True(t, aborting)
}
