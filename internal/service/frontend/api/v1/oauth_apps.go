// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/audit"
	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/connection"
)

var errOAuthAppNotFound = &Error{
	HTTPStatus: http.StatusNotFound,
	Code:       api.ErrorCodeNotFound,
	Message:    "OAuth app not found",
}

// ListOAuthApps lists every app, without secrets.
func (a *API) ListOAuthApps(ctx context.Context, _ api.ListOAuthAppsRequestObject) (api.ListOAuthAppsResponseObject, error) {
	if err := a.requireOAuthAppRead(ctx); err != nil {
		return nil, err
	}
	apps, err := a.connectionStore.ListApps(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list OAuth apps: %w", err)
	}
	resp := api.ListOAuthApps200JSONResponse{Apps: make([]api.OAuthApp, len(apps))}
	for i, app := range apps {
		resp.Apps[i] = toOAuthAppResponse(app)
	}
	return resp, nil
}

// CreateOAuthApp registers a client from its fields or from the client file
// its provider issued.
func (a *API) CreateOAuthApp(ctx context.Context, request api.CreateOAuthAppRequestObject) (api.CreateOAuthAppResponseObject, error) {
	if err := a.requireOAuthAppWrite(ctx); err != nil {
		return nil, err
	}
	body := request.Body
	if body == nil {
		return nil, badConnectionRequest("Request body is required")
	}
	provider := connection.Provider(body.Provider)
	app := &connection.App{
		Provider:     provider,
		Kind:         connection.AppKindBYO,
		ClientID:     valueOf(body.ClientId),
		ClientSecret: valueOf(body.ClientSecret),
		ClientType:   connection.ClientType(valueOf(body.ClientType)),
	}
	if file := valueOf(body.ClientFile); file != "" {
		if body.ClientId != nil || body.ClientSecret != nil || body.ClientType != nil {
			return nil, badConnectionRequest("A client file replaces clientId, clientSecret, and clientType")
		}
		parsed, err := a.connectEngine.ParseClientFile(provider, []byte(file))
		if err != nil {
			return nil, badConnectionRequest(err.Error())
		}
		app = parsed
	}
	app.Name = body.Name
	app.Agent = valueOf(body.Agent)
	app.Tenant = valueOf(body.Tenant)
	created, err := a.connectionStore.CreateApp(ctx, app)
	if err != nil {
		return nil, connectionWriteError("create OAuth app", err)
	}
	a.logAudit(ctx, audit.CategoryConnection, "oauth_app_create", oauthAppAuditDetails(created))
	return api.CreateOAuthApp201JSONResponse(toOAuthAppResponse(created)), nil
}

// GetOAuthApp returns one app, without its secret.
func (a *API) GetOAuthApp(ctx context.Context, request api.GetOAuthAppRequestObject) (api.GetOAuthAppResponseObject, error) {
	if err := a.requireOAuthAppRead(ctx); err != nil {
		return nil, err
	}
	app, err := a.connectionStore.GetApp(ctx, request.AppId)
	if errors.Is(err, connection.ErrAppNotFound) {
		return nil, errOAuthAppNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read OAuth app: %w", err)
	}
	return api.GetOAuthApp200JSONResponse(toOAuthAppResponse(app)), nil
}

// UpdateOAuthApp changes the fields a request sets. A blank secret keeps
// the stored one.
func (a *API) UpdateOAuthApp(ctx context.Context, request api.UpdateOAuthAppRequestObject) (api.UpdateOAuthAppResponseObject, error) {
	if err := a.requireOAuthAppWrite(ctx); err != nil {
		return nil, err
	}
	body := request.Body
	if body == nil {
		return nil, badConnectionRequest("Request body is required")
	}
	updated, err := a.connectionStore.UpdateApp(ctx, request.AppId, func(app *connection.App) error {
		if body.Name != nil {
			app.Name = *body.Name
		}
		if body.ClientId != nil {
			app.ClientID = *body.ClientId
		}
		if secret := valueOf(body.ClientSecret); strings.TrimSpace(secret) != "" {
			app.ClientSecret = secret
		}
		if body.ClientType != nil {
			app.ClientType = connection.ClientType(*body.ClientType)
		}
		if body.Agent != nil {
			app.Agent = *body.Agent
		}
		if body.Tenant != nil {
			app.Tenant = *body.Tenant
		}
		return nil
	})
	if errors.Is(err, connection.ErrAppNotFound) {
		return nil, errOAuthAppNotFound
	}
	if err != nil {
		return nil, connectionWriteError("update OAuth app", err)
	}
	a.logAudit(ctx, audit.CategoryConnection, "oauth_app_update", oauthAppAuditDetails(updated))
	return api.UpdateOAuthApp200JSONResponse(toOAuthAppResponse(updated)), nil
}

