// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagindex_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/persis/file/dag/dagindex"
	indexv1 "github.com/dagucloud/dagu/v2/proto/index/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

// writeDAG writes a DAG file into dir and returns its metadata as a scan sees it.
func writeDAG(t *testing.T, dir, name, content string) dagindex.YAMLFileMeta {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	info, err := os.Stat(path)
	require.NoError(t, err)
	return dagindex.YAMLFileMeta{Name: name, Size: info.Size(), ModTime: info.ModTime().UnixNano()}
}

// savedEntry is an entry saved for file. Its file is not read again while it
// stays unchanged, so the tests give it a name the file does not have.
func savedEntry(file dagindex.YAMLFileMeta, name string) *indexv1.DAGIndexEntry {
	return &indexv1.DAGIndexEntry{FilePath: file.Name, FileSize: file.Size, ModTime: file.ModTime, LoadPath: file.LoadPath, Name: name}
}

// trustAll trusts every saved entry of a file written by a test.
func trustAll() time.Time { return time.Now().Add(time.Hour) }

func refresh(t *testing.T, dir string, saved []*indexv1.DAGIndexEntry, files []dagindex.YAMLFileMeta, flags dagindex.SuspendFlags) ([]*indexv1.DAGIndexEntry, bool) {
	t.Helper()
	entries, changed, err := dagindex.Refresh(t.Context(), dir, saved, trustAll(), files, flags)
	require.NoError(t, err)
	return entries, changed
}

func TestRefresh_KeepsTheEntriesOfUnchangedFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	file := writeDAG(t, dir, "a.yaml", "name: from-file\nsteps:\n  - run: echo a\n")
	saved := savedEntry(file, "saved")
	saved.Labels = []string{"env=prod"}
	saved.Suspended = true

	entries, changed := refresh(t, dir, []*indexv1.DAGIndexEntry{saved},
		[]dagindex.YAMLFileMeta{file}, dagindex.SuspendFlags{"a.suspend": {}})

	assert.False(t, changed)
	require.Len(t, entries, 1)
	assert.True(t, proto.Equal(saved, entries[0]), "the saved entry is served as it is")
}

func TestRefresh_ReloadsAChangedFile(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		change func(*indexv1.DAGIndexEntry)
	}{
		{"size", func(e *indexv1.DAGIndexEntry) { e.FileSize++ }},
		{"modification time", func(e *indexv1.DAGIndexEntry) { e.ModTime++ }},
		{"load path", func(e *indexv1.DAGIndexEntry) { e.LoadPath = "/elsewhere/a.yaml" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			file := writeDAG(t, dir, "a.yaml", "name: from-file\nsteps:\n  - run: echo a\n")
			saved := savedEntry(file, "saved")
			tc.change(saved)

			entries, changed := refresh(t, dir, []*indexv1.DAGIndexEntry{saved},
				[]dagindex.YAMLFileMeta{file}, nil)

			assert.True(t, changed)
			require.Len(t, entries, 1)
			assert.Equal(t, "from-file", entries[0].Name)
			assert.Equal(t, file.Size, entries[0].FileSize)
			assert.Equal(t, file.ModTime, entries[0].ModTime)
		})
	}
}

func TestRefresh_LoadsANewFileAndDropsARemovedOne(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	kept := writeDAG(t, dir, "kept.yaml", "steps:\n  - run: echo a\n")
	added := writeDAG(t, dir, "added.yaml", "name: added\nsteps:\n  - run: echo a\n")
	removed := dagindex.YAMLFileMeta{Name: "removed.yaml", Size: 10, ModTime: 100}

	entries, changed := refresh(t, dir,
		[]*indexv1.DAGIndexEntry{savedEntry(kept, "kept"), savedEntry(removed, "removed")},
		[]dagindex.YAMLFileMeta{kept, added}, nil)

	assert.True(t, changed)
	require.Len(t, entries, 2)
	assert.Equal(t, "kept", entries[0].Name)
	assert.Equal(t, "added", entries[1].Name)

	_, changed = refresh(t, dir,
		[]*indexv1.DAGIndexEntry{savedEntry(kept, "kept"), savedEntry(removed, "removed")},
		[]dagindex.YAMLFileMeta{kept}, nil)
	assert.True(t, changed, "dropping an entry changes the index")
}

// A file modified after trustBefore may have been written again with the same
// size and modification time, so its saved entry is not trusted.
func TestRefresh_ReloadsARecentFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	file := writeDAG(t, dir, "a.yaml", "name: from-file\nsteps:\n  - run: echo a\n")
	saved := []*indexv1.DAGIndexEntry{savedEntry(file, "saved")}
	files := []dagindex.YAMLFileMeta{file}
	modTime := time.Unix(0, file.ModTime)

	entries, changed, err := dagindex.Refresh(t.Context(), dir, saved, modTime.Add(-time.Nanosecond), files, nil)
	require.NoError(t, err)
	assert.True(t, changed)
	require.Len(t, entries, 1)
	assert.Equal(t, "from-file", entries[0].Name)

	entries, changed, err = dagindex.Refresh(t.Context(), dir, saved, modTime, files, nil)
	require.NoError(t, err)
	assert.False(t, changed)
	require.Len(t, entries, 1)
	assert.Equal(t, "saved", entries[0].Name, "a file modified at trustBefore is trusted")
}

