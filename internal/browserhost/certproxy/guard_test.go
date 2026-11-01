// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package certproxy

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// guardConfig returns a configuration with a guard, whose only entry names
// a host the tests never reach and trusts ca for the sites they reach.
func guardConfig(t *testing.T, ca *testCA, systemPAC string) Config {
	t.Helper()
	entry := ca.clientEntry(t, testLabel, "cert.example")
	entry.CAPEM = ca.pem()
	cfg := testConfig(t, systemPAC, entry)
	cfg.Guard = true
	return cfg
}

// A site the guard carries that requires a client certificate fails, and
// the proxy says none is configured for it.
func TestServeGuardReportsMissingCertificate(t *testing.T) {
	t.Parallel()

	for name, maxVersion := range map[string]uint16{"TLS13": 0, "TLS12": tls.VersionTLS12} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ca := newTestCA(t, "corp CA")
			site := newSite(t, ca, ca, maxVersion)
			cfg := guardConfig(t, ca, "")
			ready := serveProxy(t, cfg)
			require.NotZero(t, ready.GuardPort)

			_, err := get(t, browserClientAt(ready.GuardPort, ready, "http/1.1"), site.URL)
			require.Error(t, err)
			event := waitForEvent(t, ready, cfg.Token, EventClientCertificateMissing)
			assert.Equal(t, "127.0.0.1", event.Host)
			assert.Equal(t, "127.0.0.1 asks for a TLS client certificate, and none is configured for it", event.Message)
		})
	}
}

// A site that asks for a client certificate but opens without one opens
// through the guard, which still reports the request.
func TestServeGuardCarriesOptionalCertificateSite(t *testing.T) {
	t.Parallel()

	ca := newTestCA(t, "corp CA")
	site := newSiteAuth(t, ca, ca, tls.VerifyClientCertIfGiven, 0)
	cfg := guardConfig(t, ca, "")
	ready := serveProxy(t, cfg)

	body, err := get(t, browserClientAt(ready.GuardPort, ready, "h2", "http/1.1"), site.URL)
	require.NoError(t, err)
	assert.Equal(t, "HTTP/2.0 anonymous", body)
	waitForEvent(t, ready, cfg.Token, EventClientCertificateMissing)
}

// A site that asks for no client certificate, under an authority only an
// entry's ca_file names, opens through the guard's own TLS, with nothing to
// report.
func TestServeGuardCarriesOrdinarySite(t *testing.T) {
	t.Parallel()

	ca := newTestCA(t, "corp CA")
	site := newSiteAuth(t, ca, ca, tls.NoClientCert, 0)
	cfg := guardConfig(t, ca, "")
	ready := serveProxy(t, cfg)

	body, err := get(t, browserClientAt(ready.GuardPort, ready, "http/1.1"), site.URL)
	require.NoError(t, err)
	assert.Equal(t, "HTTP/1.1 anonymous", body)
	events, err := Events(context.Background(), controlURL(ready.Port, cfg.Token), 0)
	require.NoError(t, err)
	assert.Empty(t, events)
}

// The proxy vouches for the hosts whose server certificate it verified on a
// route it still carries: once the guard stops, the browser reaches the
// guard's hosts itself, while a named host keeps the certificate port.
func TestServeVerifiedHostsFollowRoutes(t *testing.T) {
	t.Parallel()

	ca := newTestCA(t, "corp CA")
	site := newSiteAuth(t, ca, ca, tls.NoClientCert, 0)
	upstream := newTunnelProxy(t, siteAddr(site), http.StatusOK)
	authProxy := newTunnelProxy(t, siteAddr(site), http.StatusProxyAuthRequired)
	cfg := guardConfig(t, ca, fmt.Sprintf(`function FindProxyForURL(url, host) { return host == "auth.test" ? "PROXY %s" : "PROXY %s"; }`, authProxy.addr, upstream.addr))
	cfg.TrustedCAs = []TrustedCA{trustedCA(ca, siteHost)}
	ready := serveProxy(t, cfg)
	handle := controlURL(ready.Port, cfg.Token)
	verified := func(host string) bool {
		t.Helper()
		ok, err := Verified(context.Background(), handle, host)
		require.NoError(t, err)
		return ok
	}

	_, err := get(t, browserClient(ready, "http/1.1"), "https://"+siteHost+"/")
	require.NoError(t, err)
	_, err = get(t, browserClientAt(ready.GuardPort, ready, "http/1.1"), site.URL)
	require.NoError(t, err)
	assert.True(t, verified(strings.ToUpper(siteHost)))
	assert.True(t, verified("127.0.0.1"))
	assert.False(t, verified("other.test"))

	conn, _, err := connectThrough(fmt.Sprintf("127.0.0.1:%d", ready.GuardPort), "auth.test:443")
	if err == nil {
		_ = conn.Close()
	}
	waitForEvent(t, ready, cfg.Token, EventGuardOff)
	assert.True(t, verified(siteHost))
	assert.False(t, verified("127.0.0.1"))
}

// A guard connection that completes after the guard stopped does not vouch
// for its host, which the browser reaches directly from then on.
func TestVerifiedHostsIgnoreGuardAfterStop(t *testing.T) {
	t.Parallel()

	verified := newVerifiedHosts()
	verified.add("named.test", false)
	verified.dropGuarded()
	verified.add("late.test", true)
	verified.add("late-named.test", false)
	assert.True(t, verified.has("named.test"))
	assert.False(t, verified.has("late.test"))
	assert.True(t, verified.has("late-named.test"))
}

