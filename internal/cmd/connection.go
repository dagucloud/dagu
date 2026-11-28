// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	api "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/service/frontend"
)

// connectionTimeLayout prints the times that connections report.
const connectionTimeLayout = "2006-01-02 15:04 MST"

var (
	connectionWorkspaceFlag = commandLineFlag{
		name:  "workspace",
		usage: "Workspace that owns the connection (default: global, the connections of DAGs without a workspace label)",
	}
	connectionYesFlag = commandLineFlag{
		name:      "yes",
		shorthand: "y",
		usage:     "Remove without asking",
		isBool:    true,
	}
)

// Connection returns the command group for connections (Spec 107 §CLI).
func Connection() *cobra.Command {
	cmd := NewCommand(&cobra.Command{
		Use:     "connection",
		Aliases: []string{"connections"},
		Short:   "Add, test, reconnect, and remove connections",
		Long: `Work with connections: named OAuth sign-ins, such as a Google Workspace
account, that a workspace's DAGs name from steps such as mcp.call.

Every subcommand calls the server's REST API. With a remote context it
acts on that server; with the local context it calls the server that this
host's configuration describes, which must be running. No command prints
a client secret, refresh token, or access token.`,
	}, nil, func(ctx *Context, _ []string) error {
		return ctx.Command.Help()
	})

	cmd.AddCommand(connectionAddCommand())
	cmd.AddCommand(connectionTestCommand())
	cmd.AddCommand(connectionReconnectCommand())
	cmd.AddCommand(connectionRemoveCommand())
	return cmd
}

// connectionCLI is what a connection subcommand works with: the API of the
// context's server, the workspace it acts in, and the terminal.
type connectionCLI struct {
	client    *remoteClient
	workspace string
	term      *connectionTerm
	// localServer is the API base URL of this host's server when the local
	// context is selected, or empty for a remote context.
	localServer string
}

// connectionTerm is the terminal a connection subcommand talks to.
type connectionTerm struct {
	in  *bufio.Reader
	out io.Writer
	// interactive reports that a person answers on in.
	interactive bool
	// readSecret reads one line from the person without echoing it.
	readSecret func(context.Context) (string, error)
	// openBrowser opens a URL in a browser on this host. It is nil when no
	// browser can open here, such as over SSH.
	openBrowser func(string) error
}

// connectionCommand builds a connection subcommand whose run gets the
// subcommand's connectionCLI.
func connectionCommand(cmd *cobra.Command, flags []commandLineFlag, run func(ctx *Context, c *connectionCLI, args []string) error) *cobra.Command {
	return NewCommand(cmd, flags, func(ctx *Context, args []string) error {
		c, err := newConnectionCLI(ctx)
		if err != nil {
			return err
		}
		return c.explain(run(ctx, c, args))
	})
}

func newConnectionCLI(ctx *Context) (*connectionCLI, error) {
	c := &connectionCLI{term: stdConnectionTerm()}
	if flag := ctx.Command.Flags().Lookup(connectionWorkspaceFlag.name); flag != nil {
		c.workspace = strings.TrimSpace(flag.Value.String())
	}
	if ctx.IsRemote() {
		c.client = ctx.Remote
		return c, nil
	}
	client, base, err := localServerClient(ctx, ctx.Config)
	if err != nil {
		return nil, err
	}
	c.client, c.localServer = client, base
	return c, nil
}

func stdConnectionTerm() *connectionTerm {
	stdin := int(os.Stdin.Fd())
	t := &connectionTerm{
		in:          bufio.NewReader(os.Stdin),
		out:         os.Stdout,
		interactive: term.IsTerminal(stdin),
		openBrowser: systemBrowser(),
	}
	t.readSecret = t.readAnswer
	if t.interactive {
		t.readSecret = func(ctx context.Context) (string, error) { return t.readHidden(ctx, stdin) }
	}
	return t
}

// Control bytes that a terminal in raw mode sends.
const (
	ctrlC     = 0x03
	ctrlD     = 0x04
	backspace = 0x08
	deleteKey = 0x7f
)

// readHidden reads one line from the terminal fd without echo. It reads in
// raw mode, since a terminal in canonical mode cuts a pasted line at its
// limit, 1,024 bytes on macOS, and a redirect URL can be longer. The
// terminal is restored also when ctx ends, such as on SIGTERM.
func (t *connectionTerm) readHidden(ctx context.Context, fd int) (string, error) {
	state, err := term.MakeRaw(fd)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = term.Restore(fd, state)
		_, _ = fmt.Fprintln(t.out)
	}()
	return t.await(ctx, t.readRawLine)
}

// readRawLine reads one line from a terminal in raw mode, where Ctrl-C
// arrives as a byte.
func (t *connectionTerm) readRawLine() (string, error) {
	var line []byte
	for {
		b, err := t.in.ReadByte()
		if err != nil {
			return "", err
		}
		switch b {
		case '\r', '\n':
			return strings.TrimSpace(string(line)), nil
		case ctrlC:
			return "", errSignInCancelled
		case ctrlD:
			if len(line) == 0 {
				return "", io.EOF
			}
		case backspace, deleteKey:
			if len(line) > 0 {
				line = line[:len(line)-1]
			}
		default:
			line = append(line, b)
		}
	}
}

