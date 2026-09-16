// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package artifact

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// outsideBounds decides whether a whole year or month directory can be skipped,
// so an off-by-one here turns into either missing results or reading the entire
// retained history.
func TestOutsideBounds(t *testing.T) {
	t.Parallel()

	const from, to = "2026/09/15", "2026/11/20"

	tests := []struct {
		key     string
		outside bool
	}{
		{"2026", false},
		{"2025", true},
		{"2027", true},
		{"2026/09", false},
		{"2026/10", false},
		{"2026/11", false},
		{"2026/08", true},
		{"2026/12", true},
		{"2026/09/15", false},
		{"2026/09/14", true},
		{"2026/11/20", false},
		{"2026/11/21", true},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.outside, outsideBounds(tt.key, from, to), tt.key)
	}
}

func TestOutsideBoundsUnbounded(t *testing.T) {
	t.Parallel()

	assert.False(t, outsideBounds("1999", "", ""))
	assert.False(t, outsideBounds("2030/01/01", "", ""))

	// A bound set on one side only must not constrain the other.
	assert.True(t, outsideBounds("2025/12/31", "2026/01/01", ""))
	assert.False(t, outsideBounds("2030/01/01", "2026/01/01", ""))
	assert.True(t, outsideBounds("2027/01/01", "", "2026/12/31"))
	assert.False(t, outsideBounds("2020/01/01", "", "2026/12/31"))
}

// walkOrderAfter is the comparison a cursor resumes by, so it has to agree with
// the order walkFiles actually produces. They disagree if it compares joined
// paths: "a.txt" sorts before "a/b.txt" as a string, but the walk descends "a"
// first.
func TestWalkOrderAfterMatchesWalk(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	for _, rel := range []string{
		"a.txt",
		"a/b.txt",
		"a/y/z.txt",
		"a/x.txt",
		"a-b.txt",
		"b/c.txt",
		"zz.txt",
	} {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, os.WriteFile(path, []byte("x"), 0o600))
	}

	var walked []string
	require.NoError(t, walkFiles(dir, func(relPath string, _ fs.DirEntry) bool {
		walked = append(walked, relPath)
		return true
	}))
	require.Len(t, walked, 7)

	// Every adjacent pair must be strictly increasing under the comparison.
	for i := 1; i < len(walked); i++ {
		assert.True(t, walkOrderAfter(walked[i], walked[i-1]),
			"%q should come after %q", walked[i], walked[i-1])
		assert.False(t, walkOrderAfter(walked[i-1], walked[i]),
			"%q should not come after %q", walked[i-1], walked[i])
	}

	// Sorting by the comparison must reproduce the walk exactly.
	shuffled := append([]string(nil), walked...)
	sort.Slice(shuffled, func(i, j int) bool { return !walkOrderAfter(shuffled[i], shuffled[j]) })
	assert.Equal(t, walked, shuffled)
}

func TestWalkOrderAfterIdentity(t *testing.T) {
	t.Parallel()

	assert.False(t, walkOrderAfter("a/b.txt", "a/b.txt"))
}

// A page must read only as far as it needs, or one run with thousands of files
// makes every page pay for all of them.
func TestWalkFilesStopsEarly(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	for i := range 500 {
		require.NoError(t, os.WriteFile(
			filepath.Join(dir, fmt.Sprintf("f%04d.txt", i)), []byte("x"), 0o600))
	}

	visited := 0
	require.NoError(t, walkFiles(dir, func(string, fs.DirEntry) bool {
		visited++
		return visited < 3
	}))

	assert.Equal(t, 3, visited)
}

func TestWalkFilesMissingDirectory(t *testing.T) {
	t.Parallel()

	visited := 0
	err := walkFiles(filepath.Join(t.TempDir(), "gone"), func(string, fs.DirEntry) bool {
		visited++
		return true
	})

	require.NoError(t, err)
	assert.Zero(t, visited)
}
