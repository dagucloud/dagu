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
		ID:         computerhost.RecordID("run-1", "post"),
		DAGName:    "invoices",
		DAGRunID:   "run-1",
		StepName:   "post",
		Generation: 2,
		Deadline:   time.Now().Add(time.Hour).UTC().Truncate(time.Second),
		Cursor:     3,
		Outputs:    map[string]any{"doc": "42"},
	}
	require.NoError(t, store.Save(record))

	loaded, err := store.Load(record.ID)
	require.NoError(t, err)
	assert.Equal(t, record, loaded)
	assert.True(t, loaded.Waiting(time.Now()))

	require.NoError(t, store.Delete(record.ID))
	require.NoError(t, store.Delete(record.ID), "deleting twice is not an error")
	_, err = store.Load(record.ID)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestStoreRemovesExpiredRecords(t *testing.T) {
	t.Parallel()

	store := computerhost.NewStore(t.TempDir())
	expired := computerhost.Record{ID: "expired", Deadline: time.Now().Add(-time.Minute)}
	require.NoError(t, store.Save(expired))
	require.NoError(t, store.Save(computerhost.Record{ID: "waiting", Deadline: time.Now().Add(time.Hour)}))

	_, err := store.Load("expired")
	assert.ErrorIs(t, err, os.ErrNotExist)
	_, err = store.Load("waiting")
	assert.NoError(t, err)
}

func TestRecordIDIsStable(t *testing.T) {
	t.Parallel()

	assert.Equal(t, computerhost.RecordID("run", "step"), computerhost.RecordID("run", "step"))
	assert.NotEqual(t, computerhost.RecordID("run", "step"), computerhost.RecordID("run", "other"))
}
