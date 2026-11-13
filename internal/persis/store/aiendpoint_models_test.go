// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/aiendpoint"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/persis/testutil"
)

func newAIEndpointModelsStore(t *testing.T) *store.AIEndpointModelsStore {
	t.Helper()
	s, err := store.NewAIEndpointModelsStore(testutil.NewMemoryBackend().Collection("ai_endpoint_models"))
	require.NoError(t, err)
	return s
}

func TestAIEndpointModelsStorePutGetListDelete(t *testing.T) {
	ctx := context.Background()
	s := newAIEndpointModelsStore(t)
	now := time.Date(2026, 11, 4, 8, 0, 0, 0, time.UTC)
	global := &aiendpoint.Models{
		Scope: aiendpoint.GlobalScope(), Name: "cle1", Names: []string{"a", "b"},
		RefreshedAt: now, AttemptedAt: now, LastError: "the provider answered with the status 500", LastErrorAt: now,
	}
	team := &aiendpoint.Models{Scope: aiendpoint.WorkspaceScope("team"), Name: "cle1", Names: []string{"c"}}

	_, err := s.Get(ctx, aiendpoint.GlobalScope(), "cle1")
	assert.ErrorIs(t, err, aiendpoint.ErrNotFound)

	require.NoError(t, s.Put(ctx, global))
	require.NoError(t, s.Put(ctx, team))
	got, err := s.Get(ctx, aiendpoint.GlobalScope(), "cle1")
	require.NoError(t, err)
	assert.Equal(t, global, got)

	// The same name in two scopes has two lists. A put replaces the old list.
	global.Names = []string{"z"}
	require.NoError(t, s.Put(ctx, global))
	all, err := s.List(ctx)
	require.NoError(t, err)
	require.Len(t, all, 2)
	got, err = s.Get(ctx, aiendpoint.GlobalScope(), "cle1")
	require.NoError(t, err)
	assert.Equal(t, []string{"z"}, got.Names)
	got, err = s.Get(ctx, aiendpoint.WorkspaceScope("team"), "cle1")
	require.NoError(t, err)
	assert.Equal(t, []string{"c"}, got.Names)

	require.NoError(t, s.Delete(ctx, aiendpoint.GlobalScope(), "cle1"))
	require.NoError(t, s.Delete(ctx, aiendpoint.GlobalScope(), "cle1"), "a delete of a missing list gives no error")
	_, err = s.Get(ctx, aiendpoint.GlobalScope(), "cle1")
	assert.ErrorIs(t, err, aiendpoint.ErrNotFound)
	all, err = s.List(ctx)
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.Equal(t, aiendpoint.WorkspaceScope("team"), all[0].Scope)
}

// A list with details loads back with them. A record of an older server has no
// field Info and still loads.
func TestAIEndpointModelsStoreKeepsInfo(t *testing.T) {
	ctx := context.Background()
	s := newAIEndpointModelsStore(t)
	withInfo := &aiendpoint.Models{
		Scope: aiendpoint.GlobalScope(), Name: "cle1", Names: []string{"a", "b"},
		Info: []aiendpoint.ModelInfo{{ID: "a", DisplayName: "Model A", Efforts: []string{"low", "max"}}},
	}
	require.NoError(t, s.Put(ctx, withInfo))
	got, err := s.Get(ctx, aiendpoint.GlobalScope(), "cle1")
	require.NoError(t, err)
	assert.Equal(t, withInfo, got)

	old := &aiendpoint.Models{Scope: aiendpoint.GlobalScope(), Name: "cle2", Names: []string{"a"}}
	require.NoError(t, s.Put(ctx, old))
	got, err = s.Get(ctx, aiendpoint.GlobalScope(), "cle2")
	require.NoError(t, err)
	assert.Equal(t, old, got)
	assert.Nil(t, got.Info)
}

func TestAIEndpointModelsStoreRejectsABadName(t *testing.T) {
	s := newAIEndpointModelsStore(t)
	err := s.Put(context.Background(), &aiendpoint.Models{Name: "Bad Name"})
	assert.ErrorIs(t, err, aiendpoint.ErrInvalid)
}

// A list that cannot be read, such as a partial restore, counts as no list. It
// does not fail the list of all endpoints, and the next read replaces it.
func TestAIEndpointModelsStoreTreatsABadRecordAsNoList(t *testing.T) {
	ctx := context.Background()
	col := testutil.NewMemoryBackend().Collection("ai_endpoint_models")
	s, err := store.NewAIEndpointModelsStore(col)
	require.NoError(t, err)
	require.NoError(t, col.Put(ctx, &persis.Record{ID: "global/cle1", Data: []byte(`{"scope":"global"}`)}))
	require.NoError(t, s.Put(ctx, &aiendpoint.Models{Scope: aiendpoint.GlobalScope(), Name: "cle2", Names: []string{"m"}}))

	_, err = s.Get(ctx, aiendpoint.GlobalScope(), "cle1")
	require.ErrorIs(t, err, aiendpoint.ErrNotFound)
	all, err := s.List(ctx)
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.Equal(t, "cle2", all[0].Name)

	require.NoError(t, s.Put(ctx, &aiendpoint.Models{Scope: aiendpoint.GlobalScope(), Name: "cle1", Names: []string{"x"}}))
	got, err := s.Get(ctx, aiendpoint.GlobalScope(), "cle1")
	require.NoError(t, err)
	assert.Equal(t, []string{"x"}, got.Names)
}
