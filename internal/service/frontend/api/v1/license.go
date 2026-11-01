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
	"strings"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/audit"
	"github.com/dagucloud/dagu/v2/internal/cloudreport"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
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
		Expiry:       stringutil.FormatTimeSeconds(status.Expiry),
		Features:     status.Features,
		GracePeriod:  status.GracePeriod,
		GraceEndsAt:  stringutil.FormatTimeSeconds(status.GraceEndsAt),
		Community:    status.Community,
		Source:       publicLicenseSource(status.Source),
		WarningCode:  status.WarningCode,
		Error:        status.Failure,
		ServerName:   ptrOf(status.ServerName),
		ReportedName: ptrOf(status.ReportedServerName),
		LicenseId:    ptrOf(status.LicenseID),
		Workspace:    ptrOf(status.Workspace),
		ConnectedVia: ptrOf(api.LicenseStatusResponseConnectedVia(status.ConnectedVia)),
		ServerId:     ptrOf(status.ServerID),
		LastCheckIn:  ptrOf(stringutil.FormatTimeSeconds(status.LastCheckIn)),
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
// Dagu Console. A report level in the request chooses what this server
// reports once connected, unless configuration fixes it.
func (a *API) StartLicenseConnect(ctx context.Context, request api.StartLicenseConnectRequestObject) (api.StartLicenseConnectResponseObject, error) {
	if err := a.requireLicenseManagement(ctx); err != nil {
		return nil, err
	}
	var report config.ReportLevel
	if request.Body != nil && request.Body.Report != nil {
		report = config.ReportLevel(*request.Body.Report)
		if !knownReportLevel(report) {
			return nil, unknownReportLevelError("report")
		}
	}

	status, err := a.licenseManager.Connect()
	switch {
	case errors.Is(err, license.ErrManagedExternally), errors.Is(err, license.ErrAlreadyLicensed):
		return nil, &Error{Code: api.ErrorCodeConflict, Message: err.Error(), HTTPStatus: http.StatusConflict}
	case err != nil:
		return nil, internalError(err)
	}

	if report != "" && a.cloudReport != nil {
		// The configured level applies whatever the request chose.
		if err := a.setReportLevel(ctx, report); err != nil && !errors.Is(err, cloudreport.ErrLevelConfigured) {
			return nil, internalError(err)
		}
	}
	described, err := a.licenseConnectStatus(ctx, status)
	if err != nil {
		return nil, internalError(err)
	}
	return api.StartLicenseConnect200JSONResponse(described), nil
}

// GetLicenseConnect returns the latest request to connect this server to
// Dagu Console.
func (a *API) GetLicenseConnect(ctx context.Context, _ api.GetLicenseConnectRequestObject) (api.GetLicenseConnectResponseObject, error) {
	if err := a.requireLicenseManagement(ctx); err != nil {
		return nil, err
	}
	described, err := a.licenseConnectStatus(ctx, a.licenseManager.ConnectStatus())
	if err != nil {
		return nil, internalError(err)
	}
	return api.GetLicenseConnect200JSONResponse(described), nil
}

