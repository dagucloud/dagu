// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis"
)

const (
	procStoreVersion             = 1
	defaultProcStaleThreshold    = 90 * time.Second
	defaultProcHeartbeatInterval = 5 * time.Second
)

var _ exec.ProcStore = (*ProcStore)(nil)

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

// WithProcHeartbeatSyncInterval keeps configuration parity with the legacy
// proc store option.
// Collection-backed proc heartbeats are complete writes, so there is no
// separate sync loop to configure.
func WithProcHeartbeatSyncInterval(_ time.Duration) ProcStoreOption {
	return func(_ *ProcStore) {
	}
}

// WithProcLegacyDir enables transitional read/write compatibility with
// pre-refactor .proc heartbeat files under dir.
func WithProcLegacyDir(dir string) ProcStoreOption {
	return func(s *ProcStore) {
		s.legacyDir = dir
	}
}

// ProcStore implements [exec.ProcStore] on top of a [persis.Collection].
type ProcStore struct {
	col               persis.Collection
	staleTime         time.Duration
	heartbeatInterval time.Duration
	legacyDir         string

	mu         sync.Mutex
	locks      map[string]*procHeldLock
	localLocks map[string]*sync.Mutex
}

// NewProcStore creates a ProcStore backed by col.
func NewProcStore(col persis.Collection, opts ...ProcStoreOption) *ProcStore {
	s := &ProcStore{
		col:               col,
		staleTime:         defaultProcStaleThreshold,
		heartbeatInterval: defaultProcHeartbeatInterval,
		locks:             make(map[string]*procHeldLock),
		localLocks:        make(map[string]*sync.Mutex),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Acquire creates and starts a proc heartbeat.
func (s *ProcStore) Acquire(ctx context.Context, groupName string, meta exec.ProcMeta) (exec.ProcHandle, error) {
	if meta.StartedAt <= 0 {
		meta.StartedAt = time.Now().UTC().Unix()
	}
	if err := validateProcMeta(meta); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	handle := &ProcHandle{
		store:     s,
		groupName: groupName,
		recordID:  procRecordID(groupName, meta, now),
		createdAt: now,
		meta:      meta,
	}
	if s.legacyDir != "" {
		handle.legacyPath = procLegacyFilePath(s.legacyDir, groupName, meta, now)
	}
	if err := handle.startHeartbeat(ctx); err != nil {
		return nil, err
	}
	return handle, nil
}

// CountAlive returns the number of fresh DAG runs in a group.
func (s *ProcStore) CountAlive(ctx context.Context, groupName string) (int, error) {
	entries, err := s.ListEntries(ctx, groupName)
	if err != nil {
		return 0, err
	}
	seen := make(map[string]struct{})
	for _, entry := range entries {
		if !entry.Fresh {
			continue
		}
		seen[entry.Meta.DAGRun().String()] = struct{}{}
	}
	return len(seen), nil
}

// CountAliveByDAGName returns the number of fresh DAG runs for dagName in a group.
func (s *ProcStore) CountAliveByDAGName(ctx context.Context, groupName, dagName string) (int, error) {
	entries, err := s.ListEntries(ctx, groupName)
	if err != nil {
		return 0, err
	}
	seen := make(map[string]struct{})
	for _, entry := range entries {
		if !entry.Fresh || entry.Meta.Name != dagName {
			continue
		}
		seen[entry.Meta.DAGRun().String()] = struct{}{}
	}
	return len(seen), nil
}

// ListAlive returns fresh DAG runs in a group.
func (s *ProcStore) ListAlive(ctx context.Context, groupName string) ([]exec.DAGRunRef, error) {
	entries, err := s.ListEntries(ctx, groupName)
	if err != nil {
		return nil, err
	}
	return procFreshRefs(entries), nil
}

// IsRunAlive reports whether dagRun has a fresh proc entry in groupName.
func (s *ProcStore) IsRunAlive(ctx context.Context, groupName string, dagRun exec.DAGRunRef) (bool, error) {
	entries, err := s.ListEntries(ctx, groupName)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if entry.Fresh && entry.Meta.Name == dagRun.Name && entry.Meta.DAGRunID == dagRun.ID {
			return true, nil
		}
	}
	return false, nil
}

// IsAttemptAlive reports whether a specific attempt has a fresh proc entry.
func (s *ProcStore) IsAttemptAlive(ctx context.Context, groupName string, dagRun exec.DAGRunRef, attemptID string) (bool, error) {
	entries, err := s.ListEntries(ctx, groupName)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if !entry.Fresh {
			continue
		}
		if entry.Meta.Name == dagRun.Name && entry.Meta.DAGRunID == dagRun.ID && entry.Meta.AttemptID == attemptID {
			return true, nil
		}
	}
	return false, nil
}

