// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package fileutil

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A locked file refuses every other holder until it is unlocked, without a
// renewal, also within one process.
func TestTryLockFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "lock")
	first, err := TryLockFile(t.Context(), path, 0o600)
	require.NoError(t, err)

	_, err = TryLockFile(t.Context(), path, 0o600)
	assert.ErrorIs(t, err, ErrLocked)

	require.NoError(t, first.Unlock())
	second, err := TryLockFile(t.Context(), path, 0o600)
	require.NoError(t, err)
	require.NoError(t, second.Unlock())
}
