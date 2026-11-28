// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"net"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"

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
}

func (c *rewordingCoordinator) CreateInvite(context.Context, *coordinatorv1.CreateInviteRequest) (*coordinatorv1.CreateInviteResponse, error) {
	return nil, withReason(codes.FailedPrecondition, "own_certificates", "a reworded refusal")
}

func (c *rewordingCoordinator) ListInvites(context.Context, *coordinatorv1.ListInvitesRequest) (*coordinatorv1.ListInvitesResponse, error) {
	return &coordinatorv1.ListInvitesResponse{}, nil
}

func (c *rewordingCoordinator) GetJoinReadiness(context.Context, *coordinatorv1.GetJoinReadinessRequest) (*coordinatorv1.GetJoinReadinessResponse, error) {
	if c.ready {
		return &coordinatorv1.GetJoinReadinessResponse{BuiltinTls: true}, nil
	}
	return nil, withReason(codes.Unauthenticated, "require_tls", "another reworded refusal")
}

// serveRewording serves c and returns its address.
func serveRewording(t *testing.T, c *rewordingCoordinator) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer()
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
