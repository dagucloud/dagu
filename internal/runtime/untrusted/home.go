// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package untrusted

import (
	"os"
	"path/filepath"
)

// Spec 092, "Trusted steps after an untrusted step". A trusted process reads
// files of its HOME: the git config and its hooks, the start files of Python
// (.pth), of jq or of a shell. When the restricted user can write the HOME of
// the worker, for example HOME=/tmp, a trusted process of an isolated run gets
// a private HOME. The restricted user cannot reach it.

// homeVariables are the variables that name a folder below HOME that a trusted
// process reads.
var homeVariables = []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME"}

// TrustedHome returns the variables that give a trusted process a new private
// HOME, and the function that removes that HOME, when the restricted user uid
// or its group gid can write the folder home or one of the folders that lookup
// gives for the XDG variables. It returns nil when the folders are safe.
func TrustedHome(uid, gid uint32, home string, lookup func(string) (string, bool)) (map[string]string, func() error, error) {
	unsafe := home == "" || writableBy(home, uid, gid)
	for _, name := range homeVariables {
		if dir, ok := lookup(name); ok && dir != "" && writableBy(dir, uid, gid) {
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
	return map[string]string{
		"HOME":            dir,
		"XDG_CONFIG_HOME": filepath.Join(dir, ".config"),
		"XDG_DATA_HOME":   filepath.Join(dir, ".local", "share"),
		"XDG_STATE_HOME":  filepath.Join(dir, ".local", "state"),
		"XDG_CACHE_HOME":  filepath.Join(dir, ".cache"),
	}, func() error { return os.RemoveAll(dir) }, nil
}
