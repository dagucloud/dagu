// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package skill_test

import (
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

// makeFIFO creates a named pipe at path.
func makeFIFO(t *testing.T, path string) {
	t.Helper()
	require.NoError(t, syscall.Mkfifo(path, 0o600))
}
