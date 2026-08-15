// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package testutil

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/persis"
)

// CollectionFactory returns two handles to the same isolated collection.
type CollectionFactory func(t *testing.T) (persis.Collection, persis.Collection)

// RunCollectionContract verifies the behavior required from every collection.
func RunCollectionContract(t *testing.T, factory CollectionFactory) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	newRecord := func(id, data string, at time.Time) *persis.Record {
		return &persis.Record{ID: id, Data: []byte(data), CreatedAt: at, UpdatedAt: at}
	}

	t.Run("get_missing", func(t *testing.T) {
		col, _ := factory(t)
		_, err := col.Get(ctx, "no-such-id")
		assert.ErrorIs(t, err, persis.ErrNotFound)
	})

	t.Run("put_get_overwrite_and_delete", func(t *testing.T) {
		col, _ := factory(t)
		rec := newRecord("record", `{"v":1}`, now)
		require.NoError(t, col.Put(ctx, rec))

		got, err := col.Get(ctx, rec.ID)
		require.NoError(t, err)
		assert.Equal(t, rec.ID, got.ID)
		assert.Equal(t, rec.Data, got.Data)

		rec.Data = []byte(`{"v":2}`)
		require.NoError(t, col.Put(ctx, rec))
		got, err = col.Get(ctx, rec.ID)
		require.NoError(t, err)
		assert.Equal(t, rec.Data, got.Data)

		require.NoError(t, col.Delete(ctx, rec.ID))
		require.NoError(t, col.Delete(ctx, rec.ID))
		_, err = col.Get(ctx, rec.ID)
		assert.ErrorIs(t, err, persis.ErrNotFound)
	})

	t.Run("create", func(t *testing.T) {
		col, _ := factory(t)
		rec := newRecord("created", `{"v":1}`, now)
		require.NoError(t, col.Create(ctx, rec))

		duplicate := *rec
		duplicate.Data = []byte(`{"v":2}`)
		assert.ErrorIs(t, col.Create(ctx, &duplicate), persis.ErrConflict)

		got, err := col.Get(ctx, rec.ID)
		require.NoError(t, err)
		assert.Equal(t, rec.Data, got.Data)

		require.NoError(t, col.Delete(ctx, rec.ID))
		require.NoError(t, col.Create(ctx, &duplicate))
	})

	t.Run("conditional_mutations", func(t *testing.T) {
		col, _ := factory(t)
		assert.ErrorIs(t, col.CompareAndSwap(ctx, "missing", nil, []byte(`{}`)), persis.ErrNotFound)
		assert.ErrorIs(t, col.CompareAndDelete(ctx, newRecord("missing", `{}`, now)), persis.ErrNotFound)

		rec := newRecord("conditional", `{"v":1}`, now)
		require.NoError(t, col.Put(ctx, rec))
		assert.ErrorIs(t,
			col.CompareAndSwap(ctx, rec.ID, []byte(`{"v":0}`), []byte(`{"v":2}`)),
			persis.ErrConflict,
		)
		require.NoError(t, col.CompareAndSwap(ctx, rec.ID, rec.Data, []byte(`{"v":2}`)))

		stale := *rec
		assert.ErrorIs(t, col.CompareAndDelete(ctx, &stale), persis.ErrConflict)
		current, err := col.Get(ctx, rec.ID)
		require.NoError(t, err)
		require.NoError(t, col.CompareAndDelete(ctx, current))
	})

	t.Run("list", func(t *testing.T) {
		col, _ := factory(t)
		t1 := now.Add(time.Second)
		t2 := now.Add(2 * time.Second)
		t3 := now.Add(3 * time.Second)
		for _, rec := range []*persis.Record{
			newRecord("x/b", `{}`, t1),
			newRecord("x/a", `{}`, t1),
			newRecord("x/c", `{}`, t2),
			newRecord("y/a", `{}`, t3),
		} {
			require.NoError(t, col.Put(ctx, rec))
		}

		page, err := col.List(ctx, persis.ListQuery{Prefix: "x/"})
		require.NoError(t, err)
		assert.Equal(t, []string{"x/a", "x/b", "x/c"}, recordIDs(page.Records))

		since, until := t1, t3
		page, err = col.List(ctx, persis.ListQuery{Since: &since, Until: &until})
		require.NoError(t, err)
		assert.Equal(t, []string{"x/a", "x/b", "x/c"}, recordIDs(page.Records))
	})

	t.Run("pagination", func(t *testing.T) {
		col, _ := factory(t)
		for i, id := range []string{"p0", "p1", "p2", "p3", "p4"} {
			require.NoError(t, col.Put(ctx, newRecord(id, `{}`, now.Add(time.Duration(i)*time.Second))))
		}

		var ids []string
		cursor := ""
		for {
			page, err := col.List(ctx, persis.ListQuery{Cursor: cursor, Limit: 2})
			require.NoError(t, err)
			ids = append(ids, recordIDs(page.Records)...)
			if page.NextCursor == "" {
				break
			}
			cursor = page.NextCursor
		}
		assert.Equal(t, []string{"p0", "p1", "p2", "p3", "p4"}, ids)
	})

	t.Run("hierarchical_ids", func(t *testing.T) {
		col, _ := factory(t)
		rec := newRecord("dag/run-1/attempt-0", `{"status":"ok"}`, now)
		require.NoError(t, col.Put(ctx, rec))

		got, err := col.Get(ctx, rec.ID)
		require.NoError(t, err)
		assert.Equal(t, rec.ID, got.ID)

		page, err := col.List(ctx, persis.ListQuery{Prefix: "dag/run-1/"})
		require.NoError(t, err)
		assert.Equal(t, []string{rec.ID}, recordIDs(page.Records))
	})

	t.Run("concurrent_create_has_one_winner", func(t *testing.T) {
		first, second := factory(t)
		results := runTogether(
			func() error { return first.Create(ctx, newRecord("shared", `{"writer":1}`, now)) },
			func() error { return second.Create(ctx, newRecord("shared", `{"writer":2}`, now)) },
		)
		assertOneSuccessOneConflict(t, results)
	})

	t.Run("concurrent_compare_and_swap_has_one_winner", func(t *testing.T) {
		first, second := factory(t)
		require.NoError(t, first.Create(ctx, newRecord("shared", `{"v":0}`, now)))
		results := runTogether(
			func() error { return first.CompareAndSwap(ctx, "shared", []byte(`{"v":0}`), []byte(`{"v":1}`)) },
			func() error { return second.CompareAndSwap(ctx, "shared", []byte(`{"v":0}`), []byte(`{"v":2}`)) },
		)
		assertOneSuccessOneConflict(t, results)
	})

	t.Run("concurrent_swap_and_delete_have_one_winner", func(t *testing.T) {
		first, second := factory(t)
		record := newRecord("shared", `{"v":0}`, now)
		require.NoError(t, first.Create(ctx, record))
		results := runTogether(
			func() error { return first.CompareAndSwap(ctx, record.ID, record.Data, []byte(`{"v":1}`)) },
			func() error { return second.CompareAndDelete(ctx, record) },
		)
		assertOneSuccessOneConditionalFailure(t, results)
	})
}

