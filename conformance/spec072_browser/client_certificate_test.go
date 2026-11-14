// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec072_browser_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"software.sslmate.com/src/go-pkcs12"
)

// mtlsHost names the site that requires a client certificate. Chrome never
// resolves it: requests reach it through the upstream proxy, which maps it
// to the local server. Loopback addresses would bypass every proxy.
const mtlsHost = "mtls.test"

const certificatePassphrase = "conformance-passphrase"

// testCA issues certificates for one test.
type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

func newTestCA(t *testing.T, name string) *testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return &testCA{cert: cert, key: key}
}

func (ca *testCA) issue(t *testing.T, commonName string, usage x509.ExtKeyUsage, dnsNames ...string) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	serial, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: commonName},
		DNSNames:     dnsNames,
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{usage},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca.cert, &key.PublicKey, ca.key)
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return cert, key
}

func (ca *testCA) pem() string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.cert.Raw}))
}

// startMTLSSite serves a page greeting the client certificate's common name,
// and refuses connections without a certificate clientCA issued.
func startMTLSSite(t *testing.T, serverCA, clientCA *testCA) string {
	t.Helper()
	return startSite(t, serverCA, clientCA, mtlsHost, tls.RequireAndVerifyClientCert)
}

// startSite serves host with a certificate serverCA issued, asking for a
// client certificate clientCA issued as auth says. Its page greets the
// certificate's common name, or "anonymous".
func startSite(t *testing.T, serverCA, clientCA *testCA, host string, auth tls.ClientAuthType) string {
	t.Helper()
	cert, key := serverCA.issue(t, host, x509.ExtKeyUsageServerAuth, host)
	pool := x509.NewCertPool()
	pool.AddCert(clientCA.cert)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := "anonymous"
		if len(r.TLS.PeerCertificates) > 0 {
			name = r.TLS.PeerCertificates[0].Subject.CommonName
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, "<!doctype html><title>Portal</title><h1>Hello "+name+"</h1>")
	}))
	server.TLS = &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{cert.Raw}, PrivateKey: key, Leaf: cert}},
		ClientAuth:   auth,
		ClientCAs:    pool,
	}
	server.StartTLS()
	t.Cleanup(server.Close)
	return server.Listener.Addr().String()
}

// upstreamProxy is a CONNECT proxy that sends each site's host to the local
// server for it, and records the targets it was asked for.
type upstreamProxy struct {
	mu      sync.Mutex
	targets []string
}

// startUpstreamProxy starts an upstream proxy for sites, which maps host
// names to local addresses. With signIn, it asks for credentials from a
// client other than a browser, as a proxy wanting a Windows sign-in does
// from a program that cannot give one.
func startUpstreamProxy(t *testing.T, sites map[string]string, signIn bool) (*upstreamProxy, string) {
	t.Helper()
	proxy := &upstreamProxy{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "CONNECT only", http.StatusMethodNotAllowed)
			return
		}
		if signIn && !strings.Contains(r.Header.Get("User-Agent"), "Mozilla") {
			w.Header().Set("Proxy-Authenticate", "Negotiate")
			http.Error(w, "sign in", http.StatusProxyAuthRequired)
			return
		}
		proxy.mu.Lock()
		proxy.targets = append(proxy.targets, r.Host)
		proxy.mu.Unlock()
		host, _, _ := net.SplitHostPort(r.Host)
		siteAddr, ok := sites[host]
		if !ok {
			http.Error(w, "unknown host", http.StatusBadGateway)
			return
		}
		upstream, err := net.Dial("tcp", siteAddr)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		client, _, err := http.NewResponseController(w).Hijack()
		if err != nil {
			_ = upstream.Close()
			return
		}
		_, _ = io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n")
		go func() { _, _ = io.Copy(upstream, client); _ = upstream.Close() }()
		_, _ = io.Copy(client, upstream)
		_ = client.Close()
	}))
	t.Cleanup(server.Close)
	return proxy, server.URL
}

func (p *upstreamProxy) connects() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.targets...)
}

// clientCertificateEnv sets up a site that requires a certificate clientCA
// issues, and writes the step's PKCS#12 file, issued by issuer, and the
// site's CA into the project.
func clientCertificateEnv(t *testing.T, b *browserEnv, clientCA, issuer *testCA) *upstreamProxy {
	t.Helper()
	return certificateSitesEnv(t, b, clientCA, issuer, certificateSites{})
}

// certificateSites describes, besides the certificate's own site, a site no
// certificate names and how the upstream proxy treats requests.
type certificateSites struct {
	// otherAuth is how the other site asks for a client certificate; zero
	// leaves it out.
	otherAuth tls.ClientAuthType
	other     bool
	// signIn makes the upstream proxy ask a client other than the browser
	// for credentials.
	signIn bool
}

// otherHost names the site no certificate names.
const otherHost = "other.test"

