// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package proc

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/proc"
)

func testProcMeta(ref ir.DAGRunRef) proc.ProcMeta {
	return proc.ProcMeta{
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
	s := New(root, WithHeartbeatInterval(10*time.Millisecond))
	repository := persis.NewProcRepository(s)
	ref := ir.NewDAGRunRef("sidecar-dag", "run-1")

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

	count, err := repository.CountAlive(ctx, "queue-a")
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

// Queue and DAG names that Windows cannot store as a directory, such as nul,
// db:writes, or nightly., still track their runs there. Linux and macOS keep
// the directories that earlier versions wrote, so a run in progress during
// an upgrade is still found.
func TestStoreWindowsUnsafeNames(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ group, dag string }{
		{group: "nul", dag: "nul"},
		{group: "db:writes", dag: "etl"},
		{group: "nightly.", dag: "nightly."},
	} {
		t.Run(tc.group, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			root := t.TempDir()
			s := New(root, WithHeartbeatInterval(10*time.Millisecond))
			ref := ir.NewDAGRunRef(tc.dag, "run-1")

			handle, err := s.Acquire(ctx, tc.group, testProcMeta(ref))
			require.NoError(t, err)
			defer func() { _ = handle.Stop(ctx) }()

			alive, err := persis.NewProcRepository(s).IsRunAlive(ctx, tc.group, ref)
			require.NoError(t, err)
			assert.True(t, alive)

			heartbeat, err := s.LatestHeartbeat(ctx, tc.group, ref)
			require.NoError(t, err)
			require.NotNil(t, heartbeat)
			assert.Equal(t, ref, heartbeat.DAGRun)

			entries, err := s.ListAllEntries(ctx)
			require.NoError(t, err)
			require.Len(t, entries, 1)
			assert.Equal(t, tc.group, entries[0].GroupName)
			assert.Equal(t, ref, entries[0].Meta.DAGRun())

			if runtime.GOOS != "windows" {
				waitForProcFile(t, root, tc.group, tc.dag)
			}
		})
	}
}

// A queue name with a "/" still lists its runs among all entries, under the
// queue's own name.
func TestStoreListsQueueWithSlash(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := New(t.TempDir(), WithHeartbeatInterval(10*time.Millisecond))
	ref := ir.NewDAGRunRef("etl", "run-1")

	handle, err := s.Acquire(ctx, "team/etl", testProcMeta(ref))
	require.NoError(t, err)
	defer func() { _ = handle.Stop(ctx) }()

	entries, err := s.ListAllEntries(ctx)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "team/etl", entries[0].GroupName)
	assert.Equal(t, ref, entries[0].Meta.DAGRun())
}

// Released versions named a group's directory after the raw queue name,
// nested at each "/", and a DAG's directory after the raw DAG name, which
// Windows 11 accepts even for a device name such as con. A run they started
// before an upgrade still heartbeats there, so it must stay alive and listed.
func TestStoreFindsRawNameHeartbeat(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ group, dag string }{
		{group: "team/etl", dag: "etl"},
		{group: "team/.hidden", dag: "etl"},
		{group: "50%", dag: "etl"},
		{group: "con", dag: "con"},
	} {
		group := tc.group
		t.Run(group, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			root := t.TempDir()
			s := New(root)
			ref := ir.NewDAGRunRef(tc.dag, "run-1")
			meta := testProcMeta(ref)
			now := time.Now().UTC()
			path := filepath.Join(root, filepath.FromSlash(group), ref.Name, procRecordName(meta, now)+procFileExt)
			require.NoError(t, writeProcFile(path, now.Unix(), meta))

			alive, err := persis.NewProcRepository(s).IsRunAlive(ctx, group, ref)
			require.NoError(t, err)
			assert.True(t, alive)

			heartbeat, err := s.LatestHeartbeat(ctx, group, ref)
			require.NoError(t, err)
			require.NotNil(t, heartbeat)
			assert.Equal(t, ref, heartbeat.DAGRun)

			entries, err := s.ListAllEntries(ctx)
			require.NoError(t, err)
			require.Len(t, entries, 1)
			assert.Equal(t, group, entries[0].GroupName)
		})
	}
}

