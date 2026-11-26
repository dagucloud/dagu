// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

// workerNodeRecorder keeps the worker nodes in step with the workers'
// heartbeats (RFC 012 §Workers). It writes a worker node when its worker
// first connects, changes its host or labels, or goes offline, and never
// for a heartbeat that changes nothing.
type workerNodeRecorder struct {
	store inventory.WorkerNodeStore

	mu sync.Mutex
	// recorded holds each worker node as this coordinator last wrote or
	// read it, by worker ID. reconcile reloads it from the store, so a node
	// that another coordinator marked offline, or that was forgotten, is
	// written again at its worker's next heartbeat.
	recorded map[string]inventory.WorkerNode
}

// newWorkerNodeRecorder returns a recorder that writes to store, or nil when
// store is nil.
func newWorkerNodeRecorder(store inventory.WorkerNodeStore) *workerNodeRecorder {
	if store == nil {
		return nil
	}
	return &workerNodeRecorder{store: store, recorded: make(map[string]inventory.WorkerNode)}
}

// list returns the recorded worker nodes in name order, or none when no
// store is configured. A worker whose ID an inventory node holds is listed
// too; inventory.SplitWorkers tells it apart.
func (r *workerNodeRecorder) list(ctx context.Context) ([]inventory.WorkerNode, error) {
	if r == nil {
		return nil, nil
	}
	return r.store.List(ctx)
}

// observe records the worker of a heartbeat received at the given time as
// online, when the worker is a node and its record differs.
func (r *workerNodeRecorder) observe(ctx context.Context, workerID, host string, labels map[string]string, at time.Time) {
	if r == nil || !inventory.IsNodeName(workerID) {
		return
	}
	node := inventory.WorkerNode{Name: workerID, Host: host, Labels: workerNodeLabels(labels), Online: true, LastSeenAt: at}
	r.mu.Lock()
	prev, ok := r.recorded[workerID]
	r.mu.Unlock()
	if ok && prev.Online && prev.Host == node.Host && slices.Equal(prev.Labels.Strings(), node.Labels.Strings()) {
		return
	}
	// The write is not under the lock, so a slow store does not hold up
	// other workers' heartbeats. Two heartbeats of one worker may both
	// write; the records are alike.
	if err := r.store.Record(ctx, node); err != nil {
		logger.Warn(ctx, "Failed to record worker node", tag.WorkerID(workerID), tag.Error(err))
		return
	}
	r.mu.Lock()
	r.recorded[workerID] = node
	r.mu.Unlock()
}

// reconcile marks offline each worker node recorded as online whose worker
// sent no heartbeat since staleBefore, last seen at its last heartbeat, and
// then reloads the recorded nodes from the store. lastHeartbeat returns
// when a worker last sent a heartbeat, or false when it is not known.
func (r *workerNodeRecorder) reconcile(ctx context.Context, staleBefore time.Time, lastHeartbeat func(workerID string) (time.Time, bool, error)) {
	if r == nil {
		return
	}
	nodes, err := r.list(ctx)
	if err != nil {
		logger.Warn(ctx, "Failed to list worker nodes", tag.Error(err))
		return
	}
	recorded := make(map[string]inventory.WorkerNode, len(nodes))
	for _, node := range nodes {
		if node.Online {
			last, known, err := lastHeartbeat(node.Name)
			if err != nil {
				logger.Warn(ctx, "Failed to read worker heartbeat", tag.WorkerID(node.Name), tag.Error(err))
			} else if !known || last.Before(staleBefore) {
				// A worker whose heartbeat is gone was last seen when its
				// record was written.
				lastSeen := node.LastSeenAt
				if known {
					lastSeen = last
				}
				if err := r.store.MarkOffline(ctx, node, lastSeen); err != nil {
					logger.Warn(ctx, "Failed to record worker node offline", tag.WorkerID(node.Name), tag.Error(err))
				} else {
					node.Online, node.LastSeenAt = false, lastSeen
				}
			}
		}
		recorded[node.Name] = node
	}
	r.mu.Lock()
	r.recorded = recorded
	r.mu.Unlock()
}

// reconcileWorkerNodes marks offline the worker nodes whose heartbeats are
// stale. It runs before stale heartbeats are removed, so a worker's last
// heartbeat is still known.
func (h *Handler) reconcileWorkerNodes(ctx context.Context) {
	h.workerNodes.reconcile(ctx, time.Now().Add(-h.staleHeartbeatThreshold), func(workerID string) (time.Time, bool, error) {
		if h.workerHeartbeatStore == nil {
			h.mu.Lock()
			defer h.mu.Unlock()
			info, ok := h.heartbeats[workerID]
			if !ok {
				return time.Time{}, false, nil
			}
			return info.lastHeartbeatAt, true, nil
		}
		record, err := h.workerHeartbeatStore.Get(ctx, workerID)
		if errors.Is(err, dispatch.ErrWorkerHeartbeatNotFound) {
			return time.Time{}, false, nil
		}
		if err != nil {
			return time.Time{}, false, err
		}
		return record.LastHeartbeatTime(), true, nil
	})
}

// workerNodeLabels returns a worker's labels lowercased, as labels are, and
// sorted as the store lists them, so a heartbeat compares equal to the
// record it matches.
func workerNodeLabels(labels map[string]string) ir.Labels {
	strs := make([]string, 0, len(labels))
	for key, value := range labels {
		strs = append(strs, ir.ParseLabel(ir.Label{Key: key, Value: value}.String()).String())
	}
	slices.Sort(strs)
	return ir.NewLabels(strs)
}
