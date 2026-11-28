// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"net/http"
	"slices"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/audit"
	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
)

const (
	// coordinatorUnreachableMessage answers a call that reached no
	// coordinator.
	coordinatorUnreachableMessage = "no coordinator is running or reachable"
	// peerTokenMissingMessage answers a call the coordinator refused for
	// lack of the peer token.
	peerTokenMissingMessage = "the coordinator refused the server: set peer.token, or run the server where it reads the coordinator's data directory"
	// olderCoordinatorMessage answers a call that only coordinators of an
	// earlier release received.
	olderCoordinatorMessage = "the coordinator runs an earlier Dagu release without worker invites: upgrade it"
)

var workerInviteKinds = map[coordinatorv1.InviteKind]api.WorkerInviteKind{
	coordinatorv1.InviteKind_INVITE_KIND_SINGLE_USE: api.WorkerInviteKindSingleUse,
	coordinatorv1.InviteKind_INVITE_KIND_REUSABLE:   api.WorkerInviteKindReusable,
}

var configSourceKinds = map[coordinatorv1.ConfigSourceKind]api.ConfigSourceKind{
	coordinatorv1.ConfigSourceKind_CONFIG_SOURCE_KIND_DEFAULT: api.ConfigSourceKindDefault,
	coordinatorv1.ConfigSourceKind_CONFIG_SOURCE_KIND_FILE:    api.ConfigSourceKindFile,
	coordinatorv1.ConfigSourceKind_CONFIG_SOURCE_KIND_ENV:     api.ConfigSourceKindEnv,
	coordinatorv1.ConfigSourceKind_CONFIG_SOURCE_KIND_FLAG:    api.ConfigSourceKindFlag,
}

var hostEnvironments = map[coordinatorv1.HostEnvironment]api.HostEnvironment{
	coordinatorv1.HostEnvironment_HOST_ENVIRONMENT_HOST:       api.HostEnvironmentHost,
	coordinatorv1.HostEnvironment_HOST_ENVIRONMENT_CONTAINER:  api.HostEnvironmentContainer,
	coordinatorv1.HostEnvironment_HOST_ENVIRONMENT_KUBERNETES: api.HostEnvironmentKubernetes,
}

var builtinTLSReasons = map[coordinatorv1.BuiltinTLSReason]api.BuiltinTLSReason{
	coordinatorv1.BuiltinTLSReason_BUILTIN_TLS_REASON_OWN_CERTIFICATES: api.BuiltinTLSReasonOwnCertificates,
	coordinatorv1.BuiltinTLSReason_BUILTIN_TLS_REASON_CA_UNAVAILABLE:   api.BuiltinTLSReasonCaUnavailable,
}

var errNoCoordinator = &Error{
	HTTPStatus: http.StatusServiceUnavailable,
	Code:       api.ErrorCodeInternalError,
	Message:    coordinatorUnreachableMessage,
}

// CreateWorkerInvite implements the createWorkerInvite operation. Its
// response is the only one that contains the invite.
func (a *API) CreateWorkerInvite(ctx context.Context, request api.CreateWorkerInviteRequestObject) (api.CreateWorkerInviteResponseObject, error) {
	client, err := a.workerAccess(ctx)
	if err != nil {
		return nil, err
	}
	req := &coordinatorv1.CreateInviteRequest{}
	if user, ok := auth.UserFromContext(ctx); ok && user != nil {
		req.CreatedBy = user.Username
	}
	if body := request.Body; body != nil {
		req.Reusable = valueOf(body.Reusable)
		req.Address = valueOf(body.Address)
		if body.ExpiresIn != nil {
			if req.ExpiresInSeconds, err = expiresInSeconds(*body.ExpiresIn); err != nil {
				return nil, err
			}
		}
	}
	resp, err := client.CreateInvite(ctx, req)
	if err != nil {
		return nil, coordinatorCallError(err)
	}
	record := resp.GetRecord()
	details := map[string]any{"invite_id": record.GetId(), "kind": string(workerInviteKinds[record.GetKind()])}
	if record.GetAddress() != "" {
		details["address"] = record.GetAddress()
	}
	a.logAudit(ctx, audit.CategorySystem, "worker_invite_create", details)
	return api.CreateWorkerInvite201JSONResponse{
		Id:        record.GetId(),
		Invite:    resp.GetInvite(),
		ExpiresAt: unixTime(record.GetExpiresAt()),
	}, nil
}

