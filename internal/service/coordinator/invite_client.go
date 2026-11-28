// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"context"
	"time"

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
