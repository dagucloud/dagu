// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	openapiv1 "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/audit"
	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
	coordinatorv1 "github.com/dagucloud/dagu/v2/proto/coordinator/v1"
)

// The coordinator's refusals of a call without the peer token, and of a call
// over h2c under coordinator.require_tls.
const (
	peerTokenRefusal  = "the coordinator requires a peer token: set peer.token, or let this process read the coordinator's data directory"
	requireTLSRefusal = "the coordinator requires TLS: upgrade this process, or remove peer.insecure: true"
)

// testInviteID is the ID of the invite the stub coordinator creates.
const testInviteID = "0f8fad5b-d9cb-469f-a165-70867728950e"

// testInvite is the invite the stub coordinator creates.
const testInvite = "dagu1.AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA.BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"

var (
	_ coordinator.InviteClient        = (*inviteCoordinatorStub)(nil)
	_ coordinator.JoinReadinessClient = (*inviteCoordinatorStub)(nil)
)

// inviteCoordinatorStub answers the invite and join readiness calls, with
// err for every invite call when set, and records the requests.
type inviteCoordinatorStub struct {
	stubCoordinatorClient
	err        error
	invites    []*coordinatorv1.InviteRecord
	readiness  []coordinator.JoinReadiness
	membersErr error

	created *coordinatorv1.CreateInviteRequest
	deleted string
	removed *coordinatorv1.RemoveWorkerRequest
	// recordWorkerID is the worker the removed certificate's record names.
	recordWorkerID string
}

func (s *inviteCoordinatorStub) CreateInvite(_ context.Context, req *coordinatorv1.CreateInviteRequest) (*coordinatorv1.CreateInviteResponse, error) {
	s.created = req
	if s.err != nil {
		return nil, s.err
	}
	record := &coordinatorv1.InviteRecord{
		Id:        testInviteID,
		Kind:      coordinatorv1.InviteKind_INVITE_KIND_SINGLE_USE,
		ExpiresAt: 1710086400,
		Address:   req.Address,
		CreatedBy: req.CreatedBy,
		CreatedAt: 1710000000,
	}
	s.invites = append(s.invites, record)
	return &coordinatorv1.CreateInviteResponse{Invite: testInvite, Record: record}, nil
}

func (s *inviteCoordinatorStub) ListInvites(context.Context) ([]*coordinatorv1.InviteRecord, error) {
	return s.invites, s.err
}

func (s *inviteCoordinatorStub) DeleteInvite(_ context.Context, id string) error {
	s.deleted = id
	return s.err
}

func (s *inviteCoordinatorStub) RemoveWorker(_ context.Context, req *coordinatorv1.RemoveWorkerRequest) (*coordinatorv1.RemoveWorkerResponse, error) {
	s.removed = req
	if s.err != nil {
		return nil, s.err
	}
	return &coordinatorv1.RemoveWorkerResponse{
		State:          coordinatorv1.WorkerRemovalState_WORKER_REMOVAL_STATE_REMOVING,
		CertificateIds: []string{"cert-1"},
		WorkerId:       s.recordWorkerID,
	}, nil
}

func (s *inviteCoordinatorStub) GetJoinReadiness(context.Context) ([]coordinator.JoinReadiness, error) {
	return s.readiness, s.membersErr
}

// rolesAPI returns an API whose roles apply, with coordinatorCli as its
// coordinator client, and the store of its audit entries.
func rolesAPI(coordinatorCli coordinator.Client) (*API, *restAuditStore) {
	store := &restAuditStore{}
	return &API{
		authService:    struct{ AuthService }{},
		auditService:   audit.New(store),
		coordinatorCli: coordinatorCli,
	}, store
}

func userContext(role auth.Role) context.Context {
	return auth.WithUser(context.Background(), &auth.User{ID: "user-1", Username: string(role) + "-user", Role: role})
}

