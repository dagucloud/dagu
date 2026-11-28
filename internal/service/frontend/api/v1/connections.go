// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/audit"
	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/connection"
	"github.com/dagucloud/dagu/v2/internal/connection/connect"
	secretpkg "github.com/dagucloud/dagu/v2/internal/secret"
	"github.com/dagucloud/dagu/v2/internal/workspace"
)

const (
	// globalConnectionWorkspace is how the API names
	// secretpkg.GlobalWorkspace, which holds the connections of DAGs
	// without a workspace label.
	globalConnectionWorkspace = "global"
	brokenConnectionReason    = "The connection's token state is missing or cannot be decoded or decrypted. Remove the connection and add it again."
)

var (
	errConnectionsUnavailable = &Error{
		HTTPStatus: http.StatusServiceUnavailable,
		Code:       api.ErrorCodeInternalError,
		Message:    "Connections need encrypted storage, which is not available",
	}
	errConnectionNotFound = &Error{
		HTTPStatus: http.StatusNotFound,
		Code:       api.ErrorCodeNotFound,
		Message:    "Connection not found",
	}
	// errTokenStateRead ends the read of storedTokenState without a write.
	errTokenStateRead = errors.New("token state read")
)

func badConnectionRequest(message string) *Error {
	return &Error{HTTPStatus: http.StatusBadRequest, Code: api.ErrorCodeBadRequest, Message: message}
}

// ListConnections lists the connections of one workspace.
func (a *API) ListConnections(ctx context.Context, request api.ListConnectionsRequestObject) (api.ListConnectionsResponseObject, error) {
	ws, err := a.connectionWorkspace(ctx, request.Params.Workspace, auth.Role.IsSet)
	if err != nil {
		return nil, err
	}
	conns, err := a.connectionStore.List(ctx, ws)
	if err != nil {
		return nil, fmt.Errorf("failed to list connections: %w", err)
	}
	resp := api.ListConnections200JSONResponse{Connections: make([]api.Connection, 0, len(conns))}
	for _, conn := range conns {
		item, err := a.connectionResponse(ctx, conn)
		if err != nil {
			return nil, err
		}
		resp.Connections = append(resp.Connections, item)
	}
	return resp, nil
}

// CreateConnection creates a pending connection on an existing app.
func (a *API) CreateConnection(ctx context.Context, request api.CreateConnectionRequestObject) (api.CreateConnectionResponseObject, error) {
	ws, err := a.connectionWorkspace(ctx, request.Params.Workspace, auth.Role.CanManageAudit)
	if err != nil {
		return nil, err
	}
	body := request.Body
	if body == nil {
		return nil, badConnectionRequest("Request body is required")
	}
	if valueOf(body.AllowTokenExport) {
		if err := a.requireAdmin(ctx); err != nil {
			return nil, err
		}
	}
	// Workspace deletes and renames hold the write lock while they check
	// for connections, so none runs between this check and the create.
	a.workspaceWikiMu.RLock()
	defer a.workspaceWikiMu.RUnlock()
	if err := a.requireWorkspaceExists(ctx, ws); err != nil {
		return nil, err
	}
	app, err := a.connectionStore.GetApp(ctx, body.App)
	if errors.Is(err, connection.ErrAppNotFound) {
		return nil, badConnectionRequest(fmt.Sprintf("OAuth app %q not found", body.App))
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read OAuth app: %w", err)
	}
	if err := connectableApp(app); err != nil {
		return nil, err
	}
	conn := &connection.Connection{
		Workspace:        ws,
		Name:             body.Name,
		Provider:         app.Provider,
		App:              app.ID,
		Grant:            connection.GrantAuthorizationCode,
		Services:         connectionServices(body.Services),
		Scopes:           valueOf(body.Scopes),
		BoundHosts:       valueOf(body.BoundHosts),
		AllowTokenExport: valueOf(body.AllowTokenExport),
	}
	if err := validateConnectionFields(conn); err != nil {
		return nil, err
	}
	created, err := a.connectionStore.Create(ctx, conn)
	if errors.Is(err, connection.ErrAlreadyExists) {
		return nil, &Error{
			HTTPStatus: http.StatusConflict,
			Code:       api.ErrorCodeAlreadyExists,
			Message:    fmt.Sprintf("Connection %q already exists in this workspace", body.Name),
		}
	}
	if err != nil {
		return nil, connectionWriteError("create connection", err)
	}
	a.logAudit(ctx, audit.CategoryConnection, "connection_create", connectionAuditDetails(created))
	resp, err := a.connectionResponse(ctx, created)
	if err != nil {
		return nil, err
	}
	return api.CreateConnection201JSONResponse(resp), nil
}

