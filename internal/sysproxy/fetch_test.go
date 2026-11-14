// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package sysproxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testPAC = `function FindProxyForURL(url, host) {
  if (dnsDomainIs(host, ".corp.example")) return "PROXY proxy.corp.example:8080";
  return "DIRECT";
}
`

// servePAC serves body at /proxy.pac and returns the script's URL.
func servePAC(t *testing.T, body string) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/proxy.pac" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/x-ns-proxy-autoconfig")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server.URL + "/proxy.pac"
}

// fileURL returns the file URL of a local path.
func fileURL(path string) string {
	slashed := filepath.ToSlash(path)
	if !strings.HasPrefix(slashed, "/") {
		slashed = "/" + slashed
	}
	return "file://" + slashed
}

func TestFetchPAC(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("HTTP", func(t *testing.T) {
		t.Parallel()
		script, err := fetchPAC(ctx, servePAC(t, testPAC))
		require.NoError(t, err)
		assert.Equal(t, testPAC, script)
	})

	t.Run("File", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "proxy.pac")
		require.NoError(t, os.WriteFile(path, []byte(testPAC), 0o600))
		script, err := fetchPAC(ctx, fileURL(path))
		require.NoError(t, err)
		assert.Equal(t, testPAC, script)
	})

	t.Run("Oversize", func(t *testing.T) {
		t.Parallel()
		body := testPAC + strings.Repeat("//", maxPACSize/2)
		_, err := fetchPAC(ctx, servePAC(t, body))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "larger than")
	})

	t.Run("NotFound", func(t *testing.T) {
		t.Parallel()
		_, err := fetchPAC(ctx, strings.TrimSuffix(servePAC(t, testPAC), "/proxy.pac")+"/missing.pac")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "404")
	})

	t.Run("NotAScript", func(t *testing.T) {
		t.Parallel()
		_, err := fetchPAC(ctx, servePAC(t, "<html>Sign in to the network</html>"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not a proxy auto-config script")
	})

	t.Run("UnsupportedScheme", func(t *testing.T) {
		t.Parallel()
		_, err := fetchPAC(ctx, "ftp://pac.example/proxy.pac")
		assert.Error(t, err)
	})
}

func TestSystemConfigPACWhenScriptUnreachable(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.NotFoundHandler())
	scriptURL := server.URL + "/proxy.pac"
	server.Close()

	script, warnings := systemConfig{
		autoConfigURL: scriptURL,
		static:        staticConfig{fallback: []proxyServer{{kind: httpProxy, host: "proxy.example", port: "8080"}}},
	}.pac(context.Background(), nil)
	assert.Equal(t, directPAC, script)
	require.Len(t, warnings, 1)
	assert.True(t, strings.HasPrefix(warnings[0], "could not read the proxy auto-config "+scriptURL+": "), warnings[0])
	assert.True(t, strings.HasSuffix(warnings[0], "; requests go DIRECT"), warnings[0])
}
