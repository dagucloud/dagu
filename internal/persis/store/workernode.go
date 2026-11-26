// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
)

var _ inventory.WorkerNodeStore = (*WorkerNodeStore)(nil)

// WorkerNodeStore implements [inventory.WorkerNodeStore] over a
// [persis.Collection], one record per worker node keyed by its lowercased
// name.
type WorkerNodeStore struct {
	col persis.Collection
	// heartbeats holds the workers' last heartbeats, or is nil.
	heartbeats dispatch.WorkerHeartbeatStore
}

type workerNodeStoredRecord struct {
	// SchemaVersion is the record's format.
	SchemaVersion int       `json:"schema_version"`
	Name          string    `json:"name"`
	Host          string    `json:"host,omitempty"`
	Labels        []string  `json:"labels"`
	Online        bool      `json:"online"`
	LastSeenAt    time.Time `json:"last_seen_at"`
}

// workerNodeSchemaVersion is the record format this version writes.
const workerNodeSchemaVersion = 1

// NewWorkerNodeStore creates a WorkerNodeStore backed by col. Forget takes a
// worker recorded as online to be offline once its last heartbeat in
// heartbeats is stale or gone, since only a running coordinator records a
// worker going offline. With a nil heartbeats, the record alone decides.
func NewWorkerNodeStore(col persis.Collection, heartbeats dispatch.WorkerHeartbeatStore) (*WorkerNodeStore, error) {
	if col == nil {
		return nil, errors.New("worker node store: collection cannot be nil")
	}
	return &WorkerNodeStore{col: col, heartbeats: heartbeats}, nil
}

func (s *WorkerNodeStore) List(ctx context.Context) ([]inventory.WorkerNode, error) {
	recs, err := listAll(ctx, s.col, persis.ListQuery{})
	if err != nil {
		return nil, fmt.Errorf("worker node store: list: %w", err)
	}
	nodes := make([]inventory.WorkerNode, 0, len(recs))
	for _, rec := range recs {
		// A record that cannot be read is left out, as the coordinator
		// writes it again when its worker connects.
		node, err := decodeWorkerNode(rec)
		if err != nil {
			continue
		}
		nodes = append(nodes, node)
	}
	slices.SortFunc(nodes, func(a, b inventory.WorkerNode) int { return strings.Compare(a.Name, b.Name) })
	return nodes, nil
}

func (s *WorkerNodeStore) Get(ctx context.Context, name string) (inventory.WorkerNode, error) {
	rec, err := s.col.Get(ctx, workerNodeRecordID(name))
	if errors.Is(err, persis.ErrNotFound) {
		return inventory.WorkerNode{}, inventory.ErrWorkerNodeNotFound
	}
	if err != nil {
		return inventory.WorkerNode{}, fmt.Errorf("worker node store: read worker node %q: %w", name, err)
	}
	return decodeWorkerNode(rec)
}

func (s *WorkerNodeStore) Record(ctx context.Context, node inventory.WorkerNode) error {
	rec, err := encodeWorkerNode(node)
	if err != nil {
		return err
	}
	if err := s.col.Put(ctx, rec); err != nil {
		return fmt.Errorf("worker node store: record worker node %q: %w", node.Name, err)
	}
	return nil
}

func (s *WorkerNodeStore) MarkOffline(ctx context.Context, seen inventory.WorkerNode, lastSeen time.Time) error {
	current, err := s.col.Get(ctx, workerNodeRecordID(seen.Name))
	if errors.Is(err, persis.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("worker node store: read worker node %q: %w", seen.Name, err)
	}
	stored, err := decodeWorkerNode(current)
	if err != nil || !sameWorkerNode(stored, seen) {
		return nil
	}
	offline := seen
	offline.Online = false
	offline.LastSeenAt = lastSeen
	next, err := encodeWorkerNode(offline)
	if err != nil {
		return err
	}
	err = s.col.CompareAndSwap(ctx, current.ID, current.Data, next.Data)
	if errors.Is(err, persis.ErrConflict) || errors.Is(err, persis.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("worker node store: record worker node %q offline: %w", seen.Name, err)
	}
	return nil
}

