// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package mcptest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	api "github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/test"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const Timeout = 30 * time.Second

type Server struct {
	server test.Server
}

func NewServer(t *testing.T) *Server {
	t.Helper()

	return &Server{server: test.SetupServer(t)}
}

func NewAuthServer(t *testing.T) *Server {
	t.Helper()

	server := test.SetupServer(t, test.WithConfigMutator(func(cfg *config.Config) {
		cfg.Server.Auth.Mode = config.AuthModeBuiltin
		cfg.Server.Auth.Builtin.Token.Secret = "jwt-secret-key"
		cfg.Server.Auth.Builtin.Token.TTL = 24 * time.Hour
	}))

	server.Client().Post("/api/v1/auth/setup", api.SetupRequest{
		Username: "admin",
		Password: "adminpass",
	}).ExpectStatus(http.StatusOK).Send(t)

	return &Server{server: server}
}

func Context(t *testing.T) context.Context {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	t.Cleanup(cancel)
	return ctx
}

func StructuredMap(t *testing.T, result *mcpsdk.CallToolResult) map[string]any {
	t.Helper()

	require.NotNil(t, result)
	require.NotNil(t, result.StructuredContent)

	switch content := result.StructuredContent.(type) {
	case map[string]any:
		return content
	case json.RawMessage:
		return decodeMap(t, content)
	case []byte:
		return decodeMap(t, content)
	default:
		data, err := json.Marshal(content)
		require.NoError(t, err)
		return decodeMap(t, data)
	}
}

func (s *Server) Connect(t *testing.T, token string) *mcpsdk.ClientSession {
	t.Helper()

	session, err := s.TryConnect(t, token)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func decodeMap(t *testing.T, data []byte) map[string]any {
	t.Helper()

	var result map[string]any
	require.NoError(t, json.Unmarshal(data, &result))
	return result
}

func (s *Server) TryConnect(t *testing.T, token string) (*mcpsdk.ClientSession, error) {
	t.Helper()

	ctx := Context(t)
	client := mcpsdk.NewClient(&mcpsdk.Implementation{
		Name:    "dagu-conformance",
		Version: "v0.0.0",
	}, nil)

	transport := &mcpsdk.StreamableClientTransport{
		Endpoint:             s.endpoint(),
		DisableStandaloneSSE: true,
	}
	if token != "" {
		transport.HTTPClient = &http.Client{
			Transport: bearerTransport{
				token: token,
				base:  http.DefaultTransport,
			},
		}
	}

	return client.Connect(ctx, transport, nil)
}

func (s *Server) CreateAPIKey(t *testing.T, name string, surfaces ...api.CreateAPIKeyRequestAllowedSurfaces) string {
	t.Helper()

	token := s.adminToken(t)
	resp := s.server.Client().Post("/api/v1/api-keys", api.CreateAPIKeyRequest{
		Name:             name,
		Role:             api.UserRoleViewer,
		AllowedSurfaces:  surfaces,
		AttributionClass: api.CreateAPIKeyRequestAttributionClassServiceAccount,
	}).WithBearerToken(token).ExpectStatus(http.StatusCreated).Send(t)

	var result api.CreateAPIKeyResponse
	resp.Unmarshal(t, &result)
	require.NotEmpty(t, result.Key)
	return result.Key
}

func (s *Server) CreateDAG(t *testing.T, name, spec string) {
	t.Helper()

	s.server.Client().Post("/api/v1/dags", api.CreateNewDAGJSONRequestBody{
		Name: api.DAGName(name),
		Spec: &spec,
	}).ExpectStatus(http.StatusCreated).Send(t)

	t.Cleanup(func() {
		_ = s.server.Client().Delete("/api/v1/dags/" + name).Send(t)
	})
}

func (s *Server) StartDAG(t *testing.T, name string) string {
	t.Helper()

	resp := s.server.Client().Post("/api/v1/dags/"+name+"/start", api.ExecuteDAGJSONRequestBody{}).
		ExpectStatus(http.StatusOK).Send(t)

	var result api.ExecuteDAG200JSONResponse
	resp.Unmarshal(t, &result)
	require.NotEmpty(t, result.DagRunId)
	return string(result.DagRunId)
}

func (s *Server) WaitForDAGRunStatus(t *testing.T, name, dagRunID string, status api.Status) {
	t.Helper()

	var lastStatus api.Status
	var lastStatusCode int
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		resp := s.server.Client().Get(fmt.Sprintf("/api/v1/dag-runs/%s/%s", name, dagRunID)).Send(t)
		lastStatusCode = resp.Response.StatusCode()
		if !assert.Equal(c, http.StatusOK, lastStatusCode) {
			return
		}

		var result api.GetDAGRunDetails200JSONResponse
		resp.Unmarshal(t, &result)
		lastStatus = result.DagRunDetails.Status
		assert.Equal(c, status, lastStatus)
	}, 10*time.Second, 250*time.Millisecond,
		"dag run %s/%s never reached status %v (last HTTP status: %d, last run status: %v)",
		name, dagRunID, status, lastStatusCode, lastStatus)
}

func (s *Server) CreateCompletedRun(t *testing.T, name string) string {
	t.Helper()

	spec := fmt.Sprintf(`steps:
  - name: main
    run: "echo %s"
`, name)
	s.CreateDAG(t, name, spec)
	dagRunID := s.StartDAG(t, name)
	s.WaitForDAGRunStatus(t, name, dagRunID, api.StatusSuccess)
	return dagRunID
}

func (s *Server) adminToken(t *testing.T) string {
	t.Helper()

	resp := s.server.Client().Post("/api/v1/auth/login", api.LoginRequest{
		Username: "admin",
		Password: "adminpass",
	}).ExpectStatus(http.StatusOK).Send(t)

	var result api.LoginResponse
	resp.Unmarshal(t, &result)
	require.NotEmpty(t, result.Token)
	return result.Token
}

func (s *Server) endpoint() string {
	return fmt.Sprintf("http://%s:%d/mcp", s.server.Config.Server.Host, s.server.Config.Server.Port)
}

type bearerTransport struct {
	token string
	base  http.RoundTripper
}

func (t bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}

	req = req.Clone(req.Context())
	req.Header = req.Header.Clone()
	req.Header.Set("Authorization", "Bearer "+t.token)
	return base.RoundTrip(req)
}
