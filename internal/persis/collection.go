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

const (
	CollectionAPIKeys               = "api_keys"
	CollectionActiveDistributedRuns = "active_distributed_runs"
	CollectionDAGRunLeases          = "dag_run_leases"
	CollectionDAGSettings           = "dag_settings"
	CollectionDAGState              = "dag_state"
	CollectionDispatchTasks         = "dispatch_tasks"
	CollectionIncidents             = "incidents"
	CollectionLicense               = "license"
	CollectionNotifications         = "notifications"
	CollectionProfiles              = "profiles"
	CollectionQueue                 = "queue"
	CollectionRemoteNodes           = "remote_nodes"
	CollectionSchedulerState        = "scheduler_state"
	CollectionSecrets               = "secrets"
	CollectionUpgradeCheck          = "upgrade_check"
	CollectionUsers                 = "users"
	CollectionViews                 = "views"
	CollectionWebhooks              = "webhooks"
	CollectionWorkerHeartbeats      = "worker_heartbeats"
	CollectionWorkspaces            = "workspaces"
)

// Record is the storage primitive for collection-backed control-plane data.
//
// ID uses "/" as a hierarchy separator so that a [ListQuery.Prefix] of
// "mydag/" returns all records whose IDs start with that prefix — enabling
// efficient tree traversal without storage-specific query syntax.
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

	// Since is inclusive and Until is exclusive. Both bound results by
	// Record.CreatedAt.
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

// Collection is an isolated namespace of [Record]s.
// All methods must be safe for concurrent use. Mutations of the same record
// must be linearizable across clients sharing the physical namespace.
// Atomicity is per record; Collection does not define multi-record transactions.
// ErrConflict is reserved for failed conditional mutations, not transient
// backend write contention.
// Implementations map each collection to a distinct physical namespace.
type Collection interface {
	// Get returns the record identified by id.
	// Returns [ErrNotFound] if no record with that id exists.
	Get(ctx context.Context, id string) (*Record, error)

	// Put atomically creates or replaces a record.
	Put(ctx context.Context, rec *Record) error

	// Create atomically inserts rec. Returns [ErrConflict] when a record with
	// rec.ID already exists. Implementations must guarantee the check-and-insert
	// is atomic with respect to every concurrent mutation of the same record.
	Create(ctx context.Context, rec *Record) error

	// Delete removes the record with the given id.
	// Returns nil if the record does not exist.
	Delete(ctx context.Context, id string) error

	// CompareAndDelete atomically removes expected.ID only when the current
	// record still matches expected. Returns [ErrConflict] when it does not and
	// [ErrNotFound] when the record does not exist.
	CompareAndDelete(ctx context.Context, expected *Record) error

	// List returns a page of records matching q, ordered by CreatedAt and then ID,
	// both ascending.
	List(ctx context.Context, q ListQuery) (*Page, error)

	// CompareAndSwap atomically replaces record id only when its current Data
	// bytes equal expected. Returns [ErrConflict] when they do not match and
	// [ErrNotFound] when the record does not exist.
	CompareAndSwap(ctx context.Context, id string, expected, next []byte) error
}

// Backend provides isolated, named control-plane collections. Collection names
// must be non-empty portable identifiers containing only ASCII letters, digits,
// hyphens, and underscores. Passing any other name is a programmer error and
// may panic.
type Backend interface {
	Collection(name string) Collection
}
