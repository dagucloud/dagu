// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package certproxy

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newForwardProxy is an HTTP proxy that sends every plain request to the
// server at target and records the URLs requested.
func newForwardProxy(t *testing.T, target string) *tunnelProxy {
	t.Helper()
	p := &tunnelProxy{}
	forward := httputil.NewSingleHostReverseProxy(&url.URL{Scheme: "http", Host: target})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		p.requested = append(p.requested, r.URL.String())
		p.mu.Unlock()
		forward.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	p.addr = server.Listener.Addr().String()
	return p
}

// While the browser sends every request to the proxy, a plain HTTP request
// follows the device's proxy settings, except to a loopback destination,
// as it would from the browser.
func TestForwardHTTP(t *testing.T) {
	t.Parallel()
	ca := newTestCA(t, "corp CA")
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, "plain %s", r.URL.RequestURI())
		if sent, _ := io.ReadAll(r.Body); len(sent) > 0 {
			_, _ = fmt.Fprintf(w, " %s", sent)
		}
	}))
	t.Cleanup(site.Close)
	// refusingSOCKS answers every request that it cannot reach the site.
	refusingSOCKS := newSOCKSProxy(t, strings.TrimPrefix(closedURL, "http://"))
	socks := newSOCKSProxy(t, siteAddr(site))

	tests := []struct {
		name string
		// routes is the PAC result, with %s for the forward proxy.
		routes string
		url    string
		// send is the body of a POST; a request without one is a GET.
		send    string
		body    string
		proxied bool
	}{
		{name: "ThroughProxy", routes: "PROXY %s", url: "http://" + siteHost + "/page", body: "plain /page", proxied: true},
		{name: "Fallback", routes: "PROXY 127.0.0.1:1; PROXY %s", url: "http://" + siteHost + "/page", body: "plain /page", proxied: true},
		{name: "FallbackAfterSOCKS", routes: "SOCKS5 " + refusingSOCKS.addr + "; PROXY %s", url: "http://" + siteHost + "/page", body: "plain /page", proxied: true},
		// SOCKS4 takes an address, which the SOCKS proxy relays to the site.
		{name: "ThroughSOCKS4", routes: "SOCKS " + socks.addr + "; PROXY %s", url: "http://192.0.2.1/page", body: "plain /page"},
		{name: "FallbackWithBody", routes: "PROXY 127.0.0.1:1; PROXY %s", url: "http://" + siteHost + "/form", send: "a=1", body: "plain /form a=1", proxied: true},
		{name: "Loopback", routes: "PROXY %s", url: site.URL + "/page?a=1;b=2", body: "plain /page?a=1;b=2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			upstream := newForwardProxy(t, siteAddr(site))
			pac := fmt.Sprintf(`function FindProxyForURL() { return "`+tt.routes+`"; }`, upstream.addr)
			ready := serveProxy(t, testConfig(t, pac, ca.clientEntry(t, testLabel, "127.0.0.1")))

			request, err := http.NewRequest(http.MethodGet, tt.url, nil)
			if tt.send != "" {
				request, err = http.NewRequest(http.MethodPost, tt.url, strings.NewReader(tt.send))
			}
			require.NoError(t, err)
			response, err := plainClient(ready).Do(request)
			require.NoError(t, err)
			defer func() { _ = response.Body.Close() }()
			body, err := io.ReadAll(response.Body)
			require.NoError(t, err)
			assert.Equal(t, http.StatusOK, response.StatusCode)
			assert.Equal(t, tt.body, string(body))
			if tt.proxied {
				assert.Equal(t, []string{tt.url}, upstream.requests())
			} else {
				assert.Empty(t, upstream.requests())
			}
		})
	}

	t.Run("ProxyAuth", func(t *testing.T) {
		t.Parallel()
		authProxy := newTunnelProxy(t, siteAddr(site), http.StatusProxyAuthRequired)
		pac := `function FindProxyForURL() { return "PROXY ` + authProxy.addr + `"; }`
		cfg := testConfig(t, pac, ca.clientEntry(t, testLabel, "127.0.0.1"))
		ready := serveProxy(t, cfg)

		response, err := plainClient(ready).Get("http://" + siteHost + "/")
		require.NoError(t, err)
		_ = response.Body.Close()
		assert.Equal(t, http.StatusBadGateway, response.StatusCode)
		event := waitForEvent(t, ready, cfg.Token, EventProxyAuth)
		assert.Contains(t, event.Message, "could not reach "+siteHost)
	})
}

