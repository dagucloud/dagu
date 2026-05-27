// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package proc

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/internal/cmn/fileutil"
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
	requireHeartbeatAdvance(t, procFile)
	assertNoJSONFiles(t, root)

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

func requireHeartbeatAdvance(t *testing.T, procFile string) {
	t.Helper()

	initialValue, initialModTime := readHeartbeat(t, procFile)
	require.Eventually(t, func() bool {
		value, modTime := readHeartbeat(t, procFile)
		return value > initialValue || modTime.After(initialModTime)
	}, time.Second, 10*time.Millisecond)
}

func readHeartbeat(t *testing.T, procFile string) (int64, time.Time) {
	t.Helper()

	deadline := time.Now().Add(time.Second)
	for {
		data, err := fileutil.ReadFile(procFile)
		if err == nil {
			require.GreaterOrEqual(t, len(data), 8)
			info, statErr := os.Stat(procFile)
			if statErr == nil {
				return int64(binary.BigEndian.Uint64(data[:8])), info.ModTime()
			}
			err = statErr
		}
		if !fileutil.IsTransientFileError(err) || time.Now().After(deadline) {
			require.NoError(t, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func assertNoJSONFiles(t *testing.T, root string) {
	t.Helper()

	require.NoError(t, filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		require.NoError(t, err)
		if !d.IsDir() {
			assert.NotEqual(t, ".json", filepath.Ext(path), "file-backed proc store must not create collection JSON records")
		}
		return nil
	}))
}
