// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package connect

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/dagucloud/dagu/v2/internal/connection"
)

// RedirectMode is how a provider's redirect after consent reaches Dagu.
type RedirectMode string

const (
	// RedirectServerCallback redirects to this server's public callback.
	RedirectServerCallback RedirectMode = "server_callback"
	// RedirectLoopback redirects to a listener of the CLI on 127.0.0.1,
	// which sends the code to the server.
	RedirectLoopback RedirectMode = "loopback"
	// RedirectPaste redirects to a loopback URL that fails to load. The
	// user pastes that URL into the UI or the CLI.
	RedirectPaste RedirectMode = "paste"
)

const (
	// CallbackPath is the server callback's path under the public URL.
	CallbackPath = "/oauth/callback"
	// pasteRedirectURI is a loopback URL where nothing listens, so the
	// browser stays on it with the code and state in its address bar.
	pasteRedirectURI = "http://127.0.0.1:1/oauth/callback"
	loopbackHost     = "127.0.0.1"
)

// ErrRedirectUnavailable reports a redirect mode that does not work for the
// server or the app's client type.
var ErrRedirectUnavailable = errors.New("redirect mode is not available")

// RedirectOptions are what decides a session's redirect mode.
type RedirectOptions struct {
	// Mode replaces the automatic choice when set.
	Mode RedirectMode
	// PublicURL is the server's usable public URL, including its base
	// path, or empty when it has none.
	PublicURL string
	// LoopbackURI is the http://127.0.0.1 URL that the CLI listens on, or
	// empty when no listener runs.
	LoopbackURI string
}

// Redirect is a session's redirect mode and the exact redirect URI to
// register in the client.
type Redirect struct {
	Mode RedirectMode
	URI  string
}

// Offer is a redirect mode that a server offers to clients of one type.
type Offer struct {
	Mode       RedirectMode
	ClientType connection.ClientType
	// URI is the redirect URI to register in the client. It is empty for
	// loopback, whose URI is the CLI's listener on a port that it picks.
	URI string
}

// Offers returns the redirect modes that work for a server whose usable
// public URL is publicURL, in the order that ChooseRedirect tries them.
func Offers(publicURL string) []Offer {
	var offers []Offer
	web := &connection.App{ClientType: connection.ClientTypeWeb}
	if r, err := redirectFor(web, RedirectServerCallback, RedirectOptions{PublicURL: publicURL}); err == nil {
		offers = append(offers, Offer{Mode: r.Mode, ClientType: web.ClientType, URI: r.URI})
	}
	return append(offers,
		Offer{Mode: RedirectLoopback, ClientType: connection.ClientTypeDesktop},
		Offer{Mode: RedirectPaste, ClientType: connection.ClientTypeDesktop, URI: pasteRedirectURI},
	)
}

// ChooseRedirect returns the redirect for a session of app. Without a mode
// in opts, it picks the first of server callback, loopback, and paste that
// works for opts and the app's client type. A named mode that does not
// work fails with ErrRedirectUnavailable.
func ChooseRedirect(app *connection.App, opts RedirectOptions) (Redirect, error) {
	if opts.Mode != "" {
		return redirectFor(app, opts.Mode, opts)
	}
	for _, mode := range []RedirectMode{RedirectServerCallback, RedirectLoopback, RedirectPaste} {
		if r, err := redirectFor(app, mode, opts); err == nil {
			return r, nil
		}
	}
	return Redirect{}, fmt.Errorf("%w: no redirect mode works for a %s client", ErrRedirectUnavailable, app.ClientType)
}

func redirectFor(app *connection.App, mode RedirectMode, opts RedirectOptions) (Redirect, error) {
	unavailable := func(why string) (Redirect, error) {
		return Redirect{}, fmt.Errorf("%w: %s: %s", ErrRedirectUnavailable, mode, why)
	}
	switch mode {
	case RedirectServerCallback:
		if app.ClientType != connection.ClientTypeWeb {
			return unavailable("it needs a web client")
		}
		if opts.PublicURL == "" {
			return unavailable("the server has no public URL")
		}
		return Redirect{Mode: mode, URI: strings.TrimRight(opts.PublicURL, "/") + CallbackPath}, nil
	case RedirectLoopback:
		if app.ClientType != connection.ClientTypeDesktop {
			return unavailable("it needs a desktop client")
		}
		if !isLoopbackURI(opts.LoopbackURI) {
			return unavailable("it needs an http://127.0.0.1 URL with a port")
		}
		return Redirect{Mode: mode, URI: opts.LoopbackURI}, nil
	case RedirectPaste:
		if app.ClientType != connection.ClientTypeDesktop {
			return unavailable("it needs a desktop client")
		}
		return Redirect{Mode: mode, URI: pasteRedirectURI}, nil
	default:
		return unavailable("unknown mode")
	}
}

func isLoopbackURI(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	host, port, err := net.SplitHostPort(u.Host)
	if err != nil || host != loopbackHost {
		return false
	}
	n, err := strconv.ParseUint(port, 10, 16)
	return err == nil && n > 0
}
