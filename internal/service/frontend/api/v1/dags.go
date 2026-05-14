// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	osrt "runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/internal/agentsnapshot"
	"github.com/dagucloud/dagu/internal/auth"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/core/spec"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/dagucloud/dagu/internal/runtime/executor"
	"github.com/dagucloud/dagu/internal/service/audit"
	"github.com/dagucloud/dagu/internal/service/scheduler"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
)

const defaultHistoryLimit = 30

// resolveDAGName returns the DAG name from metadata, or falls back to fileName if metadata lookup fails.
func (a *API) resolveDAGName(ctx context.Context, fileName string) string {
	dag, err := a.dagStore.GetMetadata(ctx, fileName)
	if err != nil {
		return fileName
	}
	return dag.Name
}

// checkSingletonRunning returns an error if the DAG is already running in singleton mode.
func (a *API) checkSingletonRunning(ctx context.Context, dag *core.DAG) error {
	alive, err := a.procStore.CountAliveByDAGName(ctx, dag.ProcGroup(), dag.Name)
	if err != nil {
		return fmt.Errorf("failed to check singleton execution status: %w", err)
	}
	if alive > 0 {
		return &Error{
			HTTPStatus: http.StatusConflict,
			Code:       api.ErrorCodeAlreadyExists,
			Message:    fmt.Sprintf("DAG %s is already running (singleton mode)", dag.Name),
		}
	}
	return nil
}

// checkSingletonQueued returns an error if the DAG is already queued in singleton mode.
func (a *API) checkSingletonQueued(ctx context.Context, dag *core.DAG) error {
	queued, err := a.queueStore.ListByDAGName(ctx, dag.ProcGroup(), dag.Name)
	if err != nil {
		return fmt.Errorf("failed to check singleton queue status: %w", err)
	}
	if len(queued) > 0 {
		return &Error{
			HTTPStatus: http.StatusConflict,
			Code:       api.ErrorCodeAlreadyExists,
			Message:    fmt.Sprintf("DAG %s is already in queue (singleton mode)", dag.Name),
		}
	}
	return nil
}

// ValidateDAGSpec implements api.StrictServerInterface.
func (a *API) ValidateDAGSpec(ctx context.Context, request api.ValidateDAGSpecRequestObject) (api.ValidateDAGSpecResponseObject, error) {
	if request.Body == nil {
		return nil, &Error{
			HTTPStatus: http.StatusBadRequest,
			Code:       api.ErrorCodeBadRequest,
			Message:    "request body is required",
		}
	}

	name := "validated-dag"
	if request.Body.Name != nil {
		name = *request.Body.Name
	}

	// Load the DAG spec
	dag, err := a.dagStore.LoadSpec(ctx,
		[]byte(request.Body.Spec),
		spec.WithName(name),
		spec.WithAllowBuildErrors(),
	)

	var errs []string
	var loadErrs core.ErrorList
	if errors.As(err, &loadErrs) {
		errs = loadErrs.ToStringList()
	} else if err != nil {
		// Unexpected fatal error
		return nil, err
	}

	if dag != nil && len(dag.BuildErrors) > 0 {
		for _, e := range dag.BuildErrors {
			errs = append(errs, e.Error())
		}
	}

	details := toDAGDetails(dag)

	return &api.ValidateDAGSpec200JSONResponse{
		Valid:  len(errs) == 0,
		Dag:    details,
		Errors: errs,
	}, nil
}

func (a *API) CreateNewDAG(ctx context.Context, request api.CreateNewDAGRequestObject) (api.CreateNewDAGResponseObject, error) {
	if request.Body.Name == "" {
		return nil, &Error{
			HTTPStatus: http.StatusBadRequest,
			Code:       api.ErrorCodeBadRequest,
			Message:    "DAG name must not be empty",
		}
	}

	if err := core.ValidateDAGName(request.Body.Name); err != nil {
		return nil, &Error{
			HTTPStatus: http.StatusBadRequest,
			Code:       api.ErrorCodeBadRequest,
			Message:    err.Error(),
		}
	}

	var yamlSpec []byte
	var workspaceName string
	if request.Body.Spec != nil && strings.TrimSpace(*request.Body.Spec) != "" {
		dag, err := a.dagStore.LoadSpec(ctx,
			[]byte(*request.Body.Spec),
			spec.WithName(request.Body.Name),
		)

		if err != nil {
			var verrs core.ErrorList
			if errors.As(err, &verrs) {
				return nil, &Error{
					HTTPStatus: http.StatusBadRequest,
					Code:       api.ErrorCodeBadRequest,
					Message:    strings.Join(verrs.ToStringList(), "; "),
				}
			}
			return nil, &Error{
				HTTPStatus: http.StatusBadRequest,
				Code:       api.ErrorCodeBadRequest,
				Message:    err.Error(),
			}
		}
		workspaceName = dagWorkspaceName(dag)
		yamlSpec = []byte(*request.Body.Spec)
	} else {
		yamlSpec = []byte(`steps:
  - run: echo hello
`)
	}
	if err := a.requireDAGWriteForWorkspace(ctx, workspaceName); err != nil {
		return nil, err
	}

	if err := a.dagStore.Create(ctx, request.Body.Name, yamlSpec); err != nil {
		if errors.Is(err, exec.ErrDAGAlreadyExists) {
			return nil, &Error{
				HTTPStatus: http.StatusConflict,
				Code:       api.ErrorCodeAlreadyExists,
			}
		}
		return nil, fmt.Errorf("error creating DAG: %w", err)
	}

	a.logAudit(ctx, audit.CategoryDAG, "dag_create", map[string]any{"dag_name": request.Body.Name})

	return &api.CreateNewDAG201JSONResponse{
		Name: request.Body.Name,
	}, nil
}

func (a *API) DeleteDAG(ctx context.Context, request api.DeleteDAGRequestObject) (api.DeleteDAGResponseObject, error) {
	if a.dagWritesDisabled {
		return nil, errDAGWritesDisabled
	}
	dag, err := a.dagStore.GetDetails(ctx, request.FileName, spec.WithAllowBuildErrors())
	if err != nil {
		return nil, &Error{
			HTTPStatus: http.StatusNotFound,
			Code:       api.ErrorCodeNotFound,
			Message:    fmt.Sprintf("DAG %s not found", request.FileName),
		}
	}
	if err := a.requireDAGWriteForWorkspace(ctx, dagWorkspaceName(dag)); err != nil {
		return nil, err
	}
	if err := a.dagStore.Delete(ctx, request.FileName); err != nil {
		return nil, fmt.Errorf("error deleting DAG: %w", err)
	}

	if a.authService != nil && a.authService.HasWebhookStore() {
		if err := a.authService.DeleteWebhook(ctx, request.FileName); err != nil && !errors.Is(err, auth.ErrWebhookNotFound) {
			logger.Warn(ctx, "Failed to delete webhook for deleted DAG",
				tag.Name(request.FileName),
				tag.Error(err),
			)
		}
	}

	a.logAudit(ctx, audit.CategoryDAG, "dag_delete", map[string]any{"dag_name": request.FileName})

	return &api.DeleteDAG204Response{}, nil
}

func (a *API) GetDAGSpec(ctx context.Context, request api.GetDAGSpecRequestObject) (api.GetDAGSpecResponseObject, error) {
	yamlSpec, err := a.dagStore.GetSpec(ctx, request.FileName)
	if err != nil {
		return nil, err
	}

	dag, err := a.dagStore.LoadSpec(ctx,
		[]byte(yamlSpec),
		spec.WithName(request.FileName),
		spec.WithAllowBuildErrors(),
	)
	var errs []string

	var loadErrs core.ErrorList
	if errors.As(err, &loadErrs) {
		errs = loadErrs.ToStringList()
	} else if err != nil {
		return nil, err
	}

	if dag == nil {
		dag = &core.DAG{
			Name: request.FileName,
		}
		if err != nil {
			errs = append(errs, err.Error())
		}
	} else {
		errs = append(errs, extractBuildErrors(dag.BuildErrors)...)
		errs = append(errs, dag.BuildWarnings...)
	}
	if err := a.requireWorkspaceVisible(ctx, dagWorkspaceName(dag)); err != nil {
		return nil, err
	}

	details := toDAGDetails(dag)
	if details != nil {
		projectionDAG := dag
		if dag != nil {
			projectionDAG = &core.DAG{
				Name:     a.resolveDAGName(ctx, request.FileName),
				Schedule: dag.Schedule,
			}
		}
		details.NextRun = a.projectNextRun(ctx, projectionDAG)
	}

	return &api.GetDAGSpec200JSONResponse{
		Dag:    details,
		Spec:   yamlSpec,
		Errors: errs,
	}, nil
}

