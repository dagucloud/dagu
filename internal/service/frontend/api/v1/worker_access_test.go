// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api_test

import (
	"net/http"
	"testing"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/test"
	"github.com/stretchr/testify/assert"
)

// An admin, by login or by API key, passes the role check and gets 503,
// since no coordinator runs. Every other role gets 403.
func TestWorkerAccessEndpointRoles(t *testing.T) {
	t.Parallel()
	server := setupBuiltinAuthServer(t)
	adminToken := getAdminToken(t, server)

	tokens := map[string]int{
		adminToken: http.StatusServiceUnavailable,
		createAPIKeyForRole(t, server, adminToken, "admin-key", api.UserRoleAdmin):         http.StatusServiceUnavailable,
		createAPIKeyForRole(t, server, adminToken, "manager-key", api.UserRoleManager):     http.StatusForbidden,
		createAPIKeyForRole(t, server, adminToken, "developer-key", api.UserRoleDeveloper): http.StatusForbidden,
		createAPIKeyForRole(t, server, adminToken, "viewer-key", api.UserRoleViewer):       http.StatusForbidden,
	}
	requests := []func(token string, status int) *test.Request{
		func(token string, status int) *test.Request {
			return server.Client().Post("/api/v1/workers/invites", api.CreateWorkerInviteRequest{}).WithBearerToken(token).ExpectStatus(status)
		},
		func(token string, status int) *test.Request {
			return server.Client().Get("/api/v1/workers/invites").WithBearerToken(token).ExpectStatus(status)
		},
		func(token string, status int) *test.Request {
			return server.Client().Delete("/api/v1/workers/invites/invite-1").WithBearerToken(token).ExpectStatus(status)
		},
		func(token string, status int) *test.Request {
			return server.Client().Get("/api/v1/workers/join-readiness").WithBearerToken(token).ExpectStatus(status)
		},
	}
	for token, status := range tokens {
		for _, request := range requests {
			resp := request(token, status).Send(t)
			if status == http.StatusServiceUnavailable {
				var body api.Error
				resp.Unmarshal(t, &body)
				assert.Equal(t, "no_coordinator", (*body.Details)["reason"])
			}
		}
	}
}
