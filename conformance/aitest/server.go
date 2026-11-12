// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package aitest holds the helpers that the conformance suites of the AI feature share: the
// suites of Spec 093 (AI endpoints), Spec 094 (AI gateway) and Spec 095 (MCP servers). A suite
// starts a `dagu server`, a distributed stack with a coordinator and workers, or a local
// project, with the fake claude CLI that this package installs in the project of the runner.
package aitest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

// Server is a running `dagu server` and a client of its REST API. It keeps each
// response body, so that a test can check that no body holds a key.
type Server struct {
	t     *testing.T
	base  string
	proc  *harness.Process
	token string

	// Home is the data directory of the server.
	Home string

	mu     sync.Mutex
	bodies []string
}

// hermeticConfig is the configuration of a server that must not call a provider. The
// first read of a models list goes to the proxy, which refuses it, and the
// automatic refresh is off. A test that wants the server to call a fake provider
// uses StartServerWithConfig.
const hermeticConfig = `ai:
  models_refresh_hours: 0
  upstream:
    proxy_url: http://127.0.0.1:9
`

// StartServer starts `dagu server` on a free port with its own data directory. The
// server has no authentication unless env sets one. It never calls a real provider.
func StartServer(t *testing.T, dagu *harness.Runner, env []string, args ...string) *Server {
	t.Helper()
	return StartServerWithConfig(t, dagu, hermeticConfig, env, args...)
}

// StartServerWithConfig starts `dagu server` with the given configuration file.
func StartServerWithConfig(t *testing.T, dagu *harness.Runner, config string, env []string, args ...string) *Server {
	t.Helper()

	port := harness.FreePort(t)
	home := filepath.Join(t.TempDir(), "dagu")
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configPath, []byte(config), 0o600))
	cmd := append([]string{"server", "--host=127.0.0.1", fmt.Sprintf("--port=%d", port), "--config", configPath}, args...)
	s := &Server{
		t:    t,
		base: fmt.Sprintf("http://127.0.0.1:%d/api/v1", port),
		Home: home,
		proc: dagu.StartWithEnv(append([]string{"DAGU_HOME=" + home}, env...), cmd...),
	}

	deadline := time.Now().Add(harness.WaitTimeout(t))
	for time.Now().Before(deadline) {
		select {
		case <-s.proc.Done():
			require.FailNowf(t, "the server exited at start", "%s", s.proc.FailureOutput())
		default:
		}
		resp, err := http.Get(s.base + "/health") //nolint:gosec,noctx
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return s
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	require.FailNow(t, "the server did not answer", "%s", s.proc.Output())
	return nil
}

// Call sends one request and returns the status and the body. A nil body sends
// no body. The call uses the bearer token of the server when there is one.
func (s *Server) Call(method, path string, body any) (int, string) {
	s.t.Helper()
	return s.CallWithToken(s.token, method, path, body)
}

// CallWithToken sends one request with the given bearer token.
func (s *Server) CallWithToken(token, method, path string, body any) (int, string) {
	s.t.Helper()

	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		require.NoError(s.t, err)
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(s.t.Context(), method, s.base+path, reader)
	require.NoError(s.t, err)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	require.NoError(s.t, err)
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	require.NoError(s.t, err)

	s.mu.Lock()
	s.bodies = append(s.bodies, string(data))
	s.mu.Unlock()
	return resp.StatusCode, string(data)
}

// Decode reads the JSON of a body into v.
func Decode(t *testing.T, body string, v any) {
	t.Helper()
	require.NoError(t, json.Unmarshal([]byte(body), v), "body: %s", body)
}

// Login signs in with the administrator of the configuration and keeps the token.
func (s *Server) Login(username, password string) {
	s.t.Helper()
	status, body := s.CallWithToken("", http.MethodPost, "/auth/login", map[string]string{"username": username, "password": password})
	require.Equal(s.t, http.StatusOK, status, body)
	var login struct {
		Token string `json:"token"`
	}
	Decode(s.t, body, &login)
	require.NotEmpty(s.t, login.Token)
	s.token = login.Token
}

// AssertNoKey checks that the key is in no response, in no output of the server
// and in no file of its home directory. The store encrypts the key, so the
// plain text is nowhere.
func (s *Server) AssertNoKey(keys ...string) {
	s.t.Helper()

	s.mu.Lock()
	bodies := append([]string(nil), s.bodies...)
	s.mu.Unlock()
	output := s.proc.Output()
	for _, key := range keys {
		for _, body := range bodies {
			require.NotContains(s.t, body, key, "a response holds the key")
		}
		require.NotContains(s.t, output, key, "the output of the server holds the key")
	}

	files := 0
	require.NoError(s.t, filepath.WalkDir(s.Home, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		data, readErr := os.ReadFile(path) //nolint:gosec
		if readErr != nil {
			return nil
		}
		files++
		for _, key := range keys {
			if strings.Contains(string(data), key) {
				s.t.Errorf("the file %s holds the key", path)
			}
		}
		return nil
	}))
	require.Positive(s.t, files, "the home directory has files")
}
