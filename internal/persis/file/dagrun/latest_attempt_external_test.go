// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	filedagrun "github.com/dagucloud/dagu/v2/internal/persis/file/dagrun"
	"github.com/stretchr/testify/require"
)

func TestStoreLatestAttemptUsesPersistedLatestPointer(t *testing.T) {
	ctx := context.Background()
	baseDir := t.TempDir()
	repository := newFileRepository(baseDir, persis.DAGRunRepositoryOptions{})
	dag := &ir.DAG{Name: "latest-pointer"}
	startedAt := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)

	attempt, err := repository.CreateAttempt(ctx, dag, startedAt, "run-1", persis.DAGRunCreateAttemptOptions{})
	require.NoError(t, err)
	require.NoError(t, attempt.Open(ctx))
	require.NoError(t, attempt.Write(ctx, ir.DAGRunStatus{
		Name:      dag.Name,
		DAGRunID:  "run-1",
		AttemptID: attempt.ID(),
		Status:    ir.Succeeded,
		StartedAt: startedAt.Format(time.RFC3339),
	}))
	require.NoError(t, attempt.Close(ctx))

	createStatuslessRunDir(t, baseDir, dag.Name, startedAt.Add(time.Hour), "run-2")

	latest, err := repository.LatestAttempt(ctx, dag.Name, persis.DAGRunLatestAttemptOptions{})
	require.NoError(t, err)
	status, err := latest.ReadStatus(ctx)
	require.NoError(t, err)
	require.Equal(t, "run-1", status.DAGRunID)
	require.Equal(t, ir.Succeeded, status.Status)
}

func TestUpdateLatestAttemptPointerHonorsCanceledContext(t *testing.T) {
	ctx := context.Background()
	baseDir := t.TempDir()
	dagName := "latest-pointer-canceled"
	startedAt := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	statusFile := createStatuslessRunDir(t, baseDir, dagName, startedAt, "run-1")
	require.NoError(t, os.WriteFile(statusFile, []byte("{}\n"), 0600))

	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()
	err := filedagrun.UpdateLatestAttemptPointerForTest(canceledCtx, statusFile)
	require.ErrorIs(t, err, context.Canceled)

	dagRunsDir := filepath.Join(baseDir, dagName, "dag-runs")
	pointerFile := filedagrun.LatestAttemptPointerPathForTest(dagRunsDir)
	_, err = os.Stat(pointerFile)
	require.ErrorIs(t, err, os.ErrNotExist)

	require.NoError(t, filedagrun.UpdateLatestAttemptPointerForTest(ctx, statusFile))
	_, err = os.Stat(pointerFile)
	require.NoError(t, err)
}

func BenchmarkStoreLatestAttemptWithPersistedLatestPointer(b *testing.B) {
	ctx := context.Background()
	baseDir := b.TempDir()
	repository := newFileRepository(baseDir, persis.DAGRunRepositoryOptions{})
	dag := &ir.DAG{Name: "latest-pointer-bench"}
	startedAt := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)

	attempt, err := repository.CreateAttempt(ctx, dag, startedAt, "run-1", persis.DAGRunCreateAttemptOptions{})
	require.NoError(b, err)
	require.NoError(b, attempt.Open(ctx))
	require.NoError(b, attempt.Write(ctx, ir.DAGRunStatus{
		Name:      dag.Name,
		DAGRunID:  "run-1",
		AttemptID: attempt.ID(),
		Status:    ir.Succeeded,
		StartedAt: startedAt.Format(time.RFC3339),
	}))
	require.NoError(b, attempt.Close(ctx))

	for i := range 4000 {
		createStatuslessRunDir(b, baseDir, dag.Name, startedAt.Add(time.Duration(i+1)*time.Second), fmt.Sprintf("run-%d", i+2))
	}

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		latest, err := repository.LatestAttempt(ctx, dag.Name, persis.DAGRunLatestAttemptOptions{})
		require.NoError(b, err)
		status, err := latest.ReadStatus(ctx)
		require.NoError(b, err)
		if status.DAGRunID != "run-1" {
			b.Fatalf("unexpected dag-run ID %q", status.DAGRunID)
		}
	}
}

func createStatuslessRunDir(t testing.TB, baseDir, dagName string, ts time.Time, runID string) string {
	t.Helper()

	runDir := filepath.Join(
		baseDir,
		dagName,
		"dag-runs",
		ts.Format("2006"),
		ts.Format("01"),
		ts.Format("02"),
		fmt.Sprintf("dag-run_%s_%s", ts.UTC().Format("20060102_150405Z"), runID),
		fmt.Sprintf("attempt_%s_%06d", ts.UTC().Format("20060102_150405_000Z"), 1),
	)
	require.NoError(t, os.MkdirAll(runDir, 0750))
	return filepath.Join(runDir, filedagrun.JSONLStatusFile)
}