// GetConnection returns one connection.
func (a *API) GetConnection(ctx context.Context, request api.GetConnectionRequestObject) (api.GetConnectionResponseObject, error) {
	conn, err := a.getConnection(ctx, request.Name, request.Params.Workspace, auth.Role.IsSet)
	if err != nil {
		return nil, err
	}
	resp, err := a.connectionResponse(ctx, conn)
	if err != nil {
		return nil, err
	}
	return api.GetConnection200JSONResponse(resp), nil
}

// UpdateConnection changes a connection's metadata, never its tokens.
func (a *API) UpdateConnection(ctx context.Context, request api.UpdateConnectionRequestObject) (api.UpdateConnectionResponseObject, error) {
	conn, err := a.getConnection(ctx, request.Name, request.Params.Workspace, auth.Role.CanManageAudit)
	if err != nil {
		return nil, err
	}
	body := request.Body
	if body == nil {
		return nil, badConnectionRequest("Request body is required")
	}
	// Both checks run against the record the update replaces, so a change
	// that lands first, or a connection created again under the name, is
	// not judged by a stale copy.
	updated, err := a.connectionStore.Update(ctx, conn.Key(), func(c *connection.Connection) error {
		if c.ID != conn.ID {
			return connection.ErrNotFound
		}
		if body.AllowTokenExport != nil && *body.AllowTokenExport != c.AllowTokenExport {
			if err := a.requireAdmin(ctx); err != nil {
				return err
			}
		}
		if body.Services != nil {
			c.Services = connectionServices(*body.Services)
		}
		if body.Scopes != nil {
			c.Scopes = *body.Scopes
		}
		if body.BoundHosts != nil {
			c.BoundHosts = *body.BoundHosts
		}
		if body.AllowTokenExport != nil {
			c.AllowTokenExport = *body.AllowTokenExport
		}
		return validateConnectionFields(c)
	})
	if errors.Is(err, connection.ErrNotFound) {
		return nil, errConnectionNotFound
	}
	if err != nil {
		return nil, connectionWriteError("update connection", err)
	}
	a.logAudit(ctx, audit.CategoryConnection, "connection_update", connectionAuditDetails(updated))
	resp, err := a.connectionResponse(ctx, updated)
	if err != nil {
		return nil, err
	}
	return api.UpdateConnection200JSONResponse(resp), nil
}

// DeleteConnection removes a connection and revokes its refresh token as
// the connect engine decides.
func (a *API) DeleteConnection(ctx context.Context, request api.DeleteConnectionRequestObject) (api.DeleteConnectionResponseObject, error) {
	conn, err := a.getConnection(ctx, request.Name, request.Params.Workspace, auth.Role.CanManageAudit)
	if err != nil {
		return nil, err
	}
	err = a.connectEngine.Remove(ctx, conn)
	if errors.Is(err, connection.ErrNotFound) {
		return nil, errConnectionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to remove connection: %w", err)
	}
	a.logAudit(ctx, audit.CategoryConnection, "connection_delete", connectionAuditDetails(conn))
	a.attention.invalidate()
	if a.connectionRemoved != nil {
		a.connectionRemoved()
	}
	return api.DeleteConnection204Response{}, nil
}

