// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"context"
	"errors"
	"fmt"

	"github.com/dagucloud/dagu/internal/cmn/secrets"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	secretpkg "github.com/dagucloud/dagu/internal/secret"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type SecretReferenceClient interface {
	ResolveSecretReference(ctx context.Context, owner exec.HostInfo, ref core.SecretRef, workspace string, checkOnly bool) (string, error)
}

type secretReferenceResolver struct {
	client    SecretReferenceClient
	workspace string
	owner     exec.HostInfo
}

func NewSecretReferenceResolver(client SecretReferenceClient, workspace string, owner exec.HostInfo) secrets.ReferenceResolver {
	if client == nil {
		return nil
	}
	return &secretReferenceResolver{
		client:    client,
		workspace: secretpkg.NormalizeWorkspace(workspace),
		owner:     owner,
	}
}

func (r *secretReferenceResolver) ResolveReference(ctx context.Context, ref core.SecretRef) (string, error) {
	return r.resolve(ctx, ref, false)
}

func (r *secretReferenceResolver) CheckReferenceAccessibility(ctx context.Context, ref core.SecretRef) error {
	_, err := r.resolve(ctx, ref, true)
	return err
}

func (r *secretReferenceResolver) resolve(ctx context.Context, ref core.SecretRef, checkOnly bool) (string, error) {
	return r.client.ResolveSecretReference(ctx, r.owner, ref, r.workspace, checkOnly)
}

func (cli *clientImpl) ResolveSecretReference(ctx context.Context, owner exec.HostInfo, ref core.SecretRef, workspace string, checkOnly bool) (string, error) {
	if !emptySecretReferenceOwner(owner) && !completeSecretReferenceOwner(owner) {
		return "", fmt.Errorf("secret reference owner coordinator endpoint is incomplete")
	}

	req := secretReferenceRequest(ref, workspace, checkOnly)
	if completeSecretReferenceOwner(owner) {
		return cli.resolveSecretReferenceTo(ctx, owner, req)
	}
	return cli.resolveSecretReference(ctx, req)
}

func (cli *clientImpl) resolveSecretReference(ctx context.Context, req *coordinatorv1.ResolveSecretReferenceRequest) (string, error) {
	members, err := cli.getCoordinatorMembers(ctx)
	if err != nil {
		return "", err
	}

	var resp *coordinatorv1.ResolveSecretReferenceResponse
	err = cli.attemptCall(ctx, members, func(ctx context.Context, member exec.HostInfo, client *client) error {
		var callErr error
		resp, callErr = resolveSecretReferenceRPC(ctx, member.ID, client, req)
		return callErr
	})
	if err != nil {
		return "", err
	}
	return resp.GetValue(), nil
}

func (cli *clientImpl) resolveSecretReferenceTo(ctx context.Context, owner exec.HostInfo, req *coordinatorv1.ResolveSecretReferenceRequest) (string, error) {
	var resp *coordinatorv1.ResolveSecretReferenceResponse
	err := cli.callMemberWithTimeout(ctx, owner, func(ctx context.Context, client *client) error {
		var callErr error
		resp, callErr = resolveSecretReferenceRPC(ctx, owner.ID, client, req)
		return callErr
	})
	if err != nil {
		return "", err
	}
	return resp.GetValue(), nil
}

func emptySecretReferenceOwner(owner exec.HostInfo) bool {
	return owner.ID == "" && owner.Host == "" && owner.Port == 0
}

func resolveSecretReferenceRPC(ctx context.Context, coordinatorID string, client *client, req *coordinatorv1.ResolveSecretReferenceRequest) (*coordinatorv1.ResolveSecretReferenceResponse, error) {
	resp, err := client.client.ResolveSecretReference(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("resolve secret reference failed: %w", err)
	}
	if resp == nil {
		return nil, fmt.Errorf("coordinator %s returned empty secret reference response", coordinatorID)
	}
	return resp, nil
}

func completeSecretReferenceOwner(owner exec.HostInfo) bool {
	return owner.ID != "" && owner.Host != "" && owner.Port != 0
}

func secretReferenceRequest(ref core.SecretRef, workspace string, checkOnly bool) *coordinatorv1.ResolveSecretReferenceRequest {
	return &coordinatorv1.ResolveSecretReferenceRequest{
		Name:      ref.Name,
		Ref:       ref.Ref,
		Workspace: secretpkg.NormalizeWorkspace(workspace),
		CheckOnly: checkOnly,
	}
}

func (h *Handler) ResolveSecretReference(ctx context.Context, req *coordinatorv1.ResolveSecretReferenceRequest) (*coordinatorv1.ResolveSecretReferenceResponse, error) {
	if h.secretStore == nil {
		return nil, status.Error(codes.FailedPrecondition, "secret store is not configured")
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "secret reference request is required")
	}
	ref := core.SecretRef{
		Name: req.GetName(),
		Ref:  req.GetRef(),
	}
	if ref.Name == "" {
		return nil, status.Error(codes.InvalidArgument, "secret name is required")
	}
	if ref.Ref == "" {
		return nil, status.Error(codes.InvalidArgument, "secret ref is required")
	}
	if err := secretpkg.ValidateRef(ref.Ref); err != nil {
		return nil, secretReferenceRPCError(err)
	}

	resolver := secretpkg.NewReferenceResolver(h.secretStore, req.GetWorkspace())
	if req.GetCheckOnly() {
		if err := resolver.CheckReferenceAccessibility(ctx, ref); err != nil {
			return nil, secretReferenceRPCError(err)
		}
		return &coordinatorv1.ResolveSecretReferenceResponse{}, nil
	}

	value, err := resolver.ResolveReference(ctx, ref)
	if err != nil {
		return nil, secretReferenceRPCError(err)
	}
	return &coordinatorv1.ResolveSecretReferenceResponse{Value: value}, nil
}

func secretReferenceRPCError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, secretpkg.ErrInvalidRef), errors.Is(err, secretpkg.ErrInvalidWorkspace):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, secretpkg.ErrNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, secretpkg.ErrDisabled), errors.Is(err, secretpkg.ErrNoValue), errors.Is(err, secretpkg.ErrUnsupportedProvider):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, err.Error())
	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, err.Error())
	default:
		return status.Error(codes.Internal, err.Error())
	}
}
