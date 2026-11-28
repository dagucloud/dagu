// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator_test

import (
	"context"
	"net"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
)

// inviteServer creates invites, or fails every create call with err, and
// counts the create calls. With hold, a create call answers only when its
// context ends, as a coordinator whose answer is lost.
type inviteServer struct {
	coordinatorv1.UnimplementedCoordinatorServiceServer
	err   error
	hold  bool
	calls atomic.Int32
}

func (s *inviteServer) CreateInvite(ctx context.Context, req *coordinatorv1.CreateInviteRequest) (*coordinatorv1.CreateInviteResponse, error) {
	s.calls.Add(1)
	if s.hold {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if s.err != nil {
		return nil, s.err
	}
	return &coordinatorv1.CreateInviteResponse{Invite: "dagu1.pin.secret", Record: &coordinatorv1.InviteRecord{Address: req.Address}}, nil
}

func (s *inviteServer) ListInvites(context.Context, *coordinatorv1.ListInvitesRequest) (*coordinatorv1.ListInvitesResponse, error) {
	return &coordinatorv1.ListInvitesResponse{Invites: []*coordinatorv1.InviteRecord{{Id: "invite-1"}}}, nil
}

func (s *inviteServer) DeleteInvite(context.Context, *coordinatorv1.DeleteInviteRequest) (*coordinatorv1.DeleteInviteResponse, error) {
	return nil, status.Error(codes.NotFound, "no invite has this ID")
}

// memberKind starts a coordinator, or a stand-in for one, on a listener and
// returns its registry entry.
type memberKind func(t *testing.T, listener net.Listener) serviceregistry.HostInfo

// serving serves service.
func serving(service coordinatorv1.CoordinatorServiceServer) memberKind {
	return func(t *testing.T, listener net.Listener) serviceregistry.HostInfo {
		server := grpc.NewServer()
		coordinatorv1.RegisterCoordinatorServiceServer(server, service)
		go func() { _ = server.Serve(listener) }()
		t.Cleanup(server.Stop)
		return listenerMember(listener)
	}
}

// closed listens nowhere, so a call cannot reach it.
func closed(t *testing.T, listener net.Listener) serviceregistry.HostInfo {
	require.NoError(t, listener.Close())
	return listenerMember(listener)
}

// stalled accepts connections and never answers, so a call waits for a
// connection that never becomes ready.
func stalled(t *testing.T, listener net.Listener) serviceregistry.HostInfo {
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { _ = conn.Close() })
		}
	}()
	return listenerMember(listener)
}

func listenerMember(listener net.Listener) serviceregistry.HostInfo {
	host, port := parseHostPort(listener.Addr().String())
	return serviceregistry.HostInfo{ID: listener.Addr().String(), Host: host, Port: port}
}

// inviteClientOf returns a client whose registry lists one member of each
// kind, which the client tries in the order of kinds.
func inviteClientOf(t *testing.T, kinds ...memberKind) coordinator.InviteClient {
	t.Helper()
	listeners := make([]net.Listener, len(kinds))
	for i := range listeners {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		listeners[i] = listener
	}
	// The client tries the coordinators in the order of their addresses.
	sort.Slice(listeners, func(i, j int) bool { return listeners[i].Addr().String() < listeners[j].Addr().String() })
	members := make([]serviceregistry.HostInfo, len(kinds))
	for i, kind := range kinds {
		members[i] = kind(t, listeners[i])
	}
	cli := coordinator.New(&mockServiceMonitor{members: members}, coordinator.DefaultConfig())
	t.Cleanup(func() { _ = cli.Cleanup(context.Background()) })
	client, ok := cli.(coordinator.InviteClient)
	require.True(t, ok)
	return client
}

// bothOrders runs check with a client whose two coordinators are of the
// kinds a and b, in each order.
func bothOrders(t *testing.T, a, b memberKind, check func(t *testing.T, client coordinator.InviteClient)) {
	t.Helper()
	for name, kinds := range map[string][]memberKind{"first": {a, b}, "second": {b, a}} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			check(t, inviteClientOf(t, kinds...))
		})
	}
}

func TestClientCreateInvite(t *testing.T) {
	t.Parallel()

	bothOrders(t, closed, serving(&inviteServer{}), func(t *testing.T, client coordinator.InviteClient) {
		resp, err := client.CreateInvite(t.Context(), &coordinatorv1.CreateInviteRequest{Address: "192.168.1.20:50055"})
		require.NoError(t, err)
		assert.Equal(t, "dagu1.pin.secret", resp.Invite)
		assert.Equal(t, "192.168.1.20:50055", resp.Record.Address)
	})
}

