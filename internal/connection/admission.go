// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package connection

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/secret"
)

// Admit checks that a run of a DAG in workspace may use the connection
// named name, calling servers through it as MCP servers. It fails with an
// error that names the connection when the workspace has no such
// connection, when a server is not one of its services or its preset has
// no MCP server, or when a Google MCP server on a connection that signs in
// a user needs an app with Agent set.
//
// An empty workspace is that of DAGs without a workspace label, whose
// connections are in secret.GlobalWorkspace. No other workspace falls back
// to it.
//
// Admit reads no token: a connection that is pending, needs reconnecting,
// or uses an invalid app passes, and fails when a step asks for its token.
func Admit(ctx context.Context, store Store, workspace, name string, servers []string) error {
	key := Key{Workspace: secret.NormalizeWorkspace(workspace), Name: name}
	conn, err := store.Get(ctx, key)
	if errors.Is(err, ErrNotFound) {
		return fmt.Errorf("%w: %q in %s", ErrNotFound, name, describeWorkspace(key.Workspace))
	}
	if err != nil {
		return fmt.Errorf("connection %q: %w", name, err)
	}
	var agentServer string
	for _, server := range servers {
		if !slices.ContainsFunc(conn.Services, func(svc Service) bool { return svc.Name == server }) {
			return fmt.Errorf("connection %q: server %q is not one of its services (%s)", name, server, serviceNames(conn.Services))
		}
		if p, ok := LookupPreset(conn.Provider, server); ok && p.MCPURL == "" {
			return fmt.Errorf("connection %q: service %q has no MCP server", name, server)
		}
		if agentServer == "" && conn.needsAgent(server) {
			agentServer = server
		}
	}
	if agentServer == "" {
		return nil
	}
	app, err := store.GetApp(ctx, conn.App)
	if err != nil {
		return fmt.Errorf("connection %q: %w", name, err)
	}
	if !app.Agent {
		return fmt.Errorf("connection %q: app %q lacks the agent flag, which Google MCP server %q needs on a connection that signs in a user", name, app.Name, agentServer)
	}
	return nil
}

// needsAgent reports whether calling service as an MCP server through c
// needs an app with Agent set: a Google preset with an MCP server, on a
// connection that signs in a user. A service account needs no agent flag.
func (c *Connection) needsAgent(service string) bool {
	if c.Provider != ProviderGoogle || !c.Grant.signsInUser() {
		return false
	}
	p, ok := LookupPreset(c.Provider, service)
	return ok && p.MCPURL != ""
}

func describeWorkspace(workspace string) string {
	if workspace == secret.GlobalWorkspace {
		return "the connections of DAGs without a workspace label"
	}
	return fmt.Sprintf("workspace %q", workspace)
}

func serviceNames(services []Service) string {
	if len(services) == 0 {
		return "none"
	}
	names := make([]string, len(services))
	for i, svc := range services {
		names[i] = svc.Name
	}
	return strings.Join(names, ", ")
}
