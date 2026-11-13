// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package egress_test

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/runtime/egress"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The real key of the tests. Each test checks that it never leaves the proxy by
// a path other than the request to the upstream.
const testUpstreamKey = "sk-ant-real-key-0123456789"

// proxyLogs collects the lines that the proxy logs.
type proxyLogs struct {
	mu    sync.Mutex
	lines []string
}

func (l *proxyLogs) log(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *proxyLogs) text() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

// received is one request that the origin got.
type received struct {
	Method string
	URI    string
	Host   string
	Header http.Header
	Body   string
}

// recorder is an origin server that records each request.
type recorder struct {
	server *httptest.Server
	mu     sync.Mutex
	reqs   []received
}

func (r *recorder) all() []received {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]received(nil), r.reqs...)
}

func (r *recorder) only(t *testing.T) received {
	t.Helper()
	all := r.all()
	require.Len(t, all, 1)
	return all[0]
}

// startRecorder serves an origin that records the requests and answers ok. A
// handler replaces the answer. It returns the recorder and the address of the
// server. The recorder holds the server.
func startRecorder(t *testing.T, useTLS bool, handler http.HandlerFunc) (rec *recorder, host, port string) {
	t.Helper()
	rec = &recorder{}
	wrapped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rec.mu.Lock()
		rec.reqs = append(rec.reqs, received{Method: r.Method, URI: r.RequestURI, Host: r.Host, Header: r.Header.Clone(), Body: string(body)})
		rec.mu.Unlock()
		if handler != nil {
			handler(w, r)
			return
		}
		_, _ = io.WriteString(w, "ok")
	})
	if useTLS {
		rec.server = httptest.NewTLSServer(wrapped)
	} else {
		rec.server = httptest.NewServer(wrapped)
	}
	t.Cleanup(rec.server.Close)
	host, port, err := net.SplitHostPort(rec.server.Listener.Addr().String())
	require.NoError(t, err)
	return rec, host, port
}

// upstreamProxy serves a proxy with an upstream and returns its address and its
// logs.
func upstreamProxy(t *testing.T, rules []string, upstream *egress.Upstream, resolver egress.Resolver, dial egress.DialFunc, tlsConfig *tls.Config) (string, *proxyLogs) {
	t.Helper()
	parsed, err := egress.ParseRules(rules)
	require.NoError(t, err)
	logs := &proxyLogs{}
	proxy, err := egress.New(egress.Options{Rules: parsed, Resolver: resolver, Dial: dial, Upstream: upstream, Log: logs.log})
	require.NoError(t, err)
	if tlsConfig != nil {
		proxy.SetTLSConfigForTest(tlsConfig)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() { done <- proxy.Serve(listener) }()
	t.Cleanup(func() {
		_ = listener.Close()
		<-done
	})
	return listener.Addr().String(), logs
}

func resolverFor(name, address string) *fakeResolver {
	return &fakeResolver{answers: map[string][]netip.Addr{name: {netip.MustParseAddr(address)}}}
}

func mustUpstream(t *testing.T, rawURL string, auth egress.UpstreamAuth) *egress.Upstream {
	t.Helper()
	upstream, err := egress.NewUpstream(rawURL, auth, testUpstreamKey)
	require.NoError(t, err)
	return upstream
}

// originReply is the answer of the proxy, with the body read and closed.
type originReply struct {
	StatusCode int
	Header     http.Header
	Body       string
}

// originFormRequest sends a request to the proxy as to an origin server: a path
// and no absolute URL, like a client that has ANTHROPIC_BASE_URL.
func originFormRequest(t *testing.T, proxyAddr, method, target, body string, header http.Header) originReply {
	t.Helper()
	request, err := http.NewRequest(method, "http://"+proxyAddr+target, strings.NewReader(body))
	require.NoError(t, err)
	for name, values := range header {
		for _, value := range values {
			request.Header.Add(name, value)
		}
	}
	client := &http.Client{
		Transport:     &http.Transport{DisableKeepAlives: true},
		Timeout:       10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	response, err := client.Do(request)
	require.NoError(t, err)
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	return originReply{StatusCode: response.StatusCode, Header: response.Header, Body: string(data)}
}

// A request in origin form goes to the upstream with the real key. The proxy
// joins the paths, keeps the query, sets the host of the upstream and drops the
// credentials of the client, also when the client hides them in the header
// Connection.
func TestUpstreamInjectsTheKey(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		auth       egress.UpstreamAuth
		wantAPIKey []string
		wantBearer []string
	}{
		{"x-api-key", egress.AuthXAPIKey, []string{testUpstreamKey}, nil},
		{"bearer", egress.AuthBearer, nil, []string{"Bearer " + testUpstreamKey}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec, host, port := startRecorder(t, false, nil)
			upstream := mustUpstream(t, "http://allowed.example.test:"+port+"/base", tc.auth)
			proxyAddr, _ := upstreamProxy(t, []string{"allowed.example.test:" + port}, upstream, resolverFor("allowed.example.test", host), nil, nil)

			reply := originFormRequest(t, proxyAddr, http.MethodPost, "/v1/messages?beta=true&x=1", `{"model":"m"}`, http.Header{
				"X-Api-Key":           {"placeholder", "second"},
				"Authorization":       {"Bearer client-token"},
				"Proxy-Authorization": {"Basic Y2xpZW50"},
				"Proxy-Connection":    {"keep-alive"},
				"Connection":          {"X-Api-Key"},
				"Content-Type":        {"application/json"},
				"Anthropic-Version":   {"2023-06-01"},
			})
			assert.Equal(t, http.StatusOK, reply.StatusCode)
			assert.Equal(t, "ok", reply.Body)

			got := rec.only(t)
			assert.Equal(t, http.MethodPost, got.Method)
			assert.Equal(t, "/base/v1/messages?beta=true&x=1", got.URI)
			assert.Equal(t, "allowed.example.test:"+port, got.Host)
			assert.Equal(t, `{"model":"m"}`, got.Body)
			assert.Equal(t, tc.wantAPIKey, got.Header.Values("X-Api-Key"))
			assert.Equal(t, tc.wantBearer, got.Header.Values("Authorization"))
			assert.Empty(t, got.Header.Values("Proxy-Authorization"))
			assert.Empty(t, got.Header.Values("Proxy-Connection"))
			assert.Equal(t, "application/json", got.Header.Get("Content-Type"), "the other headers pass")
			assert.Equal(t, "2023-06-01", got.Header.Get("Anthropic-Version"), "the other headers pass")
			assert.Empty(t, got.Header.Values("X-Forwarded-For"), "the proxy adds no header that names the client")
		})
	}
}