// workerAccessCalls makes each worker access call, and returns its error.
func workerAccessCalls(a *API) map[string]func(context.Context) error {
	return map[string]func(context.Context) error{
		"create": func(ctx context.Context) error {
			_, err := a.CreateWorkerInvite(ctx, openapiv1.CreateWorkerInviteRequestObject{})
			return err
		},
		"list": func(ctx context.Context) error {
			_, err := a.ListWorkerInvites(ctx, openapiv1.ListWorkerInvitesRequestObject{})
			return err
		},
		"delete": func(ctx context.Context) error {
			_, err := a.DeleteWorkerInvite(ctx, openapiv1.DeleteWorkerInviteRequestObject{InviteId: testInviteID})
			return err
		},
		"remove": func(ctx context.Context) error {
			_, err := a.RemoveWorker(ctx, openapiv1.RemoveWorkerRequestObject{WorkerId: "worker-1", Params: openapiv1.RemoveWorkerParams{CertificateId: "cert-1"}})
			return err
		},
		"join readiness": func(ctx context.Context) error {
			_, err := a.GetWorkerJoinReadiness(ctx, openapiv1.GetWorkerJoinReadinessRequestObject{})
			return err
		},
	}
}

// httpStatus returns the HTTP status that err answers with.
func httpStatus(t *testing.T, err error) int {
	t.Helper()
	var apiErr *Error
	require.ErrorAs(t, err, &apiErr)
	return apiErr.HTTPStatus
}

func TestWorkerAccessRoles(t *testing.T) {
	t.Parallel()

	stub := &inviteCoordinatorStub{readiness: []coordinator.JoinReadiness{{Report: &coordinatorv1.GetJoinReadinessResponse{}}}}
	a, _ := rolesAPI(stub)
	for name, call := range workerAccessCalls(a) {
		assert.NoError(t, call(userContext(auth.RoleAdmin)), name)
		for _, role := range []auth.Role{auth.RoleManager, auth.RoleDeveloper, auth.RoleOperator, auth.RoleViewer} {
			assert.Equal(t, http.StatusForbidden, httpStatus(t, call(userContext(role))), "%s as %s", name, role)
		}
	}
}

func TestWorkerAccessNoCoordinator(t *testing.T) {
	t.Parallel()

	a, _ := rolesAPI(nil)
	for name, call := range workerAccessCalls(a) {
		assert.Equal(t, http.StatusServiceUnavailable, httpStatus(t, call(userContext(auth.RoleAdmin))), name)
	}
}

// Each refusal of the coordinator maps to the answer of Spec 108 §REST API,
// with the reason a caller picks its explanation by.
func TestWorkerAccessErrors(t *testing.T) {
	t.Parallel()

	const (
		ownCertificates = "invites need the built-in CA: this coordinator uses peer.cert_file and peer.key_file"
		caUnavailable   = "invites need the built-in CA: this coordinator could not create or read its CA; see its start warning"
		tokenMismatch   = "the peer token does not match the coordinator's peer token"
	)
	for _, tt := range []struct {
		err     error
		status  int
		message string
		reason  string
	}{
		{status.Error(codes.FailedPrecondition, ownCertificates), http.StatusConflict, ownCertificates, "own_certificates"},
		{status.Error(codes.FailedPrecondition, caUnavailable), http.StatusConflict, caUnavailable, "ca_unavailable"},
		{status.Error(codes.InvalidArgument, "address must be host:port"), http.StatusBadRequest, "address must be host:port", ""},
		{status.Error(codes.NotFound, "no invite has this ID"), http.StatusNotFound, "no invite has this ID", ""},
		{status.Error(codes.Unauthenticated, peerTokenRefusal), http.StatusServiceUnavailable, peerTokenMissingAnswer, "peer_token_missing"},
		{status.Error(codes.FailedPrecondition, "a refusal this server does not know"), http.StatusConflict, "a refusal this server does not know", "coordinator_conflict"},
		{withReason(codes.Unavailable, "retry_later", "the worker's certificate record is being written again; try again in a minute"), http.StatusServiceUnavailable,
			"the worker's certificate record is being written again; try again in a minute", "retry_later"},
		{status.Error(codes.Unauthenticated, tokenMismatch), http.StatusServiceUnavailable, coordinatorRefusedPrefix + tokenMismatch, "peer_token_mismatch"},
		{status.Error(codes.Unauthenticated, requireTLSRefusal), http.StatusServiceUnavailable, coordinatorRefusedPrefix + requireTLSRefusal, "require_tls"},
		{status.Error(codes.Unauthenticated, "refused by a proxy"), http.StatusServiceUnavailable, coordinatorRefusedPrefix + "refused by a proxy", "coordinator_refused"},
		{status.Error(codes.Unimplemented, "unknown method CreateInvite"), http.StatusConflict, olderCoordinatorMessage, "older_coordinator"},
		{status.Error(codes.Unavailable, "connection refused"), http.StatusServiceUnavailable, coordinatorUnreachableMessage + ": connection refused", "no_coordinator"},
		{status.Error(codes.DeadlineExceeded, "context deadline exceeded"), http.StatusServiceUnavailable, coordinatorUnreachableMessage + ": context deadline exceeded", "no_coordinator"},
		{errors.New("no coordinators available"), http.StatusServiceUnavailable, coordinatorUnreachableMessage, "no_coordinator"},
		{status.Error(codes.Internal, "failed to store the invite"), http.StatusInternalServerError, "the coordinator failed: failed to store the invite", ""},
	} {
		a, store := rolesAPI(&inviteCoordinatorStub{err: tt.err})
		for name, call := range workerAccessCalls(a) {
			if name == "join readiness" {
				continue
			}
			err := call(userContext(auth.RoleAdmin))
			var apiErr *Error
			require.ErrorAs(t, err, &apiErr, name)
			assert.Equal(t, tt.status, apiErr.HTTPStatus, "%s: %v", name, tt.err)
			assert.Equal(t, tt.message, apiErr.Message, "%s: %v", name, tt.err)
			assert.Equal(t, tt.reason, string(valueOf(errorReason(apiErr))), "%s: %v", name, tt.err)
		}
		assert.Empty(t, store.entries, "a refused change writes no audit entry")
	}
}

