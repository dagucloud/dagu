// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

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
	"GetDAGListActivity":              remoteNodeAccessRead,
	"GetDAGRunChanges":                remoteNodeAccessRead,
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
	"GetDAGRunNodeRuns":               remoteNodeAccessRead,
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
	"GetSubDAGRunNodeRuns":            remoteNodeAccessRead,
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
	"GetDAGMissedSlots":               remoteNodeAccessRead,
	"GetDAGDAGRunHistory":             remoteNodeAccessRead,
	"GetDAGRunHistorySamples":         remoteNodeAccessRead,
	"GetDAGRunHistoryEvents":          remoteNodeAccessRead,
	"GetDAGRunHistoryAvailability":    remoteNodeAccessRead,
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
	"ListSkills":                      remoteNodeAccessRead,
	"GetSkill":                        remoteNodeAccessRead,
	"GetSkillFile":                    remoteNodeAccessRead,
	"DownloadSkill":                   remoteNodeAccessRead,
	"DownloadSkills":                  remoteNodeAccessRead,
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
	"PreviewExecuteDAG":                    remoteNodeAccessExecute,
	"ExecuteDAGSync":                       remoteNodeAccessExecute,
	"StopAllDAGRuns":                       remoteNodeAccessExecute,
	"UpdateDAGSuspensionState":             remoteNodeAccessExecute,
	"DismissDAGMissedSlots":                remoteNodeAccessExecute,

	// Changes to DAGs, calendars, skills, Wiki pages, and Git sync, which need
	// write permission.
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
	"UpdateSkill":              remoteNodeAccessWrite,
	"DeleteSkill":              remoteNodeAccessWrite,
	"PutSkillFile":             remoteNodeAccessWrite,
	"DeleteSkillFile":          remoteNodeAccessWrite,
	"CreateWikiPage":           remoteNodeAccessWrite,
	"DeleteWikiPageBatch":      remoteNodeAccessWrite,
	"DeleteWikiPage":           remoteNodeAccessWrite,
	"UpdateWikiPage":           remoteNodeAccessWrite,
	"UploadWikiPageAttachment": remoteNodeAccessWrite,
	"RenameWikiPage":           remoteNodeAccessWrite,

	// Runs from an inline or edited spec, which need write and run permission.
	"ExecuteDAGRunFromSpec":        remoteNodeAccessWriteAndRun,
	"PreviewExecuteDAGRunFromSpec": remoteNodeAccessWriteAndRun,
	"EnqueueDAGRunFromSpec":        remoteNodeAccessWriteAndRun,
	"EditRetryDAGRun":              remoteNodeAccessWriteAndRun,
	"PreviewEditRetryDAGRun":       remoteNodeAccessWriteAndRun,

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
	"ChangePassword":        remoteNodeAccessDenied,
	"Login":                 remoteNodeAccessDenied,
	"Setup":                 remoteNodeAccessDenied,
	"GetMetrics":            remoteNodeAccessDenied,
	"TriggerWebhook":        remoteNodeAccessDenied,
	"TriggerProjectWebhook": remoteNodeAccessDenied,

	// Node inventories, host key trust, and node connection tests, which
	// remote nodes do not support.
	"ListInventoryNodes":           remoteNodeAccessDenied,
	"GetInventoryNode":             remoteNodeAccessDenied,
	"ListInventoryNodeLabels":      remoteNodeAccessDenied,
	"ListInventories":              remoteNodeAccessDenied,
	"GetInventory":                 remoteNodeAccessDenied,
	"ImportInventories":            remoteNodeAccessDenied,
	"CreateInventory":              remoteNodeAccessDenied,
	"ReplaceInventory":             remoteNodeAccessDenied,
	"DeleteInventory":              remoteNodeAccessDenied,
	"PutInventoryNode":             remoteNodeAccessDenied,
	"AddInventoryNode":             remoteNodeAccessDenied,
	"DeleteInventoryNode":          remoteNodeAccessDenied,
	"TestInventoryNodeConnections": remoteNodeAccessDenied,
	"TestNewInventoryNode":         remoteNodeAccessDenied,
	"TrustInventoryNodeHostKeys":   remoteNodeAccessDenied,
	"ForgetWorkerNode":             remoteNodeAccessDenied,

	// Connections and OAuth apps, which each server keeps for itself with
	// its own callback URL.
	"ListOAuthApps":    remoteNodeAccessDenied,
	"CreateOAuthApp":   remoteNodeAccessDenied,
	"GetOAuthApp":      remoteNodeAccessDenied,
	"UpdateOAuthApp":   remoteNodeAccessDenied,
	"DeleteOAuthApp":   remoteNodeAccessDenied,
	"ListConnections":  remoteNodeAccessDenied,
	"CreateConnection": remoteNodeAccessDenied,
	"GetConnection":    remoteNodeAccessDenied,
	"UpdateConnection": remoteNodeAccessDenied,
	"DeleteConnection": remoteNodeAccessDenied,

	"ListConnectionProviders":         remoteNodeAccessDenied,
	"StartConnectionAuthorization":    remoteNodeAccessDenied,
	"GetConnectionAuthorization":      remoteNodeAccessDenied,
	"CancelConnectionAuthorization":   remoteNodeAccessDenied,
	"CompleteConnectionAuthorization": remoteNodeAccessDenied,
	"ConfirmConnectionAuthorization":  remoteNodeAccessDenied,
	"TestConnection":                  remoteNodeAccessDenied,
	"ListConnectionTools":             remoteNodeAccessDenied,
	"GetConnectionAttention":          remoteNodeAccessDenied,
}

