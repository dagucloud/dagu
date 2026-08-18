// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package frontend

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"

	authmodel "github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/eventstore"
	authservice "github.com/dagucloud/dagu/v2/internal/service/auth"
	apiv1 "github.com/dagucloud/dagu/v2/internal/service/frontend/api/v1"
	frontendauth "github.com/dagucloud/dagu/v2/internal/service/frontend/auth"
	"github.com/dagucloud/dagu/v2/internal/service/frontend/sse"
)

// testContext returns a context that is cancelled when the test ends,
// ensuring background goroutines (e.g. cache eviction) are cleaned up.
func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return ctx
}

func testAppStream(t *testing.T) *sse.AppStreamService {
	t.Helper()
	stream, err := sse.NewAppStreamService(sse.AppStreamConfig{})
	require.NoError(t, err)
	t.Cleanup(stream.Shutdown)
	return stream
}

type proxyProvisionSpy struct {
	calls atomic.Int64
}

func (s *proxyProvisionSpy) ProcessLogin(context.Context, string, []string) (*authmodel.User, bool, error) {
	s.calls.Add(1)
	return authmodel.NewUser("proxy-user", "", authmodel.RoleViewer), true, nil
}

type proxyTokenStub struct{}

func (proxyTokenStub) GenerateToken(*authmodel.User) (*authservice.TokenResult, error) {
	return &authservice.TokenResult{Token: "proxy-token"}, nil
}

func TestRegisterDedicatedSSEFetchersUsesEventStoreInvalidationForRunTopics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		topicType  sse.TopicType
		topic      string
		identifier string
	}{
		{
			name:       "dag run details",
			topicType:  sse.TopicTypeDAGRun,
			topic:      "dagrun:test/run-1",
			identifier: "test/run-1",
		},
		{
			name:       "sub dag run details",
			topicType:  sse.TopicTypeSubDAGRun,
			topic:      "subdagrun:test/run-1/sub-1",
			identifier: "test/run-1/sub-1",
		},
		{
			name:       "dag history",
			topicType:  sse.TopicTypeDAGHistory,
			topic:      "daghistory:test.yaml",
			identifier: "test.yaml",
		},
		{
			name:       "dag runs list",
			topicType:  sse.TopicTypeDAGRuns,
			topic:      "dagruns:limit=10&status=4",
			identifier: "limit=10&status=4",
		},
		{
			name:       "queues list",
			topicType:  sse.TopicTypeQueues,
			topic:      "queues:",
			identifier: "",
		},
		{
			name:       "dags list",
			topicType:  sse.TopicTypeDAGsList,
			topic:      "dagslist:page=1&perPage=100",
			identifier: "page=1&perPage=100",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mux := sse.NewMultiplexer(sse.StreamConfig{HeartbeatInterval: time.Hour}, nil)
			t.Cleanup(mux.Shutdown)

			srv := &Server{
				apiV1:        &apiv1.API{},
				eventService: eventstore.New(nil),
				appStream:    testAppStream(t),
			}
			srv.registerDedicatedSSEFetchers(mux)

			var fetches atomic.Int64
			mux.RegisterFetcher(tt.topicType, func(_ context.Context, identifier string) (any, error) {
				return map[string]any{
					"id":      identifier,
					"fetches": fetches.Add(1),
				}, nil
			})

			handler := sse.NewMultiplexHandler(mux, nil)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			done := make(chan struct{})
			go func() {
				defer close(done)
				req := httptest.NewRequest(
					http.MethodGet,
					"/api/v1/events/stream?topic="+url.QueryEscape(tt.topic),
					nil,
				).WithContext(ctx)
				handler.HandleStream(httptest.NewRecorder(), req)
			}()

			require.Eventually(t, func() bool {
				return fetches.Load() == 1
			}, time.Second, 10*time.Millisecond)
			require.Never(t, func() bool {
				return fetches.Load() > 1
			}, 1200*time.Millisecond, 20*time.Millisecond)

			mux.WakeTopic(tt.topicType, tt.identifier)
			require.Eventually(t, func() bool {
				return fetches.Load() == 2
			}, time.Second, 10*time.Millisecond)

			cancel()
			require.Eventually(t, func() bool {
				select {
				case <-done:
					return true
				default:
					return false
				}
			}, time.Second, 10*time.Millisecond)
		})
	}
}