// With no coordinator client, the answer still names its reason.
func TestWorkerAccessNoCoordinatorReason(t *testing.T) {
	t.Parallel()

	a, _ := rolesAPI(nil)
	for name, call := range workerAccessCalls(a) {
		var apiErr *Error
		require.ErrorAs(t, call(userContext(auth.RoleAdmin)), &apiErr, name)
		assert.Equal(t, map[string]any{"reason": "no_coordinator"}, apiErr.Details, name)
	}
}

func TestCreateWorkerInvite(t *testing.T) {
	t.Parallel()

	stub := &inviteCoordinatorStub{}
	a, store := rolesAPI(stub)
	resp, err := a.CreateWorkerInvite(userContext(auth.RoleAdmin), openapiv1.CreateWorkerInviteRequestObject{
		Body: &openapiv1.CreateWorkerInviteRequest{
			Reusable:  new(true),
			ExpiresIn: new("90m500ms"),
			Address:   new("192.168.1.20:50055"),
		},
	})
	require.NoError(t, err)
	assert.Equal(t, openapiv1.CreateWorkerInvite201JSONResponse{
		Id:        testInviteID,
		Invite:    testInvite,
		ExpiresAt: new("2024-03-10T16:00:00Z"),
	}, resp)
	assert.Equal(t, &coordinatorv1.CreateInviteRequest{
		Reusable:         true,
		ExpiresInSeconds: 5401,
		Address:          "192.168.1.20:50055",
		CreatedBy:        "admin-user",
	}, stub.created)

	require.Len(t, store.entries, 1)
	entry := store.entries[0]
	assert.Equal(t, audit.CategorySystem, entry.Category)
	assert.Equal(t, "worker_invite_create", entry.Action)
	assert.JSONEq(t, `{"invite_id":"`+testInviteID+`","kind":"single_use","address":"192.168.1.20:50055"}`, entry.Details)
}

func TestCreateWorkerInviteExpiresIn(t *testing.T) {
	t.Parallel()

	for _, expiresIn := range []string{"soon", "0s", "-1h", "", "2562047h47m16.000000001s", "2562047h47m16.854775807s"} {
		stub := &inviteCoordinatorStub{}
		a, _ := rolesAPI(stub)
		_, err := a.CreateWorkerInvite(userContext(auth.RoleAdmin), openapiv1.CreateWorkerInviteRequestObject{
			Body: &openapiv1.CreateWorkerInviteRequest{ExpiresIn: &expiresIn},
		})
		var apiErr *Error
		require.ErrorAs(t, err, &apiErr, expiresIn)
		assert.Equal(t, http.StatusBadRequest, apiErr.HTTPStatus, expiresIn)
		assert.Contains(t, apiErr.Message, "expiresIn", expiresIn)
		assert.Nil(t, stub.created, "%q reaches no coordinator", expiresIn)
	}
}

