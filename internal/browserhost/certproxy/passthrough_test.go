// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package certproxy

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/browserhost"
	"github.com/dagucloud/dagu/v2/internal/test/chrometest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seenHello is what a hello told a site.
type seenHello struct {
	serverName string
	// chrome is set on a hello with GREASE cipher suites, which Chrome
	// sends and Go never does.
	chrome bool
	// retried is set on a request whose connection's handshake took a
	// HelloRetryRequest.
	retried bool
}

// helloSite is an HTTPS site that records the hellos it got, and for each
// request it answered, the hello of the connection that carried it: a
// client's own handshake with the site completed only then.
type helloSite struct {
	*httptest.Server
	mu     sync.Mutex
	hellos []seenHello
	served []seenHello
	// byConn holds the hello each connection began with.
	byConn map[net.Conn]seenHello
}

// connKey keys a request's connection in its context.
type connKey struct{}

// newHelloSite starts a helloSite with a certificate ca issued for siteHost
// and 127.0.0.1. adjust, if set, changes the configuration the site answers
// a hello with.
func newHelloSite(t *testing.T, ca *testCA, adjust func(*tls.ClientHelloInfo, *tls.Config)) *helloSite {
	t.Helper()
	cert, key := ca.issue(t, certOptions{name: siteHost, dnsNames: []string{siteHost}, ips: []net.IP{net.IPv4(127, 0, 0, 1)}})
	base := &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{cert.Raw}, PrivateKey: key}},
		ClientCAs:    ca.pool(),
		NextProtos:   []string{"h2", "http/1.1"},
	}
	site := &helloSite{byConn: map[net.Conn]seenHello{}}
	site.Server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn := r.Context().Value(connKey{}).(*tls.Conn).NetConn()
		site.mu.Lock()
		seen := site.byConn[conn]
		site.mu.Unlock()
		seen.retried = r.TLS.HelloRetryRequest
		site.record(&site.served, seen)
		_, _ = fmt.Fprintf(w, "%s %s", r.Proto, "anonymous")
	}))
	site.EnableHTTP2 = true
	site.Config.ConnContext = func(ctx context.Context, conn net.Conn) context.Context {
		return context.WithValue(ctx, connKey{}, conn)
	}
	site.TLS = &tls.Config{GetConfigForClient: func(info *tls.ClientHelloInfo) (*tls.Config, error) {
		seen := seenHello{serverName: info.ServerName, chrome: slices.ContainsFunc(info.CipherSuites, isGREASE)}
		site.record(&site.hellos, seen)
		site.mu.Lock()
		site.byConn[info.Conn] = seen
		site.mu.Unlock()
		config := base.Clone()
		if adjust != nil {
			adjust(info, config)
		}
		return config, nil
	}}
	// Handshakes the proxy abandons are what several tests expect.
	site.Config.ErrorLog = log.New(io.Discard, "", 0)
	site.StartTLS()
	t.Cleanup(site.Close)
	return site
}

func (s *helloSite) record(list *[]seenHello, seen seenHello) {
	s.mu.Lock()
	defer s.mu.Unlock()
	*list = append(*list, seen)
}

// count returns how many of list match.
func (s *helloSite) count(list *[]seenHello, match func(seenHello) bool) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, seen := range *list {
		if match(seen) {
			n++
		}
	}
	return n
}

// isGREASE reports whether value is one of the values RFC 8701 reserves.
func isGREASE(value uint16) bool {
	return value&0x0f0f == 0x0a0a && value>>8 == value&0xff
}

// namedSite matches what the test clients send: the site's name, which
// the proxy's own handshakes with 127.0.0.1 never carry.
func namedSite(seen seenHello) bool { return seen.serverName == siteHost }

// unnamed matches the proxy's own handshakes with 127.0.0.1.
func unnamed(seen seenHello) bool { return seen.serverName == "" }

