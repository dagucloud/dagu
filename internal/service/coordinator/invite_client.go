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

// InviteClient creates invites on the coordinators.
type InviteClient interface {
	// CreateInvite creates an invite on one coordinator in the registry,
	// trying the next one when a coordinator fails the call. Only its
	// response holds the invite. It fails when every coordinator fails, or
	// when none answers within 10 seconds.
	CreateInvite(ctx context.Context, req *coordinatorv1.CreateInviteRequest) (*coordinatorv1.CreateInviteResponse, error)
	// ListInvites lists the invites, oldest first, without their secrets,
	// from one coordinator in the registry. It tries the next one only when
	// a coordinator cannot be reached or predates the call, and returns a
	// refusal with its gRPC status. The coordinators share the records.
	ListInvites(ctx context.Context) ([]*coordinatorv1.InviteRecord, error)
	// DeleteInvite deletes the invite with the ID id through one
	// coordinator, as ListInvites reaches one.
	DeleteInvite(ctx context.Context, id string) error
}

var _ InviteClient = (*clientImpl)(nil)

func (cli *clientImpl) CreateInvite(ctx context.Context, req *coordinatorv1.CreateInviteRequest) (*coordinatorv1.CreateInviteResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, createInviteTimeout)
	defer cancel()
	members, err := cli.getCoordinatorMembers(ctx)
	if err != nil {
		return nil, err
	}
	var resp *coordinatorv1.CreateInviteResponse
	err = cli.attemptCall(ctx, members, func(ctx context.Context, _ serviceregistry.HostInfo, client *client) error {
		var callErr error
		resp, callErr = client.client.CreateInvite(ctx, req)
		return callErr
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
// within the bound of a create call. It tries the next coordinator only
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
