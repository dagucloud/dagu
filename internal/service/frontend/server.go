// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package frontend

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"mime"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/go-chi/httplog/v2"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/dagucloud/dagu/internal/agent"
	"github.com/dagucloud/dagu/internal/agentoauth"
	authmodel "github.com/dagucloud/dagu/internal/auth"
	"github.com/dagucloud/dagu/internal/cmn/backoff"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/cmn/crypto"
	"github.com/dagucloud/dagu/internal/cmn/eval"
	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	cmnschema "github.com/dagucloud/dagu/internal/cmn/schema"
	"github.com/dagucloud/dagu/internal/cmn/signalctx"
	"github.com/dagucloud/dagu/internal/cmn/telemetry"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/gitsync"
	"github.com/dagucloud/dagu/internal/license"
	_ "github.com/dagucloud/dagu/internal/llm/allproviders" // Register LLM providers
	"github.com/dagucloud/dagu/internal/remotenode"
	"github.com/dagucloud/dagu/internal/runtime"
	"github.com/dagucloud/dagu/internal/service/audit"
	authservice "github.com/dagucloud/dagu/internal/service/auth"
	"github.com/dagucloud/dagu/internal/service/chatbridge"
	"github.com/dagucloud/dagu/internal/service/coordinator"
	"github.com/dagucloud/dagu/internal/service/eventstore"
	"github.com/dagucloud/dagu/internal/service/frontend/api/pathutil"
	apiv1 "github.com/dagucloud/dagu/internal/service/frontend/api/v1"
	"github.com/dagucloud/dagu/internal/service/frontend/auth"
	"github.com/dagucloud/dagu/internal/service/frontend/metrics"
	"github.com/dagucloud/dagu/internal/service/frontend/sse"
	"github.com/dagucloud/dagu/internal/service/frontend/terminal"
	incidentservice "github.com/dagucloud/dagu/internal/service/incident"
	dagumcp "github.com/dagucloud/dagu/internal/service/mcp"
	notificationservice "github.com/dagucloud/dagu/internal/service/notification"
	"github.com/dagucloud/dagu/internal/service/oidcprovision"
	"github.com/dagucloud/dagu/internal/service/resource"
	"github.com/dagucloud/dagu/internal/tunnel"
	"github.com/dagucloud/dagu/internal/upgrade"
	workspacepkg "github.com/dagucloud/dagu/internal/workspace"
)

const (
	serverShutdownTimeout = 10 * time.Second
	httpShutdownBudget    = 5 * time.Second
)

type shutdownActions struct {
	stopSync               func() error
	shutdownSSEMultiplexer func()
	beforeHTTPShutdown     func()
	disableHTTPKeepAlives  func()
	shutdownHTTP           func(context.Context) error
	shutdownTerminal       func(context.Context) error
	closeAudit             func() error
}

// RouteRegistrar registers additional HTTP routes on the frontend server.
type RouteRegistrar func(context.Context, chi.Router, string)

// Server represents the HTTP server for the frontend application.
type Server struct {
	apiV1                 *apiv1.API
	agentAPI              *agent.API
	agentConfigStore      agent.ConfigStore
	config                *config.Config
	httpServer            *http.Server
	funcsConfig           funcsConfig
	builtinOIDCCfg        *auth.BuiltinOIDCConfig
	authService           *authservice.Service
	auditService          *audit.Service
	auditStore            AuditStore
	eventService          *eventstore.Service
	incidentService       *incidentservice.Service
	notificationService   *notificationservice.Service
	incidentStateFile     MonitorStateFileFunc
	notificationStateFile MonitorStateFileFunc
	syncService           gitsync.Service
	listener              net.Listener
	appStream             *sse.AppStreamService
	sseMultiplexer        *sse.Multiplexer
	terminalManager       *terminal.Manager
	metricsRegistry       *prometheus.Registry
	tunnelAPIOpts         []apiv1.APIOption
	tunnelService         *tunnel.Service
	dagStore              exec.DAGStore
	licenseManager        *license.Manager
	remoteNodeResolver    *remotenode.Resolver
	upgradeStore          upgrade.CacheStore
	agentAPICallback      func(*agent.API)
	routeRegistrars       []RouteRegistrar
}

// ServerOption is a functional option for configuring the Server.
type ServerOption func(*Server)

// WithListener sets a pre-bound listener for the server (useful for tests).
func WithListener(l net.Listener) ServerOption {
	return func(s *Server) {
		s.listener = l
	}
}

// WithLicenseManager sets the license manager for feature gating.
func WithLicenseManager(m *license.Manager) ServerOption {
	return func(s *Server) {
		if m != nil {
			s.licenseManager = m
		}
	}
}

// WithAgentAPICallback registers a callback that is invoked with the agent API
// instance after the server creates it. This allows external consumers (e.g. the
// Telegram bot) to receive the agent API without the server permanently exposing it.
func WithAgentAPICallback(fn func(*agent.API)) ServerOption {
	return func(s *Server) {
		s.agentAPICallback = fn
	}
}

// WithTunnelService enables real-time tunnel status via the API.
func WithTunnelService(ts *tunnel.Service) ServerOption {
	return func(s *Server) {
		if ts != nil {
			s.tunnelService = ts
			s.tunnelAPIOpts = append(s.tunnelAPIOpts, apiv1.WithTunnelService(ts))
		}
	}
}

// WithAPIOption appends an API option that will be applied when the server
// constructs the v1 API handler.
func WithAPIOption(opt apiv1.APIOption) ServerOption {
	return func(s *Server) {
		if opt != nil {
			s.tunnelAPIOpts = append(s.tunnelAPIOpts, opt)
		}
	}
}

// RegisterRoutes appends a route registrar that is applied before API routes
// are mounted.
func (srv *Server) RegisterRoutes(fn RouteRegistrar) {
	if fn != nil {
		srv.routeRegistrars = append(srv.routeRegistrars, fn)
	}
}

