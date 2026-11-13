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

type attemptIdentity struct {
	attemptKey string
	claimKey   string
	workerID   string
	dagRun     ir.DAGRunRef
	root       ir.DAGRunRef
	attemptID  string
}

func newAttemptIdentity(
	explicitKey string,
	workerID string,
	dagRun ir.DAGRunRef,
	root ir.DAGRunRef,
	attemptID string,
) (attemptIdentity, error) {
	if workerID == "" || dagRun.Zero() || attemptID == "" {
		return attemptIdentity{}, fmt.Errorf("worker, DAG run, and attempt identity are required")
	}
	if root.Zero() {
		root = dagRun
	}
	derivedKey := ir.GenerateAttemptKey(root.Name, root.ID, dagRun.Name, dagRun.ID, attemptID)
	if explicitKey != "" && dagRun == root && explicitKey != derivedKey {
		return attemptIdentity{}, fmt.Errorf("attempt key does not match stream metadata")
	}
	claimKey := explicitKey
	if claimKey == "" {
		claimKey = derivedKey
	}
	return attemptIdentity{
		attemptKey: derivedKey,
		claimKey:   claimKey,
		workerID:   workerID,
		dagRun:     dagRun,
		root:       root,
		attemptID:  attemptID,
	}, nil
}

func logChunkIdentity(chunk *coordinatorv1.LogChunk) (attemptIdentity, error) {
	if chunk == nil {
		return attemptIdentity{}, fmt.Errorf("log chunk is required")
	}
	return newAttemptIdentity(
		chunk.AttemptKey,
		chunk.WorkerId,
		ir.NewDAGRunRef(chunk.DagName, chunk.DagRunId),
		ir.NewDAGRunRef(chunk.RootDagRunName, chunk.RootDagRunId),
		chunk.AttemptId,
	)
}

func artifactChunkIdentity(chunk *coordinatorv1.ArtifactChunk) (attemptIdentity, error) {
	if chunk == nil {
		return attemptIdentity{}, fmt.Errorf("artifact chunk is required")
	}
	return newAttemptIdentity(
		chunk.AttemptKey,
		chunk.WorkerId,
		ir.NewDAGRunRef(chunk.DagName, chunk.DagRunId),
		ir.NewDAGRunRef(chunk.RootDagRunName, chunk.RootDagRunId),
		chunk.AttemptId,
	)
}

