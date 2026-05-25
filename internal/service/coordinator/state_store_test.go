// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/internal/dagstate"
	"github.com/dagucloud/dagu/internal/persis/store"
	"github.com/dagucloud/dagu/internal/persis/testutil"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
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
	ref := dagstate.Ref{Scope: dagstate.ScopeDAG, Namespace: "daily-agent", Key: "cursor"}

	entry, err := stateStore.Put(ctx, ref, json.RawMessage(`{"last_id":123}`), dagstate.PutOptions{})
	require.NoError(t, err)
	assert.Equal(t, int64(1), entry.Version)

	got, err := stateStore.Get(ctx, ref)
	require.NoError(t, err)
	assert.JSONEq(t, `{"last_id":123}`, string(got.Value))

	entries, err := stateStore.List(ctx, dagstate.ListOptions{
		Scope:     dagstate.ScopeDAG,
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

	_, err := stateStore.Get(ctx, dagstate.Ref{Scope: dagstate.ScopeDAG, Namespace: "daily-agent", Key: "../bad"})
	require.ErrorIs(t, err, dagstate.ErrInvalidRef)

	_, err = stateStore.Put(ctx, dagstate.Ref{Scope: dagstate.ScopeDAG, Namespace: "daily-agent", Key: "bad-json"}, json.RawMessage(`{`), dagstate.PutOptions{})
	require.ErrorIs(t, err, dagstate.ErrInvalidValue)

	_, err = stateStore.List(ctx, dagstate.ListOptions{
		Scope:     dagstate.Scope("invalid"),
		Namespace: "daily-agent",
	})
	require.ErrorIs(t, err, dagstate.ErrInvalidRef)
}

func TestStateStoreClientNormalizesValueBeforeRPC(t *testing.T) {
	t.Parallel()

	client := &capturingStateClient{}
	stateStore := NewStateStoreClient(client)
	ref := dagstate.Ref{Scope: dagstate.ScopeDAG, Namespace: "daily-agent", Key: "cursor"}

	entry, err := stateStore.Put(context.Background(), ref, json.RawMessage(`{ "b": 2, "a": 1 }`), dagstate.PutOptions{})
	require.NoError(t, err)
	assert.Equal(t, `{"a":1,"b":2}`, string(client.putValue))
	assert.JSONEq(t, `{"a":1,"b":2}`, string(entry.Value))
}

func TestStateClientErrorPreservesInvalidArgumentSubtype(t *testing.T) {
	t.Parallel()

	require.ErrorIs(t, stateClientError(status.Error(codes.InvalidArgument, "dag state: invalid ref: key is required")), dagstate.ErrInvalidRef)
	require.ErrorIs(t, stateClientError(status.Error(codes.InvalidArgument, "dag state: value too large: 1048577 bytes exceeds 1048576")), dagstate.ErrValueTooLarge)
	require.ErrorIs(t, stateClientError(status.Error(codes.InvalidArgument, "dag state: invalid value: unexpected end of JSON input")), dagstate.ErrInvalidValue)
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
		Entry: stateEntryToProto(&dagstate.Entry{
			Ref:     dagstate.Ref{Scope: dagstate.Scope(req.GetRef().GetScope()), Namespace: req.GetRef().GetNamespace(), Key: req.GetRef().GetKey()},
			Value:   append(json.RawMessage(nil), req.GetValue()...),
			Version: 1,
			Hash:    dagstate.HashValue(req.GetValue()),
		}),
	}, nil
}

func (c *capturingStateClient) DeleteState(context.Context, *coordinatorv1.DeleteStateRequest) (*coordinatorv1.DeleteStateResponse, error) {
	return &coordinatorv1.DeleteStateResponse{}, nil
}

func (c *capturingStateClient) ListState(context.Context, *coordinatorv1.ListStateRequest) (*coordinatorv1.ListStateResponse, error) {
	return &coordinatorv1.ListStateResponse{}, nil
}
