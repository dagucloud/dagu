// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cloudaccess

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"slices"

	"github.com/dagucloud/dagu/v2/internal/audit"
	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/cloudrelay"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/license"
)

const (
	// mcpPath is the one path calls reach.
	mcpPath = "/mcp"
	// callBodyLimit bounds what is read of a call's body; the relay sends at
	// most 512 KiB.
	callBodyLimit = 1 << 20
	// userIDPrefix starts the ID of a user Dagu Cloud vouches for.
	userIDPrefix = "cloud:"
)

// Refusals a call gets, by their error code.
const (
	errorRemoteAccessOff  = "remote_access_off"
	errorInvalidAssertion = "invalid_assertion"
	errorUnavailable      = "unavailable"
)

// roleOrder ranks the roles calls can get, lowest first.
var roleOrder = []auth.Role{auth.RoleViewer, auth.RoleOperator, auth.RoleDeveloper}

// calls serves the calls Dagu Cloud carries over the relay: each runs as the
// user its assertion names, in the lower of the role Dagu Cloud grants and
// the one the server allows.
type calls struct {
	settings    *Settings
	credentials func() (license.CloudCredentials, bool)
	verifier    *verifier
	mcp         http.Handler
}

func (c *calls) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if r.URL.Path != mcpPath {
		http.NotFound(w, r)
		return
	}
	status, err := c.settings.Status(ctx)
	if err != nil {
		logger.Error(ctx, "Remote access: the settings could not be read", tag.Error(err))
		refuse(w, http.StatusServiceUnavailable, errorUnavailable, "The server could not read its remote access settings.")
		return
	}
	if status.Level == config.RemoteAccessOff {
		refuse(w, http.StatusForbidden, errorRemoteAccessOff,
			"Remote access is off on this server. A Dagu administrator turns it on under Plan & features.")
		return
	}
	creds, ok := c.credentials()
	if !ok {
		refuse(w, http.StatusServiceUnavailable, errorUnavailable, "The server is not connected to Dagu Cloud.")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, callBodyLimit+1))
	if err != nil || len(body) > callBodyLimit {
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		return
	}
	a, err := c.verifier.verify(r.Header.Get(cloudrelay.AssertionHeader), creds, r.Method, r.URL.RequestURI(), body)
	if err != nil {
		logger.Warn(ctx, "Remote access: refused a call", tag.Error(err))
		refuse(w, http.StatusUnauthorized, errorInvalidAssertion, "Dagu Cloud's assertion for this call is not valid.")
		return
	}

	user := &auth.User{
		ID:              userIDPrefix + a.Subject,
		Username:        a.Email,
		Role:            lowerRole(auth.Role(a.Role), auth.Role(status.Level)),
		WorkspaceAccess: auth.AllWorkspaceAccess(),
		AuthProvider:    auth.AuthProviderCloud,
	}
	ctx = auth.WithUser(ctx, user)
	ctx = audit.WithSourceContext(ctx, &audit.SourceContext{
		Transport:      "cloud_relay",
		CredentialType: "cloud",
		CredentialID:   a.Grant,
		CredentialName: a.Client,
		SubjectType:    "cloud_user",
		SubjectID:      user.ID,
		SubjectName:    a.Email,
		CloudWorkspace: a.Workspace,
	})
	logger.Debug(ctx, "Remote access: serving a call", slog.String("user", user.ID), slog.String("role", string(user.Role)))

	r = r.WithContext(ctx)
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.Header.Del(cloudrelay.AssertionHeader)
	c.mcp.ServeHTTP(w, r)
}

// lowerRole returns the lower of two roles calls can get.
func lowerRole(a, b auth.Role) auth.Role {
	return roleOrder[min(slices.Index(roleOrder, a), slices.Index(roleOrder, b))]
}

// refuse answers a call with an error Dagu Cloud shows: its code and a
// message for the user.
func refuse(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code, "message": message})
}
