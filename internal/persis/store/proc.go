// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"fmt"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/proc"
)

const (
	procStoreVersion             = 1
	defaultProcStaleThreshold    = 90 * time.Second
	defaultProcHeartbeatInterval = 5 * time.Second
)

var _ persis.ProcStore = (*ProcStore)(nil)

// ProcStoreOption configures a ProcStore.
type ProcStoreOption func(*ProcStore)

// WithProcStaleThreshold sets the duration after which a proc entry is stale.
func WithProcStaleThreshold(d time.Duration) ProcStoreOption {
	return func(s *ProcStore) {
		if d > 0 {
			s.staleTime = d
		}
	}
}

// WithProcHeartbeatInterval sets the heartbeat write interval.
func WithProcHeartbeatInterval(d time.Duration) ProcStoreOption {
	return func(s *ProcStore) {
		if d > 0 {
			s.heartbeatInterval = d
		}
	}
}

// ProcStore implements [persis.ProcStore] as JSON records in a
// [persis.LockingCollection].
type ProcStore struct {
	col               persis.LockingCollection
	staleTime         time.Duration
	heartbeatInterval time.Duration
}

// NewProcStore creates a ProcStore backed by an isolated collection namespace.
// The collection must support scoped locking.
func NewProcStore(col persis.Collection, opts ...ProcStoreOption) (*ProcStore, error) {
	lockingCollection, ok := col.(persis.LockingCollection)
	if !ok {
		return nil, fmt.Errorf("proc store: collection does not support scoped locking")
	}
	s := &ProcStore{
		col:               lockingCollection,
		staleTime:         defaultProcStaleThreshold,
		heartbeatInterval: defaultProcHeartbeatInterval,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s, nil
}

// Acquire creates and starts a proc heartbeat.
func (s *ProcStore) Acquire(ctx context.Context, groupName string, meta proc.ProcMeta) (proc.ProcHandle, error) {
	now := time.Now().UTC()
	handle := &ProcHandle{
		store:     s,
		groupName: groupName,
		recordID:  procRecordID(groupName, meta, now),
		createdAt: now,
		meta:      meta,
	}
	if err := handle.startHeartbeat(ctx); err != nil {
		return nil, err
	}
	return handle, nil
}

// ListEntries returns all proc entries for groupName, including stale entries.
func (s *ProcStore) ListEntries(ctx context.Context, groupName string) ([]proc.ProcEntry, error) {
	entries, err := s.listCollectionEntries(ctx, groupName)
	if err != nil {
		return nil, err
	}
	return dedupeAndSortProcEntries(entries), nil
}

// LatestHeartbeat returns the latest heartbeat observation for dagRun.
func (s *ProcStore) LatestHeartbeat(ctx context.Context, groupName string, dagRun ir.DAGRunRef) (*proc.ProcHeartbeat, error) {
	return s.latestCollectionHeartbeat(ctx, groupName, dagRun)
}

// ListAllEntries returns all proc entries across all groups.
func (s *ProcStore) ListAllEntries(ctx context.Context) ([]proc.ProcEntry, error) {
	entries, err := s.listCollectionEntries(ctx, "")
	if err != nil {
		return nil, err
	}
	return dedupeAndSortProcEntries(entries), nil
}

// RemoveIfStale removes entry if it is still stale and unchanged.
func (s *ProcStore) RemoveIfStale(ctx context.Context, entry proc.ProcEntry) error {
	if entry.GroupName == "" || entry.Fresh {
		return nil
	}
	if _, ok := entry.Identity.StoreValue(procEntryIdentityCollection); ok {
		return s.removeCollectionIfStale(ctx, entry)
	}
	return nil
}

// Validate fails if any proc entry cannot be decoded.
func (s *ProcStore) Validate(ctx context.Context) error {
	_, err := s.ListAllEntries(ctx)
	if err != nil {
		return fmt.Errorf("validate proc store: %w", err)
	}
	return nil
}
