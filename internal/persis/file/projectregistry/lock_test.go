// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package projectregistry

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/dirlock"
	"github.com/dagucloud/dagu/v2/internal/project"
	"github.com/stretchr/testify/require"
)

// A writer that held the lock past its write budget can no longer rule out
// that a contender took the lock over, so it stores nothing.
func TestWriteRefusedPastBudget(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	reg := New(dir)
	now := time.Now()
	reg.now = func() time.Time {
		now = now.Add(writeBudget + time.Second)
		return now
	}

	_, err := reg.Create(t.Context(), project.CreateInput{Name: "weather"})
	require.ErrorIs(t, err, dirlock.ErrLockNotHeld)
	require.NoFileExists(t, filepath.Join(dir, fileName))
}