// localServerClient returns a client of the REST API that the server of
// cfg serves on this host, and that API's base URL. A server that binds
// every address is reached at localhost, which the server takes as its
// public URL when it has none (Spec 107 §Redirect Modes).
func localServerClient(ctx context.Context, cfg *config.Config) (*remoteClient, string, error) {
	apiPath, err := frontend.APIPath(ctx, cfg)
	if err != nil {
		return nil, "", err
	}
	scheme := "http"
	if cfg.Server.TLS != nil {
		scheme = "https"
	}
	host := cfg.Server.Host
	if addr, err := netip.ParseAddr(host); host == "" || (err == nil && addr.IsUnspecified()) {
		host = "localhost"
	}
	base := scheme + "://" + net.JoinHostPort(host, strconv.Itoa(cfg.Server.Port)) + apiPath
	client, err := newRemoteClient(&cliContext{Name: localContextName, ServerURL: base})
	if err != nil {
		return nil, "", err
	}
	if cfg.Server.TLS != nil && cfg.Server.TLS.CAFile != "" {
		if err := trustCAFile(client, cfg.Server.TLS.CAFile); err != nil {
			return nil, "", err
		}
	}
	if cfg.Server.Auth.Mode == config.AuthModeBasic {
		client.basicAuth = &cfg.Server.Auth.Basic
	}
	return client, base, nil
}

// trustCAFile makes client trust the certificates in the PEM file at path,
// beside the system's, so that a server whose certificate a private CA
// issued is reached.
func trustCAFile(client *remoteClient, path string) error {
	data, err := os.ReadFile(path) //nolint:gosec // path is the server's configured tls.ca_file
	if err != nil {
		return fmt.Errorf("failed to read server.tls.ca_file: %w", err)
	}
	pool, err := x509.SystemCertPool()
	if err != nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(data) {
		return fmt.Errorf("server.tls.ca_file %s holds no PEM certificate", path)
	}
	client.client.Transport.(*http.Transport).TLSClientConfig.RootCAs = pool
	return nil
}

// explain adds to err what to do when the local server is not running,
// presents a certificate for another host, or asks for credentials that
// the local context does not have.
func (c *connectionCLI) explain(err error) error {
	if err == nil || c.localServer == "" {
		return err
	}
	// 'dagu context add' asks for the API key, so it stays off the command
	// line.
	const addContext = "Add a context for it with 'dagu context add <name> --server <url>', which asks for its API key, and select it with --context"
	if _, ok := errors.AsType[*tls.CertificateVerificationError](err); ok {
		return fmt.Errorf("%w\nThe certificate of the server at %s does not verify for that address. %s, with the URL of the host the certificate names", err, c.localServer, addContext)
	}
	if opErr, ok := errors.AsType[*net.OpError](err); ok && opErr.Op == "dial" {
		return fmt.Errorf("%w\nNo Dagu server answers at %s. Start it with 'dagu start-all' or 'dagu server', or select a remote context with --context", err, c.localServer)
	}
	if rerr, ok := errors.AsType[*remoteError](err); ok && rerr.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("%w\nThe server at %s needs an API key. %s", err, c.localServer, addContext)
	}
	return err
}

// command returns the dagu connection command that runs sub on the
// connection named name in this CLI's workspace.
func (c *connectionCLI) command(sub, name string) string {
	s := "dagu connection " + sub + " " + name
	if c.workspace != "" {
		s += " --workspace " + c.workspace
	}
	return s
}

func (t *connectionTerm) printf(format string, args ...any) {
	_, _ = fmt.Fprintf(t.out, format, args...)
}

