// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"context"
	"errors"

	"github.com/dagucloud/dagu/v2/internal/ir"
)

// ConnectionChecker checks a connection that a run's DAG declares, before
// the run's first step.
type ConnectionChecker interface {
	// CheckConnection returns an error that names the connection when a run
	// of a DAG in workspace cannot use ref. The workspace is empty for a DAG
	// without a workspace label.
	CheckConnection(ctx context.Context, workspace string, ref ir.ConnectionRef) error
}

// checkConnections checks every connection the DAG declares and returns
// the failures together. A workflow test case reads no connection record,
// as it reads no secret through a provider.
func (a *Agent) checkConnections(ctx context.Context) error {
	if a.connectionChecker == nil || a.testSideEffects != nil {
		return nil
	}
	workspace := workspaceNameFromDAG(a.dag)
	var errs []error
	for _, ref := range a.dag.Connections {
		if err := a.connectionChecker.CheckConnection(ctx, workspace, ref); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
