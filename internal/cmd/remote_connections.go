// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	api "github.com/dagucloud/dagu/v2/api/v1"
)

// The connections REST API (Spec 107 §REST API). Every path that names a
// connection takes the workspace that owns it as a query parameter.

func connectionPath(name string, suffix string) string {
	return "/connections/" + url.PathEscape(name) + suffix
}

func workspaceQuery(workspace string) map[string]string {
	return map[string]string{"workspace": workspace}
}

func (c *remoteClient) connectionProviders(ctx context.Context) (*api.ConnectionProviderListResponse, error) {
	var out api.ConnectionProviderListResponse
	if err := c.do(ctx, http.MethodGet, "/connection-providers", nil, &out, nil); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *remoteClient) oauthApps(ctx context.Context) ([]api.OAuthApp, error) {
	var out api.OAuthAppListResponse
	if err := c.do(ctx, http.MethodGet, "/oauth-apps", nil, &out, nil); err != nil {
		return nil, err
	}
	return out.Apps, nil
}

func (c *remoteClient) oauthApp(ctx context.Context, id string) (*api.OAuthApp, error) {
	var out api.OAuthApp
	if err := c.do(ctx, http.MethodGet, "/oauth-apps/"+url.PathEscape(id), nil, &out, nil); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *remoteClient) createOAuthApp(ctx context.Context, body api.CreateOAuthAppRequest) (*api.OAuthApp, error) {
	var out api.OAuthApp
	if err := c.do(ctx, http.MethodPost, "/oauth-apps", body, &out, nil); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *remoteClient) connections(ctx context.Context, workspace string) ([]api.Connection, error) {
	var out api.ConnectionListResponse
	if err := c.do(ctx, http.MethodGet, "/connections", nil, &out, workspaceQuery(workspace)); err != nil {
		return nil, err
	}
	return out.Connections, nil
}

func (c *remoteClient) deleteOAuthApp(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/oauth-apps/"+url.PathEscape(id), nil, nil, nil)
}

func (c *remoteClient) connection(ctx context.Context, workspace, name string) (*api.Connection, error) {
	var out api.Connection
	if err := c.do(ctx, http.MethodGet, connectionPath(name, ""), nil, &out, workspaceQuery(workspace)); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *remoteClient) createConnection(ctx context.Context, workspace string, body api.CreateConnectionRequest) (*api.Connection, error) {
	var out api.Connection
	if err := c.do(ctx, http.MethodPost, "/connections", body, &out, workspaceQuery(workspace)); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *remoteClient) deleteConnection(ctx context.Context, workspace, name string) error {
	return c.do(ctx, http.MethodDelete, connectionPath(name, ""), nil, nil, workspaceQuery(workspace))
}

func (c *remoteClient) startAuthorization(ctx context.Context, workspace, name string, body api.StartConnectionAuthorizationRequest) (*api.ConnectionAuthorizationStart, error) {
	var out api.ConnectionAuthorizationStart
	if err := c.do(ctx, http.MethodPost, connectionPath(name, "/authorization"), body, &out, workspaceQuery(workspace)); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *remoteClient) authorization(ctx context.Context, workspace, name, session string) (*api.ConnectionAuthorization, error) {
	var out api.ConnectionAuthorization
	query := map[string]string{"workspace": workspace, "session": session}
	if err := c.do(ctx, http.MethodGet, connectionPath(name, "/authorization"), nil, &out, query); err != nil {
		return nil, err
	}
	return &out, nil
}

const (
	// completeTimeout bounds a complete request, which the server may spend
	// up to 45 seconds on with the provider, within its 60-second write
	// deadline.
	completeTimeout = 60 * time.Second
	// toolsTimeout bounds a tools request, which waits while the server
	// lists a stale catalog again. The server's 60-second write deadline
	// ends the answer sooner than the listing's own two-minute bound, so a
	// longer wait would gain nothing. The listing goes on after the
	// request ends.
	toolsTimeout = 60 * time.Second
)

// atLeast returns c with a client timeout of at least d, for a request
// that the server may take longer to answer than c allows.
func (c *remoteClient) atLeast(d time.Duration) *remoteClient {
	slow := *c
	if hc := *c.client; hc.Timeout != 0 && hc.Timeout < d {
		hc.Timeout = d
		slow.client = &hc
	}
	return &slow
}

func (c *remoteClient) completeAuthorization(ctx context.Context, workspace, name, redirectURL string) error {
	body := api.CompleteConnectionAuthorizationRequest{RedirectUrl: &redirectURL}
	return c.atLeast(completeTimeout).do(ctx, http.MethodPost, connectionPath(name, "/authorization/complete"), body, nil, workspaceQuery(workspace))
}

func (c *remoteClient) confirmAuthorization(ctx context.Context, workspace, name, session string) (*api.Connection, error) {
	var out api.Connection
	body := api.ConfirmConnectionAuthorizationRequest{Session: session}
	if err := c.do(ctx, http.MethodPost, connectionPath(name, "/authorization/confirm"), body, &out, workspaceQuery(workspace)); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *remoteClient) cancelAuthorization(ctx context.Context, workspace, name, session string) error {
	query := map[string]string{"workspace": workspace, "session": session}
	return c.do(ctx, http.MethodDelete, connectionPath(name, "/authorization"), nil, nil, query)
}

func (c *remoteClient) testConnection(ctx context.Context, workspace, name string) ([]api.ConnectionProbe, error) {
	var out api.ConnectionTestResponse
	if err := c.do(ctx, http.MethodPost, connectionPath(name, "/test"), nil, &out, workspaceQuery(workspace)); err != nil {
		return nil, err
	}
	return out.Services, nil
}

// connectionTools returns the tools response, and its body as the server
// sent it, whose input schemas keep their numbers exact.
func (c *remoteClient) connectionTools(ctx context.Context, workspace, name, server string) (*api.ConnectionToolsResponse, json.RawMessage, error) {
	var raw json.RawMessage
	query := map[string]string{"workspace": workspace, "server": server}
	if err := c.atLeast(toolsTimeout).do(ctx, http.MethodGet, connectionPath(name, "/tools"), nil, &raw, query); err != nil {
		return nil, nil, err
	}
	// Numbers stay literal, since a schema bound may not fit a float64.
	var out api.ConnectionToolsResponse
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&out); err != nil {
		return nil, nil, fmt.Errorf("failed to decode the tools response: %w", err)
	}
	return &out, raw, nil
}
