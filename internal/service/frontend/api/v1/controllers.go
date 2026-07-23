// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	api "github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/controller"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/service/audit"
	"github.com/dagucloud/dagu/internal/service/controllerapi"
	"github.com/dagucloud/dagu/internal/service/frontend/api/pathutil"
)

type controllerService interface {
	Create(ctx context.Context, data []byte) (*controller.Detail, error)
	ListVisible(ctx context.Context, include func(controller.Definition) bool) ([]controller.Summary, error)
	GetDefinition(ctx context.Context, id string) (*controller.Definition, error)
	Get(ctx context.Context, id string) (*controller.Detail, error)
	Update(ctx context.Context, id string, data []byte) (*controller.Detail, error)
	Delete(ctx context.Context, id string) error
	Start(ctx context.Context, id, prompt string) (controller.RuntimeView, error)
	Prompt(ctx context.Context, id, prompt string) (controller.RuntimeView, error)
	Stop(ctx context.Context, id string) (controller.RuntimeView, error)
}

func newControllerService(dataDir string, dagStore exec.DAGStore) controllerService {
	stores := controller.NewFileStores(dataDir)
	validator := controller.NewValidator(controller.NewDAGStoreResolver(dagStore))
	return controller.NewService(stores.Definitions, stores.Runtimes, stores.Locker, validator)
}

// ListControllers returns the Controllers visible in the requested workspace scope.
func (a *API) ListControllers(ctx context.Context, request api.ListControllersRequestObject) (api.ListControllersResponseObject, error) {
	filter, err := a.workspaceFilterForParams(ctx, request.Params.Workspace)
	if err != nil {
		return nil, err
	}
	items, err := a.controllerService.ListVisible(ctx, func(definition controller.Definition) bool {
		return filter == nil || filter.MatchesLabels(core.NewLabels(definition.Labels))
	})
	if err != nil {
		return nil, controllerServiceError(err)
	}
	controllers := make([]api.ControllerSummary, 0, len(items))
	for _, item := range items {
		summary := controllerapi.Summary(item)
		if err := a.hydrateControllerListDAGRun(ctx, summary.LatestDAGRun); err != nil {
			return nil, err
		}
		controllers = append(controllers, summary)
	}
	return api.ListControllers200JSONResponse{Controllers: controllers}, nil
}

func (a *API) hydrateControllerListDAGRun(ctx context.Context, run *api.ControllerListDAGRun) error {
	if run == nil {
		return nil
	}
	summary, err := a.loadControllerDAGRunSummary(ctx, run.Dag, run.DagRunId)
	if err != nil {
		return err
	}
	if summary == nil {
		return nil
	}
	run.Status = &summary.Status
	run.StatusLabel = &summary.StatusLabel
	return nil
}

func (a *API) loadControllerDAGRunSummary(ctx context.Context, dag, dagRunID string) (*api.DAGRunSummary, error) {
	if a.dagRunStore == nil {
		return nil, nil
	}
	attempt, err := a.dagRunStore.FindAttempt(ctx, exec.NewDAGRunRef(dag, dagRunID))
	if err != nil {
		if isDAGRunLookupNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("find Controller DAG run %s/%s: %w", dag, dagRunID, err)
	}
	status, err := attempt.ReadStatus(ctx)
	if err != nil {
		if isDAGRunLookupNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read Controller DAG run %s/%s: %w", dag, dagRunID, err)
	}
	if status == nil {
		return nil, nil
	}
	summary := toDAGRunSummary(*status)
	summary.Params = nil
	return &summary, nil
}

// CreateController validates and persists an ID-less Controller specification.
func (a *API) CreateController(ctx context.Context, request api.CreateControllerRequestObject) (api.CreateControllerResponseObject, error) {
	if request.Body == nil {
		return nil, ErrInvalidRequestBody
	}
	draft, err := controller.ParseCreateDefinition([]byte(request.Body.Spec))
	if err != nil {
		return nil, controllerServiceError(err)
	}
	if err := a.requireControllerWriteForWorkspace(ctx, draft.Workspace()); err != nil {
		return nil, err
	}
	detail, err := a.controllerService.Create(ctx, []byte(request.Body.Spec))
	if err != nil {
		return nil, controllerServiceError(err)
	}
	if detail == nil {
		return nil, errors.New("controller service returned no detail")
	}
	a.logControllerAudit(ctx, "create", detail.Definition.ID, detail.Definition.Workspace())
	return api.CreateController201JSONResponse{
		Body: api.ControllerCreateResponse{Id: detail.Definition.ID},
		Headers: api.CreateController201ResponseHeaders{
			Location: pathutil.BuildPublicEndpointPath(
				a.evaluateMountedAPIPath(ctx),
				"controllers/"+detail.Definition.ID,
			),
		},
	}, nil
}