// DeleteOAuthApp deletes an app that no connection uses.
func (a *API) DeleteOAuthApp(ctx context.Context, request api.DeleteOAuthAppRequestObject) (api.DeleteOAuthAppResponseObject, error) {
	if err := a.requireOAuthAppWrite(ctx); err != nil {
		return nil, err
	}
	app, err := a.connectionStore.GetApp(ctx, request.AppId)
	if errors.Is(err, connection.ErrAppNotFound) {
		return nil, errOAuthAppNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read OAuth app: %w", err)
	}
	// Every connection is read, so one that cannot be read refuses the
	// delete rather than being left on a missing app.
	conns, err := a.connectionStore.ListAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list the connections of OAuth app %q: %w", app.Name, err)
	}
	var users []string
	for _, conn := range conns {
		if conn.App == app.ID {
			users = append(users, connectionLabel(conn))
		}
	}
	if len(users) > 0 {
		return nil, &Error{
			HTTPStatus: http.StatusConflict,
			Code:       api.ErrorCodeConflict,
			Message:    fmt.Sprintf("OAuth app %q is used by connections %s. Remove them first", app.Name, strings.Join(users, ", ")),
		}
	}
	err = a.connectionStore.DeleteApp(ctx, app.ID)
	if errors.Is(err, connection.ErrAppNotFound) {
		return nil, errOAuthAppNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to delete OAuth app: %w", err)
	}
	a.logAudit(ctx, audit.CategoryConnection, "oauth_app_delete", oauthAppAuditDetails(app))
	return api.DeleteOAuthApp204Response{}, nil
}

// requireOAuthAppRead allows those who may change the notification
// settings, developer or above, and those who manage connections in some
// workspace.
func (a *API) requireOAuthAppRead(ctx context.Context) error {
	if a.connectionStore == nil || a.connectEngine == nil {
		return errConnectionsUnavailable
	}
	if err := a.requireDeveloperOrAbove(ctx); !errors.Is(err, errInsufficientPermissions) {
		return err
	}
	user, _ := auth.UserFromContext(ctx)
	for _, grant := range auth.NormalizeWorkspaceAccess(user.WorkspaceAccess).Grants {
		if grant.Role.CanManageAudit() {
			return nil
		}
	}
	return errInsufficientPermissions
}

func (a *API) requireOAuthAppWrite(ctx context.Context) error {
	if a.connectionStore == nil || a.connectEngine == nil {
		return errConnectionsUnavailable
	}
	return a.requireAdmin(ctx)
}

func toOAuthAppResponse(app *connection.App) api.OAuthApp {
	return api.OAuthApp{
		Id:                     app.ID,
		Name:                   app.Name,
		Provider:               api.ConnectionProviderName(app.Provider),
		Kind:                   api.OAuthAppKind(app.Kind),
		ClientId:               app.ClientID,
		ClientType:             api.OAuthClientType(app.ClientType),
		Agent:                  app.Agent,
		Tenant:                 ptrOf(app.Tenant),
		Status:                 api.OAuthAppStatus(app.Status),
		StatusReason:           ptrOf(app.StatusReason),
		ClientSecretConfigured: app.ClientSecret != "",
		CreatedAt:              app.CreatedAt,
		UpdatedAt:              app.UpdatedAt,
	}
}

// connectionLabel names conn with its workspace, since names repeat across
// workspaces.
func connectionLabel(conn *connection.Connection) string {
	if conn.Workspace == "" {
		return conn.Name + " (notifications)"
	}
	return connectionWorkspaceToAPI(conn.Workspace) + "/" + conn.Name
}

func oauthAppAuditDetails(app *connection.App) map[string]any {
	return map[string]any{
		"id":          app.ID,
		"name":        app.Name,
		"provider":    string(app.Provider),
		"client_id":   app.ClientID,
		"client_type": string(app.ClientType),
		"agent":       app.Agent,
	}
}
