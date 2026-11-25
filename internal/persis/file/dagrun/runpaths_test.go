// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun

import (
	"context"
	"path/filepath"
	"strings"
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

// A write that compacts a status file grown past its limit keeps the record
// in its stored form, so a long-running run keeps relative paths.
func TestRunPathsSizeCompaction(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	dirs := runpath.Dirs{Log: filepath.Join(root, "logs"), Artifact: filepath.Join(root, "artifacts")}
	file := filepath.Join(createTempDir(t), "status.dat")
	att, err := NewAttempt(file, nil, withRunPaths(runpath.New(dirs, dirs, true)))
	require.NoError(t, err)
	require.NoError(t, att.Open(ctx))
	defer func() { _ = att.Close(ctx) }()

	big := createTestStatus(ir.Running)
	big.Log = filepath.Join(dirs.Log, "etl", "dag-run_run-1", "dag-run.log")
	big.Nodes[0].Error = strings.Repeat("y", statusFileMaxSize+1)
	require.NoError(t, att.Write(ctx, big))
	require.NoError(t, att.Write(ctx, big))
	require.Zero(t, att.appended, "the second write compacts")

	stored, err := ParseStatusFile(file)
	require.NoError(t, err)
	assert.Equal(t, "./etl/dag-run_run-1/dag-run.log", stored.Log)
}