// A DAG name may start with a dot, and may even match the name of the group
// lock's directory. Its run stays alive and listed while the lock is held.
func TestStoreDotPrefixedDAGName(t *testing.T) {
	t.Parallel()

	for _, name := range []string{".hidden", ".dagu_lock_extra", ".dagu_lock"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			s := New(t.TempDir(), WithHeartbeatInterval(10*time.Millisecond))
			ref := ir.NewDAGRunRef(name, "run-1")
			require.NoError(t, s.WithLock(ctx, "queue-a", func() error {
				handle, err := s.Acquire(ctx, "queue-a", testProcMeta(ref))
				require.NoError(t, err)
				t.Cleanup(func() { _ = handle.Stop(ctx) })

				alive, err := persis.NewProcRepository(s).IsRunAlive(ctx, "queue-a", ref)
				require.NoError(t, err)
				assert.True(t, alive)

				entries, err := s.ListAllEntries(ctx)
				require.NoError(t, err)
				require.Len(t, entries, 1)
				assert.Equal(t, ref, entries[0].Meta.DAGRun())
				return nil
			}))
		})
	}
}

func TestStoreWithLockSerializesIndependentStores(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	first := New(root)
	second := New(root)
	held := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseFirst := func() {
		releaseOnce.Do(func() { close(release) })
	}
	defer releaseFirst()

	firstDone := make(chan error, 1)
	go func() {
		firstDone <- first.WithLock(context.Background(), "queue-a", func() error {
			close(held)
			<-release
			return nil
		})
	}()

	select {
	case <-held:
	case err := <-firstDone:
		require.NoError(t, err)
		t.Fatal("first store returned without acquiring the process-group lock")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	called := false
	err := second.WithLock(ctx, "queue-a", func() error {
		called = true
		return nil
	})
	assert.True(t, persis.IsProcLockError(err))
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.False(t, called)

	releaseFirst()
	require.NoError(t, <-firstDone)
}

func TestStoreReadsAndRemovesReleasedProcFiles(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	s := New(root, WithStaleThreshold(10*time.Millisecond))
	ref := ir.NewDAGRunRef("released-dag", "run-1")
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

// writeDamagedProcFile writes an undecodable file at the path the store would
// use for ref, and stamps it as last written modifiedAgo in the past.
func writeDamagedProcFile(t *testing.T, s *Store, groupName string, ref ir.DAGRunRef, modifiedAgo time.Duration) string {
	t.Helper()

	path := s.filePath(groupName, testProcMeta(ref), time.Now().UTC())
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte{0x00, 0x01}, 0o600))
	at := time.Now().Add(-modifiedAgo)
	require.NoError(t, os.Chtimes(path, at, at))
	return path
}

func TestStoreSkipsAbandonedDamagedProcFile(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	s := New(root, WithStaleThreshold(time.Minute))
	repository := persis.NewProcRepository(s)
	ref := ir.NewDAGRunRef("healthy-dag", "run-1")
	meta := testProcMeta(ref)

	now := time.Now().UTC()
	require.NoError(t, writeProcFile(s.filePath("queue-a", meta, now), now.Unix(), meta))

	writeDamagedProcFile(t, s, "queue-a", ir.NewDAGRunRef("healthy-dag", "abandoned-run"), time.Hour)

	entries, err := s.ListEntries(ctx, "queue-a")
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, ref, entries[0].Meta.DAGRun())

	count, err := repository.CountAlive(ctx, "queue-a")
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	alive, err := repository.IsRunAlive(ctx, "queue-a", ref)
	require.NoError(t, err)
	assert.True(t, alive)
}

func TestStoreDoesNotUndercountWhileDamagedProcFileLooksActive(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	s := New(root, WithStaleThreshold(time.Minute))
	repository := persis.NewProcRepository(s)

	// A damaged file that is still being written may belong to a live run, so
	// the group must not be reported as if that run were gone.
	writeDamagedProcFile(t, s, "queue-a", ir.NewDAGRunRef("healthy-dag", "run-1"), 0)

	_, err := s.ListEntries(ctx, "queue-a")
	require.ErrorIs(t, err, errInvalidProcFile)

	_, err = repository.CountAlive(ctx, "queue-a")
	require.Error(t, err)
}

