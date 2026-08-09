// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"context"
	"errors"
	"fmt"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type remoteAttemptIdentity struct {
	attemptKey string
	claimKey   string
	workerID   string
	dagRun     ir.DAGRunRef
	root       ir.DAGRunRef
	attemptID  string
}

func newRemoteAttemptIdentity(
	explicitKey string,
	workerID string,
	dagRun ir.DAGRunRef,
	root ir.DAGRunRef,
	attemptID string,
) (remoteAttemptIdentity, error) {
	if workerID == "" || dagRun.Zero() || attemptID == "" {
		return remoteAttemptIdentity{}, fmt.Errorf("worker, DAG run, and attempt identity are required")
	}
	if root.Zero() {
		root = dagRun
	}
	derivedKey := ir.GenerateAttemptKey(root.Name, root.ID, dagRun.Name, dagRun.ID, attemptID)
	if explicitKey != "" && dagRun == root && explicitKey != derivedKey {
		return remoteAttemptIdentity{}, fmt.Errorf("attempt key does not match stream metadata")
	}
	claimKey := explicitKey
	if claimKey == "" {
		claimKey = derivedKey
	}
	return remoteAttemptIdentity{
		attemptKey: derivedKey,
		claimKey:   claimKey,
		workerID:   workerID,
		dagRun:     dagRun,
		root:       root,
		attemptID:  attemptID,
	}, nil
}

func logChunkIdentity(chunk *coordinatorv1.LogChunk) (remoteAttemptIdentity, error) {
	if chunk == nil {
		return remoteAttemptIdentity{}, fmt.Errorf("log chunk is required")
	}
	return newRemoteAttemptIdentity(
		chunk.AttemptKey,
		chunk.WorkerId,
		ir.NewDAGRunRef(chunk.DagName, chunk.DagRunId),
		ir.NewDAGRunRef(chunk.RootDagRunName, chunk.RootDagRunId),
		chunk.AttemptId,
	)
}

func artifactChunkIdentity(chunk *coordinatorv1.ArtifactChunk) (remoteAttemptIdentity, error) {
	if chunk == nil {
		return remoteAttemptIdentity{}, fmt.Errorf("artifact chunk is required")
	}
	return newRemoteAttemptIdentity(
		chunk.AttemptKey,
		chunk.WorkerId,
		ir.NewDAGRunRef(chunk.DagName, chunk.DagRunId),
		ir.NewDAGRunRef(chunk.RootDagRunName, chunk.RootDagRunId),
		chunk.AttemptId,
	)
}

func statusIdentity(workerID string, runStatus *ir.DAGRunStatus) (remoteAttemptIdentity, error) {
	if runStatus == nil {
		return remoteAttemptIdentity{}, fmt.Errorf("status is required")
	}
	if workerID == "" || runStatus.DAGRun().Zero() || runStatus.AttemptID == "" {
		return remoteAttemptIdentity{}, fmt.Errorf("worker, DAG run, and attempt identity are required")
	}
	root := runStatus.Root
	if root.Zero() {
		root = runStatus.DAGRun()
	}
	attemptKey := runStatus.AttemptKey
	if attemptKey == "" {
		attemptKey = ir.GenerateAttemptKey(root.Name, root.ID, runStatus.Name, runStatus.DAGRunID, runStatus.AttemptID)
	}
	claimKey := runStatus.EffectiveClaimKey()
	if claimKey == "" {
		claimKey = attemptKey
	}
	return remoteAttemptIdentity{
		attemptKey: attemptKey,
		claimKey:   claimKey,
		workerID:   workerID,
		dagRun:     runStatus.DAGRun(),
		root:       root,
		attemptID:  runStatus.AttemptID,
	}, nil
}

func (h *Handler) validateRemoteStatusLease(
	ctx context.Context,
	workerID string,
	runStatus *ir.DAGRunStatus,
) (bool, error) {
	if workerID == "" {
		return false, status.Error(codes.InvalidArgument, "worker, DAG run, and attempt identity are required")
	}

	identity, identityErr := statusIdentity(workerID, runStatus)
	if identityErr == nil {
		lease, err := h.dagRunLeaseStore.Get(ctx, identity.claimKey)
		switch {
		case err == nil:
			return false, h.validateRemoteAttemptLease(lease, identity)
		case !errors.Is(err, dispatch.ErrDAGRunLeaseNotFound):
			return false, status.Error(codes.Internal, "failed to load distributed run lease: "+err.Error())
		}
	}

	if !isSubDAGStatus(runStatus) {
		if identityErr != nil {
			return false, status.Error(codes.InvalidArgument, identityErr.Error())
		}
		return true, nil
	}
	if runStatus.ClaimKey != "" {
		return false, status.Error(codes.FailedPrecondition, remoteAttemptRejectedLeaseInactive)
	}

	claimKey, err := h.validateRootLeaseForSubDAGStatus(ctx, workerID, runStatus.Root)
	if err != nil {
		return false, err
	}
	runStatus.ClaimKey = claimKey
	return false, nil
}

