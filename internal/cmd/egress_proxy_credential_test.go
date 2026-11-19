// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd_test

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testCredential = "proxy-credential-0123456789"

// The options of the proxy credential have a closed configuration. A source
// without --allow-with-credential, no source or two sources, a source that gives
// no value, a short value and a destination that --allow also lists stop the
// command before it serves. No error holds the credential. The test sets
// variables, so it does not run in parallel.
func TestEgressProxyCommandRejectsABadProxyCredential(t *testing.T) {
	good := keyFile(t, testCredential+"\n")
	short := keyFile(t, "short\n")
	missing := filepath.Join(t.TempDir(), "missing")
	base := []string{"serve", "--listen", "127.0.0.1:1", "--allow=api.example.com:443"}
	with := func(extra ...string) []string { return append(append([]string(nil), base...), extra...) }

	for _, tc := range []struct {
		name string
		args []string
		env  map[string]string
		want []string
	}{
		{"env without destination", with("--proxy-credential-env", "SOME_VAR"), nil, []string{"flag --proxy-credential-env", "needs the flag --allow-with-credential", `"SOME_VAR"`}},
		{"file without destination", with("--proxy-credential-file", good), nil, []string{"flag --proxy-credential-file", "needs the flag --allow-with-credential", good}},
		{"destination without credential", with("--allow-with-credential", "github.com:443"), nil, []string{"flag --allow-with-credential", "--proxy-credential-env", "--proxy-credential-file", "got none"}},
		{"two sources", with("--allow-with-credential", "github.com:443", "--proxy-credential-env", "SOME_VAR", "--proxy-credential-file", good), nil, []string{"give exactly one", "--proxy-credential-env", "--proxy-credential-file"}},
		{"bad destination", with("--allow-with-credential", "https://github.com", "--proxy-credential-file", good), nil, []string{"flag --allow-with-credential", `"https://github.com"`}},
		{"env absent", with("--allow-with-credential", "github.com:443", "--proxy-credential-env", "DAGU_TEST_PROXY_CREDENTIAL_ABSENT"), nil, []string{"flag --proxy-credential-env", "does not exist"}},
		{"env empty", with("--allow-with-credential", "github.com:443", "--proxy-credential-env", "DAGU_TEST_PROXY_CREDENTIAL_EMPTY"), map[string]string{"DAGU_TEST_PROXY_CREDENTIAL_EMPTY": ""}, []string{"flag --proxy-credential-env", "is empty"}},
		{"file missing", with("--allow-with-credential", "github.com:443", "--proxy-credential-file", missing), nil, []string{"flag --proxy-credential-file", missing}},
		{"short", with("--allow-with-credential", "github.com:443", "--proxy-credential-file", short), nil, []string{"flag --allow-with-credential", "has 5 characters, expected at least 16"}},
		{"overlap", with("--allow-with-credential", "api.example.com", "--proxy-credential-file", good), nil, []string{"flag --allow-with-credential", `"api.example.com" that needs the proxy credential overlaps the destination "api.example.com:443"`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for name, value := range tc.env {
				t.Setenv(name, value)
			}
			root := egressRoot()
			root.SetOut(io.Discard)
			root.SetErr(io.Discard)
			root.SetArgs(append([]string{"egress-proxy"}, tc.args...))
			err := root.ExecuteContext(t.Context())
			require.Error(t, err)
			for _, part := range tc.want {
				assert.True(t, errorHolds(err.Error(), part), "the error %q does not hold %q", err.Error(), part)
			}
			assert.NotContains(t, err.Error(), testCredential)
			assert.NotContains(t, err.Error(), "short\n")
		})
	}
}

// serve with --allow-with-credential opens a tunnel to the destination only for
// a client that sends the proxy credential. The credential comes from a variable
// or from a file, without the last line end of the file. The test sets a
// variable, so it does not run in parallel.
func TestEgressProxyCommandNeedsTheProxyCredential(t *testing.T) {
	for _, tc := range []struct {
		name    string
		options func(t *testing.T) []string
	}{
		{"env", func(t *testing.T) []string {
			t.Setenv("DAGU_TEST_PROXY_CREDENTIAL", testCredential)
			return []string{"--proxy-credential-env", "DAGU_TEST_PROXY_CREDENTIAL"}
		}},
		{"file", func(t *testing.T) []string {
			return []string{"--proxy-credential-file", keyFile(t, testCredential+"\n")}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, "origin ok")
			}))
			t.Cleanup(origin.Close)
			originAddr := origin.Listener.Addr().String()

			proxyAddr := freePort(t)
			args := append([]string{"egress-proxy", "serve", "--listen", proxyAddr, "--allow=other.example.com:443",
				"--allow-with-credential", originAddr}, tc.options(t)...)
			root := egressRoot()
			root.SetOut(io.Discard)
			root.SetErr(io.Discard)
			root.SetArgs(args)
			done := make(chan error, 1)
			go func() { done <- root.ExecuteContext(ctx) }()

			waitRoot := egressRoot()
			waitRoot.SetArgs([]string{"egress-proxy", "wait", "--addr", proxyAddr, "--timeout", "10s"})
			require.NoError(t, waitRoot.ExecuteContext(t.Context()))

			connect := func(auth string) string {
				conn, err := net.Dial("tcp", proxyAddr)
				require.NoError(t, err)
				defer func() { _ = conn.Close() }()
				request := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n", originAddr, originAddr)
				if auth != "" {
					request += "Proxy-Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte("git:"+auth)) + "\r\n"
				}
				_, err = io.WriteString(conn, request+"\r\n")
				require.NoError(t, err)
				status, err := bufio.NewReader(conn).ReadString('\n')
				require.NoError(t, err)
				return status
			}
			assert.Equal(t, "HTTP/1.1 407 Proxy Authentication Required\r\n", connect(""))
			assert.Equal(t, "HTTP/1.1 407 Proxy Authentication Required\r\n", connect("wrong-credential-0000"))
			assert.Equal(t, "HTTP/1.1 200 Connection Established\r\n", connect(testCredential))

			cancel()
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Fatal("the command does not end when its context ends")
			}
		})
	}
}
