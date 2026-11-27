// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/intake"
	"github.com/dagucloud/dagu/v2/internal/inventory"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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

// boundWorker returns the worker that runs a run of dag with the node list
// names, or "" for a run not bound to a worker node (intake.BoundWorker).
// admitted is the worker node the run was started bound to, or empty for a
// new run. A queued run needs no coordinator until it leaves the queue.
func (a *API) boundWorker(ctx context.Context, dag *ir.DAG, names []string, admitted string, queued bool) (string, error) {
	worker, err := intake.BoundWorker(ctx, dag, names, a.projectNodes(), queued || a.coordinatorCli != nil, admitted)
	switch {
	case errors.Is(err, intake.ErrNoCoordinator):
		return "", &Error{
			HTTPStatus: http.StatusServiceUnavailable,
			Code:       api.ErrorCodeInternalError,
			Message:    err.Error(),
		}
	case errors.Is(err, intake.ErrWorkerNodeChanged):
		return "", &Error{
			HTTPStatus: http.StatusConflict,
			Code:       api.ErrorCodeConflict,
			Message:    err.Error(),
		}
	}
	return worker, err
}

// projectNodes returns the nodes of the project, or nil without inventories.
func (a *API) projectNodes() runtime.NodeSelector {
	if a.inventoryStore == nil {
		return nil
	}
	return inventory.NewProjectNodes(a.inventoryStore, a.workerNodeStore)
}

// workerDispatchError answers the coordinator's refusal of a run bound to a
// worker node, such as `worker node "gw-1" is offline`, as a conflict that
// carries the reason. Any other error is returned as it is.
func workerDispatchError(err error) error {
	var refusal interface{ GRPCStatus() *status.Status }
	if !errors.As(err, &refusal) || refusal.GRPCStatus().Code() != codes.FailedPrecondition {
		return err
	}
	return &Error{
		HTTPStatus: http.StatusConflict,
		Code:       api.ErrorCodeConflict,
		Message:    refusal.GRPCStatus().Message(),
	}
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
