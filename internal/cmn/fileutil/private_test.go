// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package fileutil

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

// MkdirPrivate restricts a new directory, and one that existed with wider
// access, to its owner. A file written into it is the owner's only.
func TestMkdirPrivate(t *testing.T) {
	t.Parallel()
	for name, existing := range map[string]bool{"New": false, "Existing": true} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := filepath.Join(t.TempDir(), "private")
			if existing {
				require.NoError(t, os.Mkdir(dir, 0o755))
			}
			require.NoError(t, MkdirPrivate(dir))
			file := filepath.Join(dir, "secret")
			require.NoError(t, WriteFileAtomic(file, []byte("secret"), 0o600))
			requireOwnerOnly(t, dir)
			requireOwnerOnly(t, file)
		})
	}
}

// A failure to restrict the directory is a path error for it, so a caller
// can report the system's cause apart from the path.
func TestMkdirPrivateFailure(t *testing.T) {
	cause := syscall.EACCES
	previous := restrictToOwner
	restrictToOwner = func(string) error { return cause }
	t.Cleanup(func() { restrictToOwner = previous })

	dir := filepath.Join(t.TempDir(), "private")
	err := MkdirPrivate(dir)
	pathErr, ok := errors.AsType[*fs.PathError](err)
	require.True(t, ok, "%v", err)
	require.Equal(t, dir, pathErr.Path)
	require.ErrorIs(t, pathErr.Err, cause)
	require.NotContains(t, pathErr.Err.Error(), dir)
}
