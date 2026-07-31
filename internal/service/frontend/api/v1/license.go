// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/dagucloud/dagu/v2/api/v1"
)

// ActivateLicense handles license activation from the frontend.
func (a *API) ActivateLicense(ctx context.Context, request api.ActivateLicenseRequestObject) (api.ActivateLicenseResponseObject, error) {
	if err := a.requireAdmin(ctx); err != nil {
		return nil, err
	}

	if a.licenseManager == nil {
		return nil, &Error{
			Code:       api.ErrorCodeBadRequest,
			Message:    "License management is not available",
			HTTPStatus: http.StatusBadRequest,
		}
	}

	if request.Body == nil || request.Body.Key == "" {
		return nil, &Error{
			Code:       api.ErrorCodeBadRequest,
			Message:    "License key is required",
			HTTPStatus: http.StatusBadRequest,
		}
	}

	result, err := a.licenseManager.ActivateWithKey(ctx, request.Body.Key)
	if err != nil {
		slog.Warn("License activation failed", "error", err)
		return nil, &Error{
			Code:       api.ErrorCodeBadRequest,
			Message:    "License activation failed. Please verify your license key and try again.",
			HTTPStatus: http.StatusBadRequest,
		}
	}

	var expiry *string
	if !result.Expiry.IsZero() {
		s := result.Expiry.Format("2006-01-02T15:04:05Z")
		expiry = &s
	}

	return api.ActivateLicense200JSONResponse{
		Plan:     &result.Plan,
		Features: &result.Features,
		Expiry:   expiry,
	}, nil
}

// DeactivateLicense handles license deactivation from the frontend.
func (a *API) DeactivateLicense(ctx context.Context, _ api.DeactivateLicenseRequestObject) (api.DeactivateLicenseResponseObject, error) {
	if err := a.requireAdmin(ctx); err != nil {
		return nil, err
	}

	if a.licenseManager == nil {
		return nil, &Error{
			Code:       api.ErrorCodeBadRequest,
			Message:    "License management is not available",
			HTTPStatus: http.StatusBadRequest,
		}
	}

	if err := a.licenseManager.Deactivate(ctx); err != nil {
		slog.Warn("License deactivation failed", "error", err)
		return nil, &Error{
			Code:       api.ErrorCodeBadRequest,
			Message:    "License deactivation failed. Please try again.",
			HTTPStatus: http.StatusBadRequest,
		}
	}

	msg := "License deactivated"
	return api.DeactivateLicense200JSONResponse{Message: &msg}, nil
}
