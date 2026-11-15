// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package persis_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/stretchr/testify/require"
)

// A root run that waits on a sub-run, which itself waits on a leaf run, ends
// aborted together with both; steps that are not waiting keep their status.
func TestAbortWaitingRunEndsWaitingSubRuns(t *testing.T) {
	ctx := context.Background()
	repository := newRetryPathRepository(filepath.Join(t.TempDir(), "dag-runs"))
	rootRef := ir.NewDAGRunRef("root", "root-run")

	reviewStep := ir.Step{Name: "review", Approval: &ir.ApprovalConfig{}}
	callStep := ir.Step{Name: "call-child", SubDAG: &ir.SubDAG{Name: "child"}, Parallel: &ir.ParallelConfig{}}
	laterStep := ir.Step{Name: "later"}
	rootDAG := &ir.DAG{Name: rootRef.Name, Steps: []ir.Step{reviewStep, callStep, laterStep}}
	createRetryTestAttempt(t, ctx, repository, rootDAG, rootRef.ID, ir.DAGRunRef{}, ir.DAGRunStatus{
		Name:       rootRef.Name,
		DAGRunID:   rootRef.ID,
		Status:     ir.Waiting,
		FinishedAt: stringutil.FormatTime(time.Now().Add(-time.Minute)),
		Nodes: []*ir.Node{
			{Step: reviewStep, Status: ir.NodeWaiting},
			{
				Step:   callStep,
				Status: ir.NodeWaiting,
				SubRuns: []ir.SubDAGRun{
					{DAGRunID: "child-waiting", DAGName: "child"},
					{DAGRunID: "child-done", DAGName: "child"},
				},
			},
			{Step: laterStep, Status: ir.NodeNotStarted},
		},
	})

	leafCall := ir.Step{Name: "call-leaf", SubDAG: &ir.SubDAG{Name: "leaf"}}
	childDAG := &ir.DAG{Name: "child", Steps: []ir.Step{leafCall}}
	createRetryTestAttempt(t, ctx, repository, childDAG, "child-waiting", rootRef, ir.DAGRunStatus{
		Root:     rootRef,
		Parent:   rootRef,
		Name:     childDAG.Name,
		DAGRunID: "child-waiting",
		Status:   ir.Waiting,
		Nodes: []*ir.Node{{
			Step:    leafCall,
			Status:  ir.NodeWaiting,
			SubRuns: []ir.SubDAGRun{{DAGRunID: "leaf-waiting", DAGName: "leaf"}},
		}},
	})
	createRetryTestAttempt(t, ctx, repository, childDAG, "child-done", rootRef, ir.DAGRunStatus{
		Root:     rootRef,
		Parent:   rootRef,
		Name:     childDAG.Name,
		DAGRunID: "child-done",
		Status:   ir.Succeeded,
		Nodes:    []*ir.Node{{Step: leafCall, Status: ir.NodeSucceeded}},
	})

	approveStep := ir.Step{Name: "approve", Approval: &ir.ApprovalConfig{}}
	leafDAG := &ir.DAG{Name: "leaf", Steps: []ir.Step{approveStep}}
	createRetryTestAttempt(t, ctx, repository, leafDAG, "leaf-waiting", rootRef, ir.DAGRunStatus{
		Root:     rootRef,
		Parent:   ir.NewDAGRunRef(childDAG.Name, "child-waiting"),
		Name:     leafDAG.Name,
		DAGRunID: "leaf-waiting",
		Status:   ir.Waiting,
		Nodes:    []*ir.Node{{Step: approveStep, Status: ir.NodeWaiting}},
	})

	stoppedAt := time.Now()
	finishedAt := stringutil.FormatTime(stoppedAt)
	require.NoError(t, repository.AbortWaitingRun(ctx, readRootStatus(t, ctx, repository, rootRef), stoppedAt))

	root := readRootStatus(t, ctx, repository, rootRef)
	require.Equal(t, ir.Aborted, root.Status)
	require.Equal(t, finishedAt, root.FinishedAt)
	require.Equal(t, ir.NodeAborted, root.Nodes[0].Status)
	require.Equal(t, finishedAt, root.Nodes[0].FinishedAt)
	require.Equal(t, ir.NodeAborted, root.Nodes[1].Status)
	require.Equal(t, ir.NodeNotStarted, root.Nodes[2].Status)
	require.Empty(t, root.Nodes[2].FinishedAt)

	child := readSubStatus(t, ctx, repository, rootRef, "child-waiting")
	require.Equal(t, ir.Aborted, child.Status)
	require.Equal(t, ir.NodeAborted, child.Nodes[0].Status)
	require.Equal(t, ir.Succeeded, readSubStatus(t, ctx, repository, rootRef, "child-done").Status)

	leaf := readSubStatus(t, ctx, repository, rootRef, "leaf-waiting")
	require.Equal(t, ir.Aborted, leaf.Status)
	require.Equal(t, ir.NodeAborted, leaf.Nodes[0].Status)
}

