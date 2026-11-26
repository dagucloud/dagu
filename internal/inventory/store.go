// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package inventory

import (
	"context"
	"errors"
	"time"
)

var (
	// ErrNotFound reports an inventory ID that names no stored inventory.
	ErrNotFound = errors.New("inventory not found")
	// ErrStale reports a change based on a version that is no longer current.
	ErrStale = errors.New("inventory changed since it was read")
	// ErrExists reports an imported inventory whose name is taken, when the
	// import does not replace existing inventories.
	ErrExists = errors.New("already exists")
)

// Store keeps a project's inventories. Every change is checked against all of
// the project's inventories before it is stored, and concurrent changes are
// applied one at a time. A read that spans inventories sees no change in
// progress. A change forgets the host key trusted for each node name it adds
// or removes, so a renamed node is trusted again under its new name.
type Store interface {
	// List returns the inventories in name order.
	List(ctx context.Context) ([]Stored, error)
	// Get returns one inventory, or ErrNotFound.
	Get(ctx context.Context, id string) (Stored, error)
	// Create stores inv under a new ID at version 1.
	Create(ctx context.Context, inv Inventory) (Stored, error)
	// Replace replaces inventory inv.ID as a whole. version is the version
	// the change is based on; ErrStale reports that it is no longer current.
	Replace(ctx context.Context, inv Inventory, version int64) (Stored, error)
	// Delete removes an inventory, given the version the change is based on.
	Delete(ctx context.Context, id string, version int64) error
	// Import checks invs as a whole and then stores them one at a time,
	// matching existing inventories by name. It stores nothing when any
	// inventory breaks a rule, and reports every broken rule. When a write
	// fails, the result names the inventories stored before it.
	Import(ctx context.Context, invs []Inventory, opts ImportOptions) (ImportResult, error)
	// Nodes returns the nodes of every inventory with defaults applied, in
	// name order.
	Nodes(ctx context.Context) ([]ResolvedNode, error)
	// Select returns the nodes a selector matches, in name order.
	Select(ctx context.Context, selector string) ([]ResolvedNode, error)
	// Named returns the nodes names names and each node they connect
	// through, in name order. A name of no node is left out.
	Named(ctx context.Context, names []string) ([]ResolvedNode, error)
}

// Stored is an inventory as the store holds it.
type Stored struct {
	Inventory
	// Version counts the changes stored, starting at 1. A change names the
	// version it is based on.
	Version   int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ImportOptions control an import.
type ImportOptions struct {
	// Replace replaces an inventory whose name exists, keeping its ID.
	Replace bool
	// DryRun checks the import and stores nothing.
	DryRun bool
}

// ImportResult lists the imported inventories by name, in import order.
type ImportResult struct {
	Created  []string
	Replaced []string
}