func statusIdentity(workerID string, runStatus *ir.DAGRunStatus) (attemptIdentity, error) {
	if runStatus == nil {
		return attemptIdentity{}, fmt.Errorf("status is required")
	}
	if workerID == "" || runStatus.DAGRun().Zero() || runStatus.AttemptID == "" {
		return attemptIdentity{}, fmt.Errorf("worker, DAG run, and attempt identity are required")
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
	return attemptIdentity{
		attemptKey: attemptKey,
		claimKey:   claimKey,
		workerID:   workerID,
		dagRun:     runStatus.DAGRun(),
		root:       root,
		attemptID:  runStatus.AttemptID,
	}, nil
}

func (h *Handler) validateStatusLease(
	ctx context.Context,
	workerID string,
	runStatus *ir.DAGRunStatus,
) (*dispatch.DAGRunLease, bool, error) {
	if workerID == "" {
		return nil, false, status.Error(codes.InvalidArgument, "worker, DAG run, and attempt identity are required")
	}

	identity, identityErr := statusIdentity(workerID, runStatus)
	if identityErr == nil {
		lease, err := h.dagRunLeaseStore.Get(ctx, identity.claimKey)
		switch {
		case err == nil:
			return lease, false, h.validateAttemptLease(ctx, lease, identity)
		case !errors.Is(err, dispatch.ErrDAGRunLeaseNotFound):
			return nil, false, status.Error(codes.Internal, "failed to load distributed run lease: "+err.Error())
		}
	}

	if !isSubDAGStatus(runStatus) {
		if identityErr != nil {
			return nil, false, status.Error(codes.InvalidArgument, identityErr.Error())
		}
		return nil, true, nil
	}
	if runStatus.ClaimKey != "" {
		return nil, false, status.Error(codes.FailedPrecondition, remoteAttemptRejectedLeaseInactive)
	}

	claimKey, lease, err := h.validateSubDAGRootLease(ctx, workerID, runStatus.Root)
	if err != nil {
		return nil, false, err
	}
	runStatus.ClaimKey = claimKey
	return lease, false, nil
}

func (h *Handler) validateSubDAGRootLease(
	ctx context.Context,
	workerID string,
	rootRef ir.DAGRunRef,
) (string, *dispatch.DAGRunLease, error) {
	rootAttempt, err := h.dagRunRepository.FindAttempt(ctx, rootRef)
	if err != nil {
		if errors.Is(err, dagrun.ErrDAGRunIDNotFound) {
			return "", nil, status.Error(codes.FailedPrecondition, remoteAttemptRejectedLeaseInactive)
		}
		return "", nil, status.Error(codes.Internal, "failed to load root attempt: "+err.Error())
	}
	rootStatus, err := rootAttempt.ReadStatus(ctx)
	if err != nil {
		if errors.Is(err, dagrun.ErrNoStatusData) {
			return "", nil, status.Error(codes.FailedPrecondition, remoteAttemptRejectedLeaseInactive)
		}
		return "", nil, status.Error(codes.Internal, "failed to load root status: "+err.Error())
	}
	if rootStatus == nil || isTerminalRunStatus(rootStatus.Status) {
		return "", nil, status.Error(codes.FailedPrecondition, remoteAttemptRejectedLeaseInactive)
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
		return "", nil, status.Error(codes.FailedPrecondition, remoteAttemptRejectedLeaseInactive)
	}
	identity := attemptIdentity{
		attemptKey: attemptKey,
		claimKey:   claimKey,
		workerID:   workerID,
		dagRun:     rootRef,
		root:       rootRef,
		attemptID:  attemptID,
	}
	lease, err := h.attemptLease(ctx, identity)
	if err != nil {
		return "", nil, err
	}
	return claimKey, lease, nil
}

func isSubDAGStatus(runStatus *ir.DAGRunStatus) bool {
	return runStatus != nil && !runStatus.Root.Zero() && runStatus.Root != runStatus.DAGRun()
}

func runningTaskIdentity(workerID string, task *coordinatorv1.RunningTask) (attemptIdentity, error) {
	if task == nil || workerID == "" || task.AttemptKey == "" || task.DagName == "" || task.DagRunId == "" {
		return attemptIdentity{}, fmt.Errorf("running task identity is required")
	}
	dagRun := ir.NewDAGRunRef(task.DagName, task.DagRunId)
	root := ir.NewDAGRunRef(task.RootDagRunName, task.RootDagRunId)
	if root.Zero() {
		root = dagRun
	}
	return attemptIdentity{
		attemptKey: task.AttemptKey,
		claimKey:   task.AttemptKey,
		workerID:   workerID,
		dagRun:     dagRun,
		root:       root,
	}, nil
}

func (h *Handler) validateAttempt(ctx context.Context, identity attemptIdentity) error {
	_, err := h.attemptLease(ctx, identity)
	return err
}

func (h *Handler) attemptLease(ctx context.Context, identity attemptIdentity) (*dispatch.DAGRunLease, error) {
	if h.dagRunLeaseStore == nil {
		return nil, nil
	}
	lease, err := h.dagRunLeaseStore.Get(ctx, identity.claimKey)
	if err != nil {
		if errors.Is(err, dispatch.ErrDAGRunLeaseNotFound) || errors.Is(err, persis.ErrCorrupt) {
			return nil, status.Error(codes.FailedPrecondition, remoteAttemptRejectedLeaseInactive)
		}
		return nil, status.Error(codes.Internal, "failed to load distributed run lease: "+err.Error())
	}
	if err := h.validateAttemptLease(ctx, lease, identity); err != nil {
		return nil, err
	}
	return lease, nil
}

func (h *Handler) validateAttemptLease(ctx context.Context, lease *dispatch.DAGRunLease, identity attemptIdentity) error {
	if !lease.MatchesClaim(identity.claimKey, identity.workerID) {
		return status.Error(codes.FailedPrecondition, remoteAttemptRejectedSuperseded)
	}
	// Each call of an attempt comes from the pool that claimed it (Spec 092).
	if err := h.checkLeasePool(ctx, lease.WorkerPool); err != nil {
		return err
	}
	leaseRoot := lease.Root
	if leaseRoot.Zero() {
		leaseRoot = lease.DAGRun
	}
	if leaseRoot != identity.root {
		return status.Error(codes.FailedPrecondition, remoteAttemptRejectedSuperseded)
	}
	if identity.claimKey != identity.attemptKey {
		// Inline descendants inherit their dispatching ancestor's worker claim.
		// A descendant that was dispatched has a claim of its own, so a call
		// that names the claim of an ancestor for it is refused (Spec 092).
		return h.checkInlineDescendant(ctx, identity)
	}
	if lease.DAGRun != identity.dagRun ||
		(identity.attemptID != "" && lease.AttemptID != identity.attemptID) {
		return status.Error(codes.FailedPrecondition, remoteAttemptRejectedSuperseded)
	}
	return nil
}

// checkInlineDescendant refuses a call that names the claim of an ancestor for
// an attempt that is not an inline descendant of it: the attempt has a lease of
// its own, or its record has another claim key. A lease or a record that cannot
// be read fails the call.
func (h *Handler) checkInlineDescendant(ctx context.Context, identity attemptIdentity) error {
	if h.dagRunLeaseStore != nil {
		_, err := h.dagRunLeaseStore.Get(ctx, identity.attemptKey)
		switch {
		case err == nil:
			return status.Error(codes.PermissionDenied, "the attempt has a lease of its own: a call for it must name its own claim")
		case !errors.Is(err, dispatch.ErrDAGRunLeaseNotFound):
			return status.Error(codes.Internal, "failed to load distributed run lease: "+err.Error())
		}
	}
	if h.dagRunRepository == nil || identity.root == identity.dagRun {
		return nil
	}
	attempt, err := h.dagRunRepository.FindSubAttempt(ctx, identity.root, identity.dagRun.ID)
	if errors.Is(err, dagrun.ErrDAGRunIDNotFound) {
		return nil // The first status of an inline descendant creates its record.
	}
	if err != nil {
		return status.Error(codes.Internal, "failed to load sub-attempt: "+err.Error())
	}
	stored, err := attempt.ReadStatus(ctx)
	if errors.Is(err, dagrun.ErrNoStatusData) {
		return nil
	}
	if err != nil {
		return status.Error(codes.Internal, "failed to read sub-attempt status: "+err.Error())
	}
	// A dispatched attempt has its own claim key in its record from the first
	// status (writeInitialStatus). A record of an older version has none.
	if storedKey := stored.ClaimKey; storedKey != "" && storedKey != identity.claimKey {
		return status.Error(codes.PermissionDenied, "the attempt belongs to another claim: a call for it must name its own claim")
	}
	return nil
}
