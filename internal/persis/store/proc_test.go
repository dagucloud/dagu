// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store_test

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis"
	"github.com/dagucloud/dagu/internal/persis/file"
	"github.com/dagucloud/dagu/internal/persis/store"
	"github.com/dagucloud/dagu/internal/persis/testutil"
)

func newProcStore(t *testing.T, opts ...store.ProcStoreOption) *store.ProcStore {
	t.Helper()
	return store.NewProcStore(testutil.NewMemoryBackend().Collection("proc_entries"), opts...)
}

func procMeta(ref exec.DAGRunRef) exec.ProcMeta {
	return exec.ProcMeta{
		StartedAt:    time.Now().UTC().Unix(),
		Name:         ref.Name,
		DAGRunID:     ref.ID,
		AttemptID:    "attempt_" + ref.ID,
		RootName:     ref.Name,
		RootDAGRunID: ref.ID,
	}
}

func TestProcStoreAcquireStop(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := newProcStore(t)
	ref := exec.NewDAGRunRef("proc-dag", "run-1")

	proc, err := s.Acquire(ctx, "queue-a", procMeta(ref))
	require.NoError(t, err)

	count, err := s.CountAlive(ctx, "queue-a")
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	entries, err := s.ListEntries(ctx, "queue-a")
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.True(t, entries[0].Fresh)
	assert.Equal(t, ref, entries[0].DAGRun())
	assert.False(t, entries[0].Identity.IsZero())

	require.NoError(t, proc.Stop(ctx))

	count, err = s.CountAlive(ctx, "queue-a")
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}

func TestProcStoreHeartbeatAdvances(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := newProcStore(t,
		store.WithProcHeartbeatInterval(10*time.Millisecond),
		store.WithProcHeartbeatSyncInterval(10*time.Millisecond),
	)
	ref := exec.NewDAGRunRef("heartbeat-dag", "run-1")

	proc, err := s.Acquire(ctx, "queue-a", procMeta(ref))
	require.NoError(t, err)
	defer func() { _ = proc.Stop(ctx) }()

	first, err := s.LatestHeartbeat(ctx, "queue-a", ref)
	require.NoError(t, err)
	require.NotNil(t, first)

	timeout := max(time.Until(time.Unix(first.LastHeartbeatAt+1, 0).Add(500*time.Millisecond)), 500*time.Millisecond)
	require.Eventually(t, func() bool {
		next, err := s.LatestHeartbeat(ctx, "queue-a", ref)
		require.NoError(t, err)
		return next != nil && next.AdvancedSince(*first)
	}, timeout, 10*time.Millisecond)
}

func TestProcHandleRemovesEntryWhenContextCanceled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	s := newProcStore(t,
		store.WithProcHeartbeatInterval(time.Hour),
		store.WithProcStaleThreshold(time.Hour),
	)
	ref := exec.NewDAGRunRef("cancel-dag", "run-1")

	_, err := s.Acquire(ctx, "queue-a", procMeta(ref))
	require.NoError(t, err)

	count, err := s.CountAlive(context.Background(), "queue-a")
	require.NoError(t, err)
	require.Equal(t, 1, count)

	cancel()

	require.Eventually(t, func() bool {
		count, err := s.CountAlive(context.Background(), "queue-a")
		require.NoError(t, err)
		return count == 0
	}, time.Second, 10*time.Millisecond)
}

func TestProcHandleStopCleansUpWithCanceledContext(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	col := cancelAwareDeleteCollection{Collection: testutil.NewMemoryBackend().Collection("proc_entries")}
	s := store.NewProcStore(col)
	ref := exec.NewDAGRunRef("stop-cancel-dag", "run-1")

	proc, err := s.Acquire(ctx, "queue-a", procMeta(ref))
	require.NoError(t, err)

	stopCtx, cancel := context.WithCancel(ctx)
	cancel()
	require.NoError(t, proc.Stop(stopCtx))

	count, err := s.CountAlive(ctx, "queue-a")
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}