// The create response is the only one that contains the invite: no later
// listing, join readiness report, or audit entry does (Spec 108 §Secrets in
// Output).
func TestWorkerInviteOnlyInCreateResponse(t *testing.T) {
	t.Parallel()

	stub := &inviteCoordinatorStub{readiness: []coordinator.JoinReadiness{{Report: &coordinatorv1.GetJoinReadinessResponse{}}}}
	a, store := rolesAPI(stub)
	ctx := userContext(auth.RoleAdmin)
	created, err := a.CreateWorkerInvite(ctx, openapiv1.CreateWorkerInviteRequestObject{
		Body: &openapiv1.CreateWorkerInviteRequest{Address: new("192.168.1.20:50055")},
	})
	require.NoError(t, err)
	assert.Contains(t, marshal(t, created), testInvite)

	listed, err := a.ListWorkerInvites(ctx, openapiv1.ListWorkerInvitesRequestObject{})
	require.NoError(t, err)
	readiness, err := a.GetWorkerJoinReadiness(ctx, openapiv1.GetWorkerJoinReadinessRequestObject{})
	require.NoError(t, err)
	_, err = a.DeleteWorkerInvite(ctx, openapiv1.DeleteWorkerInviteRequestObject{InviteId: testInviteID})
	require.NoError(t, err)

	for _, later := range []any{listed, readiness, store.entries} {
		assert.NotContains(t, marshal(t, later), testInvite)
	}
}

func marshal(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	return string(data)
}

func TestListWorkerInvites(t *testing.T) {
	t.Parallel()

	a, _ := rolesAPI(&inviteCoordinatorStub{invites: []*coordinatorv1.InviteRecord{
		{Id: "single", Kind: coordinatorv1.InviteKind_INVITE_KIND_SINGLE_USE, CreatedBy: "admin", CreatedAt: 1710000000, ExpiresAt: 1710086400, UseCount: 1, LastUsedAt: 1710003600, Address: "192.168.1.20:50055"},
		{Id: "reusable", Kind: coordinatorv1.InviteKind_INVITE_KIND_REUSABLE, CreatedBy: "peer token", CreatedAt: 1710000000},
	}})
	resp, err := a.ListWorkerInvites(userContext(auth.RoleAdmin), openapiv1.ListWorkerInvitesRequestObject{})
	require.NoError(t, err)
	assert.Equal(t, openapiv1.ListWorkerInvites200JSONResponse{Invites: []openapiv1.WorkerInvite{
		{
			Id: "single", Kind: openapiv1.WorkerInviteKindSingleUse, CreatedBy: "admin", CreatedAt: "2024-03-09T16:00:00Z",
			ExpiresAt: new("2024-03-10T16:00:00Z"), UseCount: 1, LastUsedAt: new("2024-03-09T17:00:00Z"), Address: new("192.168.1.20:50055"),
		},
		{Id: "reusable", Kind: openapiv1.WorkerInviteKindReusable, CreatedBy: "peer token", CreatedAt: "2024-03-09T16:00:00Z"},
	}}, resp)
}

func TestDeleteWorkerInvite(t *testing.T) {
	t.Parallel()

	stub := &inviteCoordinatorStub{}
	a, store := rolesAPI(stub)
	resp, err := a.DeleteWorkerInvite(userContext(auth.RoleAdmin), openapiv1.DeleteWorkerInviteRequestObject{InviteId: testInviteID})
	require.NoError(t, err)
	assert.Equal(t, openapiv1.DeleteWorkerInvite204Response{}, resp)
	assert.Equal(t, testInviteID, stub.deleted)
	require.Len(t, store.entries, 1)
	assert.Equal(t, "worker_invite_delete", store.entries[0].Action)
	assert.JSONEq(t, `{"invite_id":"`+testInviteID+`"}`, store.entries[0].Details)
}

