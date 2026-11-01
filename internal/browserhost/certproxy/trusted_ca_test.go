// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package certproxy

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func trustedCA(ca *testCA, hosts ...string) TrustedCA {
	return TrustedCA{Label: ca.cert.Subject.CommonName, Hosts: hosts, CAPEM: ca.pem()}
}

func TestScopedTrust(t *testing.T) {
	t.Parallel()
	for _, guard := range []bool{false, true} {
		t.Run(fmt.Sprintf("Guard%t", guard), func(t *testing.T) {
			t.Parallel()
			ca := newTestCA(t, "scoped CA")
			site := newSiteAuth(t, ca, ca, tls.NoClientCert, 0)
			upstream := newTunnelProxy(t, siteAddr(site), http.StatusOK)
			cfg := testConfig(t, `function FindProxyForURL() { return "PROXY `+upstream.addr+`"; }`)
			cfg.TrustedCAs = []TrustedCA{trustedCA(ca, siteHost)}
			cfg.Guard = guard
			ready := serveProxy(t, cfg)

			body, err := get(t, browserClient(ready, "h2"), "https://"+siteHost+"/")
			require.NoError(t, err)
			assert.Equal(t, "HTTP/2.0 anonymous", body)
			event := waitForEvent(t, ready, cfg.Token, EventConnect)
			assert.False(t, event.Presented)

			port := ready.Port
			if guard {
				port = ready.GuardPort
			}
			_, err = get(t, browserClientAt(port, ready, "http/1.1"), site.URL)
			require.Error(t, err, "the same CA must not add trust to an unnamed host")
			if guard {
				waitForEvent(t, ready, cfg.Token, EventServerCertificate)
			} else {
				waitForEvent(t, ready, cfg.Token, EventRefused)
			}
		})
	}
}

func TestScopedTrustCombinesWithIdentity(t *testing.T) {
	t.Parallel()
	ca, other := newTestCA(t, "server CA"), newTestCA(t, "other CA")
	site := newSite(t, ca, other, 0)
	cfg := testConfig(t, "", other.clientEntry(t, testLabel, "127.0.0.1"))
	cfg.TrustedCAs = []TrustedCA{trustedCA(other, "*"), trustedCA(ca, "127.0.0.1")}
	ready := serveProxy(t, cfg)
	body, err := get(t, browserClient(ready, "http/1.1"), site.URL)
	require.NoError(t, err)
	assert.Equal(t, "HTTP/1.1 "+testLabel, body)
}

// A host whose server certificate later fails verification loses the
// proxy's word for it.
func TestScopedTrustWithdrawsVerification(t *testing.T) {
	t.Parallel()
	ca := newTestCA(t, "server CA")
	good, goodKey := ca.issue(t, certOptions{name: siteHost, dnsNames: []string{siteHost}})
	bad, badKey := newTestCA(t, "other CA").issue(t, certOptions{name: siteHost, dnsNames: []string{siteHost}})
	var untrusted atomic.Bool
	site := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	site.TLS = &tls.Config{
		// Each connection shows the current certificate, not a resumed
		// session's.
		SessionTicketsDisabled: true,
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			if untrusted.Load() {
				return &tls.Certificate{Certificate: [][]byte{bad.Raw}, PrivateKey: badKey}, nil
			}
			return &tls.Certificate{Certificate: [][]byte{good.Raw}, PrivateKey: goodKey}, nil
		},
	}
	site.Config.ErrorLog = log.New(io.Discard, "", 0)
	site.StartTLS()
	t.Cleanup(site.Close)
	upstream := newTunnelProxy(t, siteAddr(site), http.StatusOK)
	cfg := testConfig(t, `function FindProxyForURL() { return "PROXY `+upstream.addr+`"; }`)
	cfg.TrustedCAs = []TrustedCA{trustedCA(ca, siteHost)}
	ready := serveProxy(t, cfg)
	handle := controlURL(ready.Port, cfg.Token)

	_, err := get(t, browserClient(ready, "http/1.1"), "https://"+siteHost+"/")
	require.NoError(t, err)
	verified, err := Verified(context.Background(), handle, siteHost)
	require.NoError(t, err)
	assert.True(t, verified)

	untrusted.Store(true)
	_, err = get(t, browserClient(ready, "http/1.1"), "https://"+siteHost+"/")
	require.Error(t, err)
	waitForEvent(t, ready, cfg.Token, EventServerCertificate)
	verified, err = Verified(context.Background(), handle, siteHost)
	require.NoError(t, err)
	assert.False(t, verified)
}

// A self-signed server certificate, as an appliance presents, anchors trust
// in its own name only.
func TestScopedTrustAcceptsSelfSignedServer(t *testing.T) {
	t.Parallel()
	cert, key := newSelfSigned(t, siteHost)
	site := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "appliance") }))
	site.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{cert.Raw}, PrivateKey: key}}}
	site.StartTLS()
	t.Cleanup(site.Close)
	upstream := newTunnelProxy(t, siteAddr(site), http.StatusOK)
	cfg := testConfig(t, `function FindProxyForURL() { return "PROXY `+upstream.addr+`"; }`)
	cfg.TrustedCAs = []TrustedCA{{Label: "appliance", Hosts: []string{siteHost}, CAPEM: certPEM(cert)}}
	ready := serveProxy(t, cfg)

	body, err := get(t, browserClient(ready, "http/1.1"), "https://"+siteHost+"/")
	require.NoError(t, err)
	assert.Equal(t, "appliance", body)
}

