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
	"strings"
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
	// cancelTimeout bounds the request that cancels a session after the
	// wait for it was interrupted.
	cancelTimeout = 10 * time.Second
)

// connectOptions decide how a connect session runs.
type connectOptions struct {
	// noBrowser prints the sign-in URL without opening a browser.
	noBrowser bool
	// expectedAccount is the email address expected to sign in. A session
	// that the provider vouches for with it is confirmed without asking.
	expectedAccount string
}

// consentRedirect is the provider's redirect after consent, as the browser
// landed on it.
type consentRedirect struct {
	url string
	// done reports the result of completing the session to the browser
	// that sent the redirect.
	done func(error)
}

// errSignInCancelled reports that the person stopped the sign-in.
var errSignInCancelled = errors.New("sign-in cancelled")

// connect runs the connect flow on conn, an app's connection, from start
// to confirm, and prints each service's result.
func (c *connectionCLI) connect(ctx context.Context, conn *api.Connection, app *api.OAuthApp, opts connectOptions) error {
	mode, err := redirectMode(app.ClientType, !opts.noBrowser && c.term.openBrowser != nil)
	if err != nil {
		return err
	}
	body := api.StartConnectionAuthorizationRequest{Mode: &mode}
	if opts.expectedAccount != "" {
		body.ExpectedAccount = &opts.expectedAccount
	}
	var redirects chan consentRedirect
	if mode == api.ConnectionRedirectModeLoopback {
		listener, err := listenLoopback()
		if err != nil {
			return err
		}
		defer listener.close()
		body.LoopbackRedirectUri = &listener.uri
		redirects = listener.redirects
	}
	start, err := c.client.startAuthorization(ctx, c.workspace, conn.Name, body)
	if err != nil {
		return err
	}
	paste := mode == api.ConnectionRedirectModePaste
	c.showBeforeConsent(ctx, conn, start, paste, !paste && !opts.noBrowser)

	var status *api.ConnectionAuthorization
	if paste {
		status, err = c.completePasted(ctx, conn.Name, start.Session)
	} else {
		status, err = c.awaitUntilInterrupted(ctx, conn.Name, start.Session, redirects)
	}
	if errors.Is(err, errSignInCancelled) {
		c.cancelSession(conn.Name, start.Session)
	}
	if err != nil {
		return c.notConnected(conn, err)
	}
	return c.finishConnect(ctx, conn, app, start.Session, status)
}

// redirectMode picks the first of server callback, loopback, and paste that
// works for the app's client type, as the UI does. Loopback works only
// when a browser on this host opens the sign-in page.
func redirectMode(clientType api.OAuthClientType, canOpenBrowser bool) (api.ConnectionRedirectMode, error) {
	switch clientType {
	case api.OAuthClientTypeWeb:
		return api.ConnectionRedirectModeServerCallback, nil
	case api.OAuthClientTypeDesktop:
		if canOpenBrowser {
			return api.ConnectionRedirectModeLoopback, nil
		}
		return api.ConnectionRedirectModePaste, nil
	default:
		return "", fmt.Errorf("a %s client cannot sign in an account; use a web or desktop client", clientType)
	}
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

// completePasted completes the session from the redirect URL that the
// person pastes.
func (c *connectionCLI) completePasted(ctx context.Context, name, session string) (*api.ConnectionAuthorization, error) {
	pasted, err := c.term.readSecret()
	if err != nil && !errors.Is(err, errSignInCancelled) {
		err = fmt.Errorf("%w: %w", errSignInCancelled, err)
	}
	if err != nil {
		return nil, err
	}
	if err := c.client.completeAuthorization(ctx, c.workspace, name, pasted); err != nil {
		return nil, err
	}
	return c.client.authorization(ctx, c.workspace, name, session)
}

// awaitUntilInterrupted waits for consent until Ctrl-C.
func (c *connectionCLI) awaitUntilInterrupted(ctx context.Context, name, session string, redirects <-chan consentRedirect) (*api.ConnectionAuthorization, error) {
	c.term.printf("Waiting for consent (Ctrl-C to cancel)...\n")
	waitCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	status, err := c.awaitConsent(waitCtx, name, session, redirects)
	if err != nil && waitCtx.Err() != nil && ctx.Err() == nil {
		return nil, errSignInCancelled
	}
	return status, err
}

// awaitConsent waits until the session no longer waits for consent, and
// completes it from a redirect that the CLI receives.
func (c *connectionCLI) awaitConsent(ctx context.Context, name, session string, redirects <-chan consentRedirect) (*api.ConnectionAuthorization, error) {
	ticker := time.NewTicker(connectPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case r := <-redirects:
			err := c.client.completeAuthorization(ctx, c.workspace, name, r.url)
			r.done(err)
			if err != nil {
				return nil, err
			}
			return c.client.authorization(ctx, c.workspace, name, session)
		case <-ticker.C:
			status, err := c.client.authorization(ctx, c.workspace, name, session)
			if err != nil {
				return nil, err
			}
			if status.Phase != api.ConnectionAuthorizationPhaseWaiting {
				return status, nil
			}
		}
	}
}