// RunLockingCollectionContract verifies storage-wide same-key locking.
func RunLockingCollectionContract(t *testing.T, factory CollectionFactory) {
	t.Helper()

	t.Run("same_key_serializes", func(t *testing.T) {
		first, second := factory(t)
		firstLock := first.(persis.LockingCollection)
		secondLock := second.(persis.LockingCollection)
		entered, release := make(chan struct{}), make(chan struct{})
		firstDone := make(chan error, 1)
		go func() {
			firstDone <- firstLock.WithLock(t.Context(), "shared", func() error {
				close(entered)
				<-release
				return nil
			})
		}()
		<-entered

		ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
		defer cancel()
		called := false
		err := secondLock.WithLock(ctx, "shared", func() error {
			called = true
			return nil
		})
		assert.ErrorIs(t, err, context.DeadlineExceeded)
		assert.False(t, called)

		close(release)
		require.NoError(t, <-firstDone)
	})

	t.Run("callback_error_is_preserved", func(t *testing.T) {
		col, _ := factory(t)
		want := errors.New("callback failed")
		err := col.(persis.LockingCollection).WithLock(t.Context(), "key", func() error { return want })
		assert.ErrorIs(t, err, want)
	})
}

func recordIDs(records []*persis.Record) []string {
	ids := make([]string, len(records))
	for i, rec := range records {
		ids[i] = rec.ID
	}
	return ids
}

func runTogether(operations ...func() error) []error {
	start := make(chan struct{})
	results := make(chan error, len(operations))
	var wg sync.WaitGroup
	for _, operation := range operations {
		wg.Go(func() {
			<-start
			results <- operation()
		})
	}
	close(start)
	wg.Wait()
	close(results)

	errs := make([]error, 0, len(operations))
	for err := range results {
		errs = append(errs, err)
	}
	return errs
}

func assertOneSuccessOneConflict(t *testing.T, results []error) {
	t.Helper()
	var successes, conflicts int
	for _, err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, persis.ErrConflict):
			conflicts++
		default:
			require.NoError(t, err)
		}
	}
	assert.Equal(t, 1, successes)
	assert.Equal(t, 1, conflicts)
}

func assertOneSuccessOneConditionalFailure(t *testing.T, results []error) {
	t.Helper()
	var successes, failures int
	for _, err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, persis.ErrConflict), errors.Is(err, persis.ErrNotFound):
			failures++
		default:
			require.NoError(t, err)
		}
	}
	assert.Equal(t, 1, successes)
	assert.Equal(t, 1, failures)
}
