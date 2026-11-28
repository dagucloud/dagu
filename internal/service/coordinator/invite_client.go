// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"context"
	"slices"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
)

// missingInviteMessage refuses a delete of an ID that no invite has. It
// does not repeat the ID, which may be an invite pasted in its place.
const missingInviteMessage = "no invite has this ID: list the invites to find their IDs"

// inviteCallTimeout bounds an invite call across the coordinators, so a
// coordinator that accepts the connection but does not answer does not hold
// the caller.
const inviteCallTimeout = 10 * time.Second

// failureRanks orders the errors of coordinators that did not settle a
// call, so the error returned does not depend on the order they were tried
// in. A coordinator that predates the call says the most about what to do.
var failureRanks = map[codes.Code]int{
	codes.Unavailable:      1,
	codes.DeadlineExceeded: 2,
	codes.Unimplemented:    3,
}

// InviteClient creates, lists, and deletes invites. Each call reaches one
// coordinator in the registry, since the coordinators share the invite
// records. It tries the coordinators in a fixed order and moves to the next
// only when one cannot be reached, does not answer, or predates the call. A
// create moves on after a coordinator that does not answer only when the
// request cannot have reached it, so no invite is created twice. A refusal
// returns with its gRPC status. When no coordinator settles the call, the
// error is the one of the coordinator that predates it, else of one that
// did not answer in time, else of one that cannot be reached. A call fails
// when no coordinator answers within 10 seconds.
type InviteClient interface {
	// CreateInvite creates an invite on one coordinator in the registry.
	// Only its response holds the invite.
	CreateInvite(ctx context.Context, req *coordinatorv1.CreateInviteRequest) (*coordinatorv1.CreateInviteResponse, error)
	// ListInvites lists the invites, oldest first, without their secrets,
	// as the first coordinator that answers reads them. The coordinators
	// share the records, so that is the whole list.
	ListInvites(ctx context.Context) ([]*coordinatorv1.InviteRecord, error)
	// DeleteInvite deletes the invite with the ID id. It fails with
	// NotFound only when no invite has the ID before the delete, and
	// succeeds when the invite is gone afterwards, also when the answer of
	// the coordinator that deleted it was lost during this call.
	DeleteInvite(ctx context.Context, id string) error
}

var _ InviteClient = (*clientImpl)(nil)

// inviteCall is one call to a coordinator, with opts added to its options.
type inviteCall func(ctx context.Context, client coordinatorv1.CoordinatorServiceClient, opts ...grpc.CallOption) error

func (cli *clientImpl) CreateInvite(ctx context.Context, req *coordinatorv1.CreateInviteRequest) (*coordinatorv1.CreateInviteResponse, error) {
	var resp *coordinatorv1.CreateInviteResponse
	err := cli.callInviteCoordinator(ctx, false, func(ctx context.Context, client coordinatorv1.CoordinatorServiceClient, opts ...grpc.CallOption) (err error) {
		resp, err = client.CreateInvite(ctx, req, opts...)
		return err
	})
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (cli *clientImpl) ListInvites(ctx context.Context) ([]*coordinatorv1.InviteRecord, error) {
	var invites []*coordinatorv1.InviteRecord
	err := cli.callInviteCoordinator(ctx, true, func(ctx context.Context, client coordinatorv1.CoordinatorServiceClient, opts ...grpc.CallOption) error {
		resp, err := client.ListInvites(ctx, &coordinatorv1.ListInvitesRequest{}, opts...)
		if err != nil {
			return err
		}
		invites = resp.Invites
		return nil
	})
	return invites, err
}

func (cli *clientImpl) DeleteInvite(ctx context.Context, id string) error {
	// Reading the invites before the delete tells an ID that no invite has
	// from an invite that a delete with a lost answer removed. A failed
	// read does not stop the delete; it only leaves that unknown.
	listed, listErr := cli.inviteListed(ctx, id)
	if listErr == nil && !listed {
		return status.Error(codes.NotFound, missingInviteMessage)
	}
	err := cli.callInviteCoordinator(ctx, true, func(ctx context.Context, client coordinatorv1.CoordinatorServiceClient, opts ...grpc.CallOption) error {
		_, err := client.DeleteInvite(ctx, &coordinatorv1.DeleteInviteRequest{Id: id}, opts...)
		return err
	})
	if err == nil {
		return nil
	}
	if status.Code(err) != codes.NotFound {
		// The error kept is the worst of all coordinators, and another one
		// may have deleted the invite and lost its answer. The invites show
		// it.
		if listed, afterErr := cli.inviteListed(ctx, id); afterErr != nil || listed {
			return err
		}
	}
	if listErr != nil {
		// Without the first read, an invite gone now may never have been.
		return status.Error(codes.NotFound, missingInviteMessage)
	}
	// The invite was listed before the delete, so it is gone.
	return nil
}

// inviteListed reports whether the invites list one with the ID id.
func (cli *clientImpl) inviteListed(ctx context.Context, id string) (bool, error) {
	invites, err := cli.ListInvites(ctx)
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(invites, func(invite *coordinatorv1.InviteRecord) bool { return invite.Id == id }), nil
}

// callInviteCoordinator makes call on the coordinators of the registry in
// their sorted order, as InviteClient states, within inviteCallTimeout. A
// call that is not idempotent goes to the next coordinator after one that
// did not answer only when the request never got a connection.
func (cli *clientImpl) callInviteCoordinator(ctx context.Context, idempotent bool, call inviteCall) error {
	ctx, cancel := context.WithTimeout(ctx, inviteCallTimeout)
	defer cancel()
	members, err := cli.getCoordinatorMembers(ctx)
	if err != nil {
		return err
	}
	var failure error
	for i, member := range members {
		c, err := cli.getOrCreateDiscoveredClient(member)
		if err != nil {
			failure = worseFailure(failure, status.Errorf(codes.Unavailable, "connect to coordinator %s: %v", coordinatorAddress(member), err))
			continue
		}
		// Each coordinator gets a share of the time left, so one that does
		// not answer leaves time for the next.
		callCtx, cancelCall := cli.memberCallContext(ctx, len(members)-i)
		var p peer.Peer
		err = call(callCtx, c.client, grpc.Peer(&p))
		cancelCall()
		if !tryNextCoordinator(err, idempotent || p.Addr == nil) {
			return err
		}
		failure = worseFailure(failure, err)
	}
	return failure
}

// tryNextCoordinator reports whether a call that failed with err goes to
// the next coordinator. resendable reports whether it may be sent again
// after a coordinator that did not answer.
func tryNextCoordinator(err error, resendable bool) bool {
	// A coordinator that named why it refused answered the call, also with
	// Unavailable, such as when it asks to try again later.
	if RefusalReason(err) != "" {
		return false
	}
	code := status.Code(err)
	if code == codes.Unimplemented {
		return true
	}
	return resendable && (code == codes.Unavailable || code == codes.DeadlineExceeded)
}

// worseFailure returns whichever of current and next ranks higher in
// failureRanks, or current on a tie.
func worseFailure(current, next error) error {
	if current == nil || failureRanks[status.Code(next)] > failureRanks[status.Code(current)] {
		return next
	}
	return current
}
