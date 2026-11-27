// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build linux

package untrusted

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// trustedHome calls TrustedHome and removes the private HOME at the end of
// the test.
func trustedHome(t *testing.T, uid uint32, home string, lookup func(string) (string, bool)) map[string]string {
	t.Helper()
	entries, remove, err := TrustedHome(uid, testGID, home, lookup)
	require.NoError(t, err)
	if remove != nil {
		t.Cleanup(func() { _ = remove() })
	}
	return entries
}

// A HOME that the restricted user can write, such as /tmp, would give a trusted
// process the git config, the hooks or the .pth files that an agent planted.
// Each call makes a new private HOME, which its function removes. It needs no
// root.
func TestTrustedHome(t *testing.T) {
	t.Parallel()
	none := func(string) (string, bool) { return "", false }

	shared := t.TempDir()
	require.NoError(t, os.Chmod(shared, 0o1777)) //nolint:gosec // a folder that any user can write, as /tmp
	entries, remove, err := TrustedHome(testUID, testGID, shared, none)
	require.NoError(t, err)
	require.NotNil(t, entries)
	private := entries["HOME"]
	assert.NotEqual(t, shared, private)
	info, err := os.Stat(private)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())
	assert.False(t, writableBy(private, testUID, testGID))
	assert.Equal(t, filepath.Join(private, ".config"), entries["XDG_CONFIG_HOME"])
	assert.NotEqual(t, private, trustedHome(t, testUID, shared, none)["HOME"], "each step gets its own HOME")
	require.NoError(t, remove())
	assert.NoDirExists(t, private)

	safe := t.TempDir()
	require.NoError(t, os.Chmod(safe, 0o700))
	assert.Nil(t, trustedHome(t, testUID, safe, none), "a HOME of the worker stays")

	xdg := func(name string) (string, bool) { return shared, name == "XDG_CONFIG_HOME" }
	assert.NotNil(t, trustedHome(t, testUID, safe, xdg), "a config folder that the restricted user can write is replaced")

	assert.NotNil(t, trustedHome(t, testUID, filepath.Join(safe, "missing"), none), "a HOME that does not exist could be made by the restricted user")

	// The owner of a folder can give itself write access. The test process
	// stands in for the restricted user, so this needs no root either.
	owned := t.TempDir()
	require.NoError(t, os.Chmod(owned, 0o500))
	assert.NotNil(t, trustedHome(t, uint32(os.Getuid()), owned, none), "a HOME of the restricted user is replaced whatever its mode") // #nosec G115 -- a user ID is not negative.
}
