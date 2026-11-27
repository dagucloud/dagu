// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/connection"
	"github.com/dagucloud/dagu/v2/internal/connection/connect"
	"github.com/dagucloud/dagu/v2/internal/connection/toolcatalog"
)

// ListConnectionProviders lists the providers that the server connects and
// the redirect modes it offers.
func (a *API) ListConnectionProviders(ctx context.Context, _ api.ListConnectionProvidersRequestObject) (api.ListConnectionProvidersResponseObject, error) {
	if a.connectionStore == nil || a.connectEngine == nil {
		return nil, errConnectionsUnavailable
	}
	publicURL := a.usablePublicURL(ctx)
	resp := api.ListConnectionProviders200JSONResponse{PublicUrl: ptrOf(publicURL)}
	for _, offer := range connect.Offers(publicURL) {
		resp.Redirects = append(resp.Redirects, api.ConnectionRedirect{
			Mode:        api.ConnectionRedirectMode(offer.Mode),
			ClientType:  api.OAuthClientType(offer.ClientType),
			RedirectUri: ptrOf(offer.URI),
		})
	}
	for _, provider := range a.connectEngine.Providers() {
		resp.Providers = append(resp.Providers, toConnectionProvider(provider, a.connectEngine.Guide(provider)))
	}
	if resp.Providers == nil {
		resp.Providers = []api.ConnectionProvider{}
	}
	return resp, nil
}

// TestConnection probes each of a connection's services with its live
// token.
func (a *API) TestConnection(ctx context.Context, request api.TestConnectionRequestObject) (api.TestConnectionResponseObject, error) {
	conn, err := a.getConnection(ctx, request.Name, request.Params.Workspace, auth.Role.CanWrite)
	if err != nil {
		return nil, err
	}
	probes, err := a.connectEngine.Test(ctx, conn)
	if err != nil {
		return nil, connectError(err)
	}
	return api.TestConnection200JSONResponse{Services: toConnectionProbes(probes)}, nil
}

// ListConnectionTools reads the tool catalog of each MCP service of a
// connection, or of the one that the request names.
func (a *API) ListConnectionTools(ctx context.Context, request api.ListConnectionToolsRequestObject) (api.ListConnectionToolsResponseObject, error) {
	conn, err := a.getConnection(ctx, request.Name, request.Params.Workspace, auth.Role.CanWrite)
	if err != nil {
		return nil, err
	}
	if a.toolCatalog == nil {
		return nil, errConnectionsUnavailable
	}
	servers := mcpServers(conn)
	if request.Params.Server != nil {
		servers = []string{*request.Params.Server}
	}
	resp := api.ListConnectionTools200JSONResponse{Servers: make([]api.ConnectionToolServer, len(servers))}
	errs := make([]error, len(servers))
	// Each stale catalog lists its server, so the servers list together.
	var wg sync.WaitGroup
	for i, server := range servers {
		wg.Go(func() {
			catalog, err := a.toolCatalog.Catalog(ctx, conn, server)
			resp.Servers[i], errs[i] = toConnectionToolServer(server, catalog, err), err
		})
	}
	wg.Wait()
	if request.Params.Server != nil && errors.Is(errs[0], toolcatalog.ErrNotMCPService) {
		return nil, badConnectionRequest(errs[0].Error())
	}
	return resp, nil
}

// mcpServers returns the catalogs that conn's tools come from: each
// service with an MCP server, or the server of an mcp connection.
func mcpServers(conn *connection.Connection) []string {
	if conn.Provider == connection.ProviderMCP {
		return []string{""}
	}
	servers := []string{}
	for _, svc := range conn.Services {
		if p, ok := connection.LookupPreset(conn.Provider, svc.Name); ok && p.MCPURL != "" {
			servers = append(servers, svc.Name)
		}
	}
	return servers
}

func toConnectionProvider(provider connection.Provider, guide connect.Guide) api.ConnectionProvider {
	resp := api.ConnectionProvider{
		Name:       api.ConnectionProviderName(provider),
		UserScopes: nonNil(connection.UserScopes(provider)),
		Services:   []api.ConnectionPreset{},
	}
	if guide != nil {
		links := guide.SetupLinks()
		resp.SetupLinks = &api.ConnectionProviderSetupLinks{
			Clients:       ptrOf(links.Clients),
			ConsentScreen: ptrOf(links.ConsentScreen),
			AdminTrust:    ptrOf(links.AdminTrust),
		}
	}
	for _, p := range connection.Presets(provider) {
		preset := api.ConnectionPreset{
			Name:        p.Name,
			McpUrl:      ptrOf(p.MCPURL),
			ReadScopes:  nonNil(p.ReadScopes),
			WriteScopes: nonNil(p.WriteScopes),
			Apis:        nonNil(p.APIs),
			Preview:     p.Preview,
		}
		if guide != nil {
			preset.EnableApisUrl = ptrOf(guide.EnableAPIsLink(p.APIs))
		}
		resp.Services = append(resp.Services, preset)
	}
	return resp
}

func toConnectionToolServer(server string, catalog *connection.Catalog, err error) api.ConnectionToolServer {
	resp := api.ConnectionToolServer{Server: server, Tools: []api.ConnectionTool{}}
	if err != nil {
		resp.Error = ptrOf(err.Error())
		return resp
	}
	resp.ListedAt = ptrOf(catalog.ListedAt)
	for _, tool := range catalog.Tools {
		item := api.ConnectionTool{
			Name:        tool.Name,
			Description: ptrOf(tool.Description),
			InputSchema: jsonObject(tool.InputSchema),
		}
		if annotations := jsonObject(tool.Annotations); len(annotations) > 0 {
			item.Annotations = &annotations
		}
		resp.Tools = append(resp.Tools, item)
	}
	return resp
}

// jsonObject decodes raw as a JSON object, or returns an empty one when it
// holds none.
func jsonObject(raw json.RawMessage) map[string]any {
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return map[string]any{}
	}
	return obj
}