// ListWorkerInvites implements the listWorkerInvites operation.
func (a *API) ListWorkerInvites(ctx context.Context, _ api.ListWorkerInvitesRequestObject) (api.ListWorkerInvitesResponseObject, error) {
	client, err := a.workerAccess(ctx)
	if err != nil {
		return nil, err
	}
	records, err := client.ListInvites(ctx)
	if err != nil {
		return nil, coordinatorCallError(err)
	}
	invites := make([]api.WorkerInvite, 0, len(records))
	for _, record := range records {
		invites = append(invites, workerInvite(record))
	}
	return api.ListWorkerInvites200JSONResponse{Invites: invites}, nil
}

// DeleteWorkerInvite implements the deleteWorkerInvite operation.
func (a *API) DeleteWorkerInvite(ctx context.Context, request api.DeleteWorkerInviteRequestObject) (api.DeleteWorkerInviteResponseObject, error) {
	client, err := a.workerAccess(ctx)
	if err != nil {
		return nil, err
	}
	if err := client.DeleteInvite(ctx, request.InviteId); err != nil {
		return nil, coordinatorCallError(err)
	}
	a.logAudit(ctx, audit.CategorySystem, "worker_invite_delete", map[string]any{"invite_id": request.InviteId})
	return api.DeleteWorkerInvite204Response{}, nil
}

// RemoveWorker implements the removeWorker operation.
func (a *API) RemoveWorker(context.Context, api.RemoveWorkerRequestObject) (api.RemoveWorkerResponseObject, error) {
	return nil, &Error{
		HTTPStatus: http.StatusNotImplemented,
		Code:       api.ErrorCodeInternalError,
		Message:    "not implemented",
	}
}

// GetWorkerJoinReadiness implements the getWorkerJoinReadiness operation. It
// asks every coordinator in the registry for its report, and one of them
// for the invites.
func (a *API) GetWorkerJoinReadiness(ctx context.Context, _ api.GetWorkerJoinReadinessRequestObject) (api.GetWorkerJoinReadinessResponseObject, error) {
	if err := a.requireAdmin(ctx); err != nil {
		return nil, err
	}
	readiness, ok := a.coordinatorCli.(coordinator.JoinReadinessClient)
	if !ok {
		return nil, errNoCoordinator
	}
	access, ok := a.coordinatorCli.(coordinator.InviteClient)
	if !ok {
		return nil, errNoCoordinator
	}
	reports, err := readiness.GetJoinReadiness(ctx)
	if err != nil {
		logger.Debug(ctx, "Failed to list coordinators for join readiness", tag.Error(err))
		return nil, errNoCoordinator
	}
	resp := api.GetWorkerJoinReadiness200JSONResponse{Coordinators: make([]api.CoordinatorJoinReadiness, 0, len(reports))}
	var firstErr error
	for _, report := range reports {
		entry := api.CoordinatorJoinReadiness{
			InstanceId: report.Coordinator.ID,
			Host:       report.Coordinator.Host,
			Port:       report.Coordinator.Port,
		}
		if report.Err != nil {
			if firstErr == nil {
				firstErr = report.Err
			}
			entry.Error = &coordinatorCallError(report.Err).Message
		} else {
			entry.Report = joinReadinessReport(report.Report)
		}
		resp.Coordinators = append(resp.Coordinators, entry)
	}
	// With no report, the dialog has nothing to show, so the refusal is the
	// answer.
	if firstErr != nil && !slices.ContainsFunc(resp.Coordinators, func(c api.CoordinatorJoinReadiness) bool { return c.Report != nil }) {
		return nil, coordinatorCallError(firstErr)
	}
	invites, err := access.ListInvites(ctx)
	if err != nil {
		logger.Debug(ctx, "Failed to list worker invites for join readiness", tag.Error(err))
	}
	resp.LastInviteAddress = lastInviteAddress(invites)
	return resp, nil
}

// workerAccess returns the client for the invite and removal calls, once
// the caller is an admin.
func (a *API) workerAccess(ctx context.Context) (coordinator.InviteClient, error) {
	if err := a.requireAdmin(ctx); err != nil {
		return nil, err
	}
	client, ok := a.coordinatorCli.(coordinator.InviteClient)
	if !ok {
		return nil, errNoCoordinator
	}
	return client, nil
}

