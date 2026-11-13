// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package egress_test

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/dagucloud/dagu/v2/internal/runtime/egress"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The proxy credential of the tests. Each test checks that it never leaves the
// proxy.
const testProxyCredential = "proxy-credential-0123456789"

// The form anthropic-oauth sends the key as a Bearer token and adds the OAuth beta
// to the betas of the client, once. The log line has the digests of the client
// credential and of the key, never the values.
func TestUpstreamAnthropicOAuth(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		betas  []string
		want   string
		client http.Header
		digest string
	}{
		{"no beta", nil, egress.AnthropicOAuthBeta, http.Header{"X-Api-Key": {"placeholder"}}, "x-api-key sha256:" + egress.Digest("placeholder")},
		{"other betas", []string{"a-1, b-2", "c-3"}, "a-1,b-2,c-3," + egress.AnthropicOAuthBeta,
			http.Header{"Authorization": {"Bearer placeholder"}}, "authorization Bearer sha256:" + egress.Digest("placeholder")},
		{"beta present", []string{"a-1," + egress.AnthropicOAuthBeta}, "a-1," + egress.AnthropicOAuthBeta, nil, "client credential none"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec, host, port := startRecorder(t, false, nil)
			upstream := mustUpstream(t, "http://allowed.example.test:"+port, egress.AuthAnthropicOAuth)
			proxyAddr, logs := upstreamProxy(t, []string{"allowed.example.test:" + port}, upstream, resolverFor("allowed.example.test", host), nil, nil)

			header := tc.client.Clone()
			if header == nil {
				header = http.Header{}
			}
			for _, beta := range tc.betas {
				header.Add("Anthropic-Beta", beta)
			}
			reply := originFormRequest(t, proxyAddr, http.MethodPost, "/v1/messages", "{}", header)
			assert.Equal(t, http.StatusOK, reply.StatusCode)

			got := rec.only(t)
			assert.Equal(t, []string{"Bearer " + testUpstreamKey}, got.Header.Values("Authorization"))
			assert.Empty(t, got.Header.Values("X-Api-Key"))
			assert.Equal(t, []string{tc.want}, got.Header.Values("Anthropic-Beta"))

			text := logs.text()
			assert.Contains(t, text, tc.digest)
			assert.Contains(t, text, "replaced by the key sha256:"+egress.Digest(testUpstreamKey)+" (anthropic-oauth)")
			assert.NotContains(t, text, testUpstreamKey)
			assert.NotContains(t, text, "placeholder")
		})
	}
}

// credentialProxy serves a proxy with plain rules and rules that need the proxy
// credential. It returns its address and its logs.
func credentialProxy(t *testing.T, plain, guarded []string, resolver egress.Resolver, dial egress.DialFunc) (string, *proxyLogs) {
	t.Helper()
	plainRules, err := egress.ParseRules(plain)
	require.NoError(t, err)
	guardedRules, err := egress.ParseRules(guarded)
	require.NoError(t, err)
	logs := &proxyLogs{}
	proxy, err := egress.New(egress.Options{
		Rules: plainRules, CredentialRules: guardedRules, ProxyCredential: testProxyCredential,
		Resolver: resolver, Dial: dial, Log: logs.log,
	})
	require.NoError(t, err)
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

func basic(user, password string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+password))
}

// connectReply is the answer of the proxy to CONNECT. For the status 200, conn and
// reader carry the tunnel.
type connectReply struct {
	status    string
	challenge string
	conn      net.Conn
	reader    *bufio.Reader
}

// connectWith sends CONNECT with the given Proxy-Authorization values and returns
// the answer. The test closes the connection.
func connectWith(t *testing.T, proxyAddr, target string, auth ...string) connectReply {
	t.Helper()
	conn, err := net.Dial("tcp", proxyAddr)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	request := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n", target, target)
	for _, value := range auth {
		request += "Proxy-Authorization: " + value + "\r\n"
	}
	_, err = io.WriteString(conn, request+"\r\n")
	require.NoError(t, err)
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, nil)
	require.NoError(t, err)
	if response.StatusCode != http.StatusOK {
		_ = response.Body.Close()
	}
	return connectReply{status: response.Status, challenge: response.Header.Get("Proxy-Authenticate"), conn: conn, reader: reader}
}

