// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	apigen "github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/internal/auth"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/controller"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/service/audit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const validControllerCreateSpec = `type: controller
version: 1
name: Incident flow
description: Route incident work safely.
labels:
  - workspace=ops
llm:
  provider: openai
  model: gpt-4o
dags: []
states:
  default:
    description: Initial routing state.
    transitions:
      - to: completed
        when: Work is complete.
  completed:
    description: Work completed successfully.
    terminal: succeeded
`

type stubControllerService struct {
	detail      *controller.Detail
	items       []controller.Summary
	createCalls int
	startPrompt string
}

func (s *stubControllerService) Create(context.Context, []byte) (*controller.Detail, error) {
	s.createCalls++
	return s.detail, nil
}

func (s *stubControllerService) ListVisible(_ context.Context, include func(controller.Definition) bool) ([]controller.Summary, error) {
	result := make([]controller.Summary, 0, len(s.items))
	for _, item := range s.items {
		var labels []string
		if item.Workspace != "" {
			labels = []string{"workspace=" + item.Workspace}
		}
		if include == nil || include(controller.Definition{Labels: labels}) {
			result = append(result, item)
		}
	}
	return result, nil
}

func (s *stubControllerService) GetDefinition(context.Context, string) (*controller.Definition, error) {
	if s.detail == nil {
		return nil, controller.ErrNotFound
	}
	definition := s.detail.Definition
	return &definition, nil
}

func (s *stubControllerService) Get(context.Context, string) (*controller.Detail, error) {
	if s.detail == nil {
		return nil, controller.ErrNotFound
	}
	return s.detail, nil
}

func (s *stubControllerService) Update(context.Context, string, []byte) (*controller.Detail, error) {
	return s.detail, nil
}

func (s *stubControllerService) Delete(context.Context, string) error { return nil }

func (s *stubControllerService) Start(_ context.Context, _ string, prompt string) (controller.RuntimeView, error) {
	s.startPrompt = prompt
	return controller.RuntimeView{Status: core.Running}, nil
}

func (s *stubControllerService) Prompt(context.Context, string, string) (controller.RuntimeView, error) {
	return controller.RuntimeView{}, nil
}

func (s *stubControllerService) Stop(context.Context, string) (controller.RuntimeView, error) {
	return controller.RuntimeView{}, nil
}

type controllerAuthService struct{ AuthService }

type controllerDAGRunStore struct {
	exec.DAGRunStore
	attempts map[exec.DAGRunRef]exec.DAGRunAttempt
	refs     []exec.DAGRunRef
}

func (s *controllerDAGRunStore) FindAttempt(_ context.Context, ref exec.DAGRunRef) (exec.DAGRunAttempt, error) {
	s.refs = append(s.refs, ref)
	attempt, ok := s.attempts[ref]
	if !ok {
		return nil, exec.ErrDAGRunIDNotFound
	}
	return attempt, nil
}

func TestListControllersFiltersWorkspaceAndReturnsCompactViews(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 22, 12, 0, 0, 0, time.UTC)
	latestRun := controller.DAGRunRef{State: "default", DAG: "classify", DAGRunID: "run-1"}
	service := &stubControllerService{items: []controller.Summary{
		{ID: "ctrl_aaaaaaaaaaaaaaaa", Name: "Ops", Workspace: "ops", Status: core.Running, MaxTurns: 100, LatestDAGRun: &latestRun, ResourceUpdatedAt: now},
		{ID: "ctrl_bbbbbbbbbbbbbbbb", Name: "Default", Status: core.NotStarted, MaxTurns: 100, ResourceUpdatedAt: now},
	}}
	handler := &API{
		controllerService: service,
		dagRunStore: &controllerDAGRunStore{attempts: map[exec.DAGRunRef]exec.DAGRunAttempt{
			exec.NewDAGRunRef("classify", "run-1"): &exec.MockDAGRunAttempt{Status: &exec.DAGRunStatus{
				Name: "classify", DAGRunID: "run-1", Status: core.Succeeded,
			}},
		}},
	}
	workspaceName := apigen.Workspace("ops")

	response, err := handler.ListControllers(context.Background(), apigen.ListControllersRequestObject{
		Params: apigen.ListControllersParams{Workspace: &workspaceName},
	})

	require.NoError(t, err)
	result, ok := response.(apigen.ListControllers200JSONResponse)
	require.True(t, ok)
	require.Len(t, result.Controllers, 1)
	assert.Equal(t, "ctrl_aaaaaaaaaaaaaaaa", result.Controllers[0].Id)
	assert.Equal(t, apigen.StatusLabel("running"), result.Controllers[0].StatusLabel)
	require.NotNil(t, result.Controllers[0].LatestDAGRun)
	require.NotNil(t, result.Controllers[0].LatestDAGRun.Status)
	assert.Equal(t, apigen.Status(core.Succeeded), *result.Controllers[0].LatestDAGRun.Status)
	require.NotNil(t, result.Controllers[0].LatestDAGRun.StatusLabel)
	assert.Equal(t, apigen.StatusLabel(core.Succeeded.String()), *result.Controllers[0].LatestDAGRun.StatusLabel)
}

func TestCreateControllerChecksWorkspaceWriteBeforeCreating(t *testing.T) {
	t.Parallel()

	service := &stubControllerService{detail: controllerDetailForTest()}
	handler := &API{
		controllerService: service,
		authService:       controllerAuthService{},
		config: &config.Config{Server: config.Server{Permissions: map[config.Permission]bool{
			config.PermissionWriteDAGs: true,
		}}},
	}
	ctx := auth.WithUser(context.Background(), &auth.User{
		ID:   "viewer-1",
		Role: auth.RoleViewer,
		WorkspaceAccess: &auth.WorkspaceAccess{Grants: []auth.WorkspaceGrant{
			{Workspace: "ops", Role: auth.RoleViewer},
		}},
	})

	_, err := handler.CreateController(ctx, apigen.CreateControllerRequestObject{
		Body: &apigen.ControllerSpecRequest{Spec: validControllerCreateSpec},
	})

	require.Error(t, err)
	var apiErr *Error
	require.True(t, errors.As(err, &apiErr))
	assert.Equal(t, 403, apiErr.HTTPStatus)
	assert.Zero(t, service.createCalls)
}

func TestCreateControllerReturnsResourceLocation(t *testing.T) {
	t.Parallel()

	service := &stubControllerService{detail: controllerDetailForTest()}
	handler := &API{
		controllerService: service,
		config: &config.Config{Server: config.Server{
			BasePath:    "/dagu",
			APIBasePath: "/api/v1",
			Permissions: map[config.Permission]bool{config.PermissionWriteDAGs: true},
		}},
	}

	response, err := handler.CreateController(context.Background(), apigen.CreateControllerRequestObject{
		Body: &apigen.ControllerSpecRequest{Spec: validControllerCreateSpec},
	})

	require.NoError(t, err)
	created, ok := response.(apigen.CreateController201JSONResponse)
	require.True(t, ok)
	assert.Equal(t, "ctrl_aaaaaaaaaaaaaaaa", created.Body.Id)
	assert.Equal(t, "/dagu/api/v1/controllers/ctrl_aaaaaaaaaaaaaaaa", created.Headers.Location)
}

func TestCreateControllerHonorsReadOnlyDAGSource(t *testing.T) {
	t.Parallel()

	service := &stubControllerService{detail: controllerDetailForTest()}
	handler := &API{
		controllerService: service,
		dagWritesDisabled: true,
		config: &config.Config{Server: config.Server{Permissions: map[config.Permission]bool{
			config.PermissionWriteDAGs: true,
		}}},
	}

	_, err := handler.CreateController(context.Background(), apigen.CreateControllerRequestObject{
		Body: &apigen.ControllerSpecRequest{Spec: validControllerCreateSpec},
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, errDAGWritesDisabled)
	assert.Zero(t, service.createCalls)
}

func TestGetControllerMasksInvisibleWorkspace(t *testing.T) {
	t.Parallel()

	handler := &API{
		controllerService: &stubControllerService{detail: controllerDetailForTest()},
		authService:       controllerAuthService{},
	}
	ctx := auth.WithUser(context.Background(), &auth.User{
		ID:   "viewer-1",
		Role: auth.RoleViewer,
		WorkspaceAccess: &auth.WorkspaceAccess{Grants: []auth.WorkspaceGrant{
			{Workspace: "other", Role: auth.RoleViewer},
		}},
	})

	_, err := handler.GetController(ctx, apigen.GetControllerRequestObject{Id: "ctrl_aaaaaaaaaaaaaaaa"})

	require.Error(t, err)
	var apiErr *Error
	require.True(t, errors.As(err, &apiErr))
	assert.Equal(t, 404, apiErr.HTTPStatus)
}

func TestControllerWorkspaceMaskingPrecedesRuntimeRead(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	stores := controller.NewFileStores(dataDir)
	service := controller.NewService(
		stores.Definitions,
		stores.Runtimes,
		stores.Locker,
		controller.NewValidator(nil),
		controller.WithIDGenerator(func() (string, error) { return "ctrl_aaaaaaaaaaaaaaaa", nil }),
	)
	_, err := service.Create(context.Background(), []byte(validControllerCreateSpec))
	require.NoError(t, err)
	runtimeDir := filepath.Join(dataDir, "controller-runtime")
	require.NoError(t, os.MkdirAll(runtimeDir, 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(runtimeDir, "ctrl_aaaaaaaaaaaaaaaa.json"),
		[]byte(`{"runtimeVersion":1`),
		0o600,
	))

	handler := &API{controllerService: service, authService: controllerAuthService{}}
	ctx := auth.WithUser(context.Background(), &auth.User{
		ID:   "viewer-1",
		Role: auth.RoleViewer,
		WorkspaceAccess: &auth.WorkspaceAccess{Grants: []auth.WorkspaceGrant{
			{Workspace: "other", Role: auth.RoleViewer},
		}},
	})

	_, err = handler.GetController(ctx, apigen.GetControllerRequestObject{Id: "ctrl_aaaaaaaaaaaaaaaa"})
	require.Error(t, err)
	var apiErr *Error
	require.True(t, errors.As(err, &apiErr))
	assert.Equal(t, 404, apiErr.HTTPStatus)

	response, err := handler.ListControllers(ctx, apigen.ListControllersRequestObject{})
	require.NoError(t, err)
	list, ok := response.(apigen.ListControllers200JSONResponse)
	require.True(t, ok)
	assert.Empty(t, list.Controllers)
}

func TestGetControllerHydratesPublicDAGRunSummaries(t *testing.T) {
	t.Parallel()

	detail := controllerDetailForTest()
	detail.Runtime = &controller.RuntimeView{
		Status: core.Succeeded,
		DAGRunRefs: []controller.DAGRunRef{
			{State: "default", DAG: "classify", DAGRunID: "run-1"},
			{State: "completed", DAG: "expired", DAGRunID: "run-2"},
		},
	}
	store := &controllerDAGRunStore{attempts: map[exec.DAGRunRef]exec.DAGRunAttempt{
		exec.NewDAGRunRef("classify", "run-1"): &exec.MockDAGRunAttempt{Status: &exec.DAGRunStatus{
			Name:       "classify",
			DAGRunID:   "run-1",
			Status:     core.Succeeded,
			Params:     `{"api_key":"secret"}`,
			StartedAt:  "2026-07-22T12:00:00Z",
			FinishedAt: "2026-07-22T12:01:00Z",
		}},
	}}
	handler := &API{
		controllerService: &stubControllerService{detail: detail},
		dagRunStore:       store,
	}

	response, err := handler.GetController(context.Background(), apigen.GetControllerRequestObject{
		Id: "ctrl_aaaaaaaaaaaaaaaa",
	})

	require.NoError(t, err)
	result, ok := response.(apigen.GetController200JSONResponse)
	require.True(t, ok)
	require.Len(t, result.Runtime.DagRunRefs, 2)
	require.Len(t, result.DagRuns, 1)
	assert.Equal(t, "classify", result.DagRuns[0].Name)
	assert.Equal(t, "run-1", result.DagRuns[0].DagRunId)
	assert.Nil(t, result.DagRuns[0].Params)
	assert.Equal(t, []exec.DAGRunRef{
		exec.NewDAGRunRef("classify", "run-1"),
		exec.NewDAGRunRef("expired", "run-2"),
	}, store.refs)
}

func TestStartControllerPreservesPromptBytes(t *testing.T) {
	t.Parallel()

	service := &stubControllerService{detail: controllerDetailForTest()}
	handler := &API{
		controllerService: service,
		config: &config.Config{Server: config.Server{Permissions: map[config.Permission]bool{
			config.PermissionRunDAGs: true,
		}}},
	}
	prompt := "  'quoted'\\value\nsecond line  "

	response, err := handler.StartController(context.Background(), apigen.StartControllerRequestObject{
		Id:   "ctrl_aaaaaaaaaaaaaaaa",
		Body: &apigen.ControllerPromptRequest{Prompt: prompt},
	})

	require.NoError(t, err)
	_, ok := response.(apigen.StartController202Response)
	require.True(t, ok)
	assert.Equal(t, prompt, service.startPrompt)
}

func TestStartControllerRejectsMalformedJSON(t *testing.T) {
	t.Parallel()

	handler := &API{}
	router := apigen.Handler(apigen.NewStrictHandlerWithOptions(
		handler,
		nil,
		handler.strictHTTPServerOptions(""),
	))
	request := httptest.NewRequest(
		http.MethodPost,
		"/controllers/ctrl_aaaaaaaaaaaaaaaa/start",
		strings.NewReader(`{"prompt":`),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	assert.Equal(t, http.StatusBadRequest, response.Code)
	assert.Equal(t, "application/json", response.Header().Get("Content-Type"))
	var apiError apigen.Error
	require.NoError(t, json.NewDecoder(response.Body).Decode(&apiError))
	assert.Equal(t, apigen.ErrorCodeBadRequest, apiError.Code)
	assert.Equal(t, "Invalid request body", apiError.Message)
}

func TestStartControllerAuditOmitsPrompt(t *testing.T) {
	t.Parallel()

	store := &restAuditStore{}
	service := &stubControllerService{detail: controllerDetailForTest()}
	handler := &API{
		controllerService: service,
		auditService:      audit.New(store),
		config: &config.Config{Server: config.Server{Permissions: map[config.Permission]bool{
			config.PermissionRunDAGs: true,
		}}},
	}
	prompt := "sensitive prompt marker"

	_, err := handler.StartController(context.Background(), apigen.StartControllerRequestObject{
		Id:   "ctrl_aaaaaaaaaaaaaaaa",
		Body: &apigen.ControllerPromptRequest{Prompt: prompt},
	})

	require.NoError(t, err)
	require.Len(t, store.entries, 1)
	entry := store.entries[0]
	assert.Equal(t, audit.Category("controller"), entry.Category)
	assert.Equal(t, "start", entry.Action)
	assert.Equal(t, "controller", entry.ResourceType)
	assert.Equal(t, "ctrl_aaaaaaaaaaaaaaaa", entry.ResourceID)
	assert.NotContains(t, entry.Details, prompt)
}

func TestControllerServiceErrorIncludesValidationIssues(t *testing.T) {
	t.Parallel()

	err := controllerServiceError(&controller.ValidationError{Issues: []controller.ValidationIssue{{
		Code: "required", Path: "states.default", Message: "is required",
	}}})

	var apiErr *Error
	require.True(t, errors.As(err, &apiErr))
	assert.Equal(t, apigen.ErrorCodeControllerValidationFailed, apiErr.Code)
	issues, ok := apiErr.Details["errors"].([]controller.ValidationIssue)
	require.True(t, ok)
	require.Len(t, issues, 1)
	assert.Equal(t, "states.default", issues[0].Path)
}

func controllerDetailForTest() *controller.Detail {
	return &controller.Detail{
		Definition: controller.Definition{
			Type:        controller.DefinitionType,
			Version:     controller.DefinitionVersion,
			ID:          "ctrl_aaaaaaaaaaaaaaaa",
			Name:        "Incident flow",
			Description: "Route incident work safely.",
			Labels:      []string{"workspace=ops"},
			MaxTurns:    100,
		},
		ResourceUpdatedAt: time.Date(2026, time.July, 22, 12, 0, 0, 0, time.UTC),
	}
}
