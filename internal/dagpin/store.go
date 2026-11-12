// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package dagpin defines the server-wide set of pinned DAGs. Pinned DAGs are
// listed before all others; the set is shared by every user of a server.
package dagpin

import "context"

// Store persists pinned DAG IDs, which are DAG file names without a YAML
// extension. Implementations must be safe for concurrent use.
type Store interface {
	// List returns the pinned IDs, or an empty set when nothing is pinned.
	List(ctx context.Context) (map[string]struct{}, error)
	// Pin adds id to the set. Pinning an already pinned ID is a no-op.
	Pin(ctx context.Context, id string) error
	// Unpin removes id from the set. Unpinning an ID that is not pinned is a no-op.
	Unpin(ctx context.Context, id string) error
	// Rename moves the pin from oldID to newID. It is a no-op when oldID is
	// not pinned.
	Rename(ctx context.Context, oldID, newID string) error
}
