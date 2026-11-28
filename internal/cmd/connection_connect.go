// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	api "github.com/dagucloud/dagu/v2/api/v1"
)

const (
	// connectPollInterval is how often the CLI reads a session that waits
	// for consent, as 'dagu license activate' polls its request.
	connectPollInterval = time.Second
	// loopbackCallbackPath is the path of the CLI's loopback listener.
	loopbackCallbackPath = "/oauth/callback"
	// cancelTimeout bounds the request that cancels a session that the
	// CLI leaves unconfirmed.
	cancelTimeout = 10 * time.Second
)

// errSignInCancelled reports that the person stopped the sign-in.
var errSignInCancelled = errors.New("sign-in cancelled")

// sessionFailed is an error after which the server holds the session as
// failed, so no new tokens went live.
type sessionFailed struct{ err error }

func (e sessionFailed) Error() string { return e.err.Error() }
func (e sessionFailed) Unwrap() error { return e.err }

func isSessionFailed(err error) bool {
	_, ok := errors.AsType[sessionFailed](err)
	return ok
}

// connectOptions decide how a connect session runs.
type connectOptions struct {
	// noBrowser prints the sign-in URL without opening a browser.
	noBrowser bool
	// expectedAccount is the email address expected to sign in. A session
	// that the provider vouches for with it is confirmed without asking.
	expectedAccount string
}

// signedIn is a session whose tokens went live, with what to report.
type signedIn struct {
	conn   *api.Connection
	probes []api.ConnectionProbe
	// refreshTokenExpiresAt and refreshTokenFix are the provider's limit
	// on the refresh token, and what to do about it.
	refreshTokenExpiresAt *time.Time
	refreshTokenFix       string
}

// consentRedirect is the provider's redirect after consent, as the browser
// landed on it.
type consentRedirect struct {
	url string
	// done reports the result of completing the session to the browser
	// that sent the redirect.
	done func(error)
}

// connect runs the connect flow on conn, an app's connection, from start
// to confirm, and prints each service's result.
func (c *connectionCLI) connect(ctx context.Context, conn *api.Connection, app *api.OAuthApp, opts connectOptions) error {
	var body api.StartConnectionAuthorizationRequest
	if opts.expectedAccount != "" {
		body.ExpectedAccount = &opts.expectedAccount
	}
	var listener *loopbackListener
	if app.ClientType == api.OAuthClientTypeDesktop {
		// Only the CLI knows whether a browser opens on its host, which
		// loopback needs. The server picks the mode of other clients.
		mode := api.ConnectionRedirectModePaste
		if !opts.noBrowser && c.term.openBrowser != nil {
			l, err := listenLoopback()
			if err != nil {
				return c.notConnected(conn, err)
			}
			defer l.close()
			listener, mode = l, api.ConnectionRedirectModeLoopback
			body.LoopbackRedirectUri = &l.uri
		}
		body.Mode = &mode
	}
	start, err := c.client.startAuthorization(ctx, c.workspace, conn.Name, body)
	if err != nil {
		return c.notConnected(conn, err)
	}

	// From here an interrupt ends the wait instead of the process, so that
	// every way out that leaves the session unconfirmed cancels it, and the
	// server discards the consent's tokens at once.
	sigCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	var in *signedIn
	status, err := c.awaitSession(sigCtx, conn, start, listener, opts)
	if err == nil {
		in, err = c.settle(sigCtx, conn, start.Session, status)
	}
	switch {
	case err != nil && in != nil:
		return err
	case err != nil:
		if sigCtx.Err() != nil && ctx.Err() == nil {
			err = errSignInCancelled
		}
		return c.unsettled(conn, start, err)
	}
	stop()
	return c.report(ctx, in, app)
}