// When the device's proxy asks for credentials, the guard resets the
// connection and stops, so the browser falls back to its own route, which
// can sign in to that proxy.
func TestServeGuardStopsForProxyCredentials(t *testing.T) {
	t.Parallel()

	ca := newTestCA(t, "corp CA")
	site := newSite(t, ca, ca, 0)
	authProxy := newTunnelProxy(t, siteAddr(site), http.StatusProxyAuthRequired)
	system := fmt.Sprintf(`function FindProxyForURL(url, host) { return "PROXY %s"; }`, authProxy.addr)
	cfg := guardConfig(t, ca, system)
	ready := serveProxy(t, cfg)
	guardAddr := fmt.Sprintf("127.0.0.1:%d", ready.GuardPort)

	conn, _, err := connectThrough(guardAddr, siteHost+":443")
	if err == nil {
		_ = conn.Close()
	}
	require.Error(t, err, "the guard answers nothing; it resets the connection")
	event := waitForEvent(t, ready, cfg.Token, EventGuardOff)
	assert.Contains(t, event.Message, "asks "+siteHost+" for credentials")
	require.Eventually(t, func() bool {
		conn, err := net.DialTimeout("tcp", guardAddr, time.Second)
		if err == nil {
			_ = conn.Close()
		}
		return err != nil
	}, 5*time.Second, 50*time.Millisecond, "the guard keeps accepting connections")

	// The certificate port keeps serving the entry's hosts.
	_, status, err := connectThrough(fmt.Sprintf("127.0.0.1:%d", ready.Port), "unnamed.example:443")
	require.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, status)
}

// A site the proxy cannot reach fails the tunnel request, which the
// browser shows as such, rather than its TLS.
func TestServeAnswersUnreachableSite(t *testing.T) {
	t.Parallel()

	ca := newTestCA(t, "corp CA")
	cfg := guardConfig(t, ca, "")
	ready := serveProxy(t, cfg)
	closed, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := closed.Addr().String()
	require.NoError(t, closed.Close())

	conn, status, err := connectThrough(fmt.Sprintf("127.0.0.1:%d", ready.GuardPort), addr)
	require.NoError(t, err)
	_ = conn.Close()
	assert.Equal(t, http.StatusBadGateway, status)
	event := waitForEvent(t, ready, cfg.Token, EventUpstream)
	assert.Contains(t, event.Message, "could not reach 127.0.0.1")
}

// The proxy lists the connections still waiting on a site, with how far
// they got.
func TestServeListsPendingConnections(t *testing.T) {
	t.Parallel()

	// A site that accepts connections and never answers.
	silent, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = silent.Close() })
	go func() {
		for {
			conn, err := silent.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { _ = conn.Close() })
		}
	}()
	ca := newTestCA(t, "corp CA")
	cfg := guardConfig(t, ca, "")
	ready := serveProxy(t, cfg)

	conn, status, err := connectThrough(fmt.Sprintf("127.0.0.1:%d", ready.GuardPort), silent.Addr().String())
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, status)
	t.Cleanup(func() { _ = conn.Close() })
	go func() {
		_ = tls.Client(conn, &tls.Config{ServerName: "silent.example", InsecureSkipVerify: true}).Handshake()
	}() //nolint:gosec // the handshake never completes

	var pending []Pending
	require.Eventually(t, func() bool {
		pending, err = PendingConnections(context.Background(), controlURL(ready.Port, cfg.Token))
		return err == nil && len(pending) == 1 && pending[0].Phase == PhaseHandshake
	}, 10*time.Second, 50*time.Millisecond, "pending: %v", pending)
	assert.Equal(t, "127.0.0.1", pending[0].Host)
}

// An HTTPS proxy on the way that asks for a client certificate gets the one
// whose hosts name it, and trusts that entry's CA.
func TestServePresentsCertificateToHTTPSProxy(t *testing.T) {
	t.Parallel()

	ca := newTestCA(t, "corp CA")
	site := newSiteAuth(t, ca, ca, tls.NoClientCert, 0)
	proxyAddr := newHTTPSTunnelProxy(t, ca, siteAddr(site))
	system := fmt.Sprintf(`function FindProxyForURL(url, host) { return "HTTPS %s"; }`, proxyAddr)
	entry := ca.clientEntry(t, testLabel, "127.0.0.1")
	entry.CAPEM = ca.pem()
	cfg := testConfig(t, system, entry)
	ready := serveProxy(t, cfg)

	body, err := get(t, browserClient(ready, "http/1.1"), site.URL)
	require.NoError(t, err)
	assert.Equal(t, "HTTP/1.1 anonymous", body)
}

// newHTTPSTunnelProxy starts an HTTPS proxy, with a certificate ca issued,
// that requires a client certificate ca issued and tunnels every CONNECT to
// target.
func newHTTPSTunnelProxy(t *testing.T, ca *testCA, target string) string {
	t.Helper()
	cert, key := ca.issue(t, certOptions{name: "proxy", ips: []net.IP{net.IPv4(127, 0, 0, 1)}})
	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{cert.Raw}, PrivateKey: key}},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    ca.pool(),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go tunnelOnce(conn, target)
		}
	}()
	return listener.Addr().String()
}

// tunnelOnce answers one CONNECT request on conn by relaying to target.
func tunnelOnce(conn net.Conn, target string) {
	defer func() { _ = conn.Close() }()
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil || !strings.HasPrefix(string(buf[:n]), http.MethodConnect) {
		return
	}
	upstream, err := net.Dial("tcp", target)
	if err != nil {
		return
	}
	defer func() { _ = upstream.Close() }()
	if _, err := conn.Write([]byte(connectEstablished)); err != nil {
		return
	}
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(upstream, conn)
		close(done)
	}()
	_, _ = io.Copy(conn, upstream)
	<-done
}
