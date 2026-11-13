// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package skill

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
)

// zeroSum is the digest that stands for an entry that is not a regular file.
var zeroSum = strings.Repeat("0", 64)

type hashLine struct {
	path string
	sum  string
}

// hashLines computes the hash of a skill from its file digests. The canonical
// form is the text that `sha256sum` prints for the files of the folder, in
// ascending byte order of the relative path, with / separators: one line
// "<sha256 hex>  <path>\n" per file. The hash is the SHA-256 of that text, in
// lower case hexadecimal. It does not depend on the order of the directory
// entries, the times or the permissions of the files.
func hashLines(lines []hashLine) string {
	sorted := make([]hashLine, len(lines))
	copy(sorted, lines)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].path < sorted[j].path })
	h := sha256.New()
	for _, l := range sorted {
		h.Write([]byte(l.sum + "  " + l.path + "\n"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// HashFiles computes the hash of a list of files with the canonical form of
// Hash. Each file gives its path and its SHA-256.
func HashFiles(files []File) string {
	lines := make([]hashLine, len(files))
	for i, f := range files {
		lines[i] = hashLine{path: f.Path, sum: f.SHA256}
	}
	return hashLines(lines)
}