// The path of the upstream and the path of the request join with one slash. The
// escaped form of the path and a final slash stay as the client wrote them.
func TestUpstreamJoinsThePaths(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name         string
		upstreamPath string
		target       string
		want         string
	}{
		{"no upstream path", "", "/v1/messages", "/v1/messages"},
		{"root upstream path", "/", "/v1/messages", "/v1/messages"},
		{"upstream path", "/base", "/v1/messages", "/base/v1/messages"},
		{"final slashes", "/base/", "/v1/messages/", "/base/v1/messages/"},
		{"request root", "/base", "/", "/base/"},
		{"escaped path", "/base", "/a%2Fb/c%20d", "/base/a%2Fb/c%20d"},
		{"query only", "/base", "/?a=b", "/base/?a=b"},
		{"double slash", "", "//evil.example.test/x", "//evil.example.test/x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec, host, port := startRecorder(t, false, nil)
			upstream := mustUpstream(t, "http://allowed.example.test:"+port+tc.upstreamPath, egress.AuthXAPIKey)
			proxyAddr, _ := upstreamProxy(t, []string{"allowed.example.test:" + port}, upstream, resolverFor("allowed.example.test", host), nil, nil)

			reply := originFormRequest(t, proxyAddr, http.MethodGet, tc.target, "", nil)
			assert.Equal(t, http.StatusOK, reply.StatusCode)
			got := rec.only(t)
			assert.Equal(t, tc.want, got.URI)
			assert.Equal(t, "allowed.example.test:"+port, got.Host, "the host of the request never changes the destination")
		})
	}
}

// The upstream can be an https URL. The proxy opens the TLS connection itself and
// sends the key inside it.
func TestUpstreamOverTLS(t *testing.T) {
	t.Parallel()

	rec, _, port := startRecorder(t, true, nil)
	pool := x509.NewCertPool()
	pool.AddCert(rec.server.Certificate())
	// The test certificate is valid for example.com and for 127.0.0.1.
	upstream := mustUpstream(t, "https://example.com:"+port+"/api", egress.AuthXAPIKey)
	proxyAddr, _ := upstreamProxy(t, []string{"example.com:" + port}, upstream, resolverFor("example.com", "127.0.0.1"), nil, &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12})

	reply := originFormRequest(t, proxyAddr, http.MethodPost, "/v1/messages", "{}", http.Header{"X-Api-Key": {"placeholder"}})
	assert.Equal(t, http.StatusOK, reply.StatusCode)
	assert.Equal(t, "ok", reply.Body)
	got := rec.only(t)
	assert.Equal(t, "/api/v1/messages", got.URI)
	assert.Equal(t, "example.com:"+port, got.Host)
	assert.Equal(t, []string{testUpstreamKey}, got.Header.Values("X-Api-Key"))
}

