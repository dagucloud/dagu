// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package inventory

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// ErrNodeNotFound reports a node name that an inventory does not hold.
var ErrNodeNotFound = errors.New("node not found")

// PutNode adds node to inventory id, or replaces the node of that name,
// given the version the change is based on. It reports whether the node is
// new.
func PutNode(ctx context.Context, store Store, id string, node Node, version int64) (Stored, bool, error) {
	current, err := currentAt(ctx, store, id, version)
	if err != nil {
		return Stored{}, false, err
	}
	inv := current.Inventory
	var added bool
	inv.Nodes, added = withNode(inv.Nodes, node)
	stored, err := store.Replace(ctx, inv, version)
	return stored, added, err
}

// WithNode returns the inventories of stored as they would be with node in
// the inventory inventoryID, in place of the node of that name there, so a
// node can be checked against every rule before it is stored. It fails with
// ErrNotFound when stored holds no such inventory.
func WithNode(stored []Stored, inventoryID string, node Node) ([]Inventory, error) {
	invs := make([]Inventory, 0, len(stored))
	found := false
	for _, s := range stored {
		inv := s.Inventory
		if inv.ID == inventoryID {
			found = true
			inv.Nodes, _ = withNode(inv.Nodes, node)
		}
		invs = append(invs, inv)
	}
	if !found {
		return nil, fmt.Errorf("inventory %q: %w", inventoryID, ErrNotFound)
	}
	return invs, nil
}

// withNode returns a copy of nodes with node in place of the node of that
// name, or added, and whether it was added.
func withNode(nodes []Node, node Node) ([]Node, bool) {
	nodes = slices.Clone(nodes)
	if i := nodeIndex(nodes, node.Name); i >= 0 {
		nodes[i] = node
		return nodes, false
	}
	return append(nodes, node), true
}

// DeleteNode removes the node named name from inventory id, given the
// version the change is based on. It fails with ErrNodeNotFound when the
// inventory holds no such node.
func DeleteNode(ctx context.Context, store Store, id, name string, version int64) (Stored, error) {
	current, err := currentAt(ctx, store, id, version)
	if err != nil {
		return Stored{}, err
	}
	i := nodeIndex(current.Nodes, name)
	if i < 0 {
		return Stored{}, fmt.Errorf("%w: inventory %q has no node %q", ErrNodeNotFound, current.Name, name)
	}
	inv := current.Inventory
	inv.Nodes = slices.Delete(slices.Clone(inv.Nodes), i, i+1)
	return store.Replace(ctx, inv, version)
}

// currentAt returns inventory id when it is still at version. The change
// replaces it at that version, so one that interleaves fails as stale.
func currentAt(ctx context.Context, store Store, id string, version int64) (Stored, error) {
	current, err := store.Get(ctx, id)
	if err != nil {
		return Stored{}, err
	}
	if current.Version != version {
		return Stored{}, fmt.Errorf("%w: inventory %q is at version %d, not %d", ErrStale, current.Name, current.Version, version)
	}
	return current, nil
}

func nodeIndex(nodes []Node, name string) int {
	return slices.IndexFunc(nodes, func(n Node) bool { return strings.EqualFold(n.Name, name) })
}
