// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/cmn/stringutil"
	"github.com/dagucloud/dagu/v2/internal/license"
)

// GetLicenseStatus returns the current public license status.
func (a *API) GetLicenseStatus(_ context.Context, _ api.GetLicenseStatusRequestObject) (api.GetLicenseStatusResponseObject, error) {
	status := license.StatusFor(nil)
	if a.licenseManager != nil {
		status = a.licenseManager.Status()
	}
	return api.GetLicenseStatus200JSONResponse(toLicenseStatusResponse(status)), nil
}

func toLicenseStatusResponse(status license.Status) api.LicenseStatusResponse {
	return api.LicenseStatusResponse{
		Valid:       status.Valid,
		Plan:        status.Plan,
		Expiry:      stringutil.FormatTime(status.Expiry),
		Features:    status.Features,
		GracePeriod: status.GracePeriod,
		GraceEndsAt: stringutil.FormatTime(status.GraceEndsAt),
		Community:   status.Community,
		Source:      publicLicenseSource(status.Source),
		WarningCode: status.WarningCode,
		Error:       status.Failure,
	}
}

func publicLicenseSource(source license.DiscoverySource) string {
	if source.IsEnv() {
		return "env"
	}
	if source == license.SourceNone {
		return ""
	}
	return "file"
}

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
