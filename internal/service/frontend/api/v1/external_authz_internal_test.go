// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	generatedapi "github.com/dagucloud/dagu/v2/api/v1"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/service/frontend/extauthz"
	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// externalTestPDP allows the "subject action resource-id" triples in allow
// and records every question it receives.
type externalTestPDP struct {
	t     *testing.T
	mu    sync.Mutex
	allow map[string]bool
	asked []string
}

func (p *externalTestPDP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	type res struct {
		Type       string         `json:"type"`
		ID         string         `json:"id"`
		Properties map[string]any `json:"properties"`
	}
	var req struct {
		Subject     struct{ ID string }      `json:"subject"`
		Action      struct{ Name string }    `json:"action"`
		Resource    res                      `json:"resource"`
		Evaluations []struct{ Resource res } `json:"evaluations"`
	}
	assert.NoError(p.t, json.NewDecoder(r.Body).Decode(&req))

	p.mu.Lock()
	defer p.mu.Unlock()
	decide := func(rs res) bool {
		runID, _ := rs.Properties["run_id"].(string)
		key := req.Subject.ID + " " + req.Action.Name + " " + rs.Type + ":" + rs.ID
		if runID != "" {
			key += "#" + runID
		}
		p.asked = append(p.asked, key)
		return p.allow[key]
	}
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == "/access/v1/evaluations" {
		out := make([]map[string]bool, 0, len(req.Evaluations))
		for _, e := range req.Evaluations {
			out = append(out, map[string]bool{"decision": decide(e.Resource)})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"evaluations": out})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]bool{"decision": decide(req.Resource)})
}

func (p *externalTestPDP) questions() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.asked...)
}

type externalTestEnv struct {
	api  *API
	pdp  *externalTestPDP
	priv ed25519.PrivateKey
}

func newExternalTestEnv(t *testing.T, allowed ...string) externalTestEnv {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	der, err := x509.MarshalPKIXPublicKey(pub)
	require.NoError(t, err)
	dir := t.TempDir()
	keysFile := filepath.Join(dir, "keys.pem")
	require.NoError(t, os.WriteFile(keysFile, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), 0o600))

	pdp := &externalTestPDP{t: t, allow: map[string]bool{}}
	for _, a := range allowed {
		pdp.allow[a] = true
	}
	srv := httptest.NewServer(pdp)
	t.Cleanup(srv.Close)

	authz, err := extauthz.New(config.AuthExternal{
		Enabled: true,
		Token: config.ExternalIdentityToken{
			Header: "X-Identity-Token", Issuer: "gw", Audience: "dagu",
			MaxAge: time.Minute, PublicKeysFile: keysFile,
		},
		AuthZEN: config.ExternalAuthZEN{BaseURL: srv.URL, Timeout: time.Second},
	})
	require.NoError(t, err)

	cfg := &config.Config{}
	cfg.Server.Auth.Mode = config.AuthModeNone
	return externalTestEnv{api: &API{config: cfg, externalAuthz: authz}, pdp: pdp, priv: priv}
}

func (e externalTestEnv) token(t *testing.T, sub string) string {
	t.Helper()
	now := time.Now()
	signed, err := jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.MapClaims{
		"iss": "gw", "aud": "dagu", "sub": sub,
		"iat": now.Add(-time.Second).Unix(), "exp": now.Add(time.Minute).Unix(),
	}).SignedString(e.priv)
	require.NoError(t, err)
	return signed
}

// router mounts the identity middleware and one strict operation, as
// ConfigureRoutes does for the generated handlers.
func (e externalTestEnv) router(pattern, method, operationID string, handler generatedapi.StrictHandlerFunc) http.Handler {
	r := chi.NewRouter()
	r.Use(e.api.externalIdentityMiddleware([]string{"/api/v1/health"}, []string{"/api/v1/webhooks/"}))
	wrapped := e.api.externalAuthorizationMiddleware()(handler, operationID)
	r.Method(method, pattern, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp, err := wrapped(r.Context(), w, r, nil)
		if err != nil {
			WriteErrorResponse(w, err)
			return
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp)
	}))
	return r
}