// AuditRevocation writes the outcome of a refresh token revocation, which
// the connect engine reports, to the audit log. The entry holds no token.
func (a *API) AuditRevocation(ctx context.Context, r connect.Revocation) {
	details := map[string]any{
		"workspace": connectionWorkspaceToAPI(r.Connection.Workspace),
		"name":      r.Connection.Name,
		"id":        r.ConnectionID,
		"app":       r.App,
		"token":     string(r.Token),
		"revoked":   r.Reason == "",
	}
	if r.Reason != "" {
		details["reason"] = r.Reason
	}
	a.logAudit(ctx, audit.CategoryConnection, "connection_token_revoke", details)
}

// requireNoConnections refuses to delete or rename workspaceName while it
// owns connections. Either would orphan them, and a workspace created later
// under the old name would inherit them.
func (a *API) requireNoConnections(ctx context.Context, workspaceName string) error {
	if a.connectionStore == nil {
		return nil
	}
	conns, err := a.connectionStore.List(ctx, workspaceName)
	if err != nil {
		return fmt.Errorf("failed to list the workspace's connections: %w", err)
	}
	if len(conns) == 0 {
		return nil
	}
	names := make([]string, len(conns))
	for i, conn := range conns {
		names[i] = conn.Name
	}
	return &Error{
		HTTPStatus: http.StatusConflict,
		Code:       api.ErrorCodeConflict,
		Message:    fmt.Sprintf("Workspace %q owns connections %s. Remove them first", workspaceName, strings.Join(names, ", ")),
	}
}

// requireWorkspaceExists refuses a connection in a workspace that does not
// exist, which a workspace created later under that name would inherit.
func (a *API) requireWorkspaceExists(ctx context.Context, workspaceName string) error {
	if workspaceName == secretpkg.GlobalWorkspace || a.workspaceStore == nil {
		return nil
	}
	_, err := a.workspaceStore.GetByName(ctx, workspaceName)
	if errors.Is(err, workspace.ErrWorkspaceNotFound) {
		return &Error{
			HTTPStatus: http.StatusNotFound,
			Code:       api.ErrorCodeNotFound,
			Message:    fmt.Sprintf("Workspace %q not found", workspaceName),
		}
	}
	if err != nil {
		return fmt.Errorf("failed to read workspace: %w", err)
	}
	return nil
}

// connectionWorkspace returns the workspace that raw names, once the caller
// is found to have a role there that allowed accepts. A caller without
// access to the workspace finds no connection.
func (a *API) connectionWorkspace(ctx context.Context, raw *string, allowed func(auth.Role) bool) (string, error) {
	if a.connectionStore == nil || a.connectEngine == nil {
		return "", errConnectionsUnavailable
	}
	ws, err := parseConnectionWorkspace(raw)
	if err != nil {
		return "", err
	}
	role, ok, err := a.effectiveRoleForWorkspace(ctx, ws)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", errConnectionNotFound
	}
	if !allowed(role) {
		return "", errInsufficientPermissions
	}
	return ws, nil
}

func (a *API) getConnection(ctx context.Context, name string, rawWorkspace *string, allowed func(auth.Role) bool) (*connection.Connection, error) {
	ws, err := a.connectionWorkspace(ctx, rawWorkspace, allowed)
	if err != nil {
		return nil, err
	}
	conn, err := a.connectionStore.Get(ctx, connection.Key{Workspace: ws, Name: name})
	if errors.Is(err, connection.ErrNotFound) {
		return nil, errConnectionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read connection: %w", err)
	}
	return conn, nil
}

// connectionResponse reads conn's token state and returns what the API
// shows of conn, which holds no secret.
func (a *API) connectionResponse(ctx context.Context, conn *connection.Connection) (api.Connection, error) {
	state, err := storedTokenState(ctx, a.connectionStore, conn)
	if err != nil {
		return api.Connection{}, fmt.Errorf("connection %q: failed to read token state: %w", conn.Name, err)
	}
	return toConnectionResponse(conn, state), nil
}

