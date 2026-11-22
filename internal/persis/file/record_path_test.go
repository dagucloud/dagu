// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file_test

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
)

// IDs that Windows would store under another name, open as a device, or
// refuse, if they named files as written.
var windowsUnsafeIDs = []string{
	"nul", "CON", "com1", "aux.txt", "COM¹", "conin$", "Lpt9.log", "con .x",
	"a:b", `what?`, `q<>|"*`, "50%off", "%6Eul",
	"trail./child", "trail /child", "nul/child",
}

func TestFileCollectionWindowsUnsafeIDs(t *testing.T) {
	t.Parallel()

	for _, id := range windowsUnsafeIDs {
		t.Run(id, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			dir := filepath.Join(t.TempDir(), "collection")
			col := file.NewCollection(dir)

			require.NoError(t, col.Put(ctx, &persis.Record{ID: id, Data: []byte(`{"v":1}`)}))
			got, err := col.Get(ctx, id)
			require.NoError(t, err)
			assert.Equal(t, id, got.ID)
			assert.JSONEq(t, `{"v":1}`, string(got.Data))
			assert.ErrorIs(t, col.Create(ctx, &persis.Record{ID: id, Data: []byte(`{}`)}), persis.ErrConflict)

			require.NoError(t, col.CompareAndSwap(ctx, id, got.Data, []byte(`{"v":2}`)))
			ids, err := col.RecordIDs(ctx, "")
			require.NoError(t, err)
			assert.Equal(t, []string{id}, ids)
			page, err := col.List(ctx, persis.ListQuery{Prefix: id})
			require.NoError(t, err)
			require.Len(t, page.Records, 1)
			assert.Equal(t, id, page.Records[0].ID)
			assert.JSONEq(t, `{"v":2}`, string(page.Records[0].Data))
			assertPortableNames(t, dir)

			require.NoError(t, col.Delete(ctx, id))
			_, err = col.Get(ctx, id)
			assert.ErrorIs(t, err, persis.ErrNotFound)
		})
	}
}

func TestFileCollectionCompareAndDeleteWindowsUnsafeID(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	col := file.NewCollection(filepath.Join(t.TempDir(), "collection"))
	require.NoError(t, col.Create(ctx, &persis.Record{ID: "nul/child", Data: []byte(`{}`)}))
	rec, err := col.Get(ctx, "nul/child")
	require.NoError(t, err)

	require.NoError(t, col.CompareAndDelete(ctx, rec))

	_, err = col.Get(ctx, "nul/child")
	assert.ErrorIs(t, err, persis.ErrNotFound)
}

// Before IDs were escaped, records were written under their raw IDs, which
// Linux and macOS store as written. Such a record is still found, and its
// next write moves it to the escaped path.
func TestFileCollectionReadsRecordsUnderRawNames(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("Windows never held these records under their raw names")
	}
	ctx := t.Context()
	dir := filepath.Join(t.TempDir(), "collection")
	raw := map[string]string{
		"nul":          "nul.json",
		"a:b":          "a:b.json",
		"trail /x":     filepath.Join("trail ", "x.json"),
		"trail./child": filepath.Join("trail.", "child.json"),
	}
	for _, rel := range raw {
		path := filepath.Join(dir, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, os.WriteFile(path, []byte(`{"v":1}`), 0o600))
	}
	col := file.NewCollection(dir)

	ids, err := col.RecordIDs(ctx, "")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"nul", "a:b", "trail /x", "trail./child"}, ids)
	page, err := col.List(ctx, persis.ListQuery{Prefix: "trail./"})
	require.NoError(t, err)
	require.Len(t, page.Records, 1)
	assert.Equal(t, "trail./child", page.Records[0].ID)
	for id := range raw {
		got, err := col.Get(ctx, id)
		require.NoError(t, err, id)
		assert.Equal(t, id, got.ID)
		assert.ErrorIs(t, col.Create(ctx, &persis.Record{ID: id, Data: []byte(`{}`)}), persis.ErrConflict, id)
	}

	require.NoError(t, col.CompareAndSwap(ctx, "nul", []byte(`{"v":1}`), []byte(`{"v":2}`)))
	require.NoError(t, col.Put(ctx, &persis.Record{ID: "a:b", Data: []byte(`{"v":2}`)}))
	require.NoError(t, col.Delete(ctx, "trail /x"))
	rec, err := col.Get(ctx, "trail./child")
	require.NoError(t, err)
	require.NoError(t, col.CompareAndDelete(ctx, rec))

	page, err = col.List(ctx, persis.ListQuery{})
	require.NoError(t, err)
	require.Len(t, page.Records, 2)
	for _, rec := range page.Records {
		assert.JSONEq(t, `{"v":2}`, string(rec.Data), rec.ID)
	}
	assertPortableNames(t, dir)
}

