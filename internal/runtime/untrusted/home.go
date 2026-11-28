// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package untrusted

import (
	"fmt"
	"os"
	"path/filepath"
)

// Spec 103, "Protections for later trusted steps". A trusted process reads
// files of its HOME: the git config and its hooks, the start files of Python
// (.pth), of jq or of a shell. When the restricted user can write or replace
// the HOME of the worker or an XDG folder, for example HOME=/tmp, a trusted
// step of an isolated run gets a new private HOME. The restricted user cannot
// reach it.

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
// variables: the one that lookup gives, and its default below home when the
// value is not set or is relative. A folder in workDir, the work directory of
// the run, counts as one it can write. A relative path counts from
// workingDir, the working directory of the process. It returns nil when the
// folders are safe. The private HOME is an absolute path without links.
func TrustedHome(uid, gid uint32, workDir, workingDir, home string, lookup func(string) (string, bool)) (map[string]string, func() error, error) {
	// Not filepath.Join: a cleaned path drops a ".." after a link.
	inWorkingDir := func(path string) string {
		if path == "" || workingDir == "" || filepath.IsAbs(path) {
			return path
		}
		return workingDir + string(filepath.Separator) + path
	}
	unsafeDir := func(dir string) bool { return writableBy(dir, uid, gid) || inWorkDir(dir, workDir) }
	home = inWorkingDir(home)
	unsafe := home == "" || unsafeDir(home)
	for _, folder := range homeFolders {
		dir, ok := lookup(folder.name)
		if ok && dir != "" && unsafeDir(inWorkingDir(dir)) {
			unsafe = true
		}
		// A program that follows the XDG Base Directory Specification ignores
		// a relative value and reads the default below HOME.
		if !filepath.IsAbs(dir) && unsafeDir(home+string(filepath.Separator)+folder.dir) {
			unsafe = true
		}
	}
	if !unsafe {
		return nil, nil, nil
	}
	// A folder of the user of the worker with the mode 0700 and a random name.
	created, err := os.MkdirTemp("", "dagu-home-")
	if err != nil {
		return nil, nil, err
	}
	// A temp folder that the restricted user can write lets it replace the
	// private HOME.
	if writableBy(created, uid, gid) {
		_ = os.RemoveAll(created)
		return nil, nil, fmt.Errorf("the restricted user can replace the private HOME %s: its temp folder is not safe", created)
	}
	// The paths below HOME name the folder that Dagu made only when HOME has
	// no link, no ".." and no relative part, as TMPDIR can have.
	dir, err := filepath.EvalSymlinks(created)
	if err == nil {
		dir, err = filepath.Abs(dir)
	}
	if err != nil {
		_ = os.RemoveAll(created)
		return nil, nil, err
	}
	remove := func() error { return os.RemoveAll(dir) }
	entries := map[string]string{"HOME": dir}
	for _, folder := range homeFolders {
		entries[folder.name] = filepath.Join(dir, folder.dir)
	}
	return entries, remove, nil
}
