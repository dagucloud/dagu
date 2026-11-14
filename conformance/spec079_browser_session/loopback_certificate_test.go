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
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
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

// A private authority verifies loopback and localhost destinations, which
// Chrome does not send through a PAC script, and does not verify another host.
func TestLoopbackServerAuthority(t *testing.T) {
	t.Parallel()
	s := newSessionEnv(t)
	ca := newTestCA(t, "Loopback CA")
	certificate := ca.issueServer(t, []string{"localhost", "portal.localhost"}, []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")})
	v4 := serveCertificate(t, "127.0.0.1:0", certificate, nil, "Loopback portal")
	v6 := serveCertificate(t, "[::1]:0", certificate, nil, "Loopback portal")
	_, v6Port, err := net.SplitHostPort(v6)
	require.NoError(t, err)
	s.dagu.WriteFile("ca.pem", ca.pem())
	writeBrowserConfig(t, s.env, "browser:\n  trusted_cas:\n    - hosts: [127.0.0.1, localhost, \"::1\", portal.localhost]\n      ca_file: "+s.dagu.ProjectPath("ca.pem")+"\n")

	id, opened := openSession(t, s, "https://"+v4+"/")
	assert.Equal(t, true, opened["tls_verified"])
	assert.Equal(t, "Loopback portal", opened["title"])
	for _, rawURL := range []string{
		"https://localhost:" + v6Port + "/",
		"https://[::1]:" + v6Port + "/",
		"https://portal.localhost:" + v6Port + "/",
	} {
		s.session(`{"goto":"`+rawURL+`"}`, "do", id).ExpectExitCode(0)
		s.session(`{"expect":{"text":"Loopback portal"}}`, "do", id).ExpectExitCode(0)
	}
}

// A client certificate is presented to a loopback site.
func TestLoopbackClientCertificate(t *testing.T) {
	t.Parallel()
	s := newSessionEnv(t)
	clientCA, serverCA := newTestCA(t, "Loopback Client CA"), newTestCA(t, "Loopback Server CA")
	certificate := serverCA.issueServer(t, []string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")})
	v4 := serveCertificate(t, "127.0.0.1:0", certificate, clientCA.cert, "Hello")
	v6 := serveCertificate(t, "[::1]:0", certificate, clientCA.cert, "Hello")
	_, v6Port, err := net.SplitHostPort(v6)
	require.NoError(t, err)
	cert, key := clientCA.issue(t, "client-1", x509.ExtKeyUsageClientAuth)
	data, err := pkcs12.Modern.Encode(key, cert, nil, certificatePassphrase)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(s.dagu.ProjectPath("client.p12"), data, 0o600))
	s.dagu.WriteFile("client.passphrase", certificatePassphrase+"\n")
	s.dagu.WriteFile("ca.pem", serverCA.pem())
	writeBrowserConfig(t, s.env, fmt.Sprintf(`browser:
  client_certificates:
    - hosts: ["127.0.0.1", "localhost", "::1"]
      pfx_file: %s
      passphrase_file: %s
      ca_file: %s
`, s.dagu.ProjectPath("client.p12"), s.dagu.ProjectPath("client.passphrase"), s.dagu.ProjectPath("ca.pem")))

	id, opened := openSession(t, s, "https://"+v4+"/")
	assert.Contains(t, fmt.Sprint(opened["outline"]), "Hello client-1")
	for _, rawURL := range []string{"https://localhost:" + v6Port + "/", "https://[::1]:" + v6Port + "/"} {
		s.session(`{"goto":"`+rawURL+`"}`, "do", id).ExpectExitCode(0)
		s.session(`{"expect":{"text":"Hello client-1"}}`, "do", id).ExpectExitCode(0)
	}
}

// Loopback destinations bypass the configured proxy, as Chrome's own route
// does. Trust and routing for a host the loopback authority does not name
// stay as they were: the browser rejects its certificate, and the device
// proxy still carries its HTTPS and plain HTTP requests.
func TestLoopbackTrustStaysOnItsHost(t *testing.T) {
	t.Parallel()
	s := newSessionEnv(t)
	loopbackCA, otherCA := newTestCA(t, "Loopback CA"), newTestCA(t, "Other CA")
	loopbackCert := loopbackCA.issueServer(t, []string{"portal.localhost"}, []net.IP{net.ParseIP("127.0.0.1")})
	otherCert := otherCA.issueServer(t, []string{"outside.test"}, nil)
	v4 := serveCertificate(t, "127.0.0.1:0", loopbackCert, nil, "Loopback portal")
	other := serveCertificate(t, "127.0.0.1:0", otherCert, nil, "Outside")
	plain := strings.TrimPrefix(httptestNew(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, "<!doctype html><title>Outside plain</title><h1>Outside plain</h1>")
	}), "http://")
	proxy, proxyURL := startHostProxy(t, map[string]string{"outside.test:443": other, "outside.test:80": plain})
	s.dagu.WriteFile("ca.pem", loopbackCA.pem())
	writeBrowserConfig(t, s.env, "browser:\n  trusted_cas:\n    - hosts: [127.0.0.1, portal.localhost]\n      ca_file: "+s.dagu.ProjectPath("ca.pem")+"\n")

	id, opened := openSession(t, s, "https://"+v4+"/", "--proxy", proxyURL)
	assert.Equal(t, true, opened["tls_verified"])
	assert.Equal(t, "Loopback portal", opened["title"])
	s.session(`{"goto":"https://portal.localhost:`+portOf(t, v4)+`/"}`, "do", id).ExpectExitCode(0)
	s.session(`{"expect":{"text":"Loopback portal"}}`, "do", id).ExpectExitCode(0)
	assert.NotContains(t, proxy.requests(), "127.0.0.1:"+portOf(t, v4))
	assert.NotContains(t, proxy.requests(), "portal.localhost:"+portOf(t, v4))

	visited := s.session(`{"goto":"https://outside.test/"}`, "do", id)
	visited.ExpectExitCode(0)
	assert.Contains(t, fmt.Sprint(printed(t, visited)["outline"]), "ERR_CERT_AUTHORITY_INVALID")
	assert.Contains(t, proxy.requests(), "outside.test:443")

	s.session(`{"goto":"http://outside.test/"}`, "do", id).ExpectExitCode(0)
	s.session(`{"expect":{"text":"Outside plain"}}`, "do", id).ExpectExitCode(0)
	assert.Contains(t, proxy.requests(), "http://outside.test/")
}

