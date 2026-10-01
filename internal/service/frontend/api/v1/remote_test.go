// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/remotenode"
	"github.com/go-chi/chi/v5"
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
			var remoteCalled atomic.Bool
			remoteServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				remoteCalled.Store(true)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"success":true}`))
			}))
			t.Cleanup(remoteServer.Close)

			resolver := remotenode.NewResolver([]config.RemoteNode{{
				Name:       "edge",
				APIBaseURL: remoteServer.URL + "/api/v1",
			}}, nil)
			cfg := &config.Config{
				Server: config.Server{
					APIBasePath:      "/api/v1",
					StrictValidation: true,
					Auth: config.Auth{
						Mode: config.AuthModeBuiltin,
					},
					Permissions: map[config.Permission]bool{
						config.PermissionWriteDAGs: true,
					},
				},
			}
			a := &API{
				config:             cfg,
				authService:        remoteSyncAuthService{user: test.user},
				remoteNodeResolver: resolver,
			}
			router := chi.NewRouter()
			require.NoError(t, a.ConfigureRoutes(t.Context(), router, time.Second))

			request := httptest.NewRequest(test.method, test.path+"?remoteNode=edge", nil)
			request.Header.Set("Authorization", "Bearer caller-token")
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)

			assert.Equal(t, test.wantStatus, recorder.Code)
			assert.Equal(t, test.wantCall, remoteCalled.Load())
		})
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

func TestRemoteStepLogDownloadStreams(t *testing.T) {
	for _, suffix := range []string{"/steps/log/download", "/sub-dag-runs/child/steps/log/download"} {
		t.Run(suffix, func(t *testing.T) {
			first := bytes.Repeat([]byte("archive bytes"), 4096)
			release := make(chan struct{})
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "/api/v1/dag-runs/example/run"+suffix, r.URL.Path)
				assert.Equal(t, "identity", r.Header.Get("Accept-Encoding"))
				w.Header().Set("Content-Type", stepLogArchiveContentType)
				w.Header().Set("Content-Disposition", `attachment; filename="remote.zip"`)
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
			resp, err := client.Get(proxy.URL + "/dagu/api/v1/dag-runs/example/run" + suffix + "?remoteNode=edge")
			require.NoError(t, err)
			defer func() { _ = resp.Body.Close() }()
			require.Equal(t, stepLogArchiveContentType, resp.Header.Get("Content-Type"))
			require.Equal(t, `attachment; filename="remote.zip"`, resp.Header.Get("Content-Disposition"))
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

func TestRemoteStepLogDownloadAbort(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", stepLogArchiveContentType)
		w.Header().Set("Content-Length", "100")
		_, _ = w.Write([]byte("partial"))
	}))
	defer remote.Close()
	resolver := remotenode.NewResolver([]config.RemoteNode{{Name: "edge", APIBaseURL: remote.URL + "/api/v1"}}, nil)
	handler := WithRemoteNode(resolver, "/api/v1")(http.NotFoundHandler())
	request := httptest.NewRequest(http.MethodGet, "/api/v1/dag-runs/example/run/steps/log/download?remoteNode=edge", nil)
	recorder := httptest.NewRecorder()
	require.PanicsWithValue(t, http.ErrAbortHandler, func() { handler.ServeHTTP(recorder, request) })
	require.Equal(t, "partial", recorder.Body.String())
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
