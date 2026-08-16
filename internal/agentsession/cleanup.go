// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package agentsession owns deferred cleanup of coding-agent provider sessions.
package agentsession

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/google/uuid"
)

const (
	cleanupOwnerAffinityTTL = 24 * time.Hour
)

// CleanupJob is a durable request to remove one provider session.
type CleanupJob struct {
	Root          ir.DAGRunRef            `json:"root"`
	Resource      ir.AgentSessionResource `json:"resource"`
	ID            string                  `json:"id"`
	ClaimToken    string                  `json:"claimToken,omitempty"`
	ClaimedBy     string                  `json:"claimedBy,omitempty"`
	LastError     string                  `json:"lastError,omitempty"`
	ClaimedAt     int64                   `json:"claimedAt,omitempty"`
	NextAttemptAt int64                   `json:"nextAttemptAt,omitempty"`
	Attempts      int                     `json:"attempts,omitempty"`
}

// CleanupQueue persists provider cleanup until the owning execution host completes it.
type CleanupQueue struct {
	col persis.Collection
	now func() time.Time
}

// NewCleanupQueue creates a cleanup queue backed by col.
func NewCleanupQueue(col persis.Collection) *CleanupQueue {
	return &CleanupQueue{col: col, now: time.Now}
}

// SessionDeleter removes one provider resource from its owning host.
type SessionDeleter func(context.Context, ir.AgentSessionResource) error