// finishConnect confirms the account of a session in confirm, and prints
// each service's result.
func (c *connectionCLI) finishConnect(ctx context.Context, conn *api.Connection, app *api.OAuthApp, session string, status *api.ConnectionAuthorization) error {
	var probes []api.ConnectionProbe
	switch status.Phase {
	case api.ConnectionAuthorizationPhaseConfirm:
		account := derefString(status.Account)
		ok, err := c.term.confirm(fmt.Sprintf("Signed in as %s. Is this the right account?", account), true)
		if err == nil && !ok {
			err = fmt.Errorf("%s was not confirmed", account)
		}
		if err != nil {
			c.cancelSession(conn.Name, session)
			return c.notConnected(conn, err)
		}
		for _, dup := range derefStringSlice(status.Duplicates) {
			c.term.printf("Warning: %s in %s already holds %s on this client.\n", dup, conn.Workspace, account)
		}
		confirmed, err := c.client.confirmAuthorization(ctx, c.workspace, conn.Name, session)
		if err != nil {
			return err
		}
		conn = confirmed
		probes = derefProbes(status.Probes)
	case api.ConnectionAuthorizationPhaseConnected:
		// A session that the expected account confirmed at once has no
		// checks of its own, so its results come from a test, and its
		// duplicates warning is not shown (Spec 107 §Confirmation).
		current, err := c.client.connection(ctx, c.workspace, conn.Name)
		if err != nil {
			return err
		}
		conn = current
		c.term.printf("Signed in as %s, the expected account.\n", derefString(conn.Account))
		if probes, err = c.client.testConnection(ctx, c.workspace, conn.Name); err != nil {
			return fmt.Errorf("connection %s is connected, but its test failed: %w", conn.Name, err)
		}
	default:
		return c.notConnected(conn, fmt.Errorf("sign-in failed: %s", derefString(status.Reason)))
	}

	probeErr := reportProbes(c.term.out, probes, c.toolCounts(ctx, conn.Name, app))
	if when := formatConnectionTime(status.RefreshTokenExpiresAt); when != "" {
		c.term.printf("The refresh token expires %s. %s\n", when, derefString(status.RefreshTokenFix))
	}
	if probeErr != nil {
		return fmt.Errorf("connection %s is connected, but %w; fix them and run 'dagu connection test %s'", conn.Name, probeErr, conn.Name)
	}
	c.term.printf("Connection %s is ready.\n", conn.Name)
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
		return fmt.Errorf("%w\nConnection %s stays pending. Sign in again with 'dagu connection reconnect %s', or remove it with 'dagu connection remove %s'",
			err, conn.Name, conn.Name, conn.Name)
	}
	return fmt.Errorf("%w\nConnection %s keeps its previous tokens", err, conn.Name)
}

// cancelSession cancels a session that the CLI stopped waiting for, so its
// tokens are discarded at once rather than when it expires.
func (c *connectionCLI) cancelSession(name, session string) {
	ctx, cancel := context.WithTimeout(context.Background(), cancelTimeout)
	defer cancel()
	_ = c.client.cancelAuthorization(ctx, c.workspace, name, session)
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

// serve hands the first redirect to the connect flow, and shows the
// browser whether the session completed.
func (l *loopbackListener) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != loopbackCallbackPath {
		http.NotFound(w, r)
		return
	}
	result := make(chan error, 1)
	redirect := consentRedirect{
		url:  strings.TrimSuffix(l.uri, loopbackCallbackPath) + r.URL.RequestURI(),
		done: func(err error) { result <- err },
	}
	select {
	case l.redirects <- redirect:
	default:
		http.Error(w, "This sign-in was already received. Return to the terminal.", http.StatusConflict)
		return
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

func (l *loopbackListener) close() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = l.server.Shutdown(ctx)
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
		go func() { _ = cmd.Wait() }()
		return nil
	}
}
