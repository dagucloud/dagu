// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/cmn/dirlock"
	"github.com/dagucloud/dagu/v2/internal/persis"
)

// loseRecordLock hands the lock of record id to another owner, as stale-lock
// recovery in another process would: the lock directory is replaced by one
// that the other owner holds.
func loseRecordLock(t *testing.T, c *Collection, id string) {
	t.Helper()

	sum := sha256.Sum256([]byte(id))
	bucket := filepath.Join(c.dir, recordLockDirectoryName, fmt.Sprintf("%02x", sum[0]))
	require.NoError(t, os.RemoveAll(filepath.Join(bucket, dirlock.LockDirectoryName)))

	other := dirlock.New(bucket, nil)
	require.NoError(t, other.TryLock())
	t.Cleanup(func() { _ = other.Unlock() })
}

func TestCollection_LostLock(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Now().UTC()

	setup := func(t *testing.T) (*Collection, *persis.Record) {
		t.Helper()
		c := NewCollection(t.TempDir())
		rec := &persis.Record{ID: "item", Data: []byte(`{"v":1}`), CreatedAt: now, UpdatedAt: now}
		require.NoError(t, c.Put(ctx, rec))
		c.beforeHeldCheck = func() { loseRecordLock(t, c, rec.ID) }
		return c, rec
	}

	t.Run("CompareAndSwap", func(t *testing.T) {
		t.Parallel()
		c, rec := setup(t)

		err := c.CompareAndSwap(ctx, rec.ID, rec.Data, []byte(`{"v":2}`))
		require.ErrorIs(t, err, dirlock.ErrLockNotHeld)
		// Callers treat ErrConflict as "the record changed, drop it".
		require.NotErrorIs(t, err, persis.ErrConflict)

		got, err := c.Get(ctx, rec.ID)
		require.NoError(t, err)
		require.JSONEq(t, `{"v":1}`, string(got.Data))
	})

	t.Run("CompareAndDelete", func(t *testing.T) {
		t.Parallel()
		c, rec := setup(t)

		err := c.CompareAndDelete(ctx, rec)
		require.ErrorIs(t, err, dirlock.ErrLockNotHeld)
		// Callers treat ErrConflict as "the record changed, drop it".
		require.NotErrorIs(t, err, persis.ErrConflict)

		_, err = c.Get(ctx, rec.ID)
		require.NoError(t, err)
	})
}

func TestCollection_HeldLock(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Now().UTC()
	c := NewCollection(t.TempDir())
	rec := &persis.Record{ID: "item", Data: []byte(`{"v":1}`), CreatedAt: now, UpdatedAt: now}
	require.NoError(t, c.Put(ctx, rec))

	var checks int
	c.beforeHeldCheck = func() { checks++ }

	require.NoError(t, c.CompareAndSwap(ctx, rec.ID, rec.Data, []byte(`{"v":2}`)))
	require.Equal(t, 1, checks)

	got, err := c.Get(ctx, rec.ID)
	require.NoError(t, err)
	require.JSONEq(t, `{"v":2}`, string(got.Data))
}

// A holder that runs past the stale threshold keeps renewing the lock, so a
// contender waiting that long does not take it over.
func TestDirLockOn_RenewsWhileHeld(t *testing.T) {
	t.Parallel()

	const staleThreshold = time.Second
	dir := t.TempDir()
	holder := dirLockOn{dir: dir, staleThreshold: staleThreshold}
	contender := dirLockOn{dir: dir, staleThreshold: staleThreshold}

	err := holder.Do(t.Context(), func(held func() error) error {
		ctx, cancel := context.WithTimeout(t.Context(), 3*staleThreshold)
		defer cancel()
		err := contender.Do(ctx, func(func() error) error {
			t.Error("the contender took over a lock that its holder renews")
			return nil
		})
		require.ErrorIs(t, err, context.DeadlineExceeded)
		return held()
	})
	require.NoError(t, err)
}
