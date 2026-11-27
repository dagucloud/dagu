// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package untrusted

import (
	"fmt"
	"os"
	"path/filepath"
)

// Spec 092, "Trusted steps after an untrusted step". A trusted process reads
// files of its HOME: the git config and its hooks, the start files of Python
// (.pth), of jq or of a shell. When the restricted user can write the HOME of
// the worker, for example HOME=/tmp, a trusted process of an isolated run gets
// a private HOME. The restricted user cannot reach it.

// homeFolders are the XDG variables that name a folder that a trusted process
// reads, with the folder below HOME that it reads when the variable is not set.
var homeFolders = []struct{ name, dir string }{
	{"XDG_CONFIG_HOME", ".config"},
	{"XDG_DATA_HOME", filepath.Join(".local", "share")},
	{"XDG_STATE_HOME", filepath.Join(".local", "state")},
	{"XDG_CACHE_HOME", ".cache"},
}

// TrustedHome returns the variables that give a trusted process a new private
// HOME, and the function that removes that HOME, when the restricted user uid
// or its group gid can write or replace the folder home, or a folder of the XDG
// variables: the one that lookup gives, or its default below home. It returns
// nil when the folders are safe.
func TrustedHome(uid, gid uint32, home string, lookup func(string) (string, bool)) (map[string]string, func() error, error) {
	unsafe := home == "" || writableBy(home, uid, gid)
	for _, folder := range homeFolders {
		dir, ok := lookup(folder.name)
		if !ok || dir == "" {
			dir = filepath.Join(home, folder.dir)
		}
		if writableBy(dir, uid, gid) {
			unsafe = true
		}
	}
	if !unsafe {
		return nil, nil, nil
	}
	// A folder of the user of the worker with the mode 0700 and a random name.
	dir, err := os.MkdirTemp("", "dagu-home-")
	if err != nil {
		return nil, nil, err
	}
	remove := func() error { return os.RemoveAll(dir) }
	// A temp folder that the restricted user can write lets it replace the
	// private HOME.
	if writableBy(dir, uid, gid) {
		_ = remove()
		return nil, nil, fmt.Errorf("the restricted user can replace the private HOME %s: its temp folder is not safe", dir)
	}
	entries := map[string]string{"HOME": dir}
	for _, folder := range homeFolders {
		entries[folder.name] = filepath.Join(dir, folder.dir)
	}
	return entries, remove, nil
}