// A record written before IDs were escaped whose ID holds "%" is found under
// its raw name on every OS, Windows included, which can hold that name.
func TestFileCollectionReadsRawNamesWithPercent(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dir := filepath.Join(t.TempDir(), "collection")
	for _, rel := range []string{"50%off.json", "50%41.json", filepath.Join("q%", "item.json")} {
		path := filepath.Join(dir, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, os.WriteFile(path, []byte(`{"v":1}`), 0o600))
	}
	col := file.NewCollection(dir)

	ids, err := col.RecordIDs(ctx, "")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"50%off", "50%41", "q%/item"}, ids)
	page, err := col.List(ctx, persis.ListQuery{Prefix: "q%/"})
	require.NoError(t, err)
	require.Len(t, page.Records, 1)
	for _, id := range ids {
		got, err := col.Get(ctx, id)
		require.NoError(t, err, id)
		assert.Equal(t, id, got.ID)
		require.NoError(t, col.CompareAndSwap(ctx, id, got.Data, []byte(`{"v":2}`)), id)
	}

	page, err = col.List(ctx, persis.ListQuery{})
	require.NoError(t, err)
	require.Len(t, page.Records, 3)
	for _, rec := range page.Records {
		assert.JSONEq(t, `{"v":2}`, string(rec.Data), rec.ID)
	}
}

// An ID that spells another ID's escaped name, such as "%6Eul" for "nul",
// keeps a record of its own.
func TestFileCollectionKeepsEscapedLookalikesApart(t *testing.T) {
	t.Parallel()

	for _, pair := range [][2]string{{"nul", "%6Eul"}, {"nul/child", "%6Eul/child"}, {"a:b", "a%3Ab"}, {"a:b", "a%3ab"}, {"nul", "%6eul"}} {
		t.Run(pair[1], func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()
			col := file.NewCollection(filepath.Join(t.TempDir(), "collection"))
			for i, id := range pair {
				require.NoError(t, col.Put(ctx, &persis.Record{ID: id, Data: []byte(fmt.Sprintf(`{"v":%d}`, i))}))
			}

			for i, id := range pair {
				got, err := col.Get(ctx, id)
				require.NoError(t, err)
				assert.JSONEq(t, fmt.Sprintf(`{"v":%d}`, i), string(got.Data), id)
				page, err := col.List(ctx, persis.ListQuery{Prefix: id})
				require.NoError(t, err)
				require.Len(t, page.Records, 1, id)
				assert.Equal(t, id, page.Records[0].ID)
			}
			require.NoError(t, col.Delete(ctx, pair[1]))
			ids, err := col.RecordIDs(ctx, "")
			require.NoError(t, err)
			assert.Equal(t, []string{pair[0]}, ids)
		})
	}
}

// On a file system that ignores case, a%3ab.json is the file of a:b, whose
// escaped name is a%3Ab.json. The record a%3ab never reads, replaces, or
// removes it.
func TestFileCollectionKeepsCaseVariantsApart(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dir := filepath.Join(t.TempDir(), "collection")
	other := filepath.Join(dir, "a%3ab.json")
	require.NoError(t, os.MkdirAll(dir, 0o750))
	require.NoError(t, os.WriteFile(other, []byte(`{"v":0}`), 0o600))
	col := file.NewCollection(dir)

	_, err := col.Get(ctx, "a%3ab")
	assert.ErrorIs(t, err, persis.ErrNotFound)
	require.NoError(t, col.Create(ctx, &persis.Record{ID: "a%3ab", Data: []byte(`{"v":1}`)}))
	require.NoError(t, col.Put(ctx, &persis.Record{ID: "a%3ab", Data: []byte(`{"v":2}`)}))
	got, err := col.Get(ctx, "a%3ab")
	require.NoError(t, err)
	assert.JSONEq(t, `{"v":2}`, string(got.Data))
	require.NoError(t, col.Delete(ctx, "a%3ab"))

	raw, err := os.ReadFile(other)
	require.NoError(t, err)
	assert.JSONEq(t, `{"v":0}`, string(raw))
}

