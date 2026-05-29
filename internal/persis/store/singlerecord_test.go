// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/internal/persis"
	"github.com/dagucloud/dagu/internal/persis/store"
	"github.com/dagucloud/dagu/internal/persis/testutil"
)

type singleRecordValue struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

func newSingleRecord(t *testing.T) *store.SingleRecord[singleRecordValue] {
	t.Helper()
	col := testutil.NewMemoryBackend().Collection("single")
	return store.NewSingleRecord[singleRecordValue](col, "the-record")
}

func TestSingleRecord_SaveLoad_RoundTrip(t *testing.T) {
	t.Parallel()
	s := newSingleRecord(t)
	ctx := context.Background()

	require.NoError(t, s.Save(ctx, &singleRecordValue{Name: "alpha", Count: 7}))

	var got singleRecordValue
	found, err := s.Load(ctx, &got)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, singleRecordValue{Name: "alpha", Count: 7}, got)
}

func TestSingleRecord_Load_AbsentLeavesDstUntouched(t *testing.T) {
	t.Parallel()
	s := newSingleRecord(t)

	// Pre-populate dst with defaults; an absent record must preserve them.
	dst := singleRecordValue{Name: "default", Count: 42}
	found, err := s.Load(context.Background(), &dst)
	require.NoError(t, err)
	assert.False(t, found)
	assert.Equal(t, singleRecordValue{Name: "default", Count: 42}, dst)
}

func TestSingleRecord_Load_DecodesOverDefaults(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// Store a partial payload: only "name" is set.
	col := testutil.NewMemoryBackend().Collection("single")
	s := store.NewSingleRecord[singleRecordValue](col, "the-record")
	require.NoError(t, col.Put(ctx, &persis.Record{ID: "the-record", Data: []byte(`{"name":"stored"}`)}))

	// dst keeps its default Count because the payload omits the field.
	dst := singleRecordValue{Name: "default", Count: 99}
	found, err := s.Load(ctx, &dst)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, singleRecordValue{Name: "stored", Count: 99}, dst)
}

func TestSingleRecord_Load_CorruptReportsErrCorrupt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	col := testutil.NewMemoryBackend().Collection("single")
	s := store.NewSingleRecord[singleRecordValue](col, "the-record")

	require.NoError(t, col.Put(ctx, &persis.Record{ID: "the-record", Data: []byte("not valid json {{")}))

	var got singleRecordValue
	found, err := s.Load(ctx, &got)
	require.Error(t, err)
	assert.ErrorIs(t, err, store.ErrCorrupt)
	assert.False(t, found)
}

func TestSingleRecord_Delete(t *testing.T) {
	t.Parallel()
	s := newSingleRecord(t)
	ctx := context.Background()

	require.NoError(t, s.Save(ctx, &singleRecordValue{Name: "alpha"}))
	require.NoError(t, s.Delete(ctx))

	var got singleRecordValue
	found, err := s.Load(ctx, &got)
	require.NoError(t, err)
	assert.False(t, found)

	// Delete is idempotent: removing a missing record is not an error.
	require.NoError(t, s.Delete(ctx))
}