// unsettled cancels the session of a sign-in that ended with err before
// its tokens went live, and adds to err what became of conn. Only a
// session that the cancel ended, or that the server failed, is known to
// have left conn's previous tokens in place: a session that had ended
// otherwise may have been confirmed.
func (c *connectionCLI) unsettled(conn *api.Connection, start *api.ConnectionAuthorizationStart, err error) error {
	cancelErr := c.cancelSession(conn.Name, start.Session)
	switch {
	case cancelErr == nil, isSessionFailed(err):
		return c.notConnected(conn, err)
	case isRemoteStatus(cancelErr, http.StatusConflict, http.StatusNotFound):
		return fmt.Errorf("%w\nThe sign-in may have completed. Check connection %s with '%s'", err, conn.Name, c.command("test", conn.Name))
	default:
		return fmt.Errorf("%w\nThe session could not be cancelled (%w). The server discards its unconfirmed tokens when it expires at %s",
			err, cancelErr, start.ExpiresAt.Local().Format(connectionTimeLayout))
	}
}

// isRemoteStatus reports whether err is a server's answer with one of
// statuses.
func isRemoteStatus(err error, statuses ...int) bool {
	rerr, ok := errors.AsType[*remoteError](err)
	return ok && slices.Contains(statuses, rerr.StatusCode)
}

// awaitSession shows what to set up before consent and how to sign in, and
// waits until the session no longer waits for consent.
func (c *connectionCLI) awaitSession(ctx context.Context, conn *api.Connection, start *api.ConnectionAuthorizationStart, listener *loopbackListener, opts connectOptions) (*api.ConnectionAuthorization, error) {
	paste := start.Mode == api.ConnectionRedirectModePaste
	var redirects chan consentRedirect
	if listener != nil {
		// Before the browser opens, since a signed-in browser may redirect
		// at once.
		listener.expect(stateOf(start.AuthorizationUrl))
		redirects = listener.redirects
	}
	c.showBeforeConsent(ctx, conn, start, paste, !paste && !opts.noBrowser)
	if !paste {
		c.term.printf("Waiting for consent (Ctrl-C to cancel)...\n")
		return c.awaitConsent(ctx, conn.Name, start, redirects)
	}
	pasted, err := c.readPasted(ctx, start.ExpiresAt)
	if err != nil {
		return nil, err
	}
	redirects = make(chan consentRedirect, 1)
	redirects <- consentRedirect{url: pasted, done: func(error) {}}
	return c.awaitConsent(ctx, conn.Name, start, redirects)
}

// showBeforeConsent prints what must be set up before consent, since the
// provider shows a mismatched redirect URI or a blocked client on its own
// page, and then the sign-in URL.
func (c *connectionCLI) showBeforeConsent(ctx context.Context, conn *api.Connection, start *api.ConnectionAuthorizationStart, paste, open bool) {
	c.term.printf("Register this redirect URI in the client: %s\n", start.RedirectUri)
	trust := "the admin trusts it under API controls"
	if links := c.setupLinks(ctx, conn.Provider); links != nil && links.AdminTrust != nil {
		trust += ": " + *links.AdminTrust
	}
	c.term.printf("Client ID: %s\n  If a Workspace admin's policy blocks this client, %s\n", start.ClientId, trust)

	opened := false
	if open && c.term.openBrowser != nil && safeBrowserURL(start.AuthorizationUrl) {
		opened = c.term.openBrowser(start.AuthorizationUrl) == nil
	}
	switch {
	case paste:
		c.term.printf("Open this URL in a browser and sign in:\n%s\n", start.AuthorizationUrl)
		c.term.printf("The browser then fails to load a page at %s. Copy that page's address and paste it here (it is not shown): ",
			strings.TrimSuffix(start.RedirectUri, loopbackCallbackPath))
	case opened:
		c.term.printf("Opening the sign-in page in your browser. If it does not open, visit:\n%s\n", start.AuthorizationUrl)
	default:
		c.term.printf("Open this URL to sign in:\n%s\n", start.AuthorizationUrl)
	}
	if start.Mode == api.ConnectionRedirectModeLoopback {
		// The redirect reaches only a browser on this host.
		c.term.printf("Sign in from a browser on this host. To sign in from another one, cancel and run '%s --no-browser'.\n",
			c.command("reconnect", conn.Name))
	}
}