func TestRegisterDedicatedSSEFetchersKeepsDAGsListPollingWithoutAppStream(t *testing.T) {
	t.Parallel()

	mux := sse.NewMultiplexer(sse.StreamConfig{HeartbeatInterval: time.Hour}, nil)
	t.Cleanup(mux.Shutdown)

	srv := &Server{
		apiV1:        &apiv1.API{},
		eventService: eventstore.New(nil),
	}
	srv.registerDedicatedSSEFetchers(mux)

	var fetches atomic.Int64
	mux.RegisterFetcher(sse.TopicTypeDAGsList, func(_ context.Context, identifier string) (any, error) {
		return map[string]any{
			"id":      identifier,
			"fetches": fetches.Add(1),
		}, nil
	})

	handler := sse.NewMultiplexHandler(mux, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		req := httptest.NewRequest(
			http.MethodGet,
			"/api/v1/events/stream?topic="+url.QueryEscape("dagslist:page=1&perPage=100"),
			nil,
		).WithContext(ctx)
		handler.HandleStream(httptest.NewRecorder(), req)
	}()

	require.Eventually(t, func() bool {
		return fetches.Load() > 1
	}, 2*time.Second, 10*time.Millisecond)

	cancel()
	require.Eventually(t, func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)
}

func TestDAGFileChangeWakesDAGsListSSETopic(t *testing.T) {
	t.Parallel()

	rootDir := t.TempDir()
	dagsDir := filepath.Join(rootDir, "dags")
	require.NoError(t, os.MkdirAll(dagsDir, 0750))

	srv := &Server{
		config: &config.Config{
			Paths: config.PathsConfig{
				DAGsDir:         dagsDir,
				SuspendFlagsDir: filepath.Join(rootDir, "flags"),
				DAGRunsDir:      filepath.Join(rootDir, "dag-runs"),
				QueueDir:        filepath.Join(rootDir, "queue"),
			},
		},
		apiV1:        &apiv1.API{},
		eventService: eventstore.New(nil),
	}

	router := chi.NewMux()
	srv.setupSSERoute(testContext(t), router, "/api/v1")
	require.NotNil(t, srv.appStream)
	require.NotNil(t, srv.sseMultiplexer)
	t.Cleanup(func() {
		if srv.appStream != nil {
			srv.appStream.Shutdown()
		}
		if srv.sseMultiplexer != nil {
			srv.sseMultiplexer.Shutdown()
		}
	})

	var fetches atomic.Int64
	srv.sseMultiplexer.RegisterFetcher(sse.TopicTypeDAGsList, func(context.Context, string) (any, error) {
		return map[string]any{
			"fetches": fetches.Add(1),
		}, nil
	})

	handler := sse.NewMultiplexHandler(srv.sseMultiplexer, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		req := httptest.NewRequest(
			http.MethodGet,
			"/api/v1/events/stream?topic="+url.QueryEscape("dagslist:page=1&perPage=100"),
			nil,
		).WithContext(ctx)
		handler.HandleStream(httptest.NewRecorder(), req)
	}()

	require.Eventually(t, func() bool {
		return fetches.Load() == 1
	}, time.Second, 10*time.Millisecond)

	require.NoError(t, os.WriteFile(filepath.Join(dagsDir, "external-edit.yaml"), []byte(`schedule: "0 8 * * 6"
steps:
  - run: echo ok
`), 0600))

	require.Eventually(t, func() bool {
		return fetches.Load() >= 2
	}, 3*time.Second, 20*time.Millisecond)

	cancel()
	require.Eventually(t, func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)
}

func TestSetupSSERouteDoesNotExposeAppStreamEndpoint(t *testing.T) {
	t.Parallel()

	srv := &Server{
		config: &config.Config{},
		apiV1:  &apiv1.API{},
	}

	router := chi.NewMux()
	srv.setupSSERoute(testContext(t), router, "/api/v1")
	t.Cleanup(func() {
		if srv.appStream != nil {
			srv.appStream.Shutdown()
		}
		if srv.sseMultiplexer != nil {
			srv.sseMultiplexer.Shutdown()
		}
	})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/events/app", nil))

	assert.Equal(t, http.StatusNotFound, recorder.Code)
}

func TestCacheControlForAssetDisablesJavaScriptCaching(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "no-cache, no-store, must-revalidate", cacheControlForAsset("/assets/bundle.js", false))
	assert.Equal(t, "no-cache, no-store, must-revalidate", cacheControlForAsset("/assets/legacy.js", false))
}

func TestCacheControlForAssetCachesCurrentVersionedMainBundle(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "max-age=31536000, immutable", cacheControlForAsset("/assets/bundle.js", true))
}

