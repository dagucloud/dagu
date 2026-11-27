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
	entries, remove, err := TrustedHome(uid, testGID, "", home, lookup)
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
	require.NoError(t, os.Chmod(shared, os.ModeSticky|0o777)) //nolint:gosec // a folder that any user can write, as /tmp
	entries, remove, err := TrustedHome(testUID, testGID, "", shared, none)
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

	assert.NotNil(t, trustedHome(t, testUID, filepath.Join(shared, "missing"), none), "a HOME that does not exist could be made by the restricted user")
	assert.Nil(t, trustedHome(t, testUID, filepath.Join(safe, "missing"), none), "a HOME that only the worker could make stays")

	inSticky := filepath.Join(shared, "home")
	require.NoError(t, os.Mkdir(inSticky, 0o700))
	assert.Nil(t, trustedHome(t, testUID, inSticky, none), "a sticky folder keeps the restricted user from renaming the HOME")
	assert.NotNil(t, trustedHome(t, testUID, shared+"/missing/../home", none), "the restricted user could make the folder that a .. leaves as a link")

	open := t.TempDir()
	require.NoError(t, os.Chmod(open, 0o777)) //nolint:gosec // a folder that any user can write, without the sticky bit
	inOpen := filepath.Join(open, "home")
	require.NoError(t, os.Mkdir(inOpen, 0o700))
	assert.NotNil(t, trustedHome(t, testUID, inOpen, none), "a HOME that the restricted user can rename is replaced")
	relative, removeRelative, err := TrustedHome(testUID, testGID, open, "home", none)
	require.NoError(t, err)
	if removeRelative != nil {
		t.Cleanup(func() { _ = removeRelative() })
	}
	assert.NotNil(t, relative, "a relative HOME counts from the working directory of the process")

	// The target holds every XDG folder, so that only the link is unsafe.
	target := t.TempDir()
	for _, folder := range homeFolders {
		require.NoError(t, os.MkdirAll(filepath.Join(target, folder.dir), 0o700))
	}
	link := filepath.Join(open, "link")
	require.NoError(t, os.Symlink(target, link))
	assert.NotNil(t, trustedHome(t, testUID, link, none), "a link to a safe HOME that the restricted user can replace is replaced")
	start := filepath.Join(safe, "start")
	require.NoError(t, os.Symlink(link, start))
	assert.NotNil(t, trustedHome(t, testUID, start, none), "a safe link that leads through a link that the restricted user can replace is replaced")
	require.NoError(t, os.Mkdir(filepath.Join(open, "dir"), 0o700))
	require.NoError(t, os.Symlink(filepath.Join(open, "dir"), filepath.Join(safe, "up")))
	assert.NotNil(t, trustedHome(t, testUID, filepath.Join(safe, "up")+"/../home", none), "a .. after a link leads to the folder the restricted user can rename")

	xdgHome := filepath.Join(t.TempDir(), "home")
	require.NoError(t, os.MkdirAll(filepath.Join(xdgHome, ".config"), 0o700))
	require.NoError(t, os.Chmod(filepath.Join(xdgHome, ".config"), 0o777)) //nolint:gosec // a config folder that any user can write
	assert.NotNil(t, trustedHome(t, testUID, xdgHome, none), "a default config folder that the restricted user can write is replaced")

	// The owner of a folder can give itself write access. The test process
	// stands in for the restricted user, so this needs no root either.
	owned := t.TempDir()
	require.NoError(t, os.Chmod(owned, 0o500))
	assert.True(t, writableBy(owned, uint32(os.Getuid()), testGID), "a folder of the restricted user counts as writable whatever its mode") // #nosec G115 -- a user ID is not negative.
}

// A private HOME in a temp folder that the restricted user could rename fails
// the step instead.
func TestTrustedHomeNeedsASafeTempFolder(t *testing.T) {
	open := t.TempDir()
	require.NoError(t, os.Chmod(open, 0o777)) //nolint:gosec // a folder that any user can write, without the sticky bit
	t.Setenv("TMPDIR", open)
	entries, _, err := TrustedHome(testUID, testGID, "", "", func(string) (string, bool) { return "", false })
	require.ErrorContains(t, err, "the restricted user can replace the private HOME")
	assert.Nil(t, entries)
	names, err := os.ReadDir(open)
	require.NoError(t, err)
	assert.Empty(t, names, "the private HOME is removed")
}

// An access ACL can let the restricted user write a folder whatever its mode.
// It needs no root.
func TestTrustedHomeACL(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	require.NoError(t, os.Chmod(home, 0o755))
	setUserACL(t, home, aclNames[0])
	assert.NotNil(t, trustedHome(t, testUID, home, func(string) (string, bool) { return "", false }))
}

// A link of the restricted user in a sticky folder can be replaced by it,
// although the sticky bit keeps it from replacing an entry of the worker. The
// folder is writable by the group, not by all, so that fs.protected_symlinks
// does not already stop the link.
func TestTrustedHomeLinkOfTheUser(t *testing.T) {
	requireIsolation(t)
	shared := t.TempDir()
	require.NoError(t, os.Chown(shared, os.Geteuid(), testGID))
	require.NoError(t, os.Chmod(shared, os.ModeSticky|0o770))
	safe := t.TempDir()
	require.NoError(t, os.Chmod(safe, 0o700))
	link := filepath.Join(shared, "home")
	require.NoError(t, os.Symlink(safe, link))
	none := func(string) (string, bool) { return "", false }
	assert.Nil(t, trustedHome(t, testUID, link, none), "a link of the worker in a sticky folder stays")

	require.NoError(t, os.Lchown(link, testUID, testGID))
	assert.NotNil(t, trustedHome(t, testUID, link, none))
}
