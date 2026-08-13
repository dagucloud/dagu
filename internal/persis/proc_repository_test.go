// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package persis

import (
	"context"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/proc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProcRepositoryAcquireDefaultsAndValidatesMetadata(t *testing.T) {
	t.Parallel()

	store := &procRepositoryTestStore{handle: procRepositoryTestHandle{}}
	repository := NewProcRepository(store)
	now := time.Date(2026, time.August, 13, 1, 2, 3, 0, time.UTC)
	repository.now = func() time.Time {
		return now
	}

	handle, err := repository.Acquire(t.Context(), "queue-a", proc.ProcMeta{
		Name:      "daily",
		DAGRunID:  "run-1",
		AttemptID: "attempt-1",
	})
	require.NoError(t, err)
	assert.Equal(t, procRepositoryTestHandle{}, handle)
	assert.Equal(t, now.Unix(), store.acquired.StartedAt)

	_, err = repository.Acquire(t.Context(), "queue-a", proc.ProcMeta{
		Name:      "daily",
		DAGRunID:  "run-2",
		AttemptID: "invalid/attempt",
	})
	require.Error(t, err)
	assert.Equal(t, 1, store.acquireCalls)
}

func TestProcRepositoryLivenessQueries(t *testing.T) {
	t.Parallel()

	queueEntries := []proc.ProcEntry{
		newProcRepositoryTestEntry("queue-a", "alpha", "run-2", "attempt-2", 20, 25, true),
		newProcRepositoryTestEntry("queue-a", "alpha", "run-1", "attempt-1", 10, 15, true),
		newProcRepositoryTestEntry("queue-a", "alpha", "run-1", "attempt-retry", 11, 16, true),
		newProcRepositoryTestEntry("queue-a", "beta", "run-3", "attempt-3", 30, 35, false),
	}
	store := &procRepositoryTestStore{
		entries: map[string][]proc.ProcEntry{"queue-a": queueEntries},
		all: append(append([]proc.ProcEntry{}, queueEntries...),
			newProcRepositoryTestEntry("queue-b", "gamma", "run-4", "attempt-4", 40, 45, true)),
	}
	repository := NewProcRepository(store)

	count, err := repository.CountAlive(t.Context(), "queue-a")
	require.NoError(t, err)
	assert.Equal(t, 2, count)

	count, err = repository.CountAliveByDAGName(t.Context(), "queue-a", "alpha")
	require.NoError(t, err)
	assert.Equal(t, 2, count)

	alive, err := repository.IsRunAlive(t.Context(), "queue-a", ir.NewDAGRunRef("alpha", "run-1"))
	require.NoError(t, err)
	assert.True(t, alive)

	alive, err = repository.IsAttemptAlive(t.Context(), "queue-a", ir.NewDAGRunRef("alpha", "run-1"), "attempt-retry")
	require.NoError(t, err)
	assert.True(t, alive)

	refs, err := repository.ListAlive(t.Context(), "queue-a")
	require.NoError(t, err)
	assert.Equal(t, []ir.DAGRunRef{
		ir.NewDAGRunRef("alpha", "run-1"),
		ir.NewDAGRunRef("alpha", "run-2"),
	}, refs)

	all, err := repository.ListAllAlive(t.Context())
	require.NoError(t, err)
	assert.Equal(t, map[string][]ir.DAGRunRef{
		"queue-a": {
			ir.NewDAGRunRef("alpha", "run-1"),
			ir.NewDAGRunRef("alpha", "run-2"),
		},
		"queue-b": {ir.NewDAGRunRef("gamma", "run-4")},
	}, all)

	latest, err := repository.LatestFreshEntryByDAGName(t.Context(), "queue-a", "alpha")
	require.NoError(t, err)
	require.NotNil(t, latest)
	assert.Equal(t, "run-2", latest.Meta.DAGRunID)
}

func newProcRepositoryTestEntry(groupName, dagName, runID, attemptID string, startedAt, heartbeatAt int64, fresh bool) proc.ProcEntry {
	return proc.ProcEntry{
		GroupName: groupName,
		Meta: proc.ProcMeta{
			StartedAt: startedAt,
			Name:      dagName,
			DAGRunID:  runID,
			AttemptID: attemptID,
		},
		LastHeartbeatAt: heartbeatAt,
		Fresh:           fresh,
	}
}

type procRepositoryTestStore struct {
	entries map[string][]proc.ProcEntry
	all     []proc.ProcEntry
	handle  proc.ProcHandle

	acquired     proc.ProcMeta
	acquireCalls int
}

func (*procRepositoryTestStore) Validate(context.Context) error { return nil }

func (*procRepositoryTestStore) WithLock(_ context.Context, _ string, fn func() error) error {
	return fn()
}

func (s *procRepositoryTestStore) Acquire(_ context.Context, _ string, meta proc.ProcMeta) (proc.ProcHandle, error) {
	s.acquired = meta
	s.acquireCalls++
	return s.handle, nil
}

func (s *procRepositoryTestStore) ListEntries(_ context.Context, groupName string) ([]proc.ProcEntry, error) {
	return append([]proc.ProcEntry(nil), s.entries[groupName]...), nil
}

func (*procRepositoryTestStore) LatestHeartbeat(context.Context, string, ir.DAGRunRef) (*proc.ProcHeartbeat, error) {
	return nil, nil
}

func (s *procRepositoryTestStore) ListAllEntries(context.Context) ([]proc.ProcEntry, error) {
	return append([]proc.ProcEntry(nil), s.all...), nil
}

func (*procRepositoryTestStore) RemoveIfStale(context.Context, proc.ProcEntry) error { return nil }

type procRepositoryTestHandle struct{}

func (procRepositoryTestHandle) Stop(context.Context) error { return nil }
