// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package test

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/cmn/telemetry"
	"github.com/dagucloud/dagu/internal/service/coordinator"
	"github.com/dagucloud/dagu/internal/service/frontend"
	"github.com/dagucloud/dagu/internal/service/frontend/api/pathutil"
	apiv1 "github.com/dagucloud/dagu/internal/service/frontend/api/v1"
	"github.com/go-resty/resty/v2"
	"github.com/stretchr/testify/require"
)

// Server represents a test HTTP server instance
type Server struct {
	Helper
}

// SetupServer creates and starts a test server instance
func SetupServer(t *testing.T, opts ...HelperOption) Server {
	t.Helper()

	// Frontend server tests exercise API paths that launch DAG subprocesses.
	// Force them onto a binary built from the current source tree rather than
	// relying on whatever happens to exist in .local/bin.
	opts = append(opts, WithBuiltExecutable())

	// Create a listener and keep it alive until the server binds.
	// This prevents race conditions where parallel tests could steal the port
	// between finding it and binding to it.
	listener, err := net.Listen("tcp", "localhost:0")
	require.NoError(t, err, "failed to create listener")

	port := listener.Addr().(*net.TCPAddr).Port

	opts = append(opts, WithServerConfig(
		&config.Server{
			Host: "localhost",
			Port: port,
			Permissions: map[config.Permission]bool{
				config.PermissionWriteDAGs: true,
				config.PermissionRunDAGs:   true,
			},
		},
	))

	helper := Setup(t, opts...)

	srv := Server{Helper: helper}

	server, err := srv.newFrontendServer(listener)
	if err != nil {
		_ = listener.Close()
		t.Fatalf("server failed to initialize: %v", err)
	}

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- server.Serve(srv.Context)
	}()

	waitForServerStart(t, srv.healthURL(), serveErr)

	return srv
}

// newFrontendServer constructs the HTTP server with the provided listener.
func (srv *Server) newFrontendServer(listener net.Listener) (*frontend.Server, error) {
	cc := coordinator.New(srv.ServiceRegistry, coordinator.DefaultConfig())

	collector := telemetry.NewCollector(
		config.Version,
		srv.DAGStore,
		srv.DAGRunStore,
		srv.QueueStore,
		srv.ServiceRegistry,
	)
	collector.SetWorkerHeartbeatStore(srv.WorkerHeartbeatStore)
	mr := telemetry.NewRegistry(collector)

	// Pass the pre-bound listener to the server to avoid port race conditions
	serverOpts := append([]frontend.ServerOption{
		frontend.WithListener(listener),
		frontend.WithAPIOption(apiv1.WithDAGRunLeaseStore(srv.DAGRunLeaseStore)),
		frontend.WithAPIOption(apiv1.WithWorkerHeartbeatStore(srv.WorkerHeartbeatStore)),
	}, srv.ServerOptions...)
	server, err := frontend.NewServer(
		srv.Context, srv.Config, srv.DAGStore, srv.DAGRunStore,
		srv.QueueStore, srv.ProcStore, srv.DAGRunMgr, cc,
		srv.ServiceRegistry, mr, collector, nil,
		serverOpts...,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create server: %w", err)
	}

	return server, nil
}

func (srv *Server) healthURL() string {
	healthPath := pathutil.BuildMountedAPIEndpointPath(
		srv.Config.Server.BasePath,
		srv.Config.Server.APIBasePath,
		"health",
	)
	return fmt.Sprintf("http://%s:%d%s", srv.Config.Server.Host, srv.Config.Server.Port, healthPath)
}

// Client returns an HTTP client for the server
func (srv *Server) Client() *APIClient {
	return &APIClient{
		server: srv,
		client: resty.New(),
	}
}

// APIClient handles HTTP requests to the test server
type APIClient struct {
	server *Server
	client *resty.Client
}

// baseURL returns the base URL for the server
func (c *APIClient) baseURL() string {
	return fmt.Sprintf("http://%s:%d", c.server.Config.Server.Host, c.server.Config.Server.Port)
}

// Request represents an HTTP request being prepared
type Request struct {
	client         *APIClient
	method         string
	path           string
	body           any
	expectedStatus int
	headers        map[string]string
}

// Get prepares a GET request
func (c *APIClient) Get(path string) *Request {
	return &Request{
		client: c,
		method: http.MethodGet,
		path:   path,
	}
}

// Post prepares a POST request with the given body
func (c *APIClient) Post(path string, body any) *Request {
	return &Request{
		client: c,
		method: http.MethodPost,
		path:   path,
		body:   body,
	}
}

// Delete prepares a DELETE request
func (c *APIClient) Delete(path string) *Request {
	return &Request{
		client: c,
		method: http.MethodDelete,
		path:   path,
	}
}

