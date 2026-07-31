// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagindex_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/persis/file/dag/dagindex"
	indexv1 "github.com/dagucloud/dagu/v2/proto/index/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRefreshFailures_ClearsAnErrorAnOlderBuildRecorded covers what a user sees
// after upgrading Dagu: a DAG using syntax the previous binary could not parse
// stays listed as broken, because the file never changed and the cached entry is
// therefore still considered fresh.
func TestRefreshFailures_ClearsAnErrorAnOlderBuildRecorded(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ok.yaml"),
		[]byte("steps:\n  - name: a\n    run: echo a\n"), 0o600))

	entries := []*indexv1.DAGIndexEntry{{
		FilePath:  "ok.yaml",
		Name:      "ok",
		LoadError: "decoding failed due to the following error(s): 'spec.dag' has invalid keys: tasks",
	}}

	changed := dagindex.RefreshFailures(t.Context(), dir, entries, dagindex.SuspendFlags{})

	assert.True(t, changed, "the correction must be persisted")
	assert.Empty(t, entries[0].LoadError, "the stale error is dropped once the file parses")
	assert.Equal(t, "ok", entries[0].Name)
}

// TestRefreshFailures_KeepsARealError checks the other direction: a file that is
// still broken keeps reporting why.
func TestRefreshFailures_KeepsARealError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bad.yaml"),
		[]byte("steps: [unclosed\n  bad: : yaml\n"), 0o600))

	entries := []*indexv1.DAGIndexEntry{{
		FilePath:  "bad.yaml",
		Name:      "bad",
		LoadError: "some older message",
	}}

	dagindex.RefreshFailures(t.Context(), dir, entries, dagindex.SuspendFlags{})
	assert.NotEmpty(t, entries[0].LoadError)
}

// TestRefreshFailures_ReportsMetadataChangesUnderAnUnchangedError covers a DAG a
// newer parser reads further into without being able to load it: the error text
// is the same, but the metadata around it is not, and that correction has to
// reach the index rather than being recomputed on every listing.
func TestRefreshFailures_ReportsMetadataChangesUnderAnUnchangedError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bad.yaml"),
		[]byte("type: nonsense\nsteps:\n  - name: a\n    run: echo a\n"), 0o600))

	entries := []*indexv1.DAGIndexEntry{{FilePath: "bad.yaml", LoadError: "placeholder"}}
	require.True(t, dagindex.RefreshFailures(t.Context(), dir, entries, dagindex.SuspendFlags{}))
	require.NotEmpty(t, entries[0].LoadError, "the file is expected to stay unloadable")

	// Same error, metadata only the previous parser produced.
	entries[0].Labels = []string{"team=infra"}

	changed := dagindex.RefreshFailures(t.Context(), dir, entries, dagindex.SuspendFlags{})

	assert.True(t, changed, "a metadata-only correction still has to be persisted")
	assert.Empty(t, entries[0].Labels)
}

// TestRefreshFailures_LeavesHealthyEntriesAlone keeps the fast path intact: a
// cached success is not re-parsed on every listing.
func TestRefreshFailures_LeavesHealthyEntriesAlone(t *testing.T) {
	t.Parallel()

	entries := []*indexv1.DAGIndexEntry{{FilePath: "gone.yaml", Name: "cached"}}

	changed := dagindex.RefreshFailures(
		t.Context(), t.TempDir(), entries, dagindex.SuspendFlags{})

	assert.False(t, changed)
	assert.Equal(t, "cached", entries[0].Name, "a healthy entry is served from cache")
	assert.Empty(t, entries[0].LoadError)
}
