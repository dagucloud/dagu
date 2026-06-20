// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/internal/persis/store"
	"github.com/dagucloud/dagu/internal/persis/testutil"
	"github.com/dagucloud/dagu/internal/view"
)

func newViewStore(t *testing.T) *store.ViewStore {
	t.Helper()
	col := testutil.NewMemoryBackend().Collection("views")
	s, err := store.NewViewStore(col)
	require.NoError(t, err)
	return s
}

func newView(id string, createdAt time.Time) *view.View {
	return &view.View{
		ID:           id,
		Name:         "view-" + id,
		Type:         view.TypeKanban,
		IntervalDays: 3,
		CreatedAt:    createdAt,
		UpdatedAt:    createdAt,
	}
}

func TestViewStore_CreateAndGet(t *testing.T) {
	ctx := context.Background()
	s := newViewStore(t)
	v := newView("a", time.Now().UTC())

	require.NoError(t, s.Create(ctx, v))

	got, err := s.GetByID(ctx, "a")
	require.NoError(t, err)
	assert.Equal(t, v.ID, got.ID)
	assert.Equal(t, v.Name, got.Name)
	assert.Equal(t, view.TypeKanban, got.Type)
	assert.Equal(t, 3, got.IntervalDays)
}

func TestViewStore_CreateDuplicate(t *testing.T) {
	ctx := context.Background()
	s := newViewStore(t)
	require.NoError(t, s.Create(ctx, newView("dup", time.Now().UTC())))
	assert.ErrorIs(t, s.Create(ctx, newView("dup", time.Now().UTC())), view.ErrViewExists)
}

func TestViewStore_GetNotFound(t *testing.T) {
	_, err := newViewStore(t).GetByID(context.Background(), "missing")
	assert.ErrorIs(t, err, view.ErrViewNotFound)
}

func TestViewStore_ListOrderedByCreatedAt(t *testing.T) {
	ctx := context.Background()
	s := newViewStore(t)
	base := time.Now().UTC()

	// Insert out of chronological order.
	require.NoError(t, s.Create(ctx, newView("b", base.Add(2*time.Minute))))
	require.NoError(t, s.Create(ctx, newView("a", base.Add(1*time.Minute))))
	require.NoError(t, s.Create(ctx, newView("c", base.Add(3*time.Minute))))

	views, err := s.List(ctx)
	require.NoError(t, err)
	require.Len(t, views, 3)
	assert.Equal(t, []string{"a", "b", "c"}, []string{views[0].ID, views[1].ID, views[2].ID})
}

func TestViewStore_Update(t *testing.T) {
	ctx := context.Background()
	s := newViewStore(t)
	created := time.Now().UTC().Add(-time.Hour)
	require.NoError(t, s.Create(ctx, newView("a", created)))

	update := newView("a", time.Time{}) // caller leaves CreatedAt unset
	update.Name = "renamed"
	update.IntervalDays = 10
	require.NoError(t, s.Update(ctx, update))

	got, err := s.GetByID(ctx, "a")
	require.NoError(t, err)
	assert.Equal(t, "renamed", got.Name)
	assert.Equal(t, 10, got.IntervalDays)
	assert.Equal(t, created.Truncate(time.Millisecond), got.CreatedAt.Truncate(time.Millisecond), "CreatedAt preserved")
	assert.True(t, got.UpdatedAt.After(created), "UpdatedAt advanced")
}

func TestViewStore_UpdateNotFound(t *testing.T) {
	err := newViewStore(t).Update(context.Background(), newView("ghost", time.Now().UTC()))
	assert.ErrorIs(t, err, view.ErrViewNotFound)
}

func TestViewStore_Delete(t *testing.T) {
	ctx := context.Background()
	s := newViewStore(t)
	require.NoError(t, s.Create(ctx, newView("a", time.Now().UTC())))

	require.NoError(t, s.Delete(ctx, "a"))

	_, err := s.GetByID(ctx, "a")
	assert.ErrorIs(t, err, view.ErrViewNotFound)
}

func TestViewStore_DeleteNotFound(t *testing.T) {
	assert.ErrorIs(t, newViewStore(t).Delete(context.Background(), "missing"), view.ErrViewNotFound)
}
