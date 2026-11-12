// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec092_agent_workers_test

import (
	"bufio"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The key injection of the egress proxy (Spec 092, "The egress proxy"). The tests
// start the real binary as `dagu egress-proxy serve`, with a local origin server
// as the upstream. The key files are in testdata, with the prefix proxy_.

// proxyOrigin is an origin server that records the requests that it gets.
type proxyOrigin struct {
	server *httptest.Server
	addr   string
	mu     sync.Mutex
	seen   []proxyRequest
}

type proxyRequest struct {
	method string
	uri    string
	host   string
	header http.Header
}

func (o *proxyOrigin) requests() []proxyRequest {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]proxyRequest(nil), o.seen...)
}

func newProxyOrigin(t *testing.T, useTLS bool) *proxyOrigin {
	t.Helper()
	origin := &proxyOrigin{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin.mu.Lock()
		origin.seen = append(origin.seen, proxyRequest{method: r.Method, uri: r.RequestURI, host: r.Host, header: r.Header.Clone()})
		origin.mu.Unlock()
		_, _ = io.WriteString(w, "origin ok")
	})
	if useTLS {
		origin.server = httptest.NewTLSServer(handler)
	} else {
		origin.server = httptest.NewServer(handler)
	}
	t.Cleanup(origin.server.Close)
	origin.addr = origin.server.Listener.Addr().String()
	return origin
}

// proxyStart starts `dagu egress-proxy serve` and waits until it accepts
// connections. It returns the address of the proxy and the process.
func proxyStart(t *testing.T, runner *harness.Runner, env []string, args ...string) (string, *harness.Process) {
	t.Helper()
	addr := fmt.Sprintf("127.0.0.1:%d", harness.FreePort(t))
	command := append([]string{"egress-proxy", "serve", "--listen", addr}, args...)
	process := runner.StartWithEnv(env, command...)
	deadline := time.Now().Add(harness.WaitTimeout(t))
	for {
		conn, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			_ = conn.Close()
			return addr, process
		}
		select {
		case <-process.Done():
			t.Fatalf("the proxy ended before it served:\n%s", process.FailureOutput())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("the proxy does not accept connections on %s:\n%s", addr, process.Output())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// proxyOriginForm sends a request to the proxy as to an origin server: a path and
// no absolute URL, as a client does with ANTHROPIC_BASE_URL.
func proxyOriginForm(t *testing.T, proxyAddr, method, target string, header http.Header) (int, string) {
	t.Helper()
	request, err := http.NewRequest(method, "http://"+proxyAddr+target, strings.NewReader("{}"))
	require.NoError(t, err)
	for name, values := range header {
		for _, value := range values {
			request.Header.Add(name, value)
		}
	}
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}, Timeout: 20 * time.Second}
	response, err := client.Do(request)
	require.NoError(t, err)
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	return response.StatusCode, string(body)
}

// proxyThroughProxy sends a request in absolute form through the proxy, as a client
// does with HTTP_PROXY.
func proxyThroughProxy(t *testing.T, proxyAddr, target string, header http.Header) (int, string) {
	t.Helper()
	proxyURL, err := url.Parse("http://" + proxyAddr)
	require.NoError(t, err)
	request, err := http.NewRequest(http.MethodGet, target, nil)
	require.NoError(t, err)
	request.Header = header.Clone()
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL), DisableKeepAlives: true}, Timeout: 20 * time.Second}
	response, err := client.Do(request)
	require.NoError(t, err)
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	return response.StatusCode, string(body)
}

// proxyConnectStatus sends a CONNECT request and returns the status line.
func proxyConnectStatus(t *testing.T, proxyAddr, target string) string {
	t.Helper()
	conn, err := net.Dial("tcp", proxyAddr)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	_, err = fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target)
	require.NoError(t, err)
	status, err := bufio.NewReader(conn).ReadString('\n')
	require.NoError(t, err)
	return strings.TrimSpace(status)
}

