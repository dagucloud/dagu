// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package proc owns process liveness and control addressing.
package proc

import (
	"strings"

	"github.com/dagucloud/dagu/v2/internal/cmn/sock"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/project"
)

// DAGSocketAddr returns the control socket address for a DAG run of the
// default project.
func DAGSocketAddr(dagRun ir.DAGRunRef) string {
	return ProjectDAGSocketAddr("", dagRun)
}

// ProjectDAGSocketAddr returns the control socket address for a DAG run of
// the named project. The default project, and an empty name, keep the
// address of DAGSocketAddr, so runs of DAGs of the same name in two
// projects never share an address.
func ProjectDAGSocketAddr(projectName string, dagRun ir.DAGRunRef) string {
	identity := dagRun.Name
	if projectName != "" && !strings.EqualFold(projectName, project.DefaultName) {
		// A DAG name holds no "/", so no run of the default project has
		// this identity.
		identity = projectName + "/" + dagRun.Name
	}
	return sock.Addr(identity, dagRun.ID)
}
