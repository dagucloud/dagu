// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5"
)

// gitDirName marks a git checkout.
const gitDirName = ".git"

// trackingCheckout returns the work tree of a git checkout that tracks a
// file in dir, which is in the checkout or holds it, or "" when dir does
// not exist or no checkout tracks one. A checkout above dir counts through
// a symbolic link too.
func trackingCheckout(dir string) (string, error) {
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		return "", nil
	} else if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	for _, start := range []string{filepath.Clean(dir), resolved} {
		for root := start; ; {
			if _, err := os.Lstat(filepath.Join(root, gitDirName)); err == nil {
				rel, err := filepath.Rel(root, start)
				if err != nil {
					return "", err
				}
				if tracksUnder(root, filepath.ToSlash(rel)) {
					return root, nil
				}
			}
			parent := filepath.Dir(root)
			if parent == root {
				break
			}
			root = parent
		}
	}
	var found string
	err = filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Name() != gitDirName {
			return nil
		}
		// The checkout at dir itself was read on the way up.
		if root := filepath.Dir(path); root != dir && tracksUnder(root, ".") {
			found = root
			return fs.SkipAll
		}
		if entry.IsDir() {
			return fs.SkipDir
		}
		return nil
	})
	return found, err
}

// tracksUnder reports whether the checkout whose work tree is root tracks
// rel, a slash-separated path relative to root, or a file under it. A
// worktree or submodule, whose .git is a file, a bare repository, and a
// checkout whose index cannot be read count as tracking, since nothing
// tells that they do not. Names compare without regard to case, as they
// may on the file system.
func tracksUnder(root, rel string) bool {
	info, err := os.Lstat(filepath.Join(root, gitDirName))
	if err != nil || !info.IsDir() {
		return true
	}
	repo, err := git.PlainOpen(root)
	if err != nil {
		return true
	}
	cfg, err := repo.Config()
	if err != nil || cfg.Core.IsBare {
		return true
	}
	idx, err := repo.Storer.Index()
	if err != nil {
		return true
	}
	if rel == "." {
		return len(idx.Entries) > 0
	}
	for _, entry := range idx.Entries {
		if strings.EqualFold(entry.Name, rel) || hasPrefixFold(entry.Name, rel+"/") {
			return true
		}
	}
	return false
}

func hasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}