func TestGetWorkerJoinReadiness(t *testing.T) {
	t.Parallel()

	stub := &inviteCoordinatorStub{
		readiness: []coordinator.JoinReadiness{
			{
				Coordinator: serviceregistry.HostInfo{ID: "coordinator-1", Host: "10.0.0.5", Port: 50055},
				Report: &coordinatorv1.GetJoinReadinessResponse{
					ListenHost: "0.0.0.0", Port: 50055, ConfigFile: "/etc/dagu/config.yaml",
					HostSource:  &coordinatorv1.ConfigSource{Kind: coordinatorv1.ConfigSourceKind_CONFIG_SOURCE_KIND_FLAG, Name: "--coordinator.host"},
					Advertise:   "dagu.example.com",
					Environment: coordinatorv1.HostEnvironment_HOST_ENVIRONMENT_HOST,
					Addresses:   []string{"192.168.1.20"},
					BuiltinTls:  true, Ready: true,
				},
			},
			{
				Coordinator: serviceregistry.HostInfo{ID: "coordinator-2", Host: "10.0.0.6", Port: 50055},
				Report: &coordinatorv1.GetJoinReadinessResponse{
					ListenHost: "127.0.0.1", Port: 50055, ConfigFile: "/root/.config/dagu/config.yaml", LoopbackOnly: true,
					HostSource:       &coordinatorv1.ConfigSource{Kind: coordinatorv1.ConfigSourceKind_CONFIG_SOURCE_KIND_DEFAULT},
					Environment:      coordinatorv1.HostEnvironment_HOST_ENVIRONMENT_CONTAINER,
					BuiltinTlsReason: coordinatorv1.BuiltinTLSReason_BUILTIN_TLS_REASON_OWN_CERTIFICATES,
				},
			},
			{
				Coordinator: serviceregistry.HostInfo{ID: "coordinator-3", Host: "10.0.0.7", Port: 50055},
				Err:         status.Error(codes.Unimplemented, "unknown method GetJoinReadiness"),
			},
		},
		invites: []*coordinatorv1.InviteRecord{
			{Id: "oldest", Address: "192.168.1.10:50055"},
			{Id: "newer", Address: "192.168.1.20:50055"},
			{Id: "newest"},
		},
	}
	a, _ := rolesAPI(stub)
	resp, err := a.GetWorkerJoinReadiness(userContext(auth.RoleAdmin), openapiv1.GetWorkerJoinReadinessRequestObject{})
	require.NoError(t, err)
	assert.Equal(t, openapiv1.GetWorkerJoinReadiness200JSONResponse{
		Coordinators: []openapiv1.CoordinatorJoinReadiness{
			{
				InstanceId: "coordinator-1", Host: "10.0.0.5", Port: 50055,
				Report: &openapiv1.JoinReadinessReport{
					ListenHost: "0.0.0.0", Port: 50055, ConfigFile: "/etc/dagu/config.yaml",
					HostSource:  &openapiv1.ConfigSource{Kind: openapiv1.ConfigSourceKindFlag, Name: new("--coordinator.host")},
					Advertise:   new("dagu.example.com"),
					Environment: new(openapiv1.HostEnvironmentHost),
					Addresses:   []string{"192.168.1.20"},
					BuiltinTLS:  true, Ready: true,
				},
			},
			{
				InstanceId: "coordinator-2", Host: "10.0.0.6", Port: 50055,
				Report: &openapiv1.JoinReadinessReport{
					ListenHost: "127.0.0.1", Port: 50055, ConfigFile: "/root/.config/dagu/config.yaml", LoopbackOnly: true,
					HostSource:       &openapiv1.ConfigSource{Kind: openapiv1.ConfigSourceKindDefault},
					Environment:      new(openapiv1.HostEnvironmentContainer),
					Addresses:        []string{},
					BuiltinTLSReason: new(openapiv1.BuiltinTLSReasonOwnCertificates),
				},
			},
			{
				InstanceId: "coordinator-3", Host: "10.0.0.7", Port: 50055,
				Error: new(olderCoordinatorMessage), ErrorReason: new(openapiv1.WorkerAccessReasonOlderCoordinator),
			},
		},
		LastInviteAddress: new("192.168.1.20:50055"),
	}, resp)
}

