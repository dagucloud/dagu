// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package proc

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/internal/core/exec"
)

func testProcMeta(ref exec.DAGRunRef) exec.ProcMeta {
	return exec.ProcMeta{
		StartedAt:    time.Now().UTC().Unix(),
		Name:         ref.Name,
		DAGRunID:     ref.ID,
		AttemptID:    "attempt_" + ref.ID,
		RootName:     ref.Name,
		RootDAGRunID: ref.ID,
	}
}

func TestStoreWritesReleasedProcFileLayoutOnly(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	s := New(root,
		WithHeartbeatInterval(10*time.Millisecond),
		WithHeartbeatSyncInterval(10*time.Millisecond),
	)
	ref := exec.NewDAGRunRef("sidecar-dag", "run-1")

	handle, err := s.Acquire(ctx, "queue-a", testProcMeta(ref))
	require.NoError(t, err)
	defer func() { _ = handle.Stop(ctx) }()

	procFile := waitForProcFile(t, root, "queue-a", "sidecar-dag")
	require.NotEmpty(t, procFile)

	entries, err := s.ListEntries(ctx, "queue-a")
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "queue-a", entries[0].GroupName)
	assert.Equal(t, ref, entries[0].Meta.DAGRun())
	assert.False(t, entries[0].Identity.IsZero())
	assert.True(t, entries[0].Fresh)

	count, err := s.CountAlive(ctx, "queue-a")
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	heartbeat, err := s.LatestHeartbeat(ctx, "queue-a", ref)
	require.NoError(t, err)
	require.NotNil(t, heartbeat)
	assert.Equal(t, ref, heartbeat.DAGRun)
	assert.True(t, heartbeat.Fresh)

	require.NoError(t, handle.Stop(ctx))
	matches, err := filepath.Glob(filepath.Join(root, "queue-a", "sidecar-dag", "proc_*.proc"))
	require.NoError(t, err)
	assert.Empty(t, matches)
}

func TestStoreReadsAndRemovesReleasedProcFiles(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	s := New(root, WithStaleThreshold(10*time.Millisecond))
	ref := exec.NewDAGRunRef("released-dag", "run-1")
	meta := testProcMeta(ref)
	staleAt := time.Now().Add(-time.Hour).UTC()
	procFile := s.filePath("queue-a", meta, staleAt)
	require.NoError(t, writeProcFile(procFile, staleAt.Unix(), meta))
	require.NoError(t, os.Chtimes(procFile, staleAt, staleAt))

	entries, err := s.ListEntries(ctx, "queue-a")
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.False(t, entries[0].Fresh)
	assert.False(t, entries[0].Identity.IsZero())
	assert.NotEqual(t, procFile, entries[0].Identity.String())

	require.NoError(t, s.RemoveIfStale(ctx, entries[0]))
	_, err = os.Stat(procFile)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func waitForProcFile(t *testing.T, root, groupName, dagName string) string {
	t.Helper()

	var match string
	require.Eventually(t, func() bool {
		matches, err := filepath.Glob(filepath.Join(root, groupName, dagName, "proc_*.proc"))
		require.NoError(t, err)
		if len(matches) == 0 {
			return false
		}
		match = matches[0]
		return true
	}, time.Second, 10*time.Millisecond)
	return match
}
