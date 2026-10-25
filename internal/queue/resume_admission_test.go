// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package queue_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/queue"
	"github.com/dagucloud/dagu/v2/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResumeAdmissionDispatchRace(t *testing.T) {
	for _, order := range []string{"dispatch first", "recovery first"} {
		t.Run(order, func(t *testing.T) {
			fixture := pendingResumeAdmission(t)
			repository, queueStore, status := fixture.repository, fixture.queueStore, fixture.status
			_, err := queueStore.EnsureRetryAdmission(t.Context(), status.Name, queue.QueuePriorityLow, status.DAGRun(), admissionIdentity(status))
			require.NoError(t, err)
			items, err := queueStore.List(t.Context(), status.Name)
			require.NoError(t, err)
			require.Len(t, items, 1)
			otherRepository := testutil.NewFileDAGRunRepository(fixture.history, persis.DAGRunRepositoryOptions{})
			otherQueue := store.NewQueueStore(file.NewCollection(fixture.queueDir))
			dispatch := func() error {
				if err := queue.AcknowledgeRetryAdmission(t.Context(), otherRepository, items[0]); err != nil {
					return err
				}
				_, err := otherQueue.DeleteByItemIDs(t.Context(), status.Name, []string{items[0].ID()})
				return err
			}
			if order == "dispatch first" {
				require.NoError(t, dispatch())
				created, err := queue.EnqueueRetry(t.Context(), repository, queueStore, nil, status, queue.EnqueueRetryOptions{Resume: true})
				require.NoError(t, err)
				assert.False(t, created)
			} else {
				entered, release := make(chan struct{}), make(chan struct{})
				blocking := &blockedAdmissionStore{QueueStore: queueStore, entered: entered, release: release}
				recovered := make(chan error, 1)
				go func() {
					created, err := queue.EnqueueRetry(t.Context(), repository, blocking, nil, status, queue.EnqueueRetryOptions{Resume: true})
					assert.False(t, created, "the original item must be reused")
					recovered <- err
				}()
				<-entered
				dispatched := make(chan error, 1)
				go func() { dispatched <- dispatch() }()
				close(release)
				require.NoError(t, <-recovered)
				require.NoError(t, <-dispatched)
			}
			remaining, err := otherQueue.List(t.Context(), status.Name)
			require.NoError(t, err)
			assert.Empty(t, remaining)
			attempt, err := otherRepository.FindAttempt(t.Context(), status.DAGRun())
			require.NoError(t, err)
			latest, err := attempt.ReadStatus(t.Context())
			require.NoError(t, err)
			assert.Equal(t, ir.Queued, latest.Status, "distributed handoff may leave the source queued")
			assert.True(t, latest.ResumeAdmission.Admitted)
		})
	}
}

