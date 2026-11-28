// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
)

// inviteServer creates invites, or fails every create call with err.
type inviteServer struct {
	coordinatorv1.UnimplementedCoordinatorServiceServer
	err error
}

func (s *inviteServer) CreateInvite(_ context.Context, req *coordinatorv1.CreateInviteRequest) (*coordinatorv1.CreateInviteResponse, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &coordinatorv1.CreateInviteResponse{Invite: "dagu1.pin.secret", Record: &coordinatorv1.InviteRecord{Address: req.Address}}, nil
}

// The client tries coordinators in a random order, so the call succeeds
// whichever of the two it asks first.
func TestClientCreateInvite(t *testing.T) {
	t.Parallel()

	var members []serviceregistry.HostInfo
	for i, server := range []*inviteServer{{err: status.Error(codes.Unavailable, "down")}, {}} {
		grpcServer, addr := startMockServer(t, server)
		t.Cleanup(grpcServer.Stop)
		host, port := parseHostPort(addr)
		members = append(members, serviceregistry.HostInfo{ID: fmt.Sprintf("coordinator-%d", i), Host: host, Port: port})
	}
	client, ok := coordinator.New(&mockServiceMonitor{members: members}, coordinator.DefaultConfig()).(coordinator.InviteClient)
	require.True(t, ok)

	resp, err := client.CreateInvite(t.Context(), &coordinatorv1.CreateInviteRequest{Address: "192.168.1.20:50055"})
	require.NoError(t, err)
	assert.Equal(t, "dagu1.pin.secret", resp.Invite)
	assert.Equal(t, "192.168.1.20:50055", resp.Record.Address)
}

// A refusal from every coordinator reaches the caller with its status.
func TestClientCreateInviteRefused(t *testing.T) {
	t.Parallel()

	refusal := status.Error(codes.FailedPrecondition, "invites need the built-in CA")
	grpcServer, addr := startMockServer(t, &inviteServer{err: refusal})
	t.Cleanup(grpcServer.Stop)
	host, port := parseHostPort(addr)
	monitor := &mockServiceMonitor{members: []serviceregistry.HostInfo{{ID: "coordinator-1", Host: host, Port: port}}}
	client, ok := coordinator.New(monitor, coordinator.DefaultConfig()).(coordinator.InviteClient)
	require.True(t, ok)

	_, err := client.CreateInvite(t.Context(), &coordinatorv1.CreateInviteRequest{})
	assert.Equal(t, codes.FailedPrecondition, status.Code(err), "%v", err)
}
