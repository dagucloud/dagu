// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
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
	remoteServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		remoteCalled.Store(true)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
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

	request := httptest.NewRequest(method, path+"?remoteNode=edge", nil)
	request.Header.Set("Authorization", "Bearer caller-token")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder.Code, remoteCalled.Load()
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
		{name: "viewer reads a step log", method: http.MethodGet, path: "/api/v1/dag-runs/deploy/run-1/steps/build/log", user: viewer, wantCall: true},
		{name: "viewer cannot start a run", method: http.MethodPost, path: "/api/v1/dags/deploy/start", user: viewer, permissions: writeAndRun},
		{name: "operator starts a run", method: http.MethodPost, path: "/api/v1/dags/deploy/start", user: operator, permissions: writeAndRun, wantCall: true},
		{name: "run permission applies to admins", method: http.MethodPost, path: "/api/v1/dags/deploy/start", user: admin, permissions: writeOnly},
		{name: "operator cannot edit a spec", method: http.MethodPut, path: "/api/v1/dags/deploy/spec", user: operator, permissions: writeAndRun},
		{name: "developer edits a spec", method: http.MethodPut, path: "/api/v1/dags/deploy/spec", user: developer, permissions: writeAndRun, wantCall: true},
		{name: "inline spec needs run permission", method: http.MethodPost, path: "/api/v1/dag-runs", user: developer, permissions: writeOnly},
		{name: "developer reads workers", method: http.MethodGet, path: "/api/v1/workers", user: developer, wantCall: true},
		{name: "manager reads audit logs", method: http.MethodGet, path: "/api/v1/audit", user: manager, wantCall: true},
		{name: "developer cannot create an API key", method: http.MethodPost, path: "/api/v1/api-keys", user: developer, permissions: writeAndRun},
		{name: "admin creates an API key", method: http.MethodPost, path: "/api/v1/api-keys", user: admin, wantCall: true},
		{name: "unknown endpoints need an admin", method: http.MethodGet, path: "/api/v1/not-an-endpoint", user: developer},
		{name: "scoped user cannot read", method: http.MethodGet, path: "/api/v1/dags", user: scoped},
		{name: "webhooks are never forwarded", method: http.MethodPost, path: "/api/v1/webhooks/deploy", user: admin},
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