// A refusal from every coordinator reaches the caller with its status.
func TestClientCreateInviteRefused(t *testing.T) {
	t.Parallel()

	refusal := status.Error(codes.FailedPrecondition, "invites need the built-in CA")
	_, err := inviteClientOf(t, serving(&inviteServer{err: refusal})).CreateInvite(t.Context(), &coordinatorv1.CreateInviteRequest{})
	assert.Equal(t, codes.FailedPrecondition, status.Code(err), "%v", err)
}

// A refusal is the answer whether the client tries an unreachable
// coordinator before it or not.
func TestClientCreateInviteRefusalBeatsUnreachable(t *testing.T) {
	t.Parallel()

	refusing := serving(&inviteServer{err: status.Error(codes.FailedPrecondition, "invites need the built-in CA")})
	bothOrders(t, refusing, closed, func(t *testing.T, client coordinator.InviteClient) {
		_, err := client.CreateInvite(t.Context(), &coordinatorv1.CreateInviteRequest{})
		assert.Equal(t, codes.FailedPrecondition, status.Code(err), "%v", err)
	})
}

// A coordinator that got the create request may have stored the invite, so
// the client does not send it to another one, however its answer was lost.
func TestClientCreateInviteNotResent(t *testing.T) {
	t.Parallel()

	for name, server := range map[string]*inviteServer{
		"no answer":   {hold: true},
		"unavailable": {err: status.Error(codes.Unavailable, "the store is busy")},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			next := &inviteServer{}
			client := inviteClientOf(t, serving(server), serving(next))
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			_, err := client.CreateInvite(ctx, &coordinatorv1.CreateInviteRequest{})
			assert.Error(t, err)
			assert.Equal(t, int32(1), server.calls.Load())
			assert.Zero(t, next.calls.Load())
		})
	}
}

// A coordinator whose connection never becomes ready leaves time for the
// next one, for each call.
func TestClientInviteSkipsStalledCoordinator(t *testing.T) {
	t.Parallel()

	client := inviteClientOf(t, stalled, serving(&inviteServer{}))
	for range 3 {
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		_, err := client.CreateInvite(ctx, &coordinatorv1.CreateInviteRequest{})
		assert.NoError(t, err)
		_, err = client.ListInvites(ctx)
		assert.NoError(t, err)
		cancel()
	}
}

// With no coordinator that settles the call, the error does not depend on
// the order the client tries them in.
func TestClientInviteFailurePrecedence(t *testing.T) {
	t.Parallel()

	older := serving(&coordinatorv1.UnimplementedCoordinatorServiceServer{})
	bothOrders(t, older, closed, func(t *testing.T, client coordinator.InviteClient) {
		_, err := client.CreateInvite(t.Context(), &coordinatorv1.CreateInviteRequest{})
		assert.Equal(t, codes.Unimplemented, status.Code(err), "%v", err)
		_, err = client.ListInvites(t.Context())
		assert.Equal(t, codes.Unimplemented, status.Code(err), "%v", err)
	})
}

func TestClientListInvites(t *testing.T) {
	t.Parallel()

	older := serving(&coordinatorv1.UnimplementedCoordinatorServiceServer{})
	invites, err := inviteClientOf(t, closed, older, serving(&inviteServer{})).ListInvites(t.Context())
	require.NoError(t, err)
	require.Len(t, invites, 1)
	assert.Equal(t, "invite-1", invites[0].Id)
}

// A refusal reaches the caller with its status, whether the client tries an
// unreachable coordinator before it or not.
func TestClientDeleteInviteRefused(t *testing.T) {
	t.Parallel()

	bothOrders(t, closed, serving(&inviteServer{}), func(t *testing.T, client coordinator.InviteClient) {
		err := client.DeleteInvite(t.Context(), "missing")
		assert.Equal(t, codes.NotFound, status.Code(err), "%v", err)
	})
}

func TestClientInviteNoCoordinators(t *testing.T) {
	t.Parallel()

	_, err := inviteClientOf(t).ListInvites(t.Context())
	assert.Error(t, err)
}

// sharedInvites holds the invite IDs that coordinators share.
type sharedInvites struct {
	mu  sync.Mutex
	ids map[string]bool
}

func newSharedInvites(ids ...string) *sharedInvites {
	shared := &sharedInvites{ids: map[string]bool{}}
	for _, id := range ids {
		shared.ids[id] = true
	}
	return shared
}

func (s *sharedInvites) has(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ids[id]
}

