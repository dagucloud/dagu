// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/dirlock"
	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/persis"
)

// Collection implements [persis.Collection] as a directory of JSON files.
// "/" in record IDs maps to the OS path separator, so hierarchical IDs
// become nested subdirectories on disk.
type Collection struct {
	dir string
	// indent, when true, stores records as 2-space indented JSON on disk to
	// match the pre-refactor (<= v2.7.4) released file format. Records are
	// normalized back to compact JSON in memory on read, so Record.Data stays
	// canonical regardless of on-disk whitespace.
	indent bool
	mu     sync.RWMutex
}

var _ persis.Collection = (*Collection)(nil)

func sameRecord(a, b *persis.Record) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.ID == b.ID && bytes.Equal(a.Data, b.Data)
}

// ─── Collection methods ───────────────────────────────────────────────────────

func (c *Collection) Get(_ context.Context, id string) (*persis.Record, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	path, err := c.filePath(id)
	if err != nil {
		return nil, err
	}
	rec, err := c.readFile(path)
	if err != nil {
		return nil, err
	}
	return rec, nil
}

func (c *Collection) Put(_ context.Context, rec *persis.Record) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if rec == nil {
		return fmt.Errorf("file backend: nil record")
	}
	path, err := c.filePath(rec.ID)
	if err != nil {
		return err
	}
	return c.writeFile(path, rec)
}

// Create atomically inserts rec. Returns [persis.ErrConflict] when a
// record with rec.ID already exists. Uses O_EXCL|O_CREATE for atomic
// cross-process insert.
func (c *Collection) Create(_ context.Context, rec *persis.Record) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if rec == nil {
		return fmt.Errorf("file backend: nil record")
	}
	if !json.Valid(rec.Data) {
		return fmt.Errorf("file backend: invalid JSON record %q", rec.ID)
	}
	path, err := c.filePath(rec.ID)
	if err != nil {
		return err
	}
	body := rec.Data
	if c.indent {
		var buf bytes.Buffer
		if err := json.Indent(&buf, rec.Data, "", "  "); err != nil {
			return fmt.Errorf("file backend: indent record %q: %w", rec.ID, err)
		}
		body = buf.Bytes()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // path is sanitized via c.filePath -> pathUnderRoot
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return persis.ErrConflict
		}
		return err
	}
	if _, err := f.Write(body); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return err
	}
	mtime := rec.UpdatedAt
	if mtime.IsZero() {
		mtime = rec.CreatedAt
	}
	if mtime.IsZero() {
		return nil
	}
	return os.Chtimes(path, mtime, mtime)
}

func (c *Collection) Delete(ctx context.Context, id string) error {
	_, err := c.DeleteIfExists(ctx, id)
	return err
}

// CompareAndDelete removes expected.ID only when the current record still
// matches expected.
func (c *Collection) CompareAndDelete(_ context.Context, expected *persis.Record) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	path, err := c.filePath(expected.ID)
	if err != nil {
		return err
	}
	rec, err := c.readFile(path)
	if err != nil {
		return err
	}
	if !sameRecord(rec, expected) {
		return persis.ErrConflict
	}
	if err := fileutil.Remove(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return persis.ErrNotFound
		}
		return err
	}
	removeEmptyDirs(filepath.Dir(path), c.dir)
	return nil
}

// RecordIDs returns record IDs matching prefix without decoding record payloads.
func (c *Collection) RecordIDs(_ context.Context, prefix string) ([]string, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	ids, err := c.collectIDs(prefix)
	if err != nil {
		return nil, err
	}
	sort.Strings(ids)
	return ids, nil
}

// RecordVersion returns a cheap version token for cache validation.
func (c *Collection) RecordVersion(_ context.Context, id string) (string, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	path, err := c.filePath(id)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", persis.ErrNotFound
		}
		return "", err
	}
	return fmt.Sprintf("%d/%d", info.ModTime().UTC().UnixNano(), info.Size()), nil
}

// WithLock runs fn while holding a cross-process lock scoped to key.
func (c *Collection) WithLock(ctx context.Context, key string, fn func() error) error {
	lockDir, err := c.lockDir(key)
	if err != nil {
		return err
	}
	lock := dirlock.New(lockDir, &dirlock.LockOptions{
		StaleThreshold: 30 * time.Second,
		RetryInterval:  50 * time.Millisecond,
	})
	if err := lock.Lock(ctx); err != nil {
		return err
	}
	defer func() {
		_ = lock.Unlock()
	}()
	return fn()
}

