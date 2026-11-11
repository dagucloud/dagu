// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"fmt"
	"net/http"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/gorillamux"
)

// remoteNodeAccess is the local permission a caller needs before a request is
// forwarded to a remote node, which runs it with the node's stored credential.
type remoteNodeAccess int

const (
	// remoteNodeAccessAdmin is the zero value, so operations missing from
	// remoteNodeOperationAccess require an administrator.
	remoteNodeAccessAdmin remoteNodeAccess = iota
	remoteNodeAccessRead
	remoteNodeAccessExecute
	remoteNodeAccessWrite
	remoteNodeAccessWriteAndRun
	remoteNodeAccessDeveloper
	remoteNodeAccessManager
	remoteNodeAccessDenied
)

var errRemoteNodeOperationUnavailable = &Error{
	HTTPStatus: http.StatusForbidden,
	Code:       api.ErrorCodeForbidden,
	Message:    "This operation is not available on remote nodes",
}

// remoteNodeOperationAccess mirrors the permission each local handler
// enforces, keyed by OpenAPI operation ID.
var remoteNodeOperationAccess = map[string]remoteNodeAccess{
	// Reads that every signed-in user can make locally.
	"ListArtifacts":                   remoteNodeAccessRead,
	"GetCurrentUser":                  remoteNodeAccessRead,
	"ListCalendars":                   remoteNodeAccessRead,
	"GetCalendar":                     remoteNodeAccessRead,
	"PreviewCalendar":                 remoteNodeAccessRead,
	"GetDAGRunActivity":               remoteNodeAccessRead,
	"ListDAGRuns":                     remoteNodeAccessRead,
	"ListDAGRunsByName":               remoteNodeAccessRead,
	"GetDAGRunDetails":                remoteNodeAccessRead,
	"GetDAGRunArtifacts":              remoteNodeAccessRead,
	"DownloadDAGRunArtifact":          remoteNodeAccessRead,
	"GetDAGRunArtifactPreview":        remoteNodeAccessRead,
	"GetDAGRunLog":                    remoteNodeAccessRead,
	"DownloadDAGRunLog":               remoteNodeAccessRead,
	"GetDAGRunOutputs":                remoteNodeAccessRead,
	"GetDAGRunSpec":                   remoteNodeAccessRead,
	"DownloadDAGRunStepLogs":          remoteNodeAccessRead,
	"DownloadDAGRunStepLogsForm":      remoteNodeAccessRead,
	"GetDAGRunStepAvailability":       remoteNodeAccessRead,
	"GetDAGRunStepEvents":             remoteNodeAccessRead,
	"GetDAGRunForeachItems":           remoteNodeAccessRead,
	"GetDAGRunForeachItem":            remoteNodeAccessRead,
	"GetDAGRunForeachStepLog":         remoteNodeAccessRead,
	"DownloadDAGRunForeachStepLog":    remoteNodeAccessRead,
	"GetDAGRunStepLog":                remoteNodeAccessRead,
	"DownloadDAGRunStepLog":           remoteNodeAccessRead,
	"GetDAGRunStepMessages":           remoteNodeAccessRead,
	"GetDAGRunStepOutputs":            remoteNodeAccessRead,
	"GetDAGRunStepSamples":            remoteNodeAccessRead,
	"GetSubDAGRuns":                   remoteNodeAccessRead,
	"GetSubDAGRunDetails":             remoteNodeAccessRead,
	"GetSubDAGRunArtifacts":           remoteNodeAccessRead,
	"DownloadSubDAGRunArtifact":       remoteNodeAccessRead,
	"GetSubDAGRunArtifactPreview":     remoteNodeAccessRead,
	"GetSubDAGRunLog":                 remoteNodeAccessRead,
	"DownloadSubDAGRunLog":            remoteNodeAccessRead,
	"GetSubDAGRunSpec":                remoteNodeAccessRead,
	"DownloadSubDAGRunStepLogs":       remoteNodeAccessRead,
	"DownloadSubDAGRunStepLogsForm":   remoteNodeAccessRead,
	"GetSubDAGRunForeachItems":        remoteNodeAccessRead,
	"GetSubDAGRunForeachItem":         remoteNodeAccessRead,
	"GetSubDAGRunForeachStepLog":      remoteNodeAccessRead,
	"DownloadSubDAGRunForeachStepLog": remoteNodeAccessRead,
	"GetSubDAGRunStepLog":             remoteNodeAccessRead,
	"DownloadSubDAGRunStepLog":        remoteNodeAccessRead,
	"GetSubDAGRunStepMessages":        remoteNodeAccessRead,
	"ListDAGs":                        remoteNodeAccessRead,
	"GetAllDAGLabels":                 remoteNodeAccessRead,
	"SearchDAGs":                      remoteNodeAccessRead,
	"GetAllDAGTags":                   remoteNodeAccessRead,
	"ValidateDAGSpec":                 remoteNodeAccessRead,
	"GetDAGDetails":                   remoteNodeAccessRead,
	"GetDAGDAGRunHistory":             remoteNodeAccessRead,
	"GetDAGDAGRunDetails":             remoteNodeAccessRead,
	"GetDAGSettings":                  remoteNodeAccessRead,
	"GetDAGSpec":                      remoteNodeAccessRead,
	"GetHealthStatus":                 remoteNodeAccessRead,
	"GetLicenseStatus":                remoteNodeAccessRead,
	"GetOpenapiJson":                  remoteNodeAccessRead,
	"ListQueues":                      remoteNodeAccessRead,
	"GetQueue":                        remoteNodeAccessRead,
	"ListQueueItems":                  remoteNodeAccessRead,
	"SearchDAGFeed":                   remoteNodeAccessRead,
	"SearchDagMatches":                remoteNodeAccessRead,
	"SearchWikiPageFeed":              remoteNodeAccessRead,
	"SearchWikiPageMatches":           remoteNodeAccessRead,
	"ListServiceDAGs":                 remoteNodeAccessRead,
	"GetSchedulerPauseState":          remoteNodeAccessRead,
	"GetWorkspaceBaseConfig":          remoteNodeAccessRead,
	"GetSyncConfig":                   remoteNodeAccessRead,
	"GetSyncItemDiff":                 remoteNodeAccessRead,
	"GetSyncStatus":                   remoteNodeAccessRead,
	"ListViews":                       remoteNodeAccessRead,
	"GetView":                         remoteNodeAccessRead,
	"ListWikiPages":                   remoteNodeAccessRead,
	"ListWikiPageBacklinks":           remoteNodeAccessRead,
	"GetWikiPage":                     remoteNodeAccessRead,
	"DownloadWikiPageAttachment":      remoteNodeAccessRead,
	"GetWikiPageRevision":             remoteNodeAccessRead,
	"ListWikiPageRevisions":           remoteNodeAccessRead,
	"SearchWikiPages":                 remoteNodeAccessRead,
	"ListWorkspaces":                  remoteNodeAccessRead,
	"GetWorkspace":                    remoteNodeAccessRead,

	// Run control, which needs execute permission.
	"DequeueDAGRun":                        remoteNodeAccessExecute,
	"ResumeHumanTaskDAGRun":                remoteNodeAccessExecute,
	"CompleteHumanTask":                    remoteNodeAccessExecute,
	"PushBackHumanTask":                    remoteNodeAccessExecute,
	"RescheduleDAGRun":                     remoteNodeAccessExecute,
	"ResumeDAGRun":                         remoteNodeAccessExecute,
	"RetryDAGRun":                          remoteNodeAccessExecute,
	"RespondDAGRunStepAgentInteraction":    remoteNodeAccessExecute,
	"RestartDAGRunStepAgentSession":        remoteNodeAccessExecute,
	"ApproveDAGRunStep":                    remoteNodeAccessExecute,
	"PushBackDAGRunStep":                   remoteNodeAccessExecute,
	"RejectDAGRunStep":                     remoteNodeAccessExecute,
	"UpdateDAGRunStepStatus":               remoteNodeAccessExecute,
	"TerminateDAGRun":                      remoteNodeAccessExecute,
	"RespondSubDAGRunStepAgentInteraction": remoteNodeAccessExecute,
	"RestartSubDAGRunStepAgentSession":     remoteNodeAccessExecute,
	"ApproveSubDAGRunStep":                 remoteNodeAccessExecute,
	"PushBackSubDAGRunStep":                remoteNodeAccessExecute,
	"RejectSubDAGRunStep":                  remoteNodeAccessExecute,
	"UpdateSubDAGRunStepStatus":            remoteNodeAccessExecute,
	"ClearDAGBrowserCache":                 remoteNodeAccessExecute,
	"EnqueueDAGDAGRun":                     remoteNodeAccessExecute,
	"ExecuteDAG":                           remoteNodeAccessExecute,
	"ExecuteDAGSync":                       remoteNodeAccessExecute,
	"StopAllDAGRuns":                       remoteNodeAccessExecute,
	"UpdateDAGSuspensionState":             remoteNodeAccessExecute,

	// Changes to DAGs, calendars, Wiki pages, and Git sync, which need write
	// permission.
	"CreateCalendar":           remoteNodeAccessWrite,
	"DeleteCalendar":           remoteNodeAccessWrite,
	"UpdateCalendar":           remoteNodeAccessWrite,
	"DeleteDAGRun":             remoteNodeAccessWrite,
	"CreateNewDAG":             remoteNodeAccessWrite,
	"DeleteDAG":                remoteNodeAccessWrite,
	"RenameDAG":                remoteNodeAccessWrite,
	"UpdateDAGSpec":            remoteNodeAccessWrite,
	"SyncCleanup":              remoteNodeAccessWrite,
	"SyncDeleteBatch":          remoteNodeAccessWrite,
	"SyncDeleteMissing":        remoteNodeAccessWrite,
	"DeleteSyncItem":           remoteNodeAccessWrite,
	"DiscardSyncItemChanges":   remoteNodeAccessWrite,
	"ForgetSyncItem":           remoteNodeAccessWrite,
	"MoveSyncItem":             remoteNodeAccessWrite,
	"PublishSyncItem":          remoteNodeAccessWrite,
	"SyncPublishAll":           remoteNodeAccessWrite,
	"SyncPull":                 remoteNodeAccessWrite,
	"CreateWikiPage":           remoteNodeAccessWrite,
	"DeleteWikiPageBatch":      remoteNodeAccessWrite,
	"DeleteWikiPage":           remoteNodeAccessWrite,
	"UpdateWikiPage":           remoteNodeAccessWrite,
	"UploadWikiPageAttachment": remoteNodeAccessWrite,
	"RenameWikiPage":           remoteNodeAccessWrite,

	// Runs from an inline or edited spec, which need write and run permission.
	"ExecuteDAGRunFromSpec":  remoteNodeAccessWriteAndRun,
	"EnqueueDAGRunFromSpec":  remoteNodeAccessWriteAndRun,
	"EditRetryDAGRun":        remoteNodeAccessWriteAndRun,
	"PreviewEditRetryDAGRun": remoteNodeAccessWriteAndRun,

	// System status and workspace changes, open to developers and above.
	"GetCoordinatorStatus": remoteNodeAccessDeveloper,
	"GetResourceHistory":   remoteNodeAccessDeveloper,
	"GetSchedulerStatus":   remoteNodeAccessDeveloper,
	"GetTunnelStatus":      remoteNodeAccessDeveloper,
	"GetWorkers":           remoteNodeAccessDeveloper,
	"CreateWorkspace":      remoteNodeAccessDeveloper,
	"DeleteWorkspace":      remoteNodeAccessDeveloper,
	"UpdateWorkspace":      remoteNodeAccessDeveloper,

	// Audit and event logs, open to managers and above.
	"ListAuditLogs": remoteNodeAccessManager,
	"ListEventLogs": remoteNodeAccessManager,

	// Endpoints that act on the stored credential itself or authenticate by
	// other means.
	"ChangePassword": remoteNodeAccessDenied,
	"Login":          remoteNodeAccessDenied,
	"Setup":          remoteNodeAccessDenied,
	"GetMetrics":     remoteNodeAccessDenied,
	"TriggerWebhook": remoteNodeAccessDenied,
}