// A streamed answer reaches the client when the upstream writes it, not when the
// upstream ends it.
func TestUpstreamStreamsTheAnswer(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	_, host, port := startRecorder(t, false, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: one\n\n")
		w.(http.Flusher).Flush()
		<-release
		_, _ = io.WriteString(w, "data: two\n\n")
	})
	upstream := mustUpstream(t, "http://allowed.example.test:"+port, egress.AuthXAPIKey)
	proxyAddr, _ := upstreamProxy(t, []string{"allowed.example.test:" + port}, upstream, resolverFor("allowed.example.test", host), nil, nil)

	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}, Timeout: 10 * time.Second}
	response, err := client.Post("http://"+proxyAddr+"/v1/messages", "application/json", strings.NewReader("{}"))
	require.NoError(t, err)
	defer func() { _ = response.Body.Close() }()
	assert.Equal(t, "text/event-stream", response.Header.Get("Content-Type"))

	reader := bufio.NewReader(response.Body)
	line, err := reader.ReadString('\n')
	require.NoError(t, err, "the first event arrives while the upstream is still open")
	assert.Equal(t, "data: one\n", line)
	_, err = reader.ReadString('\n')
	require.NoError(t, err)

	releaseOnce.Do(func() { close(release) })
	rest, err := io.ReadAll(reader)
	require.NoError(t, err)
	assert.Equal(t, "data: two\n\n", string(rest))
}

// The key goes only through the route of the origin form. A request in absolute
// form to the same destination carries the headers of the client. A destination
// that no rule lists gets 403 for each form, and the proxy dials nothing for it.
func TestUpstreamLeavesTheOtherRoutesAsTheyAre(t *testing.T) {
	t.Parallel()

	rec, host, port := startRecorder(t, false, nil)
	upstream := mustUpstream(t, "http://allowed.example.test:"+port, egress.AuthXAPIKey)
	dials := &dialRecord{}
	proxyAddr, _ := upstreamProxy(t, []string{"allowed.example.test:" + port}, upstream, resolverFor("allowed.example.test", host), dials.dial, nil)
	client := proxyClient(t, proxyAddr)

	request, err := http.NewRequest(http.MethodGet, "http://allowed.example.test:"+port+"/x", nil)
	require.NoError(t, err)
	request.Header.Set("X-Api-Key", "placeholder")
	response, err := client.Do(request)
	require.NoError(t, err)
	_ = response.Body.Close()
	assert.Equal(t, http.StatusOK, response.StatusCode)
	assert.Equal(t, []string{"placeholder"}, rec.only(t).Header.Values("X-Api-Key"), "an absolute URL gets no key")

	before := len(dials.seen())
	response, err = client.Get("http://example.com/")
	require.NoError(t, err)
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	assert.Equal(t, http.StatusForbidden, response.StatusCode)
	assert.Contains(t, string(body), "not in the egress policy")
	assert.NotContains(t, string(body), testUpstreamKey)

	conn, err := net.Dial("tcp", proxyAddr)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	_, err = io.WriteString(conn, "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\n")
	require.NoError(t, err)
	status, err := bufio.NewReader(conn).ReadString('\n')
	require.NoError(t, err)
	assert.Equal(t, "HTTP/1.1 403 Forbidden", strings.TrimSpace(status))
	assert.Len(t, dials.seen(), before, "a denied destination is not dialed")
}

// Without an upstream, a request in origin form gets 400. With an upstream, a
// request target that is not a path gets 400 too.
func TestOriginFormNeedsAnUpstream(t *testing.T) {
	t.Parallel()

	raw := func(proxyAddr, request string) string {
		conn, err := net.Dial("tcp", proxyAddr)
		require.NoError(t, err)
		defer func() { _ = conn.Close() }()
		_, err = io.WriteString(conn, request)
		require.NoError(t, err)
		status, err := bufio.NewReader(conn).ReadString('\n')
		require.NoError(t, err)
		return strings.TrimSpace(status)
	}

	plain := startProxy(t, []string{"allowed.example.test"}, nil, nil)
	assert.Equal(t, "HTTP/1.1 400 Bad Request", raw(plain, "POST /v1/messages HTTP/1.1\r\nHost: x\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"))

	_, host, port := startRecorder(t, false, nil)
	upstream := mustUpstream(t, "http://allowed.example.test:"+port, egress.AuthXAPIKey)
	proxyAddr, _ := upstreamProxy(t, []string{"allowed.example.test:" + port}, upstream, resolverFor("allowed.example.test", host), nil, nil)
	assert.Equal(t, "HTTP/1.1 400 Bad Request", raw(proxyAddr, "GET * HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n"))
	assert.Equal(t, "HTTP/1.1 400 Bad Request", raw(proxyAddr, "GET http:///x HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n"))
}

