// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/internal/cmn/dirlock"
	"github.com/dagucloud/dagu/internal/persis"
	"github.com/dagucloud/dagu/internal/persis/file"
	"github.com/dagucloud/dagu/internal/persis/testutil"
)

// RunCollectionContract runs the full Collection contract against any backend.
// Used by both the file and memory backend tests.
func RunCollectionContract(t *testing.T, col persis.Collection, freshCollection func(t *testing.T) persis.Collection) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)

	t.Run("get_missing", func(t *testing.T) {
		_, err := col.Get(ctx, "no-such-id")
		assert.ErrorIs(t, err, persis.ErrNotFound)
	})

	t.Run("put_and_get", func(t *testing.T) {
		rec := &persis.Record{
			ID:        "alpha",
			Data:      []byte(`{"v":1}`),
			Encoding:  persis.EncodingJSON,
			CreatedAt: now,
			UpdatedAt: now,
		}
		require.NoError(t, col.Put(ctx, rec))

		got, err := col.Get(ctx, "alpha")
		require.NoError(t, err)
		assert.Equal(t, rec.ID, got.ID)
		assert.Equal(t, rec.Data, got.Data)
		assert.Equal(t, rec.Encoding, got.Encoding)
	})

	t.Run("put_overwrites", func(t *testing.T) {
		rec := &persis.Record{
			ID:        "beta",
			Data:      []byte(`{"v":1}`),
			Encoding:  persis.EncodingJSON,
			CreatedAt: now,
			UpdatedAt: now,
		}
		require.NoError(t, col.Put(ctx, rec))

		rec.Data = []byte(`{"v":2}`)
		require.NoError(t, col.Put(ctx, rec))

		got, err := col.Get(ctx, "beta")
		require.NoError(t, err)
		assert.Equal(t, []byte(`{"v":2}`), got.Data)
	})

	t.Run("delete", func(t *testing.T) {
		rec := &persis.Record{
			ID:        "gamma",
			Data:      []byte(`{}`),
			Encoding:  persis.EncodingJSON,
			CreatedAt: now,
			UpdatedAt: now,
		}
		require.NoError(t, col.Put(ctx, rec))
		require.NoError(t, col.Delete(ctx, "gamma"))

		_, err := col.Get(ctx, "gamma")
		assert.ErrorIs(t, err, persis.ErrNotFound)
	})

	t.Run("delete_missing_is_noop", func(t *testing.T) {
		assert.NoError(t, col.Delete(ctx, "nonexistent"))
	})

	t.Run("compare_and_delete_ok", func(t *testing.T) {
		col2 := freshCollection(t)
		rec := &persis.Record{
			ID:        "cad-ok",
			Data:      []byte(`{"v":1}`),
			Encoding:  persis.EncodingJSON,
			CreatedAt: now,
			UpdatedAt: now,
		}
		require.NoError(t, col2.Put(ctx, rec))
		got, err := col2.Get(ctx, "cad-ok")
		require.NoError(t, err)
		require.NoError(t, col2.CompareAndDelete(ctx, got))

		_, err = col2.Get(ctx, "cad-ok")
		assert.ErrorIs(t, err, persis.ErrNotFound)
	})

	t.Run("compare_and_delete_conflict", func(t *testing.T) {
		col2 := freshCollection(t)
		rec := &persis.Record{
			ID:        "cad-conflict",
			Data:      []byte(`{"v":1}`),
			Encoding:  persis.EncodingJSON,
			CreatedAt: now,
			UpdatedAt: now,
		}
		require.NoError(t, col2.Put(ctx, rec))
		expected, err := col2.Get(ctx, "cad-conflict")
		require.NoError(t, err)
		rec.Data = []byte(`{"v":2}`)
		rec.UpdatedAt = now.Add(time.Second)
		require.NoError(t, col2.Put(ctx, rec))

		err = col2.CompareAndDelete(ctx, expected)
		assert.ErrorIs(t, err, persis.ErrConflict)
		_, err = col2.Get(ctx, "cad-conflict")
		assert.NoError(t, err)
	})

	t.Run("list_all", func(t *testing.T) {
		col2 := freshCollection(t)
		t1 := now.Add(time.Millisecond)
		t2 := now.Add(2 * time.Millisecond)
		t3 := now.Add(3 * time.Millisecond)
		for _, r := range []*persis.Record{
			{ID: "x/a", Data: []byte(`{}`), Encoding: persis.EncodingJSON, CreatedAt: t2, UpdatedAt: t2},
			{ID: "x/b", Data: []byte(`{}`), Encoding: persis.EncodingJSON, CreatedAt: t1, UpdatedAt: t1},
			{ID: "y/c", Data: []byte(`{}`), Encoding: persis.EncodingJSON, CreatedAt: t3, UpdatedAt: t3},
		} {
			require.NoError(t, col2.Put(ctx, r))
		}
		page, err := col2.List(ctx, persis.ListQuery{})
		require.NoError(t, err)
		require.Len(t, page.Records, 3)
		// ordered by CreatedAt ascending
		assert.Equal(t, "x/b", page.Records[0].ID)
		assert.Equal(t, "x/a", page.Records[1].ID)
		assert.Equal(t, "y/c", page.Records[2].ID)
	})

	t.Run("list_prefix", func(t *testing.T) {
		col2 := freshCollection(t)
		t1 := now.Add(time.Millisecond)
		t2 := now.Add(2 * time.Millisecond)
		for _, r := range []*persis.Record{
			{ID: "dag1/run1", Data: []byte(`{}`), Encoding: persis.EncodingJSON, CreatedAt: t1, UpdatedAt: t1},
			{ID: "dag1/run2", Data: []byte(`{}`), Encoding: persis.EncodingJSON, CreatedAt: t2, UpdatedAt: t2},
			{ID: "dag2/run1", Data: []byte(`{}`), Encoding: persis.EncodingJSON, CreatedAt: t1, UpdatedAt: t1},
		} {
			require.NoError(t, col2.Put(ctx, r))
		}
		page, err := col2.List(ctx, persis.ListQuery{Prefix: "dag1/"})
		require.NoError(t, err)
		require.Len(t, page.Records, 2)
		assert.Equal(t, "dag1/run1", page.Records[0].ID)
		assert.Equal(t, "dag1/run2", page.Records[1].ID)
	})

	t.Run("list_time_range", func(t *testing.T) {
		col2 := freshCollection(t)
		t1 := now.Add(1 * time.Millisecond)
		t2 := now.Add(2 * time.Millisecond)
		t3 := now.Add(3 * time.Millisecond)
		for _, r := range []*persis.Record{
			{ID: "r1", Data: []byte(`{}`), Encoding: persis.EncodingJSON, CreatedAt: t1, UpdatedAt: t1},
			{ID: "r2", Data: []byte(`{}`), Encoding: persis.EncodingJSON, CreatedAt: t2, UpdatedAt: t2},
			{ID: "r3", Data: []byte(`{}`), Encoding: persis.EncodingJSON, CreatedAt: t3, UpdatedAt: t3},
		} {
			require.NoError(t, col2.Put(ctx, r))
		}
		since := t2
		page, err := col2.List(ctx, persis.ListQuery{Since: &since})
		require.NoError(t, err)
		require.Len(t, page.Records, 2)
		assert.Equal(t, "r2", page.Records[0].ID)
		assert.Equal(t, "r3", page.Records[1].ID)
	})

	t.Run("list_pagination", func(t *testing.T) {
		col2 := freshCollection(t)
		for i := range 5 {
			ts := now.Add(time.Duration(i) * time.Millisecond)
			r := &persis.Record{
				ID:        []string{"p0", "p1", "p2", "p3", "p4"}[i],
				Data:      []byte(`{}`),
				Encoding:  persis.EncodingJSON,
				CreatedAt: ts,
				UpdatedAt: ts,
			}
			require.NoError(t, col2.Put(ctx, r))
		}

		page1, err := col2.List(ctx, persis.ListQuery{Limit: 2})
		require.NoError(t, err)
		require.Len(t, page1.Records, 2)
		assert.NotEmpty(t, page1.NextCursor)

		page2, err := col2.List(ctx, persis.ListQuery{Limit: 2, Cursor: page1.NextCursor})
		require.NoError(t, err)
		require.Len(t, page2.Records, 2)
		assert.NotEmpty(t, page2.NextCursor)

		page3, err := col2.List(ctx, persis.ListQuery{Limit: 2, Cursor: page2.NextCursor})
		require.NoError(t, err)
		require.Len(t, page3.Records, 1)
		assert.Empty(t, page3.NextCursor)

		all := append(append(page1.Records, page2.Records...), page3.Records...)
		for i, r := range all {
			assert.Equal(t, []string{"p0", "p1", "p2", "p3", "p4"}[i], r.ID)
		}
	})

	t.Run("compare_and_swap_ok", func(t *testing.T) {
		col2 := freshCollection(t)
		rec := &persis.Record{
			ID:        "cas-ok",
			Data:      []byte(`{"v":1}`),
			Encoding:  persis.EncodingJSON,
			CreatedAt: now,
			UpdatedAt: now,
		}
		require.NoError(t, col2.Put(ctx, rec))
		require.NoError(t, col2.CompareAndSwap(ctx, "cas-ok", []byte(`{"v":1}`), []byte(`{"v":2}`)))

		got, err := col2.Get(ctx, "cas-ok")
		require.NoError(t, err)
		assert.Equal(t, []byte(`{"v":2}`), got.Data)
	})

	t.Run("compare_and_swap_conflict", func(t *testing.T) {
		col2 := freshCollection(t)
		rec := &persis.Record{
			ID:        "cas-conflict",
			Data:      []byte(`{"v":1}`),
			Encoding:  persis.EncodingJSON,
			CreatedAt: now,
			UpdatedAt: now,
		}
		require.NoError(t, col2.Put(ctx, rec))
		err := col2.CompareAndSwap(ctx, "cas-conflict", []byte(`{"v":99}`), []byte(`{"v":2}`))
		assert.ErrorIs(t, err, persis.ErrConflict)
	})

	t.Run("claim", func(t *testing.T) {
		col2 := freshCollection(t)
		t1 := now.Add(time.Millisecond)
		t2 := now.Add(2 * time.Millisecond)
		for _, r := range []*persis.Record{
			{ID: "q/high_001", Data: []byte(`{"n":1}`), Encoding: persis.EncodingJSON, CreatedAt: t1, UpdatedAt: t1},
			{ID: "q/high_002", Data: []byte(`{"n":2}`), Encoding: persis.EncodingJSON, CreatedAt: t2, UpdatedAt: t2},
		} {
			require.NoError(t, col2.Put(ctx, r))
		}

		claimed, err := col2.Claim(ctx, persis.ListQuery{Prefix: "q/"})
		require.NoError(t, err)
		assert.Equal(t, "q/high_001", claimed.ID)

		// verify it's gone
		_, err = col2.Get(ctx, "q/high_001")
		assert.ErrorIs(t, err, persis.ErrNotFound)

		// second claim gets next
		claimed2, err := col2.Claim(ctx, persis.ListQuery{Prefix: "q/"})
		require.NoError(t, err)
		assert.Equal(t, "q/high_002", claimed2.ID)

		// nothing left
		_, err = col2.Claim(ctx, persis.ListQuery{Prefix: "q/"})
		assert.ErrorIs(t, err, persis.ErrNotFound)
	})

	t.Run("hierarchical_ids", func(t *testing.T) {
		col2 := freshCollection(t)
		t1 := now.Add(time.Millisecond)
		rec := &persis.Record{
			ID:        "dag/run-1/attempt-0",
			Data:      []byte(`{"status":"ok"}`),
			Encoding:  persis.EncodingJSON,
			CreatedAt: t1,
			UpdatedAt: t1,
		}
		require.NoError(t, col2.Put(ctx, rec))

		got, err := col2.Get(ctx, "dag/run-1/attempt-0")
		require.NoError(t, err)
		assert.Equal(t, "dag/run-1/attempt-0", got.ID)

		page, err := col2.List(ctx, persis.ListQuery{Prefix: "dag/run-1/"})
		require.NoError(t, err)
		require.Len(t, page.Records, 1)
	})
}

