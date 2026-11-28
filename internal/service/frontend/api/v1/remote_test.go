// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/remotenode"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type remoteSyncAuthService struct {
	AuthService
	user *auth.User
}

func (s remoteSyncAuthService) GetUserFromToken(context.Context, string) (*auth.User, error) {
	return s.user, nil
}

func (s remoteSyncAuthService) HasAPIKeyStore() bool {
	return false
}

// serveRemoteNodeRequest sends a request for the "edge" remote node through
// the API routes as user. It returns the local response status and whether the
// remote node received the request.
func serveRemoteNodeRequest(t *testing.T, server config.Server, user *auth.User, method, path string) (int, bool) {
	t.Helper()

	var remoteCalled atomic.Bool
	remote := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		remoteCalled.Store(true)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})
	status := serveRemoteNodeRequestTo(t, remote, server, user, method, path, "", nil)
	return status, remoteCalled.Load()
}

// serveRemoteNodeRequestTo sends a request with body for the "edge" remote
// node, served by remote, through the API routes as user. It returns the local
// response status.
func serveRemoteNodeRequestTo(t *testing.T, remote http.Handler, server config.Server, user *auth.User, method, path, body string, header http.Header) int {
	t.Helper()

	remoteServer := httptest.NewServer(remote)
	t.Cleanup(remoteServer.Close)

	server.APIBasePath = "/api/v1"
	server.Auth = config.Auth{Mode: config.AuthModeBuiltin}
	a := &API{
		config:      &config.Config{Server: server},
		authService: remoteSyncAuthService{user: user},
		remoteNodeResolver: remotenode.NewResolver([]config.RemoteNode{{
			Name:       "edge",
			APIBaseURL: remoteServer.URL + "/api/v1",
		}}, nil),
	}
	router := chi.NewRouter()
	require.NoError(t, a.ConfigureRoutes(t.Context(), router, time.Second))

	request := httptest.NewRequest(method, path+"?remoteNode=edge", strings.NewReader(body))
	maps.Copy(request.Header, header)
	request.Header.Set("Authorization", "Bearer caller-token")
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder.Code
}