// guardClient is an HTTP client that opens a new connection to every site
// through the guard at port, with TLS configured by config.
func guardClient(port int, config *tls.Config) *http.Client {
	proxyAddr := fmt.Sprintf("127.0.0.1:%d", port)
	transport := &http.Transport{
		DisableKeepAlives: true,
		DialTLSContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
			conn, status, err := connectThrough(proxyAddr, addr)
			if err != nil {
				return nil, err
			}
			if status != http.StatusOK {
				_ = conn.Close()
				return nil, fmt.Errorf("CONNECT %s: %d", addr, status)
			}
			tlsConn := tls.Client(conn, config.Clone())
			if err := tlsConn.HandshakeContext(ctx); err != nil {
				_ = conn.Close()
				return nil, err
			}
			return tlsConn, nil
		},
	}
	return &http.Client{Transport: transport, Timeout: 20 * time.Second}
}

// throughProxy configures a client that accepts only the proxy's own
// certificates, and counts the requests for a client certificate it meets.
func throughProxy(ready Ready, asked *atomic.Int32) *tls.Config {
	return &tls.Config{
		ServerName:            siteHost,
		NextProtos:            []string{"http/1.1"},
		InsecureSkipVerify:    true, //nolint:gosec // the certificate is checked by its key below
		VerifyPeerCertificate: pinnedKey(ready.SPKI),
		GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			asked.Add(1)
			return &tls.Certificate{}, nil
		},
	}
}

// guardPassing returns a guard configuration whose only entry names a host
// the tests never reach. With trustSite, the proxy's own TLS trusts ca's
// sites, as a ca_file makes it.
func guardPassing(t *testing.T, ca *testCA, systemPAC string, trustSite bool) Config {
	t.Helper()
	entry := ca.clientEntry(t, testLabel, "cert.example")
	if trustSite {
		entry.CAPEM = ca.pem()
	}
	cfg := testConfig(t, systemPAC, entry)
	cfg.Guard = true
	return cfg
}

// A public site on TLS 1.3 that asks for no client certificate gets the
// browser's own TLS: the browser sees the site's certificate, and the site
// the browser's hello. The guard probes the site once for every connection
// after, and vouches for it.
func TestGuardPassesSiteThatAsksForNoCertificate(t *testing.T) {
	t.Parallel()

	ca := newTestCA(t, "public CA")
	site := newHelloSite(t, ca, nil)
	cfg := guardPassing(t, ca, "", false)
	ready := servePassing(t, cfg, ca)
	client := guardClient(ready.GuardPort, &tls.Config{ServerName: siteHost, RootCAs: ca.pool(), NextProtos: []string{"http/1.1"}})

	var wg sync.WaitGroup
	for range 3 {
		wg.Go(func() {
			body, err := get(t, client, site.URL)
			assert.NoError(t, err)
			assert.Equal(t, "HTTP/1.1 anonymous", body)
		})
	}
	wg.Wait()
	body, err := get(t, client, site.URL)
	require.NoError(t, err)
	assert.Equal(t, "HTTP/1.1 anonymous", body)

	assert.Equal(t, 4, site.count(&site.served, namedSite), "every request came over the browser's own TLS")
	assert.Equal(t, 1, site.count(&site.hellos, unnamed), "one probe")
	verified, err := Verified(context.Background(), controlURL(ready.Port, cfg.Token), "127.0.0.1")
	require.NoError(t, err)
	assert.True(t, verified)
}

// A site that asks for a client certificate keeps the proxy's TLS, so the
// browser never meets the request, and the proxy reports it.
func TestGuardKeepsSiteThatAsksForCertificate(t *testing.T) {
	t.Parallel()

	for name, maxVersion := range map[string]uint16{"TLS13": 0, "TLS12": tls.VersionTLS12} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ca := newTestCA(t, "public CA")
			site := newHelloSite(t, ca, func(_ *tls.ClientHelloInfo, c *tls.Config) {
				c.ClientAuth, c.MaxVersion = tls.RequireAndVerifyClientCert, maxVersion
			})
			cfg := guardPassing(t, ca, "", true)
			ready := servePassing(t, cfg, ca)
			var asked atomic.Int32

			_, err := get(t, guardClient(ready.GuardPort, throughProxy(ready, &asked)), site.URL)
			require.Error(t, err)
			waitForEvent(t, ready, cfg.Token, EventClientCertificateMissing)
			assert.Zero(t, asked.Load(), "the browser met a request for a certificate")
			assert.Zero(t, site.count(&site.served, namedSite))
		})
	}
}

