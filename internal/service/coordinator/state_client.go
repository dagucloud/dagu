// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/dagstate"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var _ StateClient = (*clientImpl)(nil)
var _ dagstate.Store = (*stateStoreClient)(nil)

func (cli *clientImpl) GetState(ctx context.Context, req *coordinatorv1.GetStateRequest) (*coordinatorv1.GetStateResponse, error) {
	var resp *coordinatorv1.GetStateResponse
	err := cli.callPinnedStateCoordinator(ctx, stateRefRoutingKey(req.GetRef()), func(ctx context.Context, _ exec.HostInfo, client *client) error {
		var callErr error
		resp, callErr = client.client.GetState(ctx, req)
		return callErr
	})
	return resp, err
}

func (cli *clientImpl) PutState(ctx context.Context, req *coordinatorv1.PutStateRequest) (*coordinatorv1.PutStateResponse, error) {
	var resp *coordinatorv1.PutStateResponse
	err := cli.callPinnedStateCoordinator(ctx, stateRefRoutingKey(req.GetRef()), func(ctx context.Context, _ exec.HostInfo, client *client) error {
		var callErr error
		resp, callErr = client.client.PutState(ctx, req)
		return callErr
	})
	return resp, err
}

func (cli *clientImpl) DeleteState(ctx context.Context, req *coordinatorv1.DeleteStateRequest) (*coordinatorv1.DeleteStateResponse, error) {
	var resp *coordinatorv1.DeleteStateResponse
	err := cli.callPinnedStateCoordinator(ctx, stateRefRoutingKey(req.GetRef()), func(ctx context.Context, _ exec.HostInfo, client *client) error {
		var callErr error
		resp, callErr = client.client.DeleteState(ctx, req)
		return callErr
	})
	return resp, err
}

func (cli *clientImpl) ListState(ctx context.Context, req *coordinatorv1.ListStateRequest) (*coordinatorv1.ListStateResponse, error) {
	var resp *coordinatorv1.ListStateResponse
	err := cli.callPinnedStateCoordinator(ctx, stateListRoutingKey(req), func(ctx context.Context, _ exec.HostInfo, client *client) error {
		var callErr error
		resp, callErr = client.client.ListState(ctx, req)
		return callErr
	})
	return resp, err
}

func stateRefRoutingKey(ref *coordinatorv1.StateRef) string {
	if ref == nil {
		return "\x00"
	}
	return ref.GetScope() + "\x00" + ref.GetNamespace()
}

func stateListRoutingKey(req *coordinatorv1.ListStateRequest) string {
	if req == nil {
		return "\x00"
	}
	return req.GetScope() + "\x00" + req.GetNamespace()
}

type stateStoreClient struct {
	client StateClient
}

// NewStateStoreClient adapts coordinator state RPCs to the dagstate store interface.
func NewStateStoreClient(client StateClient) dagstate.Store {
	if client == nil {
		return nil
	}
	return &stateStoreClient{client: client}
}

func (s *stateStoreClient) Get(ctx context.Context, ref dagstate.Ref) (*dagstate.Entry, error) {
	if err := ref.Validate(); err != nil {
		return nil, err
	}
	resp, err := s.client.GetState(ctx, &coordinatorv1.GetStateRequest{Ref: stateRefToProto(ref)})
	if err != nil {
		return nil, stateClientError(err)
	}
	if resp == nil || !resp.GetFound() {
		return nil, dagstate.ErrNotFound
	}
	return stateEntryFromProto(resp.GetEntry())
}

func (s *stateStoreClient) Put(ctx context.Context, ref dagstate.Ref, value json.RawMessage, opts dagstate.PutOptions) (*dagstate.Entry, error) {
	if err := ref.Validate(); err != nil {
		return nil, err
	}
	normalized, err := dagstate.NormalizeValue(value)
	if err != nil {
		return nil, err
	}
	req := &coordinatorv1.PutStateRequest{
		Ref:        stateRefToProto(ref),
		Value:      append([]byte(nil), normalized...),
		CreateOnly: opts.CreateOnly,
		UpdatedBy:  stateUpdateSourceToProto(opts.UpdatedBy),
	}
	if opts.ExpectedVersion != nil {
		req.HasExpectedVersion = true
		req.ExpectedVersion = *opts.ExpectedVersion
	}
	resp, err := s.client.PutState(ctx, req)
	if err != nil {
		return nil, stateClientError(err)
	}
	return stateEntryFromProto(resp.GetEntry())
}

func (s *stateStoreClient) Delete(ctx context.Context, ref dagstate.Ref) (bool, error) {
	if err := ref.Validate(); err != nil {
		return false, err
	}
	resp, err := s.client.DeleteState(ctx, &coordinatorv1.DeleteStateRequest{Ref: stateRefToProto(ref)})
	if err != nil {
		return false, stateClientError(err)
	}
	return resp.GetDeleted(), nil
}

func (s *stateStoreClient) List(ctx context.Context, opts dagstate.ListOptions) ([]*dagstate.Entry, error) {
	if err := opts.Validate(); err != nil {
		return nil, err
	}
	limit := int32(opts.Limit) //nolint:gosec // ListOptions.Validate bounds-checks limit for the proto field.
	resp, err := s.client.ListState(ctx, &coordinatorv1.ListStateRequest{
		Scope:     string(opts.Scope),
		Namespace: opts.Namespace,
		KeyPrefix: opts.KeyPrefix,
		Limit:     limit,
	})
	if err != nil {
		return nil, stateClientError(err)
	}
	entries := make([]*dagstate.Entry, 0, len(resp.GetEntries()))
	for _, item := range resp.GetEntries() {
		entry, err := stateEntryFromProto(item)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func stateEntryFromProto(entry *coordinatorv1.StateEntry) (*dagstate.Entry, error) {
	if entry == nil {
		return nil, dagstate.ErrNotFound
	}
	ref, err := stateRefFromProto(entry.GetRef())
	if err != nil {
		return nil, err
	}
	return &dagstate.Entry{
		Ref:       ref,
		Value:     append(json.RawMessage(nil), entry.GetValue()...),
		Version:   entry.GetVersion(),
		Hash:      entry.GetHash(),
		CreatedAt: time.Unix(0, entry.GetCreatedAt()).UTC(),
		UpdatedAt: time.Unix(0, entry.GetUpdatedAt()).UTC(),
		UpdatedBy: stateUpdateSourceFromProto(entry.GetUpdatedBy()),
	}, nil
}

func stateClientError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, dagstate.ErrNotFound) || errors.Is(err, dagstate.ErrConflict) || errors.Is(err, dagstate.ErrInvalidRef) || errors.Is(err, dagstate.ErrInvalidValue) || errors.Is(err, dagstate.ErrValueTooLarge) {
		return err
	}
	st, ok := status.FromError(err)
	if !ok {
		return err
	}
	switch st.Code() { //nolint:exhaustive // State RPCs only translate domain-specific status codes.
	case codes.NotFound:
		return dagstate.ErrNotFound
	case codes.Aborted:
		return dagstate.ErrConflict
	case codes.InvalidArgument:
		return stateInvalidArgumentError(st.Message())
	default:
		return err
	}
}

func stateInvalidArgumentError(message string) error {
	switch {
	case strings.Contains(message, dagstate.ErrInvalidRef.Error()):
		return dagstate.ErrInvalidRef
	case strings.Contains(message, dagstate.ErrValueTooLarge.Error()):
		return dagstate.ErrValueTooLarge
	case strings.Contains(message, dagstate.ErrInvalidValue.Error()):
		return dagstate.ErrInvalidValue
	default:
		return dagstate.ErrInvalidValue
	}
}