// setupLinks returns the provider's setup pages, or nil when the server
// does not list them.
func (c *connectionCLI) setupLinks(ctx context.Context, provider api.ConnectionProviderName) *api.ConnectionProviderSetupLinks {
	resp, err := c.client.connectionProviders(ctx)
	if err != nil {
		return nil
	}
	for _, p := range resp.Providers {
		if p.Name == provider {
			return p.SetupLinks
		}
	}
	return nil
}

// safeBrowserURL reports whether raw is a web URL, so that a server never
// makes the CLI open another kind of link.
func safeBrowserURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != ""
}

// stateOf returns the state parameter of an authorization URL.
func stateOf(authorizationURL string) string {
	u, err := url.Parse(authorizationURL)
	if err != nil {
		return ""
	}
	return u.Query().Get("state")
}

// readPasted reads the redirect URL that the person pastes, asking again
// after an empty line. It stops waiting when the session expires.
func (c *connectionCLI) readPasted(ctx context.Context, expiresAt time.Time) (string, error) {
	readCtx, cancel := context.WithDeadline(ctx, expiresAt)
	defer cancel()
	for {
		pasted, err := c.term.readSecret(readCtx)
		switch {
		case err == nil, ctx.Err() != nil, errors.Is(err, errSignInCancelled):
		case readCtx.Err() != nil:
			err = sessionFailed{errors.New("the session expired before the address was pasted")}
		default:
			err = fmt.Errorf("%w: %w", errSignInCancelled, err)
		}
		if err != nil {
			return "", err
		}
		if pasted != "" {
			return pasted, nil
		}
		c.term.printf("Paste the address of the page the browser landed on: ")
	}
}

// awaitConsent waits until the session no longer waits for consent, and
// completes it from each redirect the CLI receives. It reads the session
// once a second, and keeps waiting through failures that may pass until
// the session expires.
func (c *connectionCLI) awaitConsent(ctx context.Context, name string, start *api.ConnectionAuthorizationStart, redirects <-chan consentRedirect) (*api.ConnectionAuthorization, error) {
	ticker := time.NewTicker(connectPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case r := <-redirects:
			err := c.client.completeAuthorization(ctx, c.workspace, name, r.url)
			r.done(err)
			if err != nil && !transient(err) && ctx.Err() == nil {
				// The server refused the redirect, or failed the session
				// with the provider's error.
				return nil, sessionFailed{err}
			}
		case <-ticker.C:
		}
		status, err := c.client.authorization(ctx, c.workspace, name, start.Session)
		switch {
		case err == nil && status.Phase != api.ConnectionAuthorizationPhaseWaiting:
			return status, nil
		case err == nil:
		case ctx.Err() != nil:
			return nil, ctx.Err()
		case transient(err) && time.Now().Before(start.ExpiresAt):
		default:
			return nil, err
		}
	}
}

// transient reports an error that a later request may not repeat: a
// failed connection, a timeout, or a server error.
func transient(err error) bool {
	if rerr, ok := errors.AsType[*remoteError](err); ok {
		return rerr.StatusCode >= http.StatusInternalServerError
	}
	_, ok := errors.AsType[net.Error](err)
	return ok
}

