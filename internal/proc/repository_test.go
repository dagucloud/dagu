// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package proc

import (
	"context"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRepositoryAcquireDefaultsAndValidatesMetadata(t *testing.T) {
	t.Parallel()

	store := &repositoryTestStore{handle: repositoryTestHandle{}}
	repository := NewRepository(store)
	now := time.Date(2026, time.August, 13, 1, 2, 3, 0, time.UTC)
	repository.now = func() time.Time {
		return now
	}

	handle, err := repository.Acquire(t.Context(), "queue-a", ProcMeta{
		Name:      "daily",
		DAGRunID:  "run-1",
		AttemptID: "attempt-1",
	})
	require.NoError(t, err)
	assert.Equal(t, repositoryTestHandle{}, handle)
	assert.Equal(t, now.Unix(), store.acquired.StartedAt)

	_, err = repository.Acquire(t.Context(), "queue-a", ProcMeta{
		Name:      "daily",
		DAGRunID:  "run-2",
		AttemptID: "invalid/attempt",
	})
	require.Error(t, err)
	assert.Equal(t, 1, store.acquireCalls)
}

func TestRepositoryLivenessQueries(t *testing.T) {
	t.Parallel()

	queueEntries := []ProcEntry{
		procEntry("queue-a", "alpha", "run-2", "attempt-2", 20, 25, true),
		procEntry("queue-a", "alpha", "run-1", "attempt-1", 10, 15, true),
		procEntry("queue-a", "alpha", "run-1", "attempt-retry", 11, 16, true),
		procEntry("queue-a", "beta", "run-3", "attempt-3", 30, 35, false),
	}
	store := &repositoryTestStore{
		entries: map[string][]ProcEntry{"queue-a": queueEntries},
		all: append(append([]ProcEntry{}, queueEntries...),
			procEntry("queue-b", "gamma", "run-4", "attempt-4", 40, 45, true)),
	}
	repository := NewRepository(store)

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

func TestProcMetaValidate(t *testing.T) {
	t.Parallel()

	valid := ProcMeta{
		StartedAt:    1,
		Name:         "daily",
		DAGRunID:     "run-1",
		AttemptID:    "attempt-1",
		RootName:     "root",
		RootDAGRunID: "root-run",
	}
	require.NoError(t, valid.Validate())

	tests := []struct {
		name   string
		mutate func(*ProcMeta)
	}{
		{name: "missing name", mutate: func(meta *ProcMeta) { meta.Name = "" }},
		{name: "invalid run ID", mutate: func(meta *ProcMeta) { meta.DAGRunID = "bad/run" }},
		{name: "missing attempt ID", mutate: func(meta *ProcMeta) { meta.AttemptID = "" }},
		{name: "unsafe attempt ID", mutate: func(meta *ProcMeta) { meta.AttemptID = "bad/attempt" }},
		{name: "missing start time", mutate: func(meta *ProcMeta) { meta.StartedAt = 0 }},
		{name: "partial root", mutate: func(meta *ProcMeta) { meta.RootName = "" }},
		{name: "invalid root run ID", mutate: func(meta *ProcMeta) { meta.RootDAGRunID = "bad/root" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			meta := valid
			test.mutate(&meta)
			require.Error(t, meta.Validate())
		})
	}
}

func procEntry(groupName, dagName, runID, attemptID string, startedAt, heartbeatAt int64, fresh bool) ProcEntry {
	return ProcEntry{
		GroupName: groupName,
		Meta: ProcMeta{
			StartedAt: startedAt,
			Name:      dagName,
			DAGRunID:  runID,
			AttemptID: attemptID,
		},
		LastHeartbeatAt: heartbeatAt,
		Fresh:           fresh,
	}
}

type repositoryTestStore struct {
	entries map[string][]ProcEntry
	all     []ProcEntry
	handle  ProcHandle

	acquired     ProcMeta
	acquireCalls int
}

func (*repositoryTestStore) Validate(context.Context) error { return nil }

func (*repositoryTestStore) WithLock(_ context.Context, _ string, fn func() error) error {
	return fn()
}

func (s *repositoryTestStore) Acquire(_ context.Context, _ string, meta ProcMeta) (ProcHandle, error) {
	s.acquired = meta
	s.acquireCalls++
	return s.handle, nil
}

func (s *repositoryTestStore) ListEntries(_ context.Context, groupName string) ([]ProcEntry, error) {
	return append([]ProcEntry(nil), s.entries[groupName]...), nil
}

func (*repositoryTestStore) LatestHeartbeat(context.Context, string, ir.DAGRunRef) (*ProcHeartbeat, error) {
	return nil, nil
}

func (s *repositoryTestStore) ListAllEntries(context.Context) ([]ProcEntry, error) {
	return append([]ProcEntry(nil), s.all...), nil
}

func (*repositoryTestStore) RemoveIfStale(context.Context, ProcEntry) error { return nil }

type repositoryTestHandle struct{}

func (repositoryTestHandle) Stop(context.Context) error { return nil }