// A request in origin form goes to the upstream with the real key from the file,
// without its last line end. The path joins the path of the upstream, the query
// stays, the placeholder of the client is gone, and the key is in no output of
// the proxy. A request in absolute form to a destination that the proxy does not
// list gets the status 403, as does a CONNECT request.
func TestProxyInjectsTheKeyFromAFile(t *testing.T) {
	t.Parallel()

	origin := newProxyOrigin(t, false)
	runner := harness.NewRunner(t)
	proxyAddr, process := proxyStart(t, runner, nil,
		"--allow", origin.addr,
		"--upstream", "http://"+origin.addr+"/base",
		"--upstream-key-file", "proxy_key_lf.txt")

	status, body := proxyOriginForm(t, proxyAddr, http.MethodPost, "/v1/messages?beta=true", http.Header{
		"X-Api-Key":           {"placeholder"},
		"Authorization":       {"Bearer placeholder"},
		"Proxy-Authorization": {"Basic cGxhY2Vob2xkZXI="},
	})
	assert.Equal(t, http.StatusOK, status)
	assert.Equal(t, "origin ok", body)
	requests := origin.requests()
	require.Len(t, requests, 1)
	assert.Equal(t, http.MethodPost, requests[0].method)
	assert.Equal(t, "/base/v1/messages?beta=true", requests[0].uri)
	assert.Equal(t, origin.addr, requests[0].host, "the host is the host of the upstream")
	assert.Equal(t, []string{"sk-conformance-file-key"}, requests[0].header.Values("X-Api-Key"))
	assert.Empty(t, requests[0].header.Values("Authorization"))
	assert.Empty(t, requests[0].header.Values("Proxy-Authorization"))

	// The key goes only through the route of the origin form.
	status, _ = proxyThroughProxy(t, proxyAddr, "http://"+origin.addr+"/direct", http.Header{"X-Api-Key": {"placeholder"}})
	assert.Equal(t, http.StatusOK, status)
	requests = origin.requests()
	require.Len(t, requests, 2)
	assert.Equal(t, []string{"placeholder"}, requests[1].header.Values("X-Api-Key"), "a request in absolute form gets no key")

	status, body = proxyThroughProxy(t, proxyAddr, "http://example.com/", nil)
	assert.Equal(t, http.StatusForbidden, status)
	assert.NotContains(t, body, "sk-conformance")
	assert.Equal(t, "HTTP/1.1 403 Forbidden", proxyConnectStatus(t, proxyAddr, "example.com:443"))
	assert.Len(t, origin.requests(), 2, "a refused request reaches no origin")

	assert.NotContains(t, process.Output(), "sk-conformance", "the proxy never writes the key")
}

// The key can come from a variable. The form bearer puts it in the header
// authorization.
func TestProxyInjectsTheKeyFromTheEnvironment(t *testing.T) {
	t.Parallel()

	origin := newProxyOrigin(t, false)
	runner := harness.NewRunner(t)
	proxyAddr, process := proxyStart(t, runner, []string{"PROXY_CONFORMANCE_KEY=sk-conformance-env-key"},
		"--allow", origin.addr,
		"--upstream", "http://"+origin.addr,
		"--upstream-auth", "bearer",
		"--upstream-key-env", "PROXY_CONFORMANCE_KEY")

	status, _ := proxyOriginForm(t, proxyAddr, http.MethodGet, "/v1/models", http.Header{"X-Api-Key": {"placeholder"}})
	assert.Equal(t, http.StatusOK, status)
	requests := origin.requests()
	require.Len(t, requests, 1)
	assert.Equal(t, "/v1/models", requests[0].uri)
	assert.Equal(t, []string{"Bearer sk-conformance-env-key"}, requests[0].header.Values("Authorization"))
	assert.Empty(t, requests[0].header.Values("X-Api-Key"))
	assert.NotContains(t, process.Output(), "sk-conformance", "the proxy never writes the key")
}