// A site on TLS 1.2 keeps the proxy's TLS: under it, the guard could not
// tell whether the site asks the browser for a certificate later.
func TestGuardKeepsTLS12Site(t *testing.T) {
	t.Parallel()

	ca := newTestCA(t, "public CA")
	site := newHelloSite(t, ca, func(_ *tls.ClientHelloInfo, c *tls.Config) { c.MaxVersion = tls.VersionTLS12 })
	cfg := guardPassing(t, ca, "", true)
	ready := servePassing(t, cfg, ca)
	var asked atomic.Int32

	body, err := get(t, guardClient(ready.GuardPort, throughProxy(ready, &asked)), site.URL)
	require.NoError(t, err)
	assert.Equal(t, "HTTP/1.1 anonymous", body)
	assert.Equal(t, 1, site.count(&site.served, unnamed), "the request came over the proxy's TLS")
}

// When a site the guard passes answers the browser otherwise than it
// answered the probe, the proxy's TLS takes the connection over, replaying
// the browser's hello, and the browser does not notice.
func TestGuardTakesOverSiteAnsweringBrowserOtherwise(t *testing.T) {
	t.Parallel()

	ca := newTestCA(t, "public CA")
	site := newHelloSite(t, ca, func(info *tls.ClientHelloInfo, c *tls.Config) {
		if info.ServerName == siteHost {
			c.MaxVersion = tls.VersionTLS12
		}
	})
	cfg := guardPassing(t, ca, "", true)
	ready := servePassing(t, cfg, ca)
	var asked atomic.Int32

	body, err := get(t, guardClient(ready.GuardPort, throughProxy(ready, &asked)), site.URL)
	require.NoError(t, err)
	assert.Equal(t, "HTTP/1.1 anonymous", body)
	assert.Equal(t, 1, site.count(&site.hellos, namedSite), "the browser's hello reached the site")
	assert.Equal(t, 1, site.count(&site.served, unnamed), "the request came over the proxy's TLS")
}

// A connection the guard passed waits on the site's answer once the browser
// has asked, and the proxy lists it so.
func TestGuardListsPassedConnectionWaitingForAnswer(t *testing.T) {
	t.Parallel()

	ca := newTestCA(t, "public CA")
	cert, key := ca.issue(t, certOptions{name: siteHost, dnsNames: []string{siteHost}, ips: []net.IP{net.IPv4(127, 0, 0, 1)}})
	answer := make(chan struct{})
	site := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-answer }))
	// Session tickets would reach the browser after the request, which
	// counts as the site answering.
	site.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{cert.Raw}, PrivateKey: key}}, SessionTicketsDisabled: true}
	site.StartTLS()
	t.Cleanup(site.Close)
	// Before the site closes, which waits for the request to end.
	t.Cleanup(func() { close(answer) })
	cfg := guardPassing(t, ca, "", false)
	ready := servePassing(t, cfg, ca)

	client := guardClient(ready.GuardPort, &tls.Config{ServerName: siteHost, RootCAs: ca.pool(), NextProtos: []string{"http/1.1"}})
	go func() {
		if response, err := client.Get(site.URL); err == nil {
			_ = response.Body.Close()
		}
	}()
	var pending []Pending
	var err error
	require.Eventually(t, func() bool {
		pending, err = PendingConnections(context.Background(), controlURL(ready.Port, cfg.Token))
		return err == nil && len(pending) == 1 && pending[0].Phase == PhaseAnswer
	}, 10*time.Second, 50*time.Millisecond, "pending: %v", pending)
	assert.Equal(t, "127.0.0.1", pending[0].Host)
}

