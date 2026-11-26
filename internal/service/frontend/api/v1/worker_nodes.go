// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"fmt"
	"slices"

	api "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/audit"
	"github.com/dagucloud/dagu/v2/internal/inventory"
)

// ForgetWorkerNode forgets an offline worker node until its worker connects
// again. Workers are shared by the whole server, so it needs the admin role.
func (a *API) ForgetWorkerNode(ctx context.Context, request api.ForgetWorkerNodeRequestObject) (api.ForgetWorkerNodeResponseObject, error) {
	if err := a.requireAdmin(ctx); err != nil {
		return nil, err
	}
	name := request.NodeName
	err := inventory.ErrWorkerNodeNotFound
	if a.workerNodeStore != nil {
		err = a.workerNodeStore.Forget(ctx, name)
	}
	switch {
	case errors.Is(err, inventory.ErrWorkerNodeOnline):
		return api.ForgetWorkerNode409JSONResponse{
			Code:    api.ErrorCodeConflict,
			Message: fmt.Sprintf("worker %q is online; a worker is forgotten once it is offline", name),
		}, nil
	case errors.Is(err, inventory.ErrWorkerNodeNotFound):
		return a.forgetRefused(ctx, name)
	case err != nil:
		return nil, fmt.Errorf("failed to forget worker node: %w", err)
	}
	a.logAudit(ctx, audit.CategorySystem, "worker_node_forget", map[string]any{"node": name})
	return api.ForgetWorkerNode204Response{}, nil
}

// forgetRefused answers a forget of a name that no worker node has.
func (a *API) forgetRefused(ctx context.Context, name string) (api.ForgetWorkerNodeResponseObject, error) {
	if a.inventoryStore != nil {
		nodes, err := a.inventoryStore.Nodes(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to read inventory nodes: %w", err)
		}
		if node, ok := findInventoryNode(nodes, name); ok {
			return api.ForgetWorkerNode400JSONResponse{
				Code:    api.ErrorCodeBadRequest,
				Message: fmt.Sprintf("node %q is a node of inventory %q; remove it from its inventory", node.Name, node.InventoryName),
			}, nil
		}
	}
	return api.ForgetWorkerNode404JSONResponse{Code: api.ErrorCodeNotFound, Message: "Node not found"}, nil
}

// listWorkerNodes returns the worker nodes in name order, and the workers
// that are not nodes because a node of nodes has their name.
func (a *API) listWorkerNodes(ctx context.Context, nodes []inventory.ResolvedNode) ([]inventory.WorkerNode, []inventory.WorkerConflict, error) {
	if a.workerNodeStore == nil {
		return nil, nil, nil
	}
	workers, err := a.workerNodeStore.List(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read worker nodes: %w", err)
	}
	kept, conflicts := inventory.SplitWorkers(workers, nodes)
	return kept, conflicts, nil
}

func toWorkerNode(w inventory.WorkerNode) api.InventoryNode {
	labels := w.Labels.Strings()
	slices.Sort(labels)
	return api.InventoryNode{
		Name:       w.Name,
		Kind:       api.InventoryNodeKindWorker,
		Host:       w.Host,
		Labels:     append([]string{}, labels...),
		Online:     new(w.Online),
		LastSeenAt: new(w.LastSeenAt),
	}
}

func toWorkerConflicts(conflicts []inventory.WorkerConflict) []api.InventoryWorkerConflict {
	out := make([]api.InventoryWorkerConflict, 0, len(conflicts))
	for _, c := range conflicts {
		out = append(out, api.InventoryWorkerConflict{
			Worker:    c.Worker.Name,
			Inventory: api.InventoryRef{Id: c.Node.InventoryID, Name: c.Node.InventoryName},
		})
	}
	return out
}