// NewServer constructs a Server from the provided configuration, stores, and services.
// Returns an error if initialization fails (e.g., when builtin auth fails to initialize).
func NewServer(ctx context.Context, cfg *config.Config, dr exec.DAGStore, drs exec.DAGRunStore, qs exec.QueueStore, ps exec.ProcStore, drm runtime.Manager, cc coordinator.Client, sr exec.ServiceRegistry, mr *prometheus.Registry, collector *telemetry.Collector, rs *resource.Service, stores StoreFactories, opts ...ServerOption) (*Server, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	remoteNodes := make([]string, 0, len(cfg.Server.RemoteNodes))
	for _, n := range cfg.Server.RemoteNodes {
		remoteNodes = append(remoteNodes, n.Name)
	}

	var (
		apiOpts         []apiv1.APIOption
		builtinOIDCCfg  *auth.BuiltinOIDCConfig
		oidcEnabled     bool
		oidcButtonLabel string
		setupRequired   bool
	)
	if stores.SnapshotStoreFactory != nil {
		apiOpts = append(apiOpts, apiv1.WithSnapshotStoreFactory(stores.SnapshotStoreFactory))
	}
	if stores.WorkspaceBaseConfigStoreFactory != nil {
		apiOpts = append(apiOpts, apiv1.WithWorkspaceBaseConfigStoreFactory(stores.WorkspaceBaseConfigStoreFactory))
	}
	evaluatedBasePath := evaluateConfiguredBasePath(ctx, cfg.Server.BasePath)

	auditSvc, auditStore, err := initAuditService(cfg, stores.AuditStoreFactory)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize audit service: %w", err)
	}
	eventSvc, err := initEventService(cfg, stores.EventStoreFactory)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize event service: %w", err)
	}
	syncSvc := initSyncService(ctx, cfg)
	if syncSvc != nil {
		apiOpts = append(apiOpts, apiv1.WithSyncService(syncSvc))
	}

	if cfg.Paths.BaseConfig != "" && stores.BaseConfigStoreFactory != nil {
		baseConfigStore, bcErr := stores.BaseConfigStoreFactory(cfg.Paths.BaseConfig)
		if bcErr != nil {
			logger.Warn(ctx, "Failed to create base config store", tag.Error(bcErr))
		} else {
			apiOpts = append(apiOpts, apiv1.WithBaseConfigStore(baseConfigStore))
		}
	}

	cacheLimits := cfg.Cache.Limits()
	memoryCache := fileutil.NewCache[string]("agent_memory", cacheLimits.DAG.Limit, cacheLimits.DAG.TTL)
	memoryCache.StartEviction(ctx)
	if collector != nil {
		collector.RegisterCache(memoryCache)
	}
	var agentStores agent.RuntimeStores
	if stores.AgentStoresFactory != nil {
		agentStores = stores.AgentStoresFactory(ctx, cfg, AgentStoresOptions{
			MemoryCache:      memoryCache,
			SeedReferences:   true,
			SeedExampleSouls: true,
		})
	}
	agentConfigStore := agentStores.ConfigStore
	agentModelStore := agentStores.ModelStore
	agentSoulStore := agentStores.SoulStore
	memoryStore := agentStores.MemoryStore
	referencesDir := agentStores.ReferencesDir
	agentOAuthManager := agentStores.OAuthManager

	var docStore agent.DocStore
	if stores.DocStoreFactory != nil {
		docStore = stores.DocStoreFactory(cfg)
	}

	var authSvc *authservice.Service
	if cfg.Server.Auth.Mode == config.AuthModeBuiltin {
		if stores.BuiltinAuthFactory == nil {
			return nil, errors.New("builtin auth persistence is not configured")
		}
		result, isSetupRequired, err := stores.BuiltinAuthFactory(ctx, cfg)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize builtin auth service: %w", err)
		}
		authSvc = result.AuthService
		setupRequired = isSetupRequired
		apiOpts = append(apiOpts, apiv1.WithAuthService(result.AuthService))

		oidcCfg := cfg.Server.Auth.OIDC
		if oidcCfg.IsConfigured() {
			oidcEnabled = true
			oidcButtonLabel = oidcCfg.ButtonLabel

			provisionCfg := oidcprovision.Config{
				Issuer:         oidcCfg.Issuer,
				AutoSignup:     oidcCfg.AutoSignup,
				DefaultRole:    authmodel.Role(oidcCfg.RoleMapping.DefaultRole),
				AllowedDomains: oidcCfg.AllowedDomains,
				Whitelist:      oidcCfg.Whitelist,
				RoleMapping: oidcprovision.RoleMapperConfig{
					GroupsClaim:         oidcCfg.RoleMapping.GroupsClaim,
					GroupMappings:       oidcCfg.RoleMapping.GroupMappings,
					RoleAttributePath:   oidcCfg.RoleMapping.RoleAttributePath,
					RoleAttributeStrict: oidcCfg.RoleMapping.RoleAttributeStrict,
					SkipOrgRoleSync:     oidcCfg.RoleMapping.SkipOrgRoleSync,
					DefaultRole:         authmodel.Role(oidcCfg.RoleMapping.DefaultRole),
				},
			}
			provisionSvc, err := oidcprovision.New(result.UserStore, provisionCfg)
			if err != nil {
				return nil, fmt.Errorf("failed to create OIDC provisioning service: %w", err)
			}

			builtinOIDCCfg, err = auth.InitBuiltinOIDCConfig(
				ctx,
				oidcCfg,
				result.AuthService,
				provisionSvc,
				evaluatedBasePath,
			)
			if err != nil {
				return nil, fmt.Errorf("failed to initialize builtin OIDC: %w", err)
			}

			logger.Info(ctx, "OIDC enabled for builtin auth mode",
				slog.String("issuer", oidcCfg.Issuer),
				slog.Bool("autoSignup", oidcCfg.AutoSignup),
				slog.String("defaultRole", oidcCfg.RoleMapping.DefaultRole))
		}
	}

	// Initialize remote node store and resolver
	var (
		remoteNodeResolver *remotenode.Resolver
		encryptor          *crypto.Encryptor
		licenseChecker     license.Checker
	)
	encKey, encErr := crypto.ResolveKey(cfg.Paths.DataDir)
	if encErr != nil {
		logger.Warn(ctx, "Failed to resolve encryption key for encrypted stores", tag.Error(encErr))
	}
	if encErr == nil {
		encryptor, encErr = crypto.NewEncryptor(encKey)
		if encErr != nil {
			logger.Warn(ctx, "Failed to create encryptor for encrypted stores", tag.Error(encErr))
		} else if stores.RemoteNodeStoreFactory != nil {
			rnStore, rnErr := stores.RemoteNodeStoreFactory(cfg, encryptor)
			if rnErr != nil {
				logger.Warn(ctx, "Failed to create remote node store", tag.Error(rnErr))
			} else {
				remoteNodeResolver = remotenode.NewResolver(cfg.Server.RemoteNodes, rnStore)
				apiOpts = append(apiOpts,
					apiv1.WithRemoteNodeResolver(remoteNodeResolver),
					apiv1.WithRemoteNodeStore(rnStore),
				)
			}
		}
	}
	if remoteNodeResolver == nil {
		// Fallback: resolver with config nodes only (no store)
		remoteNodeResolver = remotenode.NewResolver(cfg.Server.RemoteNodes, nil)
		apiOpts = append(apiOpts, apiv1.WithRemoteNodeResolver(remoteNodeResolver))
	}

	// Update template remote nodes list to include store-managed nodes
	if names, err := remoteNodeResolver.ListNames(ctx); err == nil && len(names) > 0 {
		remoteNodes = names
	}

	if agentStores.SecretStore != nil {
		apiOpts = append(apiOpts, apiv1.WithSecretStore(agentStores.SecretStore))
	}
	if agentStores.ProfileStore != nil {
		apiOpts = append(apiOpts, apiv1.WithProfileStore(agentStores.ProfileStore))
	}
	if agentOAuthManager != nil {
		apiOpts = append(apiOpts, apiv1.WithAgentOAuthManager(agentOAuthManager))
	}

	if stores.DAGSettingsStoreFactory != nil {
		store, err := stores.DAGSettingsStoreFactory(cfg)
		if err != nil {
			logger.Warn(ctx, "Failed to create DAG settings store", tag.Error(err))
		} else {
			apiOpts = append(apiOpts, apiv1.WithDAGSettingsStore(store))
		}
	}

	var notificationSvc *notificationservice.Service
	if encryptor != nil && stores.NotificationStoreFactory != nil {
		store, err := stores.NotificationStoreFactory(cfg, encryptor)
		if err != nil {
			logger.Warn(ctx, "Failed to create notification settings store", tag.Error(err))
		} else {
			notificationSvc = notificationservice.New(
				store,
				dr,
				notificationservice.WithPublicURL(cfg.Server.PublicURL),
			)
			apiOpts = append(apiOpts, apiv1.WithNotificationService(notificationSvc))
		}
	} else if encryptor == nil {
		logger.Warn(ctx, "Notification settings store is disabled because encrypted storage is not available")
	}

	var incidentSvc *incidentservice.Service
	if encryptor != nil && stores.IncidentStoreFactory != nil {
		store, err := stores.IncidentStoreFactory(cfg, encryptor)
		if err != nil {
			logger.Warn(ctx, "Failed to create incident settings store", tag.Error(err))
		} else {
			incidentSvc = incidentservice.New(
				store,
				incidentservice.WithIncidentsEnabled(func() bool {
					return license.HasActiveLicense(licenseChecker)
				}),
				incidentservice.WithPublicURL(cfg.Server.PublicURL),
			)
			apiOpts = append(apiOpts, apiv1.WithIncidentService(incidentSvc))
		}
	} else if encryptor == nil {
		logger.Warn(ctx, "Incident settings store is disabled because encrypted storage is not available")
	}

	// Initialize workspace store
	var wsStore workspacepkg.Store
	if stores.WorkspaceStoreFactory != nil {
		var wsErr error
		wsStore, wsErr = stores.WorkspaceStoreFactory(cfg)
		if wsErr != nil {
			logger.Warn(ctx, "Failed to create workspace store", tag.Error(wsErr))
		} else {
			apiOpts = append(apiOpts, apiv1.WithWorkspaceStore(wsStore))
		}
	}

	auditEnabled := func() bool {
		if auditSvc == nil {
			return false
		}
		if licenseChecker == nil {
			return true
		}
		return licenseChecker.IsFeatureEnabled(license.FeatureAudit)
	}

	var agentAPI *agent.API
	if agentConfigStore != nil {
		agentAPI, err = initAgentAPI(ctx, agentConfigStore, agentModelStore, agentSoulStore, agentOAuthManager, cfg, referencesDir, dr, drs, auditSvc, auditEnabled, eventSvc, memoryStore, docStore, wsStore, newRemoteNodeAdapter(remoteNodeResolver), stores.AgentSessionStoreFactory)
		if err != nil {
			logger.Warn(ctx, "Failed to initialize agent API", tag.Error(err))
		}
	}

	var (
		upgradeStore      upgrade.CacheStore
		updateInfoChecker UpdateChecker
	)
	if cfg.Server.CheckUpdates && stores.UpgradeCheckStoreFactory != nil {
		upgradeStore, err = stores.UpgradeCheckStoreFactory(cfg)
		if err != nil {
			logger.Warn(ctx, "Failed to create upgrade check store", tag.Error(err))
		} else {
			updateInfoChecker = &updateChecker{store: upgradeStore}
		}
	}

	// Note: SSO/OIDC gating is applied after opts are processed (see below)

	srv := &Server{
		config:                cfg,
		agentAPI:              agentAPI,
		agentConfigStore:      agentConfigStore,
		builtinOIDCCfg:        builtinOIDCCfg,
		authService:           authSvc,
		auditService:          auditSvc,
		auditStore:            auditStore,
		eventService:          eventSvc,
		incidentService:       incidentSvc,
		notificationService:   notificationSvc,
		incidentStateFile:     stores.IncidentMonitorStateFileFunc,
		notificationStateFile: stores.NotificationMonitorStateFileFunc,
		syncService:           syncSvc,
		metricsRegistry:       mr,
		dagStore:              dr,
		remoteNodeResolver:    remoteNodeResolver,
		upgradeStore:          upgradeStore,
		funcsConfig: funcsConfig{
			NavbarColor:           cfg.UI.NavbarColor,
			NavbarTitle:           cfg.UI.NavbarTitle,
			BasePath:              evaluatedBasePath,
			APIBasePath:           cfg.Server.APIBasePath,
			TZ:                    cfg.Core.TZ,
			TzOffsetInSec:         cfg.Core.TzOffsetInSec,
			MaxDashboardPageLimit: cfg.UI.MaxDashboardPageLimit,
			RemoteNodes:           remoteNodes,
			Permissions:           cfg.Server.Permissions,
			Paths:                 cfg.Paths,
			AuthMode:              cfg.Server.Auth.Mode,
			OIDCEnabled:           oidcEnabled,
			OIDCButtonLabel:       oidcButtonLabel,
			TerminalEnabled:       cfg.Server.Terminal.Enabled && authSvc != nil,
			GitSyncEnabled:        cfg.GitSync.Enabled,
			WorkspaceStore:        wsStore,
			SetupRequiredChecker:  &setupChecker{authSvc: authSvc, fallback: setupRequired},
			UpdateChecker:         updateInfoChecker,
			AgentEnabledChecker:   agentConfigStore,
		},
	}

	for _, opt := range opts {
		opt(srv)
	}
	if srv.notificationService != nil {
		srv.notificationService.SetPublicURLResolver(func() string {
			if srv.config.Server.PublicURL != "" {
				return srv.config.Server.PublicURL
			}
			if srv.tunnelService != nil {
				return publicURLWithBasePath(srv.tunnelService.PublicURL(), evaluatedBasePath)
			}
			return ""
		})
	}
	if srv.incidentService != nil {
		srv.incidentService.SetPublicURLResolver(func() string {
			if srv.config.Server.PublicURL != "" {
				return srv.config.Server.PublicURL
			}
			if srv.tunnelService != nil {
				return publicURLWithBasePath(srv.tunnelService.PublicURL(), evaluatedBasePath)
			}
			return ""
		})
	}

	srv.funcsConfig.APIBasePath = srv.config.Server.APIBasePath

	// Notify callback with the agent API instance (if both are set).
	if srv.agentAPICallback != nil && srv.agentAPI != nil {
		srv.agentAPICallback(srv.agentAPI)
	}

	// Populate license checker and manager in funcsConfig after opts
	if srv.licenseManager != nil {
		licenseChecker = srv.licenseManager.Checker()
		srv.funcsConfig.LicenseChecker = licenseChecker
		srv.funcsConfig.LicenseManager = srv.licenseManager
		if srv.builtinOIDCCfg != nil {
			srv.builtinOIDCCfg.LicenseChecker = licenseChecker
		}
	}

	if srv.licenseManager != nil && srv.builtinOIDCCfg != nil && !srv.licenseManager.Checker().IsFeatureEnabled(license.FeatureSSO) {
		logger.Warn(ctx, "SSO (OIDC) is configured but currently unavailable because the active license does not enable it")
	}

	if srv.auditService != nil {
		apiOpts = append(apiOpts, apiv1.WithAuditService(srv.auditService))
	}
	if eventSvc != nil {
		apiOpts = append(apiOpts, apiv1.WithEventService(eventSvc))
	}
	apiOpts = append(apiOpts, apiv1.WithDAGMutationNotifier(func(fileName string) {
		if srv.sseMultiplexer == nil {
			return
		}
		srv.sseMultiplexer.WakeTopicType(sse.TopicTypeDAGsList)
		srv.sseMultiplexer.WakeTopic(sse.TopicTypeDAG, fileName)
	}))
	apiOpts = append(apiOpts, apiv1.WithDocMutationNotifier(func() {
		if srv.sseMultiplexer == nil {
			return
		}
		srv.sseMultiplexer.WakeTopicType(sse.TopicTypeDocTree)
		srv.sseMultiplexer.WakeTopicType(sse.TopicTypeDoc)
	}))

	// Pass license manager to API
	if srv.licenseManager != nil {
		apiOpts = append(apiOpts, apiv1.WithLicenseManager(srv.licenseManager))
	}

	allAPIOptions := append(apiOpts, srv.tunnelAPIOpts...)
	if srv.agentConfigStore != nil {
		allAPIOptions = append(allAPIOptions, apiv1.WithAgentConfigStore(srv.agentConfigStore))
	}
	if agentModelStore != nil {
		allAPIOptions = append(allAPIOptions, apiv1.WithAgentModelStore(agentModelStore))
	}

	if memoryStore != nil {
		allAPIOptions = append(allAPIOptions, apiv1.WithAgentMemoryStore(memoryStore))
	}
	if agentSoulStore != nil {
		allAPIOptions = append(allAPIOptions, apiv1.WithAgentSoulStore(agentSoulStore))
	}
	if docStore != nil {
		allAPIOptions = append(allAPIOptions, apiv1.WithDocStore(docStore))
	}
	if srv.agentAPI != nil {
		allAPIOptions = append(allAPIOptions, apiv1.WithAgentAPI(srv.agentAPI))
	}

	srv.apiV1 = apiv1.New(dr, drs, qs, ps, drm, cfg, cc, sr, mr, rs, allAPIOptions...)

	return srv, nil
}

