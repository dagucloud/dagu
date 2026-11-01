// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package certproxy

import (
	"bufio"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// testCA issues certificates for tests.
type testCA struct {
	cert *x509.Certificate
	key  crypto.Signer
}

type keyKind int

const (
	ecdsaKey keyKind = iota
	rsaKey
)

// certOptions describes a certificate a testCA issues.
type certOptions struct {
	name      string
	key       keyKind
	dnsNames  []string
	ips       []net.IP
	client    bool
	notBefore time.Time
	notAfter  time.Time
}

func newTestCA(t *testing.T, name string) *testCA {
	t.Helper()
	key := newKey(t, ecdsaKey)
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, key.Public(), key)
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return &testCA{cert: cert, key: key}
}

// newSelfSigned returns a self-signed server certificate for host that is
// not a CA, as an appliance presents, and its key.
func newSelfSigned(t *testing.T, host string) (*x509.Certificate, crypto.Signer) {
	t.Helper()
	key := newKey(t, ecdsaKey)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{host},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, key.Public(), key)
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return cert, key
}

func newKey(t *testing.T, kind keyKind) crypto.Signer {
	t.Helper()
	if kind == rsaKey {
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		require.NoError(t, err)
		return key
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	return key
}

func (ca *testCA) issue(t *testing.T, opts certOptions) (*x509.Certificate, crypto.Signer) {
	t.Helper()
	key := newKey(t, opts.key)
	serial, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	require.NoError(t, err)
	if opts.notBefore.IsZero() {
		opts.notBefore = time.Now().Add(-time.Hour)
	}
	if opts.notAfter.IsZero() {
		opts.notAfter = time.Now().Add(24 * time.Hour)
	}
	usage := x509.ExtKeyUsageServerAuth
	if opts.client {
		usage = x509.ExtKeyUsageClientAuth
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: opts.name},
		NotBefore:    opts.notBefore,
		NotAfter:     opts.notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{usage},
		DNSNames:     opts.dnsNames,
		IPAddresses:  opts.ips,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca.cert, key.Public(), ca.key)
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return cert, key
}

func (ca *testCA) pem() string {
	return certPEM(ca.cert)
}

func (ca *testCA) pool() *x509.CertPool {
	pool := x509.NewCertPool()
	pool.AddCert(ca.cert)
	return pool
}

// clientEntry issues a client certificate for hosts and returns it as an
// Entry.
func (ca *testCA) clientEntry(t *testing.T, label string, hosts ...string) Entry {
	t.Helper()
	cert, key := ca.issue(t, certOptions{name: label, client: true})
	return Entry{Label: label, Hosts: hosts, ChainPEM: certPEM(cert, ca.cert), KeyPEM: keyPEM(t, key)}
}

func certPEM(certs ...*x509.Certificate) string {
	var b strings.Builder
	for _, cert := range certs {
		_ = pem.Encode(&b, &pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
	}
	return b.String()
}

func keyPEM(t *testing.T, key crypto.Signer) string {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

// fakeBrowser answers /json/version as a browser's DevTools endpoint does.
type fakeBrowser struct {
	server *httptest.Server
	id     atomic.Value
	// replaced makes the endpoint answer as a server other than a
	// browser does.
	replaced atomic.Bool
	// answered counts the times the endpoint answered as a browser.
	answered atomic.Int64
}

func newFakeBrowser(t *testing.T) *fakeBrowser {
	t.Helper()
	fake := &fakeBrowser{}
	// Like Chrome, every browser has an identity of its own.
	fake.id.Store(rand.Text())
	fake.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json/version" || fake.replaced.Load() {
			http.NotFound(w, r)
			return
		}
		url := "ws://" + r.Host + "/devtools/browser/" + fake.id.Load().(string)
		_ = json.NewEncoder(w).Encode(map[string]string{"webSocketDebuggerUrl": url})
		fake.answered.Add(1)
	}))
	t.Cleanup(fake.server.Close)
	return fake
}

// serveProxy runs Serve until the test ends.
func serveProxy(t *testing.T, cfg Config) Ready {
	t.Helper()
	return runProxy(t, func(ctx context.Context, ready func(Ready)) error { return Serve(ctx, cfg, ready) })
}

// servePassing is serveProxy with public as the only authority whose sites
// the guard takes as public.
func servePassing(t *testing.T, cfg Config, public *testCA) Ready {
	t.Helper()
	return runProxy(t, func(ctx context.Context, ready func(Ready)) error { return serve(ctx, cfg, public.pool(), ready) })
}

