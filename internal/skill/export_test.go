// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package skill

import "testing"

// MaxFrontMatterBytes is the size limit of the front matter.
const MaxFrontMatterBytes = maxFrontMatterBytes

// HashFiles computes the hash of a list of files with the canonical form of
// Hash. Each file gives its path and its SHA-256.
func HashFiles(files []File) string {
	lines := make([]hashLine, len(files))
	for i, f := range files {
		lines[i] = hashLine{path: f.Path, sum: f.SHA256}
	}
	return hashLines(lines)
}

// AroundReadFiles runs before and after each attempt of Read reads the files of
// the folder. It changes a package variable, so the test must not be parallel.
func AroundReadFiles(t *testing.T, before, after func()) {
	t.Helper()
	orig := readFiles
	readFiles = func(dir, name string, entries []entry, issues []Issue) (*Skill, error) {
		before()
		defer after()
		return orig(dir, name, entries, issues)
	}
	t.Cleanup(func() { readFiles = orig })
}