// updateChecker implements UpdateChecker by reading from the upgrade cache store.
type updateChecker struct {
	store upgrade.CacheStore
}

func (u *updateChecker) GetUpdateInfo() (bool, string) {
	if u.store == nil {
		return false, ""
	}
	cache := upgrade.GetCachedUpdateInfo(u.store)
	if cache == nil {
		return false, ""
	}
	return cache.UpdateAvailable, cache.LatestVersion
}

// setupChecker implements SetupRequiredChecker by counting users via the auth service.
// Once users exist, caches the result to avoid hitting the store on every page load.
type setupChecker struct {
	authSvc       *authservice.Service
	fallback      bool
	setupComplete atomic.Bool
}

func (s *setupChecker) IsSetupRequired(ctx context.Context) bool {
	if s.setupComplete.Load() {
		return false
	}
	if s.authSvc == nil {
		return s.fallback
	}
	count, err := s.authSvc.CountUsers(ctx)
	if err != nil {
		return s.fallback
	}
	if count > 0 {
		s.setupComplete.Store(true)
		return false
	}
	return true
}

// initAuditService creates the configured audit store and service.
func initAuditService(cfg *config.Config, factory AuditStoreFactory) (*audit.Service, AuditStore, error) {
	if factory == nil {
		return nil, nil, nil
	}
	store, err := factory(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create audit store: %w", err)
	}
	if store == nil {
		return nil, nil, nil
	}

	return audit.New(store), store, nil
}