// An https upstream works: the proxy opens the TLS connection and sends the key
// inside it. The test gives the certificate of the origin to the Go runtime of the
// proxy with the variable of the system roots. Only Linux reads that variable.
func TestProxyInjectsTheKeyOverTLS(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "linux" {
		t.Skip("the test trusts its certificate with SSL_CERT_FILE, which only Linux reads")
	}
	origin := newProxyOrigin(t, true)
	runner := harness.NewRunner(t)
	certFile := filepath.Join(t.TempDir(), "origin.pem")
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: origin.server.Certificate().Raw})
	require.NoError(t, os.WriteFile(certFile, pemBytes, 0o600))

	proxyAddr, process := proxyStart(t, runner, []string{"SSL_CERT_FILE=" + certFile},
		"--allow", origin.addr,
		"--upstream", "https://"+origin.addr+"/api",
		"--upstream-key-file", "proxy_key_lf.txt")

	status, body := proxyOriginForm(t, proxyAddr, http.MethodPost, "/v1/messages", http.Header{"X-Api-Key": {"placeholder"}})
	assert.Equal(t, http.StatusOK, status, "the proxy output:\n%s", process.Output())
	assert.Equal(t, "origin ok", body)
	requests := origin.requests()
	require.Len(t, requests, 1)
	assert.Equal(t, "/api/v1/messages", requests[0].uri)
	assert.Equal(t, []string{"sk-conformance-file-key"}, requests[0].header.Values("X-Api-Key"))
	assert.NotContains(t, process.Output(), "sk-conformance", "the proxy never writes the key")
}

// The form anthropic-oauth puts the key in the header authorization and adds the
// OAuth beta to the betas of the client. The log line of the request has the
// digests of the placeholder and of the key, and no value.
func TestProxyAnthropicOAuthAddsTheBeta(t *testing.T) {
	t.Parallel()

	origin := newProxyOrigin(t, false)
	runner := harness.NewRunner(t)
	proxyAddr, process := proxyStart(t, runner, nil,
		"--allow", origin.addr,
		"--upstream", "http://"+origin.addr,
		"--upstream-auth", "anthropic-oauth",
		"--upstream-key-file", "proxy_key_lf.txt")

	status, _ := proxyOriginForm(t, proxyAddr, http.MethodPost, "/v1/messages", http.Header{
		"X-Api-Key":      {"placeholder"},
		"Anthropic-Beta": {"claude-code-20250219"},
	})
	assert.Equal(t, http.StatusOK, status)
	requests := origin.requests()
	require.Len(t, requests, 1)
	assert.Equal(t, []string{"Bearer sk-conformance-file-key"}, requests[0].header.Values("Authorization"))
	assert.Empty(t, requests[0].header.Values("X-Api-Key"))
	assert.Equal(t, []string{"claude-code-20250219,oauth-2025-04-20"}, requests[0].header.Values("Anthropic-Beta"))
	output := process.Output()
	// 4097889236a2 is the start of the SHA-256 of "placeholder".
	assert.Contains(t, output, "client credential x-api-key sha256:4097889236a2 replaced by the key sha256:")
	assert.Contains(t, output, "(anthropic-oauth)")
	assert.NotContains(t, output, "sk-conformance", "the proxy never writes the key")
	assert.NotContains(t, output, "placeholder")
}