// DeleteIfExists removes the record with the given id and reports whether it existed.
func (c *Collection) DeleteIfExists(_ context.Context, id string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	path, err := c.filePath(id)
	if err != nil {
		return false, err
	}
	if err := fileutil.Remove(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	removeEmptyDirs(filepath.Dir(path), c.dir)
	return true, nil
}

func (c *Collection) List(_ context.Context, q persis.ListQuery) (*persis.Page, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	recs, err := c.collect(q.Prefix, q.Since, q.Until)
	if err != nil {
		return nil, err
	}

	sort.Slice(recs, func(i, j int) bool {
		ti, tj := recs[i].CreatedAt, recs[j].CreatedAt
		if ti.Equal(tj) {
			return recs[i].ID < recs[j].ID
		}
		return ti.Before(tj)
	})

	recs = applycursor(recs, q.Cursor)

	limit := q.Limit
	if limit <= 0 {
		limit = len(recs)
	}

	var nextCursor string
	if len(recs) > limit {
		nextCursor = encodeCursor(recs[limit-1].CreatedAt, recs[limit-1].ID)
		recs = recs[:limit]
	}

	return &persis.Page{Records: recs, NextCursor: nextCursor}, nil
}

// CompareAndSwap atomically replaces the record's Data only when the current
// Data equals expected. Returns [persis.ErrConflict] on mismatch.
func (c *Collection) CompareAndSwap(_ context.Context, id string, expected, next []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	path, err := c.filePath(id)
	if err != nil {
		return err
	}
	rec, err := c.readFile(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(rec.Data, expected) {
		return persis.ErrConflict
	}
	rec.Data = next
	rec.UpdatedAt = time.Now().UTC()
	return c.writeFile(path, rec)
}

// ─── internal helpers ─────────────────────────────────────────────────────────

func (c *Collection) filePath(id string) (string, error) {
	return pathUnderRoot(c.dir, id, "record ID")
}

func (c *Collection) lockDir(key string) (string, error) {
	if key == "" {
		return c.dir, nil
	}
	path, err := pathUnderRoot(c.dir, strings.TrimSuffix(key, "/")+"/_lock", "lock key")
	if err != nil {
		return "", err
	}
	return filepath.Dir(path), nil
}

func pathUnderRoot(root, id, kind string) (string, error) {
	base := filepath.Clean(root)
	full := filepath.Clean(filepath.Join(base, idToRelPath(id)))
	rel, err := filepath.Rel(base, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("file backend: %s %q escapes collection root", kind, id)
	}
	return full, nil
}

func (c *Collection) readFile(path string) (*persis.Record, error) {
	raw, err := fileutil.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, persis.ErrNotFound
		}
		return nil, err
	}
	if !json.Valid(raw) {
		return nil, fmt.Errorf("file backend: corrupt record at %q: invalid JSON", path)
	}

	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, persis.ErrNotFound
		}
		return nil, err
	}
	rel, _ := filepath.Rel(c.dir, path)
	mtime := info.ModTime().UTC()
	data := raw
	if c.indent {
		// Normalize indented on-disk JSON back to compact so the in-memory
		// Record.Data is canonical (matches the memory backend and keeps
		// CompareAndSwap/CompareAndDelete byte comparisons stable). The
		// json.Valid check above makes json.Compact infallible here, so we
		// drop its error rather than fall back to non-canonical raw bytes.
		var buf bytes.Buffer
		_ = json.Compact(&buf, raw)
		data = buf.Bytes()
	}
	return &persis.Record{
		ID:        relPathToID(rel),
		Data:      data,
		CreatedAt: mtime,
		UpdatedAt: mtime,
	}, nil
}

