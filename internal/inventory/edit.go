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
	inv.Nodes = slices.Clone(inv.Nodes)
	i := nodeIndex(inv.Nodes, node.Name)
	if i < 0 {
		inv.Nodes = append(inv.Nodes, node)
	} else {
		inv.Nodes[i] = node
	}
	stored, err := store.Replace(ctx, inv, version)
	return stored, i < 0, err
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
