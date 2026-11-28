// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/audit"
	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/connection"
	"github.com/dagucloud/dagu/v2/internal/connection/broker"
	"github.com/dagucloud/dagu/v2/internal/connection/connect"
)

const localhostHost = "localhost"

// forwardingHeaders, and any header that starts with forwardingPrefix, mark
// a request that a proxy passed on. Keys are in canonical form.
var forwardingHeaders = []string{http.CanonicalHeaderKey("Forwarded"), http.CanonicalHeaderKey("X-Real-IP")}

const forwardingPrefix = "X-Forwarded-"

// errConnectionAppMissing reports a connection whose app was deleted while
// a connection was being created on it.
var errConnectionAppMissing = &Error{
	HTTPStatus: http.StatusConflict,
	Code:       api.ErrorCodeConflict,
	Message:    "The connection's OAuth app no longer exists. Remove the connection and add it again",
}

// localhostOriginKey keys the origin of a request that reached the server
// at localhost.
type localhostOriginKey struct{}

// StartConnectionAuthorization starts a connect, or a reconnect of a
// connection that has connected before.
func (a *API) StartConnectionAuthorization(ctx context.Context, request api.StartConnectionAuthorizationRequestObject) (api.StartConnectionAuthorizationResponseObject, error) {
	conn, err := a.getConnection(ctx, request.Name, request.Params.Workspace, auth.Role.CanManageAudit)
	if err != nil {
		return nil, err
	}
	body := valueOf(request.Body)
	// An empty mode would choose like an omitted one, also when request
	// validation is off.
	if body.Mode != nil && *body.Mode == "" {
		return nil, badConnectionRequest("mode must be omitted or name a redirect mode")
	}
	state, err := storedTokenState(ctx, a.connectionStore, conn)
	if err != nil {
		return nil, fmt.Errorf("connection %q: failed to read token state: %w", conn.Name, err)
	}
	if state == nil {
		return nil, &Error{HTTPStatus: http.StatusConflict, Code: api.ErrorCodeConflict, Message: brokenConnectionReason}
	}
	authz, err := a.connectEngine.Start(ctx, conn, connect.StartOptions{
		User: connectUser(ctx),
		Redirect: connect.RedirectOptions{
			Mode:        connect.RedirectMode(valueOf(body.Mode)),
			PublicURL:   a.usablePublicURL(ctx),
			LoopbackURI: valueOf(body.LoopbackRedirectUri),
		},
		ExpectedAccount: strings.TrimSpace(valueOf(body.ExpectedAccount)),
	})
	if err != nil {
		return nil, connectError(err)
	}
	action := "connection_connect"
	if state.Status != connection.TokenStatusPending {
		action = "connection_reconnect"
	}
	details := connectionAuditKey(conn)
	details["mode"] = string(authz.Redirect.Mode)
	details["redirect_uri"] = authz.Redirect.URI
	a.logAudit(ctx, audit.CategoryConnection, action, details)
	return api.StartConnectionAuthorization200JSONResponse{
		Session:          authz.SessionID,
		AuthorizationUrl: authz.URL,
		Mode:             api.ConnectionRedirectMode(authz.Redirect.Mode),
		RedirectUri:      authz.Redirect.URI,
		ClientId:         authz.ClientID,
		ExpiresAt:        authz.ExpiresAt,
	}, nil
}

// GetConnectionAuthorization returns the status of the session that the
// handle names.
func (a *API) GetConnectionAuthorization(ctx context.Context, request api.GetConnectionAuthorizationRequestObject) (api.GetConnectionAuthorizationResponseObject, error) {
	conn, err := a.getConnection(ctx, request.Name, request.Params.Workspace, auth.Role.IsSet)
	if err != nil {
		return nil, err
	}
	status, err := a.connectEngine.Session(ctx, conn, request.Params.Session)
	if err != nil {
		return nil, connectError(err)
	}
	return api.GetConnectionAuthorization200JSONResponse(toConnectionAuthorization(status)), nil
}

// CompleteConnectionAuthorization completes a loopback or paste session
// from the provider's redirect.
func (a *API) CompleteConnectionAuthorization(ctx context.Context, request api.CompleteConnectionAuthorizationRequestObject) (api.CompleteConnectionAuthorizationResponseObject, error) {
	conn, err := a.getConnection(ctx, request.Name, request.Params.Workspace, auth.Role.CanManageAudit)
	if err != nil {
		return nil, err
	}
	params, err := callbackParams(request.Body)
	if err != nil {
		return nil, err
	}
	confirmed, err := a.connectEngine.Complete(ctx, conn, params)
	if err != nil {
		return nil, connectError(err)
	}
	if confirmed {
		// The tokens are live, so a failed read of the account only leaves
		// it out of the audit entry.
		_, _ = a.auditConfirm(ctx, conn)
	}
	return api.CompleteConnectionAuthorization204Response{}, nil
}