// Without a single report, the refusal of the coordinators is the answer.
// Invites that cannot be read leave out the last invite's address only.
func TestGetWorkerJoinReadinessFailures(t *testing.T) {
	t.Parallel()

	ctx := userContext(auth.RoleAdmin)
	request := openapiv1.GetWorkerJoinReadinessRequestObject{}

	a, _ := rolesAPI(&inviteCoordinatorStub{readiness: []coordinator.JoinReadiness{{Err: status.Error(codes.Unauthenticated, "missing peer token")}}})
	_, err := a.GetWorkerJoinReadiness(ctx, request)
	assert.Equal(t, http.StatusServiceUnavailable, httpStatus(t, err))

	a, _ = rolesAPI(&inviteCoordinatorStub{membersErr: errors.New("no coordinators available")})
	_, err = a.GetWorkerJoinReadiness(ctx, request)
	assert.Equal(t, http.StatusServiceUnavailable, httpStatus(t, err))

	a, _ = rolesAPI(&inviteCoordinatorStub{
		readiness: []coordinator.JoinReadiness{{Report: &coordinatorv1.GetJoinReadinessResponse{}}},
		err:       status.Error(codes.FailedPrecondition, "invites need the built-in CA"),
	})
	resp, err := a.GetWorkerJoinReadiness(ctx, request)
	require.NoError(t, err)
	ok, isOK := resp.(openapiv1.GetWorkerJoinReadiness200JSONResponse)
	require.True(t, isOK)
	assert.Len(t, ok.Coordinators, 1)
	assert.Nil(t, ok.LastInviteAddress)
}

// The longest expiry rounds to whole seconds without overflowing.
func TestCreateWorkerInviteLongestExpiry(t *testing.T) {
	t.Parallel()

	stub := &inviteCoordinatorStub{}
	a, _ := rolesAPI(stub)
	_, err := a.CreateWorkerInvite(userContext(auth.RoleAdmin), openapiv1.CreateWorkerInviteRequestObject{
		Body: &openapiv1.CreateWorkerInviteRequest{ExpiresIn: new("2562047h47m16s")},
	})
	require.NoError(t, err)
	assert.Equal(t, int64(9223372036), stub.created.ExpiresInSeconds)
}

// Each refusal of the server reaches the admin with the coordinator's own
// reason, for join readiness as for the invite calls.
func TestGetWorkerJoinReadinessRefusal(t *testing.T) {
	t.Parallel()

	for refusal, want := range map[string]string{
		peerTokenRefusal:  peerTokenMissingAnswer,
		requireTLSRefusal: coordinatorRefusedPrefix + requireTLSRefusal,
	} {
		a, _ := rolesAPI(&inviteCoordinatorStub{readiness: []coordinator.JoinReadiness{{Err: status.Error(codes.Unauthenticated, refusal)}}})
		_, err := a.GetWorkerJoinReadiness(userContext(auth.RoleAdmin), openapiv1.GetWorkerJoinReadinessRequestObject{})
		var apiErr *Error
		require.ErrorAs(t, err, &apiErr)
		assert.Equal(t, http.StatusServiceUnavailable, apiErr.HTTPStatus)
		assert.Equal(t, want, apiErr.Message)
		assert.NotNil(t, apiErr.Details["reason"])
	}
}

// withReason returns a coordinator refusal that carries reason as a status
// detail, as a coordinator sends it.
func withReason(code codes.Code, reason, message string) error {
	st, err := status.New(code, message).WithDetails(&errdetails.ErrorInfo{Reason: reason, Domain: "coordinator.dagu"})
	if err != nil {
		panic(err)
	}
	return st.Err()
}

// An invite pasted as the address is refused before any coordinator sees
// it, and the answer does not repeat it.
func TestCreateWorkerInviteInviteAsAddress(t *testing.T) {
	t.Parallel()

	stub := &inviteCoordinatorStub{}
	a, store := rolesAPI(stub)
	_, err := a.CreateWorkerInvite(userContext(auth.RoleAdmin), openapiv1.CreateWorkerInviteRequestObject{
		Body: &openapiv1.CreateWorkerInviteRequest{Address: new(testInvite + ":50055")},
	})
	var apiErr *Error
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusBadRequest, apiErr.HTTPStatus)
	assert.NotContains(t, apiErr.Message, testInvite)
	assert.Nil(t, stub.created)
	assert.Empty(t, store.entries)
}