// initSyncService creates and returns a Git sync service if enabled.
func initSyncService(ctx context.Context, cfg *config.Config) gitsync.Service {
	if !cfg.GitSync.Enabled {
		return nil
	}

	syncCfg := gitsync.NewConfigFromGlobal(cfg.GitSync)
	svc := gitsync.NewService(syncCfg, cfg.Paths.DAGsDir, cfg.Paths.DataDir, cfg.Paths.BaseConfig)

	if syncCfg.AutoSync.Enabled {
		if err := svc.Start(ctx); err != nil {
			logger.Error(ctx, "Failed to start git sync auto-sync", tag.Error(err))
		} else {
			logger.Info(ctx, "Git sync auto-sync started",
				slog.String("repository", syncCfg.Repository),
				slog.String("branch", syncCfg.Branch),
				slog.Int("interval", syncCfg.AutoSync.Interval))
		}
	}

	logger.Info(ctx, "Git sync service initialized",
		slog.String("repository", syncCfg.Repository),
		slog.String("branch", syncCfg.Branch))

	return svc
}

// initAgentAPI creates and returns an agent API.
// The API uses the config store to check enabled status and resolve providers via the model store.
func initAgentAPI(ctx context.Context, configStore agent.ConfigStore, modelStore agent.ModelStore, soulStore agent.SoulStore, oauthManager *agentoauth.Manager, cfg *config.Config, referencesDir string, dagStore exec.DAGStore, dagRunStore exec.DAGRunStore, auditSvc *audit.Service, auditEnabled func() bool, eventSvc *eventstore.Service, memoryStore agent.MemoryStore, docStore agent.DocStore, workspaceStore workspacepkg.Store, remoteResolver agent.RemoteContextResolver, sessionFactory AgentSessionStoreFactory) (*agent.API, error) {
	var sessStore agent.SessionStore
	if sessionFactory != nil {
		var err error
		sessStore, err = sessionFactory(cfg)
		if err != nil {
			logger.Warn(ctx, "Failed to create session store, persistence disabled", tag.Error(err))
		}
	}
	paths := &cfg.Paths

	hooks := agent.NewHooks()
	hooks.OnBeforeToolExec(newAgentPolicyHook(configStore, auditSvc, auditEnabled))
	if auditSvc != nil {
		hooks.OnAfterToolExec(newAgentAuditHook(auditSvc, auditEnabled))
	}

	api := agent.NewAPI(agent.APIConfig{
		ConfigStore:           configStore,
		ModelStore:            modelStore,
		SoulStore:             soulStore,
		WorkingDir:            paths.DAGsDir,
		Logger:                slog.Default(),
		SessionStore:          sessStore,
		DAGStore:              dagStore,
		DAGRunStore:           dagRunStore,
		Hooks:                 hooks,
		EventService:          eventSvc,
		MemoryStore:           memoryStore,
		DocStore:              docStore,
		WorkspaceStore:        workspaceStore,
		OAuthManager:          oauthManager,
		RemoteContextResolver: remoteResolver,
		Environment: agent.EnvironmentInfo{
			DAGsDir:        paths.DAGsDir,
			DocsDir:        paths.DocsDir,
			LogDir:         paths.LogDir,
			DataDir:        paths.DataDir,
			SessionsDir:    paths.SessionsDir,
			ConfigFile:     paths.ConfigFileUsed,
			WorkingDir:     paths.DAGsDir,
			BaseConfigFile: paths.BaseConfig,
			ReferencesDir:  referencesDir,
		},
	})

	api.StartCleanup(ctx)

	logger.Info(ctx, "Agent API initialized")

	return api, nil
}

func initEventService(cfg *config.Config, factory EventStoreFactory) (*eventstore.Service, error) {
	if factory == nil {
		return nil, nil
	}
	store, err := factory(cfg)
	if err != nil {
		return nil, err
	}
	if store == nil {
		return nil, nil
	}
	return eventstore.New(store), nil
}

// newAgentAuditHook returns a hook that logs agent tool executions to the audit service.
func newAgentAuditHook(auditSvc *audit.Service, auditEnabled func() bool) agent.AfterToolExecHookFunc {
	return func(_ context.Context, info agent.ToolExecInfo, result agent.ToolOut) {
		if info.Audit == nil || !isAuditEnabled(auditSvc, auditEnabled) {
			return // tool opted out of audit
		}

		details := make(map[string]any)
		if info.Audit.DetailExtractor != nil {
			details = info.Audit.DetailExtractor(info.Input)
		}
		maps.Copy(details, result.AuditDetails)
		if result.IsError {
			details["failed"] = true
		}
		details["session_id"] = info.SessionID

		detailsJSON, _ := json.Marshal(details)
		entry := audit.NewEntry(audit.CategoryAgent, info.Audit.Action, info.User.UserID, info.User.Username).
			WithDetails(string(detailsJSON)).
			WithIPAddress(info.User.IPAddress)
		_ = auditSvc.Log(context.Background(), entry)
	}
}

func isAuditEnabled(auditSvc *audit.Service, auditEnabled func() bool) bool {
	if auditSvc == nil {
		return false
	}
	if auditEnabled == nil {
		return true
	}
	return auditEnabled()
}

// sanitizedRequestLogger wraps httplog's RequestLogger with URL sanitization
// to redact tokens in query strings.
func sanitizedRequestLogger(httpLogger *httplog.Logger) func(next http.Handler) http.Handler {
	loggerMiddleware := httplog.RequestLogger(httpLogger)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			logReq := redactTokenFromRequest(r)

			// Pass original request to next handler, but redacted request to logger
			passthrough := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				next.ServeHTTP(w, r)
			})

			loggerMiddleware(passthrough).ServeHTTP(w, logReq)
		})
	}
}

// redactTokenFromRequest returns a request with the token query parameter redacted.
// If no token is present, the original request is returned unchanged.
func redactTokenFromRequest(r *http.Request) *http.Request {
	if r.URL.RawQuery == "" || !strings.Contains(r.URL.RawQuery, "token=") {
		return r
	}

	q := r.URL.Query()
	if !q.Has("token") {
		return r
	}

	redacted := r.Clone(r.Context())
	q.Set("token", "[REDACTED]")
	redacted.URL.RawQuery = q.Encode()
	redacted.RequestURI = redacted.URL.RequestURI()

	return redacted
}