// A destination of --allow-with-credential needs the proxy credential in the header
// Proxy-Authorization (Basic). Without it, the proxy answers 407. With it, CONNECT
// opens the tunnel, and a request in absolute form reaches the destination without
// the header. The upstream can be such a destination: the route of the upstream
// needs no credential, a tunnel to the same host needs it.
func TestProxyCredentialDestinations(t *testing.T) {
	t.Parallel()

	origin := newProxyOrigin(t, false)
	runner := harness.NewRunner(t)
	proxyAddr, process := proxyStart(t, runner, nil,
		"--allow-with-credential", origin.addr,
		"--proxy-credential-file", "proxy_credential_lf.txt",
		"--upstream", "http://"+origin.addr,
		"--upstream-key-file", "proxy_key_lf.txt")
	credential := "Basic " + base64.StdEncoding.EncodeToString([]byte("git:conformance-proxy-credential-0123"))

	assert.Equal(t, "HTTP/1.1 407 Proxy Authentication Required", proxyConnectStatus(t, proxyAddr, origin.addr))
	status, _ := proxyThroughProxy(t, proxyAddr, "http://"+origin.addr+"/direct", nil)
	assert.Equal(t, http.StatusProxyAuthRequired, status)
	assert.Empty(t, origin.requests(), "a refused request reaches no origin")

	status, _ = proxyThroughProxy(t, proxyAddr, "http://"+origin.addr+"/direct", http.Header{"Proxy-Authorization": {credential}})
	assert.Equal(t, http.StatusOK, status)
	requests := origin.requests()
	require.Len(t, requests, 1)
	assert.Empty(t, requests[0].header.Values("Proxy-Authorization"), "the destination never gets the credential")

	status, _ = proxyOriginForm(t, proxyAddr, http.MethodPost, "/v1/messages", http.Header{"X-Api-Key": {"placeholder"}})
	assert.Equal(t, http.StatusOK, status, "the route of the upstream needs no credential")
	require.Len(t, origin.requests(), 2)
	assert.Equal(t, []string{"sk-conformance-file-key"}, origin.requests()[1].header.Values("X-Api-Key"))

	assert.Equal(t, "HTTP/1.1 403 Forbidden", proxyConnectStatus(t, proxyAddr, "example.com:443"))
	output := process.Output()
	assert.Contains(t, output, "no valid proxy credential")
	assert.NotContains(t, output, "conformance-proxy-credential", "the proxy never writes the credential")
	assert.NotContains(t, output, "sk-conformance", "the proxy never writes the key")
}

// Without --upstream, a request in origin form gets the status 400.
func TestProxyWithoutUpstreamRefusesOriginForm(t *testing.T) {
	t.Parallel()

	origin := newProxyOrigin(t, false)
	runner := harness.NewRunner(t)
	proxyAddr, _ := proxyStart(t, runner, nil, "--allow", origin.addr)

	status, _ := proxyOriginForm(t, proxyAddr, http.MethodPost, "/v1/messages", nil)
	assert.Equal(t, http.StatusBadRequest, status)
	status, _ = proxyThroughProxy(t, proxyAddr, "http://"+origin.addr+"/ok", nil)
	assert.Equal(t, http.StatusOK, status, "the route of the absolute form still works")
	assert.Len(t, origin.requests(), 1)
}