// An invalid create answers 400 also on a server without a coordinator.
func TestCreateWorkerInviteInvalidWithoutCoordinator(t *testing.T) {
	t.Parallel()

	a, _ := rolesAPI(nil)
	for name, body := range map[string]*openapiv1.CreateWorkerInviteRequest{
		"invite as address":    {Address: new(testInvite + ":50055")},
		"address without port": {Address: new("192.168.1.20")},
		"expiresIn":            {ExpiresIn: new("soon")},
	} {
		_, err := a.CreateWorkerInvite(userContext(auth.RoleAdmin), openapiv1.CreateWorkerInviteRequestObject{Body: body})
		assert.Equal(t, http.StatusBadRequest, httpStatus(t, err), name)
	}
	_, err := a.CreateWorkerInvite(userContext(auth.RoleViewer), openapiv1.CreateWorkerInviteRequestObject{
		Body: &openapiv1.CreateWorkerInviteRequest{ExpiresIn: new("soon")},
	})
	assert.Equal(t, http.StatusForbidden, httpStatus(t, err), "the role is checked first")
}

// A delete of a value that is no invite ID answers 400 before any
// coordinator call, also on a server without a coordinator.
func TestDeleteWorkerInviteRefusesNonID(t *testing.T) {
	t.Parallel()

	for _, stub := range []coordinator.Client{nil, &inviteCoordinatorStub{}} {
		a, store := rolesAPI(stub)
		for _, id := range []string{testInvite, "invite-1"} {
			_, err := a.DeleteWorkerInvite(userContext(auth.RoleAdmin), openapiv1.DeleteWorkerInviteRequestObject{InviteId: id})
			var apiErr *Error
			require.ErrorAs(t, err, &apiErr)
			assert.Equal(t, http.StatusBadRequest, apiErr.HTTPStatus)
			assert.NotContains(t, apiErr.Message, id)
		}
		assert.Empty(t, store.entries)
	}
}

// With no report, a coordinator's refusal is the answer over a coordinator
// that cannot be reached, whichever comes first.
func TestGetWorkerJoinReadinessPrefersRefusal(t *testing.T) {
	t.Parallel()

	unreachable := coordinator.JoinReadiness{Err: status.Error(codes.Unavailable, "connection refused")}
	refused := coordinator.JoinReadiness{Err: status.Error(codes.Unauthenticated, peerTokenRefusal)}
	for name, readiness := range map[string][]coordinator.JoinReadiness{
		"unreachable first": {unreachable, refused},
		"refusal first":     {refused, unreachable},
	} {
		a, _ := rolesAPI(&inviteCoordinatorStub{readiness: readiness})
		_, err := a.GetWorkerJoinReadiness(userContext(auth.RoleAdmin), openapiv1.GetWorkerJoinReadinessRequestObject{})
		var apiErr *Error
		require.ErrorAs(t, err, &apiErr, name)
		assert.Equal(t, map[string]any{"reason": "peer_token_missing"}, apiErr.Details, name)
	}
}

func TestRemoveWorker(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		runs *openapiv1.WorkerRemovalRuns
		want coordinatorv1.RemoveWorkerRuns
	}{
		{nil, coordinatorv1.RemoveWorkerRuns_REMOVE_WORKER_RUNS_FINISH},
		{new(openapiv1.WorkerRemovalRunsFinish), coordinatorv1.RemoveWorkerRuns_REMOVE_WORKER_RUNS_FINISH},
		{new(openapiv1.WorkerRemovalRunsStop), coordinatorv1.RemoveWorkerRuns_REMOVE_WORKER_RUNS_STOP},
	} {
		stub := &inviteCoordinatorStub{}
		a, store := rolesAPI(stub)
		resp, err := a.RemoveWorker(userContext(auth.RoleAdmin), openapiv1.RemoveWorkerRequestObject{
			WorkerId: "worker-1",
			Params:   openapiv1.RemoveWorkerParams{Runs: tt.runs, CertificateId: "cert-1"},
		})
		require.NoError(t, err)
		assert.Equal(t, openapiv1.RemoveWorker200JSONResponse{State: openapiv1.WorkerRemovalStateRemoving}, resp)
		assert.Equal(t, &coordinatorv1.RemoveWorkerRequest{WorkerId: "worker-1", Runs: tt.want, CertificateId: "cert-1"}, stub.removed)

		require.Len(t, store.entries, 1)
		entry := store.entries[0]
		assert.Equal(t, "worker_remove", entry.Action)
		var details map[string]any
		require.NoError(t, json.Unmarshal([]byte(entry.Details), &details))
		assert.Equal(t, "worker-1", details["worker_id"])
		assert.Equal(t, []any{"cert-1"}, details["certificate_ids"])
		assert.Equal(t, "removing", details["state"])
	}
}

