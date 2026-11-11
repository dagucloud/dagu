// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// errorHolds reports whether msg holds part as it is, or as %q prints it. An
// error of the command quotes a path with %q, and %q escapes the backslashes
// of a Windows path.
func errorHolds(msg, part string) bool {
	return strings.Contains(msg, part) || strings.Contains(msg, strings.Trim(strconv.Quote(part), `"`))
}

func keyFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "key")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

// The options of the upstream have a closed configuration too. An option that
// needs --upstream, no source or two sources of the key, a source that gives no
// key and a key that a header cannot carry stop the command before it serves. The
// error names the option and the variable or the path, and never holds the key.
// The test sets variables, so it does not run in parallel.
func TestEgressProxyCommandRejectsABadUpstream(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		path := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
		return path
	}
	emptyFile := write("empty", "")
	newlineFile := write("newline", "\n")
	crlfFile := write("crlf", "\r\n")
	controlFile := write("control", "sk-secret\x01tail\n")
	twoLinesFile := write("two_lines", "sk-secret\nsecond\n")
	twoLineEndsFile := write("two_line_ends", "sk-secret\n\n")
	bigFile := write("big", strings.Repeat("k", 16*1024+1))
	goodFile := write("good", "sk-secret\n")
	missingFile := filepath.Join(dir, "missing")
	const upstream = "https://api.example.com"
	base := []string{"serve", "--listen", "127.0.0.1:1", "--allow=api.example.com:443"}
	with := func(extra ...string) []string { return append(append([]string(nil), base...), extra...) }

	for _, tc := range []struct {
		name   string
		args   []string
		env    map[string]string
		want   []string
		absent []string
	}{
		{"auth without upstream", with("--upstream-auth", "bearer"), nil, []string{"flag --upstream-auth", "needs the flag --upstream", `"bearer"`}, nil},
		{"key env without upstream", with("--upstream-key-env", "SOME_VAR"), nil, []string{"flag --upstream-key-env", "needs the flag --upstream", `"SOME_VAR"`}, nil},
		{"key file without upstream", with("--upstream-key-file", goodFile), nil, []string{"flag --upstream-key-file", "needs the flag --upstream", goodFile}, []string{"sk-secret"}},
		{"upstream without a key", with("--upstream", upstream), nil, []string{"--upstream-key-env", "--upstream-key-file", "got none"}, nil},
		{"upstream with two keys", with("--upstream", upstream, "--upstream-key-env", "SOME_VAR", "--upstream-key-file", goodFile), nil, []string{"give exactly one", "--upstream-key-env", "--upstream-key-file"}, []string{"sk-secret"}},
		{"empty upstream", with("--upstream", "", "--upstream-key-file", goodFile), nil, []string{"flag --upstream", "http or https", `""`}, []string{"sk-secret"}},
		{"upstream scheme", with("--upstream", "ftp://api.example.com", "--upstream-key-file", goodFile), nil, []string{"flag --upstream", "http or https", `"ftp://api.example.com"`}, []string{"sk-secret"}},
		{"upstream query", with("--upstream", upstream+"/v1?a=b", "--upstream-key-file", goodFile), nil, []string{"flag --upstream", "no query and no fragment", "a=b"}, []string{"sk-secret"}},
		{"upstream fragment", with("--upstream", upstream+"#x", "--upstream-key-file", goodFile), nil, []string{"flag --upstream", "no query and no fragment"}, []string{"sk-secret"}},
		{"upstream not allowed", with("--upstream", "https://other.example.com", "--upstream-key-file", goodFile), nil, []string{"flag --upstream", "--allow", "other.example.com:443"}, []string{"sk-secret"}},
		{"upstream port not allowed", with("--upstream", "http://api.example.com", "--upstream-key-file", goodFile), nil, []string{"flag --upstream", "--allow", "api.example.com:80"}, []string{"sk-secret"}},
		{"unknown auth", with("--upstream", upstream, "--upstream-auth", "basic", "--upstream-key-file", goodFile), nil, []string{"flag --upstream-auth", `"x-api-key"`, `"bearer"`, `"basic"`}, []string{"sk-secret"}},
		{"env name empty", with("--upstream", upstream, "--upstream-key-env="), nil, []string{"flag --upstream-key-env", "name of a variable"}, nil},
		{"env absent", with("--upstream", upstream, "--upstream-key-env", "DAGU_TEST_EGRESS_KEY_ABSENT"), nil, []string{"flag --upstream-key-env", `"DAGU_TEST_EGRESS_KEY_ABSENT"`, "does not exist"}, nil},
		{"env empty", with("--upstream", upstream, "--upstream-key-env", "DAGU_TEST_EGRESS_KEY_EMPTY"), map[string]string{"DAGU_TEST_EGRESS_KEY_EMPTY": ""}, []string{"flag --upstream-key-env", `"DAGU_TEST_EGRESS_KEY_EMPTY"`, "is empty"}, nil},
		{"env with a control character", with("--upstream", upstream, "--upstream-key-env", "DAGU_TEST_EGRESS_KEY_BAD"), map[string]string{"DAGU_TEST_EGRESS_KEY_BAD": "sk-secret\ttail"}, []string{"flag --upstream-key-env", "control character at byte 9"}, []string{"sk-secret", "tail"}},
		{"file name empty", with("--upstream", upstream, "--upstream-key-file="), nil, []string{"flag --upstream-key-file", "path of a file"}, nil},
		{"file missing", with("--upstream", upstream, "--upstream-key-file", missingFile), nil, []string{"flag --upstream-key-file", missingFile}, nil},
		{"file is a directory", with("--upstream", upstream, "--upstream-key-file", dir), nil, []string{"flag --upstream-key-file", dir}, nil},
		{"file empty", with("--upstream", upstream, "--upstream-key-file", emptyFile), nil, []string{"flag --upstream-key-file", emptyFile, "is empty"}, nil},
		{"file with a line end only", with("--upstream", upstream, "--upstream-key-file", newlineFile), nil, []string{"flag --upstream-key-file", newlineFile, "is empty"}, nil},
		{"file with a CRLF only", with("--upstream", upstream, "--upstream-key-file", crlfFile), nil, []string{"flag --upstream-key-file", crlfFile, "is empty"}, nil},
		{"file with a control character", with("--upstream", upstream, "--upstream-key-file", controlFile), nil, []string{"flag --upstream-key-file", "control character at byte 9"}, []string{"sk-secret", "tail"}},
		{"file with two lines", with("--upstream", upstream, "--upstream-key-file", twoLinesFile), nil, []string{"flag --upstream-key-file", "control character at byte 9"}, []string{"sk-secret", "second"}},
		{"file with two line ends", with("--upstream", upstream, "--upstream-key-file", twoLineEndsFile), nil, []string{"flag --upstream-key-file", "control character at byte 9"}, []string{"sk-secret"}},
		{"file too large", with("--upstream", upstream, "--upstream-key-file", bigFile), nil, []string{"flag --upstream-key-file", bigFile, "larger than 16384 bytes"}, []string{"kkkk"}},
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
			for _, part := range tc.absent {
				assert.NotContains(t, err.Error(), part)
			}
		})
	}
}