// A tunnel that does not carry TLS, as a ws: URL opens, takes the route of
// its ws: URL, path and query included, however long, and is relayed as it
// is, for a host an entry
// names, one the guard carries, and one neither does. One that carries TLS
// keeps the route of an https: URL.
func TestCarryAllRoutesTunnelByScheme(t *testing.T) {
	t.Parallel()
	ca := newTestCA(t, "corp CA")
	// schemePAC sends the ws: URLs of /socket to plain and the others to
	// secure.
	schemePAC := func(plain, secure *tunnelProxy) string {
		return `function FindProxyForURL(url) { return shExpMatch(url, "ws://*/socket?*") ? "PROXY ` + plain.addr + `" : "PROXY ` + secure.addr + `"; }`
	}
	// The query outgrows what the proxy reads from a connection at once.
	request := "GET /socket?pad=" + strings.Repeat("a", 8<<10) + " HTTP/1.1\r\nHost: " + siteHost + "\r\n\r\n"
	guarded := func(t *testing.T, pac string) Config {
		cfg := guardConfig(t, ca, pac)
		cfg.TrustedCAs = []TrustedCA{trustedCA(ca, "127.0.0.1")}
		return cfg
	}

	for name, config := range map[string]func(t *testing.T, pac string) Config{
		"Named": func(t *testing.T, pac string) Config {
			return testConfig(t, pac, ca.clientEntry(t, testLabel, siteHost, "127.0.0.1"))
		},
		"Guarded": guarded,
		"Unguarded": func(t *testing.T, pac string) Config {
			return testConfig(t, pac, ca.clientEntry(t, testLabel, "127.0.0.1"))
		},
	} {
		t.Run("Plain"+name, func(t *testing.T) {
			t.Parallel()
			plain := newTunnelProxy(t, echoServer(t), http.StatusOK)
			secure := newTunnelProxy(t, echoServer(t), http.StatusOK)
			ready := serveProxy(t, config(t, schemePAC(plain, secure)))
			require.True(t, ready.Manual)
			conn, status, err := connectThrough(fmt.Sprintf("127.0.0.1:%d", ready.Port), siteHost+":80")
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, status)
			t.Cleanup(func() { _ = conn.Close() })
			_, err = io.WriteString(conn, request)
			require.NoError(t, err)
			greeting, err := io.ReadAll(conn)
			require.NoError(t, err)
			assert.Equal(t, "hello", string(greeting))
			assert.Equal(t, []string{siteHost + ":80"}, plain.requests())
			assert.Empty(t, secure.requests())
		})
	}

	t.Run("TLS", func(t *testing.T) {
		t.Parallel()
		site := newSiteAuth(t, ca, ca, 0, 0)
		plain := newTunnelProxy(t, echoServer(t), http.StatusOK)
		secure := newTunnelProxy(t, siteAddr(site), http.StatusOK)
		ready := serveProxy(t, guarded(t, schemePAC(plain, secure)))
		body, err := get(t, browserClient(ready, "http/1.1"), "https://"+siteHost+"/")
		require.NoError(t, err)
		assert.Equal(t, "HTTP/1.1 anonymous", body)
		assert.Contains(t, secure.requests(), siteHost+":443")
		assert.Empty(t, plain.requests())
	})
}

// plainClient sends plain HTTP requests through the proxy, as the browser
// does with a manual proxy.
func plainClient(ready Ready) *http.Client {
	proxyURL := &url.URL{Scheme: "http", Host: fmt.Sprintf("127.0.0.1:%d", ready.Port)}
	return &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
}