// The key does not appear in a log, a response body or a response header: not for
// a request that works, not for a refused one and not for a failed one.
func TestUpstreamNeverWritesTheKey(t *testing.T) {
	t.Parallel()

	_, host, port := startRecorder(t, false, nil)
	resolver := &fakeResolver{answers: map[string][]netip.Addr{"allowed.example.test": {netip.MustParseAddr(host)}}}
	upstream := mustUpstream(t, "http://allowed.example.test:"+port, egress.AuthXAPIKey)
	proxyAddr, logs := upstreamProxy(t, []string{"allowed.example.test:" + port}, upstream, resolver, nil, nil)

	// A second proxy whose upstream does not resolve: the request fails.
	failing := mustUpstream(t, "http://gone.example.test:"+port, egress.AuthBearer)
	failProxy, failLogs := upstreamProxy(t, []string{"gone.example.test:" + port}, failing, resolver, nil, nil)

	var texts []string
	collect := func(header http.Header, body string) {
		texts = append(texts, body)
		for name, values := range header {
			texts = append(texts, name+": "+strings.Join(values, ","))
		}
	}
	reply := originFormRequest(t, proxyAddr, http.MethodPost, "/v1/messages", "{}", nil)
	assert.Equal(t, http.StatusOK, reply.StatusCode)
	collect(reply.Header, reply.Body)
	reply = originFormRequest(t, failProxy, http.MethodPost, "/v1/messages", "{}", nil)
	assert.Equal(t, http.StatusBadGateway, reply.StatusCode)
	collect(reply.Header, reply.Body)
	response, err := proxyClient(t, proxyAddr).Get("http://example.com/")
	require.NoError(t, err)
	data, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	assert.Equal(t, http.StatusForbidden, response.StatusCode)
	collect(response.Header, string(data))

	for _, text := range append(texts, logs.text(), failLogs.text()) {
		assert.NotContains(t, text, testUpstreamKey)
		assert.NotContains(t, text, "real-key")
	}
	assert.Contains(t, logs.text(), "allow POST allowed.example.test:"+port+" (upstream)", "the proxy logs the decision")
	assert.NotContains(t, logs.text(), "/v1/messages", "the log has no path, which can hold a secret")
	assert.Contains(t, failLogs.text(), "gone.example.test", "the proxy logs the failure")
}

// A redirect of the upstream goes back to the client. The proxy does not follow
// it, so the key goes to no other destination.
func TestUpstreamRedirectDoesNotLeave(t *testing.T) {
	t.Parallel()

	other, _, otherPort := startRecorder(t, false, nil)
	_, host, port := startRecorder(t, false, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://allowed.example.test:"+otherPort+"/elsewhere", http.StatusFound)
	})
	upstream := mustUpstream(t, "http://allowed.example.test:"+port, egress.AuthXAPIKey)
	proxyAddr, _ := upstreamProxy(t, []string{"allowed.example.test:" + port}, upstream, resolverFor("allowed.example.test", host), nil, nil)

	reply := originFormRequest(t, proxyAddr, http.MethodGet, "/v1/x", "", nil)
	assert.Equal(t, http.StatusFound, reply.StatusCode)
	assert.Contains(t, reply.Header.Get("Location"), "/elsewhere")
	assert.Empty(t, other.all(), "the redirect target gets no request from the proxy")
}

// The upstream MUST match a rule: the host and the port. A wildcard rule that
// covers the host matches.
func TestNewChecksTheUpstreamAgainstTheRules(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		rules []string
		url   string
		ok    bool
	}{
		{"exact", []string{"api.example.com:443"}, "https://api.example.com", true},
		{"no port in the rule", []string{"api.example.com"}, "https://api.example.com:8443", true},
		{"wildcard", []string{"*.example.com"}, "https://api.example.com", true},
		{"other host", []string{"other.example.com:443"}, "https://api.example.com", false},
		{"other port", []string{"api.example.com:8443"}, "https://api.example.com", false},
		{"http default port", []string{"api.example.com:443"}, "http://api.example.com", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rules, err := egress.ParseRules(tc.rules)
			require.NoError(t, err)
			upstream, err := egress.NewUpstream(tc.url, egress.AuthXAPIKey, testUpstreamKey)
			require.NoError(t, err)
			_, err = egress.New(egress.Options{Rules: rules, Upstream: upstream})
			if tc.ok {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, egress.ErrUpstreamNotAllowed)
			assert.NotContains(t, err.Error(), testUpstreamKey)
		})
	}
}