// licenseConnectStatus describes a connection request. Its connect URL names
// the level this server reports at, if any, so that the approval page in
// Dagu Console can state what is reported once connected. It fails when the
// level cannot be read, rather than offer a link that may understate it.
func (a *API) licenseConnectStatus(ctx context.Context, status license.ConnectStatus) (api.LicenseConnectStatus, error) {
	if a.cloudReport == nil || status.URL == "" {
		return toLicenseConnectStatus(status), nil
	}
	monitoring, err := a.cloudReport.Status(ctx)
	if err != nil {
		return api.LicenseConnectStatus{}, err
	}
	if !monitoring.Level.Reports() {
		return toLicenseConnectStatus(status), nil
	}
	connectURL, err := url.Parse(status.URL)
	if err != nil {
		return api.LicenseConnectStatus{}, err
	}
	query := connectURL.Query()
	query.Set("report", string(monitoring.Level))
	connectURL.RawQuery = query.Encode()
	status.URL = connectURL.String()
	return toLicenseConnectStatus(status), nil
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

// RenameLicenseServer sets the name Dagu Console shows for this server.
func (a *API) RenameLicenseServer(ctx context.Context, request api.RenameLicenseServerRequestObject) (api.RenameLicenseServerResponseObject, error) {
	if err := a.requireLicenseManagement(ctx); err != nil {
		return nil, err
	}
	if request.Body == nil {
		return nil, &Error{Code: api.ErrorCodeBadRequest, Message: "name is required", HTTPStatus: http.StatusBadRequest}
	}

	err := a.licenseManager.RenameServer(ctx, request.Body.Name)
	if err != nil {
		return nil, renameServerError(err)
	}
	a.logAudit(ctx, audit.CategorySystem, "license_server_rename", map[string]any{
		"name": strings.TrimSpace(request.Body.Name),
	})
	return api.RenameLicenseServer200JSONResponse(toLicenseStatusResponse(a.licenseManager.Status())), nil
}

// renameServerError explains why renaming this server failed.
func renameServerError(err error) error {
	badRequest := func(message string) error {
		return &Error{Code: api.ErrorCodeBadRequest, Message: message, HTTPStatus: http.StatusBadRequest}
	}
	switch {
	case errors.Is(err, license.ErrServerNameTooLong):
		return badRequest(fmt.Sprintf("Server names are at most %d characters.", license.MaxServerNameLength))
	case errors.Is(err, license.ErrNoCheckIn):
		return badRequest("This license does not check in with Dagu Console.")
	case errors.Is(err, license.ErrRenameUnsupported):
		return badRequest("Dagu Console does not support renaming servers yet.")
	}
	if cloudErr, ok := errors.AsType[*license.CloudError](err); ok &&
		cloudErr.StatusCode >= 400 && cloudErr.StatusCode < 500 && cloudErr.StatusCode != http.StatusTooManyRequests {
		return badRequest("Dagu Console refused the name: " + cloudErr.Message)
	}
	slog.Warn("License server rename failed", "error", err)
	return &Error{Code: api.ErrorCodeBadGateway, Message: "Dagu Console could not be reached. Try again later.", HTTPStatus: http.StatusBadGateway}
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

// GetLicenseMonitoring returns what this server reports to Dagu Console.
func (a *API) GetLicenseMonitoring(ctx context.Context, _ api.GetLicenseMonitoringRequestObject) (api.GetLicenseMonitoringResponseObject, error) {
	if err := a.requireCloudReport(ctx); err != nil {
		return nil, err
	}
	status, err := a.licenseMonitoring(ctx)
	if err != nil {
		return nil, err
	}
	return api.GetLicenseMonitoring200JSONResponse(status), nil
}

// UpdateLicenseMonitoring chooses what this server reports to Dagu Console.
func (a *API) UpdateLicenseMonitoring(ctx context.Context, request api.UpdateLicenseMonitoringRequestObject) (api.UpdateLicenseMonitoringResponseObject, error) {
	if err := a.requireCloudReport(ctx); err != nil {
		return nil, err
	}
	if request.Body == nil {
		return nil, &Error{Code: api.ErrorCodeBadRequest, Message: "level is required", HTTPStatus: http.StatusBadRequest}
	}

	err := a.setReportLevel(ctx, config.ReportLevel(request.Body.Level))
	switch {
	case errors.Is(err, cloudreport.ErrLevelConfigured):
		return nil, &Error{
			Code:       api.ErrorCodeConflict,
			Message:    "cloud.report in the configuration sets what this server reports.",
			HTTPStatus: http.StatusConflict,
		}
	case errors.Is(err, cloudreport.ErrUnknownLevel):
		return nil, unknownReportLevelError("level")
	case err != nil:
		return nil, internalError(err)
	}

	status, err := a.licenseMonitoring(ctx)
	if err != nil {
		return nil, err
	}
	return api.UpdateLicenseMonitoring200JSONResponse(status), nil
}

// setReportLevel chooses what this server reports to Dagu Console and audits
// the change. It fails as Monitoring.SetLevel does.
func (a *API) setReportLevel(ctx context.Context, level config.ReportLevel) error {
	previous, err := a.cloudReport.Status(ctx)
	if err != nil {
		return err
	}
	if err := a.cloudReport.SetLevel(ctx, level); err != nil {
		return err
	}
	a.logAudit(ctx, audit.CategorySystem, "cloud_report_update", map[string]any{
		"level":          level,
		"previous_level": previous.Level,
	})
	return nil
}

func knownReportLevel(level config.ReportLevel) bool {
	switch level {
	case config.ReportOff, config.ReportHealth, config.ReportRuns, config.ReportErrors:
		return true
	}
	return false
}

// unknownReportLevelError rejects the report level in the request field
// named field.
func unknownReportLevelError(field string) error {
	return &Error{
		Code:       api.ErrorCodeBadRequest,
		Message:    field + " must be one of: off, health, runs, errors",
		HTTPStatus: http.StatusBadRequest,
	}
}

// DismissLicenseMonitoringNotice stops asking administrators what this server
// reports to Dagu Console.
func (a *API) DismissLicenseMonitoringNotice(ctx context.Context, _ api.DismissLicenseMonitoringNoticeRequestObject) (api.DismissLicenseMonitoringNoticeResponseObject, error) {
	if err := a.requireCloudReport(ctx); err != nil {
		return nil, err
	}
	if err := a.cloudReport.DismissNotice(ctx); err != nil {
		return nil, internalError(err)
	}
	status, err := a.licenseMonitoring(ctx)
	if err != nil {
		return nil, err
	}
	return api.DismissLicenseMonitoringNotice200JSONResponse(status), nil
}

// DismissLicenseMonitoringErrorsNotice stops offering administrators to have
// this server also send error messages to Dagu Console.
func (a *API) DismissLicenseMonitoringErrorsNotice(ctx context.Context, _ api.DismissLicenseMonitoringErrorsNoticeRequestObject) (api.DismissLicenseMonitoringErrorsNoticeResponseObject, error) {
	if err := a.requireCloudReport(ctx); err != nil {
		return nil, err
	}
	if err := a.cloudReport.DismissErrorsNotice(ctx); err != nil {
		return nil, internalError(err)
	}
	status, err := a.licenseMonitoring(ctx)
	if err != nil {
		return nil, err
	}
	return api.DismissLicenseMonitoringErrorsNotice200JSONResponse(status), nil
}

func (a *API) requireCloudReport(ctx context.Context) error {
	if err := a.requireAdmin(ctx); err != nil {
		return err
	}
	if a.cloudReport == nil {
		return &Error{
			Code:       api.ErrorCodeBadRequest,
			Message:    "License management is not available",
			HTTPStatus: http.StatusBadRequest,
		}
	}
	return nil
}

func (a *API) licenseMonitoring(ctx context.Context) (api.LicenseMonitoring, error) {
	status, err := a.cloudReport.Status(ctx)
	if err != nil {
		return api.LicenseMonitoring{}, internalError(err)
	}
	monitoring := api.LicenseMonitoring{
		Level:           api.LicenseMonitoringLevel(status.Level),
		Configured:      status.Configured,
		Chosen:          status.Chosen,
		NoticeDismissed: status.NoticeDismissed,
		ErrorsOffered:   status.ErrorsOffered,
	}
	if !status.LastReportAt.IsZero() {
		monitoring.LastReportAt = ptrOf(stringutil.FormatTimeSeconds(status.LastReportAt))
		monitoring.LastReport = ptrOf(string(status.LastReport))
	}
	if !status.RunsPausedUntil.IsZero() {
		monitoring.RunsPausedUntil = ptrOf(status.RunsPausedUntil)
	}
	return monitoring, nil
}

func toLicenseConnectStatus(status license.ConnectStatus) api.LicenseConnectStatus {
	return api.LicenseConnectStatus{
		State:      api.LicenseConnectStatusState(status.State),
		ConnectUrl: ptrOf(status.URL),
		Code:       ptrOf(status.Code),
		ExpiresAt:  ptrOf(stringutil.FormatTimeSeconds(status.ExpiresAt)),
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
