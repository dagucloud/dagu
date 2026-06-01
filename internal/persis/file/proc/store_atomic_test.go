// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package proc_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/internal/persis/file/proc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteProcFileAtomicRetriesMissingDirectoryRace(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "queue", "dag", "proc_test.proc")
	data := []byte("heartbeat")
	calls := 0

	err := proc.WriteProcFileAtomicWithCreateTempForTest(path, data, func(dir, pattern string) (*os.File, error) {
		calls++
		if calls == 1 {
			require.NoError(t, os.RemoveAll(dir))
		}
		return os.CreateTemp(dir, pattern)
	})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, calls, 2)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, data, got)
}
