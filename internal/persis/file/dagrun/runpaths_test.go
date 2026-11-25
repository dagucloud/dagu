// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file/runpath"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A rebuild of the retry candidates reads statuses through the shared status
// cache, so every later reader of the cache still gets resolved paths.
func TestRunPathsRetryCandidateRebuild(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	dirs := runpath.Dirs{Log: filepath.Join(root, "logs"), Artifact: filepath.Join(root, "artifacts")}
	cache := fileutil.NewCache[*ir.DAGRunStatus]("test", 10, time.Hour)
	store := NewStore(filepath.Join(root, "dag-runs"),
		WithHistoryFileCache(cache), WithRunPaths(runpath.New(dirs, dirs, true)))
	logFile := filepath.Join(dirs.Log, "etl", "dag-run_run-1", "dag-run.log")

	attempt, err := store.CreateAttempt(ctx, persis.DAGRunCreateAttemptRequest{
		DAG: &ir.DAG{Name: "etl"}, Timestamp: time.Now(), DAGRunID: "run-1",
	})
	require.NoError(t, err)
	require.NoError(t, attempt.Open(ctx))
	require.NoError(t, attempt.Write(ctx, ir.DAGRunStatus{
		Name: "etl", DAGRunID: "run-1", Status: ir.Failed, Log: logFile,
		AutoRetryLimit: 1, ProcGroup: "etl",
	}))
	require.NoError(t, attempt.Close(ctx))
	concrete, ok := attempt.(*Attempt)
	require.True(t, ok)
	require.NoError(t, markRetryCandidatesDirty(concrete.file))

	candidates, err := store.ListRetryCandidates(ctx, persis.NewUTC(time.Now().Add(-time.Hour)))
	require.NoError(t, err)
	require.Len(t, candidates, 1)

	status, err := attempt.ReadStatus(ctx)
	require.NoError(t, err)
	assert.Equal(t, logFile, status.Log)
}