func TestResumeAdmissionAcknowledgedBeforeDelete(t *testing.T) {
	fixture := pendingResumeAdmission(t)
	repository, queueStore, status := fixture.repository, fixture.queueStore, fixture.status
	_, err := queueStore.EnsureRetryAdmission(t.Context(), status.Name, queue.QueuePriorityLow, status.DAGRun(), admissionIdentity(status))
	require.NoError(t, err)
	items, err := queueStore.List(t.Context(), status.Name)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.NoError(t, queue.AcknowledgeRetryAdmission(t.Context(), repository, items[0]))
	// Stopping after acknowledgement leaves an ordinary dispatchable item.
	created, err := queue.EnqueueRetry(t.Context(), repository, queueStore, nil, status, queue.EnqueueRetryOptions{Resume: true})
	require.NoError(t, err)
	assert.False(t, created)
	attempt, err := repository.FindAttempt(t.Context(), status.DAGRun())
	require.NoError(t, err)
	latest, err := attempt.ReadStatus(t.Context())
	require.NoError(t, err)
	assert.True(t, queue.MatchesRetryAdmission(items[0], latest))
	assert.True(t, latest.ResumeAdmission.Admitted)
	count, err := queueStore.Len(t.Context(), status.Name)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

func TestResumeAdmissionSuperseded(t *testing.T) {
	for _, change := range []string{"attempt", "generation", "aborted", "legacy"} {
		t.Run(change, func(t *testing.T) {
			fixture := pendingResumeAdmission(t)
			repository, queueStore, status := fixture.repository, fixture.queueStore, fixture.status
			if change == "attempt" {
				attempt, err := repository.CreateAttempt(t.Context(), &ir.DAG{Name: status.Name}, time.Now(), status.DAGRunID, persis.DAGRunCreateAttemptOptions{Retry: true})
				require.NoError(t, err)
				newStatus := *status
				newStatus.AttemptID = attempt.ID()
				newStatus.ResumeAdmission = &ir.ResumeAdmission{ID: "new-attempt-admission"}
				require.NoError(t, attempt.Open(t.Context()))
				require.NoError(t, attempt.Write(t.Context(), newStatus))
				require.NoError(t, attempt.Close(t.Context()))
			} else {
				_, swapped, err := repository.CompareAndSwapLatestAttemptStatus(t.Context(), status.DAGRun(), status.AttemptID, ir.Queued,
					func(latest *ir.DAGRunStatus) error {
						switch change {
						case "generation":
							latest.QueuedAt = "2026-10-22T02:00:00Z"
							latest.ResumeAdmission = &ir.ResumeAdmission{ID: "new-admission"}
						case "aborted":
							latest.Status = ir.Aborted
						case "legacy":
							latest.ResumeAdmission = nil
						}
						return nil
					}, persis.DAGRunCompareAndSwapOptions{})
				require.NoError(t, err)
				require.True(t, swapped)
			}
			created, err := queue.EnqueueRetry(t.Context(), repository, queueStore, nil, status, queue.EnqueueRetryOptions{Resume: true})
			if change == "legacy" {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, queue.ErrRetryStaleLatest)
			}
			assert.False(t, created)
			count, err := queueStore.Len(t.Context(), status.Name)
			require.NoError(t, err)
			assert.Zero(t, count)
		})
	}
}

func TestResumeAdmissionRollbackIdentity(t *testing.T) {
	status := &ir.DAGRunStatus{Name: "resume", DAGRunID: "run", AttemptID: "attempt", Status: ir.Waiting}
	backend := &stubDAGRunStore{status: cloneDAGRunStatus(status)}
	var originalID, queuedAt string
	backend.beforeCAS = func() {
		if backend.casCalls != 3 {
			return
		}
		// A different admission wins after publication fails, before rollback.
		// Keep its timestamp identical so only the durable identity separates it.
		require.NotNil(t, backend.status.ResumeAdmission)
		originalID, queuedAt = backend.status.ResumeAdmission.ID, backend.status.QueuedAt
		backend.status.ResumeAdmission = &ir.ResumeAdmission{ID: originalID + "-new"}
	}
	repository := persis.NewDAGRunRepository(backend, nil, persis.DAGRunRepositoryOptions{})
	failure := errors.New("queue write failed")
	created, err := queue.EnqueueRetry(t.Context(), repository, &failedAdmissionStore{err: failure}, nil, status, queue.EnqueueRetryOptions{Resume: true})
	require.ErrorIs(t, err, failure)
	require.ErrorIs(t, err, queue.ErrRetryStaleLatest)
	assert.False(t, created)
	require.NotEmpty(t, originalID)
	assert.Equal(t, ir.Queued, backend.status.Status)
	assert.Equal(t, queuedAt, backend.status.QueuedAt)
	require.NotNil(t, backend.status.ResumeAdmission)
	assert.Equal(t, originalID+"-new", backend.status.ResumeAdmission.ID)
}

