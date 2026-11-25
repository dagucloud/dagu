// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"fmt"
	"net/http"

	api "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
)

// PreviewExecuteDAG implements api.StrictServerInterface. It plans a run of
// selected steps as ExecuteDAG would, and starts nothing.
func (a *API) PreviewExecuteDAG(ctx context.Context, request api.PreviewExecuteDAGRequestObject) (api.PreviewExecuteDAGResponseObject, error) {
	if err := a.isAllowed(config.PermissionRunDAGs); err != nil {
		return nil, err
	}
	dag, err := a.dagRepository.GetDetails(ctx, request.FileName, persis.DAGLoadOptions{AllowBuildErrors: true})
	if err != nil {
		return nil, &Error{
			HTTPStatus: http.StatusNotFound,
			Code:       api.ErrorCodeNotFound,
			Message:    fmt.Sprintf("DAG %s not found", request.FileName),
		}
	}
	if err := a.requireExecuteForWorkspace(ctx, dagWorkspaceName(dag)); err != nil {
		return nil, err
	}
	if err := buildErrorsToAPIError(dag.BuildErrors); err != nil {
		return nil, err
	}
	if request.Body == nil {
		return nil, ErrInvalidRequestBody
	}
	selection, err := selectedStepsFromBody(&request.Body.Steps, request.Body.OutputsFromRunId, request.Body.OutputsFromRuns, nil, nil, nil, nil, nil)
	if err != nil {
		return nil, err
	}
	profileName, err := a.runProfileForDAG(ctx, request.FileName, dagWorkspaceName(dag), request.Body.Profile)
	if err != nil {
		return nil, err
	}
	planned, err := a.planSelectedSteps(ctx, dag, selectedStepsStart{
		selection:   selection,
		params:      valueOf(request.Body.Params),
		profileName: profileName,
	})
	if err != nil {
		return nil, err
	}
	return api.PreviewExecuteDAG200JSONResponse(stepReuseCheck(planned.plan)), nil
}

// PreviewExecuteDAGRunFromSpec implements api.StrictServerInterface. It plans
// a run of selected steps of a submitted spec as ExecuteDAGRunFromSpec
// would, and starts nothing.
func (a *API) PreviewExecuteDAGRunFromSpec(ctx context.Context, request api.PreviewExecuteDAGRunFromSpecRequestObject) (api.PreviewExecuteDAGRunFromSpecResponseObject, error) {
	if err := a.isAllowed(config.PermissionRunDAGs); err != nil {
		return nil, err
	}
	if request.Body == nil || request.Body.Spec == "" {
		return nil, &Error{
			HTTPStatus: http.StatusBadRequest,
			Code:       api.ErrorCodeBadRequest,
			Message:    "spec is required",
		}
	}
	selection, err := selectedStepsFromBody(&request.Body.Steps, request.Body.OutputsFromRunId, request.Body.OutputsFromRuns, nil, nil, nil, nil, nil)
	if err != nil {
		return nil, err
	}
	if err := a.requireDAGWriteForWorkspace(ctx, submittedSpecRuntimeWorkspaceName(request.Body.Spec, "")); err != nil {
		return nil, err
	}
	// The spec is loaded from a temporary file under a run ID of its own,
	// which no run takes.
	scratchID, err := ir.NewDAGRunID()
	if err != nil {
		return nil, fmt.Errorf("error generating dag-run ID: %w", err)
	}
	dag, cleanup, err := a.loadInlineDAG(ctx, request.Body.Spec, request.Body.Name, scratchID)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	if err := a.requireDAGWriteForWorkspace(ctx, runtimeWorkspaceName(dag, "")); err != nil {
		return nil, err
	}
	profileName, _, err := a.explicitRunProfile(ctx, request.Body.Profile)
	if err != nil {
		return nil, err
	}
	planned, err := a.planSelectedSteps(ctx, dag, selectedStepsStart{
		selection:   selection,
		params:      valueOf(request.Body.Params),
		profileName: profileName,
		inline:      true,
	})
	if err != nil {
		return nil, err
	}
	return api.PreviewExecuteDAGRunFromSpec200JSONResponse(stepReuseCheck(planned.plan)), nil
}
