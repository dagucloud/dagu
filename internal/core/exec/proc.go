// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package exec

import (
	"context"
	"time"
)

// ProcStore is an interface for managing process storage.
type ProcStore interface {
	// Lock try to lock process group return error if it's held by another process
	Lock(ctx context.Context, groupName string) error
	// UnLock unlocks process group
	Unlock(ctx context.Context, groupName string)
	// Acquire creates a new process entry for a given group name and execution metadata.
	// It automatically starts the heartbeat for the process.
	Acquire(ctx context.Context, groupName string, meta ProcMeta) (ProcHandle, error)
	// CountAlive retrieves the number of processes associated with a group name.
	CountAlive(ctx context.Context, groupName string) (int, error)
	// CountAlive retrieves the number of processes associated with a group name.
	CountAliveByDAGName(ctx context.Context, groupName, dagName string) (int, error)
	// IsRunAlive checks if a specific DAG run is currently alive.
	IsRunAlive(ctx context.Context, groupName string, dagRun DAGRunRef) (bool, error)
	// IsAttemptAlive checks if a specific DAG-run attempt is currently alive.
	IsAttemptAlive(ctx context.Context, groupName string, dagRun DAGRunRef, attemptID string) (bool, error)
	// ListAlive returns list of running DAG runs by the group name.
	ListAlive(ctx context.Context, groupName string) ([]DAGRunRef, error)
	// ListAllAlive returns all running DAG runs across all groups.
	// Returns a map where key is the group name and value is list of DAG runs.
	ListAllAlive(ctx context.Context) (map[string][]DAGRunRef, error)
	// ListEntries returns all proc entries for a group, including stale entries.
	ListEntries(ctx context.Context, groupName string) ([]ProcEntry, error)
	// LatestFreshEntryByDAGName returns the freshest proc entry for the DAG in the group.
	LatestFreshEntryByDAGName(ctx context.Context, groupName, dagName string) (*ProcEntry, error)
	// LatestHeartbeat returns the latest heartbeat observation for a DAG-run in the group.
	LatestHeartbeat(ctx context.Context, groupName string, dagRun DAGRunRef) (*ProcHeartbeat, error)
	// ListAllEntries returns all proc entries across all groups, including stale entries.
	ListAllEntries(ctx context.Context) ([]ProcEntry, error)
	// RemoveIfStale removes the exact proc entry if it is still stale and unchanged.
	RemoveIfStale(ctx context.Context, entry ProcEntry) error
}

// ProcHandle represents a process that is associated with a dag-run.
type ProcHandle interface {
	// Stop stops the heartbeat for the process.
	Stop(ctx context.Context) error
	// GetMeta retrieves the metadata for the process.
	GetMeta() ProcMeta
}

// ProcMeta is a struct that holds metadata for a process.
type ProcMeta struct {
	StartedAt    int64
	Name         string
	DAGRunID     string
	AttemptID    string
	RootName     string
	RootDAGRunID string
}

// Root returns the root DAG-run reference if present.
func (m ProcMeta) Root() DAGRunRef {
	if m.RootName == "" || m.RootDAGRunID == "" {
		return DAGRunRef{}
	}
	return NewDAGRunRef(m.RootName, m.RootDAGRunID)
}

// DAGRun returns the DAG-run reference for the proc entry.
func (m ProcMeta) DAGRun() DAGRunRef {
	return NewDAGRunRef(m.Name, m.DAGRunID)
}

// ProcEntry represents a single proc heartbeat file on disk.
type ProcEntry struct {
	GroupName       string
	FilePath        string
	Meta            ProcMeta
	LastHeartbeatAt int64
	Fresh           bool
}

// ProcHeartbeat is a storage-independent observation of a proc heartbeat.
type ProcHeartbeat struct {
	GroupName       string
	DAGRun          DAGRunRef
	AttemptID       string
	StartedAt       int64
	LastHeartbeatAt int64
	ObservedAt      time.Time
	Fresh           bool
}

// AdvancedSince reports whether this observation is newer than previous.
func (h ProcHeartbeat) AdvancedSince(previous ProcHeartbeat) bool {
	if h.GroupName != previous.GroupName || h.DAGRun != previous.DAGRun {
		return false
	}
	if h.AttemptID != previous.AttemptID {
		return h.StartedAt > previous.StartedAt || h.ObservedAt.After(previous.ObservedAt)
	}
	return h.LastHeartbeatAt > previous.LastHeartbeatAt || h.ObservedAt.After(previous.ObservedAt)
}

// DAGRun returns the DAG-run reference for the proc entry.
func (e ProcEntry) DAGRun() DAGRunRef {
	return e.Meta.DAGRun()
}

// IsRoot reports whether the proc entry belongs to a root DAG run.
func (e ProcEntry) IsRoot() bool {
	return e.Meta.RootName == e.Meta.Name && e.Meta.RootDAGRunID == e.Meta.DAGRunID
}

// AttemptKey returns a stable identifier for the exact proc-backed attempt.
func (e ProcEntry) AttemptKey() string {
	return e.GroupName + "|" + e.Meta.Root().String() + "|" + e.Meta.Name + "|" + e.Meta.DAGRunID + "|" + e.Meta.AttemptID
}

// RunScopeKey returns a stable identifier for the DAG-run scope across attempts.
func (e ProcEntry) RunScopeKey() string {
	return e.GroupName + "|" + e.Meta.Root().String() + "|" + e.Meta.Name + "|" + e.Meta.DAGRunID
}