func (ca *testCA) issueServer(t *testing.T, dns []string, ips []net.IP) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	serial, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "loopback"},
		DNSNames:     dns,
		IPAddresses:  ips,
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca.cert, &key.PublicKey, ca.key)
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: cert}
}

// serveCertificate serves greeting on address and returns host:port.
func serveCertificate(t *testing.T, address string, certificate tls.Certificate, clientCA *x509.Certificate, greeting string) string {
	t.Helper()
	listener, err := net.Listen("tcp", address)
	require.NoError(t, err)
	pool := x509.NewCertPool()
	auth := tls.NoClientCert
	if clientCA != nil {
		pool.AddCert(clientCA)
		auth = tls.RequireAndVerifyClientCert
	}
	config := &tls.Config{Certificates: []tls.Certificate{certificate}, ClientAuth: auth, ClientCAs: pool, MinVersion: tls.VersionTLS12}
	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			name := "anonymous"
			if r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
				name = r.TLS.PeerCertificates[0].Subject.CommonName
			}
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprintf(w, "<!doctype html><title>%s</title><h1>%s %s</h1>", greeting, greeting, name)
		}),
		TLSConfig: config,
		ErrorLog:  log.New(io.Discard, "", 0),
	}
	t.Cleanup(func() { _ = server.Close() })
	go func() { _ = server.Serve(tls.NewListener(listener, config)) }()
	return listener.Addr().String()
}

func writeBrowserConfig(t *testing.T, env []string, body string) {
	t.Helper()
	var home string
	for _, entry := range env {
		if value, ok := strings.CutPrefix(entry, "DAGU_HOME="); ok {
			home = value
		}
	}
	require.NotEmpty(t, home)
	require.NoError(t, os.WriteFile(filepath.Join(home, "config.yaml"), []byte(body), 0o600))
}

// openSession opens rawURL and closes the session when the test ends.
// extra is appended to the open command, such as a proxy flag.
func openSession(t *testing.T, s *sessionEnv, rawURL string, extra ...string) (string, map[string]any) {
	t.Helper()
	opened := s.session("", append([]string{"open", rawURL}, extra...)...)
	opened.ExpectExitCode(0)
	result := printed(t, opened)
	id, _ := result["id"].(string)
	require.NotEmpty(t, id, opened.Stdout())
	t.Cleanup(func() { s.session("", "close", id, "--force") })
	return id, result
}

func portOf(t *testing.T, hostport string) string {
	t.Helper()
	_, port, err := net.SplitHostPort(hostport)
	require.NoError(t, err)
	return port
}

// startHostProxy sends each host and port to the local address sites
// names: a CONNECT tunnel, or a plain HTTP request in absolute form. It
// records the CONNECT authorities and the plain URLs requested.
func startHostProxy(t *testing.T, sites map[string]string) (*hostProxy, string) {
	t.Helper()
	proxy := &hostProxy{}
	server := httptestNew(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			proxy.record(r.URL.String())
			port := r.URL.Port()
			if port == "" {
				port = "80"
			}
			siteAddr, ok := sites[net.JoinHostPort(r.URL.Hostname(), port)]
			if !ok {
				http.Error(w, "unknown host", http.StatusBadGateway)
				return
			}
			httputil.NewSingleHostReverseProxy(&url.URL{Scheme: "http", Host: siteAddr}).ServeHTTP(w, r)
			return
		}
		proxy.record(r.Host)
		siteAddr, ok := sites[r.Host]
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
	})
	return proxy, server
}

func httptestNew(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	server := &http.Server{Handler: handler}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = server.Close() })
	go func() { _ = server.Serve(listener) }()
	return "http://" + listener.Addr().String()
}

type hostProxy struct {
	mu      sync.Mutex
	targets []string
}

func (p *hostProxy) record(target string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.targets = append(p.targets, target)
}

func (p *hostProxy) requests() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.targets...)
}