func (c *Collection) writeFile(path string, rec *persis.Record) error {
	if rec == nil {
		return fmt.Errorf("file backend: nil record")
	}
	if !json.Valid(rec.Data) {
		return fmt.Errorf("file backend: invalid JSON record %q", rec.ID)
	}
	body := rec.Data
	if c.indent {
		// Match the pre-refactor on-disk format. json.Indent over compact
		// json.Marshal output is byte-identical to json.MarshalIndent of the
		// same value, so existing released files stay format-compatible.
		var buf bytes.Buffer
		if err := json.Indent(&buf, rec.Data, "", "  "); err != nil {
			return fmt.Errorf("file backend: indent record %q: %w", rec.ID, err)
		}
		body = buf.Bytes()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	if err := fileutil.WriteFileAtomic(path, body, 0o600); err != nil {
		return err
	}
	mtime := rec.UpdatedAt
	if mtime.IsZero() {
		mtime = rec.CreatedAt
	}
	if mtime.IsZero() {
		return nil
	}
	return os.Chtimes(path, mtime, mtime)
}

func (c *Collection) collectIDs(prefix string) ([]string, error) {
	walkRoot := c.prefixWalkRoot(prefix)

	var ids []string
	err := filepath.WalkDir(walkRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".json") {
			return err
		}
		rel, _ := filepath.Rel(c.dir, path)
		id := relPathToID(rel)
		if prefix != "" && !strings.HasPrefix(id, prefix) {
			return nil
		}
		ids = append(ids, id)
		return nil
	})
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return ids, nil
}

// collect walks the collection directory and returns records matching the
// given prefix and time bounds. Corrupt or missing files are silently skipped.
func (c *Collection) collect(prefix string, since, until *time.Time) ([]*persis.Record, error) {
	walkRoot := c.prefixWalkRoot(prefix)

	var recs []*persis.Record
	err := filepath.WalkDir(walkRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".json") {
			return err
		}
		rel, _ := filepath.Rel(c.dir, path)
		id := relPathToID(rel)
		if prefix != "" && !strings.HasPrefix(id, prefix) {
			return nil
		}
		r, err := c.readFile(path)
		if err != nil {
			return nil // skip corrupt records
		}
		if since != nil && r.CreatedAt.Before(*since) {
			return nil
		}
		if until != nil && !r.CreatedAt.Before(*until) {
			return nil
		}
		recs = append(recs, r)
		return nil
	})
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return recs, nil
}

// prefixWalkRoot returns the deepest existing directory that is a valid prefix
// of all IDs matching the given prefix — avoiding a full collection scan.
func (c *Collection) prefixWalkRoot(prefix string) string {
	if prefix == "" {
		return c.dir
	}
	// Use everything up to the last "/" as the subdirectory to walk.
	lastSlash := strings.LastIndex(prefix, "/")
	if lastSlash <= 0 {
		return c.dir
	}
	sub := filepath.Join(c.dir, filepath.Join(strings.Split(prefix[:lastSlash], "/")...))
	if _, err := os.Stat(sub); err == nil {
		return sub
	}
	return c.dir
}

// ─── path helpers ─────────────────────────────────────────────────────────────

// idToRelPath converts "a/b/c" → "a/b/c.json" using the OS path separator.
func idToRelPath(id string) string {
	return filepath.Join(strings.Split(id, "/")...) + ".json"
}

// relPathToID is the inverse of idToRelPath.
func relPathToID(rel string) string {
	return filepath.ToSlash(strings.TrimSuffix(rel, ".json"))
}

// ─── cursor helpers ───────────────────────────────────────────────────────────

type cursorVal struct {
	C time.Time `json:"c"`
	I string    `json:"i"`
}

func encodeCursor(createdAt time.Time, id string) string {
	b, _ := json.Marshal(cursorVal{C: createdAt, I: id})
	return base64.RawStdEncoding.EncodeToString(b)
}

func decodeCursor(s string) (cursorVal, bool) {
	b, err := base64.RawStdEncoding.DecodeString(s)
	if err != nil {
		return cursorVal{}, false
	}
	var v cursorVal
	if err := json.Unmarshal(b, &v); err != nil {
		return cursorVal{}, false
	}
	return v, true
}

func applycursor(recs []*persis.Record, cursor string) []*persis.Record {
	if cursor == "" {
		return recs
	}
	cv, ok := decodeCursor(cursor)
	if !ok {
		return recs
	}
	for i, r := range recs {
		after := r.CreatedAt.After(cv.C)
		sameTimeAfterID := r.CreatedAt.Equal(cv.C) && r.ID > cv.I
		if after || sameTimeAfterID {
			return recs[i:]
		}
	}
	return nil
}

// removeEmptyDirs removes dir and its ancestors up to (but not including)
// stopAt if they are empty.
func removeEmptyDirs(dir, stopAt string) {
	for dir != stopAt && strings.HasPrefix(dir, stopAt) {
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) > 0 {
			return
		}
		if err := fileutil.Remove(dir); err != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}
