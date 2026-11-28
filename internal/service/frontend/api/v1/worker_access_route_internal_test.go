// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
)

// bodyWorkerIDs are worker IDs that a path segment could not carry
// unchanged: fixed segments under /workers/, slashes, escapes, and dot
// segments.
var bodyWorkerIDs = []string{"invites", "join-readiness", "a/b", "a%2Fb", "literal%", "a/", ".", ".."}

// validationModes are the server's input validation settings.
var validationModes = map[string]bool{"strict": true, "lenient": false}

// workerAccessServer serves the API routes as the server mounts them, with
// strict input validation on or off, for a caller of role.
func workerAccessServer(t *testing.T, coordinatorCli coordinator.Client, role auth.Role, strict bool) http.Handler {
	t.Helper()
	a, _ := rolesAPI(coordinatorCli)
	a.config = &config.Config{Server: config.Server{
		APIBasePath:      "/api/v1",
		Auth:             config.Auth{Mode: config.AuthModeBuiltin},
		StrictValidation: strict,
	}}
	a.authService = remoteSyncAuthService{user: &auth.User{
		ID: "user-1", Username: "user", Role: role, WorkspaceAccess: auth.AllWorkspaceAccess(),
	}}
	router := chi.NewRouter()
	router.Route("/api/v1", func(r chi.Router) {
		require.NoError(t, a.ConfigureRoutes(t.Context(), r, time.Second))
	})
	return router
}

// serve sends a request with body, when not nil, as JSON, and returns the
// response status.
func serve(t *testing.T, handler http.Handler, method, target string, body any) int {
	t.Helper()
	var payload string
	if body != nil {
		encoded, err := json.Marshal(body)
		require.NoError(t, err)
		payload = string(encoded)
	}
	request := httptest.NewRequest(method, target, strings.NewReader(payload))
	request.Header.Set("Authorization", "Bearer caller-token")
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder.Code
}

func removal(workerID string) map[string]string {
	return map[string]string{"workerId": workerID, "certificateId": "cert-1"}
}

// A removal names the worker in its body, so any worker ID reaches the
// removal, and the fixed routes keep their own handlers.
func TestRemoveWorkerTakesBodyWorkerIDs(t *testing.T) {
	t.Parallel()
	for name, strict := range validationModes {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			router := workerAccessServer(t, &inviteCoordinatorStub{}, auth.RoleAdmin, strict)
			for _, id := range bodyWorkerIDs {
				assert.Equal(t, http.StatusNotImplemented, serve(t, router, http.MethodPost, "/api/v1/workers/removals", removal(id)), id)
			}
			assert.Equal(t, http.StatusOK, serve(t, router, http.MethodGet, "/api/v1/workers/invites", nil))
			assert.Equal(t, http.StatusNoContent, serve(t, router, http.MethodDelete, "/api/v1/workers/invites/"+testInviteID, nil))
		})
	}
}

// An invite address whose host is no hostname or IP address answers 400
// before any coordinator call, in both validation modes.
func TestCreateWorkerInviteRefusesMalformedHost(t *testing.T) {
	t.Parallel()
	for name, strict := range validationModes {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			stub := &inviteCoordinatorStub{}
			router := workerAccessServer(t, stub, auth.RoleAdmin, strict)
			for _, addr := range []string{
				"coordinator.example.com/path:50055",
				"coordinator.example.com?q:50055",
				"user@coordinator.example.com:50055",
				"coordinator example.com:50055",
				"[192.168.1.20]:50055",
			} {
				status := serve(t, router, http.MethodPost, "/api/v1/workers/invites", map[string]string{"address": addr})
				assert.Equal(t, http.StatusBadRequest, status, addr)
			}
			assert.Nil(t, stub.created, "no invite reached the coordinator")
		})
	}
}

// A removal for a remote node is refused locally whatever worker it names.
func TestRemoveWorkerNeverRemote(t *testing.T) {
	t.Parallel()
	admin := &auth.User{Role: auth.RoleAdmin, WorkspaceAccess: auth.AllWorkspaceAccess()}
	for name, strict := range validationModes {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			for _, id := range bodyWorkerIDs {
				body, err := json.Marshal(removal(id))
				require.NoError(t, err)
				remote := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
					t.Errorf("the removal of %q reached the remote node", id)
				})
				status := serveRemoteNodeRequestTo(t, remote, config.Server{StrictValidation: strict}, admin,
					http.MethodPost, "/api/v1/workers/removals", string(body), nil)
				assert.Equal(t, http.StatusForbidden, status, id)
			}
		})
	}
}

// A caller who is no admin gets 403 for a removal whatever worker it
// names, and the same answer as an admin for a request no worker access
// operation serves.
func TestWorkerAccessRoleMatchesServingRouter(t *testing.T) {
	t.Parallel()
	for name, strict := range validationModes {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			viewer := workerAccessServer(t, &inviteCoordinatorStub{}, auth.RoleViewer, strict)
			admin := workerAccessServer(t, &inviteCoordinatorStub{}, auth.RoleAdmin, strict)
			for _, id := range bodyWorkerIDs {
				assert.Equal(t, http.StatusForbidden, serve(t, viewer, http.MethodPost, "/api/v1/workers/removals", removal(id)), id)
			}
			for _, target := range []string{"/api/v1/workers/worker-1", "/api/v1/workers/invites"} {
				assert.Equal(t, serve(t, admin, http.MethodDelete, target, nil), serve(t, viewer, http.MethodDelete, target, nil), target)
			}
		})
	}
}