func TestFileCollection(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	b, err := file.New(root)
	require.NoError(t, err)

	freshCollection := func(t *testing.T) persis.Collection {
		b2, err := file.New(t.TempDir())
		require.NoError(t, err)
		return b2.Collection("test")
	}

	RunCollectionContract(t, b.Collection("test"), freshCollection)
}

func TestFileCollectionWithLockOptionsUsesCustomTiming(t *testing.T) {
	t.Parallel()

	type lockOptionsCollection interface {
		WithLockOptions(ctx context.Context, key string, opts dirlock.LockOptions, fn func() error) error
	}

	col, ok := file.NewCollection(t.TempDir()).(lockOptionsCollection)
	require.True(t, ok)

	ctx := context.Background()
	entered := make(chan struct{})
	release := make(chan struct{})
	firstErr := make(chan error, 1)
	go func() {
		firstErr <- col.WithLockOptions(ctx, "shared", dirlock.LockOptions{
			StaleThreshold: time.Hour,
			RetryInterval:  time.Millisecond,
		}, func() error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered

	stealCtx, cancelSteal := context.WithTimeout(ctx, time.Second)
	defer cancelSteal()
	require.NoError(t, col.WithLockOptions(stealCtx, "shared", dirlock.LockOptions{
		StaleThreshold: 20 * time.Millisecond,
		RetryInterval:  5 * time.Millisecond,
	}, func() error {
		return nil
	}))

	close(release)
	require.NoError(t, <-firstErr)
}

func TestFileCollectionWithLockRootScopesLocksOutsideCollection(t *testing.T) {
	t.Parallel()

	type lockOptionsCollection interface {
		WithLockOptions(ctx context.Context, key string, opts dirlock.LockOptions, fn func() error) error
	}

	root := filepath.Join(t.TempDir(), "distributed")
	collectionDir := filepath.Join(root, "leases")
	col, ok := file.NewCollectionWithLockRoot(collectionDir, root).(lockOptionsCollection)
	require.True(t, ok)

	require.NoError(t, col.WithLockOptions(context.Background(), "locks/shared", dirlock.LockOptions{
		StaleThreshold: time.Hour,
		RetryInterval:  time.Millisecond,
	}, func() error {
		_, err := os.Stat(filepath.Join(root, "locks", "shared", ".dagu_lock"))
		require.NoError(t, err)

		_, err = os.Stat(filepath.Join(collectionDir, "locks", "shared", ".dagu_lock"))
		require.ErrorIs(t, err, os.ErrNotExist)
		return nil
	}))
}

func TestMemoryCollectionWithLockOptionsClampsNonPositiveRetryInterval(t *testing.T) {
	t.Parallel()

	type lockOptionsCollection interface {
		WithLockOptions(ctx context.Context, key string, opts dirlock.LockOptions, fn func() error) error
	}

	col, ok := testutil.NewMemoryBackend().Collection("test").(lockOptionsCollection)
	require.True(t, ok)

	require.NotPanics(t, func() {
		err := col.WithLockOptions(context.Background(), "shared", dirlock.LockOptions{
			RetryInterval: -time.Millisecond,
		}, func() error {
			return nil
		})
		require.NoError(t, err)
	})
}

func TestMemoryCollection(t *testing.T) {
	t.Parallel()

	b := testutil.NewMemoryBackend()

	freshCollection := func(_ *testing.T) persis.Collection {
		return testutil.NewMemoryBackend().Collection("test")
	}

	RunCollectionContract(t, b.Collection("test"), freshCollection)
}
