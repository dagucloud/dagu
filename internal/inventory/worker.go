// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package inventory

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

// daguHostWorkerID is the worker ID that runs on the Dagu host report.
const daguHostWorkerID = "local"

var (
	// ErrWorkerNodeNotFound reports a name that names no worker node.
	ErrWorkerNodeNotFound = errors.New("worker node not found")
	// ErrWorkerNodeOnline reports a worker node that cannot be forgotten
	// while its worker is online.
	ErrWorkerNodeOnline = errors.New("the worker is online")
)

// WorkerNode is a worker that sets worker.id, as the coordinator last
// recorded it (RFC 012 §Workers). The worker ID is the node's name.
type WorkerNode struct {
	Name string
	// Host is the host the worker reports. It is shown only.
	Host   string
	Labels ir.Labels
	// Online is false from when the worker stops sending heartbeats until
	// it connects again.
	Online bool
	// LastSeenAt is when the coordinator last recorded the worker: when it
	// connected, changed its host or labels, or went offline.
	LastSeenAt time.Time
}

// WorkerNodeStore keeps the worker nodes of an installation, which every
// project shares. Names are compared without case.
type WorkerNodeStore interface {
	// List returns every worker node, in name order.
	List(ctx context.Context) ([]WorkerNode, error)
	// Get returns the worker node named name, or ErrWorkerNodeNotFound.
	Get(ctx context.Context, name string) (WorkerNode, error)
	// Record stores node in place of the worker node of its name.
	Record(ctx context.Context, node WorkerNode) error
	// MarkOffline records the worker node seen as offline, last seen at
	// lastSeen. It changes nothing when the store no longer holds seen
	// exactly, because a newer record replaced it or it was forgotten.
	MarkOffline(ctx context.Context, seen WorkerNode, lastSeen time.Time) error
	// Forget removes an offline worker node until its worker connects
	// again. It fails with ErrWorkerNodeOnline while the worker is online,
	// and with ErrWorkerNodeNotFound when no worker node has the name. A
	// worker recorded as online whose heartbeats stopped is offline, even
	// when no coordinator is left to record that.
	Forget(ctx context.Context, name string) error
}

// WorkerConflict is a worker whose ID names a node of an inventory, so the
// worker is not a node.
type WorkerConflict struct {
	Worker WorkerNode
	// Node is the inventory node that holds the name.
	Node ResolvedNode
}

// IsNodeName reports whether a worker ID can name a node. A worker is a
// node only when its ID can, which the ID of a worker without worker.id,
// generated as hostname@pid, never does, and neither does local, the worker
// ID of runs on the Dagu host.
func IsNodeName(name string) bool {
	return nodeNameError(name) == nil && !strings.EqualFold(name, daguHostWorkerID)
}

// Node returns the worker node as a node that selectors match and runs bind
// to.
func (w WorkerNode) Node() ResolvedNode {
	return ResolvedNode{Name: w.Name, Kind: NodeKindWorker, Host: w.Host, Labels: slices.Clone(w.Labels)}
}

// WithWorkers returns a project's nodes, in name order: nodes, and the
// workers that are nodes (SplitWorkers).
func WithWorkers(nodes []ResolvedNode, workers []WorkerNode) []ResolvedNode {
	kept, _ := SplitWorkers(workers, nodes)
	all := slices.Grow(slices.Clone(nodes), len(kept))
	for _, w := range kept {
		all = append(all, w.Node())
	}
	slices.SortFunc(all, func(a, b ResolvedNode) int { return strings.Compare(a.Name, b.Name) })
	return all
}

// SplitWorkers returns the workers that are nodes, and the workers whose
// name a node of nodes holds, compared without case, which are not. Both
// keep the order of workers.
func SplitWorkers(workers []WorkerNode, nodes []ResolvedNode) ([]WorkerNode, []WorkerConflict) {
	// Node names are ASCII, so lowercasing compares them without case.
	byName := make(map[string]ResolvedNode, len(nodes))
	for _, n := range nodes {
		byName[strings.ToLower(n.Name)] = n
	}
	kept := make([]WorkerNode, 0, len(workers))
	var conflicts []WorkerConflict
	for _, w := range workers {
		if n, ok := byName[strings.ToLower(w.Name)]; ok {
			conflicts = append(conflicts, WorkerConflict{Worker: w, Node: n})
			continue
		}
		kept = append(kept, w)
	}
	return kept, conflicts
}