func TestProcStoreRemoveIfStale(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := newProcStore(t,
		store.WithProcStaleThreshold(20*time.Millisecond),
		store.WithProcHeartbeatInterval(time.Hour),
	)
	ref := exec.NewDAGRunRef("stale-dag", "run-1")

	proc, err := s.Acquire(ctx, "queue-a", procMeta(ref))
	require.NoError(t, err)
	defer func() { _ = proc.Stop(ctx) }()

	var stale exec.ProcEntry
	require.Eventually(t, func() bool {
		entries, err := s.ListEntries(ctx, "queue-a")
		require.NoError(t, err)
		if len(entries) != 1 || entries[0].Fresh {
			return false
		}
		stale = entries[0]
		return true
	}, time.Second, 10*time.Millisecond)

	require.NoError(t, s.RemoveIfStale(ctx, stale))

	entries, err := s.ListEntries(ctx, "queue-a")
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestProcStoreRemoveIfStaleKeepsRefreshedCollectionRecord(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	base := testutil.NewMemoryBackend().Collection("proc_entries")
	col := &refreshBeforeCompareDeleteCollection{Collection: base}
	s := store.NewProcStore(col,
		store.WithProcStaleThreshold(20*time.Millisecond),
		store.WithProcHeartbeatInterval(time.Hour),
	)
	ref := exec.NewDAGRunRef("stale-refresh-dag", "run-1")

	proc, err := s.Acquire(ctx, "queue-a", procMeta(ref))
	require.NoError(t, err)
	defer func() { _ = proc.Stop(ctx) }()

	var stale exec.ProcEntry
	require.Eventually(t, func() bool {
		entries, err := s.ListEntries(ctx, "queue-a")
		require.NoError(t, err)
		if len(entries) != 1 || entries[0].Fresh {
			return false
		}
		stale = entries[0]
		return true
	}, time.Second, 10*time.Millisecond)

	col.refresh = func(expected *persis.Record) {
		rec, err := base.Get(ctx, expected.ID)
		require.NoError(t, err)
		rec.UpdatedAt = time.Now().UTC()
		require.NoError(t, base.Put(ctx, rec))
	}

	require.NoError(t, s.RemoveIfStale(ctx, stale))
	entries, err := s.ListEntries(ctx, "queue-a")
	require.NoError(t, err)
	require.Len(t, entries, 1)
}

func TestProcStoreRemoveIfStaleIgnoresEntryWithoutStoreIdentity(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := newProcStore(t,
		store.WithProcStaleThreshold(20*time.Millisecond),
		store.WithProcHeartbeatInterval(time.Hour),
	)
	ref := exec.NewDAGRunRef("stale-no-identity-dag", "run-1")

	proc, err := s.Acquire(ctx, "queue-a", procMeta(ref))
	require.NoError(t, err)
	defer func() { _ = proc.Stop(ctx) }()

	var stale exec.ProcEntry
	require.Eventually(t, func() bool {
		entries, err := s.ListEntries(ctx, "queue-a")
		require.NoError(t, err)
		if len(entries) != 1 || entries[0].Fresh {
			return false
		}
		stale = entries[0]
		return true
	}, time.Second, 10*time.Millisecond)

	for _, tc := range []struct {
		name     string
		identity exec.ProcEntryID
	}{
		{name: "zero", identity: exec.ProcEntryID{}},
		{name: "missing separator", identity: exec.NewProcEntryID("plain-file.proc")},
		{name: "bad encoding", identity: exec.NewProcEntryID("collection:not base64")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entry := stale
			entry.Identity = tc.identity
			require.NoError(t, s.RemoveIfStale(ctx, entry))

			entries, err := s.ListEntries(ctx, "queue-a")
			require.NoError(t, err)
			require.Len(t, entries, 1)
		})
	}
}

func TestProcStoreLatestFreshEntryByDAGName(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := newProcStore(t)

	older := procMeta(exec.NewDAGRunRef("latest-dag", "run-older"))
	older.StartedAt = 100
	newer := procMeta(exec.NewDAGRunRef("latest-dag", "run-newer"))
	newer.StartedAt = 200

	proc1, err := s.Acquire(ctx, "queue-a", older)
	require.NoError(t, err)
	defer func() { _ = proc1.Stop(ctx) }()
	proc2, err := s.Acquire(ctx, "queue-a", newer)
	require.NoError(t, err)
	defer func() { _ = proc2.Stop(ctx) }()

	entry, err := s.LatestFreshEntryByDAGName(ctx, "queue-a", "latest-dag")
	require.NoError(t, err)
	require.NotNil(t, entry)
	assert.Equal(t, "run-newer", entry.Meta.DAGRunID)
}