func (a *API) UpdateDAGSpec(ctx context.Context, request api.UpdateDAGSpecRequestObject) (api.UpdateDAGSpecResponseObject, error) {
	if a.dagWritesDisabled {
		return nil, errDAGWritesDisabled
	}
	currentDAG, err := a.dagStore.GetDetails(ctx, request.FileName, spec.WithAllowBuildErrors())
	if err != nil {
		return nil, err
	}
	if err := a.requireDAGWriteForWorkspace(ctx, dagWorkspaceName(currentDAG)); err != nil {
		return nil, err
	}
	nextDAG, err := a.dagStore.LoadSpec(ctx,
		[]byte(request.Body.Spec),
		spec.WithName(request.FileName),
		spec.WithAllowBuildErrors(),
	)
	if err != nil {
		return nil, err
	}
	if nextDAG != nil {
		if err := a.requireDAGWriteForWorkspace(ctx, dagWorkspaceName(nextDAG)); err != nil {
			return nil, err
		}
	}

	err = a.dagStore.UpdateSpec(ctx, request.FileName, []byte(request.Body.Spec))

	var loadErrs core.ErrorList
	var errs []string

	if err != nil {
		if errors.As(err, &loadErrs) {
			errs = loadErrs.ToStringList()
		} else {
			return nil, err
		}
	}

	a.logAudit(ctx, audit.CategoryDAG, "dag_update", map[string]any{"dag_name": request.FileName})
	a.notifyDAGMutation(request.FileName)

	return api.UpdateDAGSpec200JSONResponse{
		Errors: errs,
	}, nil
}

func (a *API) RenameDAG(ctx context.Context, request api.RenameDAGRequestObject) (api.RenameDAGResponseObject, error) {
	if a.dagWritesDisabled {
		return nil, errDAGWritesDisabled
	}
	if err := core.ValidateDAGName(request.Body.NewFileName); err != nil {
		return nil, &Error{
			HTTPStatus: http.StatusBadRequest,
			Code:       api.ErrorCodeBadRequest,
			Message:    err.Error(),
		}
	}

	dag, err := a.dagStore.GetMetadata(ctx, request.FileName)
	if err != nil {
		return nil, &Error{
			HTTPStatus: http.StatusNotFound,
			Code:       api.ErrorCodeNotFound,
			Message:    fmt.Sprintf("DAG %s not found", request.FileName),
		}
	}
	if err := a.requireDAGWriteForWorkspace(ctx, dagWorkspaceName(dag)); err != nil {
		return nil, err
	}

	dagStatus, err := a.dagRunMgr.GetLatestStatus(ctx, dag)
	if err != nil {
		return nil, &Error{
			HTTPStatus: http.StatusNotFound,
			Code:       api.ErrorCodeNotFound,
			Message:    fmt.Sprintf("DAG %s not found", request.FileName),
		}
	}

	if dagStatus.Status == core.Running {
		return nil, &Error{
			HTTPStatus: http.StatusBadRequest,
			Code:       api.ErrorCodeNotRunning,
			Message:    "DAG is running",
		}
	}

	if err := a.dagStore.Rename(ctx, request.FileName, request.Body.NewFileName); err != nil {
		return nil, fmt.Errorf("failed to move DAG: %w", err)
	}

	a.logAudit(ctx, audit.CategoryDAG, "dag_rename", map[string]any{
		"old_name": request.FileName,
		"new_name": request.Body.NewFileName,
	})

	return api.RenameDAG200Response{}, nil
}

func (a *API) GetDAGDAGRunHistory(ctx context.Context, request api.GetDAGDAGRunHistoryRequestObject) (api.GetDAGDAGRunHistoryResponseObject, error) {
	dag, err := a.dagStore.GetDetails(ctx, request.FileName, spec.WithAllowBuildErrors())
	if err != nil {
		return nil, &Error{
			HTTPStatus: http.StatusNotFound,
			Code:       api.ErrorCodeNotFound,
			Message:    fmt.Sprintf("DAG %s not found", request.FileName),
		}
	}
	if err := a.requireWorkspaceVisible(ctx, dagWorkspaceName(dag)); err != nil {
		return nil, err
	}
	dagName := a.resolveDAGName(ctx, request.FileName)
	recentHistory := a.dagRunMgr.ListRecentStatus(ctx, dagName, defaultHistoryLimit)

	var dagRuns []api.DAGRunDetails
	for _, status := range recentHistory {
		dagRuns = append(dagRuns, ToDAGRunDetails(status))
	}

	gridData := a.readHistoryData(ctx, dag, recentHistory)
	return api.GetDAGDAGRunHistory200JSONResponse{
		DagRuns:  dagRuns,
		GridData: gridData,
	}, nil
}

func (a *API) GetDAGDetails(ctx context.Context, request api.GetDAGDetailsRequestObject) (api.GetDAGDetailsResponseObject, error) {
	resp, err := a.getDAGDetailsData(ctx, request.FileName)
	if err != nil {
		var apiErr *Error
		if errors.As(err, &apiErr) {
			return nil, apiErr
		}
		if errors.Is(err, os.ErrNotExist) {
			return nil, &Error{
				HTTPStatus: http.StatusNotFound,
				Code:       api.ErrorCodeNotFound,
				Message:    err.Error(),
			}
		}
		return nil, &Error{
			HTTPStatus: http.StatusInternalServerError,
			Code:       api.ErrorCodeInternalError,
			Message:    err.Error(),
		}
	}
	return resp, nil
}

// getDAGDetailsData returns DAG details data. Used by both HTTP handler and SSE fetcher.
func (a *API) getDAGDetailsData(ctx context.Context, fileName string) (api.GetDAGDetails200JSONResponse, error) {
	dag, err := a.dagStore.GetDetails(ctx, fileName, spec.WithAllowBuildErrors())
	if err != nil {
		return api.GetDAGDetails200JSONResponse{}, fmt.Errorf("failed to load DAG %s: %w", fileName, err)
	}
	if err := a.requireWorkspaceVisible(ctx, dagWorkspaceName(dag)); err != nil {
		return api.GetDAGDetails200JSONResponse{}, err
	}

	dagStatus, err := a.dagRunMgr.GetLatestStatus(ctx, dag)
	if err != nil && !errors.Is(err, exec.ErrNoStatusData) {
		return api.GetDAGDetails200JSONResponse{}, fmt.Errorf("failed to get latest status for DAG %s", fileName)
	}
	// If ErrNoStatusData, dagStatus will be zero-value (empty), which is fine for DAGs with no runs

	// Get the raw spec YAML for SSE updates
	yamlSpec, err := a.dagStore.GetSpec(ctx, fileName)
	if err != nil {
		// Continue even if spec fetch fails - it's optional for SSE
		yamlSpec = ""
	}

	details := toDAGDetails(dag)
	if details != nil {
		details.NextRun = a.projectNextRun(ctx, dag)
	}

	localDAGs := make([]api.LocalDag, 0, len(dag.LocalDAGs))
	for _, localDAG := range dag.LocalDAGs {
		localDAGs = append(localDAGs, toLocalDAG(localDAG))
	}

	sort.Slice(localDAGs, func(i, j int) bool {
		return localDAGs[i].Name < localDAGs[j].Name
	})

	return api.GetDAGDetails200JSONResponse{
		FilePath:     ptrOf(dag.Location),
		Dag:          details,
		LatestDAGRun: ToDAGRunDetails(dagStatus),
		Suspended:    a.dagStore.IsSuspended(ctx, fileName),
		LocalDags:    localDAGs,
		Errors:       extractBuildErrors(dag.BuildErrors),
		Spec:         &yamlSpec,
		EditorHints:  a.buildDAGEditorHints(ctx, dag, fileName),
	}, nil
}

