// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package skill

// HashFiles computes the hash of a list of files with the canonical form of
// Hash. Each file gives its path and its SHA-256.
func HashFiles(files []File) string {
	lines := make([]hashLine, len(files))
	for i, f := range files {
		lines[i] = hashLine{path: f.Path, sum: f.SHA256}
	}
	return hashLines(lines)
}
