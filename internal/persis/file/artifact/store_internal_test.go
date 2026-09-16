// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package artifact

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/persis"
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

// A cursor's day becomes a bound sliced to each key's width, so anything but a
// full day must be rejected before it reaches the walk.
func TestDecodeCursorRejectsShortDay(t *testing.T) {
	t.Parallel()

	query := persis.ArtifactQuery{Limit: 1}
	data, err := json.Marshal(cursor{
		Version: cursorVersion, Filters: filterFingerprint(query), Day: "2026", RunDir: "x",
	})
	require.NoError(t, err)
	query.Cursor = base64.RawURLEncoding.EncodeToString(data)

	_, err = decodeCursor(query)
	assert.ErrorIs(t, err, persis.ErrInvalidArtifactCursor)
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
