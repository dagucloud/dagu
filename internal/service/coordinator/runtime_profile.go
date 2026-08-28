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
	profilepkg "github.com/dagucloud/dagu/v2/internal/profile"
	secretpkg "github.com/dagucloud/dagu/v2/internal/secret"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type RuntimeProfileClient interface {
	ResolveRuntimeProfile(context.Context, serviceregistry.HostInfo, profilepkg.RuntimeRequest, RuntimeProfileRun) (*profilepkg.RuntimeResolved, error)
}

type RuntimeProfileRun struct {
	WorkerID   string
	AttemptKey string
	AttemptID  string
	DAGName    string
}

const runtimeProfileAccessDenied = "runtime profile access denied"

type runtimeProfileResolver struct {
	client   RuntimeProfileClient
	owner    serviceregistry.HostInfo
	run      RuntimeProfileRun
	fallback profilepkg.RuntimeResolver
}

func NewRuntimeProfileResolver(
	client RuntimeProfileClient,
	owner serviceregistry.HostInfo,
	run RuntimeProfileRun,
	fallback profilepkg.RuntimeResolver,
) profilepkg.RuntimeResolver {
	if client == nil {
		return fallback
	}
	return &runtimeProfileResolver{client: client, owner: owner, run: run, fallback: fallback}
}

func (r *runtimeProfileResolver) ResolveRuntime(ctx context.Context, req profilepkg.RuntimeRequest) (*profilepkg.RuntimeResolved, error) {
	resolved, err := r.client.ResolveRuntimeProfile(ctx, r.owner, req, r.run)
	if status.Code(err) == codes.Unimplemented && r.fallback != nil {
		return r.fallback.ResolveRuntime(ctx, req)
	}
	return resolved, err
}

func (cli *clientImpl) ResolveRuntimeProfile(
	ctx context.Context,
	owner serviceregistry.HostInfo,
	req profilepkg.RuntimeRequest,
	run RuntimeProfileRun,
) (*profilepkg.RuntimeResolved, error) {
	if emptyCoordinatorOwner(owner) {
		return cli.resolveRuntimeProfile(ctx, runtimeProfileRequest(req, run))
	}
	if !completeCoordinatorOwner(owner) {
		return nil, fmt.Errorf("runtime profile owner coordinator endpoint is incomplete")
	}

	var resp *coordinatorv1.ResolveRuntimeProfileResponse
	err := cli.callOwner(ctx, owner, false, func(ctx context.Context, member serviceregistry.HostInfo, client *client) error {
		var callErr error
		resp, callErr = resolveRuntimeProfileRPC(ctx, member.ID, client, runtimeProfileRequest(req, run))
		return callErr
	})
	if err != nil {
		return nil, err
	}
	return runtimeProfileFromProto(resp)
}

func (cli *clientImpl) resolveRuntimeProfile(ctx context.Context, req *coordinatorv1.ResolveRuntimeProfileRequest) (*profilepkg.RuntimeResolved, error) {
	members, err := cli.getCoordinatorMembers(ctx)
	if err != nil {
		return nil, err
	}

	var resp *coordinatorv1.ResolveRuntimeProfileResponse
	err = cli.attemptCall(ctx, members, func(ctx context.Context, member serviceregistry.HostInfo, client *client) error {
		var callErr error
		resp, callErr = resolveRuntimeProfileRPC(ctx, member.ID, client, req)
		return callErr
	})
	if err != nil {
		return nil, err
	}
	return runtimeProfileFromProto(resp)
}

func resolveRuntimeProfileRPC(
	ctx context.Context,
	coordinatorID string,
	client *client,
	req *coordinatorv1.ResolveRuntimeProfileRequest,
) (*coordinatorv1.ResolveRuntimeProfileResponse, error) {
	resp, err := client.client.ResolveRuntimeProfile(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("resolve runtime profile failed: %w", err)
	}
	if resp == nil {
		return nil, fmt.Errorf("coordinator %s returned empty runtime profile response", coordinatorID)
	}
	return resp, nil
}

func runtimeProfileRequest(req profilepkg.RuntimeRequest, run RuntimeProfileRun) *coordinatorv1.ResolveRuntimeProfileRequest {
	return &coordinatorv1.ResolveRuntimeProfileRequest{
		WorkerId:    run.WorkerID,
		AttemptKey:  run.AttemptKey,
		AttemptId:   run.AttemptID,
		ProfileName: req.ProfileName,
		Workspace:   req.Workspace,
		DagName:     run.DAGName,
	}
}

func (h *Handler) ResolveRuntimeProfile(
	ctx context.Context,
	req *coordinatorv1.ResolveRuntimeProfileRequest,
) (*coordinatorv1.ResolveRuntimeProfileResponse, error) {
	if h.profileStore == nil {
		return nil, status.Error(codes.FailedPrecondition, "profile store is not configured")
	}
	if err := h.authorizeRuntimeProfile(ctx, req); err != nil {
		return nil, err
	}

	resolved, err := profilepkg.NewResolver(h.profileStore, h.secretStore).ResolveRuntime(ctx, profilepkg.RuntimeRequest{
		ProfileName: req.GetProfileName(),
		Workspace:   req.GetWorkspace(),
	})
	if err != nil {
		return nil, runtimeProfileRPCError(err)
	}
	return runtimeProfileToProto(resolved), nil
}

