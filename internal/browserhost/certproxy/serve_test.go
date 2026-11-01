// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package certproxy

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const siteHost = "portal.test"

// newSite starts an HTTPS server for siteHost and 127.0.0.1, with a
// certificate serverCA issued, that requires a client certificate clientCA
// issued. It answers with the HTTP version and the client certificate's
// name. A maxVersion of zero allows TLS 1.3.
func newSite(t *testing.T, serverCA, clientCA *testCA, maxVersion uint16) *httptest.Server {
	t.Helper()
	return newSiteAuth(t, serverCA, clientCA, tls.RequireAndVerifyClientCert, maxVersion)
}

// newSiteAuth is newSite asking for a client certificate as auth says. It
// names a client that presented none "anonymous".
func newSiteAuth(t *testing.T, serverCA, clientCA *testCA, auth tls.ClientAuthType, maxVersion uint16) *httptest.Server {
	t.Helper()
	cert, key := serverCA.issue(t, certOptions{name: siteHost, dnsNames: []string{siteHost}, ips: []net.IP{net.IPv4(127, 0, 0, 1)}})
	site := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := "anonymous"
		if len(r.TLS.PeerCertificates) > 0 {
			name = r.TLS.PeerCertificates[0].Subject.CommonName
		}
		_, _ = fmt.Fprintf(w, "%s %s", r.Proto, name)
	}))
	site.EnableHTTP2 = true
	site.TLS = &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{cert.Raw}, PrivateKey: key}},
		ClientAuth:   auth,
		ClientCAs:    clientCA.pool(),
		NextProtos:   []string{"h2", "http/1.1"},
		MaxVersion:   maxVersion,
	}
	// Refused handshakes are what several tests expect.
	site.Config.ErrorLog = log.New(io.Discard, "", 0)
	site.StartTLS()
	t.Cleanup(site.Close)
	return site
}

func get(t *testing.T, client *http.Client, url string) (string, error) {
	t.Helper()
	response, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	return string(body), err
}

func siteAddr(site *httptest.Server) string {
	return site.Listener.Addr().String()
}

func TestServePresentsClientCertificate(t *testing.T) {
	t.Parallel()

	ca := newTestCA(t, "corp CA")
	site := newSite(t, ca, ca, 0)
	entry := ca.clientEntry(t, testLabel, "127.0.0.1")
	entry.CAPEM = ca.pem()
	cfg := testConfig(t, "", entry)
	ready := serveProxy(t, cfg)

	for want, protocols := range map[string][]string{
		"HTTP/2.0": {"h2", "http/1.1"},
		"HTTP/1.1": {"http/1.1"},
	} {
		body, err := get(t, browserClient(ready, protocols...), site.URL)
		require.NoError(t, err, want)
		assert.Equal(t, want+" "+testLabel, body)
	}
	event := waitForEvent(t, ready, cfg.Token, EventConnect)
	assert.Equal(t, "127.0.0.1", event.Host)
	assert.Equal(t, testLabel, event.Entry)
	assert.Contains(t, event.Message, "presented the client certificate "+testLabel)
}

func TestServeReportsRejectedCertificate(t *testing.T) {
	t.Parallel()

	for name, maxVersion := range map[string]uint16{"TLS13": 0, "TLS12": tls.VersionTLS12} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ca, other := newTestCA(t, "corp CA"), newTestCA(t, "other CA")
			site := newSite(t, ca, ca, maxVersion)
			entry := other.clientEntry(t, testLabel, "127.0.0.1")
			entry.CAPEM = ca.pem()
			cfg := testConfig(t, "", entry)
			ready := serveProxy(t, cfg)

			_, err := get(t, browserClient(ready, "h2", "http/1.1"), site.URL)
			require.Error(t, err)
			event := waitForEvent(t, ready, cfg.Token, EventClientCertificateRejected)
			assert.Equal(t, "127.0.0.1", event.Host)
			assert.Equal(t, testLabel, event.Entry)
			assert.Contains(t, event.Message, "127.0.0.1 rejected the client certificate "+testLabel+": ")
		})
	}
}

