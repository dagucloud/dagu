// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package persis defines the storage backend interface for Dagu's control plane.
//
// All control-plane data (dag runs, users, secrets, queues, heartbeats, etc.)
// flows through [Backend] → [Collection] → [Record]. Domain model changes live
// inside Record.Data (an opaque blob); the physical schema never changes.
//
// To add a new database backend, implement [Backend] and [Collection] only.
// All store adapters (dagrun, queue, proc, user, secret, …) work immediately.
package persis

import (
	"context"
	"time"
)

// Encoding identifies the serialization format of [Record.Data].
type Encoding string

const (
	EncodingJSON  Encoding = "json"
	EncodingProto Encoding = "proto3"
)

// Record is the universal storage primitive for all control-plane data.
//
// ID uses "/" as a hierarchy separator so that a [ListQuery.Prefix] of
// "mydag/" returns all records whose IDs start with that prefix — enabling
// efficient tree traversal without backend-specific query syntax.
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
	Encoding  Encoding
	CreatedAt time.Time
	UpdatedAt time.Time
	// ExpiresAt, when non-nil, signals the backend that this record may be
	// purged after this time. Used for heartbeat / lease / TTL records.
	ExpiresAt *time.Time
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

	// Limit caps the number of records returned. 0 = backend default.
	Limit int
}

// Page is the result of a [Collection.List] call.
type Page struct {
	Records    []*Record
	NextCursor string // empty when no further records exist
}

// Collection is a named, isolated namespace of [Record]s.
// All methods must be safe for concurrent use.
//
// Collection names map to distinct physical namespaces — directories in
// [file.Backend], rows in a single SQL table keyed by (collection, id),
// or key prefixes in etcd / Cassandra.
type Collection interface {
	// Get returns the record identified by id.
	// Returns [ErrNotFound] if no record with that id exists.
	Get(ctx context.Context, id string) (*Record, error)

	// Put creates or replaces a record.
	Put(ctx context.Context, rec *Record) error

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

	// Claim atomically removes one record matching q and returns it.
	// Returns [ErrNotFound] when no matching record exists.
	// Used exclusively by queue adapters to implement atomic dequeue.
	Claim(ctx context.Context, q ListQuery) (*Record, error)
}

// Backend is the factory for storage [Collection]s and the sole interface
// a new database driver must implement.
//
// Collections are created lazily; calling Collection("foo") never returns an
// error — failures surface on the first operation against the collection.
type Backend interface {
	// Collection returns the collection with the given name.
	// Multiple calls with the same name return the same logical namespace.
	Collection(name string) Collection

	// Close releases all resources held by the backend (file handles,
	// connection pools, etc.). No Collection method may be called after Close.
	Close() error
}
