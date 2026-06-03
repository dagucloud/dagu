// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package memstore_test

import (
	"context"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/runtime/runstate"
	"github.com/dagucloud/dagu/internal/runtime/runstate/memstore"
	"github.com/stretchr/testify/require"
)

func TestStoreRecordsRootAndChildAttempts(t *testing.T) {
	ctx := context.Background()
	store := memstore.New()
	rootRef := exec.NewDAGRunRef("root", "root-run")

	root, err := store.BeginAttempt(ctx, runstate.BeginAttemptRequest{
		DAG:   &core.DAG{Name: "root"},
		RunID: rootRef.ID,
	})
	require.NoError(t, err)
	require.Equal(t, "root-run", root.ID())

	require.NoError(t, root.Open(ctx))
	require.NoError(t, root.RecordStatus(ctx, exec.DAGRunStatus{
		Name:      rootRef.Name,
		DAGRunID:  rootRef.ID,
		AttemptID: root.ID(),
		Status:    core.Running,
	}))
	require.NoError(t, root.RecordOutputs(ctx, &exec.DAGRunOutputs{
		Outputs: map[string]string{"result": "root-value"},
	}))
	require.NoError(t, root.WriteStepMessages(ctx, "ask", []exec.LLMMessage{{Role: "assistant", Content: "done"}}))

	openedRoot, err := store.OpenAttempt(ctx, rootRef)
	require.NoError(t, err)
	rootStatus, err := openedRoot.ReadStatus(ctx)
	require.NoError(t, err)
	require.Equal(t, core.Running, rootStatus.Status)
	rootOutputs, err := openedRoot.ReadOutputs(ctx)
	require.NoError(t, err)
	require.Equal(t, map[string]string{"result": "root-value"}, rootOutputs.Outputs)
	messages, err := openedRoot.ReadStepMessages(ctx, "ask")
	require.NoError(t, err)
	require.Equal(t, []exec.LLMMessage{{Role: "assistant", Content: "done"}}, messages)

	child, err := store.BeginAttempt(ctx, runstate.BeginAttemptRequest{
		DAG:        &core.DAG{Name: "child"},
		RunID:      "child-run",
		RootDAGRun: rootRef,
	})
	require.NoError(t, err)
	require.NoError(t, child.RecordStatus(ctx, exec.DAGRunStatus{
		Name:      "child",
		DAGRunID:  "child-run",
		AttemptID: child.ID(),
		Status:    core.Succeeded,
	}))
	require.NoError(t, child.RequestCancel(ctx))

	openedChild, err := store.OpenChildAttempt(ctx, rootRef, "child-run")
	require.NoError(t, err)
	childStatus, err := openedChild.ReadStatus(ctx)
	require.NoError(t, err)
	require.Equal(t, core.Succeeded, childStatus.Status)
	cancelled, err := openedChild.CancelRequested(ctx)
	require.NoError(t, err)
	require.True(t, cancelled)
}

func TestStoreRejectsInvalidRunID(t *testing.T) {
	ctx := context.Background()
	store := memstore.New()

	_, err := store.BeginAttempt(ctx, runstate.BeginAttemptRequest{
		DAG:   &core.DAG{Name: "invalid-run-id"},
		RunID: "not valid",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "dag-run ID")

	_, err = store.BeginAttempt(ctx, runstate.BeginAttemptRequest{
		DAG:   &core.DAG{Name: "too-long-run-id"},
		RunID: strings.Repeat("a", 65),
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "dag-run ID")
}