// ListEntries returns all proc entries for groupName, including stale entries.
func (s *ProcStore) ListEntries(ctx context.Context, groupName string) ([]exec.ProcEntry, error) {
	entries, err := s.listCollectionEntries(ctx, groupName)
	if err != nil {
		return nil, err
	}
	if s.legacyDir != "" {
		legacy, err := s.listLegacyEntries(groupName)
		if err != nil {
			return nil, err
		}
		entries = append(entries, legacy...)
	}
	return dedupeAndSortProcEntries(entries), nil
}

// LatestFreshEntryByDAGName returns the newest fresh proc entry for dagName.
func (s *ProcStore) LatestFreshEntryByDAGName(ctx context.Context, groupName, dagName string) (*exec.ProcEntry, error) {
	entries, err := s.ListEntries(ctx, groupName)
	if err != nil {
		return nil, err
	}
	var freshest *exec.ProcEntry
	for i := range entries {
		entry := entries[i]
		if !entry.Fresh || entry.Meta.Name != dagName {
			continue
		}
		if freshest == nil ||
			entry.Meta.StartedAt > freshest.Meta.StartedAt ||
			(entry.Meta.StartedAt == freshest.Meta.StartedAt && entry.LastHeartbeatAt > freshest.LastHeartbeatAt) {
			copy := entry
			freshest = &copy
		}
	}
	return freshest, nil
}

// ListAllAlive returns all fresh DAG runs grouped by process group.
func (s *ProcStore) ListAllAlive(ctx context.Context) (map[string][]exec.DAGRunRef, error) {
	entries, err := s.ListAllEntries(ctx)
	if err != nil {
		return nil, err
	}
	result := make(map[string][]exec.DAGRunRef)
	seen := make(map[string]map[string]struct{})
	for _, entry := range entries {
		if !entry.Fresh {
			continue
		}
		if _, ok := seen[entry.GroupName]; !ok {
			seen[entry.GroupName] = make(map[string]struct{})
		}
		ref := entry.Meta.DAGRun()
		key := ref.String()
		if _, ok := seen[entry.GroupName][key]; ok {
			continue
		}
		seen[entry.GroupName][key] = struct{}{}
		result[entry.GroupName] = append(result[entry.GroupName], ref)
	}
	for groupName := range result {
		sort.Slice(result[groupName], func(i, j int) bool {
			if result[groupName][i].Name == result[groupName][j].Name {
				return result[groupName][i].ID < result[groupName][j].ID
			}
			return result[groupName][i].Name < result[groupName][j].Name
		})
	}
	return result, nil
}

// ListAllEntries returns all proc entries across all groups.
func (s *ProcStore) ListAllEntries(ctx context.Context) ([]exec.ProcEntry, error) {
	entries, err := s.listCollectionEntries(ctx, "")
	if err != nil {
		return nil, err
	}
	if s.legacyDir != "" {
		legacy, err := s.listAllLegacyEntries()
		if err != nil {
			return nil, err
		}
		entries = append(entries, legacy...)
	}
	return dedupeAndSortProcEntries(entries), nil
}

// RemoveIfStale removes entry if it is still stale and unchanged.
func (s *ProcStore) RemoveIfStale(ctx context.Context, entry exec.ProcEntry) error {
	if entry.GroupName == "" || entry.Fresh {
		return nil
	}
	if procEntryIsLegacyPath(entry.FilePath) {
		return s.removeLegacyIfStale(ctx, entry)
	}
	return s.removeCollectionIfStale(ctx, entry)
}

// Validate fails if any proc entry cannot be decoded.
func (s *ProcStore) Validate(ctx context.Context) error {
	_, err := s.ListAllEntries(ctx)
	if err != nil {
		return fmt.Errorf("validate proc store: %w", err)
	}
	return nil
}
