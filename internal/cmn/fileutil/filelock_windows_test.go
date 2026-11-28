// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package fileutil

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

// A lock file that another process briefly holds open without sharing, as
// a scanner can, is locked once that process lets go, within the retry
// window.
func TestTryLockFileWaitsForSharedHandle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	name, err := windows.UTF16PtrFromString(path)
	require.NoError(t, err)
	handle, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil,
		windows.CREATE_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL, 0)
	require.NoError(t, err)
	released := time.AfterFunc(100*time.Millisecond, func() { _ = windows.CloseHandle(handle) })
	t.Cleanup(func() {
		if released.Stop() {
			_ = windows.CloseHandle(handle)
		}
	})

	lock, err := TryLockFile(t.Context(), path, 0o600)
	require.NoError(t, err)
	require.NoError(t, lock.Unlock())
}

// The open stops retrying when its context ends, so a lock wait with a
// deadline never takes a file that another process lets go of later.
func TestTryLockFileStopsAtDeadline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	name, err := windows.UTF16PtrFromString(path)
	require.NoError(t, err)
	handle, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil,
		windows.CREATE_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL, 0)
	require.NoError(t, err)
	released := time.AfterFunc(2*time.Second, func() { _ = windows.CloseHandle(handle) })
	t.Cleanup(func() {
		if released.Stop() {
			_ = windows.CloseHandle(handle)
		}
	})

	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	started := time.Now()
	lock, err := TryLockFile(ctx, path, 0o600)
	if err == nil {
		_ = lock.Unlock()
	}
	require.Error(t, err, "the lock came after its deadline")
	require.Less(t, time.Since(started), time.Second)
}