// buildPublicPaths returns the set of public endpoint paths that should be
// excluded from access logging in "non-public" mode.
func buildPublicPaths(basePath, apiBasePath string, metrics config.MetricsAccess) map[string]struct{} {
	paths := []string{
		pathutil.BuildMountedAPIEndpointPath(basePath, apiBasePath, "health"),
		pathutil.BuildMountedAPIEndpointPath(basePath, apiBasePath, "auth/login"),
		pathutil.BuildMountedAPIEndpointPath(basePath, apiBasePath, "auth/setup"),
	}
	if metrics == config.MetricsAccessPublic {
		paths = append(paths, pathutil.BuildMountedAPIEndpointPath(basePath, apiBasePath, "metrics"))
	}
	set := make(map[string]struct{}, len(paths))
	for _, p := range paths {
		set[p] = struct{}{}
	}
	return set
}

// skipPathsMiddleware wraps a middleware to skip it for requests matching any of the given paths.
func skipPathsMiddleware(mw func(http.Handler) http.Handler, skip map[string]struct{}) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		wrapped := mw(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, ok := skip[r.URL.Path]; ok {
				next.ServeHTTP(w, r)
				return
			}
			wrapped.ServeHTTP(w, r)
		})
	}
}

// Serve starts the HTTP server and configures routes.
func (srv *Server) Serve(ctx context.Context) error {
	r := chi.NewMux()
	apiV1BasePath := srv.configureAPIPath(ctx)
	r.Use(auth.PreserveRawRemoteAddr)
	r.Use(middleware.RealIP)
	r.Use(middleware.Compress(5))
	if srv.config.Server.AccessLog != config.AccessLogNone {
		logLevel := slog.LevelInfo
		if srv.config.Core.Debug {
			logLevel = slog.LevelDebug
		}
		requestLogger := httplog.NewLogger("http", httplog.Options{
			LogLevel:         logLevel,
			JSON:             srv.config.Core.LogFormat == "json",
			Concise:          true,
			RequestHeaders:   srv.config.Core.Debug,
			MessageFieldName: "msg",
			ResponseHeaders:  false,
			QuietDownRoutes: []string{
				path.Join(apiV1BasePath, "events"),
				pathutil.BuildPublicEndpointPath(srv.funcsConfig.BasePath, "mcp"),
			},
			QuietDownPeriod: 10 * time.Second,
		})
		logMiddleware := sanitizedRequestLogger(requestLogger)
		if srv.config.Server.AccessLog == config.AccessLogNonPublic {
			skipPaths := buildPublicPaths(srv.funcsConfig.BasePath, srv.config.Server.APIBasePath, srv.config.Server.Metrics)
			logMiddleware = skipPathsMiddleware(logMiddleware, skipPaths)
		}
		r.Use(logMiddleware)
	}
	r.Use(middleware.Recoverer)
	r.Use(securityHeadersMiddleware(srv.config.Server.TLS != nil))
	corsOrigins := srv.config.Server.CORSAllowedOrigins
	allowCredentials := len(corsOrigins) > 0 && !slices.Contains(corsOrigins, "*")
	if !allowCredentials {
		corsOrigins = []string{"*"}
	}
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   corsOrigins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Content-Type", "Authorization", "Content-Encoding", "Accept", "MCP-Protocol-Version", "Mcp-Session-Id", "Last-Event-ID"},
		ExposedHeaders:   []string{"Mcp-Session-Id"},
		AllowCredentials: allowCredentials,
		MaxAge:           300,
	}))
	r.Use(middleware.RedirectSlashes)

	if err := srv.setupRoutes(ctx, r); err != nil {
		return err
	}

	srv.setupRegisteredRoutes(ctx, r, apiV1BasePath)

	if err := srv.setupAPIRoutes(ctx, r, apiV1BasePath); err != nil {
		return err
	}

	if srv.config.Server.Terminal.Enabled && srv.authService != nil {
		srv.setupTerminalRoute(ctx, r, apiV1BasePath)
	}

	if srv.agentAPI != nil && srv.agentConfigStore != nil {
		srv.setupAgentRoutes(ctx, r, apiV1BasePath)
	}

	srv.setupSSERoute(ctx, r, apiV1BasePath)
	srv.setupMCPRoute(ctx, r)

	addr := net.JoinHostPort(srv.config.Server.Host, strconv.Itoa(srv.config.Server.Port))
	srv.httpServer = &http.Server{
		BaseContext: func(net.Listener) context.Context {
			return ctx
		},
		Handler:           r,
		Addr:              addr,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		WriteTimeout:      60 * time.Second,
	}

	metrics.StartUptime(ctx)
	logger.Info(ctx, "Server is starting", tag.Addr(addr))

	srv.startNotificationMonitor(ctx)
	srv.startIncidentMonitor(ctx)
	srv.startPeriodicUpdateCheck(ctx)

	go srv.startServer(ctx)
	srv.setupGracefulShutdown(ctx)

	return nil
}

func (srv *Server) startNotificationMonitor(ctx context.Context) {
	if srv.notificationService == nil || srv.eventService == nil {
		return
	}
	if srv.notificationStateFile == nil {
		return
	}
	stateFile := srv.notificationStateFile(srv.config)
	if stateFile == "" {
		return
	}
	monitor := chatbridge.NewNotificationMonitor(
		srv.eventService,
		stateFile,
		srv.notificationService,
		slog.Default(),
		chatbridge.DefaultNotificationMonitorConfig(),
	)
	go monitor.Run(ctx)
}

func (srv *Server) startIncidentMonitor(ctx context.Context) {
	if srv.incidentService == nil || srv.eventService == nil {
		return
	}
	if srv.incidentStateFile == nil {
		return
	}
	stateFile := srv.incidentStateFile(srv.config)
	if stateFile == "" {
		return
	}
	monitor := chatbridge.NewNotificationMonitor(
		srv.eventService,
		stateFile,
		srv.incidentService,
		slog.Default(),
		incidentMonitorConfig(),
	)
	go monitor.Run(ctx)
}

func incidentMonitorConfig() chatbridge.NotificationMonitorConfig {
	cfg := chatbridge.DefaultNotificationMonitorConfig()
	cfg.UrgentWindow = time.Second
	cfg.SuccessWindow = time.Second
	cfg.InterestedEventTypes = []eventstore.EventType{
		eventstore.TypeDAGRunFailed,
		eventstore.TypeDAGRunSucceeded,
	}
	return cfg
}

// startPeriodicUpdateCheck runs an initial update check and then repeats
// every CacheTTL interval so that long-running servers pick up new releases.
func (srv *Server) startPeriodicUpdateCheck(ctx context.Context) {
	if srv.upgradeStore == nil {
		return
	}
	go func() {
		srv.runAutomaticUpdateCheck(ctx)

		ticker := time.NewTicker(upgrade.CacheTTL)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				srv.runAutomaticUpdateCheck(ctx)
			}
		}
	}()
}

func (srv *Server) runAutomaticUpdateCheck(ctx context.Context) {
	retryCtx := backoff.WithRetryFailureLogLevel(ctx, slog.LevelDebug)
	if _, err := upgrade.CheckAndUpdateCache(retryCtx, srv.upgradeStore, config.Version); err != nil {
		logger.Debug(ctx, "Automatic update check failed", tag.Error(err))
	}
}

func (srv *Server) configureAPIPath(_ context.Context) string {
	return pathutil.BuildMountedAPIPath(srv.funcsConfig.BasePath, srv.config.Server.APIBasePath)
}

// ensureLeadingSlash ensures the path starts with a forward slash.
func ensureLeadingSlash(p string) string {
	if strings.HasPrefix(p, "/") {
		return p
	}
	return "/" + p
}

func (srv *Server) setupRoutes(ctx context.Context, r *chi.Mux) error {
	if srv.config.Server.Headless {
		logger.Info(ctx, "Headless mode enabled: UI is disabled, but API remains active")
		return nil
	}

	basePath := srv.funcsConfig.BasePath
	srv.setupAssetRoutes(r, basePath)
	srv.setupOIDCRoutes(r, basePath)

	indexHandler := srv.useTemplate(ctx, "index.gohtml", "index")
	r.Route("/", func(r chi.Router) {
		r.Get("/*", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
			indexHandler(w, nil)
		})
	})

	return nil
}