func (s *WorkerNodeStore) Forget(ctx context.Context, name string) error {
	id := workerNodeRecordID(name)
	// The record is deleted only as it was checked, so a worker that
	// connects meanwhile is not forgotten.
	return retryConflict(ctx, func(ctx context.Context) error {
		current, err := s.col.Get(ctx, id)
		if errors.Is(err, persis.ErrNotFound) {
			return inventory.ErrWorkerNodeNotFound
		}
		if err != nil {
			return fmt.Errorf("worker node store: read worker node %q: %w", name, err)
		}
		stored, err := decodeWorkerNode(current)
		if err == nil && stored.Online {
			online, err := s.heartbeatFresh(ctx, stored.Name)
			if err != nil {
				return err
			}
			if online {
				return inventory.ErrWorkerNodeOnline
			}
		}
		err = s.col.CompareAndDelete(ctx, current)
		if errors.Is(err, persis.ErrNotFound) {
			return inventory.ErrWorkerNodeNotFound
		}
		return err
	})
}

// heartbeatFresh reports whether the worker sent a heartbeat within the
// stale threshold, or true when the store knows no heartbeats.
func (s *WorkerNodeStore) heartbeatFresh(ctx context.Context, workerID string) (bool, error) {
	if s.heartbeats == nil {
		return true, nil
	}
	record, err := s.heartbeats.Get(ctx, workerID)
	if errors.Is(err, dispatch.ErrWorkerHeartbeatNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("worker node store: read the heartbeat of worker %q: %w", workerID, err)
	}
	return dispatch.WorkerHeartbeatFresh(*record, time.Now(), dispatch.DefaultStaleWorkerHeartbeatThreshold), nil
}

// workerNodeRecordID keys a worker node's record by its name without case,
// as node names are compared, so file systems that ignore case keep one
// file.
func workerNodeRecordID(name string) string {
	return strings.ToLower(name)
}

func encodeWorkerNode(node inventory.WorkerNode) (*persis.Record, error) {
	labels := node.Labels.Strings()
	slices.Sort(labels)
	data, err := persis.Encode(workerNodeStoredRecord{
		SchemaVersion: workerNodeSchemaVersion,
		Name:          node.Name,
		Host:          node.Host,
		Labels:        append([]string{}, labels...),
		Online:        node.Online,
		LastSeenAt:    node.LastSeenAt.UTC(),
	})
	if err != nil {
		return nil, fmt.Errorf("worker node store: encode worker node %q: %w", node.Name, err)
	}
	return &persis.Record{ID: workerNodeRecordID(node.Name), Data: data, CreatedAt: node.LastSeenAt, UpdatedAt: node.LastSeenAt}, nil
}

func decodeWorkerNode(rec *persis.Record) (inventory.WorkerNode, error) {
	var payload workerNodeStoredRecord
	if err := persis.Decode(rec, &payload); err != nil {
		return inventory.WorkerNode{}, fmt.Errorf("worker node store: decode %q: %w", rec.ID, err)
	}
	if payload.Name == "" {
		return inventory.WorkerNode{}, fmt.Errorf("worker node store: record %q has no name", rec.ID)
	}
	return inventory.WorkerNode{
		Name:       payload.Name,
		Host:       payload.Host,
		Labels:     ir.NewLabels(payload.Labels),
		Online:     payload.Online,
		LastSeenAt: payload.LastSeenAt,
	}, nil
}

// sameWorkerNode reports whether a and b record the same state of a worker.
func sameWorkerNode(a, b inventory.WorkerNode) bool {
	return a.Name == b.Name && a.Host == b.Host && a.Online == b.Online &&
		a.LastSeenAt.Equal(b.LastSeenAt) && slices.Equal(sortedLabelStrings(a.Labels), sortedLabelStrings(b.Labels))
}

func sortedLabelStrings(labels ir.Labels) []string {
	strs := labels.Strings()
	slices.Sort(strs)
	return strs
}