func (a *API) buildDAGEditorHints(ctx context.Context, dag *core.DAG, fileName string) *api.DAGEditorHints {
	baseConfigData := dag.BaseConfigData
	if len(baseConfigData) == 0 && a.config.Paths.BaseConfig != "" {
		raw, err := os.ReadFile(a.config.Paths.BaseConfig) //nolint:gosec
		switch {
		case err == nil:
			baseConfigData = raw
		case errors.Is(err, os.ErrNotExist):
			// No base config is a valid state.
		default:
			logger.Warn(ctx, "Failed to read base config for DAG editor hints",
				slog.String("dagFile", fileName),
				tag.Error(err),
			)
			return nil
		}
	}

	legacyDefinitionHints, err := spec.InheritedLegacyDefinitionEditorHints(baseConfigData)
	if err != nil {
		logger.Warn(ctx, "Failed to build inherited custom editor hints",
			slog.String("dagFile", fileName),
			tag.Error(err),
		)
		return nil
	}
	actionHints, err := spec.InheritedCustomActionEditorHints(baseConfigData)
	if err != nil {
		logger.Warn(ctx, "Failed to build inherited custom editor hints",
			slog.String("dagFile", fileName),
			tag.Error(err),
		)
		return nil
	}
	if len(legacyDefinitionHints) == 0 && len(actionHints) == 0 {
		return nil
	}

	editorLegacyDefinitionHints := make([]api.InheritedLegacyDefinitionHint, 0, len(legacyDefinitionHints))
	for _, hint := range legacyDefinitionHints {
		apiHint := api.InheritedLegacyDefinitionHint{
			InputSchema: hint.InputSchema,
			Name:        hint.Name,
			TargetType:  hint.TargetType,
		}
		if len(hint.OutputSchema) > 0 {
			outputSchema := hint.OutputSchema
			apiHint.OutputSchema = &outputSchema
		}
		if hint.Description != "" {
			desc := hint.Description
			apiHint.Description = &desc
		}
		editorLegacyDefinitionHints = append(editorLegacyDefinitionHints, apiHint)
	}

	editorActionHints := make([]api.InheritedCustomActionHint, 0, len(actionHints))
	for _, hint := range actionHints {
		apiHint := api.InheritedCustomActionHint{
			InputSchema: hint.InputSchema,
			Name:        hint.Name,
		}
		if len(hint.OutputSchema) > 0 {
			outputSchema := hint.OutputSchema
			apiHint.OutputSchema = &outputSchema
		}
		if hint.Description != "" {
			desc := hint.Description
			apiHint.Description = &desc
		}
		editorActionHints = append(editorActionHints, apiHint)
	}

	return &api.DAGEditorHints{
		InheritedCustomActions:     &editorActionHints,
		InheritedLegacyDefinitions: editorLegacyDefinitionHints,
	}
}

// extractBuildErrors converts a slice of errors to a slice of strings.
func extractBuildErrors(errs []error) []string {
	result := make([]string, 0, len(errs))
	for _, e := range errs {
		result = append(result, e.Error())
	}
	return result
}

func (a *API) readHistoryData(_ context.Context, dag *core.DAG, statusList []exec.DAGRunStatus) []api.DAGGridItem {
	statusLen := len(statusList)
	nodeData := make(map[string][]core.NodeStatus)
	handlerData := make(map[string][]core.NodeStatus)
	originalIndex := make(map[string]int)
	nextOriginalIndex := 0

	addStatus := func(data map[string][]core.NodeStatus, idx int, name string, status core.NodeStatus) {
		if _, exists := data[name]; !exists {
			data[name] = make([]core.NodeStatus, statusLen)
		}
		data[name][idx] = status
	}

	for idx, st := range statusList {
		for _, node := range st.Nodes {
			if _, ok := originalIndex[node.Step.Name]; !ok {
				originalIndex[node.Step.Name] = nextOriginalIndex
				nextOriginalIndex++
			}
			addStatus(nodeData, idx, node.Step.Name, node.Status)
		}
		// Key handlers by their type (onSuccess, onFailure, etc.) not step name
		// to ensure consistent lookup later
		handlerPairs := []struct {
			handlerType core.HandlerType
			node        *exec.Node
		}{
			{core.HandlerOnSuccess, st.OnSuccess},
			{core.HandlerOnFailure, st.OnFailure},
			{core.HandlerOnAbort, st.OnAbort},
			{core.HandlerOnExit, st.OnExit},
		}
		for _, h := range handlerPairs {
			if h.node != nil {
				if _, ok := originalIndex[h.handlerType.String()]; !ok {
					originalIndex[h.handlerType.String()] = nextOriginalIndex
					nextOriginalIndex++
				}
				addStatus(handlerData, idx, h.handlerType.String(), h.node.Status)
			}
		}
	}

	toHistory := func(statuses []core.NodeStatus) []api.NodeStatus {
		history := make([]api.NodeStatus, len(statuses))
		for i, s := range statuses {
			history[i] = api.NodeStatus(s)
		}
		return history
	}

	grid := make([]api.DAGGridItem, 0, len(nodeData)+len(handlerData))
	for name, statuses := range nodeData {
		grid = append(grid, api.DAGGridItem{Name: name, History: toHistory(statuses)})
	}

	var stepIndex map[string]int
	if dag != nil {
		stepIndex = make(map[string]int)
		if dag.Type == core.TypeGraph {
			if len(dag.BuildErrors) > 0 {
				for i, step := range dag.Steps {
					stepIndex[step.Name] = i
				}
			} else {
				inDegree := make(map[string]int)
				adj := make(map[string][]string)

				for _, step := range dag.Steps {
					inDegree[step.Name] = 0
				}
				for _, step := range dag.Steps {
					for _, dep := range step.Depends {
						adj[dep] = append(adj[dep], step.Name)
						inDegree[step.Name]++
					}
				}

				var queue []string
				for _, step := range dag.Steps {
					if inDegree[step.Name] == 0 {
						queue = append(queue, step.Name)
					}
				}

				origIdx := make(map[string]int)
				for i, s := range dag.Steps {
					origIdx[s.Name] = i
				}

				var topoOrder []string
				for len(queue) > 0 {
					sort.Slice(queue, func(i, j int) bool {
						return origIdx[queue[i]] < origIdx[queue[j]]
					})
					u := queue[0]
					queue = queue[1:]
					topoOrder = append(topoOrder, u)

					for _, v := range adj[u] {
						inDegree[v]--
						if inDegree[v] == 0 {
							queue = append(queue, v)
						}
					}
				}

				for i, name := range topoOrder {
					stepIndex[name] = i
				}

				// Assign any unreached steps an index offset
				offset := len(topoOrder)
				for i, s := range dag.Steps {
					if _, ok := stepIndex[s.Name]; !ok {
						stepIndex[s.Name] = offset + i
					}
				}
			}
		} else {
			for i, step := range dag.Steps {
				stepIndex[step.Name] = i
			}
		}
	}

	sort.Slice(grid, func(i, j int) bool {
		if stepIndex != nil {
			idxI, okI := stepIndex[grid[i].Name]
			idxJ, okJ := stepIndex[grid[j].Name]
			if okI && okJ {
				return idxI < idxJ
			}
			if okI {
				return true
			}
			if okJ {
				return false
			}
		}

		origI, okOrigI := originalIndex[grid[i].Name]
		origJ, okOrigJ := originalIndex[grid[j].Name]
		if okOrigI && okOrigJ {
			return origI < origJ
		}
		if okOrigI {
			return true
		}
		if okOrigJ {
			return false
		}
		return grid[i].Name < grid[j].Name
	})

	for _, handlerType := range []core.HandlerType{
		core.HandlerOnSuccess, core.HandlerOnFailure, core.HandlerOnAbort, core.HandlerOnExit,
	} {
		if statuses, ok := handlerData[handlerType.String()]; ok {
			grid = append(grid, api.DAGGridItem{Name: handlerType.String(), History: toHistory(statuses)})
		}
	}

	return grid
}

