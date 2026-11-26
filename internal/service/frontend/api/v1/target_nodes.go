// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"fmt"
	"net/http"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/intake"
	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/ir"
)

// checkTargetNodes checks the node list a run starts with against the
// project's nodes and returns it as the run carries it, or nil without one
// (RFC 013 §Narrowing a run). A node: required DAG needs one node. The run
// starts in a subprocess, so a mistake in the list is answered here, before
// the launch.
func (a *API) checkTargetNodes(ctx context.Context, dag *ir.DAG, names []string) ([]string, error) {
	if len(names) == 0 && !dag.NodeRequired {
		return nil, nil
	}
	if a.inventoryStore == nil {
		return nil, inventoryStoreUnavailable()
	}
	known, err := inventory.NewProjectNodes(a.inventoryStore, a.workerNodeStore).Nodes(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read nodes: %w", err)
	}
	listed, err := intake.CheckTargetNodes(dag, names, known)
	if err != nil {
		return nil, &Error{
			HTTPStatus: http.StatusBadRequest,
			Code:       api.ErrorCodeBadRequest,
			Message:    "invalid targetNodes: " + err.Error(),
		}
	}
	return listed, nil
}

// targetNodesWithSelectedSteps refuses a selected-steps start with a node
// list.
func targetNodesWithSelectedSteps() *Error {
	return &Error{
		HTTPStatus: http.StatusBadRequest,
		Code:       api.ErrorCodeBadRequest,
		Message:    "targetNodes cannot be combined with selected steps",
	}
}
