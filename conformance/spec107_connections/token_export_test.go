// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec107_connections_test

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/dagucloud/dagu/v2/internal/connection"
)

const (
	storedToken    = "conformance-stored-token"
	refreshedToken = "conformance-refreshed-token"
)

// A connection secret holds a token that stays valid for at least 50
// minutes, or half the token's lifetime when that is shorter. Each stored
// token has 40 minutes left: one of an hour's lifetime is exported as it
// is, and one of two hours' lifetime is refreshed first. The token reaches
// the step, and no output, log, or run status holds it unmasked.
func TestTokenExport(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses POSIX shell snippets")
	}

	tests := []struct {
		name          string
		lifetime      time.Duration
		want          string
		wantRefreshes int32
	}{
		{name: "HalfLifetime", lifetime: time.Hour, want: storedToken},
		{name: "FiftyMinutes", lifetime: 2 * time.Hour, want: refreshedToken, wantRefreshes: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			endpoint, refreshes := newRefreshEndpoint(t)
			env := exportConnection(t, endpoint.URL, true, tt.lifetime)
			dagu := harness.NewRunner(t)
			result := dagu.RunWithEnv(env, "start", "token_export.yaml")
			result.ExpectExitCode(0)

			dagu.ExpectFileContent("token.hex", hex.EncodeToString([]byte(tt.want)))
			require.Equal(t, tt.wantRefreshes, refreshes.Load())
			for _, out := range []string{result.Stdout(), result.Stderr()} {
				require.NotContains(t, out, storedToken)
				require.NotContains(t, out, refreshedToken)
			}
			home := homeOf(env)
			expectMasked(t, home, "token=*******")
			for _, dir := range []string{home, dagu.ProjectPath(".")} {
				expectNoExportedToken(t, dir)
			}
		})
	}
}

// Without allow_token_export, the run fails at secret resolution, naming
// the connection, and no step runs.
func TestTokenExportNotAllowed(t *testing.T) {
	t.Parallel()

	endpoint, refreshes := newRefreshEndpoint(t)
	env := exportConnection(t, endpoint.URL, false, time.Hour)
	dagu := harness.NewRunner(t)
	result := dagu.RunWithEnv(env, "start", "token_export.yaml")
	result.ExpectNonZeroExitCode()
	result.ExpectStderrContains(`connection "work-api" does not allow token export`)
	dagu.ExpectNoFile("token.hex")
	require.Zero(t, refreshes.Load())
}

// exportConnection adds work-api to ops in the home that connections
// makes: an oauth2 connection that refreshes at tokenURL and holds
// storedToken, issued lifetime minus 40 minutes ago. It returns the
// environment that points dagu at the home.
func exportConnection(t *testing.T, tokenURL string, allowExport bool, lifetime time.Duration) []string {
	t.Helper()

	ctx := context.Background()
	env, store := connections(t)
	app, err := store.CreateApp(ctx, &connection.App{
		Name: "api", Provider: connection.ProviderOAuth2, Kind: connection.AppKindBYO,
		ClientID: "client", ClientSecret: "secret", ClientType: connection.ClientTypeWeb,
		Endpoints: &connection.Endpoints{Token: tokenURL},
	})
	require.NoError(t, err)
	conn, err := store.Create(ctx, &connection.Connection{
		Workspace: "ops", Name: "work-api", Provider: connection.ProviderOAuth2,
		App: app.ID, Grant: connection.GrantAuthorizationCode, BoundHosts: []string{"api.example.com"},
		AllowTokenExport: allowExport,
	})
	require.NoError(t, err)
	_, err = store.UpdateTokenState(ctx, conn, func(st *connection.TokenState) error {
		expiresAt := time.Now().Add(40 * time.Minute)
		st.Status = connection.TokenStatusConnected
		st.Generation = 1
		st.RefreshToken = "conformance-refresh-token"
		st.AccessTokens = map[string]connection.AccessToken{
			"": {Token: storedToken, IssuedAt: expiresAt.Add(-lifetime), ExpiresAt: expiresAt},
		}
		return nil
	})
	require.NoError(t, err)
	return env
}

// newRefreshEndpoint grants refreshedToken, valid for two hours, to each
// refresh, counting them.
func newRefreshEndpoint(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var refreshes atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		refreshes.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": refreshedToken, "token_type": "Bearer", "expires_in": 7200})
	}))
	t.Cleanup(srv.Close)
	return srv, &refreshes
}

// homeOf returns the DAGU_HOME that env sets.
func homeOf(env []string) string {
	for _, kv := range env {
		if home, ok := strings.CutPrefix(kv, "DAGU_HOME="); ok {
			return home
		}
	}
	return ""
}

// expectMasked fails unless a file under dir holds text.
func expectMasked(t *testing.T, dir, text string) {
	t.Helper()
	found := false
	walkFiles(t, dir, func(_ string, data []byte) {
		found = found || strings.Contains(string(data), text)
	})
	require.True(t, found, "no file under %s holds %q", dir, text)
}

// expectNoExportedToken fails when a file under dir holds either token.
func expectNoExportedToken(t *testing.T, dir string) {
	t.Helper()
	walkFiles(t, dir, func(path string, data []byte) {
		for _, token := range []string{storedToken, refreshedToken} {
			if strings.Contains(string(data), token) {
				t.Errorf("%s holds a token", path)
			}
		}
	})
}

func walkFiles(t *testing.T, dir string, visit func(path string, data []byte)) {
	t.Helper()
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		visit(path, data)
		return nil
	})
	require.NoError(t, err)
}
