// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/service/frontend/api/pathutil"
	"github.com/dagucloud/dagu/v2/internal/service/frontend/extauthz"
	"github.com/go-chi/chi/v5"
)

// WithExternalAuthorizer enables the external authorization hook
// (auth.external) on the REST API.
func WithExternalAuthorizer(authz *extauthz.Authorizer) APIOption {
	return func(a *API) {
		a.externalAuthz = authz
	}
}

var errExternalDenied = &Error{
	HTTPStatus: http.StatusForbidden,
	Code:       api.ErrorCodeForbidden,
	Message:    "Denied by the external authorization policy",
}

// externalScope tells how the PDP resource is built from the request.
type externalScope int

const (
	// scopeServer sends the server resource.
	scopeServer externalScope = iota
	// scopeDAGFile sends the workflow named by the fileName path parameter.
	scopeDAGFile
	// scopeDAGRun sends the workflow named by the name path parameter and
	// the dagRunId path parameter as the run_id property.
	scopeDAGRun
)

type externalOperation struct {
	action extauthz.Action
	scope  externalScope
}

func externalOps(action extauthz.Action, scope externalScope, operationIDs ...string) map[string]externalOperation {
	ops := make(map[string]externalOperation, len(operationIDs))
	for _, id := range operationIDs {
		ops[id] = externalOperation{action: action, scope: scope}
	}
	return ops
}

