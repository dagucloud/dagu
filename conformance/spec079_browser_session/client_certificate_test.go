// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec079_browser_session_test

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
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"software.sslmate.com/src/go-pkcs12"
)

// A session's browser presents the client certificate the host config
// names, in every command, since the proxy presenting it runs as long as the
// browser.
func TestSessionPresentsHostClientCertificate(t *testing.T) {
	t.Parallel()

	s := newSessionEnv(t)
	clientCA, serverCA := newTestCA(t, "Conformance Client CA"), newTestCA(t, "Conformance Server CA")
	proxy, proxyURL := startUpstreamProxy(t, startMTLSSite(t, serverCA, clientCA))
	cert, key := clientCA.issue(t, "client-1", x509.ExtKeyUsageClientAuth)
	data, err := pkcs12.Modern.Encode(key, cert, nil, certificatePassphrase)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(s.dagu.ProjectPath("client.p12"), data, 0o600))
	s.dagu.WriteFile("client.passphrase", certificatePassphrase+"\n")
	s.dagu.WriteFile("ca.pem", serverCA.pem())
	home := ""
	for _, entry := range s.env {
		if value, ok := strings.CutPrefix(entry, "DAGU_HOME="); ok {
			home = value
		}
	}
	require.NoError(t, os.WriteFile(filepath.Join(home, "config.yaml"), []byte(`browser:
  client_certificates:
    - hosts: [`+mtlsHost+`]
      pfx_file: `+s.dagu.ProjectPath("client.p12")+`
      passphrase_file: `+s.dagu.ProjectPath("client.passphrase")+`
      ca_file: `+s.dagu.ProjectPath("ca.pem")+`
`), 0o600))

	opened := s.session("", "open", "https://"+mtlsHost+"/", "--proxy", proxyURL)
	opened.ExpectExitCode(0)
	id, _ := printed(t, opened)["id"].(string)
	require.NotEmpty(t, id, opened.Stdout())
	t.Cleanup(func() { s.session("", "close", id, "--force") })
	assert.Contains(t, opened.Stdout(), "Hello client-1")

	s.session(`{"goto": "https://`+mtlsHost+`/again"}`, "do", id).ExpectExitCode(0)
	s.session(`{"expect": {"text": "Hello client-1"}}`, "do", id).ExpectExitCode(0)
	assert.GreaterOrEqual(t, strings.Count(strings.Join(proxy.connects(), " "), mtlsHost), 1)
}

func TestSessionScopedAuthorities(t *testing.T) {
	t.Parallel()
	s := newSessionEnv(t)
	ca := newTestCA(t, "Scoped server CA")
	cert, key := ca.issue(t, mtlsHost, x509.ExtKeyUsageServerAuth, mtlsHost, "outside.test")
	site := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "https://outside.test/", http.StatusFound)
			return
		}
		_, _ = io.WriteString(w, "<html><head><title>Scoped authority</title></head><body>Secure portal</body></html>")
	}))
	site.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{cert.Raw}, PrivateKey: key}}}
	site.StartTLS()
	t.Cleanup(site.Close)
	_, proxyURL := startUpstreamProxy(t, site.Listener.Addr().String())
	s.dagu.WriteFile("ca.pem", ca.pem())
	var home string
	for _, entry := range s.env {
		if value, ok := strings.CutPrefix(entry, "DAGU_HOME="); ok {
			home = value
		}
	}
	writeConfig := func(authorities bool) {
		text := "browser:\n  guard_client_certificates: true\n"
		if authorities {
			text += "  trusted_cas:\n    - hosts: [" + mtlsHost + "]\n      ca_file: " + s.dagu.ProjectPath("ca.pem") + "\n"
		}
		require.NoError(t, os.WriteFile(filepath.Join(home, "config.yaml"), []byte(text), 0o600))
	}
	writeConfig(true)
	opened := s.session("", "open", "https://"+mtlsHost+"/", "--proxy", proxyURL)
	opened.ExpectExitCode(0)
	result := printed(t, opened)
	assert.Equal(t, true, result["tls_verified"])
	assert.Equal(t, "Scoped authority", result["title"])
	id, _ := result["id"].(string)
	require.NotEmpty(t, id)
	t.Cleanup(func() { s.session("", "close", id, "--force") })

	// A redirect signed by the same authority gets no trust outside its scope.
	s.session("", "open", "https://"+mtlsHost+"/redirect", "--proxy", proxyURL).ExpectExitCode(1)
	writeConfig(false)
	s.session(`{"goto":"https://`+mtlsHost+`/again"}`, "do", id).ExpectExitCode(0)
	s.session("", "open", "https://"+mtlsHost+"/", "--proxy", proxyURL).ExpectExitCode(1)
}

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
	cert, key := serverCA.issue(t, mtlsHost, x509.ExtKeyUsageServerAuth, mtlsHost)
	pool := x509.NewCertPool()
	pool.AddCert(clientCA.cert)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, "<!doctype html><title>Portal</title><h1>Hello "+r.TLS.PeerCertificates[0].Subject.CommonName+"</h1>")
	}))
	server.TLS = &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{cert.Raw}, PrivateKey: key, Leaf: cert}},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    pool,
	}
	server.StartTLS()
	t.Cleanup(server.Close)
	return server.Listener.Addr().String()
}

// upstreamProxy is a CONNECT proxy that sends mtlsHost to the local site and
// records the targets it was asked for.
type upstreamProxy struct {
	mu      sync.Mutex
	targets []string
}

func startUpstreamProxy(t *testing.T, siteAddr string) (*upstreamProxy, string) {
	t.Helper()
	proxy := &upstreamProxy{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "CONNECT only", http.StatusMethodNotAllowed)
			return
		}
		proxy.mu.Lock()
		proxy.targets = append(proxy.targets, r.Host)
		proxy.mu.Unlock()
		if r.Host != mtlsHost+":443" {
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