// A remote node runs proxied requests with its stored credential, so the
// caller needs the permission the matching local handler would require.
func TestRemoteNodeAuthorization(t *testing.T) {
	all := auth.AllWorkspaceAccess()
	viewer := &auth.User{Role: auth.RoleViewer, WorkspaceAccess: all}
	operator := &auth.User{Role: auth.RoleOperator, WorkspaceAccess: all}
	developer := &auth.User{Role: auth.RoleDeveloper, WorkspaceAccess: all}
	manager := &auth.User{Role: auth.RoleManager, WorkspaceAccess: all}
	admin := &auth.User{Role: auth.RoleAdmin, WorkspaceAccess: all}
	scoped := &auth.User{Role: auth.RoleViewer, WorkspaceAccess: &auth.WorkspaceAccess{
		Grants: []auth.WorkspaceGrant{{Workspace: "ops", Role: auth.RoleAdmin}},
	}}
	writeAndRun := map[config.Permission]bool{config.PermissionWriteDAGs: true, config.PermissionRunDAGs: true}
	writeOnly := map[config.Permission]bool{config.PermissionWriteDAGs: true}

	tests := []struct {
		name        string
		method      string
		path        string
		user        *auth.User
		permissions map[config.Permission]bool
		wantCall    bool
	}{
		{name: "viewer lists DAGs", method: http.MethodGet, path: "/api/v1/dags", user: viewer, wantCall: true},
		{name: "viewer reads DAG list activity", method: http.MethodGet, path: "/api/v1/dag-list-activity", user: viewer, wantCall: true},
		{name: "viewer reads DAG run changes", method: http.MethodGet, path: "/api/v1/dag-run-changes", user: viewer, wantCall: true},
		{name: "viewer reads a step log", method: http.MethodGet, path: "/api/v1/dag-runs/deploy/run-1/steps/build/log", user: viewer, wantCall: true},
		{name: "viewer reads missed slots", method: http.MethodGet, path: "/api/v1/dags/deploy/missed-slots", user: viewer, wantCall: true},
		{name: "viewer reads run history samples", method: http.MethodGet, path: "/api/v1/dags/deploy/run-history/samples", user: viewer, wantCall: true},
		{name: "viewer reads run history events", method: http.MethodGet, path: "/api/v1/dags/deploy/run-history/events", user: viewer, wantCall: true},
		{name: "viewer reads run history availability", method: http.MethodGet, path: "/api/v1/dags/deploy/run-history/availability", user: viewer, wantCall: true},
		{name: "viewer cannot dismiss missed slots", method: http.MethodPost, path: "/api/v1/dags/deploy/missed-slots/dismissal", user: viewer, permissions: writeAndRun},
		{name: "operator dismisses missed slots", method: http.MethodPost, path: "/api/v1/dags/deploy/missed-slots/dismissal", user: operator, permissions: writeAndRun, wantCall: true},
		{name: "dismissal needs run permission", method: http.MethodPost, path: "/api/v1/dags/deploy/missed-slots/dismissal", user: admin, permissions: writeOnly},
		{name: "viewer cannot start a run", method: http.MethodPost, path: "/api/v1/dags/deploy/start", user: viewer, permissions: writeAndRun},
		{name: "operator starts a run", method: http.MethodPost, path: "/api/v1/dags/deploy/start", user: operator, permissions: writeAndRun, wantCall: true},
		{name: "run permission applies to admins", method: http.MethodPost, path: "/api/v1/dags/deploy/start", user: admin, permissions: writeOnly},
		{name: "operator cannot edit a spec", method: http.MethodPut, path: "/api/v1/dags/deploy/spec", user: operator, permissions: writeAndRun},
		{name: "developer edits a spec", method: http.MethodPut, path: "/api/v1/dags/deploy/spec", user: developer, permissions: writeAndRun, wantCall: true},
		{name: "inline spec needs run permission", method: http.MethodPost, path: "/api/v1/dag-runs", user: developer, permissions: writeOnly},
		{name: "operator previews a run", method: http.MethodPost, path: "/api/v1/dags/deploy/start-preview", user: operator, permissions: writeAndRun, wantCall: true},
		{name: "viewer cannot preview a run", method: http.MethodPost, path: "/api/v1/dags/deploy/start-preview", user: viewer, permissions: writeAndRun},
		{name: "developer previews an inline spec", method: http.MethodPost, path: "/api/v1/dag-runs/preview", user: developer, permissions: writeAndRun, wantCall: true},
		{name: "operator cannot preview an inline spec", method: http.MethodPost, path: "/api/v1/dag-runs/preview", user: operator, permissions: writeAndRun},
		{name: "developer reads workers", method: http.MethodGet, path: "/api/v1/workers", user: developer, wantCall: true},
		{name: "viewer lists skills", method: http.MethodGet, path: "/api/v1/skills", user: viewer, wantCall: true},
		{name: "viewer reads a skill", method: http.MethodGet, path: "/api/v1/skills/demo", user: viewer, wantCall: true},
		{name: "viewer reads a skill file", method: http.MethodGet, path: "/api/v1/skills/demo/files", user: viewer, wantCall: true},
		{name: "viewer downloads a skill", method: http.MethodGet, path: "/api/v1/skills/demo/download", user: viewer, wantCall: true},
		{name: "viewer downloads skills", method: http.MethodPost, path: "/api/v1/skills-download", user: viewer, wantCall: true},
		{name: "scoped user cannot download skills", method: http.MethodPost, path: "/api/v1/skills-download", user: scoped},
		{name: "viewer reads a skill named download", method: http.MethodGet, path: "/api/v1/skills/download", user: viewer, wantCall: true},
		{name: "developer writes a skill named download", method: http.MethodPut, path: "/api/v1/skills/download", user: developer, permissions: writeOnly, wantCall: true},
		{name: "developer writes a skill", method: http.MethodPut, path: "/api/v1/skills/demo", user: developer, permissions: writeOnly, wantCall: true},
		{name: "operator cannot write a skill", method: http.MethodPut, path: "/api/v1/skills/demo", user: operator, permissions: writeOnly},
		{name: "developer deletes a skill", method: http.MethodDelete, path: "/api/v1/skills/demo", user: developer, permissions: writeOnly, wantCall: true},
		{name: "viewer cannot delete a skill", method: http.MethodDelete, path: "/api/v1/skills/demo", user: viewer, permissions: writeOnly},
		{name: "developer writes a skill file", method: http.MethodPut, path: "/api/v1/skills/demo/files", user: developer, permissions: writeOnly, wantCall: true},
		{name: "viewer cannot write a skill file", method: http.MethodPut, path: "/api/v1/skills/demo/files", user: viewer, permissions: writeOnly},
		{name: "developer deletes a skill file", method: http.MethodDelete, path: "/api/v1/skills/demo/files", user: developer, permissions: writeOnly, wantCall: true},
		{name: "viewer cannot delete a skill file", method: http.MethodDelete, path: "/api/v1/skills/demo/files", user: viewer, permissions: writeOnly},
		{name: "node lists are never forwarded", method: http.MethodGet, path: "/api/v1/nodes", user: admin, permissions: writeAndRun},
		{name: "nodes are never forwarded", method: http.MethodGet, path: "/api/v1/nodes/web01", user: admin, permissions: writeAndRun},
		{name: "node labels are never forwarded", method: http.MethodGet, path: "/api/v1/node-labels", user: admin, permissions: writeAndRun},
		{name: "inventory lists are never forwarded", method: http.MethodGet, path: "/api/v1/inventories", user: admin, permissions: writeAndRun},
		{name: "inventories are never forwarded", method: http.MethodGet, path: "/api/v1/inventories/inv-1", user: admin, permissions: writeAndRun},
		{name: "inventory imports are never forwarded", method: http.MethodPost, path: "/api/v1/inventory-imports", user: admin, permissions: writeAndRun},
		{name: "inventory changes are never forwarded", method: http.MethodPut, path: "/api/v1/inventories/inv-1/nodes/web01", user: admin, permissions: writeAndRun},
		{name: "node tests are never forwarded", method: http.MethodPost, path: "/api/v1/node-connection-tests", user: admin, permissions: writeAndRun},
		{name: "host key trusts are never forwarded", method: http.MethodPost, path: "/api/v1/node-host-keys", user: admin, permissions: writeAndRun},
		{name: "connections are never forwarded", method: http.MethodGet, path: "/api/v1/connections", user: admin},
		{name: "admin lists worker invites", method: http.MethodGet, path: "/api/v1/workers/invites", user: admin, wantCall: true},
		{name: "manager cannot list worker invites", method: http.MethodGet, path: "/api/v1/workers/invites", user: manager},
		{name: "worker invites are never created remotely", method: http.MethodPost, path: "/api/v1/workers/invites", user: admin},
		{name: "worker invites are never deleted remotely", method: http.MethodDelete, path: "/api/v1/workers/invites/invite-1", user: admin},
		{name: "workers are never removed remotely", method: http.MethodDelete, path: "/api/v1/workers/worker-1/certificate", user: admin},
		{name: "connection removals are never forwarded", method: http.MethodDelete, path: "/api/v1/connections/work", user: admin},
		{name: "OAuth apps are never forwarded", method: http.MethodPost, path: "/api/v1/oauth-apps", user: admin},
		{name: "connects are never forwarded", method: http.MethodPost, path: "/api/v1/connections/work/authorization", user: admin},
		{name: "connection tools are never forwarded", method: http.MethodGet, path: "/api/v1/connections/work/tools", user: admin},
		{name: "connection providers are never forwarded", method: http.MethodGet, path: "/api/v1/connection-providers", user: admin},
		{name: "manager reads audit logs", method: http.MethodGet, path: "/api/v1/audit", user: manager, wantCall: true},
		{name: "developer cannot create an API key", method: http.MethodPost, path: "/api/v1/api-keys", user: developer, permissions: writeAndRun},
		{name: "admin creates an API key", method: http.MethodPost, path: "/api/v1/api-keys", user: admin, wantCall: true},
		{name: "unknown endpoints need an admin", method: http.MethodGet, path: "/api/v1/not-an-endpoint", user: developer},
		{name: "scoped user cannot read", method: http.MethodGet, path: "/api/v1/dags", user: scoped},
		{name: "webhooks are never forwarded", method: http.MethodPost, path: "/api/v1/webhooks/deploy", user: admin},
		{name: "project webhooks are never forwarded", method: http.MethodPost, path: "/api/v1/webhooks/ops/deploy", user: admin},
		{name: "password changes are never forwarded", method: http.MethodPost, path: "/api/v1/auth/change-password", user: admin},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status, called := serveRemoteNodeRequest(t, config.Server{Permissions: test.permissions}, test.user, test.method, test.path)

			assert.Equal(t, test.wantCall, called)
			if !test.wantCall {
				assert.Equal(t, http.StatusForbidden, status)
			}
		})
	}
}