// Patch prepares a PATCH request with the given body
func (c *APIClient) Patch(path string, body any) *Request {
	return &Request{
		client: c,
		method: http.MethodPatch,
		path:   path,
		body:   body,
	}
}

// Put prepares a PUT request with the given body
func (c *APIClient) Put(path string, body any) *Request {
	return &Request{
		client: c,
		method: http.MethodPut,
		path:   path,
		body:   body,
	}
}

// ExpectStatus sets the expected HTTP status code
func (r *Request) ExpectStatus(code int) *Request {
	r.expectedStatus = code
	return r
}

// WithHeader adds a header to the request
func (r *Request) WithHeader(key, value string) *Request {
	if r.headers == nil {
		r.headers = make(map[string]string)
	}
	r.headers[key] = value
	return r
}

// WithBearerToken adds a Bearer token to the Authorization header
func (r *Request) WithBearerToken(token string) *Request {
	return r.WithHeader("Authorization", "Bearer "+token)
}

// WithBasicAuth adds Basic authentication to the request
func (r *Request) WithBasicAuth(username, password string) *Request {
	return r.WithHeader("Authorization", "Basic "+basicAuth(username, password))
}

// basicAuth returns the base64 encoded basic auth string
func basicAuth(username, password string) string {
	auth := username + ":" + password
	return base64.StdEncoding.EncodeToString([]byte(auth))
}

// Send executes the request and returns the response
func (r *Request) Send(t *testing.T) *Response {
	t.Helper()

	req := r.client.client.R()
	url := r.client.baseURL() + r.path

	// Apply custom headers
	for key, value := range r.headers {
		req.SetHeader(key, value)
	}

	var res *resty.Response
	var err error

	switch r.method {
	case http.MethodGet:
		res, err = req.Get(url)
	case http.MethodPost:
		jsonBody, jsonErr := json.Marshal(r.body)
		require.NoError(t, jsonErr, "failed to marshal request body")

		res, err = req.
			SetBody(jsonBody).
			SetHeader("Content-Type", "application/json").
			Post(url)
	case http.MethodPatch:
		jsonBody, jsonErr := json.Marshal(r.body)
		require.NoError(t, jsonErr, "failed to marshal request body")

		res, err = req.
			SetBody(jsonBody).
			SetHeader("Content-Type", "application/json").
			Patch(url)
	case http.MethodPut:
		jsonBody, jsonErr := json.Marshal(r.body)
		require.NoError(t, jsonErr, "failed to marshal request body")

		res, err = req.
			SetBody(jsonBody).
			SetHeader("Content-Type", "application/json").
			Put(url)
	case http.MethodDelete:
		res, err = req.Delete(url)
	default:
		t.Fatalf("unsupported HTTP method: %s", r.method)
	}

	require.NoError(t, err, "failed to make %s request", r.method)

	if r.expectedStatus != 0 {
		t.Logf("expected status code: %d, actual status code: %d", r.expectedStatus, res.StatusCode())
		require.Equal(t, r.expectedStatus, res.StatusCode(), "unexpected status code")
	}

	return &Response{
		Body:     string(res.Body()),
		Response: res,
	}
}

// Response represents an HTTP response
type Response struct {
	Body     string
	Response *resty.Response
}

// Unmarshal parses the response body into the provided value
func (r *Response) Unmarshal(t *testing.T, v any) {
	t.Helper()
	err := json.Unmarshal([]byte(r.Body), v)
	require.NoError(t, err, "failed to unmarshal response body")
}

// waitForServerStart polls the health endpoint until the server is ready.
func waitForServerStart(t *testing.T, url string, serveErr <-chan error) {
	t.Helper()

	const retryDelay = 100 * time.Millisecond

	client := &http.Client{Timeout: 500 * time.Millisecond}
	deadline := time.Now().Add(10 * time.Second)
	var lastErr error

	for time.Now().Before(deadline) {
		select {
		case err := <-serveErr:
			if err != nil {
				t.Fatalf("server failed before health endpoint became ready: %v", err)
			}
			t.Fatal("server stopped before health endpoint became ready")
		default:
		}

		resp, err := client.Get(url)
		switch {
		case err == nil && resp != nil && resp.StatusCode == http.StatusOK:
			_ = resp.Body.Close()
			return
		case resp != nil:
			statusCode := resp.StatusCode
			_ = resp.Body.Close()
			lastErr = fmt.Errorf("unexpected status %d", statusCode)
		default:
			lastErr = err
		}
		time.Sleep(retryDelay)
	}

	t.Fatalf("server health endpoint did not become ready within 10s: %v", lastErr)
}