// A destination of CredentialRules needs the proxy credential in the header
// Proxy-Authorization, in the form Basic, with any user name. Without it, with a
// wrong value or with two values, the proxy answers 407 with Proxy-Authenticate
// and dials nothing. With it, CONNECT opens the tunnel and a request in absolute
// form reaches the destination without the header. Other destinations keep their
// rules. The credential is in no log.
func TestCredentialRulesNeedTheProxyCredential(t *testing.T) {
	t.Parallel()

	host, port := originServer(t, "guarded")
	_, plainPort := originServer(t, "plain")
	// Both test servers listen on the same loopback address.
	resolver := resolverFor("guarded.example.test", host)
	resolver.answers["plain.example.test"] = resolver.answers["guarded.example.test"]
	dials := &dialRecord{}
	guarded := "guarded.example.test:" + port
	proxyAddr, logs := credentialProxy(t, []string{"plain.example.test:" + plainPort}, []string{guarded}, resolver, dials.dial)

	for _, tc := range []struct {
		name string
		auth []string
	}{
		{"no credential", nil},
		{"wrong credential", []string{basic("git", "wrong-credential-000000")}},
		{"credential as the user name", []string{basic(testProxyCredential, "")}},
		{"bearer form", []string{"Bearer " + testProxyCredential}},
		{"two headers", []string{basic("git", testProxyCredential), basic("git", testProxyCredential)}},
		{"not base64", []string{"Basic !!!"}},
	} {
		reply := connectWith(t, proxyAddr, guarded, tc.auth...)
		assert.Equal(t, "407 Proxy Authentication Required", reply.status, tc.name)
		assert.Equal(t, `Basic realm="dagu egress proxy"`, reply.challenge, tc.name)
	}
	assert.Empty(t, dials.seen(), "a refused request dials nothing")

	tunnel := connectWith(t, proxyAddr, guarded, basic("anyone", testProxyCredential))
	require.Equal(t, "200 Connection Established", tunnel.status)
	_, err := io.WriteString(tunnel.conn, "GET /x HTTP/1.1\r\nHost: guarded.example.test\r\nConnection: close\r\n\r\n")
	require.NoError(t, err)
	response, err := http.ReadResponse(tunnel.reader, nil)
	require.NoError(t, err)
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	assert.Equal(t, "guarded guarded.example.test ", string(body), "the tunnel reaches the destination")

	request, err := http.NewRequest(http.MethodGet, "http://"+guarded+"/y", nil)
	require.NoError(t, err)
	request.Header.Set("Proxy-Authorization", basic("git", testProxyCredential))
	response, err = proxyClient(t, proxyAddr).Do(request)
	require.NoError(t, err)
	body, _ = io.ReadAll(response.Body)
	_ = response.Body.Close()
	assert.Equal(t, http.StatusOK, response.StatusCode)
	assert.Equal(t, "guarded "+guarded+" ", string(body), "the destination gets no Proxy-Authorization")

	response, err = proxyClient(t, proxyAddr).Get("http://" + guarded + "/z")
	require.NoError(t, err)
	_ = response.Body.Close()
	assert.Equal(t, http.StatusProxyAuthRequired, response.StatusCode, "a request in absolute form needs the credential too")

	assert.Equal(t, "200 Connection Established", connectWith(t, proxyAddr, "plain.example.test:"+plainPort).status,
		"a plain destination needs no credential")
	assert.Equal(t, "403 Forbidden", connectWith(t, proxyAddr, "example.com:443", basic("git", testProxyCredential)).status,
		"the credential opens no other destination")

	text := logs.text()
	assert.Contains(t, text, "deny CONNECT "+guarded+": no valid proxy credential")
	assert.Contains(t, text, "allow CONNECT "+guarded+" (proxy credential)")
	assert.Contains(t, text, "allow GET "+guarded+" (proxy credential)")
	assert.NotContains(t, text, testProxyCredential)
	assert.NotContains(t, text, base64.StdEncoding.EncodeToString([]byte("git:"+testProxyCredential)))
}