// externalOperations maps REST operations to PDP actions. An operation that
// is not listed here is sent as settings.read (GET) or settings.write (other
// methods) on the server resource.
var externalOperations = mergeExternalOps(
	externalOps(extauthz.ActionWorkflowsRead, scopeServer,
		"ListDAGs", "SearchDAGs", "SearchDAGFeed", "GetAllDAGLabels", "GetAllDAGTags",
		"ValidateDAGSpec", "ListWebhooks"),
	externalOps(extauthz.ActionWorkflowsRead, scopeDAGFile,
		"GetDAGDetails", "GetDAGSpec", "SearchDagMatches", "GetDAGWebhook", "GetDAGWebhookPublic",
		"GetDAGSettings", "GetDAGNotifications", "GetDAGIncidents"),
	externalOps(extauthz.ActionWorkflowsWrite, scopeServer,
		"CreateNewDAG", "ExecuteDAGRunFromSpec", "EnqueueDAGRunFromSpec"),
	externalOps(extauthz.ActionWorkflowsWrite, scopeDAGFile,
		"DeleteDAG", "UpdateDAGSpec", "RenameDAG", "UpdateDAGSuspensionState", "ClearDAGBrowserCache",
		"CreateDAGWebhook", "DeleteDAGWebhook", "RegenerateDAGWebhookToken",
		"ConfigureDAGWebhookProfileSelection", "CreateDAGWebhookProfileToken", "RevokeDAGWebhookProfileToken",
		"ToggleDAGWebhook", "EnableDAGWebhookHMAC", "ConfigureDAGWebhookHMAC",
		"RegenerateDAGWebhookHMACSecret", "DisableDAGWebhookHMAC", "EnableDAGWebhookPublic",
		"DisableDAGWebhookPublic", "RegenerateDAGWebhookPublicURL", "ConfigureDAGWebhookPublicSender",
		"DismissDAGWebhookPublicMissed", "UpdateDAGSettings", "DeleteDAGSettings", "PinDAG", "UnpinDAG",
		"UpdateDAGNotifications", "DeleteDAGNotifications", "TestDAGNotifications",
		"UpdateDAGIncidents", "DeleteDAGIncidents"),
	externalOps(extauthz.ActionRunsStart, scopeDAGFile,
		"ExecuteDAG", "ExecuteDAGSync", "EnqueueDAGDAGRun"),
	externalOps(extauthz.ActionRunsRead, scopeServer,
		"ListDAGRuns", "GetDAGRunActivity", "ListArtifacts"),
	externalOps(extauthz.ActionRunsRead, scopeDAGFile,
		"GetDAGDAGRunHistory", "GetDAGDAGRunDetails"),
	externalOps(extauthz.ActionRunsRead, scopeDAGRun,
		"ListDAGRunsByName", "GetDAGRunDetails", "GetDAGRunSpec", "GetSubDAGRuns",
		"GetDAGRunArtifacts", "GetDAGRunArtifactPreview", "DownloadDAGRunArtifact", "GetDAGRunOutputs",
		"GetDAGRunForeachItems", "GetDAGRunForeachItem", "GetDAGRunStepMessages",
		"GetSubDAGRunDetails", "GetSubDAGRunSpec", "GetSubDAGRunArtifacts", "GetSubDAGRunArtifactPreview",
		"DownloadSubDAGRunArtifact", "GetSubDAGRunForeachItems", "GetSubDAGRunForeachItem",
		"GetSubDAGRunStepMessages"),
	externalOps(extauthz.ActionLogsRead, scopeDAGRun,
		"GetDAGRunLog", "DownloadDAGRunLog", "GetDAGRunStepLog", "DownloadDAGRunStepLog",
		"GetDAGRunForeachStepLog", "DownloadDAGRunForeachStepLog", "DownloadDAGRunStepLogs",
		"DownloadDAGRunStepLogsForm", "GetSubDAGRunLog", "DownloadSubDAGRunLog", "GetSubDAGRunStepLog",
		"DownloadSubDAGRunStepLog", "GetSubDAGRunForeachStepLog", "DownloadSubDAGRunForeachStepLog",
		"DownloadSubDAGRunStepLogs", "DownloadSubDAGRunStepLogsForm"),
	externalOps(extauthz.ActionRunsStop, scopeDAGFile, "StopAllDAGRuns"),
	externalOps(extauthz.ActionRunsStop, scopeDAGRun, "TerminateDAGRun", "DequeueDAGRun"),
	externalOps(extauthz.ActionRunsRetry, scopeDAGRun,
		"RetryDAGRun", "RescheduleDAGRun", "PreviewEditRetryDAGRun", "EditRetryDAGRun",
		"ResumeDAGRun", "ResumeHumanTaskDAGRun"),
	externalOps(extauthz.ActionRunsUpdate, scopeDAGRun,
		"CompleteHumanTask", "PushBackHumanTask", "UpdateDAGRunStepStatus", "ApproveDAGRunStep",
		"RejectDAGRunStep", "PushBackDAGRunStep", "RespondDAGRunStepAgentInteraction",
		"RestartDAGRunStepAgentSession", "UpdateSubDAGRunStepStatus", "ApproveSubDAGRunStep",
		"RejectSubDAGRunStep", "PushBackSubDAGRunStep", "RespondSubDAGRunStepAgentInteraction",
		"RestartSubDAGRunStepAgentSession"),
	externalOps(extauthz.ActionRunsDelete, scopeDAGRun, "DeleteDAGRun"),
	externalOps(extauthz.ActionQueuesRead, scopeServer, "ListQueues", "GetQueue", "ListQueueItems"),
	externalOps(extauthz.ActionMetricsRead, scopeServer,
		"GetMetrics", "GetResourceHistory", "GetWorkers", "GetSchedulerStatus",
		"GetSchedulerPauseState", "GetCoordinatorStatus", "GetTunnelStatus"),
)

func mergeExternalOps(sets ...map[string]externalOperation) map[string]externalOperation {
	merged := map[string]externalOperation{}
	for _, set := range sets {
		for id, op := range set {
			if _, dup := merged[id]; dup {
				panic("duplicate external authorization mapping for " + id)
			}
			merged[id] = op
		}
	}
	return merged
}

// externalPublicOperations are the operations that skip authentication in
// every auth mode; the hook does not apply to them either.
var externalPublicOperations = map[string]bool{
	"GetHealthStatus": true,
	"Login":           true,
	"Setup":           true,
	"TriggerWebhook":  true,
}

func externalOperationFor(operationID, method string) externalOperation {
	if op, ok := externalOperations[operationID]; ok {
		return op
	}
	if method == http.MethodGet || method == http.MethodHead {
		return externalOperation{action: extauthz.ActionSettingsRead, scope: scopeServer}
	}
	return externalOperation{action: extauthz.ActionSettingsWrite, scope: scopeServer}
}

