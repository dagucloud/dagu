// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/audit"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
)

// SelfUpgrader upgrades Dagu to the latest release from the web UI and has
// the service that runs Dagu restart it.
type SelfUpgrader interface {
	// Status reports whether the upgrade is offered and how far it got.
	Status() api.SelfUpgradeStatus
	// Start begins the upgrade in the background. It fails with
	// ErrSelfUpgradeUnavailable or ErrSelfUpgradeRunning.
	Start() error
}

var (
	// ErrSelfUpgradeUnavailable reports an installation that cannot upgrade
	// itself from the web UI.
	ErrSelfUpgradeUnavailable = errors.New("this installation cannot upgrade from the web UI")
	// ErrSelfUpgradeRunning reports an upgrade already in progress.
	ErrSelfUpgradeRunning = errors.New("an upgrade is already in progress")
)

// WithSelfUpgrader sets the upgrader behind the web upgrade endpoints.
func WithSelfUpgrader(upgrader SelfUpgrader) APIOption {
	return func(a *API) {
		a.selfUpgrader = upgrader
	}
}

// GetSelfUpgrade returns whether Dagu can upgrade itself from the web UI and
// the state of an upgrade started there.
func (a *API) GetSelfUpgrade(ctx context.Context, _ api.GetSelfUpgradeRequestObject) (api.GetSelfUpgradeResponseObject, error) {
	if err := a.requireAdmin(ctx); err != nil {
		return nil, err
	}
	if a.selfUpgrader == nil {
		return api.GetSelfUpgrade200JSONResponse{State: api.SelfUpgradeStateIdle}, nil
	}
	return api.GetSelfUpgrade200JSONResponse(a.selfUpgrader.Status()), nil
}

// StartSelfUpgrade starts upgrading Dagu to the latest release.
func (a *API) StartSelfUpgrade(ctx context.Context, _ api.StartSelfUpgradeRequestObject) (api.StartSelfUpgradeResponseObject, error) {
	if err := a.requireAdmin(ctx); err != nil {
		return nil, err
	}
	if a.selfUpgrader == nil {
		return nil, selfUpgradeConflict(ErrSelfUpgradeUnavailable)
	}
	if err := a.selfUpgrader.Start(); err != nil {
		if errors.Is(err, ErrSelfUpgradeUnavailable) || errors.Is(err, ErrSelfUpgradeRunning) {
			return nil, selfUpgradeConflict(err)
		}
		return nil, err
	}
	a.logAudit(ctx, audit.CategorySystem, "self_upgrade", map[string]any{
		"from": config.Version,
	})
	return api.StartSelfUpgrade202JSONResponse(a.selfUpgrader.Status()), nil
}

func selfUpgradeConflict(err error) error {
	return &Error{
		HTTPStatus: http.StatusConflict,
		Code:       api.ErrorCodeConflict,
		Message:    err.Error(),
	}
}