// ConfirmConnectionAuthorization makes the tokens of the starter's session
// live.
func (a *API) ConfirmConnectionAuthorization(ctx context.Context, request api.ConfirmConnectionAuthorizationRequestObject) (api.ConfirmConnectionAuthorizationResponseObject, error) {
	conn, err := a.getConnection(ctx, request.Name, request.Params.Workspace, auth.Role.CanManageAudit)
	if err != nil {
		return nil, err
	}
	if request.Body == nil {
		return nil, badConnectionRequest("Request body is required")
	}
	if err := a.connectEngine.Confirm(ctx, conn, request.Body.Session, connectUser(ctx)); err != nil {
		return nil, connectError(err)
	}
	resp, err := a.auditConfirm(ctx, conn)
	if err != nil {
		return nil, err
	}
	return api.ConfirmConnectionAuthorization200JSONResponse(resp), nil
}

// auditConfirm records that the tokens of conn's session went live, with
// the account they signed in, and returns conn as the API shows it. The
// entry is written even when conn cannot be read back.
func (a *API) auditConfirm(ctx context.Context, conn *connection.Connection) (api.Connection, error) {
	resp, err := a.connectionResponse(ctx, conn)
	details := connectionAuditKey(conn)
	details["account"] = valueOf(resp.Account)
	a.logAudit(ctx, audit.CategoryConnection, "connection_connect_confirm", details)
	return resp, err
}

// CancelConnectionAuthorization fails a session that has not ended.
func (a *API) CancelConnectionAuthorization(ctx context.Context, request api.CancelConnectionAuthorizationRequestObject) (api.CancelConnectionAuthorizationResponseObject, error) {
	conn, err := a.getConnection(ctx, request.Name, request.Params.Workspace, auth.Role.CanManageAudit)
	if err != nil {
		return nil, err
	}
	if err := a.connectEngine.Cancel(ctx, conn, request.Params.Session); err != nil {
		return nil, connectError(err)
	}
	a.logAudit(ctx, audit.CategoryConnection, "connection_connect_cancel", connectionAuditKey(conn))
	return api.CancelConnectionAuthorization204Response{}, nil
}

// usablePublicURL returns the server's usable public URL: the configured
// or tunnel URL, else the origin of a request that reached the server
// directly at localhost, or empty.
func (a *API) usablePublicURL(ctx context.Context) string {
	if a.publicURL != nil {
		if u := a.publicURL(); u != "" {
			return u
		}
	}
	origin, _ := ctx.Value(localhostOriginKey{}).(string)
	return origin
}

// localhostOrigin records the origin, with basePath, of a request that
// reached the server at localhost or a loopback address without a proxy. A
// browser on the host that sent it reaches the server's callback there.
func localhostOrigin(basePath string) api.StrictMiddlewareFunc {
	basePath = strings.Trim(strings.TrimSpace(basePath), "/")
	return func(next api.StrictHandlerFunc, _ string) api.StrictHandlerFunc {
		return func(ctx context.Context, w http.ResponseWriter, r *http.Request, request any) (any, error) {
			if origin := localOrigin(r); origin != "" {
				if basePath != "" {
					origin += "/" + basePath
				}
				ctx = context.WithValue(ctx, localhostOriginKey{}, origin)
			}
			return next(ctx, w, r, request)
		}
	}
}