func (op externalOperation) resource(r *http.Request) extauthz.Resource {
	switch op.scope {
	case scopeServer:
		return extauthz.ServerResource()
	case scopeDAGFile:
		return extauthz.WorkflowResource(pathParam(r, "fileName"), pathParam(r, "dagRunId"))
	case scopeDAGRun:
		return extauthz.WorkflowResource(pathParam(r, "name"), pathParam(r, "dagRunId"))
	}
	return extauthz.ServerResource()
}

func pathParam(r *http.Request, name string) string {
	raw := chi.URLParam(r, name)
	if value, err := url.PathUnescape(raw); err == nil {
		return value
	}
	return raw
}

func (a *API) isExternalPublicOperation(operationID string) bool {
	if externalPublicOperations[operationID] {
		return true
	}
	return operationID == "GetMetrics" && a.config.Server.Metrics == config.MetricsAccessPublic
}

// externalAuthorizationMiddleware asks the PDP before each REST operation.
// The subject comes from externalIdentityMiddleware.
func (a *API) externalAuthorizationMiddleware() api.StrictMiddlewareFunc {
	return func(next api.StrictHandlerFunc, operationID string) api.StrictHandlerFunc {
		if a.externalAuthz == nil || a.isExternalPublicOperation(operationID) {
			return next
		}
		return func(ctx context.Context, w http.ResponseWriter, r *http.Request, request any) (any, error) {
			subject, ok := extauthz.SubjectFromContext(ctx)
			if !ok {
				return nil, errAuthRequired
			}
			op := externalOperationFor(operationID, r.Method)
			if err := a.externalAuthz.Check(ctx, subject, op.action, op.resource(r)); err != nil {
				return nil, errExternalDenied
			}
			response, err := next(ctx, w, r, request)
			if err != nil {
				return response, err
			}
			if list, ok := response.(api.ListDAGs200JSONResponse); ok {
				return a.filterExternalDAGList(ctx, subject, list)
			}
			return response, nil
		}
	}
}

// filterExternalDAGList keeps the workflows that the subject may read. The
// pagination counters still describe the unfiltered page.
func (a *API) filterExternalDAGList(ctx context.Context, subject extauthz.Subject, list api.ListDAGs200JSONResponse) (any, error) {
	resources := make([]extauthz.Resource, len(list.Dags))
	for i, dag := range list.Dags {
		resources[i] = extauthz.WorkflowResource(dag.FileName, "")
	}
	decisions, err := a.externalAuthz.Allowed(ctx, subject, extauthz.ActionWorkflowsRead, resources)
	if err != nil {
		return nil, errExternalDenied
	}
	kept := list.Dags[:0:0]
	for i, dag := range list.Dags {
		if decisions[i] {
			kept = append(kept, dag)
		}
	}
	list.Dags = kept
	return list, nil
}

// externalIdentityMiddleware verifies the identity token before the request
// body is read, and authorizes requests that are forwarded to a remote node,
// which the per-operation check never sees.
func (a *API) externalIdentityMiddleware(publicPaths, publicPrefixes []string) func(http.Handler) http.Handler {
	if a.externalAuthz == nil {
		return func(next http.Handler) http.Handler { return next }
	}
	public := make(map[string]struct{}, len(publicPaths))
	for _, p := range publicPaths {
		public[pathutil.NormalizePath(p)] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			normalized := pathutil.NormalizePath(r.URL.Path)
			if _, ok := public[normalized]; ok {
				next.ServeHTTP(w, r)
				return
			}
			for _, prefix := range publicPrefixes {
				if strings.HasPrefix(normalized, prefix) {
					next.ServeHTTP(w, r)
					return
				}
			}

			subject, err := a.externalAuthz.Authenticate(r)
			if err != nil {
				logger.Debug(r.Context(), "External identity token rejected", tag.Error(err))
				WriteErrorResponse(w, errAuthRequired)
				return
			}
			ctx := extauthz.WithSubject(r.Context(), subject)

			if node := r.URL.Query().Get("remoteNode"); node != "" && node != "local" {
				if err := a.externalAuthz.Check(ctx, subject, extauthz.ActionRemoteNodesUse, extauthz.RemoteNodeResource(node)); err != nil {
					WriteErrorResponse(w, errExternalDenied)
					return
				}
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
