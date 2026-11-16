// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package writegate_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/browserhost/writegate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCommitPointsAreNotedApprovedAndRevoked(t *testing.T) {
	t.Parallel()

	store := writegate.NewCommitStore(t.TempDir())
	save := writegate.CommitPoint{Key: "k1", Op: 2, Operation: "Click Save", Signature: "POST https://x/deals? {amount}", Actions: json.RawMessage(`[{"selector":"xpath=/b"}]`)}
	upload := writegate.CommitPoint{Key: "k2", Op: 3, Operation: "receipt", Signature: "POST https://x/files? {file(file)}"}
	require.NoError(t, store.Note(t.Context(), "books", "enter", save, upload))

	now := time.Now()
	approved, err := store.Approve(t.Context(), "books", "enter", nil, func(p writegate.CommitPoint) bool { return p.Key == "k1" }, now)
	require.NoError(t, err)
	require.Len(t, approved, 1)
	assert.Equal(t, []string{save.Signature}, writegate.Allowed(mustLoad(t, store, "books", "enter"), "k1"))
	assert.Empty(t, writegate.Allowed(mustLoad(t, store, "books", "enter"), "k2"))

	// Seeing the point again keeps its approval and its recording.
	again := save
	again.Actions = nil
	again.Example = &writegate.Event{URL: "https://x/deals"}
	require.NoError(t, store.Note(t.Context(), "books", "enter", again))
	points := mustLoad(t, store, "books", "enter")
	require.Len(t, points, 2)
	assert.True(t, points[0].Approved)
	assert.JSONEq(t, `[{"selector":"xpath=/b"}]`, string(points[0].Actions))
	assert.Equal(t, "https://x/deals", points[0].Example.URL)

	// Approving from another DAG copies its points.
	copied, err := store.Approve(t.Context(), "books-live", "enter", points, func(writegate.CommitPoint) bool { return true }, now)
	require.NoError(t, err)
	assert.Len(t, copied, 2)
	assert.Len(t, writegate.Allowed(mustLoad(t, store, "books-live", "enter"), "k2"), 1)

	revoked, err := store.Revoke(t.Context(), "books", "enter", func(writegate.CommitPoint) bool { return true })
	require.NoError(t, err)
	assert.Len(t, revoked, 1)
	assert.Empty(t, writegate.Allowed(mustLoad(t, store, "books", "enter"), "k1"))
	steps, err := store.Steps("books")
	require.NoError(t, err)
	assert.Equal(t, []string{"enter"}, steps)
}

func mustLoad(t *testing.T, store *writegate.CommitStore, dag, step string) []writegate.CommitPoint {
	t.Helper()
	points, err := store.Load(dag, step)
	require.NoError(t, err)
	return points
}

// DAGs and steps named con and con_ keep separate commit points on every
// system, although Windows does not store con as written.
func TestCommitPointsKeepDeviceNamesApart(t *testing.T) {
	t.Parallel()

	store := writegate.NewCommitStore(t.TempDir())
	require.NoError(t, store.Note(t.Context(), "con", "con", writegate.CommitPoint{Key: "k1"}))
	assert.Empty(t, mustLoad(t, store, "con_", "con"))
	assert.Empty(t, mustLoad(t, store, "con", "con_"))
	assert.Len(t, mustLoad(t, store, "con", "con"), 1)
}

// DAGs and steps whose names differ only in characters a file name cannot
// hold, such as etl.daily and etl_daily, keep separate commit points.
func TestCommitPointsKeepSimilarNamesApart(t *testing.T) {
	t.Parallel()

	store := writegate.NewCommitStore(t.TempDir())
	require.NoError(t, store.Note(t.Context(), "etl.daily", "a.b", writegate.CommitPoint{Key: "k1"}))
	assert.Empty(t, mustLoad(t, store, "etl_daily", "a.b"))
	assert.Empty(t, mustLoad(t, store, "etl.daily", "a_b"))
	assert.Len(t, mustLoad(t, store, "etl.daily", "a.b"), 1)
}

// A step whose raw name cleans to another step's name, such as ./b for b,
// keeps its own commit points.
func TestCommitPointsKeepCleanedNamesApart(t *testing.T) {
	t.Parallel()

	store := writegate.NewCommitStore(t.TempDir())
	require.NoError(t, store.Note(t.Context(), "books", "b", writegate.CommitPoint{Key: "k1"}))
	for _, step := range []string{"./b", "x/../b", "b/."} {
		assert.Empty(t, mustLoad(t, store, "books", step), step)
	}
}

// Earlier versions kept a device name such as con as written, which
// Windows 11 stores, so the commit points under it are still read.
func TestCommitPointsUnderRawDeviceName(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	raw := filepath.Join(dir, "writes", "con", "con.json")
	data, err := json.Marshal([]writegate.CommitPoint{{Key: "k1", Approved: true}})
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(raw), 0o700))
	require.NoError(t, os.WriteFile(raw, data, 0o600))

	store := writegate.NewCommitStore(dir)
	points := mustLoad(t, store, "con", "con")
	require.Len(t, points, 1)
	assert.True(t, points[0].Approved)
	steps, err := store.Steps("con")
	require.NoError(t, err)
	assert.Equal(t, []string{"con"}, steps)
}