func (a *API) ListDAGs(ctx context.Context, request api.ListDAGsRequestObject) (api.ListDAGsResponseObject, error) {
	sortField := a.config.UI.DAGs.SortField
	if sortField == "" {
		sortField = "name"
	}
	if request.Params.Sort != nil {
		sortField = string(*request.Params.Sort)
	}

	sortOrder := a.config.UI.DAGs.SortOrder
	if sortOrder == "" {
		sortOrder = "asc"
	}
	if request.Params.Order != nil {
		sortOrder = string(*request.Params.Order)
	}

	labelsParam, err := queryLabelsParam(request.Params.Labels, request.Params.Tags)
	if err != nil {
		return nil, err
	}
	pg := exec.NewPaginator(valueOf(request.Params.Page), valueOf(request.Params.PerPage))
	labels := parseCommaSeparatedLabels(labelsParam)
	workspaceFilter, err := a.workspaceFilterForParams(ctx, request.Params.Workspace)
	if err != nil {
		return nil, err
	}
	resp, err := a.listDAGsData(ctx, exec.ListDAGsOptions{
		Paginator:       &pg,
		Name:            valueOf(request.Params.Name),
		Labels:          labels,
		Sort:            sortField,
		Order:           sortOrder,
		WorkspaceFilter: workspaceFilter,
	})
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

func (a *API) GetAllDAGLabels(ctx context.Context, request api.GetAllDAGLabelsRequestObject) (api.GetAllDAGLabelsResponseObject, error) {
	if filter, err := a.workspaceFilterForParams(ctx, request.Params.Workspace); err != nil {
		return nil, err
	} else if filter != nil {
		pg := exec.NewPaginator(1, int(^uint(0)>>1))
		result, errs, err := a.dagStore.List(ctx, exec.ListDAGsOptions{
			Paginator:       &pg,
			WorkspaceFilter: filter,
		})
		if err != nil {
			return nil, fmt.Errorf("error getting labels: %w", err)
		}
		seen := make(map[string]struct{})
		labels := make([]string, 0)
		for _, dag := range result.Items {
			for _, label := range dag.Labels.Strings() {
				if _, ok := seen[label]; ok {
					continue
				}
				seen[label] = struct{}{}
				labels = append(labels, label)
			}
		}
		sort.Strings(labels)
		return &api.GetAllDAGLabels200JSONResponse{
			Labels: labels,
			Errors: errs,
		}, nil
	}
	labels, errs, err := a.dagStore.LabelList(ctx)
	if err != nil {
		return nil, fmt.Errorf("error getting labels: %w", err)
	}
	return &api.GetAllDAGLabels200JSONResponse{
		Labels: labels,
		Errors: errs,
	}, nil
}

func (a *API) GetAllDAGTags(ctx context.Context, request api.GetAllDAGTagsRequestObject) (api.GetAllDAGTagsResponseObject, error) {
	if filter, err := a.workspaceFilterForParams(ctx, request.Params.Workspace); err != nil {
		return nil, err
	} else if filter != nil {
		pg := exec.NewPaginator(1, int(^uint(0)>>1))
		result, errs, err := a.dagStore.List(ctx, exec.ListDAGsOptions{
			Paginator:       &pg,
			WorkspaceFilter: filter,
		})
		if err != nil {
			return nil, fmt.Errorf("error getting labels: %w", err)
		}
		seen := make(map[string]struct{})
		labels := make([]string, 0)
		for _, dag := range result.Items {
			for _, label := range dag.Labels.Strings() {
				if _, ok := seen[label]; ok {
					continue
				}
				seen[label] = struct{}{}
				labels = append(labels, label)
			}
		}
		sort.Strings(labels)
		return &api.GetAllDAGTags200JSONResponse{
			Tags:   labels,
			Errors: errs,
		}, nil
	}
	labels, errs, err := a.dagStore.LabelList(ctx)
	if err != nil {
		return nil, fmt.Errorf("error getting labels: %w", err)
	}
	return &api.GetAllDAGTags200JSONResponse{
		Tags:   labels,
		Errors: errs,
	}, nil
}

func (a *API) GetDAGDAGRunDetails(ctx context.Context, request api.GetDAGDAGRunDetailsRequestObject) (api.GetDAGDAGRunDetailsResponseObject, error) {
	dagFileName := request.FileName
	dagRunId := request.DagRunId

	// Try to get metadata first
	dag, err := a.dagStore.GetMetadata(ctx, dagFileName)
	if err != nil {
		// For DAGs with errors, try to load with AllowBuildErrors
		dag, err = a.dagStore.GetDetails(ctx, dagFileName, spec.WithAllowBuildErrors())
		if err != nil {
			return nil, &Error{
				HTTPStatus: http.StatusNotFound,
				Code:       api.ErrorCodeNotFound,
				Message:    fmt.Sprintf("DAG %s not found", dagFileName),
			}
		}
	}
	if err := a.requireWorkspaceVisible(ctx, dagWorkspaceName(dag)); err != nil {
		return nil, err
	}

	if dagRunId == "latest" {
		attempt, err := a.dagRunStore.LatestAttempt(ctx, dag.Name)
		if err != nil {
			if errors.Is(err, exec.ErrDAGRunIDNotFound) {
				return nil, &Error{
					HTTPStatus: http.StatusNotFound,
					Code:       api.ErrorCodeNotFound,
					Message:    fmt.Sprintf("no dag-runs found for DAG %s", dag.Name),
				}
			}
			return nil, fmt.Errorf("error getting latest attempt: %w", err)
		}

		latestStatus, err := a.dagRunMgr.GetLatestStatus(ctx, dag)
		if err != nil {
			return nil, fmt.Errorf("error getting latest status: %w", err)
		}
		latestStatusPtr := a.repairConfirmedStaleDistributedRunOnRead(ctx, &latestStatus, attempt.ID())
		if latestStatusPtr != nil {
			latestStatus = *latestStatusPtr
		}
		return &api.GetDAGDAGRunDetails200JSONResponse{
			DagRun: a.toDAGRunDetailsWithSpecSource(ctx, attempt, latestStatus),
		}, nil
	}

	attempt, err := a.dagRunStore.FindAttempt(ctx, exec.NewDAGRunRef(dag.Name, dagRunId))
	if err != nil {
		if errors.Is(err, exec.ErrDAGRunIDNotFound) {
			return nil, &Error{
				HTTPStatus: http.StatusNotFound,
				Code:       api.ErrorCodeNotFound,
				Message:    fmt.Sprintf("DAG run %s not found", dagRunId),
			}
		}
		return nil, fmt.Errorf("error getting DAG run attempt: %w", err)
	}

	dagStatus, err := a.dagRunMgr.GetCurrentStatus(ctx, dag, dagRunId)
	if err != nil {
		if errors.Is(err, exec.ErrNoStatusData) {
			return nil, &Error{
				HTTPStatus: http.StatusNotFound,
				Code:       api.ErrorCodeNotFound,
				Message:    fmt.Sprintf("DAG run %s not found", dagRunId),
			}
		}
		return nil, fmt.Errorf("error getting status by dag-run ID: %w", err)
	}

	dagStatus = a.repairConfirmedStaleDistributedRunOnRead(ctx, dagStatus, attempt.ID())

	return &api.GetDAGDAGRunDetails200JSONResponse{
		DagRun: a.toDAGRunDetailsWithSpecSource(ctx, attempt, *dagStatus),
	}, nil
}

func (a *API) ExecuteDAG(ctx context.Context, request api.ExecuteDAGRequestObject) (api.ExecuteDAGResponseObject, error) {
	if err := a.isAllowed(config.PermissionRunDAGs); err != nil {
		return nil, err
	}
	dag, err := a.dagStore.GetDetails(ctx, request.FileName, spec.WithAllowBuildErrors())
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
		return nil, &Error{
			HTTPStatus: http.StatusBadRequest,
			Code:       api.ErrorCodeBadRequest,
			Message:    "request body is required",
		}
	}

	dagRunId := valueOf(request.Body.DagRunId)
	params := valueOf(request.Body.Params)
	singleton := valueOf(request.Body.Singleton)
	nameOverride := strings.TrimSpace(valueOf(request.Body.DagName))

	if err := validateDAGRunID(dagRunId); err != nil {
		return nil, err
	}

	if nameOverride != "" {
		if err := core.ValidateDAGName(nameOverride); err != nil {
			return nil, &Error{
				HTTPStatus: http.StatusBadRequest,
				Code:       api.ErrorCodeBadRequest,
				Message:    err.Error(),
			}
		}
		dag.Name = nameOverride
	}

	if dagRunId == "" {
		var err error
		dagRunId, err = a.dagRunMgr.GenDAGRunID(ctx)
		if err != nil {
			return nil, fmt.Errorf("error generating dag-run ID: %w", err)
		}
	}

	if singleton {
		if err := a.checkSingletonRunning(ctx, dag); err != nil {
			return nil, err
		}
	}

	if err := a.ensureDAGRunIDUnique(ctx, dag, dagRunId); err != nil {
		return nil, err
	}

	labels, err := extractLabelsParam(request.Body.Labels, request.Body.Tags)
	if err != nil {
		return nil, err
	}

	if err := a.startDAGRun(ctx, dag, params, dagRunId, nameOverride, labels); err != nil {
		return nil, fmt.Errorf("error starting dag-run: %w", err)
	}

	detailsMap := map[string]any{
		"dag_name":   request.FileName,
		"dag_run_id": dagRunId,
	}
	if params != "" {
		detailsMap["params"] = params
	}
	a.logAudit(ctx, audit.CategoryDAG, "dag_execute", detailsMap)

	return api.ExecuteDAG200JSONResponse{
		DagRunId: dagRunId,
	}, nil
}

