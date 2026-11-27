// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/dagsettings"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	profilepkg "github.com/dagucloud/dagu/v2/internal/profile"
	"github.com/dagucloud/dagu/v2/internal/project"
	"github.com/dagucloud/dagu/v2/internal/queue"
	"github.com/dagucloud/dagu/v2/internal/runtime"
)

// WebhookProject is a project that webhook requests reach: its webhooks and
// the stores a run they start reads and writes.
type WebhookProject struct {
	// Name is the project's name as the registry stores it.
	Name string
	// Webhooks holds the project's webhooks. Nil means the auth service's
	// own store.
	Webhooks         auth.WebhookStore
	DAGRepository    *persis.DAGRepository
	DAGRunRepository *persis.DAGRunRepository
	DAGRunManager    runtime.Manager
	QueueStore       queue.QueueStore
	ProfileStore     profilepkg.Store
	DAGSettingsStore dagsettings.Store
	// LogDir and ArtifactDir hold the logs and artifacts of the project's
	// runs.
	LogDir      string
	ArtifactDir string
	// BaseConfig is the project's base config file, or "" when none
	// applies.
	BaseConfig string
}

// WebhookProjects finds the projects other than the default one that webhook
// requests reach while projects are on. Implementations are safe for
// concurrent use.
type WebhookProjects interface {
	// Get returns the registered project called name, given in any case. It
	// returns an error wrapping project.ErrNotFound when no such project is
	// registered, and another error when the project's stores are not open.
	Get(ctx context.Context, name string) (*WebhookProject, error)
	// Migrated returns the project that migration placed the DAG called
	// dagName in, under that name, while that DAG exists there. It returns
	// nil when there is none.
	Migrated(ctx context.Context, dagName string) (*WebhookProject, error)
	// Library reports whether the DAG file at path is a library DAG.
	Library(ctx context.Context, path string) (bool, error)
}

// WithWebhookProjects returns an APIOption that lets webhook requests reach
// the projects other than the default one.
func WithWebhookProjects(projects WebhookProjects) APIOption {
	return func(a *API) {
		a.webhookProjects = projects
	}
}

// TriggerProjectWebhook triggers a run of the named project's DAG via its
// webhook. It is not found while projects are off.
func (a *API) TriggerProjectWebhook(ctx context.Context, request api.TriggerProjectWebhookRequestObject) (api.TriggerProjectWebhookResponseObject, error) {
	if !a.config.ProjectsEnabled {
		return nil, &Error{
			HTTPStatus: http.StatusNotFound,
			Code:       api.ErrorCodeNotFound,
			Message:    "not found",
		}
	}
	if err := a.requireWebhookTriggering(); err != nil {
		return nil, err
	}
	target, err := a.namedWebhookProject(ctx, request.Project)
	if err != nil {
		return nil, err
	}
	resp, err := a.triggerWebhook(ctx, target, api.TriggerWebhookRequestObject{
		FileName: request.FileName,
		Params:   api.TriggerWebhookParams(request.Params),
		Body:     request.Body,
	})
	switch resp := resp.(type) {
	case api.TriggerWebhook200JSONResponse:
		return api.TriggerProjectWebhook200JSONResponse(resp), err
	case api.TriggerWebhook409JSONResponse:
		return api.TriggerProjectWebhook409JSONResponse(resp), err
	case nil:
		return nil, err
	default:
		return nil, fmt.Errorf("unexpected webhook response %T", resp)
	}
}

// namedWebhookProject returns the project a per-project webhook URL names.
func (a *API) namedWebhookProject(ctx context.Context, name string) (*WebhookProject, error) {
	if strings.EqualFold(name, project.DefaultName) {
		return a.defaultWebhookProject(), nil
	}
	var (
		target *WebhookProject
		err    = project.ErrNotFound
	)
	if a.webhookProjects != nil {
		target, err = a.webhookProjects.Get(ctx, name)
	}
	switch {
	case errors.Is(err, project.ErrNotFound):
		return nil, &Error{
			HTTPStatus: http.StatusNotFound,
			Code:       api.ErrorCodeNotFound,
			Message:    fmt.Sprintf("project %s not found", name),
		}
	case err != nil:
		// A sender retries a request the server cannot serve yet, but not
		// one for a URL that is not found.
		logger.Warn(ctx, "Webhook: project is not open", tag.Name(name), tag.Error(err))
		return nil, &Error{
			HTTPStatus: http.StatusServiceUnavailable,
			Code:       api.ErrorCodeInternalError,
			Message:    fmt.Sprintf("project %s is not available yet", name),
		}
	}
	return target, nil
}

// legacyWebhookProject returns the project a webhook URL without a project
// reaches: the one migration placed the DAG in while that DAG exists, and
// otherwise the default project.
func (a *API) legacyWebhookProject(ctx context.Context, dagName string) (*WebhookProject, error) {
	if !a.config.ProjectsEnabled || a.webhookProjects == nil {
		return a.defaultWebhookProject(), nil
	}
	target, err := a.webhookProjects.Migrated(ctx, dagName)
	if err != nil {
		logger.Error(ctx, "Webhook: failed to find the project of a migrated DAG", tag.Name(dagName), tag.Error(err))
		return nil, webhookProjectUnavailable()
	}
	if target == nil {
		return a.defaultWebhookProject(), nil
	}
	return target, nil
}

// refuseLibraryDAG reports a library DAG as not found while projects are
// on, since no webhook URL addresses a library DAG's runs in a project.
func (a *API) refuseLibraryDAG(ctx context.Context, dag *ir.DAG) error {
	if !a.config.ProjectsEnabled || a.webhookProjects == nil {
		return nil
	}
	library, err := a.webhookProjects.Library(ctx, dag.Location)
	if err != nil {
		return err
	}
	if library {
		return fmt.Errorf("%w: %s is a library DAG", persis.ErrDAGNotFound, dag.Location)
	}
	return nil
}

// defaultWebhookProject returns the default project, whose stores are the
// API's own.
func (a *API) defaultWebhookProject() *WebhookProject {
	logDir, artifactDir := a.runDirs()
	return &WebhookProject{
		Name:             project.DefaultName,
		DAGRepository:    a.dagRepository,
		DAGRunRepository: a.dagRunRepository,
		DAGRunManager:    a.dagRunMgr,
		QueueStore:       a.queueStore,
		ProfileStore:     a.profileStore,
		DAGSettingsStore: a.dagSettingsStore,
		LogDir:           logDir,
		ArtifactDir:      artifactDir,
		BaseConfig:       a.config.Paths.BaseConfig,
	}
}

func (p *WebhookProject) runQueue() runQueue {
	return runQueue{
		runs:        p.DAGRunRepository,
		queue:       p.QueueStore,
		logDir:      p.LogDir,
		artifactDir: p.ArtifactDir,
		baseConfig:  p.BaseConfig,
	}
}

func webhookProjectUnavailable() *Error {
	return &Error{
		HTTPStatus: http.StatusInternalServerError,
		Code:       api.ErrorCodeInternalError,
		Message:    "webhook validation failed",
	}
}
