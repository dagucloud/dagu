// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package exec_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis/file/dagrun"
	"github.com/stretchr/testify/require"
)

func TestResolveChildRetryRouteNestedRepeatedRun(t *testing.T) {
	ctx := context.Background()
	store := dagrun.New(filepath.Join(t.TempDir(), "dag-runs"))
	rootRef := exec.NewDAGRunRef("root", "root-run")

	rootStep := core.Step{Name: "run-middle", SubDAG: &core.SubDAG{Name: "middle"}, Parallel: &core.ParallelConfig{}}
	middleStep := core.Step{Name: "run-leaf", SubDAG: &core.SubDAG{Name: "leaf"}}
	targetStep := core.Step{ID: "target-id", Name: "target-step"}

	rootDAG := &core.DAG{Name: rootRef.Name, Steps: []core.Step{rootStep}}
	rootAttempt := createRetryTestAttempt(t, ctx, store, rootDAG, rootRef.ID, nil, exec.DAGRunStatus{
		Name:     rootRef.Name,
		DAGRunID: rootRef.ID,
		Status:   core.Failed,
		Nodes: []*exec.Node{{
			Step:            rootStep,
			Status:          core.NodeFailed,
			SubRuns:         []exec.SubDAGRun{{DAGRunID: "middle-current", DAGName: "middle", Params: "ITEM=current"}},
			SubRunsRepeated: []exec.SubDAGRun{{DAGRunID: "middle-target", DAGName: "middle", Params: "ITEM=target"}},
		}},
	})

	middleDAG := &core.DAG{Name: "middle", Steps: []core.Step{middleStep}}
	createRetryTestAttempt(t, ctx, store, middleDAG, "middle-target", &rootRef, exec.DAGRunStatus{
		Root:     rootRef,
		Parent:   rootRef,
		Name:     middleDAG.Name,
		DAGRunID: "middle-target",
		Status:   core.Failed,
		Nodes: []*exec.Node{{
			Step:    middleStep,
			Status:  core.NodeFailed,
			SubRuns: []exec.SubDAGRun{{DAGRunID: "leaf-target", DAGName: "leaf", Params: "MODE=retry"}},
		}},
	})

	leafDAG := &core.DAG{Name: "leaf", Steps: []core.Step{targetStep}}
	createRetryTestAttempt(t, ctx, store, leafDAG, "leaf-target", &rootRef, exec.DAGRunStatus{
		Root:     rootRef,
		Parent:   exec.NewDAGRunRef(middleDAG.Name, "middle-target"),
		Name:     leafDAG.Name,
		DAGRunID: "leaf-target",
		Status:   core.Succeeded,
		Nodes:    []*exec.Node{{Step: targetStep, Status: core.NodeSucceeded}},
	})

	route, targetStatus, err := exec.ResolveChildRetryRoute(ctx, store, rootRef, "leaf-target", "target-id")
	require.NoError(t, err)
	require.Equal(t, core.Succeeded, targetStatus.Status)
	require.Equal(t, "target-step", route.TargetStep)
	require.Equal(t, "run-middle", route.RootStep())
	require.Len(t, route.Segments, 2)
	require.Equal(t, "middle-target", route.Segments[0].DAGRunID)
	require.Equal(t, "ITEM=target", route.Segments[0].Params)
	require.ElementsMatch(t, []string{"middle-current", "middle-target"}, retryTestRunIDs(route.Segments[0].Runs))
	require.Equal(t, "run-leaf", route.NextStep())
	require.Equal(t, "leaf-target", route.Advance().Segments[0].DAGRunID)

	storedRoot, err := rootAttempt.ReadStatus(ctx)
	require.NoError(t, err)
	require.Equal(t, core.Failed, storedRoot.Status)
}

func TestReserveRetryAllowsOneConcurrentRequest(t *testing.T) {
	ctx := context.Background()
	store := dagrun.New(filepath.Join(t.TempDir(), "dag-runs"))
	dag := &core.DAG{Name: "retry-root", Steps: []core.Step{{Name: "step"}}}
	attempt := createRetryTestAttempt(t, ctx, store, dag, "run-1", nil, exec.DAGRunStatus{
		Name:     dag.Name,
		DAGRunID: "run-1",
		Status:   core.Failed,
		Nodes:    []*exec.Node{{Step: dag.Steps[0], Status: core.NodeFailed}},
	})
	status, err := attempt.ReadStatus(ctx)
	require.NoError(t, err)

	type result struct {
		status      *exec.DAGRunStatus
		reservation *exec.RetryReservation
		err         error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			<-start
			reserved, reservation, reserveErr := exec.ReserveRetry(ctx, store, status)
			results <- result{status: reserved, reservation: reservation, err: reserveErr}
		})
	}
	close(start)
	wg.Wait()
	close(results)

	var winner *exec.RetryReservation
	var failures int
	for got := range results {
		if got.err == nil {
			winner = got.reservation
			require.True(t, exec.IsRetryReserved(got.status))
			continue
		}
		failures++
		require.True(t, errors.Is(got.err, exec.ErrDAGRunActive) || errors.Is(got.err, exec.ErrRetryStaleLatest))
	}
	require.NotNil(t, winner)
	require.Equal(t, 1, failures)

	require.NoError(t, exec.RollbackRetryReservation(ctx, store, winner))
	rolledBack, err := attempt.ReadStatus(ctx)
	require.NoError(t, err)
	require.Equal(t, core.Failed, rolledBack.Status)
}

func createRetryTestAttempt(
	t *testing.T,
	ctx context.Context,
	store exec.DAGRunStore,
	dag *core.DAG,
	runID string,
	root *exec.DAGRunRef,
	status exec.DAGRunStatus,
) exec.DAGRunAttempt {
	t.Helper()
	attempt, err := store.CreateAttempt(ctx, dag, time.Now(), runID, exec.NewDAGRunAttemptOptions{RootDAGRun: root})
	require.NoError(t, err)
	status.AttemptID = attempt.ID()
	require.NoError(t, attempt.Open(ctx))
	require.NoError(t, attempt.Write(ctx, status))
	require.NoError(t, attempt.Close(ctx))
	return attempt
}

func retryTestRunIDs(runs []exec.SubDAGRun) []string {
	ids := make([]string, 0, len(runs))
	for _, run := range runs {
		ids = append(ids, run.DAGRunID)
	}
	return ids
}