// coordinatorCallError maps a refused or failed coordinator call to the
// answer of Spec 108 §REST API.
func coordinatorCallError(err error) *Error {
	st, ok := status.FromError(err)
	if !ok {
		return errNoCoordinator
	}
	answer := func(httpStatus int, code api.ErrorCode, message string) *Error {
		return &Error{HTTPStatus: httpStatus, Code: code, Message: message}
	}
	switch st.Code() {
	case codes.InvalidArgument:
		return answer(http.StatusBadRequest, api.ErrorCodeBadRequest, st.Message())
	case codes.NotFound:
		return answer(http.StatusNotFound, api.ErrorCodeNotFound, st.Message())
	case codes.FailedPrecondition:
		return answer(http.StatusConflict, api.ErrorCodeConflict, st.Message())
	case codes.Unimplemented:
		return answer(http.StatusConflict, api.ErrorCodeConflict, olderCoordinatorMessage)
	case codes.Unauthenticated:
		return answer(http.StatusServiceUnavailable, api.ErrorCodeInternalError, peerTokenMissingMessage)
	case codes.Unavailable, codes.DeadlineExceeded:
		return answer(http.StatusServiceUnavailable, api.ErrorCodeInternalError, coordinatorUnreachableMessage+": "+st.Message())
	default:
		return answer(http.StatusInternalServerError, api.ErrorCodeInternalError, "the coordinator failed: "+st.Message())
	}
}

// expiresInSeconds returns the seconds of the duration expiresIn, rounded
// up, or the 400 answer when it is not a positive Go duration.
func expiresInSeconds(expiresIn string) (int64, error) {
	d, err := time.ParseDuration(expiresIn)
	if err != nil || d <= 0 {
		return 0, &Error{
			HTTPStatus: http.StatusBadRequest,
			Code:       api.ErrorCodeBadRequest,
			Message:    "expiresIn must be a positive duration, such as 24h",
		}
	}
	return int64((d + time.Second - 1) / time.Second), nil
}

func workerInvite(record *coordinatorv1.InviteRecord) api.WorkerInvite {
	invite := api.WorkerInvite{
		Id:         record.GetId(),
		Kind:       workerInviteKinds[record.GetKind()],
		CreatedBy:  record.GetCreatedBy(),
		CreatedAt:  time.Unix(record.GetCreatedAt(), 0).UTC().Format(time.RFC3339),
		ExpiresAt:  unixTime(record.GetExpiresAt()),
		UseCount:   int(record.GetUseCount()),
		LastUsedAt: unixTime(record.GetLastUsedAt()),
	}
	if record.GetAddress() != "" {
		invite.Address = new(record.GetAddress())
	}
	return invite
}

func joinReadinessReport(report *coordinatorv1.GetJoinReadinessResponse) *api.JoinReadinessReport {
	result := &api.JoinReadinessReport{
		ListenHost:   report.GetListenHost(),
		Port:         int(report.GetPort()),
		ConfigFile:   report.GetConfigFile(),
		LoopbackOnly: report.GetLoopbackOnly(),
		Addresses:    append([]string{}, report.GetAddresses()...),
		BuiltinTLS:   report.GetBuiltinTls(),
		Ready:        report.GetReady(),
	}
	if kind, ok := configSourceKinds[report.GetHostSource().GetKind()]; ok {
		result.HostSource = &api.ConfigSource{Kind: kind}
		if name := report.GetHostSource().GetName(); name != "" {
			result.HostSource.Name = &name
		}
	}
	if report.GetAdvertise() != "" {
		result.Advertise = new(report.GetAdvertise())
	}
	if environment, ok := hostEnvironments[report.GetEnvironment()]; ok {
		result.Environment = &environment
	}
	if reason, ok := builtinTLSReasons[report.GetBuiltinTlsReason()]; ok {
		result.BuiltinTLSReason = &reason
	}
	return result
}

// lastInviteAddress returns the address of the newest of invites, listed
// oldest first, that has one, or nil.
func lastInviteAddress(invites []*coordinatorv1.InviteRecord) *string {
	for _, invite := range slices.Backward(invites) {
		if invite.GetAddress() != "" {
			return new(invite.GetAddress())
		}
	}
	return nil
}

// unixTime returns the Unix timestamp seconds in RFC3339, or nil for 0.
func unixTime(seconds int64) *string {
	if seconds == 0 {
		return nil
	}
	return new(time.Unix(seconds, 0).UTC().Format(time.RFC3339))
}