// remoteNodeAuthorization rejects remote node requests the caller could not
// make locally.
func (a *API) remoteNodeAuthorization(swagger *openapi3.T) (func(http.Handler) http.Handler, error) {
	router, err := gorillamux.NewRouter(swagger)
	if err != nil {
		return nil, fmt.Errorf("failed to build the remote node operation router: %w", err)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !isRemoteNodeRequest(r) {
				next.ServeHTTP(w, r)
				return
			}
			if err := a.requireRemoteNodeAccess(r.Context(), remoteNodeAccessFor(router, r)); err != nil {
				WriteErrorResponse(w, err)
				return
			}
			next.ServeHTTP(w, r)
		})
	}, nil
}

// RemoteNodeStreamAuthorization rejects live update streams for a remote node
// unless the caller may read that node's workflows.
func (a *API) RemoteNodeStreamAuthorization(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isRemoteNodeRequest(r) {
			if err := a.requireRemoteNodeAccess(r.Context(), remoteNodeAccessRead); err != nil {
				WriteErrorResponse(w, err)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func isRemoteNodeRequest(r *http.Request) bool {
	remoteNode := r.URL.Query().Get("remoteNode")
	return remoteNode != "" && remoteNode != "local"
}

// remoteNodeAccessFor returns the access the request's operation needs. A
// request that matches no operation needs an administrator.
func remoteNodeAccessFor(router routers.Router, r *http.Request) remoteNodeAccess {
	route, _, err := router.FindRoute(r)
	if err != nil {
		return remoteNodeAccessAdmin
	}
	return remoteNodeOperationAccess[route.Operation.OperationID]
}

// requireRemoteNodeAccess checks the caller against access. Server permissions
// always apply; role and workspace checks apply only where rolesApply does.
func (a *API) requireRemoteNodeAccess(ctx context.Context, access remoteNodeAccess) error {
	switch access {
	case remoteNodeAccessDenied:
		return errRemoteNodeOperationUnavailable
	case remoteNodeAccessExecute:
		if err := a.isAllowed(config.PermissionRunDAGs); err != nil {
			return err
		}
	case remoteNodeAccessWrite:
		if err := a.isAllowed(config.PermissionWriteDAGs); err != nil {
			return err
		}
	case remoteNodeAccessWriteAndRun:
		if err := a.isAllowed(config.PermissionWriteDAGs); err != nil {
			return err
		}
		if err := a.isAllowed(config.PermissionRunDAGs); err != nil {
			return err
		}
	case remoteNodeAccessAdmin, remoteNodeAccessRead, remoteNodeAccessDeveloper, remoteNodeAccessManager:
	}

	if !a.rolesApply(ctx) {
		return nil
	}
	user, ok := auth.UserFromContext(ctx)
	if !ok {
		return errAuthRequired
	}
	// Local workspace grants say nothing about a remote node's workspaces.
	if !auth.NormalizeWorkspaceAccess(user.WorkspaceAccess).All {
		return errInsufficientPermissions
	}
	if !access.allows(user.Role) {
		return errInsufficientPermissions
	}
	return nil
}

// allows reports whether role meets access. Denied operations are rejected
// before roles are checked, so they fall through to the admin rule here.
func (access remoteNodeAccess) allows(role auth.Role) bool {
	switch access {
	case remoteNodeAccessRead:
		return true
	case remoteNodeAccessExecute:
		return role.CanExecute()
	case remoteNodeAccessWrite, remoteNodeAccessWriteAndRun, remoteNodeAccessDeveloper:
		return role.CanWrite()
	case remoteNodeAccessManager:
		return role.CanManageAudit()
	case remoteNodeAccessAdmin, remoteNodeAccessDenied:
	}
	return role.IsAdmin()
}