// The options of the upstream stop the proxy at start when they are not valid. The
// message names the option and the variable or the path, and it never holds the
// key.
func TestProxyStartErrors(t *testing.T) {
	t.Parallel()

	const allow = "api.example.com:443"
	for _, tc := range []struct {
		name   string
		env    []string
		args   []string
		want   []string
		absent []string
	}{
		{"upstream without a key", nil,
			[]string{"--upstream", "https://api.example.com"},
			[]string{"--upstream-key-env", "--upstream-key-file", "got none"}, nil},
		{"upstream with two keys", []string{"PROXY_CONFORMANCE_KEY=sk-conformance-env-key"},
			[]string{"--upstream", "https://api.example.com", "--upstream-key-env", "PROXY_CONFORMANCE_KEY", "--upstream-key-file", "proxy_key_lf.txt"},
			[]string{"give exactly one"}, []string{"sk-conformance"}},
		{"key option without upstream", nil,
			[]string{"--upstream-key-file", "proxy_key_lf.txt"},
			[]string{"flag --upstream-key-file", "needs the flag --upstream", "proxy_key_lf.txt"}, []string{"sk-conformance"}},
		{"auth without upstream", nil,
			[]string{"--upstream-auth", "bearer"},
			[]string{"flag --upstream-auth", "needs the flag --upstream", `"bearer"`}, nil},
		{"unknown auth", nil,
			[]string{"--upstream", "https://api.example.com", "--upstream-auth", "basic", "--upstream-key-file", "proxy_key_lf.txt"},
			[]string{"flag --upstream-auth", `"x-api-key"`, `"bearer"`, `"basic"`}, []string{"sk-conformance"}},
		{"upstream scheme", nil,
			[]string{"--upstream", "ftp://api.example.com", "--upstream-key-file", "proxy_key_lf.txt"},
			[]string{"flag --upstream", "http or https", `"ftp://api.example.com"`}, []string{"sk-conformance"}},
		{"upstream query", nil,
			[]string{"--upstream", "https://api.example.com/v1?a=b", "--upstream-key-file", "proxy_key_lf.txt"},
			[]string{"flag --upstream", "no query and no fragment"}, []string{"sk-conformance"}},
		{"upstream not in allow", nil,
			[]string{"--upstream", "https://other.example.com", "--upstream-key-file", "proxy_key_lf.txt"},
			[]string{"flag --upstream", "--allow", "other.example.com:443"}, []string{"sk-conformance"}},
		{"upstream port not in allow", nil,
			[]string{"--upstream", "http://api.example.com", "--upstream-key-file", "proxy_key_lf.txt"},
			[]string{"flag --upstream", "--allow", "api.example.com:80"}, []string{"sk-conformance"}},
		{"variable absent", nil,
			[]string{"--upstream", "https://api.example.com", "--upstream-key-env", "PROXY_CONFORMANCE_ABSENT"},
			[]string{"flag --upstream-key-env", `"PROXY_CONFORMANCE_ABSENT"`, "does not exist"}, nil},
		{"variable empty", []string{"PROXY_CONFORMANCE_EMPTY="},
			[]string{"--upstream", "https://api.example.com", "--upstream-key-env", "PROXY_CONFORMANCE_EMPTY"},
			[]string{"flag --upstream-key-env", `"PROXY_CONFORMANCE_EMPTY"`, "is empty"}, nil},
		{"variable with a control character", []string{"PROXY_CONFORMANCE_BAD=sk-conformance-bad\tkey"},
			[]string{"--upstream", "https://api.example.com", "--upstream-key-env", "PROXY_CONFORMANCE_BAD"},
			[]string{"flag --upstream-key-env", "control character"}, []string{"sk-conformance"}},
		{"file missing", nil,
			[]string{"--upstream", "https://api.example.com", "--upstream-key-file", "proxy_key_missing.txt"},
			[]string{"flag --upstream-key-file", "proxy_key_missing.txt"}, nil},
		{"file empty", nil,
			[]string{"--upstream", "https://api.example.com", "--upstream-key-file", "proxy_key_empty.txt"},
			[]string{"flag --upstream-key-file", "proxy_key_empty.txt", "is empty"}, nil},
		{"file with a line end only", nil,
			[]string{"--upstream", "https://api.example.com", "--upstream-key-file", "proxy_key_newline.txt"},
			[]string{"flag --upstream-key-file", "proxy_key_newline.txt", "is empty"}, nil},
		{"file with two lines", nil,
			[]string{"--upstream", "https://api.example.com", "--upstream-key-file", "proxy_key_two_lines.txt"},
			[]string{"flag --upstream-key-file", "control character"}, []string{"sk-conformance", "second-line"}},
		{"credential without a destination", nil,
			[]string{"--proxy-credential-file", "proxy_credential_lf.txt"},
			[]string{"flag --proxy-credential-file", "needs the flag --allow-with-credential"}, []string{"conformance-proxy-credential"}},
		{"destination without a credential", nil,
			[]string{"--allow-with-credential", "github.com:443"},
			[]string{"flag --allow-with-credential", "--proxy-credential-env", "--proxy-credential-file", "got none"}, nil},
		{"destination in both lists", nil,
			[]string{"--allow-with-credential", "api.example.com", "--proxy-credential-file", "proxy_credential_lf.txt"},
			[]string{"flag --allow-with-credential", "overlaps", `"api.example.com:443"`}, []string{"conformance-proxy-credential"}},
		{"short credential", nil,
			[]string{"--allow-with-credential", "github.com:443", "--proxy-credential-file", "proxy_credential_short.txt"},
			[]string{"flag --allow-with-credential", "has 6 characters, expected at least 16"}, []string{"short-"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			runner := harness.NewRunner(t)
			args := append([]string{"egress-proxy", "serve", "--listen", fmt.Sprintf("127.0.0.1:%d", harness.FreePort(t)), "--allow", allow}, tc.args...)
			result := runner.RunWithEnv(tc.env, args...)
			result.ExpectExitCode(1)
			result.ExpectStderrContains(tc.want...)
			result.ExpectStderrNotContains(tc.absent...)
			result.ExpectStdoutNotContains(tc.absent...)
		})
	}
}
