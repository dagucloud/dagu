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

// A HOME that the restricted user can write, such as /tmp, would give a trusted
// process the git config, the hooks or the .pth files that an agent planted.
// It needs no root.
func TestTrustedHome(t *testing.T) {
	t.Parallel()
	none := func(string) (string, bool) { return "", false }

	shared := t.TempDir()
	require.NoError(t, os.Chmod(shared, 0o1777)) //nolint:gosec // a folder that any user can write, as /tmp
	entries, err := TrustedHome(testUID, testGID, shared, none)
	require.NoError(t, err)
	require.NotNil(t, entries)
	private := entries["HOME"]
	assert.NotEqual(t, shared, private)
	info, err := os.Stat(private)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())
	assert.False(t, writableBy(private, testUID, testGID))
	assert.Equal(t, filepath.Join(private, ".config"), entries["XDG_CONFIG_HOME"])

	safe := t.TempDir()
	require.NoError(t, os.Chmod(safe, 0o700))
	entries, err = TrustedHome(testUID, testGID, safe, none)
	require.NoError(t, err)
	assert.Nil(t, entries, "a HOME of the worker stays")

	xdg := func(name string) (string, bool) { return shared, name == "XDG_CONFIG_HOME" }
	entries, err = TrustedHome(testUID, testGID, safe, xdg)
	require.NoError(t, err)
	assert.NotNil(t, entries, "a config folder that the restricted user can write is replaced")

	entries, err = TrustedHome(testUID, testGID, filepath.Join(safe, "missing"), none)
	require.NoError(t, err)
	assert.NotNil(t, entries, "a HOME that does not exist could be made by the restricted user")

	// The owner of a folder can give itself write access. The test process
	// stands in for the restricted user, so this needs no root either.
	owned := t.TempDir()
	require.NoError(t, os.Chmod(owned, 0o500))
	entries, err = TrustedHome(uint32(os.Getuid()), testGID, owned, none) // #nosec G115 -- a user ID is not negative.
	require.NoError(t, err)
	assert.NotNil(t, entries, "a HOME of the restricted user is replaced whatever its mode")
}