// sharingCoordinator lists and deletes the shared invites. With
// loseAnswer, it deletes and answers Unavailable, as when the answer is
// lost. With failFirst, its first delete answers Unavailable and deletes
// nothing, as when a call times out before it runs.
type sharingCoordinator struct {
	coordinatorv1.UnimplementedCoordinatorServiceServer
	shared     *sharedInvites
	loseAnswer bool
	failFirst  bool
	deletes    atomic.Int32
}

func (c *sharingCoordinator) ListInvites(context.Context, *coordinatorv1.ListInvitesRequest) (*coordinatorv1.ListInvitesResponse, error) {
	c.shared.mu.Lock()
	defer c.shared.mu.Unlock()
	resp := &coordinatorv1.ListInvitesResponse{}
	for id := range c.shared.ids {
		resp.Invites = append(resp.Invites, &coordinatorv1.InviteRecord{Id: id})
	}
	return resp, nil
}

func (c *sharingCoordinator) DeleteInvite(_ context.Context, req *coordinatorv1.DeleteInviteRequest) (*coordinatorv1.DeleteInviteResponse, error) {
	if c.deletes.Add(1) == 1 && c.failFirst {
		return nil, status.Error(codes.Unavailable, "timed out before the delete ran")
	}
	c.shared.mu.Lock()
	defer c.shared.mu.Unlock()
	if !c.shared.ids[req.Id] {
		return nil, status.Error(codes.NotFound, "no invite has this ID")
	}
	delete(c.shared.ids, req.Id)
	if c.loseAnswer {
		return nil, status.Error(codes.Unavailable, "connection reset")
	}
	return &coordinatorv1.DeleteInviteResponse{}, nil
}

// A delete whose answer was lost succeeds once the invites show it gone,
// with another coordinator to ask or none. A call that ran no delete does
// not count as one: an ID that no invite has stays NotFound, and an invite
// still listed fails the delete.
func TestClientDeleteInviteOutcome(t *testing.T) {
	t.Parallel()

	t.Run("lost answer, then another coordinator", func(t *testing.T) {
		t.Parallel()
		shared := newSharedInvites("invite-1")
		lost := &sharingCoordinator{shared: shared, loseAnswer: true}
		err := inviteClientOf(t, serving(lost), serving(&sharingCoordinator{shared: shared})).DeleteInvite(t.Context(), "invite-1")
		require.NoError(t, err)
		assert.False(t, shared.has("invite-1"))
	})
	t.Run("lost answer, one coordinator", func(t *testing.T) {
		t.Parallel()
		shared := newSharedInvites("invite-1")
		err := inviteClientOf(t, serving(&sharingCoordinator{shared: shared, loseAnswer: true})).DeleteInvite(t.Context(), "invite-1")
		require.NoError(t, err)
		assert.False(t, shared.has("invite-1"))
	})
	t.Run("no such invite after a call that ran no delete", func(t *testing.T) {
		t.Parallel()
		shared := newSharedInvites()
		failing := &sharingCoordinator{shared: shared, failFirst: true}
		err := inviteClientOf(t, serving(failing), serving(&sharingCoordinator{shared: shared})).DeleteInvite(t.Context(), "missing")
		assert.Equal(t, codes.NotFound, status.Code(err), "%v", err)
	})
	t.Run("one coordinator that ran no delete", func(t *testing.T) {
		t.Parallel()
		shared := newSharedInvites("invite-1")
		err := inviteClientOf(t, serving(&sharingCoordinator{shared: shared, failFirst: true})).DeleteInvite(t.Context(), "invite-1")
		assert.Equal(t, codes.Unavailable, status.Code(err), "%v", err)
		assert.True(t, shared.has("invite-1"))
	})
}

// invitesServer lists the invites with the IDs ids.
type invitesServer struct {
	coordinatorv1.UnimplementedCoordinatorServiceServer
	ids []string
}

func (s invitesServer) ListInvites(context.Context, *coordinatorv1.ListInvitesRequest) (*coordinatorv1.ListInvitesResponse, error) {
	resp := &coordinatorv1.ListInvitesResponse{}
	for _, id := range s.ids {
		resp.Invites = append(resp.Invites, &coordinatorv1.InviteRecord{Id: id})
	}
	return resp, nil
}

// The list is the first answering coordinator's, even when another one
// would list other invites.
func TestClientListInvitesFirstAnswer(t *testing.T) {
	t.Parallel()

	client := inviteClientOf(t, closed, serving(invitesServer{}), serving(invitesServer{ids: []string{"invite-1"}}))
	invites, err := client.ListInvites(t.Context())
	require.NoError(t, err)
	assert.Empty(t, invites)
}