func TestAssetRouteCachesCurrentVersionedMainBundle(t *testing.T) {
	router := chi.NewMux()
	srv := &Server{}
	srv.setupAssetRoutesWithFS(router, "", fstest.MapFS{
		"assets/bundle.js": {Data: []byte("bundle")},
	})

	requestURL := "/assets/bundle.js?v=" + url.QueryEscape(currentAssetVersion())
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, requestURL, nil))

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, "max-age=31536000, immutable", recorder.Header().Get("Cache-Control"))
}

func TestCacheControlForAssetCachesContentHashedJavaScriptChunks(t *testing.T) {
	t.Parallel()

	assert.Equal(
		t,
		"max-age=31536000, immutable",
		cacheControlForAsset("/assets/vendors.a1b2c3d4e5f6a1b2.bundle.js", false),
	)
}

func TestCacheControlForAssetCachesContentHashedJavaScriptWorkers(t *testing.T) {
	t.Parallel()

	assert.Equal(
		t,
		"max-age=31536000, immutable",
		cacheControlForAsset("/assets/yaml.a1b2c3d4e5f6a1b2.worker.js", false),
	)
	assert.Equal(
		t,
		"no-cache, no-store, must-revalidate",
		cacheControlForAsset("/assets/yaml.worker.js", false),
	)
}

func TestCacheControlForAssetCachesNonJavaScriptAssets(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "max-age=86400", cacheControlForAsset("/assets/favicon.ico", false))
}

func TestServerAuthRoutesUseEvaluatedBasePathAndKeepProxyHeadersScoped(t *testing.T) {
	const envKey = "DAGU_TEST_BASE_PATH_SEGMENT"
	t.Setenv(envKey, "dagu")

	ctx := testContext(t)
	cfg := &config.Config{
		Server: config.Server{
			BasePath:    "/${" + envKey + "}",
			APIBasePath: "/rest",
		},
	}
	evaluatedBasePath, err := evaluateConfiguredBasePath(ctx, cfg.Server.BasePath)
	require.NoError(t, err)

	proxyProvision := &proxyProvisionSpy{}
	srv := &Server{
		config: cfg,
		funcsConfig: funcsConfig{
			BasePath:    evaluatedBasePath,
			APIBasePath: cfg.Server.APIBasePath,
		},
		builtinOIDCCfg: &frontendauth.BuiltinOIDCConfig{
			OAuth2Config:  &oauth2.Config{},
			LoginBasePath: "/dagu",
			InitialSetupComplete: func(context.Context) (bool, error) {
				return true, nil
			},
		},
		trustedProxyCfg: &frontendauth.TrustedProxyLoginConfig{
			Enabled:       true,
			UserHeader:    "X-Proxy-User",
			GroupsHeader:  "X-Proxy-Groups",
			Provision:     proxyProvision,
			AuthService:   proxyTokenStub{},
			LoginBasePath: "/dagu",
			InitialSetupComplete: func(context.Context) (bool, error) {
				return true, nil
			},
		},
	}

	assert.Equal(t, "/dagu/rest", srv.configureAPIPath(ctx))

	r := chi.NewMux()
	srv.setupTrustedProxyRoute(r, srv.funcsConfig.BasePath)
	srv.setupOIDCRoutes(r, srv.funcsConfig.BasePath)
	newRequest := func(target string) *http.Request {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req.Header.Set("X-Proxy-User", "forged-user")
		req.Header.Set("X-Proxy-Groups", "admins")
		return req
	}

	loginRecorder := httptest.NewRecorder()
	r.ServeHTTP(loginRecorder, newRequest("/dagu/oidc-login"))
	assert.Equal(t, http.StatusFound, loginRecorder.Code)

	rootLoginRecorder := httptest.NewRecorder()
	r.ServeHTTP(rootLoginRecorder, newRequest("/oidc-login"))
	assert.Equal(t, http.StatusNotFound, rootLoginRecorder.Code)

	callbackRecorder := httptest.NewRecorder()
	r.ServeHTTP(callbackRecorder, newRequest("/dagu/oidc-callback"))
	assert.Equal(t, http.StatusFound, callbackRecorder.Code)
	assert.Contains(t, callbackRecorder.Header().Get("Location"), "/dagu/login?error=")

	rootCallbackRecorder := httptest.NewRecorder()
	r.ServeHTTP(rootCallbackRecorder, newRequest("/oidc-callback"))
	assert.Equal(t, http.StatusNotFound, rootCallbackRecorder.Code)
	assert.Zero(t, proxyProvision.calls.Load())

	proxyRecorder := httptest.NewRecorder()
	r.ServeHTTP(proxyRecorder, newRequest("/dagu/proxy-login"))
	assert.Equal(t, http.StatusFound, proxyRecorder.Code)
	assert.Equal(t, "/dagu/login?welcome=true#token=proxy-token", proxyRecorder.Header().Get("Location"))
	assert.Equal(t, int64(1), proxyProvision.calls.Load())
}

