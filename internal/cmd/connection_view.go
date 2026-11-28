// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"text/tabwriter"
	"unicode"

	"github.com/spf13/cobra"

	api "github.com/dagucloud/dagu/v2/api/v1"
)

var (
	connectionServerFlag = commandLineFlag{
		name:  "server",
		usage: "List only the tools of this service, such as gmail",
	}
	connectionJSONFlag = commandLineFlag{
		name:   "json",
		usage:  "Print the tools with their input schemas as JSON",
		isBool: true,
	}
)

func connectionProvidersCommand() *cobra.Command {
	return connectionCommand(&cobra.Command{
		Use:   "providers",
		Short: "List the providers, their services, and the redirect modes the server offers",
		Long: `List the providers that the server connects, with their services and
setup pages, and the redirect modes the server offers, each with the
client type it needs and the redirect URI to register in the client.`,
		Args: cobra.NoArgs,
	}, nil, func(ctx *Context, c *connectionCLI, _ []string) error {
		return c.providers(ctx)
	})
}

// providers prints the server's providers and redirect modes.
func (c *connectionCLI) providers(ctx context.Context) error {
	resp, err := c.client.connectionProviders(ctx)
	if err != nil {
		return err
	}
	out := c.term.out
	publicURL := derefString(resp.PublicUrl)
	if publicURL == "" {
		publicURL = "none; set server.public_url for the server callback"
	}
	_, _ = fmt.Fprintf(out, "Public URL: %s\n\nRedirect modes, in the order they are picked:\n", publicURL)
	w := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "  MODE\tCLIENT TYPE\tREDIRECT URI TO REGISTER")
	for _, r := range resp.Redirects {
		uri := derefString(r.RedirectUri)
		if r.Mode == api.ConnectionRedirectModeLoopback {
			uri = "http://127.0.0.1 on a port the CLI picks"
		}
		_, _ = fmt.Fprintf(w, "  %s\t%s\t%s\n", r.Mode, r.ClientType, uri)
	}
	_ = w.Flush()
	for _, p := range resp.Providers {
		_, _ = fmt.Fprintf(out, "\nProvider %s\n", p.Name)
		w := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "  SERVICE\tMCP\tAPIS")
		for _, s := range p.Services {
			mcp := "no"
			if s.McpUrl != nil {
				mcp = "yes"
			}
			if s.Preview {
				mcp += " (developer preview)"
			}
			_, _ = fmt.Fprintf(w, "  %s\t%s\t%s\n", s.Name, mcp, strings.Join(s.Apis, ", "))
		}
		_ = w.Flush()
		if links := p.SetupLinks; links != nil {
			printLabeled(out, "  ", []labeledValue{
				{"Clients", derefString(links.Clients)},
				{"Consent screen", derefString(links.ConsentScreen)},
				{"Admin trust", derefString(links.AdminTrust)},
			})
		}
	}
	return nil
}

func connectionListCommand() *cobra.Command {
	return connectionCommand(&cobra.Command{
		Use:   "list",
		Short: "List the connections of a workspace with their account and status",
		Args:  cobra.NoArgs,
	}, []commandLineFlag{connectionWorkspaceFlag}, func(ctx *Context, c *connectionCLI, _ []string) error {
		return c.list(ctx)
	})
}

// list prints the workspace's connections.
func (c *connectionCLI) list(ctx context.Context) error {
	conns, err := c.client.connections(ctx, c.workspace)
	if err != nil {
		return err
	}
	if len(conns) == 0 {
		c.term.printf("No connections in workspace %s. Add one with '%s'.\n", orGlobal(c.workspace), c.command("add", "google"))
		return nil
	}
	w := tabwriter.NewWriter(c.term.out, 0, 2, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "NAME\tPROVIDER\tACCOUNT\tSTATUS\tSERVICES")
	for _, conn := range conns {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", conn.Name, conn.Provider, orDash(printable(derefString(conn.Account))),
			conn.Status, orDash(serviceList(conn.Services)))
	}
	return w.Flush()
}

func connectionShowCommand() *cobra.Command {
	return connectionCommand(&cobra.Command{
		Use:   "show <name>",
		Short: "Show a connection's status, account, services, and scopes",
		Args:  cobra.ExactArgs(1),
	}, []commandLineFlag{connectionWorkspaceFlag}, func(ctx *Context, c *connectionCLI, args []string) error {
		return c.show(ctx, args[0])
	})
}