// ExecuteDAGSync executes a DAG and waits for completion before returning.
// It returns the full DAGRunDetails including all node statuses.
func (a *API) ExecuteDAGSync(ctx context.Context, request api.ExecuteDAGSyncRequestObject) (api.ExecuteDAGSyncResponseObject, error) {
	if err := a.isAllowed(config.PermissionRunDAGs); err != nil {
		return nil, err
	}

	if request.Body == nil {
		return nil, &Error{
			HTTPStatus: http.StatusBadRequest,
			Code:       api.ErrorCodeBadRequest,
			Message:    "request body is required",
		}
	}

	dag, err := a.dagStore.GetDetails(ctx, request.FileName, spec.WithAllowBuildErrors())
	if err != nil {
		return nil, &Error{
			HTTPStatus: http.StatusNotFound,
			Code:       api.ErrorCodeNotFound,
			Message:    fmt.Sprintf("DAG %s not found", request.FileName),
		}
	}

	if err := buildErrorsToAPIError(dag.BuildErrors); err != nil {
		return nil, err
	}
	if err := a.requireExecuteForWorkspace(ctx, dagWorkspaceName(dag)); err != nil {
		return nil, err
	}

	dagRunId := valueOf(request.Body.DagRunId)
	params := valueOf(request.Body.Params)
	singleton := valueOf(request.Body.Singleton)
	nameOverride := strings.TrimSpace(valueOf(request.Body.DagName))
	timeout := request.Body.Timeout

	if err := validateDAGRunID(dagRunId); err != nil {
		return nil, err
	}

	if nameOverride != "" {
		if err := core.ValidateDAGName(nameOverride); err != nil {
			return nil, &Error{
				HTTPStatus: http.StatusBadRequest,
				Code:       api.ErrorCodeBadRequest,
				Message:    err.Error(),
			}
		}
		dag.Name = nameOverride
	}

	if dagRunId == "" {
		var err error
		dagRunId, err = a.dagRunMgr.GenDAGRunID(ctx)
		if err != nil {
			return nil, fmt.Errorf("error generating dag-run ID: %w", err)
		}
	}

	if singleton {
		if err := a.checkSingletonRunning(ctx, dag); err != nil {
			return nil, err
		}
	}

	if err := a.ensureDAGRunIDUnique(ctx, dag, dagRunId); err != nil {
		return nil, err
	}

	labels, err := extractLabelsParam(request.Body.Labels, request.Body.Tags)
	if err != nil {
		return nil, err
	}

	if err := a.startDAGRun(ctx, dag, params, dagRunId, nameOverride, labels); err != nil {
		return nil, fmt.Errorf("error starting dag-run: %w", err)
	}

	detailsMap := map[string]any{
		"dag_name":   request.FileName,
		"dag_run_id": dagRunId,
		"timeout":    timeout,
	}
	if params != "" {
		detailsMap["params"] = params
	}
	a.logAudit(ctx, audit.CategoryDAG, "dag_execute_sync", detailsMap)

	dagStatus, err := a.waitForDAGCompletion(ctx, dag, dagRunId, timeout)
	if err != nil {
		// Check if it's a timeout error
		if errors.Is(err, context.DeadlineExceeded) {
			return api.ExecuteDAGSync408JSONResponse{
				Code:     api.ErrorCodeTimeout,
				Message:  fmt.Sprintf("timeout waiting for DAG %s to complete after %d seconds; DAG run continues in background", dag.Name, timeout),
				DagRunId: dagRunId,
			}, nil
		}
		return nil, err
	}

	return api.ExecuteDAGSync200JSONResponse{
		DagRun: ToDAGRunDetails(*dagStatus),
	}, nil
}

// waitForDAGCompletion polls the DAG status until it reaches a terminal state or timeout.
// It returns the final DAGRunStatus or an error if timeout is exceeded.
func (a *API) waitForDAGCompletion(
	ctx context.Context,
	dag *core.DAG,
	dagRunId string,
	timeoutSeconds int,
) (*exec.DAGRunStatus, error) {
	// Create context with timeout
	waitCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSeconds)*time.Second)
	defer cancel()

	// Adaptive polling: start at 100ms, increase to max 2s
	pollInterval := 100 * time.Millisecond
	maxPollInterval := 2 * time.Second

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	var lastStatus *exec.DAGRunStatus

	for {
		select {
		case <-waitCtx.Done():
			// Timeout or context cancelled
			return lastStatus, waitCtx.Err()

		case <-ticker.C:
			status, err := a.readDAGRunStatusForSync(waitCtx, dag, dagRunId)
			if err != nil {
				// Log error but continue polling - DAG might still be initializing
				logger.Debug(waitCtx, "Error getting DAG status during wait", tag.Error(err))
				continue
			}

			if status == nil {
				continue
			}

			lastStatus = status

			// Check if execution is complete (not active) or waiting for human approval.
			// We return on "waiting" status because approval workflows
			// require external intervention that would cause indefinite blocking otherwise.
			// The client can poll the status endpoint or use callbacks to resume monitoring.
			if !status.Status.IsActive() || status.Status.IsWaiting() {
				return status, nil
			}

			// Adaptive polling: increase interval up to max
			if pollInterval < maxPollInterval {
				pollInterval = min(time.Duration(float64(pollInterval)*1.5), maxPollInterval)
				ticker.Reset(pollInterval)
			}
		}
	}
}

func (a *API) readDAGRunStatusForSync(ctx context.Context, dag *core.DAG, dagRunID string) (*exec.DAGRunStatus, error) {
	attempt, err := a.dagRunStore.FindAttempt(ctx, exec.NewDAGRunRef(dag.Name, dagRunID))
	if err != nil {
		return nil, fmt.Errorf("failed to find dag-run attempt: %w", err)
	}
	status, err := attempt.ReadStatus(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read dag-run status: %w", err)
	}
	return status, nil
}

func (a *API) startDAGRun(ctx context.Context, dag *core.DAG, params, dagRunID, nameOverride, labels string) error {
	return a.startDAGRunWithOptions(ctx, dag, startDAGRunOptions{
		params:       params,
		dagRunID:     dagRunID,
		nameOverride: nameOverride,
		triggerType:  core.TriggerTypeManual,
		labels:       labels,
	})
}