// storedTokenState returns conn's own token state, or nil when it has none:
// the state is missing, cannot be decoded or decrypted, or belongs to an
// earlier connection with the same key, as after a create that stopped
// halfway. Any other read failure is returned. The store's TokenState reads
// such a state as pending, so this reads it through UpdateTokenState, whose
// function copies it and writes nothing.
func storedTokenState(ctx context.Context, store connection.Store, conn *connection.Connection) (*connection.TokenState, error) {
	var state connection.TokenState
	_, err := store.UpdateTokenState(ctx, conn, func(st *connection.TokenState) error {
		state = *st
		return errTokenStateRead
	})
	switch {
	case errors.Is(err, errTokenStateRead):
		return &state, nil
	case errors.Is(err, connection.ErrNotFound), errors.Is(err, connection.ErrCorrupt):
		return nil, nil
	default:
		return nil, err
	}
}

// toConnectionResponse returns conn as the API shows it, with its status
// from state. A nil state shows the connection as broken.
func toConnectionResponse(conn *connection.Connection, state *connection.TokenState) api.Connection {
	resp := api.Connection{
		Name:             conn.Name,
		Workspace:        connectionWorkspaceToAPI(conn.Workspace),
		Provider:         api.ConnectionProviderName(conn.Provider),
		App:              conn.App,
		Grant:            api.ConnectionGrant(conn.Grant),
		Services:         make([]api.ConnectionService, len(conn.Services)),
		Scopes:           nonNil(conn.Scopes),
		BoundHosts:       nonNil(conn.BoundHosts),
		Resource:         ptrOf(conn.Resource),
		AllowTokenExport: conn.AllowTokenExport,
		Status:           api.ConnectionStatusBroken,
		StatusReason:     ptrOf(brokenConnectionReason),
		GrantedScopes:    []string{},
		CreatedAt:        conn.CreatedAt,
		UpdatedAt:        conn.UpdatedAt,
	}
	for i, svc := range conn.Services {
		resp.Services[i] = api.ConnectionService{Name: svc.Name, Access: api.ConnectionAccess(svc.Access), MissingScopes: []string{}}
	}
	if state == nil {
		return resp
	}
	resp.Status = api.ConnectionStatus(state.Status)
	resp.StatusReason = ptrOf(state.StatusReason)
	resp.Account = ptrOf(state.Account)
	resp.GrantedScopes = nonNil(state.GrantedScopes)
	resp.RefreshTokenConfigured = state.RefreshToken != ""
	resp.RefreshTokenExpiresAt = ptrOf(state.RefreshTokenExpiresAt)
	resp.RefreshedAt = ptrOf(state.RefreshedAt)
	// A connection that never connected has no grant to lack scopes in.
	if state.Status != connection.TokenStatusPending {
		for i, svc := range conn.Services {
			resp.Services[i].MissingScopes = nonNil(conn.MissingScopes(svc, state.GrantedScopes))
		}
	}
	return resp
}

// parseConnectionWorkspace returns the workspace that raw names. Runs find
// a connection by their DAG's workspace label, which is lowercased, so a
// workspace name with an uppercase letter is refused: no run could use its
// connections.
func parseConnectionWorkspace(raw *string) (string, error) {
	name := strings.TrimSpace(valueOf(raw))
	if name == "" || name == globalConnectionWorkspace {
		return secretpkg.GlobalWorkspace, nil
	}
	if err := workspace.ValidateName(name); err != nil {
		return "", badConnectionRequest(err.Error())
	}
	if name != strings.ToLower(name) {
		return "", badConnectionRequest(fmt.Sprintf("workspace %q has uppercase letters: DAG workspace labels are lowercased, so no run could use its connections", name))
	}
	return name, nil
}