func (h *Handler) authorizeRuntimeProfile(ctx context.Context, req *coordinatorv1.ResolveRuntimeProfileRequest) error {
	if req == nil {
		return status.Error(codes.InvalidArgument, "runtime profile request is required")
	}
	if req.GetWorkerId() == "" || req.GetAttemptKey() == "" || req.GetAttemptId() == "" {
		return status.Error(codes.InvalidArgument, "worker_id, attempt_key, and attempt_id are required")
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
			return status.Error(codes.PermissionDenied, runtimeProfileAccessDenied)
		}
		return status.Error(codes.Internal, err.Error())
	}
	if lease.WorkerID != req.GetWorkerId() || lease.AttemptID != req.GetAttemptId() {
		return status.Error(codes.PermissionDenied, runtimeProfileAccessDenied)
	}
	if !lease.IsFresh(time.Now().UTC(), h.staleLeaseThreshold) {
		return status.Error(codes.PermissionDenied, runtimeProfileAccessDenied)
	}

	attempt, err := h.authorizedAttempt(ctx, lease, runtimeProfileAccessDenied)
	if err != nil {
		return err
	}
	dag, err := h.authorizedDAG(ctx, attempt, req.GetDagName(), runtimeProfileAccessDenied)
	if err != nil {
		return err
	}
	if secretpkg.NormalizeWorkspace(req.GetWorkspace()) != dagWorkspace(dag) {
		return status.Error(codes.PermissionDenied, runtimeProfileAccessDenied)
	}

	current, err := attempt.ReadStatus(ctx)
	if err != nil {
		if errors.Is(err, dagrun.ErrNoStatusData) || errors.Is(err, dagrun.ErrCorruptedStatusData) {
			return status.Error(codes.PermissionDenied, runtimeProfileAccessDenied)
		}
		return status.Error(codes.Internal, err.Error())
	}
	profileName, err := h.leaseProfileName(ctx, lease, current, lease.Root)
	if errors.Is(err, errProfileMismatch) {
		return status.Error(codes.PermissionDenied, runtimeProfileAccessDenied)
	}
	if err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	if profileName != req.GetProfileName() {
		return status.Error(codes.PermissionDenied, runtimeProfileAccessDenied)
	}
	if err := h.pinLeaseProfile(ctx, lease, profileName); err != nil {
		if errors.Is(err, errProfileMismatch) {
			return status.Error(codes.PermissionDenied, runtimeProfileAccessDenied)
		}
		return status.Error(codes.Internal, err.Error())
	}
	return nil
}

func runtimeProfileToProto(resolved *profilepkg.RuntimeResolved) *coordinatorv1.ResolveRuntimeProfileResponse {
	if resolved == nil {
		return &coordinatorv1.ResolveRuntimeProfileResponse{}
	}
	return &coordinatorv1.ResolveRuntimeProfileResponse{
		Defaults: runtimeProfileLayerToProto(resolved.Defaults),
		Selected: runtimeProfileLayerToProto(resolved.Selected),
	}
}

func runtimeProfileLayerToProto(layer *profilepkg.Resolved) *coordinatorv1.RuntimeProfileLayer {
	if layer == nil {
		return nil
	}
	entries := make([]*coordinatorv1.RuntimeProfileEntry, 0, len(layer.Entries))
	for _, entry := range layer.Entries {
		value := layer.Variables[entry.Key]
		if entry.Kind == profilepkg.EntryKindSecret {
			value = layer.Secrets[entry.Key]
		}
		entries = append(entries, &coordinatorv1.RuntimeProfileEntry{
			Key: entry.Key, Kind: string(entry.Kind), Value: value,
		})
	}
	return &coordinatorv1.RuntimeProfileLayer{Name: layer.Name, Entries: entries}
}

func runtimeProfileFromProto(resp *coordinatorv1.ResolveRuntimeProfileResponse) (*profilepkg.RuntimeResolved, error) {
	if resp == nil {
		return nil, fmt.Errorf("runtime profile response is empty")
	}
	defaults, err := runtimeProfileLayerFromProto(resp.GetDefaults())
	if err != nil {
		return nil, err
	}
	selected, err := runtimeProfileLayerFromProto(resp.GetSelected())
	if err != nil {
		return nil, err
	}
	return &profilepkg.RuntimeResolved{Defaults: defaults, Selected: selected}, nil
}

func runtimeProfileLayerFromProto(layer *coordinatorv1.RuntimeProfileLayer) (*profilepkg.Resolved, error) {
	if layer == nil {
		return nil, nil
	}
	resolved := &profilepkg.Resolved{
		Name: layer.GetName(), Variables: make(map[string]string), Secrets: make(map[string]string),
		Entries: make([]profilepkg.ResolvedEntry, 0, len(layer.GetEntries())),
	}
	for _, entry := range layer.GetEntries() {
		kind := profilepkg.EntryKind(entry.GetKind())
		switch kind {
		case profilepkg.EntryKindVariable:
			resolved.Variables[entry.GetKey()] = entry.GetValue()
		case profilepkg.EntryKindSecret:
			resolved.Secrets[entry.GetKey()] = entry.GetValue()
		default:
			return nil, fmt.Errorf("unsupported runtime profile entry kind %q", entry.GetKind())
		}
		resolved.Entries = append(resolved.Entries, profilepkg.ResolvedEntry{Key: entry.GetKey(), Kind: kind})
	}
	return resolved, nil
}

func runtimeProfileRPCError(err error) error {
	switch {
	case errors.Is(err, profilepkg.ErrInvalidName), errors.Is(err, secretpkg.ErrInvalidWorkspace):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, profilepkg.ErrNotFound), errors.Is(err, secretpkg.ErrNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, profilepkg.ErrDisabled), errors.Is(err, secretpkg.ErrDisabled), errors.Is(err, secretpkg.ErrNoValue):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, err.Error())
	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, err.Error())
	default:
		return status.Error(codes.Internal, err.Error())
	}
}