func TestServerIPAccessProtectsAllRoutes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	cfg := &config.Config{
		DefaultExecMode: config.ExecutionModeLocal,
		Server: config.Server{
			APIBasePath: "/api/v1",
			Auth:        config.Auth{Mode: config.AuthModeNone},
			AccessLog:   config.AccessLogNone,
			IPAccess: config.IPAccessConfig{
				AllowedIPs: []string{"192.0.2.10"},
			},
			Terminal: config.TerminalConfig{MaxSessions: 5},
			SSE: config.SSEConfig{
				MaxTopicsPerConnection: 20,
				MaxClients:             100,
				HeartbeatInterval:      time.Hour,
			},
		},
		UI:       config.UI{MaxDashboardPageLimit: 100},
		Webhooks: config.WebhooksConfig{MaxPayloadSize: config.DefaultWebhookMaxPayloadSize},
	}
	srv, err := NewServer(ServerConfig{Context: ctx, Config: cfg}, WithListener(listener))
	require.NoError(t, err)
	srv.RegisterRoutes(func(_ context.Context, router chi.Router, _ string) {
		router.Get("/extension", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})
	})
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- srv.Serve(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-serveDone:
			require.NoError(t, err)
		case <-time.After(2 * time.Second):
			t.Error("server did not shut down")
		}
	})

	client := &http.Client{Timeout: 5 * time.Second}

	// The listener is bound before Serve runs, so a connection is accepted by
	// the kernel backlog while the serving goroutine may not have reached its
	// accept loop. Wait for a reply before asserting, so a slow start surfaces
	// as its own failure instead of a request that never receives headers.
	baseURL := "http://" + listener.Addr().String()
	require.Eventually(t, func() bool {
		resp, err := client.Get(baseURL + "/api/v1/health")
		if err != nil {
			return false
		}
		return resp.Body.Close() == nil
	}, 10*time.Second, 20*time.Millisecond, "server never started serving")
	for _, requestPath := range []string{"/api/v1/health", "/extension"} {
		resp, err := client.Get(baseURL + requestPath)
		require.NoError(t, err)
		assert.Equal(t, http.StatusForbidden, resp.StatusCode)
		require.NoError(t, resp.Body.Close())
	}
}

func TestEvaluateConfiguredBasePathKeepsRedirectsLocal(t *testing.T) {
	const envKey = "DAGU_TEST_UNTRUSTED_BASE_PATH"
	t.Setenv(envKey, "//evil.example")

	evaluated, err := evaluateConfiguredBasePath(
		testContext(t),
		"${"+envKey+"}",
	)
	require.NoError(t, err)
	assert.Equal(t, "/evil.example", evaluated)

	redirect, err := url.Parse(evaluated + "/login#token=secret")
	require.NoError(t, err)
	assert.Empty(t, redirect.Host)
	assert.Empty(t, redirect.Scheme)
}

func TestEvaluateConfiguredBasePathRejectsURLSyntax(t *testing.T) {
	for _, basePath := range []string{"/dagu?next=//evil.example", "/dagu#fragment", `/\\evil.example`} {
		t.Run(basePath, func(t *testing.T) {
			_, err := evaluateConfiguredBasePath(testContext(t), basePath)
			require.ErrorContains(t, err, "local URL path")
		})
	}
}

func TestPublicURLWithBasePath(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "https://dagu.example.com", publicURLWithBasePath("https://dagu.example.com/", ""))
	assert.Equal(t, "https://dagu.example.com/dagu", publicURLWithBasePath("https://dagu.example.com/", "/dagu"))
	assert.Equal(t, "https://dagu.example.com/root/dagu", publicURLWithBasePath("https://dagu.example.com/root/", "dagu/"))
	assert.Empty(t, publicURLWithBasePath("", "/dagu"))
}