func (h *Handler) validateRootLeaseForSubDAGStatus(
	ctx context.Context,
	workerID string,
	rootRef ir.DAGRunRef,
) (string, error) {
	rootAttempt, err := h.dagRunStore.FindAttempt(ctx, rootRef)
	if err != nil {
		if errors.Is(err, dagrun.ErrDAGRunIDNotFound) {
			return "", status.Error(codes.FailedPrecondition, remoteAttemptRejectedLeaseInactive)
		}
		return "", status.Error(codes.Internal, "failed to load root attempt: "+err.Error())
	}
	rootStatus, err := rootAttempt.ReadStatus(ctx)
	if err != nil {
		if errors.Is(err, dagrun.ErrNoStatusData) {
			return "", status.Error(codes.FailedPrecondition, remoteAttemptRejectedLeaseInactive)
		}
		return "", status.Error(codes.Internal, "failed to load root status: "+err.Error())
	}
	if rootStatus == nil || isTerminalRunStatus(rootStatus.Status) {
		return "", status.Error(codes.FailedPrecondition, remoteAttemptRejectedLeaseInactive)
	}

	attemptID := rootStatus.AttemptID
	if attemptID == "" {
		attemptID = rootAttempt.ID()
	}
	attemptKey := dispatch.AttemptKeyForStatus(rootStatus, rootAttempt.ID())
	claimKey := rootStatus.EffectiveClaimKey()
	if claimKey == "" {
		claimKey = attemptKey
	}
	if attemptKey == "" || claimKey == "" {
		return "", status.Error(codes.FailedPrecondition, remoteAttemptRejectedLeaseInactive)
	}
	identity := remoteAttemptIdentity{
		attemptKey: attemptKey,
		claimKey:   claimKey,
		workerID:   workerID,
		dagRun:     rootRef,
		root:       rootRef,
		attemptID:  attemptID,
	}
	if err := h.validateRemoteAttempt(ctx, identity); err != nil {
		return "", err
	}
	return claimKey, nil
}

func isSubDAGStatus(runStatus *ir.DAGRunStatus) bool {
	return runStatus != nil && !runStatus.Root.Zero() && runStatus.Root != runStatus.DAGRun()
}

func runningTaskIdentity(workerID string, task *coordinatorv1.RunningTask) (remoteAttemptIdentity, error) {
	if task == nil || workerID == "" || task.AttemptKey == "" || task.DagName == "" || task.DagRunId == "" {
		return remoteAttemptIdentity{}, fmt.Errorf("running task identity is required")
	}
	dagRun := ir.NewDAGRunRef(task.DagName, task.DagRunId)
	root := ir.NewDAGRunRef(task.RootDagRunName, task.RootDagRunId)
	if root.Zero() {
		root = dagRun
	}
	return remoteAttemptIdentity{
		attemptKey: task.AttemptKey,
		claimKey:   task.AttemptKey,
		workerID:   workerID,
		dagRun:     dagRun,
		root:       root,
	}, nil
}

func (h *Handler) validateRemoteAttempt(ctx context.Context, identity remoteAttemptIdentity) error {
	if h.dagRunLeaseStore == nil {
		return nil
	}
	lease, err := h.dagRunLeaseStore.Get(ctx, identity.claimKey)
	if err != nil {
		if errors.Is(err, dispatch.ErrDAGRunLeaseNotFound) || errors.Is(err, persis.ErrCorrupt) {
			return status.Error(codes.FailedPrecondition, remoteAttemptRejectedLeaseInactive)
		}
		return status.Error(codes.Internal, "failed to load distributed run lease: "+err.Error())
	}
	return h.validateRemoteAttemptLease(lease, identity)
}

func (h *Handler) validateRemoteAttemptLease(lease *dispatch.DAGRunLease, identity remoteAttemptIdentity) error {
	if !lease.MatchesClaim(identity.claimKey, identity.workerID) ||
		(!lease.Root.Zero() && lease.Root != identity.root) {
		return status.Error(codes.FailedPrecondition, remoteAttemptRejectedSuperseded)
	}
	if lease.Owner.Host != "" && h.owner.Host != "" && !sameCoordinatorAddress(lease.Owner, h.owner) {
		return status.Error(codes.FailedPrecondition, "attempt belongs to a different coordinator endpoint")
	}
	if identity.claimKey != identity.attemptKey {
		if lease.DAGRun != identity.root {
			return status.Error(codes.FailedPrecondition, remoteAttemptRejectedSuperseded)
		}
		return nil
	}
	if lease.DAGRun != identity.dagRun ||
		(identity.attemptID != "" && lease.AttemptID != identity.attemptID) {
		return status.Error(codes.FailedPrecondition, remoteAttemptRejectedSuperseded)
	}
	return nil
}