// A run that left the checkpoint it was read at, such as one queued for
// resume, is not stopped, and neither is the sub-run it waits on.
func TestAbortWaitingRunRejectsChangedRun(t *testing.T) {
	ctx := context.Background()
	repository := newRetryPathRepository(filepath.Join(t.TempDir(), "dag-runs"))
	rootRef := ir.NewDAGRunRef("root", "root-run")
	callStep := ir.Step{Name: "call-child", SubDAG: &ir.SubDAG{Name: "child"}}
	rootDAG := &ir.DAG{Name: rootRef.Name, Steps: []ir.Step{callStep}}
	attempt := createRetryTestAttempt(t, ctx, repository, rootDAG, rootRef.ID, ir.DAGRunRef{}, ir.DAGRunStatus{
		Name:     rootRef.Name,
		DAGRunID: rootRef.ID,
		Status:   ir.Waiting,
		Nodes: []*ir.Node{{
			Step:    callStep,
			Status:  ir.NodeWaiting,
			SubRuns: []ir.SubDAGRun{{DAGRunID: "child-waiting", DAGName: "child"}},
		}},
	})
	approveStep := ir.Step{Name: "approve", Approval: &ir.ApprovalConfig{}}
	childDAG := &ir.DAG{Name: "child", Steps: []ir.Step{approveStep}}
	createRetryTestAttempt(t, ctx, repository, childDAG, "child-waiting", rootRef, ir.DAGRunStatus{
		Root:     rootRef,
		Parent:   rootRef,
		Name:     childDAG.Name,
		DAGRunID: "child-waiting",
		Status:   ir.Waiting,
		Nodes:    []*ir.Node{{Step: approveStep, Status: ir.NodeWaiting}},
	})
	waiting := readRootStatus(t, ctx, repository, rootRef)

	queued := *waiting
	queued.Status = ir.Queued
	require.NoError(t, attempt.Open(ctx))
	require.NoError(t, attempt.Write(ctx, queued))
	require.NoError(t, attempt.Close(ctx))

	err := repository.AbortWaitingRun(ctx, waiting, time.Now())
	require.ErrorIs(t, err, dagrun.ErrWaitingAbortStateChanged)
	require.Equal(t, ir.Queued, readRootStatus(t, ctx, repository, rootRef).Status)
	require.Equal(t, ir.Waiting, readSubStatus(t, ctx, repository, rootRef, "child-waiting").Status)
}

func TestAbortWaitingRunRejectsSubRun(t *testing.T) {
	repository := newRetryPathRepository(filepath.Join(t.TempDir(), "dag-runs"))
	rootRef := ir.NewDAGRunRef("root", "root-run")
	err := repository.AbortWaitingRun(context.Background(), &ir.DAGRunStatus{
		Root:     rootRef,
		Parent:   rootRef,
		Name:     "child",
		DAGRunID: "child-run",
		Status:   ir.Waiting,
	}, time.Now())
	require.ErrorIs(t, err, dagrun.ErrWaitingAbortNotRoot)
}

func readRootStatus(t *testing.T, ctx context.Context, repository *persis.DAGRunRepository, ref ir.DAGRunRef) *ir.DAGRunStatus {
	t.Helper()
	attempt, err := repository.FindAttempt(ctx, ref)
	require.NoError(t, err)
	status, err := attempt.ReadStatus(ctx)
	require.NoError(t, err)
	return status
}

func readSubStatus(
	t *testing.T, ctx context.Context, repository *persis.DAGRunRepository, root ir.DAGRunRef, runID string,
) *ir.DAGRunStatus {
	t.Helper()
	attempt, err := repository.FindSubAttempt(ctx, root, runID)
	require.NoError(t, err)
	status, err := attempt.ReadStatus(ctx)
	require.NoError(t, err)
	return status
}
