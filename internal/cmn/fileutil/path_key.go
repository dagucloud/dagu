// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package fileutil

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
)

// ResolveExistingAncestor resolves symbolic links in the longest existing
// prefix of path and appends the components that do not exist yet, so two
// spellings of a file that is not created yet resolve to one path.
func ResolveExistingAncestor(path string) (string, error) {
	suffix := make([]string, 0)
	current := path
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			for _, s := range slices.Backward(suffix) {
				resolved = filepath.Join(resolved, s)
			}
			return filepath.Clean(resolved), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", err
		}
		suffix = append(suffix, filepath.Base(current))
		current = parent
	}
}

// IsCaseInsensitiveFS reports whether the filesystem holding path treats
// names that differ only in ASCII case as one name. When no existing
// ancestor can be probed, it reports the platform default.
func IsCaseInsensitiveFS(path string) bool {
	dir := filepath.Dir(path)
	for {
		info, err := os.Lstat(dir)
		if errors.Is(err, os.ErrNotExist) {
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
			continue
		}
		if err == nil {
			name := filepath.Base(dir)
			if alternate, ok := alternateASCIICase(name); ok {
				alternateInfo, alternateErr := os.Lstat(filepath.Join(filepath.Dir(dir), alternate))
				switch {
				case alternateErr == nil:
					return os.SameFile(info, alternateInfo)
				case errors.Is(alternateErr, os.ErrNotExist):
					return false
				}
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return runtime.GOOS == "windows" || runtime.GOOS == "darwin"
}

func alternateASCIICase(value string) (string, bool) {
	bytes := []byte(value)
	for idx, ch := range bytes {
		switch {
		case ch >= 'a' && ch <= 'z':
			bytes[idx] = ch - ('a' - 'A')
			return string(bytes), true
		case ch >= 'A' && ch <= 'Z':
			bytes[idx] = ch + ('a' - 'A')
			return string(bytes), true
		}
	}
	return value, false
}
