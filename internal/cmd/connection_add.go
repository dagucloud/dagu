// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	api "github.com/dagucloud/dagu/v2/api/v1"
)

var (
	connectionServicesFlag = commandLineFlag{
		name:  "services",
		usage: "Services, comma-separated; add :write for read and write access (e.g., gmail,calendar:write)",
	}
	connectionAgentFlag = commandLineFlag{
		name:   "agent",
		usage:  "For AI agents through mcp.call: use, or register, a client with Google's agent flag",
		isBool: true,
	}
	connectionAppFlag = commandLineFlag{
		name:  "app",
		usage: "OAuth app to use, by ID or name",
	}
	connectionClientFileFlag = commandLineFlag{
		name:  "client-file",
		usage: "Register the client JSON file that Google issued as a new OAuth app (needs admin)",
	}
	connectionNoBrowserFlag = commandLineFlag{
		name:   "no-browser",
		usage:  "Print the sign-in URL without opening a browser, such as over SSH",
		isBool: true,
	}
)

func connectionAddCommand() *cobra.Command {
	cmd := NewCommand(&cobra.Command{
		Use:   "add",
		Short: "Add a connection and sign in its account",
	}, nil, func(ctx *Context, _ []string) error {
		return ctx.Command.Help()
	})
	cmd.AddCommand(connectionCommand(&cobra.Command{
		Use:   "google [name]",
		Short: "Add a Google Workspace connection and sign in its account",
		Long: `Add a Google Workspace connection and sign in its account.

The connection uses an OAuth app: one an admin registered, named with
--app, or a client JSON file that --client-file registers, which needs
admin. Without a name, --services, or an app, the command asks for them
on a terminal.

A web client signs in through the server's callback. A desktop client
signs in through a listener on 127.0.0.1 when a browser opens on this
host, and otherwise asks for the address that the browser lands on.
Before consent, the command prints the redirect URI to register in the
client and the client ID that a Workspace admin may have to trust. After
consent, it asks to confirm the account and checks each service.`,
		Example: `  dagu connection add google work-google --services gmail,calendar --agent \
      --client-file ~/Downloads/client_secret_123.json
  dagu connection add google team-google --workspace ops --services gmail \
      --agent --app google-agent --no-browser`,
		Args: cobra.MaximumNArgs(1),
	}, []commandLineFlag{
		connectionServicesFlag, connectionAgentFlag, connectionAppFlag, connectionClientFileFlag,
		connectionNoBrowserFlag, connectionWorkspaceFlag,
	}, func(ctx *Context, c *connectionCLI, args []string) error {
		req, err := googleAddRequestOf(ctx, args)
		if err != nil {
			return err
		}
		return c.addGoogle(ctx, req)
	}))
	return cmd
}

func connectionReconnectCommand() *cobra.Command {
	return connectionCommand(&cobra.Command{
		Use:   "reconnect <name>",
		Short: "Sign in a connection's account again",
		Long: `Sign in a connection's account again, such as after it needs a reconnect
or gains a service. The previous tokens stay live until the account is
confirmed. When the same account signs in, it is confirmed without asking.`,
		Args: cobra.ExactArgs(1),
	}, []commandLineFlag{connectionNoBrowserFlag, connectionWorkspaceFlag}, func(ctx *Context, c *connectionCLI, args []string) error {
		noBrowser, err := ctx.Command.Flags().GetBool(connectionNoBrowserFlag.name)
		if err != nil {
			return err
		}
		return c.reconnect(ctx, args[0], noBrowser)
	})
}

// reconnect runs the connect flow on the connection named name, expecting
// the account it holds, so that the same account is confirmed without
// asking.
func (c *connectionCLI) reconnect(ctx context.Context, name string, noBrowser bool) error {
	conn, err := c.client.connection(ctx, c.workspace, name)
	if err != nil {
		return err
	}
	app, err := c.client.oauthApp(ctx, conn.App)
	if err != nil {
		return fmt.Errorf("failed to read the OAuth app of connection %s: %w", conn.Name, err)
	}
	return c.connect(ctx, conn, app, connectOptions{noBrowser: noBrowser, expectedAccount: derefString(conn.Account)})
}

// googleAddRequest is what 'connection add google' was given. The wizard
// asks for what is missing.
type googleAddRequest struct {
	name     string
	services []api.ConnectionServiceRequest
	// agent is set by --agent, or by the wizard's question.
	agent      bool
	appRef     string
	clientFile string
	noBrowser  bool
}