// settle confirms the account of a session in confirm, or reads the
// connection that the expected account confirmed at once. It returns nil
// while the session's tokens are not live.
func (c *connectionCLI) settle(ctx context.Context, conn *api.Connection, session string, status *api.ConnectionAuthorization) (*signedIn, error) {
	switch status.Phase {
	case api.ConnectionAuthorizationPhaseConfirm:
		account := derefString(status.Account)
		ok, err := c.term.confirm(ctx, fmt.Sprintf("Signed in as %s. Is this the right account?", account), true)
		if err == nil && !ok {
			err = fmt.Errorf("%s was not confirmed", account)
		}
		if err != nil {
			return nil, err
		}
		for _, dup := range derefStringSlice(status.Duplicates) {
			c.term.printf("Warning: %s in %s already holds %s on this client.\n", dup, conn.Workspace, account)
		}
		live, err := c.client.confirmAuthorization(ctx, c.workspace, conn.Name, session)
		if err != nil {
			return nil, err
		}
		return &signedIn{
			conn:                  live,
			probes:                derefProbes(status.Probes),
			refreshTokenExpiresAt: status.RefreshTokenExpiresAt,
			refreshTokenFix:       derefString(status.RefreshTokenFix),
		}, nil
	case api.ConnectionAuthorizationPhaseConnected:
		// A session that the expected account confirmed at once has no
		// checks of its own, so its results come from a test, and its
		// duplicates warning is not shown (Spec 107 §Confirmation).
		in := &signedIn{conn: conn}
		live, err := c.client.connection(ctx, c.workspace, conn.Name)
		if err != nil {
			return in, fmt.Errorf("connection %s is connected, but reading it failed: %w", conn.Name, err)
		}
		in.conn, in.refreshTokenExpiresAt = live, live.RefreshTokenExpiresAt
		c.term.printf("Signed in as %s, the expected account.\n", derefString(live.Account))
		if in.probes, err = c.client.testConnection(ctx, c.workspace, conn.Name); err != nil {
			return in, fmt.Errorf("connection %s is connected, but its test failed: %w", conn.Name, err)
		}
		return in, nil
	case api.ConnectionAuthorizationPhaseFailed:
		return nil, sessionFailed{fmt.Errorf("sign-in failed: %s", derefString(status.Reason))}
	case api.ConnectionAuthorizationPhaseWaiting:
		return nil, errors.New("the session still waits for consent")
	default:
		return nil, fmt.Errorf("the session is in an unknown phase %q", status.Phase)
	}
}

// report prints each service's result and the refresh token's limit.
func (c *connectionCLI) report(ctx context.Context, in *signedIn, app *api.OAuthApp) error {
	name := in.conn.Name
	probeErr := reportProbes(c.term.out, in.conn.Services, in.probes, c.toolCounts(ctx, name, app))
	if when := formatConnectionTime(in.refreshTokenExpiresAt); when != "" {
		fix := in.refreshTokenFix
		if fix == "" {
			fix = "Reconnect before then."
		}
		c.term.printf("The refresh token expires %s. %s\n", when, fix)
	}
	if probeErr != nil {
		return fmt.Errorf("connection %s is connected, but %w; fix them and run '%s'", name, probeErr, c.command("test", name))
	}
	c.term.printf("Connection %s is ready.\n", name)
	return nil
}

// toolCounts returns the number of MCP tools of each service, which the
// checks after consent listed for an app with the agent flag.
func (c *connectionCLI) toolCounts(ctx context.Context, name string, app *api.OAuthApp) map[string]int {
	if !app.Agent {
		return nil
	}
	resp, err := c.client.connectionTools(ctx, c.workspace, name, "")
	if err != nil {
		return nil
	}
	counts := make(map[string]int, len(resp.Servers))
	for _, s := range resp.Servers {
		if s.Error == nil {
			counts[s.Server] = len(s.Tools)
		}
	}
	return counts
}

// notConnected adds to err what became of conn, which keeps the tokens it
// had before the session.
func (c *connectionCLI) notConnected(conn *api.Connection, err error) error {
	if conn.Status == api.ConnectionStatusPending {
		return fmt.Errorf("%w\nConnection %s stays pending. Sign in again with '%s', or remove it with '%s'",
			err, conn.Name, c.command("reconnect", conn.Name), c.command("remove", conn.Name))
	}
	return fmt.Errorf("%w\nConnection %s keeps its previous tokens", err, conn.Name)
}

