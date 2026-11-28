// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package fileutil

import (
	"os"
	"path/filepath"
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
