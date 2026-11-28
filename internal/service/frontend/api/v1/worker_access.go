// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"cmp"
	"context"
	"errors"
	"math"
	"net/http"
	"slices"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
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
	// coordinatorRefusedPrefix starts the answer to a call the coordinator
	// refused to the server, before the coordinator's reason, such as a
	// missing peer token or a TLS requirement.
	coordinatorRefusedPrefix = "the coordinator refused the server: "
	// peerTokenMissingAnswer answers a call the coordinator refused for
	// lack of the peer token.
	peerTokenMissingAnswer = coordinatorRefusedPrefix + "it requires the peer token: set peer.token"
	// maxExpiresInSeconds keeps an invite's expiry within what the
	// coordinator accepts.
	maxExpiresInSeconds = math.MaxInt64 / int64(time.Second)
	// olderCoordinatorMessage answers a call that only coordinators of an
	// earlier release received.
	olderCoordinatorMessage = "the coordinator runs an earlier Dagu release without worker invites: upgrade it"
)

var workerInviteKinds = map[coordinatorv1.InviteKind]api.WorkerInviteKind{
	coordinatorv1.InviteKind_INVITE_KIND_SINGLE_USE: api.WorkerInviteKindSingleUse,
	coordinatorv1.InviteKind_INVITE_KIND_REUSABLE:   api.WorkerInviteKindReusable,
}

var workerRemovalRuns = map[api.WorkerRemovalRuns]coordinatorv1.RemoveWorkerRuns{
	api.WorkerRemovalRunsFinish: coordinatorv1.RemoveWorkerRuns_REMOVE_WORKER_RUNS_FINISH,
	api.WorkerRemovalRunsStop:   coordinatorv1.RemoveWorkerRuns_REMOVE_WORKER_RUNS_STOP,
}

var workerRemovalStates = map[coordinatorv1.WorkerRemovalState]api.WorkerRemovalState{
	coordinatorv1.WorkerRemovalState_WORKER_REMOVAL_STATE_REMOVING: api.WorkerRemovalStateRemoving,
	coordinatorv1.WorkerRemovalState_WORKER_REMOVAL_STATE_REMOVED:  api.WorkerRemovalStateRemoved,
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

// coordinatorAnswer is how the server answers a coordinator refusal.
type coordinatorAnswer struct {
	httpStatus int
	code       api.ErrorCode
	// message returns the answer's message from the coordinator's.
	message func(coordinatorMessage string) string
	// reason is the answer's reason when the coordinator names none.
	reason api.WorkerAccessReason
}

// coordinatorAnswers maps each coordinator refusal to its answer, as Spec
// 108 §REST API states. Other codes answer 500.
var coordinatorAnswers = map[codes.Code]coordinatorAnswer{
	codes.InvalidArgument:    {http.StatusBadRequest, api.ErrorCodeBadRequest, asSent, ""},
	codes.NotFound:           {http.StatusNotFound, api.ErrorCodeNotFound, asSent, ""},
	codes.FailedPrecondition: {http.StatusConflict, api.ErrorCodeConflict, asSent, api.WorkerAccessReasonCoordinatorConflict},
	codes.Unimplemented: {http.StatusConflict, api.ErrorCodeConflict,
		func(string) string { return olderCoordinatorMessage }, api.WorkerAccessReasonOlderCoordinator},
	codes.Unauthenticated: {http.StatusServiceUnavailable, api.ErrorCodeInternalError,
		prefixed(coordinatorRefusedPrefix), api.WorkerAccessReasonCoordinatorRefused},
	codes.Unavailable: {http.StatusServiceUnavailable, api.ErrorCodeInternalError,
		prefixed(coordinatorUnreachableMessage + ": "), api.WorkerAccessReasonNoCoordinator},
	codes.DeadlineExceeded: {http.StatusServiceUnavailable, api.ErrorCodeInternalError,
		prefixed(coordinatorUnreachableMessage + ": "), api.WorkerAccessReasonNoCoordinator},
}

// reasonMessages words the answers whose reason the coordinator's message,
// or the code's wording, does not fit.
var reasonMessages = map[api.WorkerAccessReason]func(string) string{
	// The guidance names the setting only.
	api.WorkerAccessReasonPeerTokenMissing: func(string) string { return peerTokenMissingAnswer },
	// A coordinator answered; it cannot do it yet.
	api.WorkerAccessReasonRetryLater: asSent,
}

// asSent answers with the coordinator's message.
func asSent(message string) string { return message }

// prefixed answers with the coordinator's message after prefix.
func prefixed(prefix string) func(string) string {
	return func(message string) string { return prefix + message }
}

var errNoCoordinator = &Error{
	HTTPStatus: http.StatusServiceUnavailable,
	Code:       api.ErrorCodeInternalError,
	Message:    coordinatorUnreachableMessage,
	Details:    reasonDetails(api.WorkerAccessReasonNoCoordinator),
}

// workerAccessOperations are the worker invite, removal, and join readiness
// operations, which an admin alone may call.
var workerAccessOperations = map[string]bool{
	"CreateWorkerInvite":     true,
	"ListWorkerInvites":      true,
	"DeleteWorkerInvite":     true,
	"RemoveWorker":           true,
	"GetWorkerJoinReadiness": true,
}

// workerAccessAuthorization refuses a worker access request of a caller who
// is no admin before the request's input is validated, so such a caller
// gets 403 whatever the input. It matches requests with the router of the
// operations' own path patterns, on the path the serving router routes on.
func (a *API) workerAccessAuthorization(swagger *openapi3.T) (func(http.Handler) http.Handler, error) {
	router := chi.NewRouter()
	for path, item := range swagger.Paths.Map() {
		for method, operation := range item.Operations() {
			if workerAccessOperations[operation.OperationID] {
				router.MethodFunc(method, path, func(http.ResponseWriter, *http.Request) {})
			}
		}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rctx := chi.RouteContext(r.Context())
			if rctx == nil || !router.Match(chi.NewRouteContext(), r.Method, cmp.Or(rctx.RoutePath, r.URL.Path)) {
				next.ServeHTTP(w, r)
				return
			}
			if err := a.requireAdmin(r.Context()); err != nil {
				WriteErrorResponse(w, err)
				return
			}
			next.ServeHTTP(w, r)
		})
	}, nil
}

