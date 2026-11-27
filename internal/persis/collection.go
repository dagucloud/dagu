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
	"strings"
	"time"
)

const (
	CollectionAPIKeys               = "api_keys"
	CollectionActiveDistributedRuns = "active_distributed_runs"
	CollectionAgentSessionCleanups  = "agent_session_cleanups"
	CollectionDAGPins               = "dag_pins"
	CollectionDAGRunLeases          = "dag_run_leases"
	CollectionDAGSettings           = "dag_settings"
	CollectionDAGState              = "dag_state"
	CollectionDispatchTasks         = "dispatch_tasks"
	CollectionHostKeys              = "host_keys"
	CollectionIncidents             = "incidents"
	CollectionInventories           = "inventories"
	CollectionLicense               = "license"
	CollectionNotifications         = "notifications"
	CollectionProfiles              = "profiles"
	CollectionPublicWebhooks        = "public_webhooks"
	CollectionQueue                 = "queue"
	CollectionRemoteNodes           = "remote_nodes"
	CollectionSchedulerState        = "scheduler_state"
	CollectionSecrets               = "secrets"
	CollectionUpgradeCheck          = "upgrade_check"
	CollectionUsers                 = "users"
	CollectionViews                 = "views"
	CollectionWatchState            = "watch_state"
	CollectionWebhooks              = "webhooks"
	CollectionWebhookDAGClaims      = "webhook_dag_claims"
	CollectionWorkerCertificates    = "worker_certificates"
	CollectionWorkerHeartbeats      = "worker_heartbeats"
	CollectionWorkerInvites         = "worker_invites"
	CollectionWorkerNodes           = "worker_nodes"
	CollectionWorkspaces            = "workspaces"
)

// Server-wide collections whose counterparts hold a project's records.
const (
	// CollectionIncidentProviders holds incident providers. A project's
	// incident policies and states are in CollectionIncidents.
	CollectionIncidentProviders = "incident_providers"
	// CollectionNotificationChannels holds notification channels and
	// notification settings. A project's DAG routes and route sets are in
	// CollectionNotifications.
	CollectionNotificationChannels = "notification_channels"
	// CollectionOAuthApps holds the OAuth apps that connections use.
	CollectionOAuthApps = "oauth_apps"
	// CollectionConnections holds connection metadata.
	CollectionConnections = "connections"
	// CollectionConnectionTokens holds the token state of connections.
	CollectionConnectionTokens = "connection_tokens"
	// CollectionConnectionCatalogs holds the tool catalogs of connections.
	CollectionConnectionCatalogs = "connection_catalogs"
	// CollectionSchedulerProcess holds the scheduler process's pause flag,
	// restart backoff, and outages. A project's scheduler state is in
	// CollectionSchedulerState.
	CollectionSchedulerProcess = "scheduler_process"
)

// SchedulerDAGRecordID names a DAG's record in the scheduler state
// collection: the ended periods in which its slots could not run. The name
// is one segment of the ID on every host, and no two DAGs share a record.
func SchedulerDAGRecordID(dag string) string {
	return "dags/" + dagSegmentEscaper.Replace(dag)
}

// dagSegmentEscaper keeps a DAG name, which may name a subdirectory, to one
// ID segment. The collection stores the segment as every host can.
var dagSegmentEscaper = strings.NewReplacer("%", "%25", "/", "%2F")

// Record is the storage primitive for collection-backed control-plane data.
//
// ID uses "/" as a hierarchy separator so that a [ListQuery.Prefix] of
// "mydag/" returns all records whose IDs start with that prefix — enabling
// efficient tree traversal without storage-specific query syntax.
//
// A segment between separators may hold any characters. The backend maps it
// to a name its storage can hold, so callers pass IDs unescaped. IDs that
// differ only in letter case may share one record.
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

	// Lock returns the lock that serializes changes spanning several records
	// of the named collection, across every client of the backend.
	Lock(name string) Lock
}

// Lock serializes a group of reads and writes that must not interleave with
// another group's, such as a check across many records followed by a write.
type Lock interface {
	// Do runs fn while holding the lock. fn calls held right before each
	// write: held fails once the lock has passed to another client, for
	// example after this one stalled past the lock's stale threshold.
	Do(ctx context.Context, fn func(held func() error) error) error
}
