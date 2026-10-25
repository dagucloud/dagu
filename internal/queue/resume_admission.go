// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package queue

import (
	"context"
	"errors"
	"fmt"

	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
)

var errAdmissionUnchanged = errors.New("resume admission is already acknowledged or no longer current")

// ResumeAdmissionPending reports a recoverable queue publication, rather than
// an ordinary queued retry or a legacy admission with no durable identity.
func ResumeAdmissionPending(status *ir.DAGRunStatus) bool {
	return status != nil && status.Status == ir.Queued && status.Resume &&
		status.ResumeAdmission != nil && status.ResumeAdmission.ID != "" && !status.ResumeAdmission.Admitted
}

func resumeAdmissionIdentity(status *ir.DAGRunStatus) RetryAdmissionIdentity {
	return RetryAdmissionIdentity{
		ID: status.ResumeAdmission.ID, AttemptID: status.AttemptID,
		AttemptKey: status.AttemptKey, QueuedAt: status.QueuedAt, Root: status.Root,
	}
}

func ensureResumeAdmission(
	ctx context.Context,
	repository *persis.DAGRunRepository,
	store RetryAdmissionStore,
	dag *ir.DAG,
	status *ir.DAGRunStatus,
	root ir.DAGRunRef,
) (bool, error) {
	created := false
	_, swapped, err := repository.CompareAndSwapLatestAttemptStatus(
		ctx, status.DAGRun(), status.AttemptID, ir.Queued,
		func(latest *ir.DAGRunStatus) error {
			if !ResumeAdmissionPending(latest) {
				return errAdmissionUnchanged
			}
			if status.Status == ir.Queued && (status.QueuedAt != latest.QueuedAt ||
				status.ResumeAdmission == nil || status.ResumeAdmission.ID != latest.ResumeAdmission.ID) {
				return ErrRetryStaleLatest
			}
			name := retryProcGroup(dag, latest)
			if name == "" {
				return errors.New("proc group is empty")
			}
			var err error
			created, err = store.EnsureRetryAdmission(ctx, name, QueuePriorityLow, latest.DAGRun(), resumeAdmissionIdentity(latest))
			if err != nil {
				return err
			}
			// Publish while holding the latest-attempt transition lock. A crash
			// before this write leaves the same deterministic item recoverable;
			// the dispatcher also acknowledges before removing that item.
			latest.ResumeAdmission = &ir.ResumeAdmission{ID: latest.ResumeAdmission.ID, Admitted: true}
			return nil
		}, persis.DAGRunCompareAndSwapOptions{RootDAGRun: root, ExpectedAttemptKey: status.AttemptKey},
	)
	if errors.Is(err, errAdmissionUnchanged) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !swapped {
		return false, ErrRetryStaleLatest
	}
	return created, nil
}

// MatchesRetryAdmission rejects an old item from a superseded attempt or
// retry generation. An acknowledged item remains runnable until dispatch
// succeeds; acknowledgement is not an execution claim.
func MatchesRetryAdmission(item QueuedItemData, status *ir.DAGRunStatus) bool {
	identity := itemRetryAdmission(item)
	if identity == nil {
		return true
	}
	return status != nil && status.Resume && status.ResumeAdmission != nil &&
		status.ResumeAdmission.ID == identity.ID && status.AttemptID == identity.AttemptID &&
		status.AttemptKey == identity.AttemptKey && status.QueuedAt == identity.QueuedAt
}

// AcknowledgeRetryAdmission records publication before the dispatcher deletes
// an item. This closes the crash window between Create and the publisher's
// status write, including a distributed handoff that leaves status Queued.
// A failure must leave the item in the queue for the next dispatch pass.
func AcknowledgeRetryAdmission(ctx context.Context, repository *persis.DAGRunRepository, item QueuedItemData) error {
	identity := itemRetryAdmission(item)
	if identity == nil {
		return nil
	}
	ref, err := item.Data()
	if err != nil {
		return nil // Invalid items are discarded, never admitted by recovery.
	}
	_, _, err = repository.CompareAndSwapLatestAttemptStatus(
		ctx, *ref, identity.AttemptID, ir.Queued,
		func(latest *ir.DAGRunStatus) error {
			if !MatchesRetryAdmission(item, latest) || latest.ResumeAdmission.Admitted {
				return errAdmissionUnchanged
			}
			latest.ResumeAdmission = &ir.ResumeAdmission{ID: identity.ID, Admitted: true}
			return nil
		}, persis.DAGRunCompareAndSwapOptions{RootDAGRun: identity.Root, ExpectedAttemptKey: identity.AttemptKey},
	)
	if errors.Is(err, errAdmissionUnchanged) || errors.Is(err, dagrun.ErrDAGRunIDNotFound) ||
		errors.Is(err, dagrun.ErrNoStatusData) || errors.Is(err, dagrun.ErrCorruptedStatusData) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("acknowledge resume admission: %w", err)
	}
	return nil
}

func itemRetryAdmission(item QueuedItemData) *RetryAdmissionIdentity {
	if item, ok := item.(RetryAdmissionItem); ok {
		return item.RetryAdmission()
	}
	return nil
}
