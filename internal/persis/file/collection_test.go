// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
			CreatedAt: now,
			UpdatedAt: now,
		}
		require.NoError(t, col.Put(ctx, rec))

		got, err := col.Get(ctx, "alpha")
		require.NoError(t, err)
		assert.Equal(t, rec.ID, got.ID)
		assert.Equal(t, rec.Data, got.Data)
	})

	t.Run("put_overwrites", func(t *testing.T) {
		rec := &persis.Record{
			ID:        "beta",
			Data:      []byte(`{"v":1}`),
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

	t.Run("create_inserts_new_record", func(t *testing.T) {
		col2 := freshCollection(t)
		rec := &persis.Record{
			ID:        "create-new",
			Data:      []byte(`{"n":1}`),
			CreatedAt: now,
			UpdatedAt: now,
		}
		require.NoError(t, col2.Create(ctx, rec))

		got, err := col2.Get(ctx, "create-new")
		require.NoError(t, err)
		assert.Equal(t, []byte(`{"n":1}`), got.Data)
	})

	t.Run("create_returns_conflict_when_present", func(t *testing.T) {
		col2 := freshCollection(t)
		rec := &persis.Record{
			ID:        "create-dup",
			Data:      []byte(`{"n":1}`),
			CreatedAt: now,
			UpdatedAt: now,
		}
		require.NoError(t, col2.Create(ctx, rec))

		dup := *rec
		dup.Data = []byte(`{"n":2}`)
		err := col2.Create(ctx, &dup)
		assert.ErrorIs(t, err, persis.ErrConflict)

		// Original record is unchanged.
		got, err := col2.Get(ctx, "create-dup")
		require.NoError(t, err)
		assert.Equal(t, []byte(`{"n":1}`), got.Data)
	})

	t.Run("create_after_delete_succeeds", func(t *testing.T) {
		col2 := freshCollection(t)
		rec := &persis.Record{
			ID:        "create-recycled",
			Data:      []byte(`{"n":1}`),
			CreatedAt: now,
			UpdatedAt: now,
		}
		require.NoError(t, col2.Create(ctx, rec))
		require.NoError(t, col2.Delete(ctx, "create-recycled"))

		fresh := *rec
		fresh.Data = []byte(`{"n":2}`)
		require.NoError(t, col2.Create(ctx, &fresh))

		got, err := col2.Get(ctx, "create-recycled")
		require.NoError(t, err)
		assert.Equal(t, []byte(`{"n":2}`), got.Data)
	})

	t.Run("delete", func(t *testing.T) {
		rec := &persis.Record{
			ID:        "gamma",
			Data:      []byte(`{}`),
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
			{ID: "x/a", Data: []byte(`{}`), CreatedAt: t2, UpdatedAt: t2},
			{ID: "x/b", Data: []byte(`{}`), CreatedAt: t1, UpdatedAt: t1},
			{ID: "y/c", Data: []byte(`{}`), CreatedAt: t3, UpdatedAt: t3},
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
			{ID: "dag1/run1", Data: []byte(`{}`), CreatedAt: t1, UpdatedAt: t1},
			{ID: "dag1/run2", Data: []byte(`{}`), CreatedAt: t2, UpdatedAt: t2},
			{ID: "dag2/run1", Data: []byte(`{}`), CreatedAt: t1, UpdatedAt: t1},
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
			{ID: "r1", Data: []byte(`{}`), CreatedAt: t1, UpdatedAt: t1},
			{ID: "r2", Data: []byte(`{}`), CreatedAt: t2, UpdatedAt: t2},
			{ID: "r3", Data: []byte(`{}`), CreatedAt: t3, UpdatedAt: t3},
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
			CreatedAt: now,
			UpdatedAt: now,
		}
		require.NoError(t, col2.Put(ctx, rec))
		err := col2.CompareAndSwap(ctx, "cas-conflict", []byte(`{"v":99}`), []byte(`{"v":2}`))
		assert.ErrorIs(t, err, persis.ErrConflict)
	})

	t.Run("hierarchical_ids", func(t *testing.T) {
		col2 := freshCollection(t)
		t1 := now.Add(time.Millisecond)
		rec := &persis.Record{
			ID:        "dag/run-1/attempt-0",
			Data:      []byte(`{"status":"ok"}`),
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

func TestFileCollectionRecordNamespaceVersionTracksPrefixDirectory(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	col := file.NewCollection(t.TempDir())
	versionCol := col.(interface {
		RecordNamespaceVersion(context.Context, string) (string, error)
	})

	missingVersion, err := versionCol.RecordNamespaceVersion(ctx, "pending/")
	require.NoError(t, err)
	assert.Equal(t, "missing:pending/", missingVersion)

	rec := &persis.Record{
		ID:        "pending/task",
		Data:      []byte(`{"v":1}`),
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	require.NoError(t, col.Put(ctx, rec))
	existingVersion, err := versionCol.RecordNamespaceVersion(ctx, "pending/")
	require.NoError(t, err)
	assert.NotEqual(t, missingVersion, existingVersion)

	require.NoError(t, col.Delete(ctx, rec.ID))
	deletedVersion, err := versionCol.RecordNamespaceVersion(ctx, "pending/")
	require.NoError(t, err)
	assert.Equal(t, missingVersion, deletedVersion)

	_, err = versionCol.RecordNamespaceVersion(ctx, "../pending/")
	assert.Error(t, err)
}

func TestFileCollectionWritesRawJSONBody(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	col := file.NewCollection(root)
	raw := []byte(`{"id":"user-1","name":"admin"}`)
	rec := &persis.Record{
		ID:        "users/user-1",
		Data:      raw,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}

	require.NoError(t, col.Put(ctx, rec))

	path := filepath.Join(root, "users", "user-1.json")
	gotRaw, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, raw, gotRaw)

	var body map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(gotRaw, &body))
	assert.NotContains(t, body, "encoding")
	assert.NotContains(t, body, "data")

	got, err := col.Get(ctx, "users/user-1")
	require.NoError(t, err)
	assert.Equal(t, raw, got.Data)
}

// TestFileCollectionIndentedMatchesReleasedFormat pins the on-disk bytes of an
// indented collection to json.MarshalIndent(v, "", "  ") — the exact format the
// pre-refactor (<= v2.7.4) file stores wrote — so upgrades need no migration.
func TestFileCollectionIndentedMatchesReleasedFormat(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	col := file.NewCollection(root, file.WithIndentedJSON())

	type sample struct {
		ID   string   `json:"id"`
		Name string   `json:"name"`
		Tags []string `json:"tags"`
	}
	v := sample{ID: "u1", Name: "admin", Tags: []string{"a", "b"}}

	compact, err := json.Marshal(v)
	require.NoError(t, err)
	rec := &persis.Record{
		ID:        "users/u1",
		Data:      compact,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	require.NoError(t, col.Put(ctx, rec))

	// On-disk bytes must equal the released json.MarshalIndent output.
	onDisk, err := os.ReadFile(filepath.Join(root, "users", "u1.json"))
	require.NoError(t, err)
	wantIndented, err := json.MarshalIndent(v, "", "  ")
	require.NoError(t, err)
	assert.Equal(t, wantIndented, onDisk)

	// Get normalizes back to canonical compact Data.
	got, err := col.Get(ctx, "users/u1")
	require.NoError(t, err)
	assert.Equal(t, compact, got.Data)
}

// TestFileCollectionIndentedReadsLegacyIndentedFile verifies a file written by
// an older release (indented, no envelope) is read back as canonical compact
// Data without any migration step.
func TestFileCollectionIndentedReadsLegacyIndentedFile(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	col := file.NewCollection(root, file.WithIndentedJSON())

	compact := []byte(`{"id":"k1","name":"ci"}`)
	legacy, err := json.MarshalIndent(json.RawMessage(compact), "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "api_keys"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(root, "api_keys", "k1.json"), legacy, 0o600))

	got, err := col.Get(ctx, "api_keys/k1")
	require.NoError(t, err)
	assert.Equal(t, compact, got.Data)
}

// TestFileCollectionIndentedContract runs the full Collection contract against
// an indented collection, proving CompareAndSwap, CompareAndDelete, List, and
// Claim all stay correct when records are indented on disk.
func TestFileCollectionIndentedContract(t *testing.T) {
	t.Parallel()

	freshCollection := func(t *testing.T) persis.Collection {
		return file.NewCollection(t.TempDir(), file.WithIndentedJSON())
	}
	RunCollectionContract(t, file.NewCollection(t.TempDir(), file.WithIndentedJSON()), freshCollection)
}

func TestFileCollectionPutNilReturnsError(t *testing.T) {
	t.Parallel()

	col := file.NewCollection(t.TempDir())
	err := col.Put(context.Background(), nil)
	require.ErrorContains(t, err, "nil record")
}

// TestFileCollectionCreateIsAtomicAcrossGoroutines races 16 goroutines on the
// same ID and asserts that exactly one Create succeeds and the rest see
// ErrConflict. Exercises the O_EXCL guarantee against in-process concurrency.
func TestFileCollectionCreateIsAtomicAcrossGoroutines(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	col := file.NewCollection(t.TempDir())

	const goroutines = 16
	var (
		wg        sync.WaitGroup
		successes int64
		conflicts int64
		other     int64
	)
	start := make(chan struct{})
	for range goroutines {
		wg.Go(func() {
			<-start
			err := col.Create(ctx, &persis.Record{
				ID:        "shared",
				Data:      []byte(`{}`),
				CreatedAt: time.Now().UTC(),
				UpdatedAt: time.Now().UTC(),
			})
			switch err {
			case nil:
				atomic.AddInt64(&successes, 1)
			case persis.ErrConflict:
				atomic.AddInt64(&conflicts, 1)
			default:
				atomic.AddInt64(&other, 1)
			}
		})
	}
	close(start)
	wg.Wait()

	assert.Equal(t, int64(1), successes, "exactly one Create must win")
	assert.Equal(t, int64(goroutines-1), conflicts, "all losers must see ErrConflict")
	assert.Equal(t, int64(0), other, "no other error class is acceptable")
}

func TestMemoryCollection(t *testing.T) {
	t.Parallel()

	b := testutil.NewMemoryBackend()

	freshCollection := func(_ *testing.T) persis.Collection {
		return testutil.NewMemoryBackend().Collection("test")
	}

	RunCollectionContract(t, b.Collection("test"), freshCollection)
}