// A suspend flag changes no DAG file, so it reaches the saved entry without a
// load, and the saved entry itself stays as it was.
func TestRefresh_AppliesSuspendFlags(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	file := writeDAG(t, dir, "a.yaml", "name: from-file\nsteps:\n  - run: echo a\n")
	saved := savedEntry(file, "saved")

	entries, changed := refresh(t, dir, []*indexv1.DAGIndexEntry{saved},
		[]dagindex.YAMLFileMeta{file}, dagindex.SuspendFlags{"a.suspend": {}})

	assert.True(t, changed)
	require.Len(t, entries, 1)
	assert.True(t, entries[0].Suspended)
	assert.Equal(t, "saved", entries[0].Name)
	assert.False(t, saved.Suspended)
}

// A cancel before every needed load is done fails the refresh, since entries
// without the remaining files would hide DAGs. A refresh that needs no load
// still serves the saved entries.
func TestRefresh_ContextEnd(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	unchanged := writeDAG(t, dir, "unchanged.yaml", "steps:\n  - run: echo a\n")
	added := writeDAG(t, dir, "added.yaml", "steps:\n  - run: echo a\n")
	saved := []*indexv1.DAGIndexEntry{savedEntry(unchanged, "unchanged")}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, _, err := dagindex.Refresh(ctx, dir, saved, trustAll(), []dagindex.YAMLFileMeta{unchanged, added}, nil)
	require.ErrorIs(t, err, context.Canceled)

	entries, changed, err := dagindex.Refresh(ctx, dir, saved, trustAll(), []dagindex.YAMLFileMeta{unchanged}, nil)
	require.NoError(t, err)
	assert.False(t, changed)
	require.Len(t, entries, 1)
	assert.Equal(t, "unchanged", entries[0].Name)
}

// TestRefresh_ClearsAnErrorAnOlderBuildRecorded covers what a user sees after
// upgrading Dagu: a DAG using syntax the previous binary could not parse
// stays listed as broken, because the file never changed and the cached entry
// is therefore still considered fresh.
func TestRefresh_ClearsAnErrorAnOlderBuildRecorded(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	file := writeDAG(t, dir, "ok.yaml", "steps:\n  - name: a\n    run: echo a\n")
	saved := savedEntry(file, "ok")
	saved.LoadError = "decoding failed due to the following error(s): 'spec.dag' has invalid keys: tasks"

	entries, changed := refresh(t, dir, []*indexv1.DAGIndexEntry{saved},
		[]dagindex.YAMLFileMeta{file}, nil)

	assert.True(t, changed, "the correction must be persisted")
	require.Len(t, entries, 1)
	assert.Empty(t, entries[0].LoadError, "the stale error is dropped once the file parses")
	assert.Equal(t, "ok", entries[0].Name)
}

// TestRefresh_KeepsARealError checks the other direction: a file that is still
// broken keeps reporting why.
func TestRefresh_KeepsARealError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	file := writeDAG(t, dir, "bad.yaml", "steps: [unclosed\n  bad: : yaml\n")
	saved := savedEntry(file, "bad")
	saved.LoadError = "some older message"

	entries, _ := refresh(t, dir, []*indexv1.DAGIndexEntry{saved},
		[]dagindex.YAMLFileMeta{file}, nil)

	require.Len(t, entries, 1)
	assert.NotEmpty(t, entries[0].LoadError)
}

// TestRefresh_ReportsMetadataChangesUnderAnUnchangedError covers a DAG a newer
// parser reads further into without being able to load it: the error text is
// the same, but the metadata around it is not, and that correction has to
// reach the index rather than being recomputed on every listing.
func TestRefresh_ReportsMetadataChangesUnderAnUnchangedError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	file := writeDAG(t, dir, "bad.yaml", "type: nonsense\nsteps:\n  - name: a\n    run: echo a\n")
	saved := savedEntry(file, "bad")
	saved.LoadError = "placeholder"
	files := []dagindex.YAMLFileMeta{file}

	entries, changed := refresh(t, dir, []*indexv1.DAGIndexEntry{saved}, files, nil)
	require.True(t, changed)
	require.Len(t, entries, 1)
	require.NotEmpty(t, entries[0].LoadError, "the file is expected to stay unloadable")

	// Same error, metadata only the previous parser produced.
	entries[0].Labels = []string{"team=infra"}

	entries, changed = refresh(t, dir, entries, files, nil)

	assert.True(t, changed, "a metadata-only correction still has to be persisted")
	assert.Empty(t, entries[0].Labels)
}