// runProxy runs a proxy with run until the test ends.
func runProxy(t *testing.T, run func(context.Context, func(Ready)) error) Ready {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan Ready, 1)
	// done is closed, not sent on, so both the wait below and the cleanup
	// see Serve return.
	done := make(chan struct{})
	var serveErr error
	go func() {
		defer close(done)
		serveErr = run(ctx, func(r Ready) { ready <- r })
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	select {
	case r := <-ready:
		return r
	case <-done:
		t.Fatalf("Serve returned before it was ready: %v", serveErr)
	case <-time.After(10 * time.Second):
		t.Fatal("Serve was not ready in time")
	}
	return Ready{}
}

// testConfig returns a configuration serving entries for a fake browser.
func testConfig(t *testing.T, systemPAC string, entries ...Entry) Config {
	t.Helper()
	return Config{Token: rand.Text(), CDPURL: newFakeBrowser(t).server.URL, SystemPAC: systemPAC, Entries: entries}
}

// connectThrough opens a CONNECT tunnel to target through the HTTP proxy
// at proxyAddr and returns the status it answered.
func connectThrough(proxyAddr, target string) (net.Conn, int, error) {
	conn, err := net.DialTimeout("tcp", proxyAddr, 5*time.Second)
	if err != nil {
		return nil, 0, err
	}
	if _, err := fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target); err != nil {
		_ = conn.Close()
		return nil, 0, err
	}
	response, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodConnect})
	if err != nil {
		_ = conn.Close()
		return nil, 0, err
	}
	_ = response.Body.Close()
	return conn, response.StatusCode, nil
}

// browserClient is an HTTP client that reaches every server through the
// proxy, trusting only certificates with the proxy's key, as a browser
// given the proxy's flags does.
func browserClient(ready Ready, protocols ...string) *http.Client {
	return browserClientAt(ready.Port, ready, protocols...)
}

// browserClientAt is browserClient for the proxy's port port, such as its
// guard's.
func browserClientAt(port int, ready Ready, protocols ...string) *http.Client {
	proxyAddr := fmt.Sprintf("127.0.0.1:%d", port)
	transport := &http.Transport{
		ForceAttemptHTTP2: true,
		DialTLSContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
			conn, status, err := connectThrough(proxyAddr, addr)
			if err != nil {
				return nil, err
			}
			if status != http.StatusOK {
				_ = conn.Close()
				return nil, fmt.Errorf("CONNECT %s: %d", addr, status)
			}
			host, _, _ := net.SplitHostPort(addr)
			tlsConn := tls.Client(conn, &tls.Config{
				ServerName:            host,
				NextProtos:            protocols,
				InsecureSkipVerify:    true, //nolint:gosec // the certificate is checked by its key below
				VerifyPeerCertificate: pinnedKey(ready.SPKI),
			})
			if err := tlsConn.HandshakeContext(ctx); err != nil {
				_ = conn.Close()
				return nil, err
			}
			return tlsConn, nil
		},
	}
	return &http.Client{Transport: transport, Timeout: 20 * time.Second}
}

// pinnedKey accepts only a leaf whose public key hashes to spki.
func pinnedKey(spki string) func([][]byte, [][]*x509.Certificate) error {
	return func(raw [][]byte, _ [][]*x509.Certificate) error {
		leaf, err := x509.ParseCertificate(raw[0])
		if err != nil {
			return err
		}
		sum := sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
		if base64.StdEncoding.EncodeToString(sum[:]) != spki {
			return errors.New("the certificate does not carry the proxy's key")
		}
		return nil
	}
}

// tunnelProxy is an HTTP proxy that tunnels every CONNECT to one address,
// whatever the request names, and records what was requested.
type tunnelProxy struct {
	addr      string
	mu        sync.Mutex
	requested []string
}

func newTunnelProxy(t *testing.T, target string, status int) *tunnelProxy {
	t.Helper()
	p := &tunnelProxy{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		p.requested = append(p.requested, r.Host)
		p.mu.Unlock()
		if r.Method != http.MethodConnect || status != http.StatusOK {
			if status == http.StatusProxyAuthRequired {
				w.Header().Set("Proxy-Authenticate", `Basic realm="corp"`)
			}
			w.WriteHeader(status)
			return
		}
		upstream, err := net.Dial("tcp", target)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			_ = upstream.Close()
			return
		}
		_, _ = io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n")
		pipe(conn, upstream)
	}))
	t.Cleanup(server.Close)
	p.addr = server.Listener.Addr().String()
	return p
}