// remoteNodeProfileOperations take a runtime profile in the request body.
// Running with a protected profile needs an administrator, and only the remote
// node knows which of its profiles are protected.
var remoteNodeProfileOperations = map[string]bool{
	"ExecuteDAG":                   true,
	"PreviewExecuteDAG":            true,
	"ExecuteDAGSync":               true,
	"EnqueueDAGDAGRun":             true,
	"ExecuteDAGRunFromSpec":        true,
	"PreviewExecuteDAGRunFromSpec": true,
	"EnqueueDAGRunFromSpec":        true,
}

// maxRemoteNodeProfileProbeBody bounds how much of a run request body is read
// to find the runtime profile it names.
const maxRemoteNodeProfileProbeBody = 10 << 20

// remoteNodeAuthorization rejects remote node requests the caller could not
// make locally.
func (a *API) remoteNodeAuthorization(swagger *openapi3.T, apiBasePath string) (func(http.Handler) http.Handler, error) {
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
			operationID := remoteNodeOperationFor(router, r)
			if err := a.requireRemoteNodeAccess(r.Context(), remoteNodeOperationAccess[operationID]); err != nil {
				WriteErrorResponse(w, err)
				return
			}
			if remoteNodeProfileOperations[operationID] {
				if err := a.requireRemoteNodeProfileAccess(r, apiBasePath); err != nil {
					WriteErrorResponse(w, err)
					return
				}
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

// remoteNodeOperationFor returns the request's OpenAPI operation ID, or an
// empty string, which needs an administrator, when it matches none.
func remoteNodeOperationFor(router routers.Router, r *http.Request) string {
	route, _, err := router.FindRoute(r)
	if err != nil {
		return ""
	}
	return route.Operation.OperationID
}

// requireRemoteNodeProfileAccess rejects a run request that names a protected
// runtime profile of the remote node unless the caller is an administrator.
func (a *API) requireRemoteNodeProfileAccess(r *http.Request, apiBasePath string) error {
	if a.requireAdmin(r.Context()) == nil {
		return nil
	}
	profile, err := requestedRunProfile(r)
	if err != nil || profile == "" {
		return err
	}
	protected, err := a.remoteNodeProfileProtected(r, apiBasePath, profile)
	if err != nil {
		return err
	}
	if protected {
		return errInsufficientPermissions
	}
	return nil
}

// requestedRunProfile returns the runtime profile a run request body names and
// leaves the body in place for the proxy to forward.
func requestedRunProfile(r *http.Request) (string, error) {
	if r.Body == nil {
		return "", nil
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxRemoteNodeProfileProbeBody+1))
	if err != nil {
		return "", fmt.Errorf("failed to read request body: %w", err)
	}
	if len(body) > maxRemoteNodeProfileProbeBody {
		return "", &Error{
			HTTPStatus: http.StatusRequestEntityTooLarge,
			Code:       api.ErrorCodeBadRequest,
			Message:    "request body is too large to forward to a remote node",
		}
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	if len(bytes.TrimSpace(body)) == 0 {
		return "", nil
	}

	var request struct {
		Profile *string `json:"profile"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		return "", &Error{
			HTTPStatus: http.StatusBadRequest,
			Code:       api.ErrorCodeBadRequest,
			Message:    "invalid request body",
		}
	}
	if request.Profile == nil {
		return "", nil
	}
	return strings.TrimSpace(*request.Profile), nil
}

// remoteNodeProfileProtected asks the remote node of r whether its runtime
// profile name is protected, as far as the node's stored credential matters.
func (a *API) remoteNodeProfileProtected(r *http.Request, apiBasePath, name string) (bool, error) {
	ctx := r.Context()
	if a.remoteNodeResolver == nil {
		return false, errRemoteNodeProfileCheck(name)
	}
	node, err := a.remoteNodeResolver.GetByName(ctx, r.URL.Query().Get("remoteNode"))
	if err != nil {
		return false, errRemoteNodeProfileCheck(name)
	}

	profilePath := strings.TrimRight(apiBasePath, "/") + "/profiles/" + url.PathEscape(name)
	probe, err := http.NewRequestWithContext(ctx, http.MethodGet, profilePath, nil)
	if err != nil {
		return false, errRemoteNodeProfileCheck(name)
	}
	// The probe carries the headers the run is forwarded with, so a proxy in
	// front of the remote node authenticates both the same way.
	probe.Header = r.Header.Clone()
	probe.Header.Del("Content-Length")
	probe.Header.Del("Transfer-Encoding")
	proxy := &remoteNodeProxy{remoteNode: node, apiBasePath: apiBasePath}
	resp, err := proxy.doRequest(nil, probe)
	if err != nil {
		return false, errRemoteNodeProfileCheck(name)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		// A 403 means the credential is below administrator, and the remote node
		// refuses a protected profile to such a credential itself. A 401 means
		// the node does not accept the credential, so it refuses the run too.
		return false, nil
	case http.StatusNotFound:
		return false, &Error{
			HTTPStatus: http.StatusNotFound,
			Code:       api.ErrorCodeNotFound,
			Message:    fmt.Sprintf("runtime profile %s not found", name),
		}
	default:
		return false, errRemoteNodeProfileCheck(name)
	}

	reader, closeReader, err := remoteNodeResponseReader(resp)
	if err != nil {
		return false, errRemoteNodeProfileCheck(name)
	}
	defer closeReader()
	var profile struct {
		Protected bool `json:"protected"`
	}
	if err := json.NewDecoder(io.LimitReader(reader, 1<<20)).Decode(&profile); err != nil {
		return false, errRemoteNodeProfileCheck(name)
	}
	return profile.Protected, nil
}

func errRemoteNodeProfileCheck(name string) error {
	return &Error{
		HTTPStatus: http.StatusBadGateway,
		Code:       api.ErrorCodeBadGateway,
		Message:    fmt.Sprintf("could not check runtime profile %s on the remote node", name),
	}
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