// Only the remote node knows whether its runtime profile is protected, so a run
// that names a profile is checked against the remote node before it is
// forwarded, as the remote node would check it for the caller's own role.
func TestRemoteNodeProfileAuthorization(t *testing.T) {
	all := auth.AllWorkspaceAccess()
	operator := &auth.User{Role: auth.RoleOperator, WorkspaceAccess: all}
	admin := &auth.User{Role: auth.RoleAdmin, WorkspaceAccess: all}
	runPermission := map[config.Permission]bool{config.PermissionRunDAGs: true}

	tests := []struct {
		name       string
		body       string
		user       *auth.User
		wantStatus int
		wantRun    bool
	}{
		{name: "operator runs with an open profile", body: `{"profile":"dev"}`, user: operator, wantStatus: http.StatusOK, wantRun: true},
		{name: "operator runs without a profile", body: `{}`, user: operator, wantStatus: http.StatusOK, wantRun: true},
		{name: "operator cannot run with a protected profile", body: `{"profile":" prod "}`, user: operator, wantStatus: http.StatusForbidden},
		{name: "admin runs with a protected profile", body: `{"profile":"prod"}`, user: admin, wantStatus: http.StatusOK, wantRun: true},
		{name: "unknown profile is not forwarded", body: `{"profile":"missing"}`, user: operator, wantStatus: http.StatusNotFound},
		// The node's credential may not read profiles, so the node itself
		// refuses a protected profile to it.
		{name: "operator runs when the node credential cannot read profiles", body: `{"profile":"hidden"}`, user: operator, wantStatus: http.StatusOK, wantRun: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var forwardedBody atomic.Value
			remote := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/v1/profiles/dev":
					_, _ = w.Write([]byte(`{"protected":false}`))
				case "/api/v1/profiles/prod":
					_, _ = w.Write([]byte(`{"protected":true}`))
				case "/api/v1/profiles/hidden":
					w.WriteHeader(http.StatusForbidden)
					_, _ = w.Write([]byte(`{"code":"forbidden","message":"insufficient permissions"}`))
				case "/api/v1/dags/deploy/start":
					body, _ := io.ReadAll(r.Body)
					forwardedBody.Store(string(body))
					_, _ = w.Write([]byte(`{}`))
				default:
					w.WriteHeader(http.StatusNotFound)
					_, _ = w.Write([]byte(`{"code":"not_found","message":"not found"}`))
				}
			})

			status := serveRemoteNodeRequestTo(t, remote, config.Server{Permissions: runPermission}, test.user, http.MethodPost, "/api/v1/dags/deploy/start", test.body, nil)

			assert.Equal(t, test.wantStatus, status)
			if test.wantRun {
				assert.Equal(t, test.body, forwardedBody.Load())
			} else {
				assert.Nil(t, forwardedBody.Load())
			}
		})
	}
}