func certificateSitesEnv(t *testing.T, b *browserEnv, clientCA, issuer *testCA, sites certificateSites) *upstreamProxy {
	t.Helper()
	serverCA := newTestCA(t, "Conformance Server CA")
	addrs := map[string]string{mtlsHost: startMTLSSite(t, serverCA, clientCA)}
	if sites.other {
		addrs[otherHost] = startSite(t, serverCA, clientCA, otherHost, sites.otherAuth)
	}
	proxy, proxyURL := startUpstreamProxy(t, addrs, sites.signIn)
	cert, key := issuer.issue(t, "client-1", x509.ExtKeyUsageClientAuth)
	data, err := pkcs12.Modern.Encode(key, cert, []*x509.Certificate{issuer.cert}, certificatePassphrase)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(b.dagu.ProjectPath("client.p12"), data, 0o600))
	b.dagu.WriteFile("ca.pem", serverCA.pem())
	b.env = append(b.env, "UPSTREAM_PROXY="+proxyURL, "CERT_PASSPHRASE="+certificatePassphrase, "OTHER_URL=https://"+otherHost+"/")
	return proxy
}

// A site that requires a client certificate opens with the one the step
// names, reached through the proxy the step sets.
func TestBrowserClientCertificate(t *testing.T) {
	t.Parallel()

	b := newBrowserEnv(t)
	clientCA := newTestCA(t, "Conformance Client CA")
	proxy := clientCertificateEnv(t, b, clientCA, clientCA)
	result := b.dagu.RunWithEnv(b.env, "start", "client_certificate.yaml")
	result.ExpectExitCode(0)
	require.Contains(t, proxy.connects(), mtlsHost+":443")
	require.Zero(t, b.model.total())
}

// A certificate the site does not accept fails the step with the site's
// refusal.
func TestBrowserClientCertificateRejected(t *testing.T) {
	t.Parallel()

	b := newBrowserEnv(t)
	clientCertificateEnv(t, b, newTestCA(t, "Conformance Client CA"), newTestCA(t, "Other CA"))
	result := b.dagu.RunWithEnv(b.env, "start", "client_certificate.yaml")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains(mtlsHost + " rejected the client certificate")
}

// A wrong passphrase fails the step before a browser starts, without
// revealing the passphrase given.
func TestBrowserClientCertificateWrongPassphrase(t *testing.T) {
	t.Parallel()

	b := newBrowserEnv(t)
	clientCA := newTestCA(t, "Conformance Client CA")
	proxy := clientCertificateEnv(t, b, clientCA, clientCA)
	b.env = append(b.env, "CERT_PASSPHRASE=not-the-passphrase")
	result := b.dagu.RunWithEnv(b.env, "start", "client_certificate.yaml")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains("with.browser.client_certificates[0]")
	result.ExpectStderrNotContains("not-the-passphrase")
	require.Empty(t, proxy.connects())
}

// A site no certificate names that asks for one fails at once, saying so,
// instead of leaving the browser waiting on the system's certificate
// prompt.
func TestBrowserGuardReportsMissingCertificate(t *testing.T) {
	t.Parallel()

	b := newBrowserEnv(t)
	clientCA := newTestCA(t, "Conformance Client CA")
	certificateSitesEnv(t, b, clientCA, clientCA, certificateSites{other: true, otherAuth: tls.RequireAndVerifyClientCert})
	began := time.Now()
	result := b.dagu.RunWithEnv(b.env, "start", "client_certificate_guard.yaml")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains(otherHost + " asks for a TLS client certificate, and none is configured for it")
	require.Less(t, time.Since(began), time.Minute, "the step fails without waiting out the goto")
}

// A site that asks for a certificate but opens without one opens.
func TestBrowserGuardOpensOptionalCertificateSite(t *testing.T) {
	t.Parallel()

	b := newBrowserEnv(t)
	clientCA := newTestCA(t, "Conformance Client CA")
	certificateSitesEnv(t, b, clientCA, clientCA, certificateSites{other: true, otherAuth: tls.VerifyClientCertIfGiven})
	b.dagu.RunWithEnv(b.env, "start", "client_certificate_guard.yaml").ExpectExitCode(0)
}

// When the device's proxy asks the certificate proxy for credentials it
// cannot give, the browser falls back to reaching other sites itself.
func TestBrowserGuardFallsBackForProxySignIn(t *testing.T) {
	t.Parallel()

	b := newBrowserEnv(t)
	clientCA := newTestCA(t, "Conformance Client CA")
	proxy := certificateSitesEnv(t, b, clientCA, clientCA, certificateSites{other: true, otherAuth: tls.NoClientCert, signIn: true})
	// The browser does not trust the test site's CA, so the step only opens
	// it; the upstream proxy records only the browser's requests.
	b.dagu.RunWithEnv(b.env, "start", "client_certificate_fallback.yaml").ExpectExitCode(0)
	require.Contains(t, proxy.connects(), otherHost+":443", "the browser reached the site through the device's proxy")
}
