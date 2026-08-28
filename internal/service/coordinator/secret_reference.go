// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	secretpkg "github.com/dagucloud/dagu/v2/internal/secret"
	"github.com/dagucloud/dagu/v2/internal/secret/providers"
	secretref "github.com/dagucloud/dagu/v2/internal/secret/ref"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
	"github.com/dagucloud/dagu/v2/internal/workspace"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type SecretReferenceClient interface {
	ResolveSecretReference(ctx context.Context, owner serviceregistry.HostInfo, ref secretref.Ref, workspace string, checkOnly bool, run SecretReferenceRun) (string, error)
}

type SecretReferenceRun struct {
	WorkerID   string
	AttemptKey string
	AttemptID  string
	DAGName    string
}

const secretReferenceAccessDenied = "secret reference access denied"

type secretReferenceResolver struct {
	client    SecretReferenceClient
	workspace string
	owner     serviceregistry.HostInfo
	run       SecretReferenceRun
}

func NewSecretReferenceResolver(client SecretReferenceClient, workspace string, owner serviceregistry.HostInfo, run SecretReferenceRun) providers.ReferenceResolver {
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

func (r *secretReferenceResolver) ResolveReference(ctx context.Context, ref secretref.Ref) (string, error) {
	return r.resolve(ctx, ref, false)
}

func (r *secretReferenceResolver) CheckReferenceAccessibility(ctx context.Context, ref secretref.Ref) error {
	_, err := r.resolve(ctx, ref, true)
	return err
}

func (r *secretReferenceResolver) resolve(ctx context.Context, ref secretref.Ref, checkOnly bool) (string, error) {
	return r.client.ResolveSecretReference(ctx, r.owner, ref, r.workspace, checkOnly, r.run)
}

func (cli *clientImpl) ResolveSecretReference(ctx context.Context, owner serviceregistry.HostInfo, ref secretref.Ref, workspace string, checkOnly bool, run SecretReferenceRun) (string, error) {
	if !emptyCoordinatorOwner(owner) && !completeCoordinatorOwner(owner) {
		return "", fmt.Errorf("secret reference owner coordinator endpoint is incomplete")
	}

	req := secretReferenceRequest(ref, workspace, checkOnly, run)
	if completeCoordinatorOwner(owner) {
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
	err = cli.attemptCall(ctx, members, func(ctx context.Context, member serviceregistry.HostInfo, client *client) error {
		var callErr error
		resp, callErr = resolveSecretReferenceRPC(ctx, member.ID, client, req)
		return callErr
	})
	if err != nil {
		return "", err
	}
	return resp.GetValue(), nil
}

func (cli *clientImpl) resolveSecretReferenceTo(ctx context.Context, owner serviceregistry.HostInfo, req *coordinatorv1.ResolveSecretReferenceRequest) (string, error) {
	var resp *coordinatorv1.ResolveSecretReferenceResponse
	err := cli.callOwner(ctx, owner, false, func(ctx context.Context, member serviceregistry.HostInfo, client *client) error {
		var callErr error
		resp, callErr = resolveSecretReferenceRPC(ctx, member.ID, client, req)
		return callErr
	})
	if err != nil {
		return "", err
	}
	return resp.GetValue(), nil
}

func emptyCoordinatorOwner(owner serviceregistry.HostInfo) bool {
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

func completeCoordinatorOwner(owner serviceregistry.HostInfo) bool {
	return owner.Host != "" && owner.Port != 0
}

func secretReferenceRequest(ref secretref.Ref, workspace string, checkOnly bool, run SecretReferenceRun) *coordinatorv1.ResolveSecretReferenceRequest {
	return &coordinatorv1.ResolveSecretReferenceRequest{
		Name:       ref.Name,
		Ref:        ref.Ref,
		Workspace:  secretpkg.NormalizeWorkspace(workspace),
		CheckOnly:  checkOnly,
		WorkerId:   run.WorkerID,
		AttemptKey: run.AttemptKey,
		AttemptId:  run.AttemptID,
		DagName:    run.DAGName,
	}
}

func (h *Handler) ResolveSecretReference(ctx context.Context, req *coordinatorv1.ResolveSecretReferenceRequest) (*coordinatorv1.ResolveSecretReferenceResponse, error) {
	if h.secretStore == nil {
		return nil, status.Error(codes.FailedPrecondition, "secret store is not configured")
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "secret reference request is required")
	}
	ref := secretref.Ref{
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

func (h *Handler) authorizeSecretReference(ctx context.Context, req *coordinatorv1.ResolveSecretReferenceRequest, ref secretref.Ref) error {
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
	if h.dagRunRepository == nil {
		return status.Error(codes.FailedPrecondition, "DAG-run repository is not configured")
	}

	lease, err := h.dagRunLeaseStore.Get(ctx, req.GetAttemptKey())
	if err != nil {
		if errors.Is(err, dispatch.ErrDAGRunLeaseNotFound) {
			return status.Error(codes.PermissionDenied, secretReferenceAccessDenied)
		}
		return status.Error(codes.Internal, err.Error())
	}
	if lease.WorkerID != req.GetWorkerId() || lease.AttemptID != req.GetAttemptId() {
		return status.Error(codes.PermissionDenied, secretReferenceAccessDenied)
	}
	if !lease.IsFresh(time.Now().UTC(), h.staleLeaseThreshold) {
		return status.Error(codes.PermissionDenied, secretReferenceAccessDenied)
	}

	attempt, err := h.authorizedAttempt(ctx, lease, secretReferenceAccessDenied)
	if err != nil {
		return err
	}
	dag, err := h.authorizedDAG(ctx, attempt, req.GetDagName(), secretReferenceAccessDenied)
	if err != nil {
		return err
	}
	if secretpkg.NormalizeWorkspace(req.GetWorkspace()) != dagWorkspace(dag) {
		return status.Error(codes.PermissionDenied, secretReferenceAccessDenied)
	}
	if !secretReferenceDeclared(dag, ref) {
		return status.Error(codes.PermissionDenied, secretReferenceAccessDenied)
	}
	return nil
}

func (h *Handler) authorizedAttempt(ctx context.Context, lease *dispatch.DAGRunLease, deniedMessage string) (dagrun.Attempt, error) {
	if lease == nil {
		return nil, status.Error(codes.PermissionDenied, deniedMessage)
	}

	var (
		attempt dagrun.Attempt
		err     error
	)
	if !lease.Root.Zero() && lease.Root != lease.DAGRun {
		attempt, err = h.dagRunRepository.FindSubAttempt(ctx, lease.Root, lease.DAGRun.ID)
	} else {
		attempt, err = h.dagRunRepository.FindAttempt(ctx, lease.DAGRun)
	}
	if err != nil {
		if errors.Is(err, dagrun.ErrDAGRunIDNotFound) || errors.Is(err, dagrun.ErrNoStatusData) || errors.Is(err, dagrun.ErrCorruptedStatusData) {
			return nil, status.Error(codes.PermissionDenied, deniedMessage)
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	if attempt.ID() != lease.AttemptID {
		return nil, status.Error(codes.PermissionDenied, deniedMessage)
	}
	return attempt, nil
}

func (h *Handler) authorizedDAG(ctx context.Context, attempt dagrun.Attempt, dagName, deniedMessage string) (*ir.DAG, error) {
	dag, err := attempt.ReadDAG(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if dag == nil {
		return nil, status.Error(codes.FailedPrecondition, "dag definition is not available")
	}
	if dagName == "" || dagName == dag.Name {
		return dag, nil
	}
	// Follow only declared DAG edges so repository contents do not grant access.
	child := h.reachableDAG(ctx, dag, dagName, map[string]struct{}{dag.Name: {}})
	if child != nil {
		return child, nil
	}
	return nil, status.Error(codes.PermissionDenied, deniedMessage)
}

func localDAGByName(dag *ir.DAG, name string) *ir.DAG {
	if dag == nil {
		return nil
	}
	if child := dag.LocalDAGs[name]; child != nil {
		return child
	}
	for _, child := range dag.LocalDAGs {
		if found := localDAGByName(child, name); found != nil {
			return found
		}
	}
	return nil
}

func (h *Handler) reachableDAG(ctx context.Context, dag *ir.DAG, name string, visited map[string]struct{}) *ir.DAG {
	if dag == nil {
		return nil
	}
	if dag.Name == name {
		return dag
	}
	if child := localDAGByName(dag, name); child != nil {
		return child
	}
	if h.dagRepository == nil {
		return nil
	}

	for _, childName := range externalSubDAGNames(dag) {
		if _, found := visited[childName]; found {
			continue
		}
		visited[childName] = struct{}{}

		child, err := h.dagRepository.GetDetails(ctx, childName, persis.DAGLoadOptions{})
		if err != nil {
			continue
		}
		if found := h.reachableDAG(ctx, child, name, visited); found != nil {
			return found
		}
	}
	return nil
}

func externalSubDAGNames(dag *ir.DAG) []string {
	var names []string
	var collect func(*ir.DAG)
	collect = func(current *ir.DAG) {
		if current == nil {
			return
		}
		for _, step := range current.Steps {
			if step.SubDAG != nil && localDAGByName(dag, step.SubDAG.Name) == nil {
				names = append(names, step.SubDAG.Name)
			}
		}
		for _, child := range current.LocalDAGs {
			collect(child)
		}
	}
	collect(dag)
	return names
}

func dagWorkspace(dag *ir.DAG) string {
	if dag == nil {
		return secretpkg.GlobalWorkspace
	}
	if workspaceName, found := workspace.WorkspaceNameFromLabels(dag.Labels); found {
		return secretpkg.NormalizeWorkspace(workspaceName)
	}
	return secretpkg.GlobalWorkspace
}

func secretReferenceDeclared(dag *ir.DAG, ref secretref.Ref) bool {
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