func TestStoreLatestHeartbeatDoesNotReportExitWhileDamagedProcFileLooksActive(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	s := New(root, WithStaleThreshold(time.Minute))
	ref := ir.NewDAGRunRef("healthy-dag", "run-1")

	// Callers read a nil heartbeat as the run having exited, so damage that is
	// named for this run must not be reported as an absence.
	damaged := writeDamagedProcFile(t, s, "queue-a", ref, 0)

	_, err := s.LatestHeartbeat(ctx, "queue-a", ref)
	require.ErrorIs(t, err, errInvalidProcFile)

	at := time.Now().Add(-time.Hour)
	require.NoError(t, os.Chtimes(damaged, at, at))

	heartbeat, err := s.LatestHeartbeat(ctx, "queue-a", ref)
	require.NoError(t, err)
	assert.Nil(t, heartbeat)
}

func TestStoreLatestHeartbeatIgnoresDamageBelongingToAnotherRun(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	s := New(root, WithHeartbeatInterval(10*time.Millisecond), WithStaleThreshold(time.Minute))
	ref := ir.NewDAGRunRef("healthy-dag", "run-1")

	handle, err := s.Acquire(ctx, "queue-a", testProcMeta(ref))
	require.NoError(t, err)
	defer func() { _ = handle.Stop(ctx) }()
	require.NotEmpty(t, waitForProcFile(t, root, "queue-a", "healthy-dag"))

	// Damage under another DAG in the same group says nothing about this run,
	// so it must not block callers that only ask about this one.
	writeDamagedProcFile(t, s, "queue-a", ir.NewDAGRunRef("other-dag", "other-run"), 0)

	heartbeat, err := s.LatestHeartbeat(ctx, "queue-a", ref)
	require.NoError(t, err)
	require.NotNil(t, heartbeat)
	assert.Equal(t, ref, heartbeat.DAGRun)
}

func TestStoreValidateIgnoresDamagedProcFiles(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	s := New(root, WithStaleThreshold(time.Minute))
	writeDamagedProcFile(t, s, "queue-a", ir.NewDAGRunRef("healthy-dag", "run-1"), 0)

	// Every command validates the proc directory, so a damaged file must not
	// make the store unusable.
	require.NoError(t, s.Validate(ctx))
}

func TestStoreTreatsAbandonedFutureHeartbeatAsStale(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	s := New(root, WithStaleThreshold(time.Minute))
	ref := ir.NewDAGRunRef("skewed-dag", "run-1")
	meta := testProcMeta(ref)

	// Nothing has written the file recently, so a heartbeat stamped in the
	// future must not keep the entry alive forever.
	writtenAt := time.Now().Add(-time.Hour).UTC()
	procFile := s.filePath("queue-a", meta, writtenAt)
	require.NoError(t, writeProcFile(procFile, time.Now().Add(time.Hour).UTC().Unix(), meta))
	require.NoError(t, os.Chtimes(procFile, writtenAt, writtenAt))

	entries, err := s.ListEntries(ctx, "queue-a")
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.False(t, entries[0].Fresh)

	require.NoError(t, s.RemoveIfStale(ctx, entries[0]))
	_, err = os.Stat(procFile)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestStoreKeepsRecentlyWrittenProcFileFreshDespiteClockSkew(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	s := New(root, WithStaleThreshold(time.Minute))
	ref := ir.NewDAGRunRef("skewed-dag", "run-1")
	meta := testProcMeta(ref)

	// A process whose clock runs ahead is still alive, and the write time proves
	// it. Freshness must follow the file, not the timestamp the writer recorded.
	procFile := s.filePath("queue-a", meta, time.Now().UTC())
	require.NoError(t, writeProcFile(procFile, time.Now().Add(time.Hour).UTC().Unix(), meta))

	entries, err := s.ListEntries(ctx, "queue-a")
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.True(t, entries[0].Fresh)

	require.NoError(t, s.RemoveIfStale(ctx, entries[0]))
	assert.FileExists(t, procFile)
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
