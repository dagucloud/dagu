// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dirlock_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/internal/cmn/dirlock"
	"github.com/stretchr/testify/require"
)

func TestUnlockReleasesLiveLockNameBeforeCleanup(t *testing.T) {
	dir := t.TempDir()
	lock := dirlock.New(dir, nil)
	require.NoError(t, lock.TryLock())

	lockDir := filepath.Join(dir, ".dagu_lock")
	unexpectedDir := filepath.Join(lockDir, "unexpected")
	require.NoError(t, os.Mkdir(unexpectedDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(unexpectedDir, "file"), []byte("data"), 0o600))

	require.NoError(t, lock.Unlock())
	require.NoDirExists(t, lockDir)

	next := dirlock.New(dir, nil)
	require.NoError(t, next.TryLock())
	require.NoError(t, next.Unlock())
}
