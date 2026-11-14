// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package computerhost_test

import (
	"os"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/computerhost"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStoreRoundTrip(t *testing.T) {
	t.Parallel()

	store := computerhost.NewStore(t.TempDir())
	record := computerhost.Record{
		DAGName:    "invoices",
		DAGRunID:   "run-1",
		StepName:   "post",
		Generation: 2,
		Deadline:   time.Now().Add(time.Hour).UTC().Truncate(time.Second),
		Cursor:     3,
		Outputs:    map[string]any{"doc": "42"},
	}
	require.NoError(t, store.Save(record))

	loaded, err := store.Load("run-1", "post")
	require.NoError(t, err)
	assert.Equal(t, record, loaded)
	assert.True(t, loaded.Waiting(time.Now()))

	_, err = store.Load("run-1", "other")
	require.ErrorIs(t, err, os.ErrNotExist, "each step has its own record")

	require.NoError(t, store.Delete("run-1", "post"))
	require.NoError(t, store.Delete("run-1", "post"), "deleting twice is not an error")
	_, err = store.Load("run-1", "post")
	assert.ErrorIs(t, err, os.ErrNotExist)
}

// Names are hashed into file names, so path separators in them cannot reach
// outside the store.
func TestStoreKeepsRecordsInside(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := computerhost.NewStore(dir)
	require.NoError(t, store.Save(computerhost.Record{DAGRunID: "../../run", StepName: "../step", Deadline: time.Now().Add(time.Hour)}))

	_, err := store.Load("../../run", "../step")
	require.NoError(t, err)
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Len(t, entries, 1, "only the store's own directory")
}

func TestStoreRemovesExpiredRecords(t *testing.T) {
	t.Parallel()

	store := computerhost.NewStore(t.TempDir())
	require.NoError(t, store.Save(computerhost.Record{DAGRunID: "run-1", StepName: "expired", Deadline: time.Now().Add(-time.Minute)}))
	require.NoError(t, store.Save(computerhost.Record{DAGRunID: "run-1", StepName: "waiting", Deadline: time.Now().Add(time.Hour)}))

	_, err := store.Load("run-1", "expired")
	assert.ErrorIs(t, err, os.ErrNotExist)
	_, err = store.Load("run-1", "waiting")
	assert.NoError(t, err)
}

func TestStoreRequiresStep(t *testing.T) {
	t.Parallel()

	store := computerhost.NewStore(t.TempDir())
	require.Error(t, store.Save(computerhost.Record{DAGRunID: "run-1"}))
}
