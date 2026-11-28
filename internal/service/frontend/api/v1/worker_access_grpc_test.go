// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"net"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	openapiv1 "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
)

// rewordingCoordinator refuses with messages this server does not know,
// and the reason as a status detail. With ready, it reports join readiness.
type rewordingCoordinator struct {
	coordinatorv1.UnimplementedCoordinatorServiceServer
	ready bool
	calls atomic.Int32
}

func (c *rewordingCoordinator) CreateInvite(context.Context, *coordinatorv1.CreateInviteRequest) (*coordinatorv1.CreateInviteResponse, error) {
	return nil, withReason(codes.FailedPrecondition, "own_certificates", "a reworded refusal")
}

func (c *rewordingCoordinator) ListInvites(context.Context, *coordinatorv1.ListInvitesRequest) (*coordinatorv1.ListInvitesResponse, error) {
	c.calls.Add(1)
	// A failed read, then a delete with no answer: what once let a pasted
	// invite through as a deleted ID.
	return nil, status.Error(codes.Internal, "failed to list the invites")
}

func (c *rewordingCoordinator) DeleteInvite(context.Context, *coordinatorv1.DeleteInviteRequest) (*coordinatorv1.DeleteInviteResponse, error) {
	c.calls.Add(1)
	return nil, status.Error(codes.Unavailable, "connection reset")
}

func (c *rewordingCoordinator) GetJoinReadiness(context.Context, *coordinatorv1.GetJoinReadinessRequest) (*coordinatorv1.GetJoinReadinessResponse, error) {
	if c.ready {
		return &coordinatorv1.GetJoinReadinessResponse{BuiltinTls: true}, nil
	}
	return nil, withReason(codes.Unauthenticated, "require_tls", "another reworded refusal")
}

// serveRewording serves c with opts and returns its address.
func serveRewording(t *testing.T, c *rewordingCoordinator, opts ...grpc.ServerOption) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer(opts...)
	coordinatorv1.RegisterCoordinatorServiceServer(server, c)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	return listener.Addr().String()
}

// The reason a coordinator sends reaches the REST answer over gRPC, though
// the server knows neither message: in the error of a refused create, and
// in the errorReason of a coordinator that did not report join readiness.
func TestWorkerAccessReasonOverGRPC(t *testing.T) {
	t.Parallel()

	registry, err := coordinator.NewStaticRegistry([]string{
		serveRewording(t, &rewordingCoordinator{ready: true}),
		serveRewording(t, &rewordingCoordinator{}),
	})
	require.NoError(t, err)
	client := coordinator.New(registry, coordinator.DefaultConfig())
	t.Cleanup(func() { _ = client.Cleanup(context.Background()) })
	a, _ := rolesAPI(client)
	ctx := userContext(auth.RoleAdmin)

	_, err = a.CreateWorkerInvite(ctx, openapiv1.CreateWorkerInviteRequestObject{})
	var apiErr *Error
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusConflict, apiErr.HTTPStatus)
	assert.Equal(t, "a reworded refusal", apiErr.Message)
	assert.Equal(t, map[string]any{"reason": "own_certificates"}, apiErr.Details)

	resp, err := a.GetWorkerJoinReadiness(ctx, openapiv1.GetWorkerJoinReadinessRequestObject{})
	require.NoError(t, err)
	readiness, ok := resp.(openapiv1.GetWorkerJoinReadiness200JSONResponse)
	require.True(t, ok)
	reasons := map[bool]*openapiv1.WorkerAccessReason{}
	for _, entry := range readiness.Coordinators {
		reasons[entry.Report != nil] = entry.ErrorReason
	}
	assert.Nil(t, reasons[true])
	assert.Equal(t, new(openapiv1.WorkerAccessReasonRequireTls), reasons[false])
}

// An invite pasted in place of an invite ID answers 400 and reaches neither
// the coordinator nor the audit, over a real gRPC client.
func TestDeleteWorkerInviteRefusesInviteOverGRPC(t *testing.T) {
	t.Parallel()

	c := &rewordingCoordinator{}
	registry, err := coordinator.NewStaticRegistry([]string{serveRewording(t, c)})
	require.NoError(t, err)
	client := coordinator.New(registry, coordinator.DefaultConfig())
	t.Cleanup(func() { _ = client.Cleanup(context.Background()) })
	a, store := rolesAPI(client)

	_, err = a.DeleteWorkerInvite(userContext(auth.RoleAdmin), openapiv1.DeleteWorkerInviteRequestObject{InviteId: testInvite})
	assert.Equal(t, http.StatusBadRequest, httpStatus(t, err))
	assert.Zero(t, c.calls.Load())
	assert.Empty(t, store.entries)
}

// A coordinator under coordinator.require_tls refuses the server's h2c
// call, and the REST answer keeps the reason through the client's rewrite
// of the refusal message.
func TestWorkerAccessRequireTLSReason(t *testing.T) {
	t.Parallel()

	addr := serveRewording(t, &rewordingCoordinator{}, grpc.ChainUnaryInterceptor(coordinator.RequireTLSUnaryInterceptor()))
	registry, err := coordinator.NewStaticRegistry([]string{addr})
	require.NoError(t, err)
	client := coordinator.New(registry, coordinator.DefaultConfig())
	t.Cleanup(func() { _ = client.Cleanup(context.Background()) })
	a, _ := rolesAPI(client)

	_, err = a.CreateWorkerInvite(userContext(auth.RoleAdmin), openapiv1.CreateWorkerInviteRequestObject{})
	var apiErr *Error
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusServiceUnavailable, apiErr.HTTPStatus)
	assert.Equal(t, map[string]any{"reason": "require_tls"}, apiErr.Details)
}

// A coordinator of an earlier release refuses under coordinator.require_tls
// with the message alone, and the REST answer still names require_tls.
func TestWorkerAccessRequireTLSReasonFromOlder(t *testing.T) {
	t.Parallel()

	older := grpc.ChainUnaryInterceptor(func(context.Context, any, *grpc.UnaryServerInfo, grpc.UnaryHandler) (any, error) {
		return nil, status.Error(codes.Unauthenticated, requireTLSRefusal)
	})
	registry, err := coordinator.NewStaticRegistry([]string{serveRewording(t, &rewordingCoordinator{}, older)})
	require.NoError(t, err)
	client := coordinator.New(registry, coordinator.DefaultConfig())
	t.Cleanup(func() { _ = client.Cleanup(context.Background()) })
	a, _ := rolesAPI(client)

	_, err = a.CreateWorkerInvite(userContext(auth.RoleAdmin), openapiv1.CreateWorkerInviteRequestObject{})
	var apiErr *Error
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, map[string]any{"reason": "require_tls"}, apiErr.Details)
}
