// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"net/http"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/audit"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
)

// GetLicenseRemoteAccess returns how far calls from Dagu Cloud may go on this
// server, and whether they reach it.
func (a *API) GetLicenseRemoteAccess(ctx context.Context, _ api.GetLicenseRemoteAccessRequestObject) (api.GetLicenseRemoteAccessResponseObject, error) {
	if err := a.requireCloudAccess(ctx); err != nil {
		return nil, err
	}
	access, err := a.licenseRemoteAccess(ctx)
	if err != nil {
		return nil, err
	}
	return api.GetLicenseRemoteAccess200JSONResponse(access), nil
}

// UpdateLicenseRemoteAccess chooses how far calls from Dagu Cloud may go on
// this server. It changes nothing unless it can change all it was asked to.
func (a *API) UpdateLicenseRemoteAccess(ctx context.Context, request api.UpdateLicenseRemoteAccessRequestObject) (api.UpdateLicenseRemoteAccessResponseObject, error) {
	if err := a.requireCloudAccess(ctx); err != nil {
		return nil, err
	}
	body := request.Body
	if body == nil || (body.Level == nil && body.Approvals == nil) {
		return nil, &Error{Code: api.ErrorCodeBadRequest, Message: "level or approvals is required", HTTPStatus: http.StatusBadRequest}
	}
	previous, err := a.cloudAccess.Status(ctx)
	if err != nil {
		return nil, internalError(err)
	}
	switch {
	case body.Level != nil && !body.Level.Valid():
		return nil, unknownRemoteAccessLevelError("level")
	case body.Level != nil && previous.LevelConfigured:
		return nil, configuredError("cloud.remote_access in the configuration sets the remote access level.")
	case body.Approvals != nil && previous.ApprovalsConfigured:
		return nil, configuredError("cloud.remote_approvals in the configuration sets whether remote calls may decide waiting steps.")
	}

	var level *config.RemoteAccessLevel
	details := map[string]any{}
	if body.Level != nil {
		level = new(config.RemoteAccessLevel(*body.Level))
		details["level"], details["previous_level"] = *level, previous.Level
	}
	if body.Approvals != nil {
		details["approvals"], details["previous_approvals"] = *body.Approvals, previous.Approvals
	}
	if err := a.cloudAccess.Set(ctx, level, body.Approvals); err != nil {
		return nil, internalError(err)
	}
	a.logAudit(ctx, audit.CategorySystem, "cloud_remote_access_update", details)

	access, err := a.licenseRemoteAccess(ctx)
	if err != nil {
		return nil, err
	}
	return api.UpdateLicenseRemoteAccess200JSONResponse(access), nil
}

// chooseRemoteAccessLevel records level as the administrator's choice of how
// far calls from Dagu Cloud may go, unless configuration fixes the level.
func (a *API) chooseRemoteAccessLevel(ctx context.Context, level config.RemoteAccessLevel) error {
	previous, err := a.cloudAccess.Status(ctx)
	if err != nil {
		return err
	}
	if previous.LevelConfigured {
		return nil
	}
	if err := a.cloudAccess.Set(ctx, &level, nil); err != nil {
		return err
	}
	a.logAudit(ctx, audit.CategorySystem, "cloud_remote_access_update", map[string]any{
		"level": level, "previous_level": previous.Level,
	})
	return nil
}

func (a *API) requireCloudAccess(ctx context.Context) error {
	if err := a.requireAdmin(ctx); err != nil {
		return err
	}
	if a.cloudAccess == nil {
		return &Error{
			Code:       api.ErrorCodeBadRequest,
			Message:    "License management is not available",
			HTTPStatus: http.StatusBadRequest,
		}
	}
	return nil
}

func (a *API) licenseRemoteAccess(ctx context.Context) (api.LicenseRemoteAccess, error) {
	status, err := a.cloudAccess.Status(ctx)
	if err != nil {
		return api.LicenseRemoteAccess{}, internalError(err)
	}
	state := a.cloudAccess.State()
	access := api.LicenseRemoteAccess{
		Level:               api.LicenseRemoteAccessLevel(status.Level),
		LevelConfigured:     status.LevelConfigured,
		Approvals:           status.Approvals,
		ApprovalsConfigured: status.ApprovalsConfigured,
		Connected:           state.Connected,
	}
	if state.Error != "" {
		access.Error = ptrOf(state.Error)
	}
	// The last call only adds to the rest, which stays available without it.
	last, found, err := a.cloudAccess.LastCall(ctx)
	if err != nil {
		logger.Warn(ctx, "Remote access: the last call could not be read", tag.Error(err))
	}
	if err == nil && found {
		access.LastCall = &api.LicenseRemoteAccessLastCall{At: last.At, Client: optionalString(last.Client), User: optionalString(last.User)}
	}
	return access, nil
}

func unknownRemoteAccessLevelError(field string) error {
	return &Error{
		Code:       api.ErrorCodeBadRequest,
		Message:    field + " must be one of: off, viewer, operator, developer",
		HTTPStatus: http.StatusBadRequest,
	}
}

func configuredError(message string) error {
	return &Error{Code: api.ErrorCodeConflict, Message: message, HTTPStatus: http.StatusConflict}
}
