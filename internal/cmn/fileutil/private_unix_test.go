// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package fileutil

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// requireOwnerOnly fails the test when a group or other users may access
// path.
func requireOwnerOnly(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Zero(t, info.Mode().Perm()&0o077, "%s has mode %v", path, info.Mode().Perm())
}
