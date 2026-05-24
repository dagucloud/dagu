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
	"github.com/dagucloud/dagu/internal/persis"
)

// Collection implements [persis.Collection] as a directory of JSON files.
// "/" in record IDs maps to the OS path separator, so hierarchical IDs
// become nested subdirectories on disk.
type Collection struct {
	dir string
	mu  sync.RWMutex
}

var _ persis.Collection = (*Collection)(nil)

// fileRecord is the on-disk JSON envelope for a [persis.Record].
// Data is kept as json.RawMessage so the file is human-readable when
// the encoding is JSON.
type fileRecord struct {
	ID        string          `json:"id"`
	Encoding  persis.Encoding `json:"encoding"`
	Data      json.RawMessage `json:"data"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
	ExpiresAt *time.Time      `json:"expires_at,omitempty"`
}

func toFileRecord(r *persis.Record) *fileRecord {
	var data json.RawMessage
	if r.Encoding == persis.EncodingJSON {
		data = json.RawMessage(r.Data)
	} else {
		// Non-JSON (e.g. proto/binary) payloads are not valid JSON, so base64-encode
		// them as a JSON string so the file envelope always marshals cleanly.
		quoted, _ := json.Marshal(base64.StdEncoding.EncodeToString(r.Data))
		data = json.RawMessage(quoted)
	}
	return &fileRecord{
		ID:        r.ID,
		Encoding:  r.Encoding,
		Data:      data,
		CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt,
		ExpiresAt: r.ExpiresAt,
	}
}

func (fr *fileRecord) toRecord() *persis.Record {
	data := []byte(fr.Data)
	if fr.Encoding != persis.EncodingJSON {
		// Non-JSON data was stored as a base64 JSON string; decode it.
		var encoded string
		if json.Unmarshal(fr.Data, &encoded) == nil {
			if decoded, err := base64.StdEncoding.DecodeString(encoded); err == nil {
				data = decoded
			}
		}
	}
	return &persis.Record{
		ID:        fr.ID,
		Encoding:  fr.Encoding,
		Data:      data,
		CreatedAt: fr.CreatedAt,
		UpdatedAt: fr.UpdatedAt,
		ExpiresAt: fr.ExpiresAt,
	}
}

func sameRecord(a, b *persis.Record) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.ID != b.ID || a.Encoding != b.Encoding || !bytes.Equal(a.Data, b.Data) {
		return false
	}
	if !a.CreatedAt.Equal(b.CreatedAt) || !a.UpdatedAt.Equal(b.UpdatedAt) {
		return false
	}
	if a.ExpiresAt == nil || b.ExpiresAt == nil {
		return a.ExpiresAt == b.ExpiresAt
	}
	return a.ExpiresAt.Equal(*b.ExpiresAt)
}

// ─── Collection methods ───────────────────────────────────────────────────────

func (c *Collection) Get(_ context.Context, id string) (*persis.Record, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	path, err := c.filePath(id)
	if err != nil {
		return nil, err
	}
	fr, err := c.readFile(path)
	if err != nil {
		return nil, err
	}
	return fr.toRecord(), nil
}

func (c *Collection) Put(_ context.Context, rec *persis.Record) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	path, err := c.filePath(rec.ID)
	if err != nil {
		return err
	}
	return c.writeFile(path, toFileRecord(rec))
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
	fr, err := c.readFile(path)
	if err != nil {
		return err
	}
	if !sameRecord(fr.toRecord(), expected) {
		return persis.ErrConflict
	}
	if err := os.Remove(path); err != nil {
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
	if err := os.Remove(path); err != nil {
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
	fr, err := c.readFile(path)
	if err != nil {
		return err
	}
	// Compare decoded Record.Data bytes, not the raw envelope bytes, so CAS
	// works correctly regardless of whether data was base64-wrapped on disk.
	rec := fr.toRecord()
	if !bytes.Equal(rec.Data, expected) {
		return persis.ErrConflict
	}
	rec.Data = next
	updated := toFileRecord(rec)
	updated.UpdatedAt = time.Now().UTC()
	return c.writeFile(path, updated)
}

// Claim atomically dequeues one record matching q.
// Records are ordered by filename (natural sort), so queue adapters control
// priority by encoding it at the start of the ID (e.g., "high_<uuid>").
func (c *Collection) Claim(_ context.Context, q persis.ListQuery) (*persis.Record, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	walkRoot := c.prefixWalkRoot(q.Prefix)

	var candidates []string
	_ = filepath.WalkDir(walkRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".json") {
			return err
		}
		rel, _ := filepath.Rel(c.dir, path)
		if q.Prefix != "" && !strings.HasPrefix(relPathToID(rel), q.Prefix) {
			return nil
		}
		candidates = append(candidates, path)
		return nil
	})
	sort.Strings(candidates)

	for _, path := range candidates {
		fr, err := c.readFile(path)
		if err != nil {
			if !errors.Is(err, persis.ErrNotFound) {
				return nil, err
			}
			continue
		}
		if err := os.Remove(path); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return nil, err
			}
			continue
		}
		removeEmptyDirs(filepath.Dir(path), c.dir)
		return fr.toRecord(), nil
	}
	return nil, persis.ErrNotFound
}

// ─── internal helpers ─────────────────────────────────────────────────────────

func (c *Collection) filePath(id string) (string, error) {
	base := filepath.Clean(c.dir)
	full := filepath.Clean(filepath.Join(base, idToRelPath(id)))
	rel, err := filepath.Rel(base, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("file backend: record ID %q escapes collection root", id)
	}
	return full, nil
}

func (c *Collection) lockDir(key string) (string, error) {
	if key == "" {
		return c.dir, nil
	}
	path, err := c.filePath(strings.TrimSuffix(key, "/") + "/_lock")
	if err != nil {
		return "", err
	}
	return filepath.Dir(path), nil
}

func (c *Collection) readFile(path string) (*fileRecord, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // path is derived from a validated root + record ID
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, persis.ErrNotFound
		}
		return nil, err
	}
	var fr fileRecord
	if err := json.Unmarshal(raw, &fr); err != nil {
		return nil, fmt.Errorf("file backend: corrupt record at %q: %w", path, err)
	}
	// Legacy format: flat JSON without the "encoding" envelope field.
	// Wrap the raw bytes as the Data payload so old files are readable
	// by the new adapters. The file is rewritten in new format on next Put.
	if fr.Encoding == "" {
		rel, _ := filepath.Rel(c.dir, path)
		info, _ := os.Stat(path)
		mtime := time.Now().UTC()
		if info != nil {
			mtime = info.ModTime().UTC()
		}
		fr = fileRecord{
			ID:        relPathToID(rel),
			Encoding:  persis.EncodingJSON,
			Data:      json.RawMessage(raw),
			CreatedAt: mtime,
			UpdatedAt: mtime,
		}
	}
	return &fr, nil
}

func (c *Collection) writeFile(path string, fr *fileRecord) error {
	data, err := json.Marshal(fr)
	if err != nil {
		return err
	}
	return writeAtomic(path, data)
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
		fr, err := c.readFile(path)
		if err != nil {
			return nil // skip corrupt records
		}
		r := fr.toRecord()
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

// ─── file I/O helpers ─────────────────────────────────────────────────────────

// writeAtomic writes data to path via a temp-file + rename to avoid partial writes.
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, path)
}

// removeEmptyDirs removes dir and its ancestors up to (but not including)
// stopAt if they are empty.
func removeEmptyDirs(dir, stopAt string) {
	for dir != stopAt && strings.HasPrefix(dir, stopAt) {
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) > 0 {
			return
		}
		if err := os.Remove(dir); err != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}
