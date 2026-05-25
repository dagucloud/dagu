// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dagucloud/dagu/internal/dagstate"
	"github.com/dagucloud/dagu/internal/persis/store"
	"github.com/dagucloud/dagu/internal/persis/testutil"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
)

func TestHandlerStateRPCPutGetListDelete(t *testing.T) {
	t.Parallel()

	h := NewHandler(HandlerConfig{
		StateStore: store.NewDAGStateStore(testutil.NewMemoryBackend().Collection("dag_state")),
	})
	ctx := context.Background()
	ref := &coordinatorv1.StateRef{Scope: string(dagstate.ScopeDAG), Namespace: "daily-agent", Key: "cursor"}

	putResp, err := h.PutState(ctx, &coordinatorv1.PutStateRequest{
		Ref:   ref,
		Value: []byte(`{"last_id":123}`),
		UpdatedBy: &coordinatorv1.StateUpdateSource{
			DagName:  "daily-agent",
			DagRunId: "run-1",
			StepName: "save",
		},
	})
	require.NoError(t, err)
	require.NotNil(t, putResp.Entry)
	assert.Equal(t, int64(1), putResp.Entry.Version)

	getResp, err := h.GetState(ctx, &coordinatorv1.GetStateRequest{Ref: ref})
	require.NoError(t, err)
	require.True(t, getResp.Found)
	require.NotNil(t, getResp.Entry)
	assert.Equal(t, int64(1), getResp.Entry.Version)
	assert.JSONEq(t, `{"last_id":123}`, string(getResp.Entry.Value))

	listResp, err := h.ListState(ctx, &coordinatorv1.ListStateRequest{
		Scope:     string(dagstate.ScopeDAG),
		Namespace: "daily-agent",
		KeyPrefix: "cur",
	})
	require.NoError(t, err)
	require.Len(t, listResp.Entries, 1)
	assert.Equal(t, "cursor", listResp.Entries[0].Ref.Key)

	deleteResp, err := h.DeleteState(ctx, &coordinatorv1.DeleteStateRequest{Ref: ref})
	require.NoError(t, err)
	assert.True(t, deleteResp.Deleted)

	getResp, err = h.GetState(ctx, &coordinatorv1.GetStateRequest{Ref: ref})
	require.NoError(t, err)
	assert.False(t, getResp.Found)
}

func TestHandlerStateRPCConflictAndMissingStore(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ref := &coordinatorv1.StateRef{Scope: string(dagstate.ScopeDAG), Namespace: "daily-agent", Key: "cursor"}
	h := NewHandler(HandlerConfig{
		StateStore: store.NewDAGStateStore(testutil.NewMemoryBackend().Collection("dag_state")),
	})
	_, err := h.PutState(ctx, &coordinatorv1.PutStateRequest{Ref: ref, Value: []byte(`1`)})
	require.NoError(t, err)

	_, err = h.PutState(ctx, &coordinatorv1.PutStateRequest{
		Ref:                ref,
		Value:              []byte(`2`),
		HasExpectedVersion: true,
		ExpectedVersion:    99,
	})
	require.Error(t, err)
	assert.Equal(t, codes.Aborted, status.Code(err))

	missing := NewHandler(HandlerConfig{})
	_, err = missing.GetState(ctx, &coordinatorv1.GetStateRequest{Ref: ref})
	require.Error(t, err)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func TestStateRPCErrorPreservesContextCodes(t *testing.T) {
	t.Parallel()

	assert.Equal(t, codes.Canceled, status.Code(stateRPCError(context.Canceled)))
	assert.Equal(t, codes.DeadlineExceeded, status.Code(stateRPCError(context.DeadlineExceeded)))
}