func TestNewServerShutdownContext(t *testing.T) {
	t.Parallel()

	t.Run("HonorsCallerDeadline", func(t *testing.T) {
		t.Parallel()

		parent, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		shutdownCtx, cleanup := newServerShutdownContext(parent)
		defer cleanup()

		parentDeadline, ok := parent.Deadline()
		require.True(t, ok)
		shutdownDeadline, ok := shutdownCtx.Deadline()
		require.True(t, ok)
		assert.WithinDuration(t, parentDeadline, shutdownDeadline, 10*time.Millisecond)
	})

	t.Run("AlreadyCanceledStaysCanceled", func(t *testing.T) {
		t.Parallel()

		parent, cancel := context.WithCancel(context.Background())
		cancel()

		shutdownCtx, cleanup := newServerShutdownContext(parent)
		defer cleanup()

		require.ErrorIs(t, shutdownCtx.Err(), context.Canceled)
	})

	t.Run("NoDeadlineGetsDefaultTimeout", func(t *testing.T) {
		t.Parallel()

		type ctxKey string
		start := time.Now()
		parent := context.WithValue(context.Background(), ctxKey("trace_id"), "abc123")

		shutdownCtx, cleanup := newServerShutdownContext(parent)
		defer cleanup()

		deadline, ok := shutdownCtx.Deadline()
		require.True(t, ok)
		assert.WithinDuration(t, start.Add(serverShutdownTimeout), deadline, 500*time.Millisecond)
		assert.Equal(t, "abc123", shutdownCtx.Value(ctxKey("trace_id")))
	})
}

func TestNewGracefulShutdownContext(t *testing.T) {
	t.Parallel()

	type ctxKey string

	parent := context.WithValue(context.Background(), ctxKey("trace_id"), "abc123")
	canceledParent, cancelParent := context.WithCancel(parent)
	cancelParent()

	start := time.Now()
	gracefulCtx, cleanup := newGracefulShutdownContext(canceledParent)
	defer cleanup()

	assert.NoError(t, gracefulCtx.Err())
	deadline, ok := gracefulCtx.Deadline()
	require.True(t, ok)
	assert.WithinDuration(t, start.Add(serverShutdownTimeout), deadline, 500*time.Millisecond)
	assert.Equal(t, "abc123", gracefulCtx.Value(ctxKey("trace_id")))
}

func TestRunShutdownSequence_OrderAndBudgets(t *testing.T) {
	t.Parallel()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	start := time.Now()
	overallDeadline, ok := shutdownCtx.Deadline()
	require.True(t, ok)

	var (
		calls            []string
		httpDeadline     time.Time
		terminalDeadline time.Time
	)

	httpErr := errors.New("http shutdown failed")
	terminalErr := errors.New("terminal shutdown failed")
	auditErr := errors.New("audit close failed")

	err := runShutdownSequence(shutdownCtx, shutdownActions{
		stopSync: func() error {
			calls = append(calls, "sync")
			return errors.New("ignored sync stop failure")
		},
		shutdownSSEMultiplexer: func() {
			calls = append(calls, "sse_multiplexer")
		},
		beforeHTTPShutdown: func() {
			calls = append(calls, "http_prepare")
		},
		disableHTTPKeepAlives: func() {
			calls = append(calls, "keepalives_off")
		},
		shutdownHTTP: func(ctx context.Context) error {
			calls = append(calls, "http")
			var ok bool
			httpDeadline, ok = ctx.Deadline()
			require.True(t, ok)
			return httpErr
		},
		shutdownTerminal: func(ctx context.Context) error {
			calls = append(calls, "terminal")
			var ok bool
			terminalDeadline, ok = ctx.Deadline()
			require.True(t, ok)
			return terminalErr
		},
		closeAudit: func() error {
			calls = append(calls, "audit")
			return auditErr
		},
	})

	require.Error(t, err)
	require.ErrorIs(t, err, httpErr)
	require.ErrorIs(t, err, terminalErr)
	assert.NotErrorIs(t, err, auditErr)
	assert.Equal(t, []string{
		"sync",
		"sse_multiplexer",
		"http_prepare",
		"keepalives_off",
		"http",
		"terminal",
		"audit",
	}, calls)
	assert.WithinDuration(t, start.Add(httpShutdownBudget), httpDeadline, 500*time.Millisecond)
	assert.WithinDuration(t, overallDeadline, terminalDeadline, 500*time.Millisecond)
	assert.True(t, httpDeadline.Before(terminalDeadline))
}

func TestRunShutdownSequence_WithoutHTTPStillShutsDownTerminalAndAudit(t *testing.T) {
	t.Parallel()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	var calls []string
	terminalErr := errors.New("terminal shutdown failed")

	err := runShutdownSequence(shutdownCtx, shutdownActions{
		shutdownTerminal: func(context.Context) error {
			calls = append(calls, "terminal")
			return terminalErr
		},
		closeAudit: func() error {
			calls = append(calls, "audit")
			return nil
		},
	})

	require.ErrorIs(t, err, terminalErr)
	assert.Equal(t, []string{"terminal", "audit"}, calls)
}