// show prints one connection, and how to grant the scopes its services
// lack.
func (c *connectionCLI) show(ctx context.Context, name string) error {
	conn, err := c.client.connection(ctx, c.workspace, name)
	if err != nil {
		return err
	}
	// A viewer may read a connection but not the apps, so the app's ID
	// stands in for its name. Any other failure is shown beside the ID.
	app := conn.App
	a, err := c.client.oauthApp(ctx, conn.App)
	switch {
	case err == nil:
		app = fmt.Sprintf("%s (%s)", a.Name, a.Id)
	case !isRemoteStatus(err, http.StatusForbidden):
		app = fmt.Sprintf("%s (not read: %v)", conn.App, err)
	}
	status := string(conn.Status)
	if reason := derefString(conn.StatusReason); reason != "" {
		status += ": " + reason
	}
	printLabeled(c.term.out, "", []labeledValue{
		{"Name", conn.Name},
		{"Workspace", conn.Workspace},
		{"Provider", string(conn.Provider)},
		{"App", app},
		{"Status", status},
		{"Account", derefString(conn.Account)},
		{"Services", serviceList(conn.Services)},
		{"Scopes", strings.Join(conn.Scopes, ", ")},
		{"Bound hosts", strings.Join(conn.BoundHosts, ", ")},
		{"Granted scopes", strings.Join(conn.GrantedScopes, ", ")},
		{"Refreshed", formatConnectionTime(conn.RefreshedAt)},
		{"Refresh token expires", formatConnectionTime(conn.RefreshTokenExpiresAt)},
		{"Token export", strconv.FormatBool(conn.AllowTokenExport)},
	})
	for _, svc := range conn.Services {
		if len(svc.MissingScopes) > 0 {
			c.term.printf("Service %s lacks %s. Run '%s' to grant it.\n",
				svc.Name, strings.Join(svc.MissingScopes, ", "), c.command("reconnect", conn.Name))
		}
	}
	return nil
}

func connectionToolsCommand() *cobra.Command {
	return connectionCommand(&cobra.Command{
		Use:   "tools <name>",
		Short: "List the MCP tools of a connection's services",
		Long: `List the MCP tools of a connection's services, read from the tool
catalog that the server keeps. A catalog older than 10 minutes is listed
again. Fails when a service's tools could not be listed.`,
		Args: cobra.ExactArgs(1),
	}, []commandLineFlag{connectionWorkspaceFlag, connectionServerFlag, connectionJSONFlag}, func(ctx *Context, c *connectionCLI, args []string) error {
		server, err := ctx.StringParam(connectionServerFlag.name)
		if err != nil {
			return err
		}
		asJSON, err := ctx.Command.Flags().GetBool(connectionJSONFlag.name)
		if err != nil {
			return err
		}
		return c.tools(ctx, args[0], server, asJSON)
	})
}

// tools prints the MCP tools of the connection's services, or of server
// alone when it is set, as a table or as JSON.
func (c *connectionCLI) tools(ctx context.Context, name, server string, asJSON bool) error {
	resp, raw, err := c.client.connectionTools(ctx, c.workspace, name, server)
	if netErr, ok := errors.AsType[net.Error](err); ok && netErr.Timeout() {
		// The server goes on listing a stale catalog after the request
		// ends, so a later request reads the new listing.
		return fmt.Errorf("%w\nThe server may still be listing the tools. Run '%s' again shortly", err, c.command("tools", name))
	}
	if err != nil {
		return err
	}
	if asJSON {
		// The body is printed as the server sent it, so that the input
		// schemas keep their numbers exact.
		var indented bytes.Buffer
		if err := json.Indent(&indented, raw, "", "  "); err != nil {
			return err
		}
		indented.WriteByte('\n')
		if _, err := indented.WriteTo(c.term.out); err != nil {
			return err
		}
		return toolErrors(resp)
	}
	out := c.term.out
	for i, s := range resp.Servers {
		if i > 0 {
			_, _ = fmt.Fprintln(out)
		}
		if msg := derefString(s.Error); msg != "" {
			_, _ = fmt.Fprintf(out, "%s: %s\n", s.Server, printable(msg))
			continue
		}
		_, _ = fmt.Fprintf(out, "%s: %d tools, listed %s\n", s.Server, len(s.Tools), formatConnectionTime(s.ListedAt))
		w := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
		for _, tool := range s.Tools {
			access := "writes"
			if readOnly, _ := derefMap(tool.Annotations)["readOnlyHint"].(bool); readOnly {
				access = "read-only"
			}
			_, _ = fmt.Fprintf(w, "  %s\t%s\t%s\n", printable(tool.Name), access, printable(firstLine(derefString(tool.Description))))
		}
		_ = w.Flush()
	}
	return toolErrors(resp)
}