// A proxy in front of the remote node may authenticate requests by a header the
// caller sends, such as a cookie. The probe carries the same headers as the
// run, so the proxy cannot reject the probe and then accept the run.
func TestRemoteNodeProfileProbeSendsCallerHeaders(t *testing.T) {
	operator := &auth.User{Role: auth.RoleOperator, WorkspaceAccess: auth.AllWorkspaceAccess()}
	var forwarded atomic.Bool
	remote := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if c, err := r.Cookie("proxy_session"); err != nil || c.Value != "admin" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"code":"unauthorized","message":"sign in"}`))
			return
		}
		switch r.URL.Path {
		case "/api/v1/profiles/prod":
			_, _ = w.Write([]byte(`{"protected":true}`))
		case "/api/v1/dags/deploy/start":
			forwarded.Store(true)
			_, _ = w.Write([]byte(`{}`))
		}
	})
	header := http.Header{"Cookie": {"proxy_session=admin"}}

	status := serveRemoteNodeRequestTo(t, remote, config.Server{Permissions: map[config.Permission]bool{config.PermissionRunDAGs: true}}, operator, http.MethodPost, "/api/v1/dags/deploy/start", `{"profile":"prod"}`, header)

	assert.Equal(t, http.StatusForbidden, status)
	assert.False(t, forwarded.Load())
}

func TestRemoteNodeStreamAuthorization(t *testing.T) {
	viewer := &auth.User{Role: auth.RoleViewer, WorkspaceAccess: auth.AllWorkspaceAccess()}
	scoped := &auth.User{Role: auth.RoleViewer, WorkspaceAccess: &auth.WorkspaceAccess{
		Grants: []auth.WorkspaceGrant{{Workspace: "ops", Role: auth.RoleAdmin}},
	}}
	tests := []struct {
		name     string
		query    string
		user     *auth.User
		wantNext bool
	}{
		{name: "local stream", query: "", user: scoped, wantNext: true},
		{name: "viewer opens a remote stream", query: "?remoteNode=edge", user: viewer, wantNext: true},
		{name: "scoped user cannot open a remote stream", query: "?remoteNode=edge", user: scoped},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			a := &API{config: &config.Config{}, authService: remoteSyncAuthService{}}
			var nextCalled bool
			handler := a.RemoteNodeStreamAuthorization(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				nextCalled = true
			}))
			request := httptest.NewRequest(http.MethodGet, "/api/v1/events/stream"+test.query, nil)
			request = request.WithContext(auth.WithUser(request.Context(), test.user))
			recorder := httptest.NewRecorder()

			handler.ServeHTTP(recorder, request)

			assert.Equal(t, test.wantNext, nextCalled)
			if !test.wantNext {
				assert.Equal(t, http.StatusForbidden, recorder.Code)
			}
		})
	}
}

func TestRemoteSyncAuthorization(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		path       string
		user       *auth.User
		wantStatus int
		wantCall   bool
	}{
		{
			name:   "scoped user cannot read",
			method: http.MethodGet,
			path:   "/api/v1/sync/status",
			user: &auth.User{
				Role: auth.RoleViewer,
				WorkspaceAccess: &auth.WorkspaceAccess{Grants: []auth.WorkspaceGrant{
					{Workspace: "ops", Role: auth.RoleDeveloper},
				}},
			},
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "operator cannot write",
			method:     http.MethodPost,
			path:       "/api/v1/sync/pull",
			user:       &auth.User{Role: auth.RoleOperator, WorkspaceAccess: auth.AllWorkspaceAccess()},
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "developer cannot test connection",
			method:     http.MethodPost,
			path:       "/api/v1/sync/test-connection",
			user:       &auth.User{Role: auth.RoleDeveloper, WorkspaceAccess: auth.AllWorkspaceAccess()},
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "viewer can read",
			method:     http.MethodGet,
			path:       "/api/v1/sync/status",
			user:       &auth.User{Role: auth.RoleViewer, WorkspaceAccess: auth.AllWorkspaceAccess()},
			wantStatus: http.StatusOK,
			wantCall:   true,
		},
		{
			name:       "developer can write",
			method:     http.MethodPost,
			path:       "/api/v1/sync/pull",
			user:       &auth.User{Role: auth.RoleDeveloper, WorkspaceAccess: auth.AllWorkspaceAccess()},
			wantStatus: http.StatusOK,
			wantCall:   true,
		},
		{
			name:       "admin can test connection",
			method:     http.MethodPost,
			path:       "/api/v1/sync/test-connection",
			user:       &auth.User{Role: auth.RoleAdmin, WorkspaceAccess: auth.AllWorkspaceAccess()},
			wantStatus: http.StatusOK,
			wantCall:   true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := config.Server{
				StrictValidation: true,
				Permissions: map[config.Permission]bool{
					config.PermissionWriteDAGs: true,
				},
			}
			status, called := serveRemoteNodeRequest(t, server, test.user, test.method, test.path)

			assert.Equal(t, test.wantStatus, status)
			assert.Equal(t, test.wantCall, called)
		})
	}
}

func TestRemoteLicenseAuthorization(t *testing.T) {
	viewer := &auth.User{Role: auth.RoleViewer, WorkspaceAccess: auth.AllWorkspaceAccess()}
	admin := &auth.User{Role: auth.RoleAdmin, WorkspaceAccess: auth.AllWorkspaceAccess()}
	tests := []struct {
		name       string
		method     string
		path       string
		user       *auth.User
		wantCall   bool
		wantStatus int
	}{
		{name: "viewer reads status", method: http.MethodGet, path: "/api/v1/license/status", user: viewer, wantCall: true},
		{name: "viewer cannot deactivate", method: http.MethodPost, path: "/api/v1/license/deactivate", user: viewer},
		{name: "viewer cannot start a connection", method: http.MethodPost, path: "/api/v1/license/connect", user: viewer},
		{name: "viewer cannot read a connection", method: http.MethodGet, path: "/api/v1/license/connect", user: viewer},
		{name: "admin can start a connection", method: http.MethodPost, path: "/api/v1/license/connect", user: admin, wantCall: true},
		// Paths that match no operation need an admin, and the proxy refuses
		// to forward a path that differs from its clean form.
		{name: "doubled slash", method: http.MethodPost, path: "/api/v1//license/deactivate", user: viewer},
		{name: "dot segment", method: http.MethodPost, path: "/api/v1/x/../license/deactivate", user: viewer},
		{name: "encoded slash", method: http.MethodPost, path: "/api/v1/%2Flicense/deactivate", user: viewer},
		{name: "doubled slash to sync", method: http.MethodPost, path: "/api/v1//sync/push", user: viewer},
		{name: "admin doubled slash", method: http.MethodPost, path: "/api/v1//license/deactivate", user: admin, wantStatus: http.StatusBadRequest},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status, called := serveRemoteNodeRequest(t, config.Server{}, test.user, test.method, test.path)

			assert.Equal(t, test.wantCall, called)
			if !test.wantCall {
				want := test.wantStatus
				if want == 0 {
					want = http.StatusForbidden
				}
				assert.Equal(t, want, status)
			}
		})
	}
}

func TestIsCleanPath(t *testing.T) {
	for p, want := range map[string]bool{
		"/api/v1/license/status":  true,
		"/api/v1/dags/":           true,
		"/":                       true,
		"/api/v1//license/status": false,
		"/api/v1/./license":       false,
		"/api/v1/x/../license":    false,
		"/api/v1/dags//":          false,
	} {
		assert.Equal(t, want, isCleanPath(p), p)
	}
}

func TestRemoteNodeProxyPreservesHumanTaskCompletionRequest(t *testing.T) {
	const rawBody = "{\n  \"count\": 9007199254740993\n}\n"
	type receivedRequest struct {
		path          string
		rawQuery      string
		body          string
		authorization string
		contentType   string
		customHeader  string
		contentLength int64
		err           error
	}
	receivedRequests := make(chan receivedRequest, 1)

	remoteServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		receivedRequests <- receivedRequest{
			path:          r.URL.Path,
			rawQuery:      r.URL.RawQuery,
			body:          string(body),
			authorization: r.Header.Get("Authorization"),
			contentType:   r.Header.Get("Content-Type"),
			customHeader:  r.Header.Get("X-Request-ID"),
			contentLength: r.ContentLength,
			err:           err,
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(remoteServer.Close)

	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/dag-runs/deploy/run-1/human-tasks/review/complete?remoteNode=edge&keep=1",
		strings.NewReader(rawBody),
	)
	request.Header.Set("Authorization", "Bearer caller-token")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Request-ID", "request-1")
	proxy := remoteNodeProxy{
		remoteNode: &remotenode.RemoteNode{
			APIBaseURL: remoteServer.URL + "/api/v1",
			AuthType:   remotenode.AuthTypeToken,
			AuthToken:  "remote-token",
		},
		apiBasePath: "/api/v1",
	}

	response, err := proxy.proxy(request)
	require.NoError(t, err)
	t.Cleanup(func() { _ = response.Body.Close() })

	received := <-receivedRequests
	assert.Equal(t, http.StatusOK, response.StatusCode)
	require.NoError(t, received.err)
	assert.Equal(t, "/api/v1/dag-runs/deploy/run-1/human-tasks/review/complete", received.path)
	assert.Equal(t, "keep=1", received.rawQuery)
	assert.Equal(t, rawBody, received.body)
	assert.Equal(t, "Bearer remote-token", received.authorization)
	assert.Equal(t, "application/json", received.contentType)
	assert.Equal(t, "request-1", received.customHeader)
	assert.Equal(t, int64(len(rawBody)), received.contentLength)
}

// The local permission check names the operation from the path as escaped,
// so the remote node must receive that path rather than its decoded form, in
// which an escaped slash would name a different operation.
func TestRemoteNodeProxyKeepsEscapedPath(t *testing.T) {
	received := make(chan string, 1)
	remoteServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received <- r.URL.EscapedPath()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(remoteServer.Close)

	request := httptest.NewRequest(http.MethodGet, "/api/v1/dag-runs/job%2Frun-1%2Fdequeue?remoteNode=edge", nil)
	proxy := remoteNodeProxy{
		remoteNode:  &remotenode.RemoteNode{APIBaseURL: remoteServer.URL + "/api/v1"},
		apiBasePath: "/api/v1",
	}

	response, err := proxy.proxy(request)
	require.NoError(t, err)
	t.Cleanup(func() { _ = response.Body.Close() })

	assert.Equal(t, "/api/v1/dag-runs/job%2Frun-1%2Fdequeue", <-received)
}

func TestRemoteNodeProxyPreservesStructuredError(t *testing.T) {
	const responseBody = `{
		"code":"human_task_resume_failed",
		"message":"completion was saved but resume failed",
		"details":{"completionStored":true,"resumePending":true}
	}`
	remoteServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(responseBody))
	}))
	t.Cleanup(remoteServer.Close)

	resolver := remotenode.NewResolver([]config.RemoteNode{{
		Name:       "edge",
		APIBaseURL: remoteServer.URL + "/api/v1",
	}}, nil)
	handler := WithRemoteNode(resolver, "/api/v1")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "local handler called", http.StatusInternalServerError)
	}))
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/dag-runs/deploy/run-1/human-tasks/review/complete?remoteNode=edge",
		strings.NewReader(`{}`),
	)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	assert.Equal(t, "application/json", recorder.Header().Get("Content-Type"))
	assert.JSONEq(t, responseBody, recorder.Body.String())
}

// A skill file read through a remote node keeps the headers that name the
// skill version and the file.
func TestRemoteNodeProxyKeepsFileHeaders(t *testing.T) {
	remoteServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", `attachment; filename="a.bin"`)
		w.Header().Set("ETag", `"abc123"`)
		_, _ = w.Write([]byte("bytes"))
	}))
	t.Cleanup(remoteServer.Close)

	resolver := remotenode.NewResolver([]config.RemoteNode{{
		Name:       "edge",
		APIBaseURL: remoteServer.URL + "/api/v1",
	}}, nil)
	handler := WithRemoteNode(resolver, "/api/v1")(http.NotFoundHandler())
	request := httptest.NewRequest(http.MethodGet, "/api/v1/skills/demo/files?path=a.bin&remoteNode=edge", nil)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, `"abc123"`, recorder.Header().Get("ETag"))
	assert.Equal(t, `attachment; filename="a.bin"`, recorder.Header().Get("Content-Disposition"))
	assert.Equal(t, "bytes", recorder.Body.String())
}

func TestLegacyWikiProxyPath(t *testing.T) {
	tests := map[string]string{
		"/api/v1/wiki":                        "/api/v1/docs",
		"/api/v1/wiki/search":                 "/api/v1/docs/search",
		"/api/v1/wiki/page":                   "/api/v1/docs/doc",
		"/api/v1/wiki/page/revisions":         "/api/v1/docs/doc/revisions",
		"/api/v1/search/wiki/matches":         "/api/v1/search/docs/matches",
		"/api/v1/events/wiki-tree":            "/api/v1/events/docs-tree",
		"/api/v1/events/wiki/guides%2Fdeploy": "/api/v1/events/docs/guides%2Fdeploy",
	}
	for requestPath, expected := range tests {
		t.Run(requestPath, func(t *testing.T) {
			actual, ok := legacyWikiProxyPath(requestPath, "/api/v1")
			require.True(t, ok)
			assert.Equal(t, expected, actual)
		})
	}
}

func TestRemoteLogDownloadStreams(t *testing.T) {
	for _, test := range []struct {
		suffix      string
		contentType string
	}{
		{suffix: "/steps/log/download", contentType: stepLogArchiveContentType},
		{suffix: "/sub-dag-runs/child/steps/log/download", contentType: stepLogArchiveContentType},
		{suffix: "/log/download", contentType: "text/plain"},
		{suffix: "/steps/build/log/download", contentType: "text/plain"},
		{suffix: "/sub-dag-runs/child/log/download", contentType: "text/plain"},
		{suffix: "/sub-dag-runs/child/steps/build/log/download", contentType: "text/plain"},
	} {
		t.Run(test.suffix, func(t *testing.T) {
			first := bytes.Repeat([]byte("log bytes"), 4096)
			release := make(chan struct{})
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "/api/v1/dag-runs/example/run"+test.suffix, r.URL.Path)
				w.Header().Set("Content-Type", test.contentType)
				w.Header().Set("Content-Disposition", `attachment; filename="remote.log"`)
				_, _ = w.Write(first)
				_ = http.NewResponseController(w).Flush()
				select {
				case <-release:
					_, _ = w.Write([]byte("tail"))
				case <-r.Context().Done():
				}
			}))
			defer remote.Close()
			defer close(release)
			resolver := remotenode.NewResolver([]config.RemoteNode{{Name: "edge", APIBaseURL: remote.URL + "/api/v1"}}, nil)
			handler := WithRemoteNode(resolver, "/dagu/api/v1")(http.NotFoundHandler())
			proxy := httptest.NewUnstartedServer(logDownloadDeadline("/dagu/api/v1")(handler))
			proxy.Config.WriteTimeout = 50 * time.Millisecond
			proxy.Start()
			defer proxy.Close()
			client := proxy.Client()
			client.Timeout = 5 * time.Second
			resp, err := client.Get(proxy.URL + "/dagu/api/v1/dag-runs/example/run" + test.suffix + "?remoteNode=edge")
			require.NoError(t, err)
			defer func() { _ = resp.Body.Close() }()
			require.Equal(t, test.contentType, resp.Header.Get("Content-Type"))
			require.Equal(t, `attachment; filename="remote.log"`, resp.Header.Get("Content-Disposition"))
			got := make([]byte, 4096)
			_, err = io.ReadFull(resp.Body, got)
			require.NoError(t, err)
			require.Equal(t, first[:len(got)], got)
			// Finish after the normal server write deadline, once streaming is observed.
			time.Sleep(100 * time.Millisecond)
			release <- struct{}{}
			tail, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.Equal(t, append(first[len(got):], []byte("tail")...), tail)
		})
	}
}

// A skill ZIP streams through a remote node with its headers, also past the
// write deadline of the server, so the proxy never holds the whole ZIP.
func TestRemoteSkillDownloadStreams(t *testing.T) {
	for _, test := range []struct{ method, path string }{
		{method: http.MethodGet, path: "/skills/demo/download"},
		{method: http.MethodPost, path: "/skills-download"},
	} {
		t.Run(test.method, func(t *testing.T) {
			first := bytes.Repeat([]byte("zip bytes"), 4096)
			release := make(chan struct{})
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, test.method+" /api/v1"+test.path, r.Method+" "+r.URL.Path)
				w.Header().Set("Content-Type", "application/zip")
				w.Header().Set("Content-Disposition", `attachment; filename="demo.zip"`)
				w.Header().Set("Cache-Control", "no-store")
				_, _ = w.Write(first)
				_ = http.NewResponseController(w).Flush()
				select {
				case <-release:
					_, _ = w.Write([]byte("tail"))
				case <-r.Context().Done():
				}
			}))
			defer remote.Close()
			defer close(release)
			resolver := remotenode.NewResolver([]config.RemoteNode{{Name: "edge", APIBaseURL: remote.URL + "/api/v1"}}, nil)
			handler := WithRemoteNode(resolver, "/api/v1")(http.NotFoundHandler())
			proxy := httptest.NewUnstartedServer(logDownloadDeadline("/api/v1")(handler))
			proxy.Config.WriteTimeout = 50 * time.Millisecond
			proxy.Start()
			defer proxy.Close()
			client := proxy.Client()
			client.Timeout = 5 * time.Second
			request, err := http.NewRequest(test.method, proxy.URL+"/api/v1"+test.path+"?remoteNode=edge", strings.NewReader(`{"names":["demo"]}`))
			require.NoError(t, err)
			resp, err := client.Do(request)
			require.NoError(t, err)
			defer func() { _ = resp.Body.Close() }()
			require.Equal(t, http.StatusOK, resp.StatusCode)
			require.Equal(t, `attachment; filename="demo.zip"`, resp.Header.Get("Content-Disposition"))
			require.Equal(t, "no-store", resp.Header.Get("Cache-Control"))
			got := make([]byte, 4096)
			_, err = io.ReadFull(resp.Body, got)
			require.NoError(t, err)
			// Finish after the normal server write deadline, once streaming is observed.
			time.Sleep(100 * time.Millisecond)
			release <- struct{}{}
			tail, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.Equal(t, append(first, []byte("tail")...), append(got, tail...))
		})
	}
}

// A remote node may start a skill ZIP before it has read the request body.
// The proxy keeps forwarding the body while the ZIP streams back, so a client
// that sends the rest of its body after the headers arrive still gets the ZIP.
func TestRemoteSkillDownloadBodyAfterHeaders(t *testing.T) {
	const head, rest = `{"names":`, `["demo"]}`
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.NoError(t, http.NewResponseController(w).EnableFullDuplex())
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write([]byte("zip"))
		_ = http.NewResponseController(w).Flush()
		body, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		assert.Equal(t, head+rest, string(body))
		_, _ = w.Write([]byte("tail"))
	}))
	defer remote.Close()
	resolver := remotenode.NewResolver([]config.RemoteNode{{Name: "edge", APIBaseURL: remote.URL + "/api/v1"}}, nil)
	proxy := httptest.NewServer(WithRemoteNode(resolver, "/api/v1")(http.NotFoundHandler()))
	defer proxy.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	bodyReader, bodyWriter := io.Pipe()
	// The client waits for its body to end even after the deadline, so the
	// deadline also ends the body.
	context.AfterFunc(ctx, func() { _ = bodyWriter.CloseWithError(ctx.Err()) })
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, proxy.URL+"/api/v1/skills-download?remoteNode=edge", bodyReader)
	require.NoError(t, err)
	go func() { _, _ = bodyWriter.Write([]byte(head)) }()
	resp, err := proxy.Client().Do(request)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	_, _ = bodyWriter.Write([]byte(rest))
	require.NoError(t, bodyWriter.Close())

	got, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, "ziptail", string(got))
}

func TestRemoteLogDownloadAbort(t *testing.T) {
	for _, suffix := range []string{"/steps/log/download", "/steps/build/log/download"} {
		t.Run(suffix, func(t *testing.T) {
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Length", "100")
				_, _ = w.Write([]byte("partial"))
			}))
			defer remote.Close()
			resolver := remotenode.NewResolver([]config.RemoteNode{{Name: "edge", APIBaseURL: remote.URL + "/api/v1"}}, nil)
			handler := WithRemoteNode(resolver, "/api/v1")(http.NotFoundHandler())
			request := httptest.NewRequest(http.MethodGet, "/api/v1/dag-runs/example/run"+suffix+"?remoteNode=edge", nil)
			recorder := httptest.NewRecorder()
			require.PanicsWithValue(t, http.ErrAbortHandler, func() { handler.ServeHTTP(recorder, request) })
			require.Equal(t, "partial", recorder.Body.String())
		})
	}
}

// Log download bodies are forwarded unchanged, so the remote may only pick an
// encoding the client accepts.
func TestRemoteLogDownloadEncoding(t *testing.T) {
	for _, test := range []struct {
		name       string
		accept     string
		wantAccept string
	}{
		{name: "gzip", accept: "gzip", wantAccept: "gzip"},
		{name: "none", wantAccept: "identity"},
	} {
		t.Run(test.name, func(t *testing.T) {
			remote := httptest.NewServer(middleware.Compress(5)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, test.wantAccept, r.Header.Get("Accept-Encoding"))
				w.Header().Set("Content-Type", "text/plain")
				_, _ = w.Write([]byte("log body"))
			})))
			defer remote.Close()
			resolver := remotenode.NewResolver([]config.RemoteNode{{Name: "edge", APIBaseURL: remote.URL + "/api/v1"}}, nil)
			handler := WithRemoteNode(resolver, "/api/v1")(http.NotFoundHandler())
			request := httptest.NewRequest(http.MethodGet, "/api/v1/dag-runs/example/run/log/download?remoteNode=edge", nil)
			if test.accept != "" {
				request.Header.Set("Accept-Encoding", test.accept)
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			require.Equal(t, http.StatusOK, recorder.Code)
			body := io.Reader(recorder.Body)
			if test.accept == "gzip" {
				require.Equal(t, "gzip", recorder.Header().Get("Content-Encoding"))
				require.Contains(t, recorder.Header().Values("Vary"), "Accept-Encoding")
				reader, err := gzip.NewReader(body)
				require.NoError(t, err)
				body = reader
			} else {
				require.Empty(t, recorder.Header().Get("Content-Encoding"))
			}
			got, err := io.ReadAll(body)
			require.NoError(t, err)
			require.Equal(t, "log body", string(got))
		})
	}
}

// Writers that cannot flush still receive the whole log download.
func TestRemoteLogDownloadWithoutFlush(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("log body"))
	}))
	defer remote.Close()
	resolver := remotenode.NewResolver([]config.RemoteNode{{Name: "edge", APIBaseURL: remote.URL + "/api/v1"}}, nil)
	handler := WithRemoteNode(resolver, "/api/v1")(http.NotFoundHandler())
	request := httptest.NewRequest(http.MethodGet, "/api/v1/dag-runs/example/run/log/download?remoteNode=edge", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(struct{ http.ResponseWriter }{recorder}, request)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "log body", recorder.Body.String())
}

// A remote that stops sending mid-download fails the transfer instead of
// holding the client connection open.
func TestRemoteLogDownloadIdleTimeout(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("partial"))
		_ = http.NewResponseController(w).Flush()
		<-r.Context().Done()
	}))
	defer remote.Close()
	proxy := remoteNodeProxy{
		remoteNode:     &remotenode.RemoteNode{APIBaseURL: remote.URL + "/api/v1"},
		apiBasePath:    "/api/v1",
		logIdleTimeout: 50 * time.Millisecond,
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/dag-runs/example/run/log/download?remoteNode=edge", nil)
	resp, err := proxy.proxy(request)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	require.ErrorIs(t, err, os.ErrDeadlineExceeded)
	require.Equal(t, "partial", string(body))
}

func TestIsLogDownload(t *testing.T) {
	for _, suffix := range []string{
		"/log/download",
		"/steps/build/log/download",
		"/steps/log/download",
		"/sub-dag-runs/child/log/download",
		"/sub-dag-runs/child/steps/build/log/download",
		"/sub-dag-runs/child/steps/log/download",
		"/steps/loop/foreach/0/steps/build/log/download",
		"/sub-dag-runs/child/steps/loop/foreach/0/steps/build/log/download",
	} {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/dag-runs/example/run"+suffix, nil)
		assert.True(t, isLogDownload(request, "/api/v1"), suffix)
	}
	for _, path := range []string{
		"/api/v1/dag-runs/example/run/log",
		"/api/v1/dag-runs/example/run/steps/build/log",
		"/api/v1/dags/example/log/download",
	} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		assert.False(t, isLogDownload(request, "/api/v1"), path)
	}
}

func TestRemoteStepLogForm(t *testing.T) {
	for _, suffix := range []string{"/steps/log/download", "/sub-dag-runs/child/steps/log/download"} {
		t.Run(suffix, func(t *testing.T) {
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodGet, r.Method)
				assert.Equal(t, "/api/v1/dag-runs/example/run"+suffix, r.URL.Path)
				assert.Empty(t, r.URL.RawQuery)
				body, err := io.ReadAll(r.Body)
				assert.NoError(t, err)
				assert.Empty(t, body)
				assert.Zero(t, r.ContentLength)
				assert.Equal(t, "Bearer remote-token", r.Header.Get("Authorization"))
				assert.Equal(t, "identity", r.Header.Get("Accept-Encoding"))
				w.Header().Set("Content-Type", stepLogArchiveContentType)
				w.Header().Set("Content-Disposition", `attachment; filename="remote.zip"`)
				_, _ = w.Write([]byte("archive"))
			}))
			defer remote.Close()
			resolver := remotenode.NewResolver([]config.RemoteNode{{Name: "edge", APIBaseURL: remote.URL + "/api/v1", AuthType: "token", AuthToken: "remote-token"}}, nil)
			a := &API{
				config:             &config.Config{Server: config.Server{BasePath: "/dagu", APIBasePath: "/api/v1", StrictValidation: true, Auth: config.Auth{Mode: config.AuthModeBuiltin}}},
				authService:        remoteSyncAuthService{user: &auth.User{Role: auth.RoleAdmin, WorkspaceAccess: auth.AllWorkspaceAccess()}},
				remoteNodeResolver: resolver,
			}
			router := chi.NewRouter()
			require.NoError(t, a.ConfigureRoutes(t.Context(), router, time.Second))
			request := httptest.NewRequest(http.MethodPost, "/dagu/api/v1/dag-runs/example/run"+suffix+"?remoteNode=edge", strings.NewReader("token=caller-token"))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)
			require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
			require.Equal(t, "archive", recorder.Body.String())
		})
	}
}
