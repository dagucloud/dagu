// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package persis defines persistence contracts for Dagu's control plane.
//
// Collection-backed records flow through [Collection] and [Record].
// Domain-specific persistence contracts use dedicated store interfaces such as
// [DAGDefinitionStore], [DAGRunStore], and [ProcStore].
//
// Domain model changes for collection-backed records live inside Record.Data,
// leaving their physical schema unchanged.
package persis

import (
	"context"
	"time"
)

// Record is the storage primitive for collection-backed control-plane data.
//
// ID uses "/" as a hierarchy separator so that a [ListQuery.Prefix] of
// "mydag/" returns all records whose IDs start with that prefix — enabling
// efficient tree traversal without storage-specific query syntax.
//
// Example ID formats:
//
//	dag_runs   → "mydag/run-abc123/attempt-0"
//	secrets    → "default/db-password"
//	sessions   → "user-1/sess-xyz"
//	queue_items → "high_9f3a..." (priority + uuid, sort order = dequeue order)
type Record struct {
	ID        string
	Data      []byte
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ListQuery controls what [Collection.List] returns.
type ListQuery struct {
	// Prefix filters records whose ID starts with this string.
	// An empty Prefix returns all records in the collection.
	Prefix string

	// Since and Until bound results by Record.CreatedAt.
	Since *time.Time
	Until *time.Time

	// Cursor resumes a previous [Page] iteration.
	// Pass [Page.NextCursor] from the prior call; empty starts from the beginning.
	Cursor string

	// Limit caps the number of records returned. 0 uses the implementation default.
	Limit int
}

// Page is the result of a [Collection.List] call.
type Page struct {
	Records    []*Record
	NextCursor string // empty when no further records exist
}

// Collection is a named, isolated namespace of [Record]s.
// All methods must be safe for concurrent use. Implementations map each
// collection to a distinct physical namespace.
type Collection interface {
	// Get returns the record identified by id.
	// Returns [ErrNotFound] if no record with that id exists.
	Get(ctx context.Context, id string) (*Record, error)

	// Put creates or replaces a record.
	Put(ctx context.Context, rec *Record) error

	// Create atomically inserts rec. Returns [ErrConflict] when a record with
	// rec.ID already exists. Implementations must guarantee the check-and-insert
	// is atomic with respect to concurrent Put, CompareAndSwap, and Create calls.
	Create(ctx context.Context, rec *Record) error

	// Delete removes the record with the given id.
	// Returns nil if the record does not exist.
	Delete(ctx context.Context, id string) error

	// CompareAndDelete atomically removes expected.ID only when the current
	// record still matches expected. Returns [ErrConflict] when it does not.
	CompareAndDelete(ctx context.Context, expected *Record) error

	// List returns a page of records matching q, ordered by CreatedAt ascending.
	List(ctx context.Context, q ListQuery) (*Page, error)

	// CompareAndSwap atomically replaces record id only when its current Data
	// bytes equal expected. Returns [ErrConflict] when they do not match.
	// Used for optimistic concurrency on DAGRunStatus updates.
	CompareAndSwap(ctx context.Context, id string, expected, next []byte) error
}

// LockingCollection runs operations under a storage-wide lock scoped by key.
// Implementations must serialize the same key across all clients sharing the
// collection's physical namespace.
type LockingCollection interface {
	Collection
	WithLock(ctx context.Context, key string, fn func() error) error
}
