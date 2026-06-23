// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/secrets"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	secretpkg "github.com/dagucloud/dagu/internal/secret"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type SecretReferenceClient interface {
	ResolveSecretReference(ctx context.Context, owner exec.HostInfo, ref core.SecretRef, workspace string, checkOnly bool, run SecretReferenceRun) (string, error)
}

type SecretReferenceRun struct {
	WorkerID   string
	AttemptKey string
	AttemptID  string
}

type secretReferenceResolver struct {
	client    SecretReferenceClient
	workspace string
	owner     exec.HostInfo
	run       SecretReferenceRun
}

func NewSecretReferenceResolver(client SecretReferenceClient, workspace string, owner exec.HostInfo, run SecretReferenceRun) secrets.ReferenceResolver {
	if client == nil {
		return nil
	}
	return &secretReferenceResolver{
		client:    client,
		workspace: secretpkg.NormalizeWorkspace(workspace),
		owner:     owner,
		run:       run,
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
	return r.client.ResolveSecretReference(ctx, r.owner, ref, r.workspace, checkOnly, r.run)
}

func (cli *clientImpl) ResolveSecretReference(ctx context.Context, owner exec.HostInfo, ref core.SecretRef, workspace string, checkOnly bool, run SecretReferenceRun) (string, error) {
	if !emptySecretReferenceOwner(owner) && !completeSecretReferenceOwner(owner) {
		return "", fmt.Errorf("secret reference owner coordinator endpoint is incomplete")
	}

	req := secretReferenceRequest(ref, workspace, checkOnly, run)
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

func secretReferenceRequest(ref core.SecretRef, workspace string, checkOnly bool, run SecretReferenceRun) *coordinatorv1.ResolveSecretReferenceRequest {
	return &coordinatorv1.ResolveSecretReferenceRequest{
		Name:       ref.Name,
		Ref:        ref.Ref,
		Workspace:  secretpkg.NormalizeWorkspace(workspace),
		CheckOnly:  checkOnly,
		WorkerId:   run.WorkerID,
		AttemptKey: run.AttemptKey,
		AttemptId:  run.AttemptID,
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
	if err := h.authorizeSecretReference(ctx, req, ref); err != nil {
		return nil, err
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

func (h *Handler) authorizeSecretReference(ctx context.Context, req *coordinatorv1.ResolveSecretReferenceRequest, ref core.SecretRef) error {
	if req.GetWorkerId() == "" {
		return status.Error(codes.InvalidArgument, "worker_id is required")
	}
	if req.GetAttemptKey() == "" {
		return status.Error(codes.InvalidArgument, "attempt_key is required")
	}
	if req.GetAttemptId() == "" {
		return status.Error(codes.InvalidArgument, "attempt_id is required")
	}
	if h.dagRunLeaseStore == nil {
		return status.Error(codes.FailedPrecondition, "dag-run lease store is not configured")
	}
	if h.dagRunStore == nil {
		return status.Error(codes.FailedPrecondition, "dag-run store is not configured")
	}

	lease, err := h.dagRunLeaseStore.Get(ctx, req.GetAttemptKey())
	if err != nil {
		if errors.Is(err, exec.ErrDAGRunLeaseNotFound) {
			return status.Error(codes.PermissionDenied, "secret reference access denied")
		}
		return status.Error(codes.Internal, err.Error())
	}
	if lease.WorkerID != req.GetWorkerId() || lease.AttemptID != req.GetAttemptId() {
		return status.Error(codes.PermissionDenied, "secret reference access denied")
	}
	if !lease.IsFresh(time.Now().UTC(), h.staleLeaseThreshold) {
		return status.Error(codes.PermissionDenied, "secret reference access denied")
	}

	dag, err := h.secretReferenceDAG(ctx, lease)
	if err != nil {
		return err
	}
	if secretpkg.NormalizeWorkspace(req.GetWorkspace()) != secretReferenceWorkspace(dag) {
		return status.Error(codes.PermissionDenied, "secret reference access denied")
	}
	if !secretReferenceDeclared(dag, ref) {
		return status.Error(codes.PermissionDenied, "secret reference access denied")
	}
	return nil
}

func (h *Handler) secretReferenceDAG(ctx context.Context, lease *exec.DAGRunLease) (*core.DAG, error) {
	if lease == nil {
		return nil, status.Error(codes.PermissionDenied, "secret reference access denied")
	}

	var (
		attempt exec.DAGRunAttempt
		err     error
	)
	if !lease.Root.Zero() && lease.Root != lease.DAGRun {
		attempt, err = h.dagRunStore.FindSubAttempt(ctx, lease.Root, lease.DAGRun.ID)
	} else {
		attempt, err = h.dagRunStore.FindAttempt(ctx, lease.DAGRun)
	}
	if err != nil {
		if errors.Is(err, exec.ErrDAGRunIDNotFound) || errors.Is(err, exec.ErrNoStatusData) || errors.Is(err, exec.ErrCorruptedStatusFile) {
			return nil, status.Error(codes.PermissionDenied, "secret reference access denied")
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	if attempt.ID() != lease.AttemptID {
		return nil, status.Error(codes.PermissionDenied, "secret reference access denied")
	}

	dag, err := attempt.ReadDAG(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if dag == nil {
		return nil, status.Error(codes.FailedPrecondition, "dag definition is not available")
	}
	return dag, nil
}

func secretReferenceWorkspace(dag *core.DAG) string {
	if dag == nil {
		return secretpkg.GlobalWorkspace
	}
	if workspaceName, found := exec.WorkspaceNameFromLabels(dag.Labels); found {
		return secretpkg.NormalizeWorkspace(workspaceName)
	}
	return secretpkg.GlobalWorkspace
}

func secretReferenceDeclared(dag *core.DAG, ref core.SecretRef) bool {
	if dag == nil {
		return false
	}
	for _, declared := range dag.Secrets {
		if declared.Name == ref.Name && declared.Ref == ref.Ref && declared.Provider == "" && declared.Key == "" && len(declared.Options) == 0 {
			return true
		}
	}
	return false
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