// readLine reads one line, without its line ending. It fails at the end of
// the input when the line is empty.
func (t *connectionTerm) readLine() (string, error) {
	line, err := t.in.ReadString('\n')
	if err != nil && (!errors.Is(err, io.EOF) || line == "") {
		if errors.Is(err, io.EOF) {
			return "", errors.New("no answer: the input ended")
		}
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// readAnswer reads one line, and stops waiting for it when ctx ends.
func (t *connectionTerm) readAnswer(ctx context.Context) (string, error) {
	return t.await(ctx, t.readLine)
}

// await returns what read returns, or ctx's error once ctx ends first. A
// read that ctx stopped waiting for is left to finish on its own.
func (t *connectionTerm) await(ctx context.Context, read func() (string, error)) (string, error) {
	type answer struct {
		line string
		err  error
	}
	ch := make(chan answer, 1)
	go func() {
		line, err := read()
		ch <- answer{line, err}
	}()
	select {
	case a := <-ch:
		return a.line, a.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// ask writes question and returns the answer, or def for an empty one.
func (t *connectionTerm) ask(ctx context.Context, question, def string) (string, error) {
	if def != "" {
		t.printf("%s [%s]: ", question, def)
	} else {
		t.printf("%s: ", question)
	}
	answer, err := t.readAnswer(ctx)
	if err != nil {
		return "", err
	}
	if answer == "" {
		return def, nil
	}
	return answer, nil
}

// confirm asks a yes or no question, whose empty answer is def.
func (t *connectionTerm) confirm(ctx context.Context, question string, def bool) (bool, error) {
	hint := "[y/N]"
	if def {
		hint = "[Y/n]"
	}
	t.printf("%s %s ", question, hint)
	answer, err := t.readAnswer(ctx)
	if err != nil {
		return false, err
	}
	switch strings.ToLower(answer) {
	case "":
		return def, nil
	case "y", "yes":
		return true, nil
	case "n", "no":
		return false, nil
	default:
		return false, fmt.Errorf("answer y or n, not %q", answer)
	}
}

func connectionTestCommand() *cobra.Command {
	return connectionCommand(&cobra.Command{
		Use:   "test <name>",
		Short: "Probe each service of a connection with its live token",
		Long: `Probe each service of a connection with its live token. With an app that
has the agent flag, each service with an MCP server lists its tools, which
refreshes its tool catalog; any other service reads its REST profile.
Fails when any service fails.`,
		Args: cobra.ExactArgs(1),
	}, []commandLineFlag{connectionWorkspaceFlag}, func(ctx *Context, c *connectionCLI, args []string) error {
		conn, err := c.client.connection(ctx, c.workspace, args[0])
		if err != nil {
			return err
		}
		probes, err := c.client.testConnection(ctx, c.workspace, conn.Name)
		if err != nil {
			return err
		}
		return reportProbes(c.term.out, conn.Services, probes, nil)
	})
}

// reportProbes prints each service's access and result, with its MCP tool
// count from tools when known, and fails when any service failed.
func reportProbes(out io.Writer, services []api.ConnectionService, probes []api.ConnectionProbe, tools map[string]int) error {
	access := make(map[string]api.ConnectionAccess, len(services))
	for _, svc := range services {
		access[svc.Name] = svc.Access
	}
	w := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	failed := 0
	for _, p := range probes {
		result := "ok"
		if n, ok := tools[p.Service]; ok && p.Ok {
			result = fmt.Sprintf("ok  (%d MCP tools)", n)
		}
		if !p.Ok {
			failed++
			result = "failed: " + derefString(p.Error)
		}
		_, _ = fmt.Fprintf(w, "  %s\t%s\t%s\n", p.Service, access[p.Service], result)
	}
	_ = w.Flush()
	if failed > 0 {
		return fmt.Errorf("%d of %d services failed", failed, len(probes))
	}
	return nil
}

func connectionRemoveCommand() *cobra.Command {
	return connectionCommand(&cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a connection with its tokens, and revoke its refresh token",
		Long: `Remove a connection with its tokens and tool catalogs, then revoke its
refresh token where the provider supports it, unless another connection on
this server holds the same account on the same client. Connections on
other servers that use the same client can lose access.`,
		Args: cobra.ExactArgs(1),
	}, []commandLineFlag{connectionWorkspaceFlag, connectionYesFlag}, func(ctx *Context, c *connectionCLI, args []string) error {
		yes, err := ctx.Command.Flags().GetBool(connectionYesFlag.name)
		if err != nil {
			return err
		}
		return c.remove(ctx, args[0], yes)
	})
}

// remove removes the connection named name after saying who else may lose
// access, and asks first unless yes.
func (c *connectionCLI) remove(ctx context.Context, name string, yes bool) error {
	conn, err := c.client.connection(ctx, c.workspace, name)
	if err != nil {
		return err
	}
	same := "the same client"
	if conn.Provider == api.ConnectionProviderNameGoogle {
		same = "any OAuth client in the same Google Cloud project"
	}
	c.term.printf("Removing %s revokes its refresh token, unless another connection on this server holds the same account on the same client. Connections on other servers that use %s may lose access.\n",
		conn.Name, same)
	if !yes {
		if !c.term.interactive {
			return errors.New("pass --yes to remove a connection without a terminal")
		}
		ok, err := c.term.confirm(ctx, fmt.Sprintf("Remove connection %s?", conn.Name), false)
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("nothing removed")
		}
	}
	if err := c.client.deleteConnection(ctx, c.workspace, conn.Name); err != nil {
		return err
	}
	c.term.printf("Connection %s was removed.\n", conn.Name)
	return nil
}

// findApp returns the app whose ID is ref, else the one app named ref.
func (c *connectionCLI) findApp(ctx context.Context, ref string) (*api.OAuthApp, error) {
	apps, err := c.client.oauthApps(ctx)
	if err != nil {
		return nil, err
	}
	var named []api.OAuthApp
	for _, app := range apps {
		if app.Id == ref {
			return &app, nil
		}
		if app.Name == ref {
			named = append(named, app)
		}
	}
	switch len(named) {
	case 0:
		return nil, fmt.Errorf("no OAuth app has the ID or name %q", ref)
	case 1:
		return &named[0], nil
	default:
		return nil, fmt.Errorf("%d OAuth apps are named %q; name one by its ID", len(named), ref)
	}
}

func formatConnectionTime(t *time.Time) string {
	if t == nil || t.IsZero() {
		return ""
	}
	return t.Local().Format(connectionTimeLayout)
}