// A worker on the peer token cannot be removed (Spec 108 §REST API).
func TestRemoveWorkerOnPeerToken(t *testing.T) {
	t.Parallel()

	const message = "this worker uses the peer token, so it would connect again: stop it on its host, or change the peer token"
	a, _ := rolesAPI(&inviteCoordinatorStub{err: status.Error(codes.FailedPrecondition, message)})
	_, err := a.RemoveWorker(userContext(auth.RoleAdmin), openapiv1.RemoveWorkerRequestObject{
		WorkerId: "token-worker",
		Params:   openapiv1.RemoveWorkerParams{CertificateId: "cert-1"},
	})
	var apiErr *Error
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusConflict, apiErr.HTTPStatus)
	assert.Equal(t, message, apiErr.Message)
	assert.Equal(t, map[string]any{"reason": "peer_token_worker"}, apiErr.Details)
}

// A removal names the certificate the workers list shows, so a worker that
// joined again is not removed by mistake (Spec 108 §REST API).
func TestRemoveWorkerNeedsCertificate(t *testing.T) {
	t.Parallel()

	stub := &inviteCoordinatorStub{}
	a, _ := rolesAPI(stub)
	_, err := a.RemoveWorker(userContext(auth.RoleAdmin), openapiv1.RemoveWorkerRequestObject{WorkerId: "worker-1"})
	assert.Equal(t, http.StatusBadRequest, httpStatus(t, err))
	assert.Nil(t, stub.removed)

	const changed = "this worker no longer uses the named certificate, as after joining again: list the workers and remove it again"
	a, _ = rolesAPI(&inviteCoordinatorStub{err: withReason(codes.FailedPrecondition, "certificate_changed", changed)})
	_, err = a.RemoveWorker(userContext(auth.RoleAdmin), openapiv1.RemoveWorkerRequestObject{
		WorkerId: "worker-1",
		Params:   openapiv1.RemoveWorkerParams{CertificateId: "old-cert"},
	})
	var apiErr *Error
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, http.StatusConflict, apiErr.HTTPStatus)
	assert.Equal(t, changed, apiErr.Message)
	assert.Equal(t, map[string]any{"reason": "certificate_changed"}, apiErr.Details)
}

// The removal audit names the worker that the certificate's record names.
func TestRemoveWorkerAuditsRecordWorker(t *testing.T) {
	t.Parallel()

	a, store := rolesAPI(&inviteCoordinatorStub{recordWorkerID: "worker-before-restart"})
	_, err := a.RemoveWorker(userContext(auth.RoleAdmin), openapiv1.RemoveWorkerRequestObject{
		WorkerId: "worker-1",
		Params:   openapiv1.RemoveWorkerParams{CertificateId: "cert-1"},
	})
	require.NoError(t, err)
	require.Len(t, store.entries, 1)
	assert.Contains(t, store.entries[0].Details, `"worker_id":"worker-before-restart"`)
}

// An invalid removal answers 400 also on a server without a coordinator,
// after the role check.
func TestRemoveWorkerInvalidWithoutCoordinator(t *testing.T) {
	t.Parallel()

	a, _ := rolesAPI(nil)
	for name, params := range map[string]openapiv1.RemoveWorkerParams{
		"runs":          {CertificateId: "cert-1", Runs: new(openapiv1.WorkerRemovalRuns("later"))},
		"certificateId": {},
	} {
		_, err := a.RemoveWorker(userContext(auth.RoleAdmin), openapiv1.RemoveWorkerRequestObject{WorkerId: "worker-1", Params: params})
		assert.Equal(t, http.StatusBadRequest, httpStatus(t, err), name)
	}
	_, err := a.RemoveWorker(userContext(auth.RoleViewer), openapiv1.RemoveWorkerRequestObject{WorkerId: "worker-1"})
	assert.Equal(t, http.StatusForbidden, httpStatus(t, err))
}
