// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator_test

import (
	"context"
	"fmt"
	"net"
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

// inviteRecords lists one invite and deletes none.
type inviteRecords struct {
	coordinatorv1.UnimplementedCoordinatorServiceServer
}

func (inviteRecords) ListInvites(context.Context, *coordinatorv1.ListInvitesRequest) (*coordinatorv1.ListInvitesResponse, error) {
	return &coordinatorv1.ListInvitesResponse{Invites: []*coordinatorv1.InviteRecord{{Id: "invite-1"}}}, nil
}

func (inviteRecords) DeleteInvite(context.Context, *coordinatorv1.DeleteInviteRequest) (*coordinatorv1.DeleteInviteResponse, error) {
	return nil, status.Error(codes.NotFound, "no invite has this ID")
}

// inviteClient returns a client whose registry lists a coordinator that
// does not answer, one that predates the invite calls, and one that serves
// them, in a random order.
func inviteClient(t *testing.T) coordinator.InviteClient {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	host, closedPort := parseHostPort(listener.Addr().String())
	require.NoError(t, listener.Close())
	members := []serviceregistry.HostInfo{{ID: "closed", Host: host, Port: closedPort}}
	for id, server := range map[string]coordinatorv1.CoordinatorServiceServer{
		"older":   &coordinatorv1.UnimplementedCoordinatorServiceServer{},
		"current": inviteRecords{},
	} {
		grpcServer, addr := startMockServer(t, server)
		t.Cleanup(grpcServer.Stop)
		host, port := parseHostPort(addr)
		members = append(members, serviceregistry.HostInfo{ID: id, Host: host, Port: port})
	}
	client, ok := coordinator.New(&mockServiceMonitor{members: members}, coordinator.DefaultConfig()).(coordinator.InviteClient)
	require.True(t, ok)
	return client
}

func TestClientListInvites(t *testing.T) {
	t.Parallel()

	invites, err := inviteClient(t).ListInvites(t.Context())
	require.NoError(t, err)
	require.Len(t, invites, 1)
	assert.Equal(t, "invite-1", invites[0].Id)
}

// A refusal reaches the caller with its status, whichever coordinator the
// client asks first.
func TestClientDeleteInviteRefused(t *testing.T) {
	t.Parallel()

	err := inviteClient(t).DeleteInvite(t.Context(), "missing")
	assert.Equal(t, codes.NotFound, status.Code(err), "%v", err)
}