// extractLabelsParam validates and serializes an optional labels array into a comma-separated string.
func extractLabelsParam(labels, deprecatedTags *[]string) (string, error) {
	if labels != nil && deprecatedTags != nil && len(*labels) > 0 && len(*deprecatedTags) > 0 {
		return "", &Error{
			HTTPStatus: http.StatusBadRequest,
			Code:       api.ErrorCodeBadRequest,
			Message:    "labels and deprecated tags cannot both be set",
		}
	}
	if (labels == nil || len(*labels) == 0) && deprecatedTags != nil {
		labels = deprecatedTags
	}
	if labels == nil || len(*labels) == 0 {
		return "", nil
	}
	parsed := core.NewLabels(*labels)
	if err := core.ValidateLabels(parsed); err != nil {
		return "", &Error{
			HTTPStatus: http.StatusBadRequest,
			Code:       api.ErrorCodeBadRequest,
			Message:    fmt.Sprintf("invalid labels: %s", err.Error()),
		}
	}
	return strings.Join(parsed.Strings(), ","), nil
}

// buildErrorsToAPIError returns an API error if the DAG has build errors, nil otherwise.
func buildErrorsToAPIError(buildErrors []error) *Error {
	if len(buildErrors) == 0 {
		return nil
	}
	return &Error{
		HTTPStatus: http.StatusBadRequest,
		Code:       api.ErrorCodeBadRequest,
		Message:    strings.Join(extractBuildErrors(buildErrors), "; "),
	}
}

// validateDAGRunID checks that a user-provided dagRunID contains only safe characters.
// Skips validation if the ID is empty (will be auto-generated).
func validateDAGRunID(dagRunID string) error {
	if dagRunID == "" {
		return nil
	}
	if err := exec.ValidateDAGRunID(dagRunID); err != nil {
		return &Error{
			HTTPStatus: http.StatusBadRequest,
			Code:       api.ErrorCodeBadRequest,
			Message:    err.Error(),
		}
	}
	return nil
}

// ensureDAGRunIDUnique validates that the given dagRunID is not already in use for this DAG.
func (a *API) ensureDAGRunIDUnique(ctx context.Context, dag *core.DAG, dagRunID string) error {
	if dagRunID == "" {
		return fmt.Errorf("dagRunID must be non-empty")
	}
	if _, err := a.dagRunStore.FindAttempt(ctx, exec.NewDAGRunRef(dag.Name, dagRunID)); err == nil {
		return &Error{
			HTTPStatus: http.StatusConflict,
			Code:       api.ErrorCodeAlreadyExists,
			Message:    fmt.Sprintf("dag-run ID %s already exists for DAG %s", dagRunID, dag.Name),
		}
	} else if !errors.Is(err, exec.ErrDAGRunIDNotFound) {
		return fmt.Errorf("failed to verify dag-run ID uniqueness: %w", err)
	}
	return nil
}

type startDAGRunOptions struct {
	params       string
	dagRunID     string
	nameOverride string
	fromRunID    string
	target       string
	triggerType  core.TriggerType
	labels       string
}

// waitForDAGStatusChange waits until the DAG status transitions from NotStarted.
// Returns true if the status changed, false if timeout or context cancelled.
func (a *API) waitForDAGStatusChange(ctx context.Context, dag *core.DAG, dagRunID string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	pollInterval := 100 * time.Millisecond

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return false
		default:
			status, _ := a.dagRunMgr.GetCurrentStatus(ctx, dag, dagRunID)
			if status != nil && status.Status != core.NotStarted {
				return true
			}
			time.Sleep(pollInterval)
		}
	}
	return false
}

// dispatchStartToCoordinator dispatches a DAG start operation to the coordinator
// and waits for the DAG status to change from NotStarted within the given timeout.
func (a *API) dispatchStartToCoordinator(ctx context.Context, dag *core.DAG, dagRunID string, timeout time.Duration, params, labels string) error {
	var taskOpts []executor.TaskOption
	if len(dag.WorkerSelector) > 0 {
		taskOpts = append(taskOpts, executor.WithWorkerSelector(dag.WorkerSelector))
	}
	if params != "" {
		taskOpts = append(taskOpts, executor.WithTaskParams(params))
	}
	if labels != "" {
		taskOpts = append(taskOpts, executor.WithLabels(labels))
	}
	taskOpts = append(taskOpts, executor.WithBaseConfig(executor.ResolveBaseConfig(dag.BaseConfigData, a.config.Paths.BaseConfig)))
	if dag.SourceFile != "" {
		taskOpts = append(taskOpts, executor.WithSourceFile(dag.SourceFile))
	}
	if snapshot, err := agentsnapshot.BuildFromPaths(ctx, dag, a.config.Paths, a.dagStore); err != nil {
		return fmt.Errorf("build distributed agent snapshot: %w", err)
	} else if len(snapshot) > 0 {
		taskOpts = append(taskOpts, executor.WithAgentSnapshot(snapshot))
	}

	task := executor.CreateTask(
		dag.Name,
		string(dag.YamlData),
		coordinatorv1.Operation_OPERATION_START,
		dagRunID,
		taskOpts...,
	)

	if err := a.coordinatorCli.Dispatch(ctx, task); err != nil {
		return fmt.Errorf("error dispatching to coordinator: %w", err)
	}

	if !a.waitForDAGStatusChange(ctx, dag, dagRunID, timeout) {
		return &Error{
			HTTPStatus: http.StatusInternalServerError,
			Code:       api.ErrorCodeInternalError,
			Message:    "DAG did not start after coordinator dispatch",
		}
	}

	return nil
}

func (a *API) startDAGRunWithOptions(ctx context.Context, dag *core.DAG, opts startDAGRunOptions) error {
	resolvedDAG, err := spec.ResolveRuntimeParams(ctx, dag, opts.params, spec.ResolveRuntimeParamsOptions{
		BaseConfig: a.config.Paths.BaseConfig,
	})
	if err != nil {
		return &Error{
			HTTPStatus: http.StatusBadRequest,
			Code:       api.ErrorCodeBadRequest,
			Message:    err.Error(),
		}
	}
	dag = resolvedDAG

	if err := buildErrorsToAPIError(dag.BuildErrors); err != nil {
		return err
	}

	if err := core.ValidateStartParams(dag.DefaultParams, core.StartParamInput{
		RawParams: opts.params,
	}); err != nil {
		return &Error{
			HTTPStatus: http.StatusBadRequest,
			Code:       api.ErrorCodeBadRequest,
			Message:    err.Error(),
		}
	}

	return a.startPreparedDAGRunWithOptions(ctx, dag, opts, opts.params)
}

func (a *API) startPreparedDAGRunWithOptions(
	ctx context.Context,
	dag *core.DAG,
	opts startDAGRunOptions,
	dispatchParams string,
) error {
	// Check if this DAG should be dispatched to the coordinator for distributed execution
	if core.ShouldDispatchToCoordinator(dag, a.coordinatorCli != nil, a.defaultExecMode) {
		timeout := 10 * time.Second
		if osrt.GOOS == "windows" {
			timeout = 20 * time.Second
		}
		return a.dispatchStartToCoordinator(ctx, dag, opts.dagRunID, timeout, dispatchParams, opts.labels)
	}

	// Only pass trigger type if it's a known value (not TriggerTypeUnknown)
	triggerTypeStr := ""
	if opts.triggerType != core.TriggerTypeUnknown {
		triggerTypeStr = opts.triggerType.String()
	}
	target := opts.target
	fromRunID := opts.fromRunID
	if fromRunID != "" && dispatchParams != "" && len(dag.YamlData) > 0 {
		if dag.Location == "" || fileMissing(dag.Location) {
			tempPath, err := writeInlineRescheduleSpec(dag.Name, opts.dagRunID, dag.YamlData)
			if err != nil {
				return fmt.Errorf("error preparing inline dag snapshot: %w", err)
			}
			dag.Location = tempPath
			target = tempPath
			fromRunID = ""
		}
	}
	spec := a.subCmdBuilder.Start(dag, runtime.StartOptions{
		Params:       dispatchParams,
		DAGRunID:     opts.dagRunID,
		Quiet:        true,
		NameOverride: opts.nameOverride,
		FromRunID:    fromRunID,
		Target:       target,
		TriggerType:  triggerTypeStr,
		Labels:       opts.labels,
	})

	if err := runtime.Start(ctx, spec); err != nil {
		return fmt.Errorf("error starting DAG: %w", err)
	}

	timeout := 10 * time.Second
	if osrt.GOOS == "windows" {
		timeout = 20 * time.Second
	}

	if !a.waitForDAGStatusChange(ctx, dag, opts.dagRunID, timeout) {
		return &Error{
			HTTPStatus: http.StatusInternalServerError,
			Code:       api.ErrorCodeInternalError,
			Message:    "DAG did not start",
		}
	}

	return nil
}