// cancelSession cancels a session that the CLI leaves unconfirmed, so its
// tokens are discarded at once rather than when it expires. The server
// refuses to cancel a session that already ended, which changes nothing.
func (c *connectionCLI) cancelSession(name, session string) error {
	ctx, cancel := context.WithTimeout(context.Background(), cancelTimeout)
	defer cancel()
	return c.client.cancelAuthorization(ctx, c.workspace, name, session)
}

func derefProbes(v *[]api.ConnectionProbe) []api.ConnectionProbe {
	if v == nil {
		return nil
	}
	return *v
}

// loopbackListener receives the provider's redirect on 127.0.0.1, where
// the browser that consented runs.
type loopbackListener struct {
	uri       string
	redirects chan consentRedirect
	server    *http.Server

	mu sync.Mutex
	// state is the session's state parameter. A request without it is not
	// this sign-in's redirect.
	state    string
	accepted bool
}

func listenLoopback() (*loopbackListener, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("failed to listen on 127.0.0.1 for the sign-in redirect: %w", err)
	}
	l := &loopbackListener{
		uri:       "http://" + ln.Addr().String() + loopbackCallbackPath,
		redirects: make(chan consentRedirect, 1),
	}
	l.server = &http.Server{Handler: http.HandlerFunc(l.serve), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = l.server.Serve(ln) }()
	return l, nil
}

// expect sets the state parameter of the session whose redirect the
// listener waits for.
func (l *loopbackListener) expect(state string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.state = state
}

// serve hands the session's redirect to the connect flow once, and shows
// the browser whether the session completed. It refuses any other request
// and keeps listening.
func (l *loopbackListener) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != loopbackCallbackPath {
		http.NotFound(w, r)
		return
	}
	l.mu.Lock()
	ours := l.state != "" && r.URL.Query().Get("state") == l.state
	first := ours && !l.accepted
	l.accepted = l.accepted || ours
	l.mu.Unlock()
	switch {
	case !ours:
		http.Error(w, "This address is not the redirect of the sign-in in progress.", http.StatusBadRequest)
		return
	case !first:
		http.Error(w, "This sign-in was already received. Return to the terminal.", http.StatusConflict)
		return
	}
	result := make(chan error, 1)
	l.redirects <- consentRedirect{
		url:  strings.TrimSuffix(l.uri, loopbackCallbackPath) + r.URL.RequestURI(),
		done: func(err error) { result <- err },
	}
	select {
	case err := <-result:
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprintln(w, "Dagu could not complete the sign-in. The terminal shows why.")
			return
		}
		_, _ = fmt.Fprintln(w, "Signed in. Return to the terminal to confirm the account; this tab can be closed.")
	case <-r.Context().Done():
	}
}

// close stops the listener, and drops a request still in flight after a
// second.
func (l *loopbackListener) close() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := l.server.Shutdown(ctx); err != nil {
		_ = l.server.Close()
	}
}

// systemBrowser returns a function that opens a URL in this host's
// browser, or nil when no browser can open here: over SSH, or on a Unix
// host without a display.
func systemBrowser() func(string) error {
	if os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_TTY") != "" {
		return nil
	}
	var name string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		name = "open"
	case "windows":
		name, args = "rundll32", []string{"url.dll,FileProtocolHandler"}
	default:
		if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
			return nil
		}
		name = "xdg-open"
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return nil
	}
	return func(u string) error {
		cmd := exec.Command(path, append(args, u)...) //nolint:gosec // u is checked by safeBrowserURL
		if err := cmd.Start(); err != nil {
			return err
		}
		// A launcher such as xdg-open may wait for the browser to exit, so
		// its exit is not awaited. The sign-in URL is printed either way.
		go func() { _ = cmd.Wait() }()
		return nil
	}
}