func (p *tunnelProxy) requests() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.requested...)
}

// socksProxy is a SOCKS5 and SOCKS4 server, without authentication, that
// connects every request to one address and records the address requested.
type socksProxy struct {
	addr      string
	mu        sync.Mutex
	requested []string
}

func newSOCKSProxy(t *testing.T, target string) *socksProxy {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	p := &socksProxy{addr: listener.Addr().String()}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go p.serve(conn, target)
		}
	}()
	return p
}

func (p *socksProxy) serve(conn net.Conn, target string) {
	defer func() { _ = conn.Close() }()
	reader := bufio.NewReader(conn)
	// Greeting: version, method count, methods; answer "no authentication".
	header := make([]byte, 2)
	if _, err := io.ReadFull(reader, header); err != nil {
		return
	}
	if header[0] == 4 {
		p.serve4(conn, reader, target)
		return
	}
	if _, err := io.ReadFull(reader, make([]byte, header[1])); err != nil {
		return
	}
	_, _ = conn.Write([]byte{5, 0})
	// Request: version, CONNECT, reserved, address type, address, port.
	request := make([]byte, 4)
	if _, err := io.ReadFull(reader, request); err != nil {
		return
	}
	var host string
	switch request[3] {
	case 1:
		addr := make([]byte, 4)
		_, _ = io.ReadFull(reader, addr)
		host = net.IP(addr).String()
	case 3:
		length, _ := reader.ReadByte()
		name := make([]byte, length)
		_, _ = io.ReadFull(reader, name)
		host = string(name)
	default:
		return
	}
	port := make([]byte, 2)
	if _, err := io.ReadFull(reader, port); err != nil {
		return
	}
	p.mu.Lock()
	p.requested = append(p.requested, net.JoinHostPort(host, fmt.Sprint(int(port[0])<<8|int(port[1]))))
	p.mu.Unlock()
	upstream, err := net.Dial("tcp", target)
	if err != nil {
		_, _ = conn.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	_, _ = conn.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0})
	pipe(&readerConn{Conn: conn, reader: reader}, upstream)
}

// serve4 answers a SOCKS4 request past its version and command: the port,
// the IPv4 address, and a user ID ending in a zero byte.
func (p *socksProxy) serve4(conn net.Conn, reader *bufio.Reader, target string) {
	request := make([]byte, 6)
	if _, err := io.ReadFull(reader, request); err != nil {
		return
	}
	if _, err := reader.ReadBytes(0); err != nil {
		return
	}
	p.mu.Lock()
	p.requested = append(p.requested, net.JoinHostPort(net.IP(request[2:]).String(), fmt.Sprint(int(request[0])<<8|int(request[1]))))
	p.mu.Unlock()
	upstream, err := net.Dial("tcp", target)
	if err != nil {
		_, _ = conn.Write([]byte{0, 91, 0, 0, 0, 0, 0, 0})
		return
	}
	_, _ = conn.Write([]byte{0, 90, 0, 0, 0, 0, 0, 0})
	pipe(&readerConn{Conn: conn, reader: reader}, upstream)
}

func (p *socksProxy) requests() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.requested...)
}

type readerConn struct {
	net.Conn
	reader io.Reader
}

func (c *readerConn) Read(b []byte) (int, error) { return c.reader.Read(b) }

// pipe copies between a and b until either ends.
func pipe(a, b net.Conn) {
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(a, b); done <- struct{}{} }()
	go func() { _, _ = io.Copy(b, a); done <- struct{}{} }()
	<-done
	_ = a.Close()
	_ = b.Close()
	<-done
}

// waitForEvent polls the proxy's events until one of kind arrives.
func waitForEvent(t *testing.T, ready Ready, token, kind string) Event {
	t.Helper()
	var found Event
	require.Eventually(t, func() bool {
		events, err := Events(context.Background(), controlURL(ready.Port, token), 0)
		if err != nil {
			return false
		}
		for _, event := range events {
			if event.Kind == kind {
				found = event
				return true
			}
		}
		return false
	}, 10*time.Second, 50*time.Millisecond, "no %s event", kind)
	return found
}