func evaluateConfiguredBasePath(ctx context.Context, basePath string) string {
	evaluated, err := eval.String(ctx, basePath, eval.WithOSExpansion())
	if err != nil {
		logger.Warn(ctx, "Failed to evaluate server base path", tag.Path(basePath), tag.Error(err))
		return basePath
	}
	return evaluated
}

func publicURLWithBasePath(publicURL, basePath string) string {
	publicURL = strings.TrimRight(strings.TrimSpace(publicURL), "/")
	if publicURL == "" {
		return ""
	}
	basePath = strings.Trim(strings.TrimSpace(basePath), "/")
	if basePath == "" {
		return publicURL
	}
	return publicURL + "/" + basePath
}

func (srv *Server) setupAssetRoutes(r *chi.Mux, basePath string) {
	assetsPath := ensureLeadingSlash(path.Join(strings.TrimRight(basePath, "/"), "assets/*"))

	fileServer := http.FileServer(http.FS(assetsFS))
	if basePath != "" && basePath != "/" {
		fileServer = http.StripPrefix(strings.TrimRight(basePath, "/"), fileServer)
	}

	r.Get(assetsPath, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", cacheControlForAsset(r.URL.Path))

		// Serve schemas from shared package instead of embedded assets
		if strings.HasSuffix(r.URL.Path, "dag.schema.json") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(cmnschema.DAGSchemaJSON)
			return
		}
		if strings.HasSuffix(r.URL.Path, "config.schema.json") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(cmnschema.ConfigSchemaJSON)
			return
		}

		if ctype := mime.TypeByExtension(path.Ext(r.URL.Path)); ctype != "" {
			w.Header().Set("Content-Type", ctype)
		}
		fileServer.ServeHTTP(w, r)
	})
}

func cacheControlForAsset(assetPath string) string {
	base := path.Base(assetPath)
	lowerBase := strings.ToLower(base)
	if strings.HasSuffix(lowerBase, ".bundle.js") && !strings.EqualFold(base, "bundle.js") {
		return "max-age=31536000, immutable"
	}
	if strings.HasSuffix(lowerBase, ".js") {
		return "no-cache, no-store, must-revalidate"
	}
	return "max-age=86400"
}

func (srv *Server) setupOIDCRoutes(r *chi.Mux, basePath string) {
	if srv.builtinOIDCCfg == nil {
		return
	}
	r.Get(pathutil.BuildPublicEndpointPath(basePath, "oidc-login"), auth.BuiltinOIDCLoginHandler(srv.builtinOIDCCfg))
	r.Get(pathutil.BuildPublicEndpointPath(basePath, "oidc-callback"), auth.BuiltinOIDCCallbackHandler(srv.builtinOIDCCfg))
}

func (srv *Server) setupAPIRoutes(ctx context.Context, r *chi.Mux, apiV1BasePath string) error {
	var setupErr error
	r.Route(apiV1BasePath, func(r chi.Router) {
		if err := srv.apiV1.ConfigureRoutes(ctx, r); err != nil {
			logger.Error(ctx, "Failed to configure API routes", tag.Error(err))
			setupErr = err
		}
	})
	return setupErr
}

func (srv *Server) setupRegisteredRoutes(ctx context.Context, r chi.Router, apiV1BasePath string) {
	for _, register := range srv.routeRegistrars {
		register(ctx, r, apiV1BasePath)
	}
}

func (srv *Server) setupTerminalRoute(ctx context.Context, r *chi.Mux, apiV1BasePath string) {
	shell := srv.config.Core.DefaultShell
	if shell == "" {
		shell = terminal.GetDefaultShell()
	}
	srv.terminalManager = terminal.NewManager(ctx, srv.config.Server.Terminal.MaxSessions)
	var auditChecker license.Checker
	if srv.licenseManager != nil {
		auditChecker = srv.licenseManager.Checker()
	}
	termHandler := terminal.NewHandler(srv.authService, srv.auditService, auditChecker, srv.terminalManager, shell)
	wsPath := path.Join(apiV1BasePath, "terminal/ws")
	r.Get(wsPath, termHandler.ServeHTTP)
	logger.Info(ctx, "Terminal WebSocket route configured", slog.String("path", wsPath))
}

func (srv *Server) setupSSERoute(ctx context.Context, r *chi.Mux, apiV1BasePath string) {
	srv.appStream = nil
	logger.Info(ctx, "App SSE stream disabled; multiplexed SSE is the supported live-update transport")

	var sseMetrics *sse.Metrics
	if srv.metricsRegistry != nil {
		sseMetrics = sse.NewMetrics(srv.metricsRegistry)
	}

	srv.sseMultiplexer = sse.NewMultiplexer(sse.StreamConfig{
		MaxTopicsPerConnection: srv.config.Server.SSE.MaxTopicsPerConnection,
		MaxClients:             srv.config.Server.SSE.MaxClients,
		HeartbeatInterval:      srv.config.Server.SSE.HeartbeatInterval,
		WriteBufferSize:        srv.config.Server.SSE.WriteBufferSize,
		SlowClientTimeout:      srv.config.Server.SSE.SlowClientTimeout,
	}, sseMetrics)
	srv.registerDedicatedSSEFetchers(srv.sseMultiplexer)
	if srv.eventService != nil {
		sse.StartDAGRunEventInvalidation(srv.sseMultiplexer.Context(), srv.eventService, srv.sseMultiplexer, slog.Default(), time.Second)
	}

	multiplexHandler := sse.NewMultiplexHandler(srv.sseMultiplexer, srv.remoteNodeResolver)
	appHandler := sse.NewAppHandler(srv.appStream, srv.remoteNodeResolver)

	authOpts := srv.buildStreamAuthOptions("restricted")

	r.Route(path.Join(apiV1BasePath, "events"), func(r chi.Router) {
		r.Use(auth.QueryTokenMiddleware())
		r.Use(auth.ClientIPMiddleware())
		r.Use(auth.Middleware(authOpts))
		r.Use(srv.injectDefaultStreamUserMiddleware())

		r.Get("/app", appHandler.HandleStream)
		r.Get("/stream", multiplexHandler.HandleStream)
		r.Post("/stream/topics", multiplexHandler.HandleTopicMutation)
	})

	logger.Info(ctx, "SSE routes configured", slog.String("basePath", apiV1BasePath))
}

func (srv *Server) setupMCPRoute(ctx context.Context, r *chi.Mux) {
	mcpPath := pathutil.BuildPublicEndpointPath(srv.funcsConfig.BasePath, "mcp")
	mcpHandler := dagumcp.NewHTTPHandler(srv.apiV1)
	authOpts := srv.buildStreamAuthOptions("Dagu MCP")
	authOpts.RequiredAPIKeySurface = authmodel.APIKeySurfaceMCP
	authOpts.OnDenied = srv.logMCPAuthDenied

	r.Group(func(r chi.Router) {
		r.Use(srv.mcpAuditSeedMiddleware())
		r.Use(auth.QueryTokenMiddleware())
		r.Use(auth.ClientIPMiddleware())
		r.Use(auth.Middleware(authOpts))
		r.Use(srv.injectDefaultStreamUserMiddleware())
		r.Use(srv.mcpAuditSubjectMiddleware())
		r.Use(clearWriteDeadlineMiddleware)
		r.Handle(mcpPath, mcpHandler)
	})

	logger.Info(ctx, "MCP route configured", slog.String("path", mcpPath))
}

func clearWriteDeadlineMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := http.NewResponseController(w).SetWriteDeadline(time.Time{}); err != nil {
			logger.Warn(r.Context(), "Failed to clear write deadline for MCP response",
				tag.Error(err),
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
			)
		}
		next.ServeHTTP(w, r)
	})
}

