// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package exec_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis/file/dagrun"
	"github.com/stretchr/testify/require"
)

func TestResolveRetryPathNestedRepeatedRun(t *testing.T) {
	ctx := context.Background()
	store := dagrun.New(filepath.Join(t.TempDir(), "dag-runs"))
	rootRef := exec.NewDAGRunRef("root", "root-run")

	rootStep := core.Step{Name: "run-middle", SubDAG: &core.SubDAG{Name: "middle"}, Parallel: &core.ParallelConfig{}}
	middleStep := core.Step{Name: "run-leaf", SubDAG: &core.SubDAG{Name: "leaf"}}
	targetStep := core.Step{Name: "target-step"}

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

	path, targetStatus, err := exec.ResolveRetryPath(ctx, store, rootRef, "leaf-target", "target-step")
	require.NoError(t, err)
	require.Equal(t, core.Succeeded, targetStatus.Status)
	require.Equal(t, "target-step", path.Step)
	require.Equal(t, "run-middle", path.RootStep())
	require.Equal(t, []exec.RetryHop{
		{Step: "run-middle", RunID: "middle-target"},
		{Step: "run-leaf", RunID: "leaf-target"},
	}, path.Hops)
	require.Equal(t, "run-leaf", path.NextStep())

	storedRoot, err := rootAttempt.ReadStatus(ctx)
	require.NoError(t, err)
	require.Equal(t, core.Failed, storedRoot.Status)
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
