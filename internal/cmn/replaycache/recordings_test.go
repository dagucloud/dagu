// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package replaycache_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/cmn/replaycache"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func openRecordings(path string) *replaycache.Recordings[string] {
	return replaycache.Open[string](path)
}

// commitRecording writes one entry the way a successful run does.
func commitRecording(t *testing.T, path, key, entry string) {
	t.Helper()
	recordings := openRecordings(path)
	recordings.Stage(key, entry)
	require.NoError(t, recordings.Commit(context.Background()))
}

func lookup(path, key string) (string, bool) {
	return openRecordings(path).Lookup(key)
}

func TestRecordingsKeepOnlyCommitted(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "step.json")
	recordings := openRecordings(path)
	recordings.Stage("act", "click")
	_, ok := lookup(path, "act")
	assert.False(t, ok, "a staged recording is not visible before commit")

	require.NoError(t, recordings.Commit(context.Background()))
	entry, ok := lookup(path, "act")
	require.True(t, ok)
	assert.Equal(t, "click", entry)

	failed := openRecordings(path)
	failed.Stage("other", "type")
	failed.Discard()
	require.NoError(t, failed.Commit(context.Background()))
	_, ok = lookup(path, "other")
	assert.False(t, ok, "a discarded recording is never written")
}

// Find serves a lookup whose key is not known in advance, and what it
// finds can be dropped like a looked-up recording.
func TestRecordingsFind(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "step.json")
	commitRecording(t, path, "b", "second")
	commitRecording(t, path, "a", "first")

	recordings := openRecordings(path)
	key, entry, ok := recordings.Find(func(_ string, entry string) bool { return strings.HasPrefix(entry, "s") })
	require.True(t, ok)
	assert.Equal(t, "b", key)
	assert.Equal(t, "second", entry)
	_, _, ok = recordings.Find(func(string, string) bool { return false })
	assert.False(t, ok)
	key, _, ok = recordings.Find(func(string, string) bool { return true })
	require.True(t, ok)
	assert.Equal(t, "a", key, "keys are visited in order")

	recordings.Drop("b")
	require.NoError(t, recordings.Commit(context.Background()))
	_, ok = lookup(path, "b")
	assert.False(t, ok, "a found recording that was dropped is removed")
	_, ok = lookup(path, "a")
	assert.True(t, ok)

	replaced := openRecordings(path)
	_, _, ok = replaced.Find(func(key string, _ string) bool { return key == "a" })
	require.True(t, ok)
	commitRecording(t, path, "a", "newer")
	replaced.Drop("a")
	require.NoError(t, replaced.Commit(context.Background()))
	entry, ok = lookup(path, "a")
	require.True(t, ok, "a recording another run replaced since is kept")
	assert.Equal(t, "newer", entry)

	_, _, ok = openRecordings(filepath.Join(t.TempDir(), "none.json")).Find(func(string, string) bool { return true })
	assert.False(t, ok, "a missing file holds none")
}

// Runs of one DAG share the file, so a commit merges into what other runs
// wrote after it was opened.
func TestRecordingsCommitMergesConcurrentRuns(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "step.json")
	first := openRecordings(path)
	second := openRecordings(path)
	first.Stage("a", "click")
	second.Stage("b", "type")
	require.NoError(t, first.Commit(context.Background()))
	require.NoError(t, second.Commit(context.Background()))

	for key, want := range map[string]string{"a": "click", "b": "type"} {
		entry, ok := lookup(path, key)
		require.True(t, ok, key)
		assert.Equal(t, want, entry)
	}
}

func TestRecordingsEvictReplayed(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "step.json")
	commitRecording(t, path, "a", "click")
	commitRecording(t, path, "b", "type")

	failed := openRecordings(path)
	_, ok := failed.Lookup("a")
	require.True(t, ok)
	failed.Stage("c", "scroll")
	require.NoError(t, failed.Evict(context.Background()))

	_, ok = lookup(path, "a")
	assert.False(t, ok, "the replayed recording is dropped")
	_, ok = lookup(path, "b")
	assert.True(t, ok, "a recording the run did not replay stays")
	_, ok = lookup(path, "c")
	assert.False(t, ok, "what the failed run recorded is not kept")
}

