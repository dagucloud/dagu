// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package persis

import "github.com/dagucloud/dagu/v2/internal/ir"

// DAGChangeType identifies the kind of DAG lifecycle event.
type DAGChangeType int

const (
	DAGChangeAdded DAGChangeType = iota
	DAGChangeUpdated
	DAGChangeDeleted
)

// DAGChangeEvent represents a DAG lifecycle event for a stored DAG definition.
type DAGChangeEvent struct {
	DAGEntry
	Type DAGChangeType
	// More reports that further events of the same change follow, as when
	// one refresh changes several DAGs. The last event of a change has it
	// unset.
	More bool
}

// DAGEntry pairs a DAG definition with its stable persistence identity.
type DAGEntry struct {
	DefinitionID string
	DAG          *ir.DAG
}
