// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store_test

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/internal/cmn/fileutil"
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
	assert.NotEmpty(t, entries[0].FilePath)

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

func TestProcStoreAcquireCleansRecordWhenLegacyWriteFails(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	legacyRoot := filepath.Join(t.TempDir(), "legacy-root")
	require.NoError(t, os.WriteFile(legacyRoot, []byte("not a directory"), 0o600))
	col := testutil.NewMemoryBackend().Collection("proc_entries")
	s := store.NewProcStore(col, store.WithProcLegacyDir(legacyRoot))
	ref := exec.NewDAGRunRef("failed-legacy-dag", "run-1")

	proc, err := s.Acquire(ctx, "queue-a", procMeta(ref))
	require.Error(t, err)
	require.Nil(t, proc)

	page, listErr := col.List(ctx, persis.ListQuery{Prefix: "queue-a/"})
	require.NoError(t, listErr)
	assert.Empty(t, page.Records)
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

	col.refresh = func() {
		rec, err := base.Get(ctx, stale.FilePath)
		require.NoError(t, err)
		rec.UpdatedAt = time.Now().UTC()
		require.NoError(t, base.Put(ctx, rec))
	}

	require.NoError(t, s.RemoveIfStale(ctx, stale))
	_, err = base.Get(ctx, stale.FilePath)
	require.NoError(t, err)
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

func TestProcStoreFileBackendWritesLegacySidecar(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	s := store.NewProcStore(file.NewCollection(root),
		store.WithProcLegacyDir(root),
		store.WithProcHeartbeatInterval(10*time.Millisecond),
		store.WithProcHeartbeatSyncInterval(10*time.Millisecond),
	)
	ref := exec.NewDAGRunRef("sidecar-dag", "run-1")

	proc, err := s.Acquire(ctx, "queue-a", procMeta(ref))
	require.NoError(t, err)
	defer func() { _ = proc.Stop(ctx) }()

	procFile := waitForLegacyProcFile(t, root, "queue-a", "sidecar-dag")
	requireLegacyHeartbeatAdvance(t, procFile)

	count, err := s.CountAlive(ctx, "queue-a")
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	require.NoError(t, proc.Stop(ctx))

	matches, err := filepath.Glob(filepath.Join(root, "queue-a", "sidecar-dag", "proc_*.proc"))
	require.NoError(t, err)
	assert.Empty(t, matches)
}

func TestProcStoreLatestHeartbeatPrefersCollectionRecordOverLegacySidecar(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	s := store.NewProcStore(file.NewCollection(root),
		store.WithProcLegacyDir(root),
		store.WithProcHeartbeatInterval(time.Hour),
	)
	ref := exec.NewDAGRunRef("sidecar-prefer-dag", "run-1")

	proc, err := s.Acquire(ctx, "queue-a", procMeta(ref))
	require.NoError(t, err)
	defer func() { _ = proc.Stop(ctx) }()

	procFile := waitForLegacyProcFile(t, root, "queue-a", "sidecar-prefer-dag")
	require.NoError(t, os.WriteFile(procFile, []byte("invalid legacy proc sidecar"), 0o600))

	heartbeat, err := s.LatestHeartbeat(ctx, "queue-a", ref)
	require.NoError(t, err)
	require.NotNil(t, heartbeat)
	assert.Equal(t, ref, heartbeat.DAGRun)
	assert.True(t, heartbeat.Fresh)
}

func TestProcStoreLatestHeartbeatFallsBackToFreshLegacyWhenCollectionRecordIsStale(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	col := file.NewCollection(root)
	s := store.NewProcStore(col,
		store.WithProcLegacyDir(root),
		store.WithProcStaleThreshold(time.Second),
	)
	ref := exec.NewDAGRunRef("sidecar-fallback-dag", "run-1")
	meta := procMeta(ref)
	staleAt := time.Now().Add(-time.Hour).UTC()
	freshAt := time.Now().UTC()

	data, err := json.Marshal(map[string]any{
		"version":         1,
		"groupName":       "queue-a",
		"meta":            meta,
		"lastHeartbeatAt": staleAt.Unix(),
	})
	require.NoError(t, err)
	require.NoError(t, col.Put(ctx, &persis.Record{
		ID:        "queue-a/sidecar-fallback-dag/proc_stale",
		Encoding:  persis.EncodingJSON,
		Data:      data,
		CreatedAt: staleAt,
		UpdatedAt: staleAt,
	}))
	_ = writeLegacyProcFile(t, root, "queue-a", meta, freshAt)

	heartbeat, err := s.LatestHeartbeat(ctx, "queue-a", ref)
	require.NoError(t, err)
	require.NotNil(t, heartbeat)
	assert.True(t, heartbeat.Fresh)
	assert.Equal(t, freshAt.Unix(), heartbeat.LastHeartbeatAt)
}

func TestProcStoreLatestHeartbeatFallsBackToLegacyWhenCollectionReadFails(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	col := listErrorCollection{Collection: testutil.NewMemoryBackend().Collection("proc_entries")}
	s := store.NewProcStore(col, store.WithProcLegacyDir(root))
	ref := exec.NewDAGRunRef("collection-error-dag", "run-1")
	meta := procMeta(ref)
	heartbeatAt := time.Now().UTC()
	_ = writeLegacyProcFile(t, root, "queue-a", meta, heartbeatAt)

	heartbeat, err := s.LatestHeartbeat(ctx, "queue-a", ref)
	require.NoError(t, err)
	require.NotNil(t, heartbeat)
	assert.Equal(t, ref, heartbeat.DAGRun)
	assert.Equal(t, heartbeatAt.Unix(), heartbeat.LastHeartbeatAt)
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

func TestProcStoreLatestHeartbeatSkipsCorruptLegacySidecars(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	s := store.NewProcStore(testutil.NewMemoryBackend().Collection("proc_entries"),
		store.WithProcLegacyDir(root),
	)
	ref := exec.NewDAGRunRef("legacy-corrupt-dag", "run-1")
	meta := procMeta(ref)
	heartbeatAt := time.Now().UTC()
	_ = writeLegacyProcFile(t, root, "queue-a", meta, heartbeatAt)

	otherMeta := procMeta(exec.NewDAGRunRef(ref.Name, "run-other"))
	corruptFile := writeLegacyProcFile(t, root, "queue-a", otherMeta, heartbeatAt)
	require.NoError(t, os.WriteFile(corruptFile, []byte("invalid legacy proc sidecar"), 0o600))

	heartbeat, err := s.LatestHeartbeat(ctx, "queue-a", ref)
	require.NoError(t, err)
	require.NotNil(t, heartbeat)
	assert.Equal(t, ref, heartbeat.DAGRun)
	assert.Equal(t, heartbeatAt.Unix(), heartbeat.LastHeartbeatAt)
}

func TestProcStoreReadsAndRemovesLegacyProcFiles(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	ref := exec.NewDAGRunRef("legacy-dag", "run-1")
	meta := procMeta(ref)
	legacyFile := writeLegacyProcFile(t, root, "queue-a", meta, time.Now().Add(-time.Hour))

	s := store.NewProcStore(file.NewCollection(root),
		store.WithProcLegacyDir(root),
		store.WithProcStaleThreshold(10*time.Millisecond),
	)

	entries, err := s.ListEntries(ctx, "queue-a")
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.False(t, entries[0].Fresh)
	assert.Equal(t, legacyFile, entries[0].FilePath)

	require.NoError(t, s.RemoveIfStale(ctx, entries[0]))

	_, err = os.Stat(legacyFile)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func waitForLegacyProcFile(t *testing.T, root, groupName, dagName string) string {
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

func requireLegacyHeartbeatAdvance(t *testing.T, procFile string) {
	t.Helper()

	initialValue, initialModTime := readLegacyHeartbeat(t, procFile)
	require.Eventually(t, func() bool {
		value, modTime := readLegacyHeartbeat(t, procFile)
		return value > initialValue || modTime.After(initialModTime)
	}, time.Second, 10*time.Millisecond)
}

func readLegacyHeartbeat(t *testing.T, procFile string) (int64, time.Time) {
	t.Helper()

	data, err := fileutil.ReadFile(procFile)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(data), 8)
	info, err := os.Stat(procFile)
	require.NoError(t, err)
	return int64(binary.BigEndian.Uint64(data[:8])), info.ModTime()
}

func writeLegacyProcFile(t *testing.T, root, groupName string, meta exec.ProcMeta, heartbeatAt time.Time) string {
	t.Helper()

	dir := filepath.Join(root, groupName, meta.Name)
	require.NoError(t, os.MkdirAll(dir, 0o750))
	fileName := "proc_" + heartbeatAt.UTC().Format("20060102_150405") + "Z_" +
		hex.EncodeToString([]byte(meta.DAGRunID)) + "_" +
		hex.EncodeToString([]byte(meta.AttemptID)) + ".proc"
	procFile := filepath.Join(dir, fileName)

	payload, err := json.Marshal(map[string]any{
		"version":         1,
		"dag_name":        meta.Name,
		"dag_run_id":      meta.DAGRunID,
		"attempt_id":      meta.AttemptID,
		"root_name":       meta.RootName,
		"root_dag_run_id": meta.RootDAGRunID,
		"started_at":      meta.StartedAt,
	})
	require.NoError(t, err)
	data := make([]byte, 8+len(payload))
	binary.BigEndian.PutUint64(data[:8], uint64(heartbeatAt.Unix())) //nolint:gosec // test timestamp.
	copy(data[8:], payload)
	require.NoError(t, os.WriteFile(procFile, data, 0o600))
	require.NoError(t, os.Chtimes(procFile, heartbeatAt, heartbeatAt))
	return procFile
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

type listErrorCollection struct {
	persis.Collection
}

func (c listErrorCollection) List(context.Context, persis.ListQuery) (*persis.Page, error) {
	return nil, errors.New("collection unavailable")
}

func (c listErrorCollection) RecordIDs(context.Context, string) ([]string, error) {
	return nil, errors.New("collection unavailable")
}

type refreshBeforeCompareDeleteCollection struct {
	persis.Collection
	once    sync.Once
	refresh func()
}

func (c *refreshBeforeCompareDeleteCollection) CompareAndDelete(ctx context.Context, expected *persis.Record) error {
	c.once.Do(func() {
		if c.refresh != nil {
			c.refresh()
		}
	})
	return c.Collection.CompareAndDelete(ctx, expected)
}
