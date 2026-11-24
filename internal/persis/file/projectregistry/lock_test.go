// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package projectregistry

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/dirlock"
	"github.com/dagucloud/dagu/v2/internal/project"
	"github.com/stretchr/testify/require"
)

// The clock jumps past the write budget the moment the lock exists, standing
// for a writer that paused right after creating it. A contender may watch the
// lock from then on, so the writer can no longer rule out a takeover and
// stores nothing.
func TestWriteRefusedPastBudget(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	reg := New(dir)
	start := time.Now()
	reg.now = func() time.Time {
		if _, err := os.Stat(filepath.Join(reg.lockDir, dirlock.LockDirectoryName)); err == nil {
			return start.Add(writeBudget + time.Second)
		}
		return start
	}

	_, err := reg.Create(t.Context(), project.CreateInput{Name: "weather"})
	require.ErrorIs(t, err, dirlock.ErrLockNotHeld)
	require.NoFileExists(t, filepath.Join(dir, fileName))
}
