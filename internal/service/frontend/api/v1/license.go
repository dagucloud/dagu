// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

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
		Valid:        status.Valid,
		Plan:         status.Plan,
		Expiry:       stringutil.FormatTime(status.Expiry),
		Features:     status.Features,
		GracePeriod:  status.GracePeriod,
		GraceEndsAt:  stringutil.FormatTime(status.GraceEndsAt),
		Community:    status.Community,
		Source:       publicLicenseSource(status.Source),
		WarningCode:  status.WarningCode,
		Error:        status.Failure,
		ServerName:   ptrOf(status.ServerName),
		LicenseId:    ptrOf(status.LicenseID),
		Workspace:    ptrOf(status.Workspace),
		ConnectedVia: ptrOf(api.LicenseStatusResponseConnectedVia(status.ConnectedVia)),
		ServerId:     ptrOf(status.ServerID),
		LastCheckIn:  ptrOf(stringutil.FormatTime(status.LastCheckIn)),
		ConsoleUrl:   ptrOf(status.ConsoleURL),
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
			Message:    activationFailureMessage(err),
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

// activationFailureMessage returns Dagu Console's explanation for a rejected
// activation, which tells the user what to fix, and a generic message for
// network or server failures.
func activationFailureMessage(err error) string {
	if cloudErr, ok := errors.AsType[*license.CloudError](err); ok {
		switch cloudErr.StatusCode {
		case http.StatusBadRequest, http.StatusNotFound, http.StatusConflict:
			if cloudErr.Message != "" {
				return cloudErr.Message
			}
		}
		if cloudErr.StatusCode == http.StatusNotFound {
			return "Dagu Console does not recognize this license key."
		}
	}
	return "License activation failed. Please verify your license key and try again."
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

	result, err := a.licenseManager.Deactivate(ctx)
	if err != nil {
		slog.Warn("License deactivation failed", "error", err)
		return nil, &Error{
			Code:       api.ErrorCodeBadRequest,
			Message:    "License deactivation failed. Please try again.",
			HTTPStatus: http.StatusBadRequest,
		}
	}

	msg := "License deactivated"
	return api.DeactivateLicense200JSONResponse{
		Message:       &msg,
		ReleaseFailed: &result.ReleaseFailed,
	}, nil
}

// StartLicenseConnect starts, or resumes, a request to connect this server to
// Dagu Console.
func (a *API) StartLicenseConnect(ctx context.Context, _ api.StartLicenseConnectRequestObject) (api.StartLicenseConnectResponseObject, error) {
	if err := a.requireLicenseManagement(ctx); err != nil {
		return nil, err
	}

	status, err := a.licenseManager.Connect()
	switch {
	case errors.Is(err, license.ErrManagedExternally), errors.Is(err, license.ErrAlreadyLicensed):
		return nil, &Error{Code: api.ErrorCodeConflict, Message: err.Error(), HTTPStatus: http.StatusConflict}
	case err != nil:
		return nil, internalError(err)
	}
	return api.StartLicenseConnect200JSONResponse(toLicenseConnectStatus(status)), nil
}

// GetLicenseConnect returns the latest request to connect this server to
// Dagu Console.
func (a *API) GetLicenseConnect(ctx context.Context, _ api.GetLicenseConnectRequestObject) (api.GetLicenseConnectResponseObject, error) {
	if err := a.requireLicenseManagement(ctx); err != nil {
		return nil, err
	}
	return api.GetLicenseConnect200JSONResponse(toLicenseConnectStatus(a.licenseManager.ConnectStatus())), nil
}

// CancelLicenseConnect abandons the pending request to connect this server
// to Dagu Console.
func (a *API) CancelLicenseConnect(ctx context.Context, _ api.CancelLicenseConnectRequestObject) (api.CancelLicenseConnectResponseObject, error) {
	if err := a.requireLicenseManagement(ctx); err != nil {
		return nil, err
	}
	a.licenseManager.CancelConnect()
	return api.CancelLicenseConnect200JSONResponse(toLicenseConnectStatus(a.licenseManager.ConnectStatus())), nil
}

// RefreshLicense checks in with Dagu Console immediately.
func (a *API) RefreshLicense(ctx context.Context, _ api.RefreshLicenseRequestObject) (api.RefreshLicenseResponseObject, error) {
	if err := a.requireLicenseManagement(ctx); err != nil {
		return nil, err
	}

	err := a.licenseManager.Refresh(ctx)
	switch {
	case errors.Is(err, license.ErrNoCheckIn):
		return nil, &Error{Code: api.ErrorCodeBadRequest, Message: "This license does not check in with Dagu Console.", HTTPStatus: http.StatusBadRequest}
	case err != nil:
		slog.Warn("License refresh failed", "error", err)
		return nil, &Error{Code: api.ErrorCodeBadGateway, Message: "Dagu Console could not be reached. Try again later.", HTTPStatus: http.StatusBadGateway}
	}
	return api.RefreshLicense200JSONResponse(toLicenseStatusResponse(a.licenseManager.Status())), nil
}

func (a *API) requireLicenseManagement(ctx context.Context) error {
	if err := a.requireAdmin(ctx); err != nil {
		return err
	}
	if a.licenseManager == nil {
		return &Error{
			Code:       api.ErrorCodeBadRequest,
			Message:    "License management is not available",
			HTTPStatus: http.StatusBadRequest,
		}
	}
	return nil
}

func toLicenseConnectStatus(status license.ConnectStatus) api.LicenseConnectStatus {
	return api.LicenseConnectStatus{
		State:      api.LicenseConnectStatusState(status.State),
		ConnectUrl: ptrOf(status.URL),
		Code:       ptrOf(status.Code),
		ExpiresAt:  ptrOf(stringutil.FormatTime(status.ExpiresAt)),
		Error:      ptrOf(status.Error),
	}
}

// licenseProxyAuthorization requires an administrator before a license
// request is forwarded to a remote node, which would otherwise run it with
// the node's own stored credentials. Reading the license status stays open
// to every signed-in user, as it is locally.
func (a *API) licenseProxyAuthorization(apiBasePath string) func(http.Handler) http.Handler {
	licensePath := strings.TrimRight(apiBasePath, "/") + "/license/"
	statusPath := licensePath + "status"
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			remoteNode := r.URL.Query().Get("remoteNode")
			if remoteNode == "" || remoteNode == "local" ||
				!strings.HasPrefix(r.URL.Path, licensePath) ||
				(r.Method == http.MethodGet && r.URL.Path == statusPath) {
				next.ServeHTTP(w, r)
				return
			}
			if err := a.requireAdmin(r.Context()); err != nil {
				WriteErrorResponse(w, err)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
