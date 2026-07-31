// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package exec_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/core"
	"github.com/dagucloud/dagu/v2/internal/core/exec"
	"github.com/dagucloud/dagu/v2/internal/persis/file/dagrun"
	"github.com/stretchr/testify/require"
)

func TestResolveRetryPathNestedRun(t *testing.T) {
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
			Step:   rootStep,
			Status: core.NodeFailed,
			SubRuns: []exec.SubDAGRun{
				{DAGRunID: "middle-current", DAGName: "middle", Params: "ITEM=current"},
				{DAGRunID: "middle-target", DAGName: "middle", Params: "ITEM=target"},
			},
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

// TestResolveRetryPathRejectsRepeatingStep asserts that steps inside child DAG
// runs of a repeating step cannot be retried individually.
func TestResolveRetryPathRejectsRepeatingStep(t *testing.T) {
	t.Run("child run from an earlier repeat cycle", func(t *testing.T) {
		rootStep := core.Step{Name: "run-child", SubDAG: &core.SubDAG{Name: "child"}}
		_, _, err := resolveRetryPathForChild(t, rootStep, exec.Node{
			Step:            rootStep,
			Status:          core.NodeFailed,
			SubRuns:         []exec.SubDAGRun{{DAGRunID: "child-current", DAGName: "child"}},
			SubRunsRepeated: []exec.SubDAGRun{{DAGRunID: "child-target", DAGName: "child"}},
		})
		require.ErrorIs(t, err, exec.ErrRepeatingStepTarget)
	})

	t.Run("latest child run of a repeating step", func(t *testing.T) {
		rootStep := core.Step{
			Name:         "run-child",
			SubDAG:       &core.SubDAG{Name: "child"},
			RepeatPolicy: core.RepeatPolicy{RepeatMode: core.RepeatModeWhile},
		}
		_, _, err := resolveRetryPathForChild(t, rootStep, exec.Node{
			Step:    rootStep,
			Status:  core.NodeFailed,
			SubRuns: []exec.SubDAGRun{{DAGRunID: "child-target", DAGName: "child"}},
		})
		require.ErrorIs(t, err, exec.ErrRepeatingStepTarget)
	})
}

// resolveRetryPathForChild persists a root run holding rootNode plus the
// "child-target" child run, then resolves the retry path to that child's
// "target-step".
func resolveRetryPathForChild(
	t *testing.T,
	rootStep core.Step,
	rootNode exec.Node,
) (exec.RetryPath, *exec.DAGRunStatus, error) {
	t.Helper()
	ctx := context.Background()
	store := dagrun.New(filepath.Join(t.TempDir(), "dag-runs"))
	rootRef := exec.NewDAGRunRef("root", "root-run")
	targetStep := core.Step{Name: "target-step"}

	rootDAG := &core.DAG{Name: rootRef.Name, Steps: []core.Step{rootStep}}
	createRetryTestAttempt(t, ctx, store, rootDAG, rootRef.ID, nil, exec.DAGRunStatus{
		Name:     rootRef.Name,
		DAGRunID: rootRef.ID,
		Status:   core.Failed,
		Nodes:    []*exec.Node{&rootNode},
	})

	childDAG := &core.DAG{Name: "child", Steps: []core.Step{targetStep}}
	createRetryTestAttempt(t, ctx, store, childDAG, "child-target", &rootRef, exec.DAGRunStatus{
		Root:     rootRef,
		Parent:   rootRef,
		Name:     childDAG.Name,
		DAGRunID: "child-target",
		Status:   core.Failed,
		Nodes:    []*exec.Node{{Step: targetStep, Status: core.NodeFailed}},
	})

	return exec.ResolveRetryPath(ctx, store, rootRef, "child-target", targetStep.Name)
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
