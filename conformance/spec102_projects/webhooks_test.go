// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package spec102_projects_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/conformance/harness"
	"github.com/stretchr/testify/require"
)

// TestWebhookURLs proves Spec 102 "Webhooks". A v2 server gives a DAG of
// the default project and a library DAG webhooks. With projects on, the
// default project's DAG answers both its v2 URL and its per-project URL,
// neither URL reaches the library DAG, and a URL that names no registered
// project is not found. With projects off, no per-project URL exists.
func TestWebhookURLs(t *testing.T) {
	t.Parallel()

	in := newLibraryInstall(t)
	in.write(t, "dags/plain.yaml", "steps:\n  - run: echo plain\n")
	in.write(t, "library/shared.yaml", "steps:\n  - run: echo shared\n")
	auth := []string{
		"DAGU_AUTH_MODE=builtin",
		"DAGU_AUTH_BUILTIN_INITIAL_ADMIN_USERNAME=admin",
		"DAGU_AUTH_BUILTIN_INITIAL_ADMIN_PASSWORD=adminpass",
	}

	var tokens map[string]string
	in.serve(t, false, auth, func(base string) {
		session := login(t, base)
		tokens = map[string]string{
			"plain":  createWebhook(t, base, session, "plain"),
			"shared": createWebhook(t, base, session, "shared"),
		}
		requireTrigger(t, base, "shared", tokens["shared"], http.StatusOK)
		requireTrigger(t, base, "default/plain", tokens["plain"], http.StatusNotFound)
	})

	in.serve(t, true, auth, func(base string) {
		requireTrigger(t, base, "plain", tokens["plain"], http.StatusOK)
		requireTrigger(t, base, "default/plain", tokens["plain"], http.StatusOK)
		requireTrigger(t, base, "Default/plain", tokens["plain"], http.StatusOK)
		requireTrigger(t, base, "shared", tokens["shared"], http.StatusNotFound)
		requireTrigger(t, base, "default/shared", tokens["shared"], http.StatusNotFound)
		requireTrigger(t, base, "nowhere/plain", tokens["plain"], http.StatusNotFound)
	})
}

// serve runs a server of the install with projects on or off while use
// calls its API at base.
func (in *libraryInstall) serve(t *testing.T, projectsOn bool, extraEnv []string, use func(base string)) {
	t.Helper()
	env := append(slices.Clone(in.env), extraEnv...)
	if projectsOn {
		env = append(env, "DAGU_EXPERIMENTAL_PROJECTS=true")
	}
	port := harness.FreePort(t)
	server := in.dagu.StartWithEnv(env, "server", "--host=127.0.0.1", fmt.Sprintf("--port=%d", port),
		"--dags="+filepath.Join(in.home, "dags"))
	defer server.Stop()
	base := fmt.Sprintf("http://127.0.0.1:%d/api/v1", port)
	waitForHealth(t, server, base, func(body string) bool {
		return strings.Contains(body, `"status":"healthy"`)
	})
	use(base)
}

// login returns a session token of the initial admin.
func login(t *testing.T, base string) string {
	t.Helper()
	code, body := post(t, base+"/auth/login", "", `{"username":"admin","password":"adminpass"}`)
	require.Equal(t, http.StatusOK, code, body)
	var answer struct {
		Token string `json:"token"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &answer))
	return answer.Token
}

// createWebhook gives the DAG a webhook and returns the webhook's token.
func createWebhook(t *testing.T, base, session, dagName string) string {
	t.Helper()
	code, body := post(t, base+"/dags/"+dagName+"/webhook", session, "")
	require.Equal(t, http.StatusCreated, code, body)
	var answer struct {
		Token string `json:"token"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &answer))
	require.NotEmpty(t, answer.Token)
	return answer.Token
}

// requireTrigger posts to the webhook URL /webhooks/<path> with token and
// requires the status code want.
func requireTrigger(t *testing.T, base, path, token string, want int) {
	t.Helper()
	code, body := post(t, base+"/webhooks/"+path, token, `{}`)
	require.Equal(t, want, code, "POST /webhooks/%s: %s", path, body)
}

// post makes a POST request with a JSON body and a bearer token, when
// given, and returns the status code and body.
func post(t *testing.T, url, token, body string) (int, string) {
	t.Helper()
	return authorized(t, http.MethodPost, url, token, body)
}

// authorized makes a request with a JSON body and a bearer token, when given,
// and returns the status code and body.
func authorized(t *testing.T, method, url, token, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, url, strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	// A project delete waits for every process to release the project.
	resp, err := (&http.Client{Timeout: time.Minute}).Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	var answer bytes.Buffer
	_, err = answer.ReadFrom(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, answer.String()
}
