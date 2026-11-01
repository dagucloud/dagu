// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package certproxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
	"sync/atomic"

	"github.com/dagucloud/dagu/v2/internal/cmn/hostpattern"
)

// forwardingHeaders are the request headers httputil.ReverseProxy drops in
// its Rewrite mode, which a forward proxy passes on as the browser sent
// them.
var forwardingHeaders = []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto"}

// forwarder relays the plain HTTP requests the browser sends while the
// proxy carries every scheme, on the route the device's proxy settings
// give each URL.
type forwarder struct {
	s     *server
	proxy *httputil.ReverseProxy

	mu sync.Mutex
	// transports holds a transport per route, so connections are reused.
	transports map[route]*http.Transport
}

func newForwarder(s *server) *forwarder {
	f := &forwarder{s: s, transports: map[route]*http.Transport{}}
	f.proxy = &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			// The request names its site in absolute form already.
			r.Out.URL.RawQuery = r.In.URL.RawQuery
			for _, name := range forwardingHeaders {
				if values, ok := r.In.Header[name]; ok {
					r.Out.Header[name] = values
				}
			}
		},
		Transport:    f,
		ErrorLog:     log.New(io.Discard, "", 0),
		ErrorHandler: f.fail,
	}
	return f
}

func (f *forwarder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.proxy.ServeHTTP(w, r)
}

// RoundTrip sends req on the first of its routes that reaches the next hop.
func (f *forwarder) RoundTrip(req *http.Request) (*http.Response, error) {
	routes := f.s.routesTo(req.Context(), req.URL.String(), hostpattern.Canonical(req.URL.Hostname()), "")
	// A transport closes the body of a request it could not send, which
	// would leave nothing to send on the next route.
	body := &unsentBody{ReadCloser: req.Body}
	if req.Body != nil && req.Body != http.NoBody {
		req = req.Clone(req.Context())
		req.Body = body
	}
	var errs []error
	for _, r := range routes {
		response, err := f.transport(r).RoundTrip(req)
		if err == nil && r.kind != routeDirect && response.StatusCode == http.StatusProxyAuthRequired {
			_ = response.Body.Close()
			err = fmt.Errorf("the proxy %s %w", r.addr, errProxyAuth)
		}
		if err == nil {
			return response, nil
		}
		errs = append(errs, fmt.Errorf("through %s: %w", r, err))
		if !unreached(err) || body.read.Load() {
			break
		}
	}
	if len(errs) == 0 {
		return nil, errors.New("the device's proxy settings give it no route the certificate proxy supports")
	}
	return nil, errors.Join(errs...)
}

// unsentBody is a request body that stays open until the request starts
// sending it, so a request no route carried can take the next one. The
// server closes a body never read once the request ends.
type unsentBody struct {
	io.ReadCloser
	read atomic.Bool
}

func (b *unsentBody) Read(p []byte) (int, error) {
	b.read.Store(true)
	return b.ReadCloser.Read(p)
}

func (b *unsentBody) Close() error {
	if !b.read.Load() {
		return nil
	}
	return b.ReadCloser.Close()
}

// unreached reports whether err is the failure to connect to a request's
// next hop, before anything of the request was sent. A SOCKS proxy that
// cannot reach the site counts, as it does for Chrome.
func unreached(err error) bool {
	var opErr *net.OpError
	return errors.As(err, &opErr) && (opErr.Op == "dial" || opErr.Op == "proxyconnect" || opErr.Op == socksConnectOp)
}

// transport returns the transport that sends requests on r.
func (f *forwarder) transport(r route) *http.Transport {
	f.mu.Lock()
	defer f.mu.Unlock()
	if transport, ok := f.transports[r]; ok {
		return transport
	}
	dialer := &net.Dialer{Timeout: dialTimeout}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
			return dialDirect(ctx, dialer, addr)
		},
		TLSHandshakeTimeout: dialTimeout,
	}
	switch r.kind {
	case routeProxy:
		transport.Proxy = http.ProxyURL(&url.URL{Scheme: "http", Host: r.addr})
	case routeHTTPS:
		transport.Proxy = http.ProxyURL(&url.URL{Scheme: "https", Host: r.addr})
		host, _, _ := net.SplitHostPort(r.addr)
		transport.TLSClientConfig = f.s.proxyTLS(host)
	case routeSOCKS5:
		transport.Proxy = http.ProxyURL(&url.URL{Scheme: "socks5", Host: r.addr})
	case routeSOCKS4:
		transport.DialContext = func(ctx context.Context, _, addr string) (net.Conn, error) {
			return dialSOCKS4(ctx, dialer, r.addr, addr)
		}
	case routeDirect:
	}
	f.transports[r] = transport
	return transport
}

// fail answers a request the proxy could not relay and records why. A
// request the browser abandoned is not reported.
func (f *forwarder) fail(w http.ResponseWriter, r *http.Request, err error) {
	if !errors.Is(err, context.Canceled) {
		f.s.reportUnreachable(hostpattern.Canonical(r.URL.Hostname()), f.s.guardEntry, err)
	}
	http.Error(w, "the certificate proxy could not reach the site", http.StatusBadGateway)
}

// closeIdle closes the connections no request is using.
func (f *forwarder) closeIdle() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, transport := range f.transports {
		transport.CloseIdleConnections()
	}
}