// GetController returns a Controller definition and current-or-last runtime snapshot.
func (a *API) GetController(ctx context.Context, request api.GetControllerRequestObject) (api.GetControllerResponseObject, error) {
	detail, err := a.getVisibleController(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	response, err := a.controllerDetailResponse(ctx, detail)
	if err != nil {
		return nil, err
	}
	return api.GetController200JSONResponse(response), nil
}

// UpdateControllerSpec replaces an inactive Controller definition.
func (a *API) UpdateControllerSpec(ctx context.Context, request api.UpdateControllerSpecRequestObject) (api.UpdateControllerSpecResponseObject, error) {
	if request.Body == nil {
		return nil, ErrInvalidRequestBody
	}
	current, err := a.getVisibleControllerDefinition(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	if err := a.requireControllerWriteForWorkspace(ctx, current.Workspace()); err != nil {
		return nil, err
	}
	next, err := controller.ParseDefinition([]byte(request.Body.Spec))
	if err != nil {
		return nil, controllerServiceError(err)
	}
	if next.ID != request.Id || next.Workspace() != current.Workspace() {
		return nil, &Error{
			HTTPStatus: http.StatusConflict,
			Code:       api.ErrorCodeConflict,
			Message:    "Controller ID and workspace are immutable",
		}
	}
	detail, err := a.controllerService.Update(ctx, request.Id, []byte(request.Body.Spec))
	if err != nil {
		return nil, controllerServiceError(err)
	}
	if detail == nil {
		return nil, errors.New("controller service returned no detail")
	}
	a.logControllerAudit(ctx, "update", request.Id, detail.Definition.Workspace())
	response, err := a.controllerDetailResponse(ctx, detail)
	if err != nil {
		return nil, err
	}
	return api.UpdateControllerSpec200JSONResponse(response), nil
}

// DeleteController removes an inactive Controller definition and runtime snapshot.
func (a *API) DeleteController(ctx context.Context, request api.DeleteControllerRequestObject) (api.DeleteControllerResponseObject, error) {
	definition, err := a.getVisibleControllerDefinition(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	workspaceName := definition.Workspace()
	if err := a.requireControllerWriteForWorkspace(ctx, workspaceName); err != nil {
		return nil, err
	}
	if err := a.controllerService.Delete(ctx, request.Id); err != nil {
		return nil, controllerServiceError(err)
	}
	a.logControllerAudit(ctx, "delete", request.Id, workspaceName)
	return api.DeleteController204Response{}, nil
}

// StartController accepts a new Controller execution prompt.
func (a *API) StartController(ctx context.Context, request api.StartControllerRequestObject) (api.StartControllerResponseObject, error) {
	if request.Body == nil {
		return nil, ErrInvalidRequestBody
	}
	definition, err := a.getExecutableControllerDefinition(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	if _, err := a.controllerService.Start(ctx, request.Id, request.Body.Prompt); err != nil {
		return nil, controllerServiceError(err)
	}
	a.logControllerAudit(ctx, "start", request.Id, definition.Workspace())
	return api.StartController202Response{}, nil
}

// PromptController accepts a prompt for a Controller waiting on user input.
func (a *API) PromptController(ctx context.Context, request api.PromptControllerRequestObject) (api.PromptControllerResponseObject, error) {
	if request.Body == nil {
		return nil, ErrInvalidRequestBody
	}
	definition, err := a.getExecutableControllerDefinition(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	if _, err := a.controllerService.Prompt(ctx, request.Id, request.Body.Prompt); err != nil {
		return nil, controllerServiceError(err)
	}
	a.logControllerAudit(ctx, "prompt", request.Id, definition.Workspace())
	return api.PromptController202Response{}, nil
}

// StopController accepts a Controller stop request.
func (a *API) StopController(ctx context.Context, request api.StopControllerRequestObject) (api.StopControllerResponseObject, error) {
	definition, err := a.getExecutableControllerDefinition(ctx, request.Id)
	if err != nil {
		return nil, err
	}
	if _, err := a.controllerService.Stop(ctx, request.Id); err != nil {
		return nil, controllerServiceError(err)
	}
	a.logControllerAudit(ctx, "stop", request.Id, definition.Workspace())
	return api.StopController202Response{}, nil
}

func (a *API) getVisibleController(ctx context.Context, id string) (*controller.Detail, error) {
	// Authorize from the definition before reading runtime data so hidden
	// workspaces remain masked even when their runtime snapshot is corrupt.
	if _, err := a.getVisibleControllerDefinition(ctx, id); err != nil {
		return nil, err
	}
	detail, err := a.controllerService.Get(ctx, id)
	if err != nil {
		return nil, controllerServiceError(err)
	}
	return detail, nil
}

func (a *API) getVisibleControllerDefinition(ctx context.Context, id string) (*controller.Definition, error) {
	definition, err := a.controllerService.GetDefinition(ctx, id)
	if err != nil {
		if errors.Is(err, controller.ErrDefinitionCorrupt) {
			return nil, &Error{HTTPStatus: http.StatusNotFound, Code: api.ErrorCodeNotFound, Message: "Controller not found"}
		}
		return nil, controllerServiceError(err)
	}
	if definition == nil {
		return nil, errors.New("controller service returned no definition")
	}
	if err := a.requireWorkspaceVisible(ctx, definition.Workspace()); err != nil {
		return nil, err
	}
	return definition, nil
}

func (a *API) getExecutableControllerDefinition(ctx context.Context, id string) (*controller.Definition, error) {
	definition, err := a.getVisibleControllerDefinition(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := a.isAllowed(config.PermissionRunDAGs); err != nil {
		return nil, err
	}
	if err := a.requireExecuteForWorkspace(ctx, definition.Workspace()); err != nil {
		return nil, err
	}
	return definition, nil
}

func (a *API) requireControllerWriteForWorkspace(ctx context.Context, workspaceName string) error {
	return a.requireDAGWriteForWorkspace(ctx, workspaceName)
}

func (a *API) logControllerAudit(ctx context.Context, action, id, workspaceName string) {
	a.LogAudit(ctx, audit.Category("controller"), action, map[string]any{
		"controller_id": id,
		"resource_type": "controller",
		"resource_id":   id,
		"workspace":     workspaceName,
	})
}

func (a *API) controllerDetailResponse(ctx context.Context, detail *controller.Detail) (api.ControllerDetail, error) {
	if detail == nil {
		return api.ControllerDetail{}, errors.New("controller service returned no detail")
	}
	response := controllerapi.Detail(*detail)
	if detail.Runtime == nil {
		return response, nil
	}

	for _, ref := range detail.Runtime.DAGRunRefs {
		summary, err := a.loadControllerDAGRunSummary(ctx, ref.DAG, ref.DAGRunID)
		if err != nil {
			return api.ControllerDetail{}, err
		}
		if summary == nil {
			continue
		}
		response.DagRuns = append(response.DagRuns, *summary)
	}
	return response, nil
}

func controllerServiceError(err error) error {
	if err == nil {
		return nil
	}
	var existing *Error
	if errors.As(err, &existing) {
		return existing
	}
	var validationErr *controller.ValidationError
	if errors.As(err, &validationErr) {
		return &Error{
			HTTPStatus: http.StatusBadRequest,
			Code:       api.ErrorCodeControllerValidationFailed,
			Message:    validationErr.Error(),
			Details: map[string]any{
				"errors": validationErr.Issues,
			},
		}
	}
	switch {
	case errors.Is(err, controller.ErrNotFound):
		return &Error{HTTPStatus: http.StatusNotFound, Code: api.ErrorCodeNotFound, Message: "Controller not found"}
	case errors.Is(err, controller.ErrAlreadyExists):
		return &Error{HTTPStatus: http.StatusConflict, Code: api.ErrorCodeAlreadyExists, Message: err.Error()}
	case errors.Is(err, controller.ErrInvalidDefinition), errors.Is(err, controller.ErrInvalidPrompt):
		return &Error{HTTPStatus: http.StatusBadRequest, Code: api.ErrorCodeBadRequest, Message: err.Error()}
	case errors.Is(err, controller.ErrActiveController), errors.Is(err, controller.ErrInvalidLifecycle):
		return &Error{HTTPStatus: http.StatusConflict, Code: api.ErrorCodeConflict, Message: err.Error()}
	case errors.Is(err, controller.ErrRuntimeCorrupt):
		return &Error{HTTPStatus: http.StatusInternalServerError, Code: api.ErrorCodeControllerRuntimeCorrupt, Message: "Controller runtime is corrupt"}
	case errors.Is(err, controller.ErrDefinitionCorrupt):
		return &Error{HTTPStatus: http.StatusInternalServerError, Code: api.ErrorCodeInternalError, Message: "Controller definition is corrupt"}
	default:
		return fmt.Errorf("controller service: %w", err)
	}
}