func (srv *Server) registerDedicatedSSEFetchers(registrar *sse.Multiplexer) {
	registrar.RegisterFetcher(sse.TopicTypeDAGRun, srv.apiV1.GetDAGRunDetailsData)
	registrar.RegisterFetcher(sse.TopicTypeSubDAGRun, srv.apiV1.GetSubDAGRunDetailsData)
	registrar.RegisterFetcher(sse.TopicTypeDAG, srv.apiV1.GetDAGDetailsData)
	registrar.RegisterFetcher(sse.TopicTypeDAGHistory, srv.apiV1.GetDAGHistoryData)
	registrar.RegisterFetcher(sse.TopicTypeDAGRunLogs, srv.apiV1.GetDAGRunLogsData)
	registrar.RegisterFetcher(sse.TopicTypeStepLog, srv.apiV1.GetStepLogData)
	registrar.RegisterFetcher(sse.TopicTypeDAGRuns, srv.apiV1.GetDAGRunsListData)
	registrar.RegisterFetcher(sse.TopicTypeQueues, srv.apiV1.GetQueuesListData)
	registrar.RegisterFetcher(sse.TopicTypeDAGsList, srv.apiV1.GetDAGsListData)
	registrar.RegisterFetcher(sse.TopicTypeDoc, srv.apiV1.GetDocContentData)
	registrar.RegisterFetcher(sse.TopicTypeDocTree, srv.apiV1.GetDocTreeData)

	// Document topics are invalidated by API doc mutations. They should not
	// keep polling the docs store while an SSE connection is open.
	registrar.SetRefreshMode(sse.TopicTypeDoc, sse.TopicRefreshModeOnDemand)
	registrar.SetRefreshMode(sse.TopicTypeDocTree, sse.TopicRefreshModeOnDemand)
	registrar.SetPublishOnWake(sse.TopicTypeDocTree, true)

	// Run-driven topics have an event-store invalidation path. Keeping them on
	// demand avoids repeated full-list and history reads while browsers are
	// connected; DAG-run event collection wakes the exact and aggregate topics.
	if srv.eventService != nil {
		for _, topicType := range []sse.TopicType{
			sse.TopicTypeDAGRun,
			sse.TopicTypeSubDAGRun,
			sse.TopicTypeDAGHistory,
			sse.TopicTypeDAGRuns,
			sse.TopicTypeQueues,
			sse.TopicTypeDAGsList,
		} {
			registrar.SetRefreshMode(topicType, sse.TopicRefreshModeOnDemand)
		}
		registrar.SetPublishOnWake(sse.TopicTypeDAGsList, true)
		registrar.SetPublishOnWake(sse.TopicTypeDAGRuns, true)
		registrar.SetPublishOnWake(sse.TopicTypeQueues, true)
	}
}

func (srv *Server) setupAgentRoutes(ctx context.Context, r *chi.Mux, apiV1BasePath string) {
	authMiddleware := srv.buildAgentAuthMiddleware(ctx)
	// Only the SSE stream endpoint is registered as a manual route.
	// All other agent endpoints are served through the OpenAPI handler.
	streamPath := path.Join(apiV1BasePath, "agent/sessions/{id}/stream")
	r.With(srv.agentAPI.EnabledMiddleware(), authMiddleware).Get(
		streamPath, srv.handleAgentStream(apiV1BasePath),
	)
	logger.Info(ctx, "Agent SSE stream route configured")
}

// handleAgentStream returns a handler that checks for remoteNode and either
// proxies the SSE stream to the remote node or delegates to the local handler.
func (srv *Server) handleAgentStream(apiV1BasePath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		remoteNodeName := r.URL.Query().Get("remoteNode")
		if remoteNodeName == "" || remoteNodeName == "local" {
			srv.agentAPI.HandleStream(w, r)
			return
		}
		srv.proxyAgentStream(w, r, remoteNodeName, apiV1BasePath)
	}
}

// proxyAgentStream proxies the agent SSE stream to a remote node.
// It follows the same streaming pattern as sse/proxy.go:proxyToRemoteNode.
func (srv *Server) proxyAgentStream(w http.ResponseWriter, r *http.Request, nodeName, apiV1BasePath string) {
	if srv.remoteNodeResolver == nil {
		http.Error(w, "remote node resolution not available", http.StatusServiceUnavailable)
		return
	}

	node, err := srv.remoteNodeResolver.GetByName(r.Context(), nodeName)
	if err != nil {
		http.Error(w, fmt.Sprintf("unknown remote node: %s", nodeName), http.StatusBadRequest)
		return
	}

	// Build remote URL: strip apiBasePath prefix, append to node's APIBaseURL.
	remoteURL := buildAgentStreamRemoteURL(node.APIBaseURL, r.URL.Path, apiV1BasePath)

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, remoteURL, nil)
	if err != nil {
		http.Error(w, "failed to create proxy request", http.StatusInternalServerError)
		return
	}
	req.Header.Set("Accept", "text/event-stream")
	node.ApplyAuth(req)

	// Forward the token query param for auth on the remote node.
	if token := r.URL.Query().Get("token"); token != "" {
		q := req.URL.Query()
		q.Set("token", token)
		req.URL.RawQuery = q.Encode()
	}

	client := &http.Client{
		// Timeout: 0 is safe for SSE because the request is created with
		// r.Context() which is cancelled when the client disconnects.
		Timeout: 0,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: node.SkipTLSVerify, //nolint:gosec
				MinVersion:         tls.VersionTLS12,
			},
			MaxIdleConns:       10,
			IdleConnTimeout:    90 * time.Second,
			DisableCompression: true,
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		if r.Context().Err() != nil {
			return // Client disconnected
		}
		http.Error(w, "failed to connect to remote node", http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		http.Error(w, fmt.Sprintf("remote node returned status: %d", resp.StatusCode), resp.StatusCode)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	// Set SSE headers and clear the write deadline to prevent the server's
	// WriteTimeout (60s) from killing this long-lived SSE connection.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{})

	// Stream chunks from remote to client.
	buf := make([]byte, 4096)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, writeErr := w.Write(buf[:n]); writeErr != nil {
				return // defer resp.Body.Close() handles cleanup
			}
			flusher.Flush()
		}
		if readErr != nil {
			return
		}
	}
}

// buildAgentStreamRemoteURL constructs the SSE stream URL for a remote node.
func buildAgentStreamRemoteURL(baseURL, requestPath, apiV1BasePath string) string {
	parts := strings.SplitN(requestPath, apiV1BasePath, 2)
	if len(parts) < 2 {
		return strings.TrimSuffix(baseURL, "/") + requestPath
	}
	return strings.TrimSuffix(baseURL, "/") + parts[1]
}

func (srv *Server) buildAgentAuthMiddleware(_ context.Context) func(http.Handler) http.Handler {
	authOptions := srv.buildStreamAuthOptions("Dagu Agent")

	return func(next http.Handler) http.Handler {
		return srv.injectDefaultStreamUserMiddleware()(auth.QueryTokenMiddleware()(
			auth.ClientIPMiddleware()(
				auth.Middleware(authOptions)(next),
			),
		))
	}
}