// New refuses a list of destinations with a proxy credential without the
// credential, a credential without such a destination, a short credential, a
// credential with a control character, and two lists that can match one
// destination. No error holds the credential.
func TestNewChecksTheCredentialRules(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		plain      []string
		guarded    []string
		credential string
		want       string
	}{
		{"valid", []string{"api.example.com:443"}, []string{"github.com:443"}, testProxyCredential, ""},
		{"other port", []string{"github.com:80"}, []string{"github.com:443"}, testProxyCredential, ""},
		{"sibling wildcards", []string{"*.a.example.com"}, []string{"*.b.example.com"}, testProxyCredential, ""},
		{"no credential", []string{"api.example.com"}, []string{"github.com:443"}, "", "need a proxy credential, got none"},
		{"no rule", []string{"api.example.com"}, nil, testProxyCredential, "needs at least one destination"},
		{"short", []string{"api.example.com"}, []string{"github.com:443"}, "short-0123", "has 10 characters, expected at least 16"},
		{"control character", []string{"api.example.com"}, []string{"github.com:443"}, "proxy-credential\x01-0123", "control character at byte 16"},
		{"same rule", []string{"github.com:443"}, []string{"github.com:443"}, testProxyCredential, `"github.com:443" that needs the proxy credential overlaps the destination "github.com:443"`},
		{"plain rule with no port", []string{"github.com"}, []string{"github.com:443"}, testProxyCredential, "overlaps"},
		{"guarded rule with no port", []string{"github.com:443"}, []string{"github.com"}, testProxyCredential, "overlaps"},
		{"plain wildcard", []string{"*.example.com"}, []string{"git.example.com:443"}, testProxyCredential, "overlaps"},
		{"guarded wildcard", []string{"git.example.com"}, []string{"*.example.com"}, testProxyCredential, "overlaps"},
		{"nested wildcards", []string{"*.example.com"}, []string{"*.git.example.com"}, testProxyCredential, "overlaps"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			plain, err := egress.ParseRules(tc.plain)
			require.NoError(t, err)
			guarded, err := egress.ParseRules(tc.guarded)
			require.NoError(t, err)
			_, err = egress.New(egress.Options{Rules: plain, CredentialRules: guarded, ProxyCredential: tc.credential})
			if tc.want == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, egress.ErrCredentialRules)
			assert.Contains(t, err.Error(), tc.want)
			if tc.credential != "" {
				assert.NotContains(t, err.Error(), strings.TrimSuffix(tc.credential, "-0123"))
			}
		})
	}
}

// The upstream can be a destination of CredentialRules, with no plain rule. The
// route of the upstream works with no proxy credential, because the proxy itself
// dials the upstream. A client that opens a tunnel or sends a request in absolute
// form to the same host needs the credential, so it cannot use the host with a
// key of its own.
func TestUpstreamBehindTheProxyCredential(t *testing.T) {
	t.Parallel()

	rec, host, port := startRecorder(t, false, nil)
	upstream := mustUpstream(t, "http://api.example.test:"+port, egress.AuthAnthropicOAuth)
	guarded, err := egress.ParseRules([]string{"api.example.test:" + port})
	require.NoError(t, err)
	logs := &proxyLogs{}
	proxy, err := egress.New(egress.Options{
		CredentialRules: guarded, ProxyCredential: testProxyCredential, Upstream: upstream,
		Resolver: resolverFor("api.example.test", host), Log: logs.log,
	})
	require.NoError(t, err)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() { done <- proxy.Serve(listener) }()
	t.Cleanup(func() {
		_ = listener.Close()
		<-done
	})
	proxyAddr := listener.Addr().String()

	reply := originFormRequest(t, proxyAddr, http.MethodPost, "/v1/messages", "{}", http.Header{"X-Api-Key": {"placeholder"}})
	assert.Equal(t, http.StatusOK, reply.StatusCode)
	assert.Equal(t, []string{"Bearer " + testUpstreamKey}, rec.only(t).Header.Values("Authorization"))

	assert.Equal(t, "407 Proxy Authentication Required", connectWith(t, proxyAddr, "api.example.test:"+port).status)
	response, err := proxyClient(t, proxyAddr).Get("http://api.example.test:" + port + "/v1/models")
	require.NoError(t, err)
	_ = response.Body.Close()
	assert.Equal(t, http.StatusProxyAuthRequired, response.StatusCode)
	assert.Len(t, rec.all(), 1, "only the route of the upstream reached the host")
	assert.NotContains(t, logs.text(), testUpstreamKey)
}
