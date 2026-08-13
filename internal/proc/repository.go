// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package proc

import (
	"context"
	"sort"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

// Repository provides application-level access to process liveness.
type Repository struct {
	store Store
	now   func() time.Time
}

// NewRepository creates a process repository backed by store.
func NewRepository(store Store) *Repository {
	return &Repository{store: store, now: time.Now}
}

// Validate checks whether the backing store is usable.
func (r *Repository) Validate(ctx context.Context) error {
	return r.store.Validate(ctx)
}

// WithLock runs fn while holding the process-group lock.
func (r *Repository) WithLock(ctx context.Context, groupName string, fn func() error) error {
	return r.store.WithLock(ctx, groupName, fn)
}

// Acquire creates a process entry and starts its heartbeat.
func (r *Repository) Acquire(ctx context.Context, groupName string, meta ProcMeta) (ProcHandle, error) {
	if meta.StartedAt <= 0 {
		meta.StartedAt = r.now().UTC().Unix()
	}
	if err := meta.Validate(); err != nil {
		return nil, err
	}
	return r.store.Acquire(ctx, groupName, meta)
}

// CountAlive returns the number of distinct fresh DAG runs in a group.
func (r *Repository) CountAlive(ctx context.Context, groupName string) (int, error) {
	entries, err := r.store.ListEntries(ctx, groupName)
	if err != nil {
		return 0, err
	}
	seen := make(map[string]struct{})
	for _, entry := range entries {
		if entry.Fresh {
			seen[entry.Meta.DAGRun().String()] = struct{}{}
		}
	}
	return len(seen), nil
}

// CountAliveByDAGName returns the number of distinct fresh DAG runs for a DAG in a group.
func (r *Repository) CountAliveByDAGName(ctx context.Context, groupName, dagName string) (int, error) {
	entries, err := r.store.ListEntries(ctx, groupName)
	if err != nil {
		return 0, err
	}
	seen := make(map[string]struct{})
	for _, entry := range entries {
		if entry.Fresh && entry.Meta.Name == dagName {
			seen[entry.Meta.DAGRun().String()] = struct{}{}
		}
	}
	return len(seen), nil
}

// IsRunAlive reports whether a DAG run has a fresh entry in the group.
func (r *Repository) IsRunAlive(ctx context.Context, groupName string, dagRun ir.DAGRunRef) (bool, error) {
	entries, err := r.store.ListEntries(ctx, groupName)
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

// IsAttemptAlive reports whether a DAG-run attempt has a fresh entry in the group.
func (r *Repository) IsAttemptAlive(ctx context.Context, groupName string, dagRun ir.DAGRunRef, attemptID string) (bool, error) {
	entries, err := r.store.ListEntries(ctx, groupName)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if entry.Fresh && entry.Meta.Name == dagRun.Name && entry.Meta.DAGRunID == dagRun.ID && entry.Meta.AttemptID == attemptID {
			return true, nil
		}
	}
	return false, nil
}

// ListAlive returns distinct fresh DAG runs in canonical order.
func (r *Repository) ListAlive(ctx context.Context, groupName string) ([]ir.DAGRunRef, error) {
	entries, err := r.store.ListEntries(ctx, groupName)
	if err != nil {
		return nil, err
	}
	return freshRefs(entries), nil
}

// ListAllAlive returns distinct fresh DAG runs grouped in canonical order.
func (r *Repository) ListAllAlive(ctx context.Context) (map[string][]ir.DAGRunRef, error) {
	entries, err := r.store.ListAllEntries(ctx)
	if err != nil {
		return nil, err
	}
	result := make(map[string][]ir.DAGRunRef)
	seen := make(map[string]map[string]struct{})
	for _, entry := range entries {
		if !entry.Fresh {
			continue
		}
		if _, ok := seen[entry.GroupName]; !ok {
			seen[entry.GroupName] = make(map[string]struct{})
		}
		ref := entry.Meta.DAGRun()
		if _, ok := seen[entry.GroupName][ref.String()]; ok {
			continue
		}
		seen[entry.GroupName][ref.String()] = struct{}{}
		result[entry.GroupName] = append(result[entry.GroupName], ref)
	}
	for groupName := range result {
		sortDAGRuns(result[groupName])
	}
	return result, nil
}

// ListEntries returns all entries in a group, including stale entries.
func (r *Repository) ListEntries(ctx context.Context, groupName string) ([]ProcEntry, error) {
	return r.store.ListEntries(ctx, groupName)
}

// LatestFreshEntryByDAGName returns the newest fresh entry for a DAG in a group.
func (r *Repository) LatestFreshEntryByDAGName(ctx context.Context, groupName, dagName string) (*ProcEntry, error) {
	entries, err := r.store.ListEntries(ctx, groupName)
	if err != nil {
		return nil, err
	}
	var freshest *ProcEntry
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

// LatestHeartbeat returns the latest heartbeat observation for a DAG run.
func (r *Repository) LatestHeartbeat(ctx context.Context, groupName string, dagRun ir.DAGRunRef) (*ProcHeartbeat, error) {
	return r.store.LatestHeartbeat(ctx, groupName, dagRun)
}

// ListAllEntries returns all entries, including stale entries.
func (r *Repository) ListAllEntries(ctx context.Context) ([]ProcEntry, error) {
	return r.store.ListAllEntries(ctx)
}

// RemoveIfStale removes the exact entry when it remains stale and unchanged.
func (r *Repository) RemoveIfStale(ctx context.Context, entry ProcEntry) error {
	return r.store.RemoveIfStale(ctx, entry)
}

func freshRefs(entries []ProcEntry) []ir.DAGRunRef {
	seen := make(map[string]ir.DAGRunRef)
	for _, entry := range entries {
		if entry.Fresh {
			ref := entry.Meta.DAGRun()
			seen[ref.String()] = ref
		}
	}
	refs := make([]ir.DAGRunRef, 0, len(seen))
	for _, ref := range seen {
		refs = append(refs, ref)
	}
	sortDAGRuns(refs)
	return refs
}

func sortDAGRuns(refs []ir.DAGRunRef) {
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Name == refs[j].Name {
			return refs[i].ID < refs[j].ID
		}
		return refs[i].Name < refs[j].Name
	})
}
