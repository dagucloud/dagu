// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"context"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestValidateAttemptLeaseAcceptsNestedInlineChild(t *testing.T) {
	t.Parallel()

	root := ir.NewDAGRunRef("root", "root-run")
	parent := ir.NewDAGRunRef("parent", "parent-run")
	child := ir.NewDAGRunRef("child", "child-run")
	lease := &dispatch.DAGRunLease{
		AttemptKey: "parent-claim",
		DAGRun:     parent,
		Root:       root,
		AttemptID:  "parent-attempt",
		WorkerID:   "worker-2",
	}

	err := (&Handler{}).validateAttemptLease(context.Background(), lease, attemptIdentity{
		attemptKey: "child-attempt-key",
		claimKey:   lease.AttemptKey,
		workerID:   lease.WorkerID,
		dagRun:     child,
		root:       root,
		attemptID:  "child-attempt",
	})

	require.NoError(t, err)
}

func TestValidateAttemptLeaseAcceptsNestedInlineChildWithUnsetLeaseRoot(t *testing.T) {
	t.Parallel()

	root := ir.NewDAGRunRef("root", "root-run")
	lease := &dispatch.DAGRunLease{
		AttemptKey: "root-claim",
		DAGRun:     root,
		AttemptID:  "root-attempt",
		WorkerID:   "worker-2",
	}

	err := (&Handler{}).validateAttemptLease(context.Background(), lease, attemptIdentity{
		attemptKey: "child-attempt-key",
		claimKey:   lease.AttemptKey,
		workerID:   lease.WorkerID,
		dagRun:     ir.NewDAGRunRef("child", "child-run"),
		root:       root,
		attemptID:  "child-attempt",
	})

	require.NoError(t, err)
}

func TestValidateAttemptLeaseRejectsNestedInlineChildFromDifferentRoot(t *testing.T) {
	t.Parallel()

	root := ir.NewDAGRunRef("root", "root-run")
	lease := &dispatch.DAGRunLease{
		AttemptKey: "parent-claim",
		DAGRun:     ir.NewDAGRunRef("parent", "parent-run"),
		Root:       root,
		AttemptID:  "parent-attempt",
		WorkerID:   "worker-2",
	}

	err := (&Handler{}).validateAttemptLease(context.Background(), lease, attemptIdentity{
		attemptKey: "child-attempt-key",
		claimKey:   lease.AttemptKey,
		workerID:   lease.WorkerID,
		dagRun:     ir.NewDAGRunRef("child", "child-run"),
		root:       ir.NewDAGRunRef("other-root", "other-root-run"),
		attemptID:  "child-attempt",
	})

	require.Equal(t, codes.FailedPrecondition, status.Code(err))
	require.Equal(t, remoteAttemptRejectedSuperseded, status.Convert(err).Message())
}

func TestValidateAttemptLeaseRejectsNestedInlineChildFromDifferentRootWithUnsetLeaseRoot(t *testing.T) {
	t.Parallel()

	root := ir.NewDAGRunRef("root", "root-run")
	lease := &dispatch.DAGRunLease{
		AttemptKey: "root-claim",
		DAGRun:     root,
		AttemptID:  "root-attempt",
		WorkerID:   "worker-2",
	}

	err := (&Handler{}).validateAttemptLease(context.Background(), lease, attemptIdentity{
		attemptKey: "child-attempt-key",
		claimKey:   lease.AttemptKey,
		workerID:   lease.WorkerID,
		dagRun:     ir.NewDAGRunRef("child", "child-run"),
		root:       ir.NewDAGRunRef("other-root", "other-root-run"),
		attemptID:  "child-attempt",
	})

	require.Equal(t, codes.FailedPrecondition, status.Code(err))
	require.Equal(t, remoteAttemptRejectedSuperseded, status.Convert(err).Message())
}

func TestValidateAttemptLeaseRejectsNestedInlineChildFromDifferentWorker(t *testing.T) {
	t.Parallel()

	root := ir.NewDAGRunRef("root", "root-run")
	lease := &dispatch.DAGRunLease{
		AttemptKey: "parent-claim",
		DAGRun:     ir.NewDAGRunRef("parent", "parent-run"),
		Root:       root,
		AttemptID:  "parent-attempt",
		WorkerID:   "worker-2",
	}

	err := (&Handler{}).validateAttemptLease(context.Background(), lease, attemptIdentity{
		attemptKey: "child-attempt-key",
		claimKey:   lease.AttemptKey,
		workerID:   "worker-3",
		dagRun:     ir.NewDAGRunRef("child", "child-run"),
		root:       root,
		attemptID:  "child-attempt",
	})

	require.Equal(t, codes.FailedPrecondition, status.Code(err))
	require.Equal(t, remoteAttemptRejectedSuperseded, status.Convert(err).Message())
}
