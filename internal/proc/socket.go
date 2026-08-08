// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package proc owns process liveness and control addressing.
package proc

import (
	"github.com/dagucloud/dagu/v2/internal/cmn/sock"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

// DAGSocketAddr returns the control socket address for a DAG run.
func DAGSocketAddr(dag *ir.DAG, runID string) string {
	identity := dag.Location
	if identity == "" {
		identity = dag.Name
	}
	return sock.Addr(identity, runID)
}

// SubDAGSocketAddr returns the control socket address for a child DAG run.
func SubDAGSocketAddr(dag *ir.DAG, runID string) string {
	return sock.Addr(dag.GetName(), runID)
}