// serve with --upstream sends a request in origin form to the upstream with the
// key. The key comes from a variable or from a file, and the last line end of the
// file is not part of the key. The key of a bearer upstream goes in the header
// authorization. The test sets a variable, so it does not run in parallel.
func TestEgressProxyCommandInjectsTheKey(t *testing.T) {
	for _, tc := range []struct {
		name    string
		auth    string
		options func(t *testing.T) []string
		header  string
		want    string
	}{
		{"env", "x-api-key", func(t *testing.T) []string {
			t.Setenv("DAGU_TEST_EGRESS_KEY", "sk-from-env")
			return []string{"--upstream-key-env", "DAGU_TEST_EGRESS_KEY"}
		}, "X-Api-Key", "sk-from-env"},
		{"file with LF", "x-api-key", func(t *testing.T) []string {
			return []string{"--upstream-key-file", keyFile(t, "sk-from-file\n")}
		}, "X-Api-Key", "sk-from-file"},
		{"file with CRLF", "x-api-key", func(t *testing.T) []string {
			return []string{"--upstream-key-file", keyFile(t, "sk-from-file\r\n")}
		}, "X-Api-Key", "sk-from-file"},
		{"file with no line end", "x-api-key", func(t *testing.T) []string {
			return []string{"--upstream-key-file", keyFile(t, "sk-from-file")}
		}, "X-Api-Key", "sk-from-file"},
		{"bearer", "bearer", func(t *testing.T) []string {
			return []string{"--upstream-key-file", keyFile(t, "sk-bearer\n")}
		}, "Authorization", "Bearer sk-bearer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			var mu sync.Mutex
			var headers []http.Header
			var uris []string
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				headers = append(headers, r.Header.Clone())
				uris = append(uris, r.RequestURI)
				mu.Unlock()
				_, _ = io.WriteString(w, "ok")
			}))
			t.Cleanup(origin.Close)
			originAddr := origin.Listener.Addr().String()

			proxyAddr := freePort(t)
			args := append([]string{"egress-proxy", "serve", "--listen", proxyAddr, "--allow=" + originAddr,
				"--upstream", "http://" + originAddr + "/base", "--upstream-auth", tc.auth}, tc.options(t)...)
			root := egressRoot()
			root.SetOut(io.Discard)
			root.SetErr(io.Discard)
			root.SetArgs(args)
			done := make(chan error, 1)
			go func() { done <- root.ExecuteContext(ctx) }()

			waitRoot := egressRoot()
			waitRoot.SetArgs([]string{"egress-proxy", "wait", "--addr", proxyAddr, "--timeout", "10s"})
			require.NoError(t, waitRoot.ExecuteContext(t.Context()))

			request, err := http.NewRequest(http.MethodPost, "http://"+proxyAddr+"/v1/messages?beta=true", strings.NewReader("{}"))
			require.NoError(t, err)
			request.Header.Set("X-Api-Key", "placeholder")
			request.Header.Set("Authorization", "Bearer placeholder")
			client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}, Timeout: 10 * time.Second}
			response, err := client.Do(request)
			require.NoError(t, err)
			_ = response.Body.Close()
			assert.Equal(t, http.StatusOK, response.StatusCode)

			mu.Lock()
			require.Len(t, headers, 1)
			assert.Equal(t, []string{"/base/v1/messages?beta=true"}, uris)
			assert.Equal(t, []string{tc.want}, headers[0].Values(tc.header))
			other := "Authorization"
			if tc.header == "Authorization" {
				other = "X-Api-Key"
			}
			assert.Empty(t, headers[0].Values(other))
			mu.Unlock()

			cancel()
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Fatal("the command does not end when its context ends")
			}
		})
	}
}