// A server's refusal of the client certificate is reported even when it
// arrives after the browser closed its side: the proxy reads what the
// server sends before it closes the connection to it.
func TestServeReportsRejectionAfterBrowserCloses(t *testing.T) {
	t.Parallel()

	ca, other := newTestCA(t, "corp CA"), newTestCA(t, "other CA")
	cert, key := ca.issue(t, certOptions{name: siteHost, ips: []net.IP{net.IPv4(127, 0, 0, 1)}})
	release := make(chan struct{})
	var releaseOnce sync.Once
	site := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	site.TLS = &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{cert.Raw}, PrivateKey: key}},
		// Under TLS 1.3 the client finishes its handshake before the
		// server judges its certificate; holding the verdict orders it
		// after the browser closed.
		ClientAuth: tls.RequireAnyClientCert,
		VerifyPeerCertificate: func([][]byte, [][]*x509.Certificate) error {
			<-release
			return errors.New("the client certificate is not accepted")
		},
	}
	site.Config.ErrorLog = log.New(io.Discard, "", 0)
	site.StartTLS()
	t.Cleanup(site.Close)
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	entry := other.clientEntry(t, testLabel, "127.0.0.1")
	entry.CAPEM = ca.pem()
	cfg := testConfig(t, "", entry)
	ready := serveProxy(t, cfg)

	raw, status, err := connectThrough(fmt.Sprintf("127.0.0.1:%d", ready.Port), siteAddr(site))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, status)
	browser := tls.Client(raw, &tls.Config{
		ServerName:            "127.0.0.1",
		InsecureSkipVerify:    true, //nolint:gosec // the certificate is checked by its key below
		VerifyPeerCertificate: pinnedKey(ready.SPKI),
	})
	_, err = io.WriteString(browser, "GET / HTTP/1.1\r\nHost: 127.0.0.1\r\n\r\n")
	require.NoError(t, err)
	require.NoError(t, browser.Close())
	// Long enough for a proxy that ends the tunnel on the browser's close
	// to have done so; a proxy that reads on needs no wait at all.
	time.Sleep(200 * time.Millisecond)
	releaseOnce.Do(func() { close(release) })

	event := waitForEvent(t, ready, cfg.Token, EventClientCertificateRejected)
	assert.Equal(t, "127.0.0.1", event.Host)
}

func TestServeReportsUntrustedServer(t *testing.T) {
	t.Parallel()

	ca := newTestCA(t, "corp CA")
	site := newSite(t, ca, ca, 0)
	cfg := testConfig(t, "", ca.clientEntry(t, testLabel, "127.0.0.1"))
	ready := serveProxy(t, cfg)

	_, err := get(t, browserClient(ready, "http/1.1"), site.URL)
	require.Error(t, err)
	event := waitForEvent(t, ready, cfg.Token, EventServerCertificate)
	assert.Equal(t, "127.0.0.1", event.Host)
	assert.Contains(t, event.Message, "ca_file of "+testLabel)
}

func TestServeFollowsSystemRoute(t *testing.T) {
	t.Parallel()

	ca := newTestCA(t, "corp CA")
	site := newSite(t, ca, ca, 0)
	entry := ca.clientEntry(t, testLabel, siteHost)
	entry.CAPEM = ca.pem()
	// siteHost does not resolve, so only the system's route reaches it.
	siteURL := "https://" + siteHost + "/"

	t.Run("Proxy", func(t *testing.T) {
		t.Parallel()
		upstream := newTunnelProxy(t, siteAddr(site), http.StatusOK)
		ready := serveProxy(t, testConfig(t, `function FindProxyForURL(url, host) { return "PROXY `+upstream.addr+`"; }`, entry))
		body, err := get(t, browserClient(ready, "h2"), siteURL)
		require.NoError(t, err)
		assert.Equal(t, "HTTP/2.0 "+testLabel, body)
		assert.Equal(t, []string{siteHost + ":443"}, upstream.requests())
	})

	t.Run("SOCKS5", func(t *testing.T) {
		t.Parallel()
		upstream := newSOCKSProxy(t, siteAddr(site))
		ready := serveProxy(t, testConfig(t, `function FindProxyForURL(url, host) { return "SOCKS5 `+upstream.addr+`"; }`, entry))
		body, err := get(t, browserClient(ready, "http/1.1"), siteURL)
		require.NoError(t, err)
		assert.Equal(t, "HTTP/1.1 "+testLabel, body)
		assert.Equal(t, []string{siteHost + ":443"}, upstream.requests())
	})

	t.Run("ProxyAskingForCredentials", func(t *testing.T) {
		t.Parallel()
		upstream := newTunnelProxy(t, siteAddr(site), http.StatusProxyAuthRequired)
		cfg := testConfig(t, `function FindProxyForURL(url, host) { return "PROXY `+upstream.addr+`"; }`, entry)
		ready := serveProxy(t, cfg)
		_, err := get(t, browserClient(ready, "http/1.1"), siteURL)
		require.Error(t, err)
		event := waitForEvent(t, ready, cfg.Token, EventProxyAuth)
		assert.Equal(t, siteHost, event.Host)
		assert.Contains(t, event.Message, "not supported for certificate hosts")
	})

	t.Run("UnsupportedRouteIsSkipped", func(t *testing.T) {
		t.Parallel()
		upstream := newTunnelProxy(t, siteAddr(site), http.StatusOK)
		cfg := testConfig(t, `function FindProxyForURL(url, host) { return "QUIC 127.0.0.1:1; PROXY `+upstream.addr+`"; }`, entry)
		ready := serveProxy(t, cfg)
		body, err := get(t, browserClient(ready, "http/1.1"), siteURL)
		require.NoError(t, err)
		assert.Equal(t, "HTTP/1.1 "+testLabel, body)
		event := waitForEvent(t, ready, cfg.Token, EventUpstream)
		assert.Contains(t, event.Message, "QUIC 127.0.0.1:1")
	})
}

