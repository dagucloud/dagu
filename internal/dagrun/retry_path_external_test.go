// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagrun_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	filedagrun "github.com/dagucloud/dagu/v2/internal/persis/file/dagrun"
	"github.com/stretchr/testify/require"
)

func TestResolveRetryPathNestedRun(t *testing.T) {
	ctx := context.Background()
	store := filedagrun.New(filepath.Join(t.TempDir(), "dag-runs"))
	rootRef := ir.NewDAGRunRef("root", "root-run")

	rootStep := ir.Step{Name: "run-middle", SubDAG: &ir.SubDAG{Name: "middle"}, Parallel: &ir.ParallelConfig{}}
	middleStep := ir.Step{Name: "run-leaf", SubDAG: &ir.SubDAG{Name: "leaf"}}
	targetStep := ir.Step{Name: "target-step"}

	rootDAG := &ir.DAG{Name: rootRef.Name, Steps: []ir.Step{rootStep}}
	rootAttempt := createRetryTestAttempt(t, ctx, store, rootDAG, rootRef.ID, nil, ir.DAGRunStatus{
		Name:     rootRef.Name,
		DAGRunID: rootRef.ID,
		Status:   ir.Failed,
		Nodes: []*ir.Node{{
			Step:   rootStep,
			Status: ir.NodeFailed,
			SubRuns: []ir.SubDAGRun{
				{DAGRunID: "middle-current", DAGName: "middle", Params: "ITEM=current"},
				{DAGRunID: "middle-target", DAGName: "middle", Params: "ITEM=target"},
			},
		}},
	})

	middleDAG := &ir.DAG{Name: "middle", Steps: []ir.Step{middleStep}}
	createRetryTestAttempt(t, ctx, store, middleDAG, "middle-target", &rootRef, ir.DAGRunStatus{
		Root:     rootRef,
		Parent:   rootRef,
		Name:     middleDAG.Name,
		DAGRunID: "middle-target",
		Status:   ir.Failed,
		Nodes: []*ir.Node{{
			Step:    middleStep,
			Status:  ir.NodeFailed,
			SubRuns: []ir.SubDAGRun{{DAGRunID: "leaf-target", DAGName: "leaf", Params: "MODE=retry"}},
		}},
	})

	leafDAG := &ir.DAG{Name: "leaf", Steps: []ir.Step{targetStep}}
	createRetryTestAttempt(t, ctx, store, leafDAG, "leaf-target", &rootRef, ir.DAGRunStatus{
		Root:     rootRef,
		Parent:   ir.NewDAGRunRef(middleDAG.Name, "middle-target"),
		Name:     leafDAG.Name,
		DAGRunID: "leaf-target",
		Status:   ir.Succeeded,
		Nodes:    []*ir.Node{{Step: targetStep, Status: ir.NodeSucceeded}},
	})

	path, targetStatus, err := dagrun.ResolveRetryPath(ctx, store, rootRef, "leaf-target", "target-step")
	require.NoError(t, err)
	require.Equal(t, ir.Succeeded, targetStatus.Status)
	require.Equal(t, "target-step", path.Step)
	require.Equal(t, "run-middle", path.RootStep())
	require.Equal(t, []dagrun.RetryHop{
		{Step: "run-middle", RunID: "middle-target"},
		{Step: "run-leaf", RunID: "leaf-target"},
	}, path.Hops)
	require.Equal(t, "run-leaf", path.NextStep())

	storedRoot, err := rootAttempt.ReadStatus(ctx)
	require.NoError(t, err)
	require.Equal(t, ir.Failed, storedRoot.Status)
}

// TestResolveRetryPathRejectsRepeatingStep asserts that steps inside child DAG
// runs of a repeating step cannot be retried individually.
func TestResolveRetryPathRejectsRepeatingStep(t *testing.T) {
	t.Run("child run from an earlier repeat cycle", func(t *testing.T) {
		rootStep := ir.Step{Name: "run-child", SubDAG: &ir.SubDAG{Name: "child"}}
		_, _, err := resolveRetryPathForChild(t, rootStep, ir.Node{
			Step:            rootStep,
			Status:          ir.NodeFailed,
			SubRuns:         []ir.SubDAGRun{{DAGRunID: "child-current", DAGName: "child"}},
			SubRunsRepeated: []ir.SubDAGRun{{DAGRunID: "child-target", DAGName: "child"}},
		})
		require.ErrorIs(t, err, dagrun.ErrRepeatingStepTarget)
	})

	t.Run("latest child run of a repeating step", func(t *testing.T) {
		rootStep := ir.Step{
			Name:         "run-child",
			SubDAG:       &ir.SubDAG{Name: "child"},
			RepeatPolicy: ir.RepeatPolicy{RepeatMode: ir.RepeatModeWhile},
		}
		_, _, err := resolveRetryPathForChild(t, rootStep, ir.Node{
			Step:    rootStep,
			Status:  ir.NodeFailed,
			SubRuns: []ir.SubDAGRun{{DAGRunID: "child-target", DAGName: "child"}},
		})
		require.ErrorIs(t, err, dagrun.ErrRepeatingStepTarget)
	})
}

// resolveRetryPathForChild persists a root run holding rootNode plus the
// "child-target" child run, then resolves the retry path to that child's
// "target-step".
func resolveRetryPathForChild(
	t *testing.T,
	rootStep ir.Step,
	rootNode ir.Node,
) (dagrun.RetryPath, *ir.DAGRunStatus, error) {
	t.Helper()
	ctx := context.Background()
	store := filedagrun.New(filepath.Join(t.TempDir(), "dag-runs"))
	rootRef := ir.NewDAGRunRef("root", "root-run")
	targetStep := ir.Step{Name: "target-step"}

	rootDAG := &ir.DAG{Name: rootRef.Name, Steps: []ir.Step{rootStep}}
	createRetryTestAttempt(t, ctx, store, rootDAG, rootRef.ID, nil, ir.DAGRunStatus{
		Name:     rootRef.Name,
		DAGRunID: rootRef.ID,
		Status:   ir.Failed,
		Nodes:    []*ir.Node{&rootNode},
	})

	childDAG := &ir.DAG{Name: "child", Steps: []ir.Step{targetStep}}
	createRetryTestAttempt(t, ctx, store, childDAG, "child-target", &rootRef, ir.DAGRunStatus{
		Root:     rootRef,
		Parent:   rootRef,
		Name:     childDAG.Name,
		DAGRunID: "child-target",
		Status:   ir.Failed,
		Nodes:    []*ir.Node{{Step: targetStep, Status: ir.NodeFailed}},
	})

	return dagrun.ResolveRetryPath(ctx, store, rootRef, "child-target", targetStep.Name)
}

func createRetryTestAttempt(
	t *testing.T,
	ctx context.Context,
	store dagrun.DAGRunStore,
	dag *ir.DAG,
	runID string,
	root *ir.DAGRunRef,
	status ir.DAGRunStatus,
) dagrun.DAGRunAttempt {
	t.Helper()
	attempt, err := store.CreateAttempt(ctx, dag, time.Now(), runID, dagrun.NewDAGRunAttemptOptions{RootDAGRun: root})
	require.NoError(t, err)
	status.AttemptID = attempt.ID()
	require.NoError(t, attempt.Open(ctx))
	require.NoError(t, attempt.Write(ctx, status))
	require.NoError(t, attempt.Close(ctx))
	return attempt
}