func googleAddRequestOf(ctx *Context, args []string) (googleAddRequest, error) {
	req := googleAddRequest{}
	if len(args) > 0 {
		req.name = args[0]
	}
	flags := ctx.Command.Flags()
	var err error
	if req.agent, err = flags.GetBool(connectionAgentFlag.name); err != nil {
		return req, err
	}
	if req.noBrowser, err = flags.GetBool(connectionNoBrowserFlag.name); err != nil {
		return req, err
	}
	if req.appRef, err = ctx.StringParam(connectionAppFlag.name); err != nil {
		return req, err
	}
	if req.clientFile, err = ctx.StringParam(connectionClientFileFlag.name); err != nil {
		return req, err
	}
	if req.appRef != "" && req.clientFile != "" {
		return req, errors.New("pass --app or --client-file, not both")
	}
	services, err := ctx.StringParam(connectionServicesFlag.name)
	if err != nil {
		return req, err
	}
	if services != "" {
		if req.services, err = parseConnectionServices(services); err != nil {
			return req, err
		}
	}
	return req, nil
}

// parseConnectionServices reads services as --services takes them: names,
// comma-separated, each with :read, the default, or :write.
func parseConnectionServices(raw string) ([]api.ConnectionServiceRequest, error) {
	var out []api.ConnectionServiceRequest
	for item := range strings.SplitSeq(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		name, access, _ := strings.Cut(item, ":")
		svc := api.ConnectionServiceRequest{Name: strings.TrimSpace(name), Access: api.ConnectionAccessRead}
		switch api.ConnectionAccess(strings.TrimSpace(access)) {
		case "", api.ConnectionAccessRead:
		case api.ConnectionAccessWrite:
			svc.Access = api.ConnectionAccessWrite
		default:
			return nil, fmt.Errorf("service %q: access must be read or write", item)
		}
		out = append(out, svc)
	}
	if len(out) == 0 {
		return nil, errors.New("--services names no service")
	}
	return out, nil
}

// addGoogle creates a Google connection on an app, and signs in its
// account.
func (c *connectionCLI) addGoogle(ctx context.Context, req googleAddRequest) error {
	if err := c.completeGoogleRequest(ctx, &req); err != nil {
		return err
	}
	app, registered, err := c.googleApp(ctx, &req)
	if err != nil {
		return err
	}
	conn, err := c.client.createConnection(ctx, c.workspace, api.CreateConnectionRequest{
		Name:     req.name,
		App:      app.Id,
		Services: req.services,
	})
	if err != nil && registered {
		return c.unregister(ctx, app, err)
	}
	if err != nil {
		return err
	}
	if registered {
		c.term.printf("Registered OAuth app %s (%s).\n", app.Name, app.Id)
	}
	return c.connect(ctx, conn, app, connectOptions{noBrowser: req.noBrowser})
}

// completeGoogleRequest asks on a terminal for the name and the services
// when they are missing, and fails without one.
func (c *connectionCLI) completeGoogleRequest(ctx context.Context, req *googleAddRequest) error {
	if req.name != "" && len(req.services) > 0 {
		return nil
	}
	if !c.term.interactive {
		return errors.New("pass a connection name and --services, or run the command on a terminal to be asked for them")
	}
	var err error
	if req.name == "" {
		if req.name, err = c.term.ask(ctx, "Connection name", ""); err != nil {
			return err
		}
	}
	for len(req.services) == 0 {
		available := "gmail, calendar"
		if names := c.googleServices(ctx); len(names) > 0 {
			available = strings.Join(names, ", ")
		}
		c.term.printf("Services: %s\n", available)
		answer, err := c.term.ask(ctx, "Services to use, comma-separated; add :write for read and write access", "")
		if err != nil {
			return err
		}
		if req.services, err = parseConnectionServices(answer); err != nil {
			c.term.printf("%v\n", err)
		}
	}
	return nil
}

// googleServices returns the names of the Google services that the server
// connects.
func (c *connectionCLI) googleServices(ctx context.Context) []string {
	resp, err := c.client.connectionProviders(ctx)
	if err != nil {
		return nil
	}
	var names []string
	for _, p := range resp.Providers {
		if p.Name != api.ConnectionProviderNameGoogle {
			continue
		}
		for _, s := range p.Services {
			names = append(names, s.Name)
		}
	}
	return names
}

// googleApp returns the app that the connection uses: the one --app names,
// the one --client-file registers, or, without either, the one fitting app
// or the one the person picks, and whether it registered the app.
func (c *connectionCLI) googleApp(ctx context.Context, req *googleAddRequest) (*api.OAuthApp, bool, error) {
	app, err := c.chooseApp(ctx, req)
	if err != nil || app != nil {
		return app, false, err
	}
	app, err = c.registerClientFile(ctx, req)
	return app, err == nil, err
}

