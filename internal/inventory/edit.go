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

// Editor changes a project's inventories. After each change it forgets the
// trusted host key of every node name the change adds or removes, so a host
// that takes a node's name never inherits the key a user trusted for another
// host (RFC 012 §Host keys). A renamed node is removed and added, so its key
// is trusted again.
type Editor struct {
	store    Store
	hostKeys HostKeyStore
}

// NewEditor returns an editor of store's inventories. A nil hostKeys holds
// no trusted keys, so none are forgotten.
func NewEditor(store Store, hostKeys HostKeyStore) *Editor {
	return &Editor{store: store, hostKeys: hostKeys}
}

// Create stores inv under a new ID.
func (e *Editor) Create(ctx context.Context, inv Inventory) (Stored, error) {
	var stored Stored
	err := e.forgetChanged(ctx, func() (err error) {
		stored, err = e.store.Create(ctx, inv)
		return err
	})
	return stored, err
}

// Replace replaces inventory inv.ID as a whole, given the version the change
// is based on.
func (e *Editor) Replace(ctx context.Context, inv Inventory, version int64) (Stored, error) {
	var stored Stored
	err := e.forgetChanged(ctx, func() (err error) {
		stored, err = e.store.Replace(ctx, inv, version)
		return err
	})
	return stored, err
}

// Delete removes an inventory, given the version the change is based on.
func (e *Editor) Delete(ctx context.Context, id string, version int64) error {
	return e.forgetChanged(ctx, func() error {
		return e.store.Delete(ctx, id, version)
	})
}

// Import imports invs as [Store.Import] does.
func (e *Editor) Import(ctx context.Context, invs []Inventory, opts ImportOptions) (ImportResult, error) {
	if opts.DryRun {
		return e.store.Import(ctx, invs, opts)
	}
	var result ImportResult
	err := e.forgetChanged(ctx, func() (err error) {
		result, err = e.store.Import(ctx, invs, opts)
		return err
	})
	return result, err
}

// PutNode adds node to inventory id, or replaces the node of that name,
// given the version the change is based on. It reports whether the node is
// new.
func (e *Editor) PutNode(ctx context.Context, id string, node Node, version int64) (Stored, bool, error) {
	current, err := e.current(ctx, id, version)
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
	stored, err := e.Replace(ctx, inv, version)
	return stored, i < 0, err
}

// DeleteNode removes the node named name from inventory id, given the
// version the change is based on. It fails with ErrNodeNotFound when the
// inventory holds no such node.
func (e *Editor) DeleteNode(ctx context.Context, id, name string, version int64) (Stored, error) {
	current, err := e.current(ctx, id, version)
	if err != nil {
		return Stored{}, err
	}
	i := nodeIndex(current.Nodes, name)
	if i < 0 {
		return Stored{}, fmt.Errorf("%w: inventory %q has no node %q", ErrNodeNotFound, current.Name, name)
	}
	inv := current.Inventory
	inv.Nodes = slices.Delete(slices.Clone(inv.Nodes), i, i+1)
	return e.Replace(ctx, inv, version)
}

// current returns inventory id when it is still at version.
func (e *Editor) current(ctx context.Context, id string, version int64) (Stored, error) {
	current, err := e.store.Get(ctx, id)
	if err != nil {
		return Stored{}, err
	}
	if current.Version != version {
		return Stored{}, fmt.Errorf("%w: inventory %q is at version %d, not %d", ErrStale, current.Name, current.Version, version)
	}
	return current, nil
}

// forgetChanged runs write, then forgets the trusted keys of the node names
// it added or removed. A key that cannot be forgotten fails the call after
// the change is stored, since the key must not reach the next host of that
// name.
func (e *Editor) forgetChanged(ctx context.Context, write func() error) error {
	if e.hostKeys == nil {
		return write()
	}
	before, err := e.nodeNames(ctx)
	if err != nil {
		return err
	}
	writeErr := write()
	after, err := e.nodeNames(ctx)
	if err != nil {
		return errors.Join(writeErr, err)
	}
	var errs []error
	for name := range symmetricDifference(before, after) {
		if err := e.hostKeys.Forget(ctx, name); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(writeErr, errors.Join(errs...))
}

// nodeNames returns the lowercased names of every node.
func (e *Editor) nodeNames(ctx context.Context) (map[string]struct{}, error) {
	nodes, err := e.store.Nodes(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read the nodes: %w", err)
	}
	names := make(map[string]struct{}, len(nodes))
	for _, node := range nodes {
		names[strings.ToLower(node.Name)] = struct{}{}
	}
	return names, nil
}

func symmetricDifference(a, b map[string]struct{}) map[string]struct{} {
	diff := map[string]struct{}{}
	for name := range a {
		if _, ok := b[name]; !ok {
			diff[name] = struct{}{}
		}
	}
	for name := range b {
		if _, ok := a[name]; !ok {
			diff[name] = struct{}{}
		}
	}
	return diff
}

func nodeIndex(nodes []Node, name string) int {
	return slices.IndexFunc(nodes, func(n Node) bool { return strings.EqualFold(n.Name, name) })
}