// localOrigin returns the origin at which r reached the server when its
// host is localhost or a loopback address, or "". The peer is not checked,
// since a published container port delivers the browser's request from a
// bridge gateway. A proxy may rewrite Host to a loopback address, so a
// request with a forwarding header, or from a browser page of another
// origin, does not count.
func localOrigin(r *http.Request) string {
	if !isLocalhost((&url.URL{Host: r.Host}).Hostname()) {
		return ""
	}
	for key := range r.Header {
		if strings.HasPrefix(key, forwardingPrefix) || slices.Contains(forwardingHeaders, key) {
			return ""
		}
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	origin := scheme + "://" + r.Host
	for _, header := range []string{"Origin", "Referer"} {
		if v := r.Header.Get(header); v != "" {
			u, err := url.Parse(v)
			if err != nil || u.Scheme+"://"+u.Host != origin {
				return ""
			}
		}
	}
	return origin
}

// isLocalhost reports whether host is localhost or a loopback address,
// which Google accepts in a web client's redirect URI over HTTP.
func isLocalhost(host string) bool {
	if strings.EqualFold(host, localhostHost) {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// callbackParams returns the provider's redirect that body carries, as the
// URL the browser landed on or as its parameters.
func callbackParams(body *api.CompleteConnectionAuthorizationRequest) (connect.CallbackParams, error) {
	if body == nil {
		return connect.CallbackParams{}, badConnectionRequest("Request body is required")
	}
	params := connect.CallbackParams{
		State:            valueOf(body.State),
		Code:             valueOf(body.Code),
		Error:            valueOf(body.Error),
		ErrorDescription: valueOf(body.ErrorDescription),
	}
	if raw := strings.TrimSpace(valueOf(body.RedirectUrl)); raw != "" {
		if params != (connect.CallbackParams{}) {
			return connect.CallbackParams{}, badConnectionRequest("redirectUrl replaces state, code, error, and errorDescription")
		}
		// The URL carries a code, so the error does not quote it.
		u, err := url.Parse(raw)
		if err != nil {
			return connect.CallbackParams{}, badConnectionRequest("redirectUrl is not a URL")
		}
		q := u.Query()
		params = connect.CallbackParams{
			State:            q.Get("state"),
			Code:             q.Get("code"),
			Error:            q.Get("error"),
			ErrorDescription: q.Get("error_description"),
		}
	}
	if params.State == "" || (params.Code == "" && params.Error == "") {
		return connect.CallbackParams{}, badConnectionRequest("The redirect needs a state, and a code or an error")
	}
	return params, nil
}

func toConnectionAuthorization(s connect.SessionStatus) api.ConnectionAuthorization {
	resp := api.ConnectionAuthorization{
		Phase:                 api.ConnectionAuthorizationPhase(s.Phase),
		Account:               ptrOf(s.Account),
		AccountId:             ptrOf(s.AccountID),
		RefreshTokenExpiresAt: ptrOf(s.RefreshTokenExpiresAt),
		RefreshTokenFix:       ptrOf(s.RefreshTokenFix),
		Reason:                ptrOf(s.Reason),
		ExpiresAt:             ptrOf(s.ExpiresAt),
	}
	if s.Phase == connect.PhaseConfirm {
		resp.GrantedScopes = ptrOf(nonNil(s.GrantedScopes))
		resp.Probes = ptrOf(toConnectionProbes(s.Probes))
		resp.Duplicates = ptrOf(nonNil(s.Duplicates))
	}
	return resp
}

func toConnectionProbes(probes []connection.Probe) []api.ConnectionProbe {
	out := make([]api.ConnectionProbe, len(probes))
	for i, p := range probes {
		out[i] = api.ConnectionProbe{Service: p.Service, Ok: p.Error == "", Error: ptrOf(p.Error)}
	}
	return out
}

// connectError maps an error of the connect engine to the API's.
func connectError(err error) error {
	var status int
	code := api.ErrorCodeConflict
	switch {
	case errors.Is(err, connection.ErrAppNotFound):
		return errConnectionAppMissing
	case errors.Is(err, connection.ErrNotFound):
		return errConnectionNotFound
	case errors.Is(err, connect.ErrSessionNotFound):
		status, code = http.StatusNotFound, api.ErrorCodeNotFound
	case errors.Is(err, connect.ErrNotStarter):
		status, code = http.StatusForbidden, api.ErrorCodeForbidden
	case errors.Is(err, connect.ErrRedirectUnavailable), errors.Is(err, connect.ErrUnsupported),
		errors.Is(err, connect.ErrConsentFailed):
		status, code = http.StatusBadRequest, api.ErrorCodeBadRequest
	case errors.Is(err, connect.ErrSessionEnded), errors.Is(err, connect.ErrAwaitingConsent),
		errors.Is(err, connect.ErrCallbackRefused), errors.Is(err, broker.ErrReconnectRequired):
		status = http.StatusConflict
	case errors.Is(err, broker.ErrRefreshFailed):
		status, code = http.StatusBadGateway, api.ErrorCodeBadGateway
	default:
		return err
	}
	return &Error{HTTPStatus: status, Code: code, Message: err.Error()}
}

// connectUser names the caller as the starter of a session.
func connectUser(ctx context.Context) string {
	user, ok := auth.UserFromContext(ctx)
	if !ok || user == nil {
		return ""
	}
	if user.ID != "" {
		return user.ID
	}
	return user.Username
}

// connectionAuditKey returns the audit details that name conn.
func connectionAuditKey(conn *connection.Connection) map[string]any {
	return map[string]any{
		"workspace": connectionWorkspaceToAPI(conn.Workspace),
		"name":      conn.Name,
		"id":        conn.ID,
	}
}