func TestResumeAdmissionWriteFailure(t *testing.T) {
	for _, afterCreate := range []bool{false, true} {
		t.Run(map[bool]string{false: "before create", true: "after create"}[afterCreate], func(t *testing.T) {
			fixture := pendingResumeAdmission(t)
			repository, queueStore, status := fixture.repository, fixture.queueStore, fixture.status
			status, swapped, err := repository.CompareAndSwapLatestAttemptStatus(t.Context(), status.DAGRun(), status.AttemptID, ir.Queued,
				func(latest *ir.DAGRunStatus) error {
					latest.Status, latest.Resume, latest.ResumeAdmission = ir.Waiting, false, nil
					return nil
				}, persis.DAGRunCompareAndSwapOptions{})
			require.NoError(t, err)
			require.True(t, swapped)
			failure := errors.New("queue write failed")
			failing := &failedAdmissionStore{QueueStore: queueStore, afterCreate: afterCreate, err: failure}
			created, err := queue.EnqueueRetry(t.Context(), repository, failing, nil, status, queue.EnqueueRetryOptions{Resume: true})
			require.ErrorIs(t, err, failure)
			assert.False(t, created)
			attempt, err := repository.FindAttempt(t.Context(), status.DAGRun())
			require.NoError(t, err)
			latest, err := attempt.ReadStatus(t.Context())
			require.NoError(t, err)
			assert.Equal(t, ir.Waiting, latest.Status)
			assert.Nil(t, latest.ResumeAdmission)
			created, err = queue.EnqueueRetry(t.Context(), repository, queueStore, nil, latest, queue.EnqueueRetryOptions{Resume: true})
			require.NoError(t, err)
			assert.True(t, created)
			latest, err = attempt.ReadStatusUncached(t.Context())
			require.NoError(t, err)
			items, err := queueStore.List(t.Context(), status.Name)
			require.NoError(t, err)
			matching := 0
			for _, item := range items {
				if queue.MatchesRetryAdmission(item, latest) {
					matching++
				}
			}
			assert.Equal(t, 1, matching, "an uncertain write cannot execute against the next generation")
		})
	}
}

type resumeAdmissionFixture struct {
	repository *persis.DAGRunRepository
	queueStore *store.QueueStore
	status     *ir.DAGRunStatus
	history    string
	queueDir   string
}

func pendingResumeAdmission(t *testing.T) resumeAdmissionFixture {
	t.Helper()
	root := t.TempDir()
	history, queueDir := filepath.Join(root, "history"), filepath.Join(root, "queue")
	repository := testutil.NewFileDAGRunRepository(history, persis.DAGRunRepositoryOptions{})
	dag := &ir.DAG{Name: "resume"}
	attempt, err := repository.CreateAttempt(t.Context(), dag, time.Now(), "run", persis.DAGRunCreateAttemptOptions{})
	require.NoError(t, err)
	status := &ir.DAGRunStatus{Name: dag.Name, DAGRunID: "run", AttemptID: attempt.ID(), Status: ir.Queued,
		Resume: true, QueuedAt: "2026-10-22T01:02:03Z", ResumeAdmission: &ir.ResumeAdmission{ID: "admission-1"}}
	require.NoError(t, attempt.Open(t.Context()))
	require.NoError(t, attempt.Write(t.Context(), *status))
	require.NoError(t, attempt.Close(t.Context()))
	return resumeAdmissionFixture{repository, store.NewQueueStore(file.NewCollection(queueDir)), status, history, queueDir}
}

func admissionIdentity(status *ir.DAGRunStatus) queue.RetryAdmissionIdentity {
	return queue.RetryAdmissionIdentity{ID: status.ResumeAdmission.ID, AttemptID: status.AttemptID,
		AttemptKey: status.AttemptKey, QueuedAt: status.QueuedAt, Root: status.Root}
}

type blockedAdmissionStore struct {
	*store.QueueStore
	entered chan struct{}
	release chan struct{}
}

func (s *blockedAdmissionStore) EnsureRetryAdmission(ctx context.Context, name string, priority queue.QueuePriority, ref ir.DAGRunRef, identity queue.RetryAdmissionIdentity) (bool, error) {
	close(s.entered)
	<-s.release
	return s.QueueStore.EnsureRetryAdmission(ctx, name, priority, ref, identity)
}

type failedAdmissionStore struct {
	*store.QueueStore
	afterCreate bool
	err         error
}

func (s *failedAdmissionStore) EnsureRetryAdmission(ctx context.Context, name string, priority queue.QueuePriority, ref ir.DAGRunRef, identity queue.RetryAdmissionIdentity) (bool, error) {
	if s.afterCreate {
		if _, err := s.QueueStore.EnsureRetryAdmission(ctx, name, priority, ref, identity); err != nil {
			return false, err
		}
	}
	return false, s.err
}
