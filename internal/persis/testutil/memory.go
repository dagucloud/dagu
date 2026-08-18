// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package testutil provides test helpers for the persistence layer.
package testutil

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/persis"
)

// MemoryBackend groups named in-memory collections for unit tests.
type MemoryBackend struct {
	mu   sync.Mutex
	cols map[string]*MemoryCollection
}

var _ persis.Backend = (*MemoryBackend)(nil)

// NewMemoryBackend returns an empty in-memory backend.
func NewMemoryBackend() *MemoryBackend {
	return &MemoryBackend{cols: make(map[string]*MemoryCollection)}
}

func (b *MemoryBackend) Collection(name string) persis.Collection {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.cols[name]; !ok {
		b.cols[name] = &MemoryCollection{
			records: make(map[string]*persis.Record),
		}
	}
	return b.cols[name]
}

// ─── MemoryCollection ────────────────────────────────────────────────────────

// MemoryCollection is a single in-memory [persis.Collection].
type MemoryCollection struct {
	mu      sync.Mutex
	records map[string]*persis.Record
}

var _ persis.Collection = (*MemoryCollection)(nil)

func (c *MemoryCollection) Get(_ context.Context, id string) (*persis.Record, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, ok := c.records[id]
	if !ok {
		return nil, persis.ErrNotFound
	}
	return copyRecord(r), nil
}

func (c *MemoryCollection) Put(_ context.Context, rec *persis.Record) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.records[rec.ID] = copyRecord(rec)
	return nil
}

// Create atomically inserts rec. Returns [persis.ErrConflict] when a
// record with rec.ID already exists.
func (c *MemoryCollection) Create(_ context.Context, rec *persis.Record) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if rec == nil {
		return fmt.Errorf("memory backend: nil record")
	}
	if _, ok := c.records[rec.ID]; ok {
		return persis.ErrConflict
	}
	c.records[rec.ID] = copyRecord(rec)
	return nil
}

func (c *MemoryCollection) Delete(_ context.Context, id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.records, id)
	return nil
}

func (c *MemoryCollection) CompareAndDelete(_ context.Context, expected *persis.Record) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	current, ok := c.records[expected.ID]
	if !ok {
		return persis.ErrNotFound
	}
	if !sameMemoryRecord(current, expected) {
		return persis.ErrConflict
	}
	delete(c.records, expected.ID)
	return nil
}

// RecordIDs returns record IDs matching prefix without decoding record payloads.
func (c *MemoryCollection) RecordIDs(_ context.Context, prefix string) ([]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ids := make([]string, 0, len(c.records))
	for id := range c.records {
		if prefix != "" && !strings.HasPrefix(id, prefix) {
			continue
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

// RecordVersion returns a cheap version token for cache validation.
func (c *MemoryCollection) RecordVersion(_ context.Context, id string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, ok := c.records[id]
	if !ok {
		return "", persis.ErrNotFound
	}
	return fmt.Sprintf("%d/%d", r.UpdatedAt.UTC().UnixNano(), len(r.Data)), nil
}

func (c *MemoryCollection) List(_ context.Context, q persis.ListQuery) (*persis.Page, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	var recs []*persis.Record
	for _, r := range c.records {
		if q.Prefix != "" && !strings.HasPrefix(r.ID, q.Prefix) {
			continue
		}
		if q.Since != nil && r.CreatedAt.Before(*q.Since) {
			continue
		}
		if q.Until != nil && !r.CreatedAt.Before(*q.Until) {
			continue
		}
		recs = append(recs, copyRecord(r))
	}

	sort.Slice(recs, func(i, j int) bool {
		ti, tj := recs[i].CreatedAt, recs[j].CreatedAt
		if ti.Equal(tj) {
			return recs[i].ID < recs[j].ID
		}
		return ti.Before(tj)
	})

	recs = applyMemCursor(recs, q.Cursor)

	limit := q.Limit
	if limit <= 0 {
		limit = len(recs)
	}

	var nextCursor string
	if len(recs) > limit {
		nextCursor = memEncodeCursor(recs[limit-1].CreatedAt, recs[limit-1].ID)
		recs = recs[:limit]
	}

	return &persis.Page{Records: recs, NextCursor: nextCursor}, nil
}

func (c *MemoryCollection) CompareAndSwap(_ context.Context, id string, expected, next []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	r, ok := c.records[id]
	if !ok {
		return persis.ErrNotFound
	}
	if !bytes.Equal(r.Data, expected) {
		return persis.ErrConflict
	}
	updated := copyRecord(r)
	updated.Data = make([]byte, len(next))
	copy(updated.Data, next)
	updated.UpdatedAt = time.Now().UTC()
	c.records[id] = updated
	return nil
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func copyRecord(r *persis.Record) *persis.Record {
	cp := *r
	if len(r.Data) > 0 {
		cp.Data = make([]byte, len(r.Data))
		copy(cp.Data, r.Data)
	}
	return &cp
}

func sameMemoryRecord(a, b *persis.Record) bool {
	if a == nil || b == nil {
		return a == b
	}
	// CAS identity is Data-only — must match file/collection.go sameRecord.
	return a.ID == b.ID && bytes.Equal(a.Data, b.Data)
}

type memCursorVal struct {
	C time.Time `json:"c"`
	I string    `json:"i"`
}

func memEncodeCursor(t time.Time, id string) string {
	b, _ := json.Marshal(memCursorVal{C: t, I: id})
	return base64.RawStdEncoding.EncodeToString(b)
}

func memDecodeCursor(s string) (memCursorVal, bool) {
	b, err := base64.RawStdEncoding.DecodeString(s)
	if err != nil {
		return memCursorVal{}, false
	}
	var v memCursorVal
	if err := json.Unmarshal(b, &v); err != nil {
		return memCursorVal{}, false
	}
	return v, true
}

func applyMemCursor(recs []*persis.Record, cursor string) []*persis.Record {
	if cursor == "" {
		return recs
	}
	cv, ok := memDecodeCursor(cursor)
	if !ok {
		return recs
	}
	for i, r := range recs {
		if r.CreatedAt.After(cv.C) || (r.CreatedAt.Equal(cv.C) && r.ID > cv.I) {
			return recs[i:]
		}
	}
	return nil
}
