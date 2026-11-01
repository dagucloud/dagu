// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package certproxy

import (
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestImplicitBypass(t *testing.T) {
	t.Parallel()
	for _, host := range []string{"localhost", "*.localhost", "portal.localhost", "127.0.0.1", "127.1.2.3", "::1", "::ffff:127.0.0.1", "169.254.1.1", "fe80::1"} {
		assert.Truef(t, implicitBypass(host), host)
	}
	for _, host := range []string{"*", "portal.example.com", "10.0.0.1", "*.example.com", "192.168.1.1"} {
		assert.Falsef(t, implicitBypass(host), host)
	}
	// Chrome leaves the name loopback out of PAC scripts only on Windows.
	assert.Equal(t, runtime.GOOS == "windows", implicitBypass("loopback"))
}

// Chrome reaches a localhost name itself, without the system resolver or a
// proxy, so the certificate proxy does the same even when the device's
// proxy settings send every request to a proxy.
func TestLoopbackSkipsDeviceProxy(t *testing.T) {
	t.Parallel()
	const host = "portal.localhost"
	ca := newTestCA(t, "corp CA")
	cert, key := ca.issue(t, certOptions{name: host, dnsNames: []string{host}})
	site := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "local portal")
	}))
	site.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{cert.Raw}, PrivateKey: key}}}
	site.StartTLS()
	t.Cleanup(site.Close)
	upstream := newTunnelProxy(t, siteAddr(site), http.StatusOK)
	cfg := testConfig(t, `function FindProxyForURL() { return "PROXY `+upstream.addr+`"; }`)
	cfg.TrustedCAs = []TrustedCA{trustedCA(ca, host)}
	ready := serveProxy(t, cfg)

	body, err := get(t, browserClient(ready), "https://"+net.JoinHostPort(host, portOf(t, siteAddr(site)))+"/")
	require.NoError(t, err)
	assert.Equal(t, "local portal", body)
	assert.Empty(t, upstream.requests())
}

func portOf(t *testing.T, hostport string) string {
	t.Helper()
	_, port, err := net.SplitHostPort(hostport)
	require.NoError(t, err)
	return port
}

func TestChromeArgsUsesManualProxyForLoopback(t *testing.T) {
	t.Parallel()
	handle := Handle{PACURL: "data:pac", SPKI: "abc", Port: 9, Manual: true, DebugPort: 9222}
	assert.Equal(t, []string{
		"--proxy-server=127.0.0.1:9",
		"--ignore-certificate-errors-spki-list=abc",
		"--proxy-bypass-list=<-loopback>;127.0.0.1:9222",
	}, handle.ChromeArgs())

	handle.Manual = false
	assert.Equal(t, []string{
		"--proxy-pac-url=data:pac",
		"--ignore-certificate-errors-spki-list=abc",
	}, handle.ChromeArgs())
}

func TestReadyReportsManualProxy(t *testing.T) {
	t.Parallel()
	ca := newTestCA(t, "corp CA")
	ready := serveProxy(t, testConfig(t, "", ca.clientEntry(t, testLabel, "127.0.0.1")))
	assert.True(t, ready.Manual)
	ready = serveProxy(t, testConfig(t, "", ca.clientEntry(t, testLabel, siteHost)))
	assert.False(t, ready.Manual)
	ready = serveProxy(t, testConfig(t, "", ca.clientEntry(t, testLabel, "*")))
	assert.False(t, ready.Manual)
}

// A loopback address the settings do not name is spliced through, so the
// browser keeps verifying it. An ordinary host still goes through the guard.
func TestCarryAllKeepsUnrelatedHosts(t *testing.T) {
	t.Parallel()
	ca := newTestCA(t, "corp CA")

	t.Run("Loopback", func(t *testing.T) {
		t.Parallel()
		echo, err := net.Listen("tcp", "[::1]:0")
		require.NoError(t, err)
		t.Cleanup(func() { _ = echo.Close() })
		accepted := make(chan net.Conn, 1)
		go func() {
			conn, err := echo.Accept()
			if err != nil {
				return
			}
			accepted <- conn
		}()
		upstream := newTunnelProxy(t, echo.Addr().String(), http.StatusOK)
		pac := `function FindProxyForURL() { return "PROXY ` + upstream.addr + `"; }`
		ready := serveProxy(t, testConfig(t, pac, ca.clientEntry(t, testLabel, "127.0.0.1")))
		conn, status, err := connectThrough(fmt.Sprintf("127.0.0.1:%d", ready.Port), echo.Addr().String())
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, status)
		_, err = io.WriteString(conn, "ping")
		require.NoError(t, err)
		peer := <-accepted
		t.Cleanup(func() { _ = peer.Close(); _ = conn.Close() })
		buf := make([]byte, 4)
		_, err = io.ReadFull(peer, buf)
		require.NoError(t, err)
		assert.Equal(t, "ping", string(buf))
		_, err = io.WriteString(peer, "pong")
		require.NoError(t, err)
		buf = make([]byte, 4)
		_, err = io.ReadFull(conn, buf)
		require.NoError(t, err)
		assert.Equal(t, "pong", string(buf))
		assert.Empty(t, upstream.requests())
	})

	t.Run("Guard", func(t *testing.T) {
		t.Parallel()
		site := newSiteAuth(t, ca, ca, 0, 0)
		upstream := newTunnelProxy(t, siteAddr(site), http.StatusOK)
		cfg := guardConfig(t, ca, `function FindProxyForURL() { return "PROXY `+upstream.addr+`"; }`)
		cfg.TrustedCAs = []TrustedCA{trustedCA(ca, "127.0.0.1")}
		ready := serveProxy(t, cfg)
		require.True(t, ready.Manual)
		body, err := get(t, browserClient(ready, "http/1.1"), "https://"+siteHost+"/")
		require.NoError(t, err)
		assert.Equal(t, "HTTP/1.1 anonymous", body)
		assert.Contains(t, upstream.requests(), siteHost+":443")
	})
}