func TestProcStoreLockWaitsForSameProcessContention(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := newProcStore(t)

	require.NoError(t, s.Lock(ctx, "queue-a"))

	started := make(chan struct{})
	acquired := make(chan error, 1)
	go func() {
		close(started)
		acquired <- s.Lock(ctx, "queue-a")
	}()
	<-started

	select {
	case err := <-acquired:
		require.Failf(t, "second lock returned before unlock", "err: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	s.Unlock(ctx, "queue-a")
	require.NoError(t, <-acquired)
	s.Unlock(ctx, "queue-a")
}

func TestProcStoreBackendLockReturnsCanceledContextBeforeAcquired(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	col := contextIgnoringLockCollection{Collection: testutil.NewMemoryBackend().Collection("proc_entries")}
	s := store.NewProcStore(col)

	require.ErrorIs(t, s.Lock(ctx, "queue-a"), context.Canceled)
	require.NoError(t, s.Lock(context.Background(), "queue-a"))
	s.Unlock(context.Background(), "queue-a")
}

func TestProcStoreRejectsFutureCollectionHeartbeat(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	col := testutil.NewMemoryBackend().Collection("proc_entries")
	s := store.NewProcStore(col)
	meta := procMeta(exec.NewDAGRunRef("future-dag", "run-1"))
	data, err := json.Marshal(map[string]any{
		"version":         1,
		"groupName":       "queue-a",
		"meta":            meta,
		"lastHeartbeatAt": time.Now().Add(10 * time.Minute).Unix(),
	})
	require.NoError(t, err)

	now := time.Now().UTC()
	require.NoError(t, col.Put(ctx, &persis.Record{
		ID:        "queue-a/future-dag/proc_future",
		Encoding:  persis.EncodingJSON,
		Data:      data,
		CreatedAt: now,
		UpdatedAt: now,
	}))

	_, err = s.ListEntries(ctx, "queue-a")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "future")
}

func TestProcStoreRejectsCollectionRecordGroupMismatch(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	col := testutil.NewMemoryBackend().Collection("proc_entries")
	s := store.NewProcStore(col)
	meta := procMeta(exec.NewDAGRunRef("mismatch-dag", "run-1"))
	data, err := json.Marshal(map[string]any{
		"version":         1,
		"groupName":       "queue-b",
		"meta":            meta,
		"lastHeartbeatAt": time.Now().Unix(),
	})
	require.NoError(t, err)

	now := time.Now().UTC()
	require.NoError(t, col.Put(ctx, &persis.Record{
		ID:        "queue-a/mismatch-dag/proc_mismatch",
		Encoding:  persis.EncodingJSON,
		Data:      data,
		CreatedAt: now,
		UpdatedAt: now,
	}))

	_, err = s.CountAlive(ctx, "queue-a")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "record group mismatch")
}

func TestProcStoreFileBackendSurfacesCorruptCollectionRecord(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "queue-a"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(root, "queue-a", "corrupt.json"), []byte("{"), 0o600))

	s := store.NewProcStore(file.NewCollection(root))

	_, err := s.CountAlive(ctx, "queue-a")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "corrupt record")

	err = s.Validate(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "corrupt record")
}

func TestProcStoreLatestHeartbeatSkipsCorruptCollectionRecords(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	col := testutil.NewMemoryBackend().Collection("proc_entries")
	s := store.NewProcStore(col)
	ref := exec.NewDAGRunRef("collection-corrupt-dag", "run-1")

	proc, err := s.Acquire(ctx, "queue-a", procMeta(ref))
	require.NoError(t, err)
	defer func() { _ = proc.Stop(ctx) }()

	otherMeta := procMeta(exec.NewDAGRunRef(ref.Name, "run-other"))
	now := time.Now().UTC()
	require.NoError(t, col.Put(ctx, &persis.Record{
		ID:        procRecordIDForTest("queue-a", otherMeta, now),
		Encoding:  persis.EncodingJSON,
		Data:      []byte("{"),
		CreatedAt: now,
		UpdatedAt: now,
	}))

	heartbeat, err := s.LatestHeartbeat(ctx, "queue-a", ref)
	require.NoError(t, err)
	require.NotNil(t, heartbeat)
	assert.Equal(t, ref, heartbeat.DAGRun)
}

func procRecordIDForTest(groupName string, meta exec.ProcMeta, t time.Time) string {
	return filepath.ToSlash(filepath.Join(
		groupName,
		meta.Name,
		"proc_"+t.UTC().Format("20060102_150405")+"Z_"+
			hex.EncodeToString([]byte(meta.DAGRunID))+"_"+
			hex.EncodeToString([]byte(meta.AttemptID)),
	))
}

type contextIgnoringLockCollection struct {
	persis.Collection
}

func (c contextIgnoringLockCollection) WithLock(_ context.Context, _ string, fn func() error) error {
	return fn()
}

type cancelAwareDeleteCollection struct {
	persis.Collection
}

func (c cancelAwareDeleteCollection) Delete(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.Collection.Delete(ctx, id)
}

type refreshBeforeCompareDeleteCollection struct {
	persis.Collection
	once    sync.Once
	refresh func(*persis.Record)
}

func (c *refreshBeforeCompareDeleteCollection) CompareAndDelete(ctx context.Context, expected *persis.Record) error {
	c.once.Do(func() {
		if c.refresh != nil {
			c.refresh(expected)
		}
	})
	return c.Collection.CompareAndDelete(ctx, expected)
}
