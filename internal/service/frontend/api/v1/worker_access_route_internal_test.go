// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	openapiv1 "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/auth"
)

// workerAccessRouter serves a under /api/v1 as the server mounts it, for a
// caller of role, with the worker access authorization in front.
func workerAccessRouter(t *testing.T, a *API, role auth.Role) http.Handler {
	t.Helper()
	swagger, err := loadBaseOpenAPISpec()
	require.NoError(t, err)
	authorization, err := a.workerAccessAuthorization(swagger)
	require.NoError(t, err)
	root := chi.NewRouter()
	root.Route("/api/v1", func(r chi.Router) {
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				next.ServeHTTP(w, r.WithContext(auth.WithUser(r.Context(), &auth.User{Username: "user", Role: role})))
			})
		})
		r.Use(authorization)
		r.Mount("/", openapiv1.Handler(openapiv1.NewStrictHandlerWithOptions(a, nil, openapiv1.StrictHTTPServerOptions{
			RequestErrorHandlerFunc:  func(w http.ResponseWriter, r *http.Request, _ error) { a.handleError(w, r, ErrInvalidRequestBody) },
			ResponseErrorHandlerFunc: a.handleError,
		})))
	})
	return root
}

func serve(t *testing.T, handler http.Handler, method, target string) int {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(method, target, nil))
	return recorder.Code
}

// Worker IDs equal to a fixed segment under /workers/ reach the removal,
// and the fixed routes keep their own handlers.
func TestWorkerRemovalRouteTakesLiteralWorkerIDs(t *testing.T) {
	t.Parallel()

	a, _ := rolesAPI(&inviteCoordinatorStub{})
	router := workerAccessRouter(t, a, auth.RoleAdmin)
	for _, id := range []string{"invites", "join-readiness"} {
		assert.Equal(t, http.StatusNotImplemented, serve(t, router, http.MethodDelete, "/api/v1/workers/"+id+"?certificateId=cert-1"), id)
	}
	assert.Equal(t, http.StatusOK, serve(t, router, http.MethodGet, "/api/v1/workers/invites"))
	assert.Equal(t, http.StatusNoContent, serve(t, router, http.MethodDelete, "/api/v1/workers/invites/"+testInviteID))
}

// The role check matches exactly the requests the serving router sends to
// a worker access operation: an empty worker ID gets the same answer for a
// viewer as for an admin's request that no handler serves.
func TestWorkerAccessRoleMatchesServingRouter(t *testing.T) {
	t.Parallel()

	a, _ := rolesAPI(&inviteCoordinatorStub{})
	viewer := workerAccessRouter(t, a, auth.RoleViewer)
	admin := workerAccessRouter(t, a, auth.RoleAdmin)
	for _, target := range []string{"/api/v1/workers/?certificateId=cert-1", "/api/v1/workers/a%2Fb?certificateId=cert-1"} {
		adminStatus := serve(t, admin, http.MethodDelete, target)
		viewerStatus := serve(t, viewer, http.MethodDelete, target)
		if adminStatus == http.StatusNotImplemented {
			assert.Equal(t, http.StatusForbidden, viewerStatus, target)
		} else {
			assert.Equal(t, adminStatus, viewerStatus, target)
		}
	}
}