// A rewrite that stops between writing the escaped file and removing the
// raw one leaves both. The escaped one is current.
func TestFileCollectionPrefersEscapedOverRawName(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("Windows never held these records under their raw names")
	}
	ctx := t.Context()
	dir := filepath.Join(t.TempDir(), "collection")
	col := file.NewCollection(dir)
	require.NoError(t, col.Put(ctx, &persis.Record{ID: "nul", Data: []byte(`{"v":2}`)}))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "nul.json"), []byte(`{"v":1}`), 0o600))

	got, err := col.Get(ctx, "nul")
	require.NoError(t, err)
	assert.JSONEq(t, `{"v":2}`, string(got.Data))
	ids, err := col.RecordIDs(ctx, "")
	require.NoError(t, err)
	assert.Equal(t, []string{"nul"}, ids)
	page, err := col.List(ctx, persis.ListQuery{})
	require.NoError(t, err)
	require.Len(t, page.Records, 1)
	assert.JSONEq(t, `{"v":2}`, string(page.Records[0].Data))

	require.NoError(t, col.Delete(ctx, "nul"))
	_, err = col.Get(ctx, "nul")
	assert.ErrorIs(t, err, persis.ErrNotFound)
}

// The raw copy of a record is never listed in place of its escaped copy,
// even when the escaped copy falls outside a listing's time bounds.
func TestFileCollectionTimeBoundsKeepRawCopyHidden(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("Windows never held these records under their raw names")
	}
	ctx := t.Context()
	dir := filepath.Join(t.TempDir(), "collection")
	col := file.NewCollection(dir)
	old := time.Now().Add(-48 * time.Hour)
	require.NoError(t, col.Put(ctx, &persis.Record{ID: "nul", Data: []byte(`{"v":2}`), CreatedAt: old, UpdatedAt: old}))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "nul.json"), []byte(`{"v":1}`), 0o600))

	since := time.Now().Add(-time.Hour)
	page, err := col.List(ctx, persis.ListQuery{Since: &since})
	require.NoError(t, err)
	assert.Empty(t, page.Records)
}

// A raw path that cannot be inspected is reported, not taken for a missing
// record.
func TestFileCollectionReportsUnreadableRawPath(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a directory this user cannot search")
	}
	ctx := t.Context()
	dir := filepath.Join(t.TempDir(), "collection")
	locked := filepath.Join(dir, "trail.")
	require.NoError(t, os.MkdirAll(locked, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(locked, "child.json"), []byte(`{}`), 0o600))
	require.NoError(t, os.Chmod(locked, 0))
	t.Cleanup(func() { _ = os.Chmod(locked, 0o750) })
	col := file.NewCollection(dir)

	_, err := col.Get(ctx, "trail./child")
	require.Error(t, err)
	assert.NotErrorIs(t, err, persis.ErrNotFound)
}

func TestRecordIDFromPath(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	dir := filepath.Join(t.TempDir(), "collection")
	col := file.NewCollection(dir)
	require.NoError(t, col.Put(ctx, &persis.Record{ID: "con/item_1", Data: []byte(`{}`)}))

	var rels []string
	require.NoError(t, filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(path, ".json") {
			rel, _ := filepath.Rel(dir, path)
			rels = append(rels, rel)
		}
		return err
	}))

	require.Len(t, rels, 1)
	assert.Equal(t, "con/item_1", file.RecordIDFromPath(rels[0]))
}

// assertPortableNames fails when a file or directory under dir has a name
// that Windows reserves, refuses, or rewrites, so the data directory works
// when it is moved there.
func assertPortableNames(t *testing.T, dir string) {
	t.Helper()
	devices := map[string]bool{"CON": true, "PRN": true, "AUX": true, "NUL": true, "CONIN$": true, "CONOUT$": true}
	for _, prefix := range []string{"COM", "LPT"} {
		for _, n := range []string{"0", "1", "2", "3", "4", "5", "6", "7", "8", "9", "¹", "²", "³"} {
			devices[prefix+n] = true
		}
	}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || path == dir {
			return err
		}
		name := d.Name()
		base, _, _ := strings.Cut(name, ".")
		assert.False(t, devices[strings.ToUpper(strings.TrimRight(base, " "))], "device name %q", name)
		assert.False(t, strings.ContainsAny(name, `<>:"\|?*`), "forbidden character in %q", name)
		assert.False(t, strings.HasSuffix(name, ".") || strings.HasSuffix(name, " "), "trailing dot or space in %q", name)
		return nil
	})
	require.NoError(t, err)
}