func writeInlineRescheduleSpec(name, dagRunID string, data []byte) (string, error) {
	tmpDir := filepath.Join(os.TempDir(), name, dagRunID)
	if err := os.MkdirAll(tmpDir, 0o750); err != nil {
		return "", err
	}

	path := filepath.Join(tmpDir, fmt.Sprintf("%s.yaml", name))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", err
	}

	return path, nil
}

func fileMissing(path string) bool {
	if path == "" {
		return true
	}
	_, err := os.Stat(path)
	return errors.Is(err, os.ErrNotExist)
}

func (a *API) EnqueueDAGDAGRun(ctx context.Context, request api.EnqueueDAGDAGRunRequestObject) (api.EnqueueDAGDAGRunResponseObject, error) {
	if err := a.isAllowed(config.PermissionRunDAGs); err != nil {
		return nil, err
	}

	dag, err := a.dagStore.GetDetails(ctx, request.FileName, spec.WithAllowBuildErrors(), spec.WithoutEval())
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
		return nil, &Error{
			HTTPStatus: http.StatusBadRequest,
			Code:       api.ErrorCodeBadRequest,
			Message:    "request body is required",
		}
	}

	if request.Body.Queue != nil && *request.Body.Queue != "" {
		dag.Queue = *request.Body.Queue
	}

	nameOverride := strings.TrimSpace(valueOf(request.Body.DagName))
	if nameOverride != "" {
		if err := core.ValidateDAGName(nameOverride); err != nil {
			return nil, &Error{
				HTTPStatus: http.StatusBadRequest,
				Code:       api.ErrorCodeBadRequest,
				Message:    err.Error(),
			}
		}
		dag.Name = nameOverride
	}

	dagRunId := valueOf(request.Body.DagRunId)
	if err := validateDAGRunID(dagRunId); err != nil {
		return nil, err
	}
	if dagRunId == "" {
		var err error
		dagRunId, err = a.dagRunMgr.GenDAGRunID(ctx)
		if err != nil {
			return nil, fmt.Errorf("error generating dag-run ID: %w", err)
		}
	}

	singleton := valueOf(request.Body.Singleton)
	if singleton {
		if err := a.checkSingletonRunning(ctx, dag); err != nil {
			return nil, err
		}
		if err := a.checkSingletonQueued(ctx, dag); err != nil {
			return nil, err
		}
	}

	labels, err := extractLabelsParam(request.Body.Labels, request.Body.Tags)
	if err != nil {
		return nil, err
	}

	if err := a.enqueueDAGRun(ctx, dag, valueOf(request.Body.Params), dagRunId, nameOverride, core.TriggerTypeManual, labels); err != nil {
		return nil, fmt.Errorf("error enqueuing dag-run: %w", err)
	}

	enqueueDetails := map[string]any{
		"dag_name":   request.FileName,
		"dag_run_id": dagRunId,
	}
	if request.Body.Params != nil && *request.Body.Params != "" {
		enqueueDetails["params"] = *request.Body.Params
	}
	a.logAudit(ctx, audit.CategoryDAG, "dag_enqueue", enqueueDetails)

	return api.EnqueueDAGDAGRun200JSONResponse{
		DagRunId: dagRunId,
	}, nil
}

func (a *API) enqueueDAGRun(ctx context.Context, dag *core.DAG, params, dagRunID, nameOverride string, triggerType core.TriggerType, labels string) error {
	resolvedDAG, err := spec.ResolveRuntimeParams(ctx, dag, params, spec.ResolveRuntimeParamsOptions{
		BaseConfig: a.config.Paths.BaseConfig,
	})
	if err != nil {
		return &Error{
			HTTPStatus: http.StatusBadRequest,
			Code:       api.ErrorCodeBadRequest,
			Message:    err.Error(),
		}
	}
	dag = resolvedDAG

	if err := buildErrorsToAPIError(dag.BuildErrors); err != nil {
		return err
	}

	if err := core.ValidateStartParams(dag.DefaultParams, core.StartParamInput{
		RawParams: params,
	}); err != nil {
		return &Error{
			HTTPStatus: http.StatusBadRequest,
			Code:       api.ErrorCodeBadRequest,
			Message:    err.Error(),
		}
	}

	// Only pass trigger type if it's a known value (not TriggerTypeUnknown)
	triggerTypeStr := ""
	if triggerType != core.TriggerTypeUnknown {
		triggerTypeStr = triggerType.String()
	}
	opts := runtime.EnqueueOptions{
		Params:       params,
		DAGRunID:     dagRunID,
		NameOverride: nameOverride,
		TriggerType:  triggerTypeStr,
		Labels:       labels,
	}
	if dag.Queue != "" {
		opts.Queue = dag.Queue
	}

	spec := a.subCmdBuilder.Enqueue(dag, opts)
	if err := runtime.Run(ctx, spec); err != nil {
		return fmt.Errorf("error enqueuing DAG: %w", err)
	}

	if !a.waitForDAGStatusChange(ctx, dag, dagRunID, 3*time.Second) {
		return &Error{
			HTTPStatus: http.StatusInternalServerError,
			Code:       api.ErrorCodeInternalError,
			Message:    "Failed to enqueue dagRun execution",
		}
	}

	return nil
}

func (a *API) UpdateDAGSuspensionState(ctx context.Context, request api.UpdateDAGSuspensionStateRequestObject) (api.UpdateDAGSuspensionStateResponseObject, error) {
	if err := a.isAllowed(config.PermissionRunDAGs); err != nil {
		return nil, err
	}

	dag, err := a.dagStore.GetMetadata(ctx, request.FileName)
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

	if err := a.dagStore.ToggleSuspend(ctx, request.FileName, request.Body.Suspend); err != nil {
		return nil, fmt.Errorf("error toggling suspend: %w", err)
	}

	action := "dag_suspend"
	if !request.Body.Suspend {
		action = "dag_resume"
	}
	a.logAudit(ctx, audit.CategoryDAG, action, map[string]any{
		"dag_name":  request.FileName,
		"suspended": request.Body.Suspend,
	})
	a.notifyDAGMutation(request.FileName)

	return api.UpdateDAGSuspensionState200Response{}, nil
}

func (a *API) SearchDAGs(ctx context.Context, request api.SearchDAGsRequestObject) (api.SearchDAGsResponseObject, error) {
	ret, errs, err := a.dagStore.Grep(ctx, request.Params.Q)
	if err != nil {
		return nil, fmt.Errorf("error searching DAGs: %w", err)
	}

	var results []api.SearchResultItem
	for _, item := range ret {
		if !a.canAccessWorkspace(ctx, dagWorkspaceName(item.DAG)) {
			continue
		}
		var matches []api.SearchMatchItem
		for _, match := range item.Matches {
			matches = append(matches, api.SearchMatchItem{
				Line:       match.Line,
				LineNumber: match.LineNumber,
				StartLine:  match.StartLine,
			})
		}

		results = append(results, api.SearchResultItem{
			Name:    item.Name,
			Dag:     toDAG(item.DAG),
			Matches: matches,
		})
	}

	return &api.SearchDAGs200JSONResponse{
		Results: results,
		Errors:  errs,
	}, nil
}