// Chrome loads every page through the guard. Its own handshake reaches a
// site the guard passes, including one that asks Chrome to retry its hello,
// and never one that asks for a client certificate.
func TestGuardPassesChromesOwnHandshake(t *testing.T) {
	t.Parallel()
	chrome := chrometest.Require(t)

	for name, tc := range map[string]struct {
		adjust func(*tls.ClientHelloInfo, *tls.Config)
		// passes is set where Chrome's own TLS should reach the site, and
		// retries where its hello should be retried.
		passes, retries bool
	}{
		"plain": {passes: true},
		"HelloRetryRequest": {
			adjust:  func(_ *tls.ClientHelloInfo, c *tls.Config) { c.CurvePreferences = []tls.CurveID{tls.CurveP384} },
			passes:  true,
			retries: true,
		},
		"asks for a certificate": {
			adjust: func(_ *tls.ClientHelloInfo, c *tls.Config) { c.ClientAuth = tls.RequestClientCert },
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ca := newTestCA(t, "public CA")
			site := newHelloSite(t, ca, tc.adjust)
			// Chrome never resolves siteHost; the device's proxy carries the
			// certificate proxy's connections to the site.
			upstream := newTunnelProxy(t, siteAddr(site.Server), http.StatusOK)
			cfg := guardPassing(t, ca, fmt.Sprintf(`function FindProxyForURL() { return "PROXY %s"; }`, upstream.addr), true)
			ready := servePassing(t, cfg, ca)
			runChrome(t, chrome, ready, "https://"+siteHost+"/")

			byChrome := func(seen seenHello) bool { return seen.chrome && seen.serverName == siteHost }
			byProxy := func(seen seenHello) bool { return !seen.chrome && seen.serverName == siteHost }
			loaded, other := byChrome, byProxy
			if !tc.passes {
				loaded, other = byProxy, byChrome
			}
			require.Eventually(t, func() bool {
				return site.count(&site.served, loaded) > 0
			}, time.Minute, 100*time.Millisecond, "the page never loaded the expected way")
			assert.Zero(t, site.count(&site.served, other))
			if tc.retries {
				assert.Positive(t, site.count(&site.served, func(seen seenHello) bool { return loaded(seen) && seen.retried }))
			}
			if !tc.passes {
				waitForEvent(t, ready, cfg.Token, EventClientCertificateMissing)
				assert.Positive(t, site.count(&site.hellos, byChrome), "Chrome's hello reached the site before the verdict")
			}
		})
	}
}

// runChrome opens url in a headless Chrome that reaches every site through
// the guard alone, until the test ends: the proxy's PAC script would let
// Chrome go around a guard connection that fails. Chrome accepts any server
// certificate, so a handshake of its own that reaches the site completes.
func runChrome(t *testing.T, chrome string, ready Ready, url string) {
	t.Helper()
	profile, err := os.MkdirTemp("", "dagu-certproxy-chrome-")
	require.NoError(t, err)
	t.Cleanup(func() {
		// Chrome's last processes may still be writing as it exits.
		assert.Eventually(t, func() bool { return os.RemoveAll(profile) == nil },
			10*time.Second, 200*time.Millisecond, "remove the browser profile")
	})
	pac := fmt.Sprintf(`function FindProxyForURL() { return "PROXY 127.0.0.1:%d"; }`, ready.GuardPort)
	cmd := exec.Command(chrome,
		"--headless",
		"--no-sandbox",
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-background-networking",
		"--disable-component-update",
		"--ignore-certificate-errors",
		"--remote-debugging-port=0",
		"--user-data-dir="+profile,
		"--proxy-pac-url="+pacDataURLPrefix+base64.StdEncoding.EncodeToString([]byte(pac)),
		"--ignore-certificate-errors-spki-list="+ready.SPKI,
		url,
	)
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { closeChrome(cmd, profile) })
}

// closeChrome closes the Chrome cmd runs through its DevTools port, which
// ends every process it started; killing it alone would leave them running.
// It kills Chrome only when that does not end it in time.
func closeChrome(cmd *exec.Cmd, profile string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(exited)
	}()
	// Chrome writes the port it chose to the profile's DevToolsActivePort.
	if data, err := os.ReadFile(filepath.Join(profile, "DevToolsActivePort")); err == nil {
		port, _, _ := strings.Cut(string(data), "\n")
		_ = browserhost.CloseBrowser(ctx, "http://127.0.0.1:"+port)
	}
	select {
	case <-exited:
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		<-exited
	}
}
