// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package frontend

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"

	authmodel "github.com/dagucloud/dagu/internal/auth"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/persis/fileuser"
	"github.com/dagucloud/dagu/internal/service/eventstore"
	apiv1 "github.com/dagucloud/dagu/internal/service/frontend/api/v1"
	frontendauth "github.com/dagucloud/dagu/internal/service/frontend/auth"
	"github.com/dagucloud/dagu/internal/service/frontend/sse"
)

// testContext returns a context that is cancelled when the test ends,
// ensuring background goroutines (e.g. cache eviction) are cleaned up.
func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return ctx
}

// testConfig creates a minimal config for initBuiltinAuthService tests.
// All directories point to subdirectories of the given temp dir.
func testConfig(tmpDir string, ia config.InitialAdmin) *config.Config {
	return &config.Config{
		Paths: config.PathsConfig{
			UsersDir:    filepath.Join(tmpDir, "users"),
			APIKeysDir:  filepath.Join(tmpDir, "apikeys"),
			WebhooksDir: filepath.Join(tmpDir, "webhooks"),
			DataDir:     filepath.Join(tmpDir, "data"),
		},
		Server: config.Server{
			Auth: config.Auth{
				Mode: config.AuthModeBuiltin,
				Builtin: config.AuthBuiltin{
					Token: config.TokenConfig{
						Secret: "test-secret-for-jwt-signing",
						TTL:    24 * time.Hour,
					},
					InitialAdmin: ia,
				},
			},
		},
	}
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

func TestInitBuiltinAuthService_AutoProvision(t *testing.T) {
	t.Parallel()

	t.Run("ProvisionsAdminWhenNoUsers", func(t *testing.T) {
		t.Parallel()
		cfg := testConfig(t.TempDir(), config.InitialAdmin{
			Username: "testadmin",
			Password: "securepass123",
		})

		result, setupRequired, err := initBuiltinAuthService(testContext(t), cfg, nil, nil)
		require.NoError(t, err)
		assert.False(t, setupRequired, "setup should not be required after auto-provisioning")

		// Verify user was created
		count, err := result.AuthService.CountUsers(testContext(t))
		require.NoError(t, err)
		assert.Equal(t, int64(1), count)

		// Verify user has correct role and username
		user, err := result.UserStore.GetByUsername(testContext(t), "testadmin")
		require.NoError(t, err)
		assert.Equal(t, "testadmin", user.Username)
		assert.Equal(t, authmodel.RoleAdmin, user.Role)
	})

	t.Run("SkipsWhenUsersExist", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()
		cfg := testConfig(tmpDir, config.InitialAdmin{
			Username: "testadmin",
			Password: "securepass123",
		})

		// Pre-create a user directly in the store
		store, err := fileuser.New(cfg.Paths.UsersDir)
		require.NoError(t, err)
		existing := authmodel.NewUser("existinguser", "$2a$12$K8gHXqrFdFvMwJBG0VlJGuAGz3FwBmTm8xnNQblN2tCxrQgPLmwHa", authmodel.RoleAdmin)
		require.NoError(t, store.Create(testContext(t), existing))

		result, setupRequired, err := initBuiltinAuthService(testContext(t), cfg, nil, nil)
		require.NoError(t, err)
		assert.False(t, setupRequired)

		// Verify no additional user was created
		count, err := result.AuthService.CountUsers(testContext(t))
		require.NoError(t, err)
		assert.Equal(t, int64(1), count)
	})

	t.Run("SkipsWhenNotConfigured", func(t *testing.T) {
		t.Parallel()
		cfg := testConfig(t.TempDir(), config.InitialAdmin{})

		_, setupRequired, err := initBuiltinAuthService(testContext(t), cfg, nil, nil)
		require.NoError(t, err)
		assert.True(t, setupRequired, "setup should be required when initial_admin is not configured")
	})

	t.Run("FailsOnInvalidPassword", func(t *testing.T) {
		t.Parallel()
		cfg := testConfig(t.TempDir(), config.InitialAdmin{
			Username: "testadmin",
			Password: "short", // less than 8 characters
		})

		_, _, err := initBuiltinAuthService(testContext(t), cfg, nil, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to auto-provision initial admin user")
	})

	t.Run("Idempotent", func(t *testing.T) {
		t.Parallel()
		tmpDir := t.TempDir()
		cfg := testConfig(tmpDir, config.InitialAdmin{
			Username: "testadmin",
			Password: "securepass123",
		})

		// First call: provisions the user
		_, setupRequired, err := initBuiltinAuthService(testContext(t), cfg, nil, nil)
		require.NoError(t, err)
		assert.False(t, setupRequired)

		// Second call: should not create a duplicate
		result, setupRequired, err := initBuiltinAuthService(testContext(t), cfg, nil, nil)
		require.NoError(t, err)
		assert.False(t, setupRequired)

		count, err := result.AuthService.CountUsers(testContext(t))
		require.NoError(t, err)
		assert.Equal(t, int64(1), count)
	})
}

// TestInitBuiltinAuthService_UserCanAuthenticate verifies that the auto-provisioned
// user can actually authenticate (password was hashed correctly).
func TestInitBuiltinAuthService_UserCanAuthenticate(t *testing.T) {
	t.Parallel()
	cfg := testConfig(t.TempDir(), config.InitialAdmin{
		Username: "authadmin",
		Password: "mypassword123",
	})

	result, _, err := initBuiltinAuthService(testContext(t), cfg, nil, nil)
	require.NoError(t, err)

	// Authenticate via the auth service
	user, err := result.AuthService.Authenticate(testContext(t), "authadmin", "mypassword123")
	require.NoError(t, err)
	assert.Equal(t, "authadmin", user.Username)
	assert.Equal(t, authmodel.RoleAdmin, user.Role)

	// Wrong password should fail
	_, err = result.AuthService.Authenticate(testContext(t), "authadmin", "wrongpassword")
	require.Error(t, err)
}

func TestServerUsesEvaluatedBasePathForOIDCAndAPI(t *testing.T) {
	const envKey = "DAGU_TEST_BASE_PATH_SEGMENT"
	t.Setenv(envKey, "dagu")

	ctx := testContext(t)
	cfg := &config.Config{
		Server: config.Server{
			BasePath:    "/${" + envKey + "}",
			APIBasePath: "/rest",
		},
	}

	srv := &Server{
		config: cfg,
		funcsConfig: funcsConfig{
			BasePath:    evaluateConfiguredBasePath(ctx, cfg.Server.BasePath),
			APIBasePath: cfg.Server.APIBasePath,
		},
		builtinOIDCCfg: &frontendauth.BuiltinOIDCConfig{
			OAuth2Config:  &oauth2.Config{},
			LoginBasePath: "/dagu",
		},
	}

	assert.Equal(t, "/dagu/rest", srv.configureAPIPath(ctx))

	r := chi.NewMux()
	srv.setupOIDCRoutes(r, srv.funcsConfig.BasePath)

	loginRecorder := httptest.NewRecorder()
	r.ServeHTTP(loginRecorder, httptest.NewRequest(http.MethodGet, "/dagu/oidc-login", nil))
	assert.Equal(t, http.StatusFound, loginRecorder.Code)

	rootLoginRecorder := httptest.NewRecorder()
	r.ServeHTTP(rootLoginRecorder, httptest.NewRequest(http.MethodGet, "/oidc-login", nil))
	assert.Equal(t, http.StatusNotFound, rootLoginRecorder.Code)

	callbackRecorder := httptest.NewRecorder()
	r.ServeHTTP(callbackRecorder, httptest.NewRequest(http.MethodGet, "/dagu/oidc-callback", nil))
	assert.Equal(t, http.StatusFound, callbackRecorder.Code)
	assert.Contains(t, callbackRecorder.Header().Get("Location"), "/dagu/login?error=")

	rootCallbackRecorder := httptest.NewRecorder()
	r.ServeHTTP(rootCallbackRecorder, httptest.NewRequest(http.MethodGet, "/oidc-callback", nil))
	assert.Equal(t, http.StatusNotFound, rootCallbackRecorder.Code)
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