func (a *API) StopAllDAGRuns(ctx context.Context, request api.StopAllDAGRunsRequestObject) (api.StopAllDAGRunsResponseObject, error) {
	if err := a.isAllowed(config.PermissionRunDAGs); err != nil {
		return nil, err
	}

	// Get the DAG metadata to ensure it exists
	dag, err := a.dagStore.GetMetadata(ctx, request.FileName)
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

	// Get all running DAG-runs for this DAG
	runningStatuses, err := a.dagRunStore.ListStatuses(ctx,
		exec.WithExactName(dag.Name),
		exec.WithStatuses([]core.Status{core.Running}),
	)
	if err != nil {
		return nil, fmt.Errorf("error listing running DAG-runs: %w", err)
	}

	// Stop each running DAG-run
	var stopErrors []string
	var stoppedRunIDs []string
	for _, runningStatus := range runningStatuses {
		runID := runningStatus.DAGRunID
		stopErr := a.dagRunMgr.Stop(ctx, dag, runID)
		if stopErr != nil {
			stopErrors = append(stopErrors, fmt.Sprintf("failed to stop run %q: %s", runID, stopErr))
		} else {
			stoppedRunIDs = append(stoppedRunIDs, runID)
		}
		if ctx.Err() != nil {
			stopErrors = append(stopErrors, fmt.Sprintf("context is cancelled: %s", ctx.Err()))
			break
		}
	}

	if len(stoppedRunIDs) > 0 {
		a.logAudit(ctx, audit.CategoryDAG, "dag_stop_all", map[string]any{
			"dag_name":        request.FileName,
			"stopped_run_ids": stoppedRunIDs,
			"count":           len(stoppedRunIDs),
		})
	}

	return &api.StopAllDAGRuns200JSONResponse{
		Errors: stopErrors,
	}, nil
}

// SSE Data Methods for DAGs

// GetDAGDetailsData returns DAG details for SSE.
// Identifier format: "fileName"
func (a *API) GetDAGDetailsData(ctx context.Context, fileName string) (any, error) {
	return a.getDAGDetailsData(ctx, fileName)
}

// GetDAGHistoryData returns DAG execution history for SSE.
// Identifier format: "fileName"
func (a *API) GetDAGHistoryData(ctx context.Context, fileName string) (any, error) {
	return withDAGRunReadTimeout(ctx, dagRunReadRequestInfo{
		endpoint: "/dags/{fileName}/dag-runs",
		dagName:  fileName,
	}, func(readCtx context.Context) (api.GetDAGDAGRunHistory200JSONResponse, error) {
		dag, err := a.dagStore.GetDetails(readCtx, fileName, spec.WithAllowBuildErrors())
		if err != nil {
			return api.GetDAGDAGRunHistory200JSONResponse{}, err
		}
		if err := a.requireWorkspaceVisible(readCtx, dagWorkspaceName(dag)); err != nil {
			return api.GetDAGDAGRunHistory200JSONResponse{}, err
		}

		dagName := a.resolveDAGName(readCtx, fileName)
		recentHistory := a.dagRunMgr.ListRecentStatus(readCtx, dagName, defaultHistoryLimit)

		var dagRuns []api.DAGRunDetails
		for _, status := range recentHistory {
			dagRuns = append(dagRuns, ToDAGRunDetails(status))
		}

		gridData := a.readHistoryData(readCtx, dag, recentHistory)
		return api.GetDAGDAGRunHistory200JSONResponse{
			DagRuns:  dagRuns,
			GridData: gridData,
		}, nil
	})
}

// GetDAGsListData returns DAGs list for SSE.
// Identifier format: URL query string (e.g., "page=1&perPage=100&name=mydag")
func (a *API) GetDAGsListData(ctx context.Context, queryString string) (any, error) {
	return withDAGRunReadTimeout(ctx, dagRunReadRequestInfo{
		endpoint: "/dags",
	}, func(readCtx context.Context) (any, error) {
		params, err := url.ParseQuery(queryString)
		if err != nil {
			logger.Warn(readCtx, "Failed to parse query string for DAGs list",
				tag.Error(err),
				slog.String("queryString", queryString),
			)
		}

		page := parseIntParam(params.Get("page"), 1)
		perPage := parseIntParam(params.Get("perPage"), 100)

		sortField := a.config.UI.DAGs.SortField
		if sortField == "" {
			sortField = "name"
		}
		if rawSort := params.Get("sort"); rawSort != "" {
			sortField = rawSort
		}
		sortOrder := a.config.UI.DAGs.SortOrder
		if sortOrder == "" {
			sortOrder = "asc"
		}
		if rawOrder := params.Get("order"); rawOrder != "" {
			sortOrder = rawOrder
		}

		var labelsParam, deprecatedTagsParam *string
		if rawLabels := params.Get("labels"); rawLabels != "" {
			labelsParam = &rawLabels
		}
		if rawTags := params.Get("tags"); rawTags != "" {
			deprecatedTagsParam = &rawTags
		}
		labelQueryParam, labelErr := queryLabelsParam(labelsParam, deprecatedTagsParam)
		if labelErr != nil {
			return nil, labelErr
		}
		labels := parseCommaSeparatedLabels(labelQueryParam)

		pg := exec.NewPaginator(page, perPage)
		workspaceParam := workspaceParamFromValues(params)
		workspaceFilter, err := a.workspaceFilterForParams(readCtx, workspaceParam)
		if err != nil {
			return nil, err
		}
		listOpts := exec.ListDAGsOptions{
			Paginator:       &pg,
			Name:            params.Get("name"),
			Labels:          labels,
			Sort:            sortField,
			Order:           sortOrder,
			WorkspaceFilter: workspaceFilter,
		}

		return a.listDAGsData(readCtx, listOpts)
	})
}

func (a *API) listDAGsData(ctx context.Context, listOpts exec.ListDAGsOptions) (api.ListDAGs200JSONResponse, error) {
	projectionTime := time.Now()
	nextRunProjection := a.nextRunProjection(ctx)

	listOpts.Time = &projectionTime
	listOpts.NextRunProjection = nextRunProjection

	result, errList, err := a.dagStore.List(ctx, listOpts)
	if err != nil {
		return api.ListDAGs200JSONResponse{}, fmt.Errorf("error listing DAGs: %w", err)
	}

	dagFiles := make([]api.DAGFile, 0, len(result.Items))
	for _, item := range result.Items {
		dagStatus, statusErr := a.dagRunMgr.GetLatestStatus(ctx, item)
		nextRun := nextRunProjection(item, projectionTime)
		var nextRunAt *time.Time
		if !nextRun.IsZero() {
			nextRunAt = &nextRun
		}

		dagFile := api.DAGFile{
			FileName:     item.FileName(),
			FilePath:     ptrOf(item.Location),
			LatestDAGRun: toDAGRunSummary(dagStatus),
			Suspended:    a.dagStore.IsSuspended(ctx, item.FileName()),
			Dag:          toDAG(item),
			NextRun:      nextRunAt,
			Errors:       extractBuildErrors(item.BuildErrors),
		}
		if statusErr != nil {
			errList = append(errList, statusErr.Error())
		}
		dagFiles = append(dagFiles, dagFile)
	}

	return api.ListDAGs200JSONResponse{
		Dags:       dagFiles,
		Errors:     errList,
		Pagination: toPagination(result),
	}, nil
}

func (a *API) projectNextRun(ctx context.Context, dag *core.DAG) *time.Time {
	nextRun := a.nextRunProjection(ctx)(dag, time.Now())
	if nextRun.IsZero() {
		return nil
	}
	return &nextRun
}

func (a *API) nextRunProjection(ctx context.Context) func(*core.DAG, time.Time) time.Time {
	var schedulerState *scheduler.SchedulerState
	if a.schedulerStateStore != nil {
		state, loadErr := a.schedulerStateStore.Load(ctx)
		if loadErr != nil {
			logger.Warn(ctx, "Failed to load scheduler state for DAG next-run projection", tag.Error(loadErr))
		} else {
			schedulerState = state
		}
	}

	return func(dag *core.DAG, now time.Time) time.Time {
		return scheduler.NextPlannedRun(dag, now, schedulerState)
	}
}

// parseIntParam parses an integer string, returning defaultVal if parsing fails or value is <= 0.
func parseIntParam(s string, defaultVal int) int {
	if s == "" {
		return defaultVal
	}
	if v, err := strconv.Atoi(s); err == nil && v > 0 {
		return v
	}
	return defaultVal
}