// chooseApp returns the app that --app names, or the one fitting app or
// the one the person picks. It returns nil when a client file is to be
// registered.
func (c *connectionCLI) chooseApp(ctx context.Context, req *googleAddRequest) (*api.OAuthApp, error) {
	if req.clientFile != "" {
		return nil, nil
	}
	if req.appRef != "" {
		app, err := c.findApp(ctx, req.appRef)
		if err != nil {
			return nil, err
		}
		if app.Provider != api.ConnectionProviderNameGoogle {
			return nil, fmt.Errorf("OAuth app %s is a %s app, not a Google one", app.Name, app.Provider)
		}
		if req.agent && !app.Agent {
			return nil, fmt.Errorf("OAuth app %s lacks the agent flag, which --agent needs: Google requires a separate client for AI agents", app.Name)
		}
		return app, nil
	}
	if c.term.interactive && !req.agent {
		agent, err := c.term.confirm(ctx, "Will AI agents use this connection through mcp.call? Google requires a separate client for them.", false)
		if err != nil {
			return nil, err
		}
		req.agent = agent
	}
	apps, err := c.client.oauthApps(ctx)
	if err != nil {
		return nil, err
	}
	var fitting []api.OAuthApp
	for _, app := range apps {
		if app.Provider == api.ConnectionProviderNameGoogle && app.Agent == req.agent && app.Status == api.OAuthAppStatusOk {
			fitting = append(fitting, app)
		}
	}
	if !c.term.interactive {
		if len(fitting) == 1 {
			return &fitting[0], nil
		}
		return nil, fmt.Errorf("%d Google apps fit; name one with --app, or register a client with --client-file\n%s",
			len(fitting), c.appRequest(ctx, req.agent))
	}
	return c.pickApp(ctx, req, fitting)
}

// pickApp asks the person to pick one of apps, or returns nil after the
// person names a client file to register.
func (c *connectionCLI) pickApp(ctx context.Context, req *googleAddRequest, apps []api.OAuthApp) (*api.OAuthApp, error) {
	if len(apps) == 0 {
		c.term.printf("No fitting Google app is registered.\n%s\n", c.appRequest(ctx, req.agent))
	} else {
		c.term.printf("OAuth apps:\n")
		for i, app := range apps {
			c.term.printf("  %d) %s (%s client)\n", i+1, app.Name, app.ClientType)
		}
		c.term.printf("  %d) Register a client JSON file (needs admin)\n", len(apps)+1)
		answer, err := c.term.ask(ctx, "Choose", "1")
		if err != nil {
			return nil, err
		}
		n, err := strconv.Atoi(answer)
		if err != nil || n < 1 || n > len(apps)+1 {
			return nil, fmt.Errorf("choose a number from 1 to %d", len(apps)+1)
		}
		if n <= len(apps) {
			return &apps[n-1], nil
		}
	}
	path, err := c.term.ask(ctx, "Path to the client JSON file", "")
	if err != nil {
		return nil, err
	}
	req.clientFile = path
	return nil, nil
}

// registerClientFile registers the client file as an app named after the
// connection.
func (c *connectionCLI) registerClientFile(ctx context.Context, req *googleAddRequest) (*api.OAuthApp, error) {
	data, err := os.ReadFile(req.clientFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read the client file: %w", err)
	}
	file := string(data)
	app, err := c.client.createOAuthApp(ctx, api.CreateOAuthAppRequest{
		Name:       req.name,
		Provider:   api.CreateOAuthAppRequestProvider(api.ConnectionProviderNameGoogle),
		ClientFile: &file,
		Agent:      &req.agent,
	})
	if rerr, ok := errors.AsType[*remoteError](err); ok && rerr.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("%w\nRegistering a client file needs admin. Ask an admin to register it, then name it with --app.\n%s",
			err, c.appRequest(ctx, req.agent))
	}
	if err != nil {
		return nil, err
	}
	return app, nil
}

// unregister deletes app, which this command registered for a connection
// that could not be created, so that no app holds the client's secret
// without a use. It returns createErr with what became of the app.
func (c *connectionCLI) unregister(ctx context.Context, app *api.OAuthApp, createErr error) error {
	if err := c.client.deleteOAuthApp(ctx, app.Id); err != nil {
		return fmt.Errorf("%w\nThe OAuth app %s (%s) that the client file registered stays, since deleting it failed: %w", createErr, app.Name, app.Id, err)
	}
	return createErr
}

// appRequest says what to ask an admin to register: the client type, the
// redirect URI, and whether the agent flag is needed (Spec 107
// §Permissions).
func (c *connectionCLI) appRequest(ctx context.Context, agent bool) string {
	kind := "a desktop client"
	if resp, err := c.client.connectionProviders(ctx); err == nil {
		for _, r := range resp.Redirects {
			if r.Mode == api.ConnectionRedirectModeServerCallback {
				kind = fmt.Sprintf("a web client with the redirect URI %s, or a desktop client", derefString(r.RedirectUri))
			}
		}
	}
	flag := "without the agent flag"
	if agent {
		flag = "with Google's \"Use this client for an AI-powered agent\" set, and the agent flag"
	}
	return fmt.Sprintf("Ask an admin to register %s, %s.", kind, flag)
}
