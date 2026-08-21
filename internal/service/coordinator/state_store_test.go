// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/persis/testutil"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestStateStoreClientUsesCoordinatorRPC(t *testing.T) {
	t.Parallel()

	handler := NewHandler(HandlerConfig{
		StateStore: store.NewDAGStateStore(testutil.NewMemoryBackend().Collection("dag_state")),
	})
	stateStore := NewStateStoreClient(fakeStateClient{handler: handler})
	ctx := context.Background()
	ref := dagrun.StateRef{Scope: dagrun.StateScopeDAG, Namespace: "daily-agent", Key: "cursor"}

	entry, err := stateStore.Put(ctx, ref, json.RawMessage(`{"last_id":123}`), dagrun.StatePutOptions{})
	require.NoError(t, err)
	assert.Equal(t, int64(1), entry.Version)

	got, err := stateStore.Get(ctx, ref)
	require.NoError(t, err)
	assert.JSONEq(t, `{"last_id":123}`, string(got.Value))

	entries, err := stateStore.List(ctx, dagrun.StateListOptions{
		Scope:     dagrun.StateScopeDAG,
		Namespace: "daily-agent",
		KeyPrefix: "cur",
	})
	require.NoError(t, err)
	require.Len(t, entries, 1)

	deleted, err := stateStore.Delete(ctx, ref)
	require.NoError(t, err)
	assert.True(t, deleted)
}

func TestStateStoreClientValidatesBeforeRPC(t *testing.T) {
	t.Parallel()

	stateStore := NewStateStoreClient(fakeStateClient{
		handler: NewHandler(HandlerConfig{
			StateStore: store.NewDAGStateStore(testutil.NewMemoryBackend().Collection("dag_state")),
		}),
	})
	ctx := context.Background()

	_, err := stateStore.Get(ctx, dagrun.StateRef{Scope: dagrun.StateScopeDAG, Namespace: "daily-agent", Key: "../bad"})
	require.ErrorIs(t, err, dagrun.ErrInvalidStateRef)

	_, err = stateStore.Put(ctx, dagrun.StateRef{Scope: dagrun.StateScopeDAG, Namespace: "daily-agent", Key: "bad-json"}, json.RawMessage(`{`), dagrun.StatePutOptions{})
	require.ErrorIs(t, err, dagrun.ErrInvalidStateValue)

	_, err = stateStore.List(ctx, dagrun.StateListOptions{
		Scope:     dagrun.StateScope("invalid"),
		Namespace: "daily-agent",
	})
	require.ErrorIs(t, err, dagrun.ErrInvalidStateRef)
}

func TestStateStoreClientNormalizesValueBeforeRPC(t *testing.T) {
	t.Parallel()

	client := &capturingStateClient{}
	stateStore := NewStateStoreClient(client)
	ref := dagrun.StateRef{Scope: dagrun.StateScopeDAG, Namespace: "daily-agent", Key: "cursor"}

	entry, err := stateStore.Put(context.Background(), ref, json.RawMessage(`{ "b": 2, "a": 1 }`), dagrun.StatePutOptions{})
	require.NoError(t, err)
	assert.Equal(t, `{"a":1,"b":2}`, string(client.putValue))
	assert.JSONEq(t, `{"a":1,"b":2}`, string(entry.Value))
}

func TestStateClientErrorPreservesInvalidArgumentSubtype(t *testing.T) {
	t.Parallel()

	require.ErrorIs(t, stateClientError(status.Error(codes.InvalidArgument, "dag state: invalid ref: key is required")), dagrun.ErrInvalidStateRef)
	require.ErrorIs(t, stateClientError(status.Error(codes.InvalidArgument, "dag state: value too large: 1048577 bytes exceeds 1048576")), dagrun.ErrStateValueTooLarge)
	require.ErrorIs(t, stateClientError(status.Error(codes.InvalidArgument, "dag state: invalid value: unexpected end of JSON input")), dagrun.ErrInvalidStateValue)
}

type fakeStateClient struct {
	handler *Handler
}

func (f fakeStateClient) GetState(ctx context.Context, req *coordinatorv1.GetStateRequest) (*coordinatorv1.GetStateResponse, error) {
	return f.handler.GetState(ctx, req)
}

func (f fakeStateClient) PutState(ctx context.Context, req *coordinatorv1.PutStateRequest) (*coordinatorv1.PutStateResponse, error) {
	return f.handler.PutState(ctx, req)
}

func (f fakeStateClient) DeleteState(ctx context.Context, req *coordinatorv1.DeleteStateRequest) (*coordinatorv1.DeleteStateResponse, error) {
	return f.handler.DeleteState(ctx, req)
}

func (f fakeStateClient) ListState(ctx context.Context, req *coordinatorv1.ListStateRequest) (*coordinatorv1.ListStateResponse, error) {
	return f.handler.ListState(ctx, req)
}

type capturingStateClient struct {
	putValue []byte
}

func (c *capturingStateClient) GetState(context.Context, *coordinatorv1.GetStateRequest) (*coordinatorv1.GetStateResponse, error) {
	return &coordinatorv1.GetStateResponse{Found: false}, nil
}

func (c *capturingStateClient) PutState(_ context.Context, req *coordinatorv1.PutStateRequest) (*coordinatorv1.PutStateResponse, error) {
	c.putValue = append([]byte(nil), req.GetValue()...)
	return &coordinatorv1.PutStateResponse{
		Entry: stateEntryToProto(&dagrun.StateEntry{
			StateRef: dagrun.StateRef{
				Scope:     dagrun.StateScope(req.GetRef().GetScope()),
				Namespace: req.GetRef().GetNamespace(),
				Key:       req.GetRef().GetKey(),
			},
			Value:   append(json.RawMessage(nil), req.GetValue()...),
			Version: 1,
			Hash:    dagrun.HashStateValue(req.GetValue()),
		}),
	}, nil
}

func (c *capturingStateClient) DeleteState(context.Context, *coordinatorv1.DeleteStateRequest) (*coordinatorv1.DeleteStateResponse, error) {
	return &coordinatorv1.DeleteStateResponse{}, nil
}

func (c *capturingStateClient) ListState(context.Context, *coordinatorv1.ListStateRequest) (*coordinatorv1.ListStateResponse, error) {
	return &coordinatorv1.ListStateResponse{}, nil
}