// CreateWorkerInvite implements the createWorkerInvite operation. Its
// response is the only one that contains the invite.
func (a *API) CreateWorkerInvite(ctx context.Context, request api.CreateWorkerInviteRequestObject) (api.CreateWorkerInviteResponseObject, error) {
	if err := a.requireAdmin(ctx); err != nil {
		return nil, err
	}
	// The request is checked before the coordinator client, so an invalid
	// one answers 400 also on a server without a coordinator.
	req := &coordinatorv1.CreateInviteRequest{}
	if user, ok := auth.UserFromContext(ctx); ok && user != nil {
		req.CreatedBy = user.Username
	}
	if body := request.Body; body != nil {
		req.Reusable = valueOf(body.Reusable)
		req.Address = valueOf(body.Address)
		// An invite pasted as the address would reach the audit entry and
		// every later listing, so it never leaves this server.
		if coordinator.RedactInvites(req.Address) != req.Address {
			return nil, &Error{
				HTTPStatus: http.StatusBadRequest,
				Code:       api.ErrorCodeBadRequest,
				Message:    "address must be the coordinator's host:port, not an invite",
			}
		}
		if req.Address != "" && !coordinator.ValidInviteAddress(req.Address) {
			return nil, &Error{
				HTTPStatus: http.StatusBadRequest,
				Code:       api.ErrorCodeBadRequest,
				Message:    "address must be host:port, with a hostname, an IPv4 address, or a bracketed IPv6 address as host",
			}
		}
		if body.ExpiresIn != nil {
			seconds, err := expiresInSeconds(*body.ExpiresIn)
			if err != nil {
				return nil, err
			}
			req.ExpiresInSeconds = seconds
		}
	}
	client, err := a.workerAccess(ctx)
	if err != nil {
		return nil, err
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
	if err := a.requireAdmin(ctx); err != nil {
		return nil, err
	}
	// Invite IDs are UUIDs. Anything else, such as an invite pasted in
	// place of its ID, never reaches a coordinator or the audit entry.
	if uuid.Validate(request.InviteId) != nil {
		return nil, &Error{
			HTTPStatus: http.StatusBadRequest,
			Code:       api.ErrorCodeBadRequest,
			Message:    "this is not an invite ID: list the invites to find their IDs",
		}
	}
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
func (a *API) RemoveWorker(ctx context.Context, request api.RemoveWorkerRequestObject) (api.RemoveWorkerResponseObject, error) {
	client, err := a.workerAccess(ctx)
	if err != nil {
		return nil, err
	}
	runs := api.WorkerRemovalRunsFinish
	if request.Params.Runs != nil {
		runs = *request.Params.Runs
	}
	if !runs.Valid() {
		return nil, &Error{HTTPStatus: http.StatusBadRequest, Code: api.ErrorCodeBadRequest, Message: "runs must be finish or stop"}
	}
	if request.Params.CertificateId == "" {
		return nil, &Error{HTTPStatus: http.StatusBadRequest, Code: api.ErrorCodeBadRequest, Message: "certificateId is required: take it from the workers list"}
	}
	resp, err := client.RemoveWorker(ctx, &coordinatorv1.RemoveWorkerRequest{
		WorkerId:      request.WorkerId,
		Runs:          workerRemovalRuns[runs],
		CertificateId: request.Params.CertificateId,
	})
	if err != nil {
		return nil, coordinatorCallError(err)
	}
	state := workerRemovalStates[resp.GetState()]
	a.logAudit(ctx, audit.CategorySystem, "worker_remove", map[string]any{
		// The worker the removed certificate's record names.
		"worker_id":       cmp.Or(resp.GetWorkerId(), request.WorkerId),
		"certificate_ids": resp.GetCertificateIds(),
		"runs":            string(runs),
		"state":           string(state),
	})
	return api.RemoveWorker200JSONResponse{State: state}, nil
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
	// failure is the refusal to answer with when no coordinator reports.
	var failure error
	for _, report := range reports {
		entry := api.CoordinatorJoinReadiness{
			InstanceId: report.Coordinator.ID,
			Host:       report.Coordinator.Host,
			Port:       report.Coordinator.Port,
		}
		if report.Err != nil {
			failure = moreTelling(failure, report.Err)
			answer := coordinatorCallError(report.Err)
			entry.Error = &answer.Message
			entry.ErrorReason = errorReason(answer)
		} else {
			entry.Report = joinReadinessReport(report.Report)
		}
		resp.Coordinators = append(resp.Coordinators, entry)
	}
	// With no report, the dialog has nothing to show, so the refusal is the
	// answer.
	if failure != nil && !slices.ContainsFunc(resp.Coordinators, func(c api.CoordinatorJoinReadiness) bool { return c.Report != nil }) {
		return nil, coordinatorCallError(failure)
	}
	invites, err := access.ListInvites(ctx)
	if err != nil {
		logger.Debug(ctx, "Failed to list worker invites for join readiness", tag.Error(err))
	}
	resp.LastInviteAddress = lastInviteAddress(invites)
	return resp, nil
}

// moreTelling returns whichever of current and next says more about what to
// change: a coordinator's refusal over a failure to reach one, or current
// when both are of a kind.
func moreTelling(current, next error) error {
	if current == nil || (unreached(current) && !unreached(next)) {
		return next
	}
	return current
}

// unreached reports whether err says only that no coordinator answered.
func unreached(err error) bool {
	if coordinator.RefusalReason(err) != "" {
		return false
	}
	code := status.Code(err)
	return code == codes.Unavailable || code == codes.DeadlineExceeded || code == codes.Unknown
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
	// The status itself, not one that FromError builds around the message
	// of an error that wraps it.
	var withStatus interface{ GRPCStatus() *status.Status }
	if !errors.As(err, &withStatus) {
		return errNoCoordinator
	}
	st := withStatus.GRPCStatus()
	answer, ok := coordinatorAnswers[st.Code()]
	if !ok {
		return &Error{
			HTTPStatus: http.StatusInternalServerError,
			Code:       api.ErrorCodeInternalError,
			Message:    "the coordinator failed: " + st.Message(),
		}
	}
	reason := api.WorkerAccessReason(coordinator.RefusalReason(err))
	if !reason.Valid() {
		reason = answer.reason
	}
	message := answer.message
	if worded, ok := reasonMessages[reason]; ok {
		message = worded
	}
	return &Error{
		HTTPStatus: answer.httpStatus,
		Code:       answer.code,
		Message:    message(st.Message()),
		Details:    reasonDetails(reason),
	}
}

// reasonDetails returns the details of an answer with reason, or nil for
// none.
func reasonDetails(reason api.WorkerAccessReason) map[string]any {
	if reason == "" {
		return nil
	}
	return map[string]any{"reason": string(reason)}
}

// errorReason returns the reason of the answer e, or nil for none.
func errorReason(e *Error) *api.WorkerAccessReason {
	reason, ok := e.Details["reason"].(string)
	if !ok {
		return nil
	}
	return new(api.WorkerAccessReason(reason))
}

// expiresInSeconds returns the seconds of the duration expiresIn, rounded
// up, or the 400 answer when it is not a positive Go duration the
// coordinator accepts.
func expiresInSeconds(expiresIn string) (int64, error) {
	invalid := &Error{
		HTTPStatus: http.StatusBadRequest,
		Code:       api.ErrorCodeBadRequest,
		Message:    "expiresIn must be a positive duration of at most 2562047h47m16s, such as 24h",
	}
	d, err := time.ParseDuration(expiresIn)
	if err != nil || d <= 0 {
		return 0, invalid
	}
	seconds := int64(d / time.Second)
	if d%time.Second != 0 {
		seconds++
	}
	if seconds > maxExpiresInSeconds {
		return 0, invalid
	}
	return seconds, nil
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