// An authority adds to the trust a host has without it: the system's CAs
// and every client certificate's ca_file, with or without the guard.
func TestScopedTrustKeepsGuardTrust(t *testing.T) {
	t.Parallel()
	for _, guard := range []bool{false, true} {
		t.Run(fmt.Sprintf("Guard%t", guard), func(t *testing.T) {
			t.Parallel()
			corp, unrelated := newTestCA(t, "corp CA"), newTestCA(t, "unrelated CA")
			site := newSiteAuth(t, corp, corp, tls.NoClientCert, 0)
			upstream := newTunnelProxy(t, siteAddr(site), http.StatusOK)
			identity := corp.clientEntry(t, testLabel, "other.test")
			identity.CAPEM = corp.pem()
			cfg := testConfig(t, `function FindProxyForURL() { return "PROXY `+upstream.addr+`"; }`, identity)
			cfg.TrustedCAs = []TrustedCA{trustedCA(unrelated, siteHost)}
			cfg.Guard = guard
			ready := serveProxy(t, cfg)

			body, err := get(t, browserClient(ready, "http/1.1"), "https://"+siteHost+"/")
			require.NoError(t, err)
			assert.Equal(t, "HTTP/1.1 anonymous", body)
		})
	}
}

func TestScopedTrustRejectsInvalidServer(t *testing.T) {
	t.Parallel()
	for _, invalid := range []string{"expired", "expired authority", "hostname"} {
		t.Run(invalid, func(t *testing.T) {
			t.Parallel()
			ca := newTestCA(t, "server CA")
			if invalid == "expired authority" {
				ca.cert.NotBefore, ca.cert.NotAfter = time.Now().Add(-2*time.Hour), time.Now().Add(-time.Hour)
				der, err := x509.CreateCertificate(rand.Reader, ca.cert, ca.cert, ca.key.Public(), ca.key)
				require.NoError(t, err)
				ca.cert, err = x509.ParseCertificate(der)
				require.NoError(t, err)
			}
			opts := certOptions{name: siteHost, ips: []net.IP{net.IPv4(127, 0, 0, 1)}}
			switch invalid {
			case "expired":
				opts.notBefore, opts.notAfter = time.Now().Add(-2*time.Hour), time.Now().Add(-time.Hour)
			case "hostname":
				opts.ips, opts.dnsNames = nil, []string{siteHost}
			}
			cert, key := ca.issue(t, opts)
			site := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
			site.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{cert.Raw}, PrivateKey: key}}}
			site.Config.ErrorLog = log.New(io.Discard, "", 0)
			site.StartTLS()
			t.Cleanup(site.Close)
			cfg := testConfig(t, "")
			cfg.TrustedCAs = []TrustedCA{trustedCA(ca, "127.0.0.1")}
			ready := serveProxy(t, cfg)
			_, err := get(t, browserClient(ready, "http/1.1"), site.URL)
			require.Error(t, err)
			waitForEvent(t, ready, cfg.Token, EventServerCertificate)
		})
	}
}

func TestScopedTrustKeepsMandatoryRoute(t *testing.T) {
	t.Parallel()
	ca := newTestCA(t, "server CA")
	site := newSiteAuth(t, ca, ca, tls.NoClientCert, 0)
	upstream := newTunnelProxy(t, siteAddr(site), http.StatusProxyAuthRequired)
	cfg := testConfig(t, `function FindProxyForURL() { return "PROXY `+upstream.addr+`"; }`)
	cfg.TrustedCAs = []TrustedCA{trustedCA(ca, siteHost)}
	cfg.Guard = true
	ready := serveProxy(t, cfg)
	_, err := get(t, browserClient(ready, "http/1.1"), "https://"+siteHost+"/")
	require.Error(t, err)
	waitForEvent(t, ready, cfg.Token, EventProxyAuth)
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", ready.GuardPort), time.Second)
	require.NoError(t, err, "a configured host's proxy failure must not disable the guard")
	_ = conn.Close()
}

func TestScopedTrustUsesProxyHostname(t *testing.T) {
	t.Parallel()
	serverCA, proxyCA := newTestCA(t, "server CA"), newTestCA(t, "proxy CA")
	site := newSiteAuth(t, serverCA, serverCA, tls.NoClientCert, 0)
	proxy := newHTTPSTunnelProxy(t, proxyCA, siteAddr(site))
	cfg := testConfig(t, `function FindProxyForURL() { return "HTTPS `+proxy+`"; }`, proxyCA.clientEntry(t, testLabel, "127.0.0.1"))
	cfg.TrustedCAs = []TrustedCA{trustedCA(serverCA, siteHost)}
	ready := serveProxy(t, cfg)
	_, err := get(t, browserClient(ready, "http/1.1"), "https://"+siteHost+"/")
	require.Error(t, err, "destination trust must not authenticate the HTTPS proxy")

	cfg.TrustedCAs = append(cfg.TrustedCAs, trustedCA(proxyCA, "127.0.0.1"))
	ready = serveProxy(t, cfg)
	body, err := get(t, browserClient(ready, "http/1.1"), "https://"+siteHost+"/")
	require.NoError(t, err)
	assert.Equal(t, "HTTP/1.1 anonymous", body)
}