func (srv *Server) injectDefaultStreamUserMiddleware() func(http.Handler) http.Handler {
	if srv.config.Server.Auth.Mode != config.AuthModeNone {
		return func(next http.Handler) http.Handler {
			return next
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if user, ok := authmodel.UserFromContext(r.Context()); ok && user != nil {
				next.ServeHTTP(w, r)
				return
			}

			ctx := authmodel.WithUser(r.Context(), &authmodel.User{
				ID:       "admin",
				Username: "admin",
				Role:     authmodel.RoleAdmin,
			})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// buildStreamAuthOptions builds auth options for streaming endpoints (SSE, Agent SSE).
// In basic auth mode, auth is disabled because EventSource/WebSocket cannot send
// Basic auth headers. This matches the pre-existing behavior.
func (srv *Server) buildStreamAuthOptions(realm string) auth.Options {
	authCfg := srv.config.Server.Auth

	// When auth mode is "none", disable all authentication entirely.
	if authCfg.Mode == config.AuthModeNone {
		return auth.Options{Realm: realm}
	}

	// Basic auth mode: require credentials for SSE endpoints just like REST.
	// Browsers handle 401 + WWW-Authenticate: Basic challenges natively,
	// caching credentials per origin/realm, so EventSource requests will
	// include Basic auth automatically after the user authenticates once.
	if authCfg.Mode == config.AuthModeBasic {
		return auth.Options{
			Realm:                 realm,
			BasicAuthEnabled:      true,
			AuthRequired:          true,
			RequiredAPIKeySurface: authmodel.APIKeySurfaceREST,
			Creds:                 map[string]string{authCfg.Basic.Username: authCfg.Basic.Password},
		}
	}

	opts := auth.Options{
		Realm:                 realm,
		AuthRequired:          true,
		RequiredAPIKeySurface: authmodel.APIKeySurfaceREST,
	}

	if authCfg.Mode == config.AuthModeBuiltin && srv.authService != nil {
		opts.JWTValidator = srv.authService
		if srv.authService.HasAPIKeyStore() {
			opts.APIKeyValidator = srv.authService
		}
	}

	return opts
}

func (srv *Server) startServer(ctx context.Context) {
	tlsCfg := srv.config.Server.TLS
	hasListener := srv.listener != nil

	if tlsCfg != nil {
		logger.Info(ctx, "Starting TLS server",
			tag.Cert(tlsCfg.CertFile), slog.String("key", tlsCfg.KeyFile),
			slog.Bool("preBoundListener", hasListener))
	} else if hasListener {
		logger.Info(ctx, "Starting server on pre-bound listener")
	}

	err := srv.serveHTTP(tlsCfg, hasListener)
	if err != nil && err != http.ErrServerClosed {
		logger.Error(ctx, "Server failed to start or unexpected shutdown", tag.Error(err))
	}
}

func (srv *Server) serveHTTP(tlsCfg *config.TLSConfig, hasListener bool) error {
	switch {
	case hasListener && tlsCfg != nil:
		return srv.httpServer.ServeTLS(srv.listener, tlsCfg.CertFile, tlsCfg.KeyFile)
	case hasListener:
		return srv.httpServer.Serve(srv.listener)
	case tlsCfg != nil:
		return srv.httpServer.ListenAndServeTLS(tlsCfg.CertFile, tlsCfg.KeyFile)
	default:
		return srv.httpServer.ListenAndServe()
	}
}

// Shutdown gracefully shuts down the server.
func (srv *Server) Shutdown(ctx context.Context) error {
	shutdownCtx, cancel := newServerShutdownContext(ctx)
	defer cancel()

	return runShutdownSequence(shutdownCtx, srv.shutdownActions(shutdownCtx))
}

func newServerShutdownContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		return context.WithTimeout(context.Background(), serverShutdownTimeout)
	}
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, serverShutdownTimeout)
}

func newGracefulShutdownContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		return context.WithTimeout(context.Background(), serverShutdownTimeout)
	}
	return context.WithTimeout(context.WithoutCancel(ctx), serverShutdownTimeout)
}

func newShutdownPhaseContext(parent context.Context, budget time.Duration) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	if budget <= 0 {
		return context.WithCancel(parent)
	}
	if deadline, ok := parent.Deadline(); ok {
		candidate := time.Now().Add(budget)
		if candidate.Before(deadline) {
			return context.WithDeadline(parent, candidate)
		}
		return context.WithDeadline(parent, deadline)
	}
	return context.WithTimeout(parent, budget)
}

func (srv *Server) shutdownActions(ctx context.Context) shutdownActions {
	actions := shutdownActions{}

	if srv.syncService != nil {
		actions.stopSync = func() error {
			if err := srv.syncService.Stop(); err != nil {
				logger.Warn(ctx, "Failed to stop git sync service", tag.Error(err))
				return err
			}
			return nil
		}
	}
	if srv.appStream != nil && srv.sseMultiplexer == nil {
		actions.shutdownSSEMultiplexer = func() {
			srv.appStream.Shutdown()
			logger.Info(ctx, "App SSE stream shut down")
		}
	}
	if srv.sseMultiplexer != nil {
		actions.shutdownSSEMultiplexer = func() {
			if srv.appStream != nil {
				srv.appStream.Shutdown()
				logger.Info(ctx, "App SSE stream shut down")
			}
			srv.sseMultiplexer.Shutdown()
			logger.Info(ctx, "SSE multiplexer shut down")
		}
	}
	if srv.httpServer != nil {
		actions.beforeHTTPShutdown = func() {
			logger.Info(ctx, "Server is shutting down", tag.Addr(srv.httpServer.Addr))
		}
		actions.disableHTTPKeepAlives = func() {
			srv.httpServer.SetKeepAlivesEnabled(false)
		}
		actions.shutdownHTTP = func(shutdownCtx context.Context) error {
			return srv.httpServer.Shutdown(shutdownCtx)
		}
	}
	if srv.terminalManager != nil {
		actions.shutdownTerminal = func(shutdownCtx context.Context) error {
			if err := srv.terminalManager.Shutdown(shutdownCtx); err != nil {
				logger.Warn(ctx, "Terminal manager did not shut down cleanly", tag.Error(err))
				return err
			}
			logger.Info(ctx, "Terminal manager shut down")
			return nil
		}
	}
	if srv.auditStore != nil {
		actions.closeAudit = func() error {
			if err := srv.auditStore.Close(); err != nil {
				logger.Warn(ctx, "Failed to close audit store", tag.Error(err))
				return err
			}
			return nil
		}
	}

	return actions
}

func runShutdownSequence(shutdownCtx context.Context, actions shutdownActions) error {
	var shutdownErr error

	if actions.stopSync != nil {
		_ = actions.stopSync()
	}
	if actions.shutdownSSEMultiplexer != nil {
		actions.shutdownSSEMultiplexer()
	}
	if actions.shutdownHTTP != nil {
		if actions.beforeHTTPShutdown != nil {
			actions.beforeHTTPShutdown()
		}
		if actions.disableHTTPKeepAlives != nil {
			actions.disableHTTPKeepAlives()
		}
		httpCtx, cancelHTTP := newShutdownPhaseContext(shutdownCtx, httpShutdownBudget)
		if err := actions.shutdownHTTP(httpCtx); err != nil {
			shutdownErr = errors.Join(shutdownErr, err)
		}
		cancelHTTP()
	}
	if actions.shutdownTerminal != nil {
		terminalCtx, cancelTerminal := newShutdownPhaseContext(shutdownCtx, 0)
		if err := actions.shutdownTerminal(terminalCtx); err != nil {
			shutdownErr = errors.Join(shutdownErr, err)
		}
		cancelTerminal()
	}
	if actions.closeAudit != nil {
		_ = actions.closeAudit()
	}

	return shutdownErr
}

func (srv *Server) setupGracefulShutdown(ctx context.Context) {
	if signalctx.OSSignalsDisabled(ctx) {
		<-ctx.Done()
		logger.Info(ctx, "Context done, shutting down server")
	} else {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
		defer signal.Stop(quit)

		select {
		case <-ctx.Done():
			logger.Info(ctx, "Context done, shutting down server")
		case sig := <-quit:
			logger.Info(ctx, "Received shutdown signal", slog.String("signal", sig.String()))
		}
	}

	shutdownCtx, cancel := newGracefulShutdownContext(ctx)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error(ctx, "Failed to shutdown server gracefully", tag.Error(err))
	}
}