func serveExternal(h http.Handler, method, target, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	if token != "" {
		req.Header.Set("X-Identity-Token", token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func okHandler(called *bool) generatedapi.StrictHandlerFunc {
	return func(context.Context, http.ResponseWriter, *http.Request, any) (any, error) {
		*called = true
		return map[string]string{"ok": "yes"}, nil
	}
}

func TestExternalAuthzStartWorkflow(t *testing.T) {
	t.Parallel()
	env := newExternalTestEnv(t, "alice runs.start workflow:daily-report")
	var called bool
	h := env.router("/api/v1/dags/{fileName}/start", http.MethodPost, "ExecuteDAG", okHandler(&called))

	rec := serveExternal(h, http.MethodPost, "/api/v1/dags/daily-report/start", "")
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.False(t, called)
	require.Empty(t, env.pdp.questions(), "the PDP is not asked without a valid token")

	rec = serveExternal(h, http.MethodPost, "/api/v1/dags/daily-report/start", env.token(t, "bob"))
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.False(t, called)

	rec = serveExternal(h, http.MethodPost, "/api/v1/dags/daily-report/start", env.token(t, "alice"))
	require.Equal(t, http.StatusOK, rec.Code)
	require.True(t, called)
	require.Equal(t, []string{
		"bob runs.start workflow:daily-report",
		"alice runs.start workflow:daily-report",
	}, env.pdp.questions())
}

func TestExternalAuthzRunResource(t *testing.T) {
	t.Parallel()
	env := newExternalTestEnv(t, "alice runs.stop workflow:etl#r-42")
	var called bool
	h := env.router("/api/v1/dag-runs/{name}/{dagRunId}/stop", http.MethodPost, "TerminateDAGRun", okHandler(&called))

	rec := serveExternal(h, http.MethodPost, "/api/v1/dag-runs/etl/r-42/stop", env.token(t, "alice"))
	require.Equal(t, http.StatusOK, rec.Code)
	require.True(t, called)

	rec = serveExternal(h, http.MethodPost, "/api/v1/dag-runs/etl/r-43/stop", env.token(t, "alice"))
	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestExternalAuthzFallbackActions(t *testing.T) {
	t.Parallel()
	env := newExternalTestEnv(t, "alice settings.read server:dagu")
	var called bool
	get := env.router("/api/v1/wiki", http.MethodGet, "ListWikiPages", okHandler(&called))
	post := env.router("/api/v1/wiki", http.MethodPost, "CreateWikiPage", okHandler(&called))

	require.Equal(t, http.StatusOK, serveExternal(get, http.MethodGet, "/api/v1/wiki", env.token(t, "alice")).Code)
	require.Equal(t, http.StatusForbidden, serveExternal(post, http.MethodPost, "/api/v1/wiki", env.token(t, "alice")).Code)
	require.Equal(t, []string{
		"alice settings.read server:dagu",
		"alice settings.write server:dagu",
	}, env.pdp.questions())
}

func TestExternalAuthzPublicPaths(t *testing.T) {
	t.Parallel()
	env := newExternalTestEnv(t)
	var called bool
	health := env.router("/api/v1/health", http.MethodGet, "GetHealthStatus", okHandler(&called))
	require.Equal(t, http.StatusOK, serveExternal(health, http.MethodGet, "/api/v1/health", "").Code)
	require.True(t, called)

	called = false
	hook := env.router("/api/v1/webhooks/{fileName}", http.MethodPost, "TriggerWebhook", okHandler(&called))
	require.Equal(t, http.StatusOK, serveExternal(hook, http.MethodPost, "/api/v1/webhooks/daily-report", "").Code)
	require.True(t, called)
	require.Empty(t, env.pdp.questions())
}

func TestExternalAuthzRemoteNode(t *testing.T) {
	t.Parallel()
	env := newExternalTestEnv(t,
		"alice workflows.read server:dagu",
		"alice remote_nodes.use remote_node:edge",
	)
	var called bool
	h := env.router("/api/v1/dags", http.MethodGet, "ListDAGs", okHandler(&called))

	require.Equal(t, http.StatusOK, serveExternal(h, http.MethodGet, "/api/v1/dags?remoteNode=local", env.token(t, "alice")).Code)
	require.Equal(t, http.StatusOK, serveExternal(h, http.MethodGet, "/api/v1/dags?remoteNode=edge", env.token(t, "alice")).Code)
	require.Equal(t, http.StatusForbidden, serveExternal(h, http.MethodGet, "/api/v1/dags?remoteNode=core", env.token(t, "alice")).Code)
}

func TestExternalAuthzFiltersDAGList(t *testing.T) {
	t.Parallel()
	env := newExternalTestEnv(t,
		"alice workflows.read server:dagu",
		"alice workflows.read workflow:a",
		"alice workflows.read workflow:c",
	)
	list := func(context.Context, http.ResponseWriter, *http.Request, any) (any, error) {
		return generatedapi.ListDAGs200JSONResponse{Dags: []generatedapi.DAGFile{
			{FileName: "a"}, {FileName: "b"}, {FileName: "c"},
		}}, nil
	}
	h := env.router("/api/v1/dags", http.MethodGet, "ListDAGs", list)

	rec := serveExternal(h, http.MethodGet, "/api/v1/dags", env.token(t, "alice"))
	require.Equal(t, http.StatusOK, rec.Code)
	var body generatedapi.ListDAGs200JSONResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	var names []string
	for _, d := range body.Dags {
		names = append(names, d.FileName)
	}
	require.Equal(t, []string{"a", "c"}, names)
}

func TestExternalAuthzDisabled(t *testing.T) {
	t.Parallel()
	a := &API{config: &config.Config{}}
	var called bool
	next := okHandler(&called)
	wrapped := a.externalAuthorizationMiddleware()(next, "ExecuteDAG")
	_, err := wrapped(context.Background(), nil, httptest.NewRequest(http.MethodPost, "/", nil), nil)
	require.NoError(t, err)
	require.True(t, called)
}

// The mapping refers only to operations of the generated server interface,
// so that a renamed operation cannot silently fall back to settings.*.
func TestExternalOperationsExist(t *testing.T) {
	t.Parallel()
	iface := reflect.TypeFor[generatedapi.StrictServerInterface]()
	for id := range externalOperations {
		_, ok := iface.MethodByName(id)
		require.Truef(t, ok, "external authorization maps unknown operation %q", id)
	}
	for id := range externalPublicOperations {
		_, ok := iface.MethodByName(id)
		require.Truef(t, ok, "external authorization lists unknown public operation %q", id)
	}
}