// RunCleanupLoop processes eligible jobs until ctx is cancelled.
func RunCleanupLoop(
	ctx context.Context,
	ownerWorkerID string,
	queue *CleanupQueue,
	repository *persis.DAGRunRepository,
	deleteSession SessionDeleter,
) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		processed, err := ProcessNextCleanup(ctx, ownerWorkerID, queue, repository, deleteSession)
		if err != nil && !errors.Is(err, context.Canceled) {
			logger.Warn(ctx, "Agent session cleanup failed", tag.Error(err), slog.String("worker-id", ownerWorkerID))
		}
		if processed {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// ProcessNextCleanup processes one eligible job.
func ProcessNextCleanup(
	ctx context.Context,
	ownerWorkerID string,
	queue *CleanupQueue,
	repository *persis.DAGRunRepository,
	deleteSession SessionDeleter,
) (bool, error) {
	job, err := queue.Claim(ctx, ownerWorkerID, time.Minute)
	if errors.Is(err, persis.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if repository == nil || deleteSession == nil {
		err := errors.New("agent session cleanup processor is not configured")
		return true, errors.Join(err, queue.Release(ctx, ownerWorkerID, job.ID, job.ClaimToken, err.Error()))
	}
	_, findErr := repository.FindAttempt(ctx, job.Root)
	switch {
	case findErr == nil:
		err := errors.New("DAG run still exists")
		return true, queue.Release(ctx, ownerWorkerID, job.ID, job.ClaimToken, err.Error())
	case !errors.Is(findErr, dagrun.ErrDAGRunIDNotFound):
		return true, errors.Join(findErr, queue.Release(ctx, ownerWorkerID, job.ID, job.ClaimToken, findErr.Error()))
	}
	if err := deleteSession(ctx, job.Resource); err != nil {
		return true, errors.Join(err, queue.Release(ctx, ownerWorkerID, job.ID, job.ClaimToken, err.Error()))
	}
	if err := queue.Complete(ctx, ownerWorkerID, job.ID, job.ClaimToken); err != nil {
		return true, err
	}
	logger.Info(ctx, "Removed retained agent session",
		slog.String("provider", job.Resource.Provider),
		slog.String("session-id", job.Resource.SessionID),
		slog.String("dag-run-id", job.Root.ID),
		slog.String("worker-id", ownerWorkerID),
	)
	return true, nil
}

// EnqueueDAGRunRemoval records cleanup for all Dagu-owned sessions retained by root.
func (q *CleanupQueue) EnqueueDAGRunRemoval(ctx context.Context, root ir.DAGRunRef, resources []ir.AgentSessionResource) error {
	if q == nil || q.col == nil {
		return errors.New("agent session cleanup queue is not configured")
	}
	now := q.now().UTC()
	for _, resource := range resources {
		if resource.Provider == "" || resource.SessionID == "" {
			continue
		}
		job := CleanupJob{Root: root, Resource: resource}
		job.ID = cleanupJobID(root, resource)
		data, err := persis.Encode(job)
		if err != nil {
			return err
		}
		err = q.col.Create(ctx, &persis.Record{ID: job.ID, Data: data, CreatedAt: now, UpdatedAt: now})
		if err != nil && !errors.Is(err, persis.ErrConflict) {
			return fmt.Errorf("enqueue agent session cleanup: %w", err)
		}
	}
	return nil
}

// Claim reserves the next eligible cleanup job for ownerWorkerID.
func (q *CleanupQueue) Claim(ctx context.Context, ownerWorkerID string, lease time.Duration) (*CleanupJob, error) {
	if q == nil || q.col == nil {
		return nil, persis.ErrNotFound
	}
	var claimed *CleanupJob
	now := q.now().UTC()
	err := q.visit(ctx, func(record *persis.Record, job CleanupJob) (bool, error) {
		ownerMatches := sameCleanupOwner(job.Resource.OwnerWorkerID, ownerWorkerID)
		if !ownerMatches && record.CreatedAt.Add(cleanupOwnerAffinityTTL).After(now) {
			return false, nil
		}
		if job.NextAttemptAt > now.UnixMilli() {
			return false, nil
		}
		if job.ClaimToken != "" && time.UnixMilli(job.ClaimedAt).Add(lease).After(now) {
			return false, nil
		}
		job.ClaimToken = uuid.NewString()
		job.ClaimedBy = ownerWorkerID
		job.ClaimedAt = now.UnixMilli()
		data, err := persis.Encode(job)
		if err != nil {
			return false, err
		}
		if err := q.col.CompareAndSwap(ctx, record.ID, record.Data, data); err != nil {
			if errors.Is(err, persis.ErrConflict) || errors.Is(err, persis.ErrNotFound) {
				return false, nil
			}
			return false, err
		}
		claimed = &job
		return true, nil
	})
	if err != nil {
		return nil, err
	}
	if claimed == nil {
		return nil, persis.ErrNotFound
	}
	return claimed, nil
}

func sameCleanupOwner(resourceOwner, claimant string) bool {
	if resourceOwner == claimant {
		return true
	}
	return (resourceOwner == "" || resourceOwner == "local") && (claimant == "" || claimant == "local")
}

// Complete removes a claimed cleanup job.
func (q *CleanupQueue) Complete(ctx context.Context, ownerWorkerID, id, claimToken string) error {
	return q.updateClaim(ctx, ownerWorkerID, id, claimToken, func(record *persis.Record, _ *CleanupJob) error {
		return q.col.CompareAndDelete(ctx, record)
	})
}

// Release records a failed cleanup and makes it eligible for a later retry.
func (q *CleanupQueue) Release(ctx context.Context, ownerWorkerID, id, claimToken, message string) error {
	return q.updateClaim(ctx, ownerWorkerID, id, claimToken, func(record *persis.Record, job *CleanupJob) error {
		now := q.now().UTC()
		job.Attempts++
		job.ClaimToken = ""
		job.ClaimedBy = ""
		job.ClaimedAt = 0
		job.LastError = message
		delay := min(time.Duration(job.Attempts)*time.Minute, 15*time.Minute)
		job.NextAttemptAt = now.Add(delay).UnixMilli()
		data, err := persis.Encode(job)
		if err != nil {
			return err
		}
		return q.col.CompareAndSwap(ctx, record.ID, record.Data, data)
	})
}

func (q *CleanupQueue) updateClaim(
	ctx context.Context,
	ownerWorkerID, id, claimToken string,
	update func(*persis.Record, *CleanupJob) error,
) error {
	if q == nil || q.col == nil {
		return errors.New("agent session cleanup queue is not configured")
	}
	record, err := q.col.Get(ctx, id)
	if err != nil {
		return err
	}
	var job CleanupJob
	if err := persis.Decode(record, &job); err != nil {
		return err
	}
	if job.ClaimedBy != ownerWorkerID || claimToken == "" || job.ClaimToken != claimToken {
		return persis.ErrConflict
	}
	return update(record, &job)
}

func (q *CleanupQueue) visit(ctx context.Context, visit func(*persis.Record, CleanupJob) (bool, error)) error {
	query := persis.ListQuery{Limit: 100}
	for {
		page, err := q.col.List(ctx, query)
		if err != nil {
			return err
		}
		for _, record := range page.Records {
			var job CleanupJob
			if err := persis.Decode(record, &job); err != nil {
				return err
			}
			stop, err := visit(record, job)
			if err != nil || stop {
				return err
			}
		}
		if page.NextCursor == "" {
			return nil
		}
		query.Cursor = page.NextCursor
	}
}

func cleanupJobID(root ir.DAGRunRef, resource ir.AgentSessionResource) string {
	sum := sha256.Sum256([]byte(root.Name + "\x00" + root.ID + "\x00" + resource.Provider + "\x00" + resource.OwnerWorkerID + "\x00" + resource.SessionID))
	return hex.EncodeToString(sum[:])
}