// toolErrors fails when a service's tools could not be listed.
func toolErrors(resp *api.ConnectionToolsResponse) error {
	var failed []string
	for _, s := range resp.Servers {
		if s.Error != nil {
			failed = append(failed, s.Server)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("the tools of %s could not be listed", strings.Join(failed, ", "))
	}
	return nil
}

func connectionAppsCommand() *cobra.Command {
	cmd := NewCommand(&cobra.Command{
		Use:   "apps",
		Short: "List and remove the OAuth apps that connections use",
	}, nil, func(ctx *Context, _ []string) error {
		return ctx.Command.Help()
	})
	cmd.AddCommand(connectionCommand(&cobra.Command{
		Use:   "list",
		Short: "List the OAuth apps, without secrets",
		Args:  cobra.NoArgs,
	}, nil, func(ctx *Context, c *connectionCLI, _ []string) error {
		return c.listApps(ctx)
	}))
	cmd.AddCommand(connectionCommand(&cobra.Command{
		Use:   "remove <app>",
		Short: "Remove an OAuth app that no connection uses",
		Long:  "Remove an OAuth app, named by its ID or name. An app that a connection uses is refused, naming the connections. Needs admin.",
		Args:  cobra.ExactArgs(1),
	}, nil, func(ctx *Context, c *connectionCLI, args []string) error {
		return c.removeApp(ctx, args[0])
	}))
	return cmd
}

// listApps prints the OAuth apps, which hold no secret.
func (c *connectionCLI) listApps(ctx context.Context) error {
	apps, err := c.client.oauthApps(ctx)
	if err != nil {
		return err
	}
	if len(apps) == 0 {
		c.term.printf("No OAuth apps are registered. An admin registers one with '%s --client-file FILE'.\n", c.command("add", "google"))
		return nil
	}
	w := tabwriter.NewWriter(c.term.out, 0, 2, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "ID\tNAME\tPROVIDER\tCLIENT TYPE\tAGENT\tSTATUS\tCLIENT ID")
	for _, app := range apps {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%t\t%s\t%s\n", app.Id, app.Name, app.Provider, app.ClientType, app.Agent, app.Status, app.ClientId)
	}
	return w.Flush()
}

// removeApp removes the app whose ID or name is ref.
func (c *connectionCLI) removeApp(ctx context.Context, ref string) error {
	app, err := c.findApp(ctx, ref)
	if err != nil {
		return err
	}
	if err := c.client.deleteOAuthApp(ctx, app.Id); err != nil {
		return err
	}
	c.term.printf("OAuth app %s (%s) was removed.\n", app.Name, app.Id)
	return nil
}

type labeledValue struct{ label, value string }

// printLabeled prints each row with a value, with the values aligned.
func printLabeled(out io.Writer, indent string, rows []labeledValue) {
	w := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	for _, row := range rows {
		if row.value != "" {
			_, _ = fmt.Fprintf(w, "%s%s:\t%s\n", indent, row.label, printable(row.value))
		}
	}
	_ = w.Flush()
}

// printable drops the control characters of text that a provider or an
// MCP server wrote, so that it cannot move the cursor or recolor the
// terminal. A tab or line break becomes a space, which keeps words apart
// on the one line the text gets.
func printable(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case !unicode.IsControl(r):
			return r
		case unicode.IsSpace(r):
			return ' '
		default:
			return -1
		}
	}, s)
}

// orGlobal names the workspace that an empty --workspace selects.
func orGlobal(workspace string) string {
	if workspace == "" {
		return "global"
	}
	return workspace
}

// serviceList prints services as --services takes them.
func serviceList(services []api.ConnectionService) string {
	names := make([]string, len(services))
	for i, svc := range services {
		names[i] = svc.Name
		if svc.Access == api.ConnectionAccessWrite {
			names[i] += ":" + string(api.ConnectionAccessWrite)
		}
	}
	return strings.Join(names, ",")
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}