func TestServeRefusesUnnamedHost(t *testing.T) {
	t.Parallel()

	ca := newTestCA(t, "corp CA")
	cfg := testConfig(t, "", ca.clientEntry(t, testLabel, siteHost))
	ready := serveProxy(t, cfg)

	conn, status, err := connectThrough(fmt.Sprintf("127.0.0.1:%d", ready.Port), "other.test:443")
	require.NoError(t, err)
	_ = conn.Close()
	assert.Equal(t, http.StatusForbidden, status)
	event := waitForEvent(t, ready, cfg.Token, EventRefused)
	assert.Equal(t, "other.test", event.Host)
	assert.Empty(t, event.Entry)
}

func TestServeControlPaths(t *testing.T) {
	t.Parallel()

	ca := newTestCA(t, "corp CA")
	entry := ca.clientEntry(t, testLabel, siteHost)
	status := func(url string) int {
		response, err := http.Get(url) //nolint:gosec // a loopback test server
		require.NoError(t, err)
		_ = response.Body.Close()
		return response.StatusCode
	}

	t.Run("NeedToken", func(t *testing.T) {
		t.Parallel()
		cfg := testConfig(t, "", entry)
		ready := serveProxy(t, cfg)
		require.True(t, strings.HasPrefix(ready.PACURL, pacDataURLPrefix))
		base := fmt.Sprintf("http://127.0.0.1:%d", ready.Port)
		assert.Equal(t, http.StatusOK, status(base+"/"+cfg.Token+"/events"))
		assert.Equal(t, http.StatusNotFound, status(base+"/events"))
		assert.Equal(t, http.StatusNotFound, status(base+"/other-token/events"))
		assert.Equal(t, http.StatusNotFound, status(base+"/"+cfg.Token+"/proxy.pac"), "a PAC in a data: URL is not served")
		assert.Equal(t, http.StatusNotFound, status(base+"/"))

		// A request for another site sent through the proxy does not reach
		// the control paths.
		proxyURL, err := url.Parse(base)
		require.NoError(t, err)
		client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
		response, err := client.Get("http://portal.example.com/" + cfg.Token + "/events")
		require.NoError(t, err)
		_ = response.Body.Close()
		assert.Equal(t, http.StatusNotFound, response.StatusCode)
	})

	t.Run("ServeLargePAC", func(t *testing.T) {
		t.Parallel()
		system := "// " + strings.Repeat("x", maxPACDataURL) + "\nfunction FindProxyForURL(url, host) { return \"DIRECT\"; }"
		cfg := testConfig(t, system, entry)
		ready := serveProxy(t, cfg)
		base := fmt.Sprintf("http://127.0.0.1:%d", ready.Port)
		assert.Equal(t, base+"/"+cfg.Token+"/proxy.pac", ready.PACURL)

		response, err := http.Get(ready.PACURL) //nolint:gosec // a loopback test server
		require.NoError(t, err)
		defer func() { _ = response.Body.Close() }()
		assert.Equal(t, http.StatusOK, response.StatusCode)
		assert.Equal(t, pacContentType, response.Header.Get("Content-Type"))
		script, err := io.ReadAll(response.Body)
		require.NoError(t, err)
		assert.Equal(t, composePAC(ready.Port, 0, cfg.routedHosts(), system), string(script))
		assert.Equal(t, http.StatusNotFound, status(base+"/other-token/proxy.pac"))
	})
}

func TestServeRejectsInvalidConfig(t *testing.T) {
	t.Parallel()

	ca := newTestCA(t, "corp CA")
	entry := ca.clientEntry(t, testLabel, siteHost)
	broken := entry
	broken.KeyPEM = "not a key"
	for name, cfg := range map[string]Config{
		"NoToken":   {CDPURL: "http://127.0.0.1:1", Entries: []Entry{entry}},
		"NoBrowser": {Token: "t", Entries: []Entry{entry}},
		"NoEntries": {Token: "t", CDPURL: "http://127.0.0.1:1"},
		"BadKey":    {Token: "t", CDPURL: "http://127.0.0.1:1", Entries: []Entry{broken}},
	} {
		err := Serve(t.Context(), cfg, func(Ready) { t.Errorf("%s: ready", name) })
		assert.Error(t, err, name)
	}
}
