// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dagucloud/dagu/v2/internal/cmn/backoff"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
)

// createInviteTimeout bounds a create call, so a coordinator that accepts the
// connection but does not answer does not hold the caller.
const createInviteTimeout = 10 * time.Second

// InviteClient creates, lists, and deletes invites. Each call reaches one
// coordinator in the registry, since the coordinators share the invite
// records. It tries the next coordinator only when one cannot be reached,
// does not answer, or predates the call, and returns a refusal with its
// gRPC status. A call fails when no coordinator answers within 10 seconds.
type InviteClient interface {
	// CreateInvite creates an invite on one coordinator in the registry.
	// Only its response holds the invite.
	CreateInvite(ctx context.Context, req *coordinatorv1.CreateInviteRequest) (*coordinatorv1.CreateInviteResponse, error)
	// ListInvites lists the invites, oldest first, without their secrets.
	ListInvites(ctx context.Context) ([]*coordinatorv1.InviteRecord, error)
	// DeleteInvite deletes the invite with the ID id.
	DeleteInvite(ctx context.Context, id string) error
}

var _ InviteClient = (*clientImpl)(nil)

func (cli *clientImpl) CreateInvite(ctx context.Context, req *coordinatorv1.CreateInviteRequest) (*coordinatorv1.CreateInviteResponse, error) {
	var resp *coordinatorv1.CreateInviteResponse
	err := cli.callInviteCoordinator(ctx, func(ctx context.Context, client coordinatorv1.CoordinatorServiceClient) (err error) {
		resp, err = client.CreateInvite(ctx, req)
		return err
	})
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (cli *clientImpl) ListInvites(ctx context.Context) ([]*coordinatorv1.InviteRecord, error) {
	var invites []*coordinatorv1.InviteRecord
	err := cli.callInviteCoordinator(ctx, func(ctx context.Context, client coordinatorv1.CoordinatorServiceClient) error {
		resp, err := client.ListInvites(ctx, &coordinatorv1.ListInvitesRequest{})
		if err != nil {
			return err
		}
		invites = resp.Invites
		return nil
	})
	return invites, err
}

func (cli *clientImpl) DeleteInvite(ctx context.Context, id string) error {
	return cli.callInviteCoordinator(ctx, func(ctx context.Context, client coordinatorv1.CoordinatorServiceClient) error {
		_, err := client.DeleteInvite(ctx, &coordinatorv1.DeleteInviteRequest{Id: id})
		return err
	})
}

// callInviteCoordinator makes call on one coordinator in the registry,
// within createInviteTimeout. It tries the next coordinator only
// when one cannot be reached or predates the call. The coordinators share
// the records, so a refusal is the answer and returns as is.
func (cli *clientImpl) callInviteCoordinator(ctx context.Context, call func(context.Context, coordinatorv1.CoordinatorServiceClient) error) error {
	ctx, cancel := context.WithTimeout(ctx, createInviteTimeout)
	defer cancel()
	members, err := cli.getCoordinatorMembers(ctx)
	if err != nil {
		return err
	}
	var refusal error
	err = cli.attemptCall(ctx, members, func(ctx context.Context, _ serviceregistry.HostInfo, client *client) error {
		err := call(ctx, client.client)
		switch status.Code(err) {
		case codes.OK, codes.Unavailable, codes.DeadlineExceeded, codes.Unimplemented:
			return err
		}
		refusal = err
		return backoff.PermanentError(err)
	})
	if refusal != nil {
		return refusal
	}
	return err
}