func connectionWorkspaceToAPI(name string) string {
	if name == secretpkg.GlobalWorkspace {
		return globalConnectionWorkspace
	}
	return name
}

// connectableApp refuses an app that a new authorization code connection
// cannot use.
func connectableApp(app *connection.App) error {
	switch {
	case !providerAccepted(app.Provider):
		return unacceptedProvider(app.Provider)
	case app.Kind == connection.AppKindDCR:
		return badConnectionRequest(fmt.Sprintf("OAuth app %q belongs to the connection that registered it", app.Name))
	case app.ClientType != connection.ClientTypeWeb && app.ClientType != connection.ClientTypeDesktop:
		return badConnectionRequest(fmt.Sprintf("OAuth app %q has a %s client, which the authorization code grant cannot use", app.Name, app.ClientType))
	}
	return nil
}

// providerAccepted reports whether the API takes apps and connections of
// provider. An oauth2 or mcp app needs endpoints, and an mcp connection a
// resource, which no request sets yet.
func providerAccepted(provider connection.Provider) bool {
	return provider == connection.ProviderGoogle || provider == connection.ProviderMicrosoft
}

func unacceptedProvider(provider connection.Provider) *Error {
	return badConnectionRequest(fmt.Sprintf("%s apps and connections need endpoints or a resource URL, which the API does not take yet", provider))
}

// validateConnectionFields checks the fields a request sets beyond what the
// store validates: each service is a preset of the provider, and each bound
// host is a hostname or host:port.
func validateConnectionFields(conn *connection.Connection) error {
	for _, svc := range conn.Services {
		if _, ok := connection.LookupPreset(conn.Provider, svc.Name); !ok {
			return badConnectionRequest(fmt.Sprintf("service %q is not a %s service", svc.Name, conn.Provider))
		}
	}
	for _, host := range conn.BoundHosts {
		if !validBoundHost(host) {
			return badConnectionRequest(fmt.Sprintf("bound host %q must be a hostname or host:port, without a scheme, path, or wildcard", host))
		}
	}
	return nil
}

// validBoundHost reports whether entry is a hostname or host:port, with no
// scheme, user, path, or wildcard.
func validBoundHost(entry string) bool {
	u, err := url.Parse("https://" + entry)
	return err == nil && u.Host == entry && u.Hostname() != "" &&
		!strings.HasSuffix(entry, ":") && !strings.Contains(entry, "*")
}

func connectionServices(services []api.ConnectionServiceRequest) []connection.Service {
	out := make([]connection.Service, len(services))
	for i, svc := range services {
		out[i] = connection.Service{Name: svc.Name, Access: connection.Access(svc.Access)}
	}
	return out
}

// connectionWriteError maps a store error of a write to a bad request when
// the record is invalid or reserved, and passes on an API error that the
// update function returned.
func connectionWriteError(action string, err error) error {
	if apiErr, ok := errors.AsType[*Error](err); ok {
		return apiErr
	}
	if errors.Is(err, connection.ErrInvalid) || errors.Is(err, connection.ErrReserved) {
		return badConnectionRequest(err.Error())
	}
	return fmt.Errorf("failed to %s: %w", action, err)
}

func connectionAuditDetails(conn *connection.Connection) map[string]any {
	services := make([]string, len(conn.Services))
	for i, svc := range conn.Services {
		services[i] = svc.Name + ":" + string(svc.Access)
	}
	return map[string]any{
		"workspace":          connectionWorkspaceToAPI(conn.Workspace),
		"name":               conn.Name,
		"id":                 conn.ID,
		"provider":           string(conn.Provider),
		"app":                conn.App,
		"services":           services,
		"scopes":             nonNil(conn.Scopes),
		"bound_hosts":        nonNil(conn.BoundHosts),
		"allow_token_export": conn.AllowTokenExport,
	}
}
