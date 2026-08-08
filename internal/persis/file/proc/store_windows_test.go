// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package proc

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"

	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
)

func TestStoreListEntriesWaitsForTransientDirectorySharingViolation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := New(root)
	ref := dagrun.NewDAGRunRef("shared-dag", "run-1")
	meta := testProcMeta(ref)
	require.NoError(t, writeProcFile(store.filePath("queue-a", meta, time.Now().UTC()), time.Now().UTC().Unix(), meta))

	dagDir := filepath.Join(root, "queue-a", ref.Name)
	path, err := windows.UTF16PtrFromString(dagDir)
	require.NoError(t, err)
	handle, err := windows.CreateFile(
		path,
		windows.GENERIC_READ,
		0,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS,
		0,
	)
	require.NoError(t, err)
	var closeOnce sync.Once
	closeHandle := func() (err error) {
		closeOnce.Do(func() { err = windows.CloseHandle(handle) })
		return err
	}
	t.Cleanup(func() { _ = closeHandle() })

	_, err = os.ReadDir(dagDir)
	require.Error(t, err)
	require.True(t, fileutil.IsTransientFileError(err), "expected a transient sharing violation, got %v", err)

	released := make(chan error, 1)
	go func() {
		time.Sleep(100 * time.Millisecond)
		released <- closeHandle()
	}()

	entries, err := store.ListEntries(t.Context(), "queue-a")
	require.NoError(t, <-released)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, ref, entries[0].Meta.DAGRun())
}
