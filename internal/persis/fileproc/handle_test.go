// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package fileproc

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/stretchr/testify/require"
)

func TestProcHandle(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	meta := testProcMetaFromRun(exec.NewDAGRunRef("test_proc", "run-1"))

	fileName := procFilePath(tmpDir, exec.NewUTC(time.Now()), meta)
	proc := NewProcHandler(fileName, meta, 0, 0)

	ctx := context.Background()
	err := proc.startHeartbeat(ctx)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = proc.Stop(ctx)
	})

	// File is created synchronously by startHeartbeat
	_, err = os.Stat(fileName)
	require.NoError(t, err)

	// Stop the process
	err = proc.Stop(ctx)
	require.NoError(t, err)

	// Check if the file is deleted
	_, err = os.Stat(fileName)
	require.Error(t, err, "file should be deleted")
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestProcHandle_Restart(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	ctx := context.Background()

	meta := testProcMetaFromRun(exec.NewDAGRunRef("test_proc", "run-1"))
	fileName := procFilePath(tmpDir, exec.NewUTC(time.Now()), meta)
	proc := NewProcHandler(fileName, meta, 0, 0)

	err := proc.startHeartbeat(ctx)
	require.NoError(t, err)

	// Restart the process
	err = proc.Stop(ctx)
	require.NoError(t, err)

	err = proc.startHeartbeat(ctx)
	require.NoError(t, err)

	// Check if the file is created again
	_, err = os.Stat(fileName)
	require.NoError(t, err)

	err = proc.Stop(ctx)
	require.NoError(t, err)

	// Check if the file is deleted again
	_, err = os.Stat(fileName)
	require.Error(t, err, "file should be deleted")
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestProcHandle_RemovesEmptyParentDir(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	ctx := context.Background()

	// Create a subdirectory for the proc file
	subDir := filepath.Join(tmpDir, "subdir")
	err := os.MkdirAll(subDir, 0750)
	require.NoError(t, err)

	meta := testProcMetaFromRun(exec.NewDAGRunRef("test_proc", "run-1"))
	fileName := procFilePath(subDir, exec.NewUTC(time.Now()), meta)
	proc := NewProcHandler(fileName, meta, 0, 0)
	dagDir := filepath.Dir(fileName)

	err = proc.startHeartbeat(ctx)
	require.NoError(t, err)

	// Check if the file is created
	_, err = os.Stat(fileName)
	require.NoError(t, err)

	// Stop the process
	err = proc.Stop(ctx)
	require.NoError(t, err)

	// Check if the file is deleted
	_, err = os.Stat(fileName)
	require.ErrorIs(t, err, os.ErrNotExist)

	// Check if the DAG directory is also removed.
	_, err = os.Stat(dagDir)
	require.ErrorIs(t, err, os.ErrNotExist, "empty DAG directory should be removed")
}

func TestProcHandle_StartPublishesInitializedFile(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	ctx := context.Background()

	meta := testProcMetaFromRun(exec.NewDAGRunRef("test_proc", "run-1"))
	fileName := procFilePath(tmpDir, exec.NewUTC(time.Now()), meta)
	proc := NewProcHandler(fileName, meta, 0, 0)

	require.NoError(t, proc.startHeartbeat(ctx))
	t.Cleanup(func() {
		_ = proc.Stop(ctx)
	})

	entries, err := os.ReadDir(filepath.Dir(fileName))
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, filepath.Base(fileName), entries[0].Name())

	entry, err := readProcEntry(fileName, meta.Name, time.Minute, time.Now())
	require.NoError(t, err)
	require.Equal(t, meta, entry.Meta)
}

func TestProcHandle_HeartbeatContinuesWhileSyncBlocked(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	ctx := context.Background()

	meta := testProcMetaFromRun(exec.NewDAGRunRef("test_proc", "run-1"))
	fileName := procFilePath(tmpDir, exec.NewUTC(time.Now()), meta)
	proc := NewProcHandler(fileName, meta, 20*time.Millisecond, time.Millisecond)

	syncStarted := make(chan struct{})
	releaseSync := make(chan struct{})
	var closeStarted sync.Once
	proc.syncFile = func(*os.File) error {
		closeStarted.Do(func() {
			close(syncStarted)
		})
		<-releaseSync
		return nil
	}

	require.NoError(t, proc.startHeartbeat(ctx))
	t.Cleanup(func() {
		close(releaseSync)
		_ = proc.Stop(ctx)
	})

	require.Eventually(t, func() bool {
		select {
		case <-syncStarted:
			return true
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)

	initialInfo, err := os.Stat(fileName)
	require.NoError(t, err)
	initialModTime := initialInfo.ModTime()

	require.Eventually(t, func() bool {
		info, statErr := os.Stat(fileName)
		return statErr == nil && info.ModTime().After(initialModTime)
	}, time.Second, 10*time.Millisecond)
}

func TestProcHandle_StopWaitsForInFlightSync(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	ctx := context.Background()

	meta := testProcMetaFromRun(exec.NewDAGRunRef("test_proc", "run-1"))
	fileName := procFilePath(tmpDir, exec.NewUTC(time.Now()), meta)
	proc := NewProcHandler(fileName, meta, 20*time.Millisecond, time.Millisecond)

	syncStarted := make(chan struct{})
	releaseSync := make(chan struct{})
	var closeStarted sync.Once
	var closeRelease sync.Once
	release := func() {
		closeRelease.Do(func() {
			close(releaseSync)
		})
	}
	proc.syncFile = func(*os.File) error {
		closeStarted.Do(func() {
			close(syncStarted)
		})
		<-releaseSync
		return nil
	}

	require.NoError(t, proc.startHeartbeat(ctx))
	t.Cleanup(func() {
		release()
		_ = proc.Stop(ctx)
	})

	require.Eventually(t, func() bool {
		select {
		case <-syncStarted:
			return true
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)

	stopDone := make(chan error, 1)
	go func() {
		stopDone <- proc.Stop(ctx)
	}()

	select {
	case err := <-stopDone:
		require.NoError(t, err)
		t.Fatal("Stop returned before the in-flight sync finished")
	case <-time.After(100 * time.Millisecond):
	}

	_, err := os.Stat(fileName)
	require.NoError(t, err, "heartbeat file should not be removed while sync is still in-flight")

	release()
	require.NoError(t, <-stopDone)

	_, err = os.Stat(fileName)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestProcHandle_OpenInitializedProcFileRecreatesMissingParentDir(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	meta := testProcMetaFromRun(exec.NewDAGRunRef("test_proc", "run-1"))
	fileName := procFilePath(tmpDir, exec.NewUTC(time.Now()), meta)
	proc := NewProcHandler(fileName, meta, 0, 0)
	dagDir := filepath.Dir(fileName)

	require.NoError(t, os.MkdirAll(dagDir, 0750))
	require.NoError(t, os.Remove(dagDir))

	fd, err := proc.openInitializedProcFile(time.Now().Unix())
	require.NoError(t, err)
	require.NoError(t, fd.Close())
	t.Cleanup(func() {
		_ = os.Remove(fileName)
	})

	_, err = os.Stat(fileName)
	require.NoError(t, err)
}