// A recording another run replaced after this run replayed it is not
// this run's to drop.
func TestRecordingsEvictKeepsReplacedEntry(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "step.json")
	commitRecording(t, path, "a", "click")
	failed := openRecordings(path)
	_, ok := failed.Lookup("a")
	require.True(t, ok)
	commitRecording(t, path, "a", "double click")

	require.NoError(t, failed.Evict(context.Background()))
	entry, ok := lookup(path, "a")
	require.True(t, ok)
	assert.Equal(t, "double click", entry)
}

// Runs of a step can start together and act one after another, such as
// foreach items that wait for one browser profile, so a lookup sees what
// other runs dropped or replaced after this run began.
func TestRecordingsLookupSeesOtherRuns(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "step.json")
	commitRecording(t, path, "a", "click")
	commitRecording(t, path, "b", "click")
	waiting := openRecordings(path)

	failed := openRecordings(path)
	_, ok := failed.Lookup("a")
	require.True(t, ok)
	require.NoError(t, failed.Evict(context.Background()))
	commitRecording(t, path, "b", "double click")

	_, ok = waiting.Lookup("a")
	assert.False(t, ok, "a recording another run dropped is not replayed")
	entry, ok := waiting.Lookup("b")
	require.True(t, ok)
	assert.Equal(t, "double click", entry, "a recording another run replaced is replayed as replaced")
}

// A recording that no longer replays is dropped once the run that healed it
// is kept, unless the run recorded what it did instead or another run
// replaced it.
func TestRecordingsCommitDropsHealed(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "step.json")
	for _, key := range []string{"a", "b", "c"} {
		commitRecording(t, path, key, "click")
	}
	healed := openRecordings(path)
	for _, key := range []string{"a", "b", "c"} {
		_, ok := healed.Lookup(key)
		require.True(t, ok, key)
		healed.Drop(key)
	}
	healed.Stage("b", "double click")
	commitRecording(t, path, "c", "type")
	require.NoError(t, healed.Commit(context.Background()))

	_, ok := lookup(path, "a")
	assert.False(t, ok, "the dropped recording is removed")
	entry, ok := lookup(path, "b")
	require.True(t, ok)
	assert.Equal(t, "double click", entry, "what the run recorded replaces the drop")
	entry, ok = lookup(path, "c")
	require.True(t, ok)
	assert.Equal(t, "type", entry, "a recording another run replaced stays")
}

func TestRecordingsEvictLastEntryRemovesFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "step.json")
	commitRecording(t, path, "a", "click")
	failed := openRecordings(path)
	_, _ = failed.Lookup("a")
	require.NoError(t, failed.Evict(context.Background()))

	_, err := os.Stat(path)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

// A step paused for input resumes in a new process, which takes over what
// the paused attempt recorded, dropped, and replayed.
func TestRecordingsHeldAcrossPause(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "step.json")
	commitRecording(t, path, "a", "click")
	commitRecording(t, path, "c", "scroll")
	paused := openRecordings(path)
	_, _ = paused.Lookup("a")
	paused.Stage("b", "type")
	_, _ = paused.Lookup("c")
	paused.Drop("c")
	pending, used := paused.Held()

	resumed := openRecordings(path)
	resumed.Hold(pending, used)
	require.NoError(t, resumed.Commit(context.Background()))
	entry, ok := lookup(path, "b")
	require.True(t, ok)
	assert.Equal(t, "type", entry)
	_, ok = lookup(path, "c")
	assert.False(t, ok, "the drop before the pause is applied")

	resumed = openRecordings(path)
	resumed.Hold(pending, used)
	require.NoError(t, resumed.Evict(context.Background()))
	_, ok = lookup(path, "a")
	assert.False(t, ok, "the replay before the pause is dropped")
}
