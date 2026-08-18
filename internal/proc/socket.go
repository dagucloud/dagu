// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package proc owns process liveness and control addressing.
package proc

import (
	"github.com/dagucloud/dagu/v2/internal/cmn/sock"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

// DAGSocketAddr returns the control socket address for a DAG run.
func DAGSocketAddr(dagRun ir.DAGRunRef) string {
	return sock.Addr(dagRun.Name, dagRun.ID)
}
