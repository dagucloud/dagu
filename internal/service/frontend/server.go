// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package frontend

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
	"unicode"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/httplog/v2"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/dagucloud/dagu/v2/internal/audit"
	authmodel "github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/cmn/backoff"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	cmnschema "github.com/dagucloud/dagu/v2/internal/cmn/schema"
	"github.com/dagucloud/dagu/v2/internal/cmn/signalctx"
	cmnvalue "github.com/dagucloud/dagu/v2/internal/cmn/value"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/eventstore"
	"github.com/dagucloud/dagu/v2/internal/gitsync"
	"github.com/dagucloud/dagu/v2/internal/license"
	_ "github.com/dagucloud/dagu/v2/internal/llm/allproviders" // Register LLM providers
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/queue"
	"github.com/dagucloud/dagu/v2/internal/remotenode"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/schedulerstate"
	authservice "github.com/dagucloud/dagu/v2/internal/service/auth"
	"github.com/dagucloud/dagu/v2/internal/service/authmapping"
	"github.com/dagucloud/dagu/v2/internal/service/chatbridge"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	"github.com/dagucloud/dagu/v2/internal/service/frontend/api/pathutil"
	apiv1 "github.com/dagucloud/dagu/v2/internal/service/frontend/api/v1"
	"github.com/dagucloud/dagu/v2/internal/service/frontend/auth"
	"github.com/dagucloud/dagu/v2/internal/service/frontend/metrics"
	"github.com/dagucloud/dagu/v2/internal/service/frontend/sse"
	"github.com/dagucloud/dagu/v2/internal/service/frontend/terminal"
	incidentservice "github.com/dagucloud/dagu/v2/internal/service/incident"
	dagumcp "github.com/dagucloud/dagu/v2/internal/service/mcp"
	notificationservice "github.com/dagucloud/dagu/v2/internal/service/notification"
	"github.com/dagucloud/dagu/v2/internal/service/oidcprovision"
	"github.com/dagucloud/dagu/v2/internal/service/resource"
	"github.com/dagucloud/dagu/v2/internal/service/trustedproxyprovision"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
	"github.com/dagucloud/dagu/v2/internal/telemetry"
	"github.com/dagucloud/dagu/v2/internal/tunnel"
	"github.com/dagucloud/dagu/v2/internal/upgrade"
	workspacepkg "github.com/dagucloud/dagu/v2/internal/workspace"
)

const (
	serverShutdownTimeout = 10 * time.Second
	httpShutdownBudget    = 5 * time.Second
	httpWriteTimeout      = 60 * time.Second
)

var wikiSSETopicTypes = [...]sse.TopicType{
	sse.TopicTypeWikiPage,
	sse.TopicTypeWikiTree,
	sse.TopicTypeLegacyDoc,
	sse.TopicTypeLegacyDocTree,
}

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
	apiV1                *apiv1.API
	config               *config.Config
	httpServer           *http.Server
	funcsConfig          funcsConfig
	builtinOIDCCfg       *auth.BuiltinOIDCConfig
	trustedProxyCfg      *auth.TrustedProxyLoginConfig
	authService          *authservice.Service
	auditService         *audit.Service
	auditStore           io.Closer
	eventService         *eventstore.Service
	incidentService      *incidentservice.Service
	notificationService  *notificationservice.Service
	incidentState        chatbridge.StateStore
	newIncidentLease     func() chatbridge.Lease
	notificationState    chatbridge.StateStore
	newNotificationLease func() chatbridge.Lease
	syncService          gitsync.Service
	listener             net.Listener
	appStream            *sse.AppStreamService
	sseMultiplexer       *sse.Multiplexer
	terminalManager      *terminal.Manager
	metricsRegistry      *prometheus.Registry
	tunnelAPIOpts        []apiv1.APIOption
	tunnelService        *tunnel.Service
	dagRepository        *persis.DAGRepository
	licenseManager       *license.Manager
	remoteNodeResolver   *remotenode.Resolver
	upgradeStore         upgrade.CacheStore
	routeRegistrars      []RouteRegistrar
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

func toOIDCWorkspaceMappings(mappings map[string][]config.OIDCWorkspaceGrant) map[string][]oidcprovision.WorkspaceGrantConfig {
	if len(mappings) == 0 {
		return nil
	}
	result := make(map[string][]oidcprovision.WorkspaceGrantConfig, len(mappings))
	for group, grants := range mappings {
		converted := make([]oidcprovision.WorkspaceGrantConfig, len(grants))
		for i, grant := range grants {
			converted[i] = oidcprovision.WorkspaceGrantConfig{
				Workspace: grant.Workspace,
				Role:      grant.Role,
			}
		}
		result[group] = converted
	}
	return result
}

func toOIDCPolicy(policy config.OIDCPolicy) oidcprovision.Policy {
	return oidcprovision.Policy{
		AutoSignup:     policy.AutoSignup,
		AllowedDomains: policy.AllowedDomains,
		Whitelist:      policy.Whitelist,
		RoleMapping: oidcprovision.RoleMapperConfig{
			GroupsClaim:            policy.RoleMapping.GroupsClaim,
			GroupMappings:          policy.RoleMapping.GroupMappings,
			WorkspaceMappings:      toOIDCWorkspaceMappings(policy.RoleMapping.WorkspaceMappings),
			DefaultWorkspaceAccess: policy.RoleMapping.DefaultWorkspaceAccess,
			RoleAttributePath:      policy.RoleMapping.RoleAttributePath,
			RoleAttributeStrict:    policy.RoleMapping.RoleAttributeStrict,
			SkipOrgRoleSync:        policy.RoleMapping.SkipOrgRoleSync,
			DefaultRole:            authmodel.Role(policy.RoleMapping.DefaultRole),
		},
	}
}

func toConfigOIDCWorkspaceMappings(mappings map[string][]oidcprovision.WorkspaceGrantConfig) map[string][]config.OIDCWorkspaceGrant {
	if len(mappings) == 0 {
		return nil
	}
	result := make(map[string][]config.OIDCWorkspaceGrant, len(mappings))
	for group, grants := range mappings {
		converted := make([]config.OIDCWorkspaceGrant, len(grants))
		for i, grant := range grants {
			converted[i] = config.OIDCWorkspaceGrant{
				Workspace: grant.Workspace,
				Role:      grant.Role,
			}
		}
		result[group] = converted
	}
	return result
}

func toConfigOIDCMapping(mapping oidcprovision.RoleMapperConfig) config.OIDCRoleMapping {
	return config.OIDCRoleMapping{
		GroupsClaim:            mapping.GroupsClaim,
		GroupMappings:          mapping.GroupMappings,
		WorkspaceMappings:      toConfigOIDCWorkspaceMappings(mapping.WorkspaceMappings),
		DefaultWorkspaceAccess: mapping.DefaultWorkspaceAccess,
		RoleAttributePath:      mapping.RoleAttributePath,
		RoleAttributeStrict:    mapping.RoleAttributeStrict,
		SkipOrgRoleSync:        mapping.SkipOrgRoleSync,
		DefaultRole:            string(mapping.DefaultRole),
	}
}

func toTrustedProxyGroupMappings(mappings map[string]string) map[string]authmodel.Role {
	if len(mappings) == 0 {
		return nil
	}
	result := make(map[string]authmodel.Role, len(mappings))
	for group, role := range mappings {
		result[group] = authmodel.Role(role)
	}
	return result
}

func toTrustedProxyWorkspaceMappings(mappings map[string][]config.TrustedProxyWorkspaceGrant) map[string][]authmapping.WorkspaceGrantConfig {
	if len(mappings) == 0 {
		return nil
	}
	result := make(map[string][]authmapping.WorkspaceGrantConfig, len(mappings))
	for group, grants := range mappings {
		converted := make([]authmapping.WorkspaceGrantConfig, len(grants))
		for i, grant := range grants {
			converted[i] = authmapping.WorkspaceGrantConfig{
				Workspace: grant.Workspace,
				Role:      authmodel.Role(grant.Role),
			}
		}
		result[group] = converted
	}
	return result
}

// RegisterRoutes appends a route registrar that is applied before API routes
// are mounted.
func (srv *Server) RegisterRoutes(fn RouteRegistrar) {
	if fn != nil {
		srv.routeRegistrars = append(srv.routeRegistrars, fn)
	}
}

// ServerConfig contains the dependencies used by the frontend server.
type ServerConfig struct {
	Context              context.Context
	Config               *config.Config
	DAGRepository        *persis.DAGRepository
	DAGRunRepository     *persis.DAGRunRepository
	ProcRepository       *persis.ProcRepository
	QueueStore           queue.QueueStore
	DAGRunManager        runtime.Manager
	CoordinatorClient    coordinator.Client
	ServiceRegistry      serviceregistry.ServiceRegistry
	DAGRunLeaseStore     dispatch.DAGRunLeaseStore
	WorkerHeartbeatStore dispatch.WorkerHeartbeatStore
	SchedulerStateStore  schedulerstate.Store
	Caches               []fileutil.CacheMetrics
	LicenseManager       *license.Manager
	ResourceService      *resource.Service
	Stores               Stores
}

// NewServer constructs a Server from the provided configuration, stores, and services.
// Returns an error if initialization fails (e.g., when builtin auth fails to initialize).
func NewServer(setup ServerConfig, opts ...ServerOption) (*Server, error) {
	ctx := setup.Context
	if ctx == nil {
		ctx = context.Background()
	}
	cfg := setup.Config
	dr := setup.DAGRepository
	dagRunRepository := setup.DAGRunRepository
	qs := setup.QueueStore
	processes := setup.ProcRepository
	drm := setup.DAGRunManager
	cc := setup.CoordinatorClient
	sr := setup.ServiceRegistry
	rs := setup.ResourceService
	stores := setup.Stores

	collector := telemetry.NewCollector(config.Version, dr, dagRunRepository, qs, sr)
	collector.SetWorkerHeartbeatStore(setup.WorkerHeartbeatStore)
	for _, cache := range setup.Caches {
		collector.RegisterCache(cache)
	}
	mr := telemetry.NewRegistry(collector)
	if setup.LicenseManager != nil {
		opts = append(opts, WithLicenseManager(setup.LicenseManager))
	}
	if setup.DAGRunLeaseStore != nil {
		opts = append(opts, WithAPIOption(apiv1.WithDAGRunLeaseStore(setup.DAGRunLeaseStore)))
	}
	if setup.WorkerHeartbeatStore != nil {
		opts = append(opts, WithAPIOption(apiv1.WithWorkerHeartbeatStore(setup.WorkerHeartbeatStore)))
	}
	opts = append(opts, WithAPIOption(apiv1.WithSchedulerStateStore(setup.SchedulerStateStore)))

	remoteNodes := make([]string, 0, len(cfg.Server.RemoteNodes))
	for _, n := range cfg.Server.RemoteNodes {
		remoteNodes = append(remoteNodes, n.Name)
	}
	evaluatedBasePath, err := evaluateConfiguredBasePath(ctx, cfg.Server.BasePath)
	if err != nil {
		return nil, err
	}

	var (
		apiOpts         []apiv1.APIOption
		builtinOIDCCfg  *auth.BuiltinOIDCConfig
		trustedProxyCfg = &auth.TrustedProxyLoginConfig{LoginBasePath: evaluatedBasePath}
		oidcEnabled     bool
		oidcButtonLabel string
		setupRequired   bool
	)
	if stores.WorkspaceBaseConfig != nil {
		apiOpts = append(apiOpts, apiv1.WithWorkspaceBaseConfigProvider(stores.WorkspaceBaseConfig))
	}

	var auditSvc *audit.Service
	if stores.Audit != nil {
		auditSvc = audit.New(stores.Audit)
	}
	var auditStore io.Closer
	if closer, ok := stores.Audit.(io.Closer); ok {
		auditStore = closer
	}
	eventSvc := stores.Event
	syncSvc := initSyncService(ctx, cfg)
	if syncSvc != nil {
		apiOpts = append(apiOpts, apiv1.WithSyncService(syncSvc))
	}

	if stores.BaseConfig != nil {
		apiOpts = append(apiOpts, apiv1.WithBaseConfigStore(stores.BaseConfig))
	}

	// Initialize the workspace store before OIDC provisioning so login-time
	// mapping can report dormant grants without making workspace existence a
	// prerequisite for authentication.
	wsStore := stores.Workspace
	var workspaceExists func(context.Context, string) (bool, error)
	if wsStore != nil {
		apiOpts = append(apiOpts, apiv1.WithWorkspaceStore(wsStore))
		workspaceExists = func(ctx context.Context, name string) (bool, error) {
			_, err := wsStore.GetByName(ctx, name)
			switch {
			case err == nil:
				return true, nil
			case errors.Is(err, workspacepkg.ErrWorkspaceNotFound):
				return false, nil
			default:
				return false, err
			}
		}
	}

	var authSvc *authservice.Service
	if cfg.Server.Auth.Mode == config.AuthModeBuiltin {
		if stores.AuthService == nil || stores.UserStore == nil {
			return nil, errors.New("builtin auth persistence is not configured")
		}
		authSvc = stores.AuthService
		setupRequired = stores.AuthSetupRequired
		apiOpts = append(apiOpts, apiv1.WithAuthService(stores.AuthService))

		trustedProxy := cfg.Server.Auth.Proxy
		if trustedProxy.Enabled {
			trustedProvisionSvc, err := trustedproxyprovision.New(stores.UserStore, trustedproxyprovision.Config{
				UsersDir:        cfg.Paths.UsersDir,
				Source:          trustedProxy.Source,
				AutoSignup:      trustedProxy.AutoSignup,
				SkipOrgRoleSync: trustedProxy.RoleMapping.SkipOrgRoleSync,
				WorkspaceExists: workspaceExists,
				RoleMapping: authmapping.Config{
					DefaultRole:            authmodel.Role(trustedProxy.RoleMapping.DefaultRole),
					GroupMappings:          toTrustedProxyGroupMappings(trustedProxy.RoleMapping.GroupMappings),
					WorkspaceMappings:      toTrustedProxyWorkspaceMappings(trustedProxy.RoleMapping.WorkspaceMappings),
					DefaultWorkspaceAccess: trustedProxy.RoleMapping.DefaultWorkspaceAccess,
					Strict:                 trustedProxy.RoleMapping.RequireMapping,
				},
			})
			if err != nil {
				return nil, fmt.Errorf("failed to create proxy authentication provisioning service: %w", err)
			}
			trustedProxyCfg = &auth.TrustedProxyLoginConfig{
				Enabled:        true,
				UserHeader:     trustedProxy.Headers.User,
				GroupsHeader:   trustedProxy.Headers.Groups,
				GroupsRequired: len(trustedProxy.RoleMapping.GroupMappings) > 0 || len(trustedProxy.RoleMapping.WorkspaceMappings) > 0,
				Provision:      trustedProvisionSvc,
				AuthService:    stores.AuthService,
				InitialSetupComplete: func(ctx context.Context) (bool, error) {
					count, err := stores.AuthService.CountUsers(ctx)
					return count > 0, err
				},
				LoginBasePath: evaluatedBasePath,
			}
			logger.Info(ctx, "Proxy authentication enabled",
				slog.Bool("autoSignup", trustedProxy.AutoSignup),
				slog.String("defaultRole", trustedProxy.RoleMapping.DefaultRole),
				slog.Bool("skipOrgRoleSync", trustedProxy.RoleMapping.SkipOrgRoleSync))
		}

		oidcCfg := cfg.Server.Auth.OIDC
		if oidcCfg.IsConfigured() {
			oidcEnabled = true
			oidcButtonLabel = oidcCfg.ButtonLabel
			configPolicy := oidcCfg.Policy()
			policy := toOIDCPolicy(configPolicy)
			loader := config.NewOIDCPolicyLoader(
				cfg.Paths.ConfigFilesUsed,
				configPolicy,
			)

			provisionCfg := oidcprovision.Config{
				Issuer:         oidcCfg.Issuer,
				AutoSignup:     policy.AutoSignup,
				DefaultRole:    policy.RoleMapping.DefaultRole,
				AllowedDomains: policy.AllowedDomains,
				Whitelist:      policy.Whitelist,
				RoleMapping:    policy.RoleMapping,
				LoadPolicy: func(context.Context) (oidcprovision.Policy, error) {
					policy, err := loader.Load()
					if err != nil {
						return oidcprovision.Policy{}, err
					}
					return toOIDCPolicy(policy), nil
				},
			}
			provisionCfg.WorkspaceExists = workspaceExists
			provisionSvc, err := oidcprovision.New(stores.UserStore, provisionCfg)
			if err != nil {
				return nil, fmt.Errorf("failed to create OIDC provisioning service: %w", err)
			}
			apiOpts = append(apiOpts, apiv1.WithOIDCRoleMapping(
				func() config.OIDCRoleMapping {
					return toConfigOIDCMapping(provisionSvc.RoleMapping())
				},
			))

			builtinOIDCCfg, err = auth.InitBuiltinOIDCConfig(
				ctx,
				oidcCfg,
				stores.AuthService,
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

	var (
		remoteNodeResolver *remotenode.Resolver
		licenseChecker     license.Checker
	)
	if stores.RemoteNode != nil {
		remoteNodeResolver = remotenode.NewResolver(cfg.Server.RemoteNodes, stores.RemoteNode)
		apiOpts = append(apiOpts,
			apiv1.WithRemoteNodeResolver(remoteNodeResolver),
			apiv1.WithRemoteNodeStore(stores.RemoteNode),
		)
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

	if stores.Secret != nil {
		apiOpts = append(apiOpts, apiv1.WithSecretStore(stores.Secret))
	}
	if stores.Profile != nil {
		apiOpts = append(apiOpts, apiv1.WithProfileStore(stores.Profile))
	}

	if stores.DAGSettings != nil {
		apiOpts = append(apiOpts, apiv1.WithDAGSettingsStore(stores.DAGSettings))
	}

	if stores.Wiki != nil {
		apiOpts = append(apiOpts, apiv1.WithWikiStore(stores.Wiki))
	}

	if stores.View != nil {
		apiOpts = append(apiOpts, apiv1.WithViewStore(stores.View))
	}

	var notificationSvc *notificationservice.Service
	if stores.Notification != nil && eventSvc != nil && stores.NotificationState != nil {
		notificationSvc = notificationservice.New(
			stores.Notification,
			dr,
			notificationservice.WithPublicURL(cfg.Server.PublicURL),
		)
		apiOpts = append(apiOpts, apiv1.WithNotificationService(notificationSvc))
	} else if stores.Notification != nil {
		slog.Default().Warn("Notification delivery is unavailable because the event or state store is disabled")
	}

	var incidentSvc *incidentservice.Service
	if stores.Incident != nil {
		incidentSvc = incidentservice.New(
			stores.Incident,
			incidentservice.WithIncidentsEnabled(func() bool {
				return license.HasActiveLicense(licenseChecker)
			}),
			incidentservice.WithPublicURL(cfg.Server.PublicURL),
		)
		apiOpts = append(apiOpts, apiv1.WithIncidentService(incidentSvc))
	}

	upgradeStore := stores.Upgrade
	var updateInfoChecker UpdateChecker
	if upgradeStore != nil {
		updateInfoChecker = &updateChecker{store: upgradeStore}
	}

	// Note: SSO/OIDC gating is applied after opts are processed (see below)

	srv := &Server{
		config:               cfg,
		builtinOIDCCfg:       builtinOIDCCfg,
		trustedProxyCfg:      trustedProxyCfg,
		authService:          authSvc,
		auditService:         auditSvc,
		auditStore:           auditStore,
		eventService:         eventSvc,
		incidentService:      incidentSvc,
		notificationService:  notificationSvc,
		incidentState:        stores.IncidentState,
		newIncidentLease:     stores.NewIncidentLease,
		notificationState:    stores.NotificationState,
		newNotificationLease: stores.NewNotificationLease,
		syncService:          syncSvc,
		metricsRegistry:      mr,
		dagRepository:        dr,
		remoteNodeResolver:   remoteNodeResolver,
		upgradeStore:         upgradeStore,
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
			ProxyEnabled:          cfg.Server.Auth.Proxy.Enabled,
			ProxyButtonLabel:      cfg.Server.Auth.Proxy.ButtonLabel,
			TerminalEnabled:       cfg.Server.Terminal.Enabled && authSvc != nil,
			GitSyncEnabled:        cfg.GitSync.Enabled,
			WorkspaceStore:        wsStore,
			SetupRequiredChecker:  &setupChecker{authSvc: authSvc, fallback: setupRequired},
			UpdateChecker:         updateInfoChecker,
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

	// Populate license checker and manager in funcsConfig after opts
	if srv.licenseManager != nil {
		licenseChecker = srv.licenseManager.Checker()
		srv.funcsConfig.LicenseChecker = licenseChecker
		srv.funcsConfig.LicenseManager = srv.licenseManager
		if srv.builtinOIDCCfg != nil {
			srv.builtinOIDCCfg.LicenseChecker = licenseChecker
		}
		if srv.trustedProxyCfg != nil {
			srv.trustedProxyCfg.LicenseChecker = licenseChecker
		}
	}

	if srv.licenseManager != nil && srv.builtinOIDCCfg != nil && !srv.licenseManager.Checker().IsFeatureEnabled(license.FeatureSSO) {
		logger.Warn(ctx, "SSO (OIDC) is configured but currently unavailable because the active license does not enable it")
	}
	if srv.licenseManager != nil && srv.trustedProxyCfg != nil && srv.trustedProxyCfg.Enabled && !srv.licenseManager.Checker().IsFeatureEnabled(license.FeatureSSO) {
		logger.Warn(ctx, "Proxy authentication is configured but currently unavailable because the active license does not enable it")
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
	apiOpts = append(apiOpts, apiv1.WithWikiMutationNotifier(func() {
		if srv.sseMultiplexer == nil {
			return
		}
		srv.wakeWikiTopics()
	}))
	// Pass license manager to API
	if srv.licenseManager != nil {
		apiOpts = append(apiOpts, apiv1.WithLicenseManager(srv.licenseManager))
	}

	allAPIOptions := append(apiOpts, srv.tunnelAPIOpts...)

	srv.apiV1 = apiv1.New(dr, dagRunRepository, qs, processes, drm, cfg, cc, sr, mr, rs, allAPIOptions...)

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

// initSyncService creates and returns a Git sync service if enabled.
func initSyncService(ctx context.Context, cfg *config.Config) gitsync.Service {
	if !cfg.GitSync.Enabled {
		return nil
	}

	syncCfg := gitsync.NewConfigFromGlobal(cfg.GitSync)
	svc := gitsync.NewService(syncCfg, cfg.Paths.DAGsDir, cfg.Paths.WikiDir, cfg.Paths.DataDir)

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
	ipAccessPolicy, err := newIPAccessPolicy(srv.config.Server.IPAccess)
	if err != nil {
		return fmt.Errorf("configure IP access: %w", err)
	}
	r.Use(auth.PreserveRawRemoteAddr)
	r.Use(middleware.Compress(5))
	if srv.config.Server.AccessLog != config.AccessLogNone {
		logLevel := slog.LevelInfo
		if srv.config.Core.Debug {
			logLevel = slog.LevelDebug
		}
		requestLogger := httplog.NewLogger("http", httplog.Options{
			LogLevel:       logLevel,
			JSON:           srv.config.Core.LogFormat == "json",
			Concise:        true,
			RequestHeaders: srv.config.Core.Debug,
			HideRequestHeaders: trustedProxyRequestHeaders(
				srv.config.Server.Auth.Proxy,
			),
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
	r.Use(ipAccessPolicy.middleware)
	r.Use(corsPolicy{
		allowedOrigins: srv.config.Server.CORSAllowedOrigins,
		publicURL:      srv.config.Server.PublicURL,
		setupPath:      path.Join(apiV1BasePath, "auth/setup"),
	}.middleware)
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
		WriteTimeout:      httpWriteTimeout,
	}

	if err := srv.startMonitors(ctx); err != nil {
		return err
	}

	metrics.StartUptime(ctx)
	logger.Info(ctx, "Server is starting", tag.Addr(addr))
	srv.startPeriodicUpdateCheck(ctx)

	go srv.startServer(ctx)
	srv.setupGracefulShutdown(ctx)

	return nil
}

func trustedProxyRequestHeaders(cfg config.AuthTrustedProxy) []string {
	if !cfg.Enabled {
		return nil
	}
	headers := make([]string, 0, 2)
	if cfg.Headers.User != "" {
		headers = append(headers, cfg.Headers.User)
	}
	if cfg.Headers.Groups != "" {
		headers = append(headers, cfg.Headers.Groups)
	}
	return headers
}

func (srv *Server) startMonitors(ctx context.Context) error {
	notificationMonitor := srv.newNotificationMonitor()
	incidentMonitor := srv.newIncidentMonitor()

	if notificationMonitor != nil {
		if err := notificationMonitor.Bootstrap(ctx); err != nil {
			return fmt.Errorf("bootstrap notification monitor: %w", err)
		}
	}

	if incidentMonitor != nil {
		if err := incidentMonitor.Bootstrap(ctx); err != nil {
			return fmt.Errorf("bootstrap incident monitor: %w", err)
		}
	}

	if notificationMonitor != nil {
		go notificationMonitor.Run(ctx)
	}

	if incidentMonitor != nil {
		go incidentMonitor.Run(ctx)
	}
	return nil
}

func (srv *Server) newNotificationMonitor() *chatbridge.NotificationMonitor {
	if srv.notificationService == nil || srv.eventService == nil {
		return nil
	}
	if srv.notificationState == nil {
		return nil
	}
	var lease chatbridge.Lease
	if srv.newNotificationLease != nil {
		lease = srv.newNotificationLease()
	}
	return chatbridge.NewNotificationMonitor(
		srv.eventService,
		srv.notificationState,
		lease,
		srv.notificationService,
		slog.Default(),
		chatbridge.DefaultNotificationMonitorConfig(),
	)
}

func (srv *Server) newIncidentMonitor() *chatbridge.NotificationMonitor {
	if srv.incidentService == nil || srv.eventService == nil {
		return nil
	}
	if srv.incidentState == nil {
		return nil
	}
	var lease chatbridge.Lease
	if srv.newIncidentLease != nil {
		lease = srv.newIncidentLease()
	}
	return chatbridge.NewNotificationMonitor(
		srv.eventService,
		srv.incidentState,
		lease,
		srv.incidentService,
		slog.Default(),
		incidentMonitorConfig(),
	)
}

func incidentMonitorConfig() chatbridge.NotificationMonitorConfig {
	cfg := chatbridge.DefaultNotificationMonitorConfig()
	cfg.UrgentWindow = time.Second
	cfg.SuccessWindow = time.Second
	cfg.InterestedEventTypes = []eventstore.EventType{
		eventstore.TypeDAGRunFailed,
		eventstore.TypeDAGRunSucceeded,
		eventstore.TypeDAGRunPartiallySucceeded,
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
	basePath := srv.funcsConfig.BasePath
	srv.setupTrustedProxyRoute(r, basePath)
	if srv.config.Server.Headless {
		logger.Info(ctx, "Headless mode enabled: UI is disabled, but API remains active")
		return nil
	}

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

func evaluateConfiguredBasePath(ctx context.Context, basePath string) (string, error) {
	resolver := cmnvalue.NewResolver(cmnvalue.StaticScope{}, cmnvalue.RuntimeScope{})
	evaluated, err := resolver.String(ctx, basePath, cmnvalue.ServerBasePathField("server.base_path"))
	if err != nil {
		return "", fmt.Errorf("evaluate server base path: %w", err)
	}
	if strings.ContainsAny(evaluated, "?#\\") || strings.IndexFunc(evaluated, unicode.IsControl) >= 0 {
		return "", fmt.Errorf("evaluate server base path: result must be a local URL path")
	}

	cleaned := path.Clean("/" + strings.TrimLeft(strings.TrimSpace(evaluated), "/"))
	if cleaned == "/" {
		return "", nil
	}
	return cleaned, nil
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
	srv.setupAssetRoutesWithFS(r, basePath, assetsFS)
}

func (srv *Server) setupAssetRoutesWithFS(r *chi.Mux, basePath string, assetFS fs.FS) {
	assetsPath := ensureLeadingSlash(path.Join(strings.TrimRight(basePath, "/"), "assets/*"))

	fileServer := http.FileServer(http.FS(assetFS))
	if basePath != "" && basePath != "/" {
		fileServer = http.StripPrefix(strings.TrimRight(basePath, "/"), fileServer)
	}

	r.Get(assetsPath, func(w http.ResponseWriter, r *http.Request) {
		isCurrentVersion := r.URL.Query().Get("v") == currentAssetVersion()
		w.Header().Set("Cache-Control", cacheControlForAsset(r.URL.Path, isCurrentVersion))

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

func cacheControlForAsset(assetPath string, isCurrentVersion bool) string {
	base := path.Base(assetPath)
	lowerBase := strings.ToLower(base)
	if lowerBase == "bundle.js" && isCurrentVersion {
		return "max-age=31536000, immutable"
	}
	if hasContentHashSuffix(lowerBase, ".worker.js") {
		return "max-age=31536000, immutable"
	}
	if strings.HasSuffix(lowerBase, ".bundle.js") && lowerBase != "bundle.js" {
		return "max-age=31536000, immutable"
	}
	if strings.HasSuffix(lowerBase, ".js") {
		return "no-cache, no-store, must-revalidate"
	}
	return "max-age=86400"
}

func hasContentHashSuffix(base, suffix string) bool {
	if !strings.HasSuffix(base, suffix) {
		return false
	}
	stem := strings.TrimSuffix(base, suffix)
	hashStart := strings.LastIndex(stem, ".")
	if hashStart < 0 {
		return false
	}
	hash := stem[hashStart+1:]
	if len(hash) != 16 {
		return false
	}
	for _, char := range hash {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func (srv *Server) setupTrustedProxyRoute(r *chi.Mux, basePath string) {
	r.Handle(
		pathutil.BuildPublicEndpointPath(basePath, "proxy-login"),
		auth.TrustedProxyLoginHandler(srv.trustedProxyCfg),
	)
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
		if err := srv.apiV1.ConfigureRoutes(ctx, r, httpWriteTimeout); err != nil {
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
	appStream, err := sse.NewAppStreamService(sse.AppStreamConfig{
		Paths: srv.config.Paths,
	})
	if err != nil {
		logger.Warn(ctx, "Failed to start SSE invalidation stream", tag.Error(err))
	} else {
		srv.appStream = appStream
	}

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
	srv.startAppStreamInvalidationBridge(ctx)
	if srv.eventService != nil {
		sse.StartDAGRunEventInvalidation(srv.sseMultiplexer.Context(), srv.eventService, srv.sseMultiplexer, slog.Default(), time.Second)
	}

	multiplexHandler := sse.NewMultiplexHandler(srv.sseMultiplexer, srv.remoteNodeResolver)

	authOpts := srv.buildStreamAuthOptions("restricted")

	r.Route(path.Join(apiV1BasePath, "events"), func(r chi.Router) {
		r.Use(auth.QueryTokenMiddleware())
		r.Use(auth.ClientIPMiddleware())
		r.Use(auth.Middleware(authOpts))
		r.Use(srv.injectDefaultStreamUserMiddleware())

		r.Get("/stream", multiplexHandler.HandleStream)
		r.Post("/stream/topics", multiplexHandler.HandleTopicMutation)
	})

	logger.Info(ctx, "SSE routes configured", slog.String("basePath", apiV1BasePath))
}

func (srv *Server) startAppStreamInvalidationBridge(ctx context.Context) {
	if srv.appStream == nil || srv.sseMultiplexer == nil {
		return
	}

	bridgeCtx := srv.sseMultiplexer.Context()
	events, unsubscribe := srv.appStream.Subscribe(bridgeCtx)
	go func() {
		defer unsubscribe()
		for {
			select {
			case <-bridgeCtx.Done():
				return
			case event, ok := <-events:
				if !ok {
					return
				}
				srv.wakeMultiplexedTopicsForAppEvent(event)
			}
		}
	}()
	logger.Info(ctx, "SSE invalidation stream configured for multiplexed topics")
}

func (srv *Server) wakeMultiplexedTopicsForAppEvent(event sse.AppEvent) {
	if srv.sseMultiplexer == nil {
		return
	}

	switch event.Type {
	case sse.AppEventTypeDAGChanged:
		srv.sseMultiplexer.WakeTopicType(sse.TopicTypeDAGsList)
		srv.sseMultiplexer.WakeTopicType(sse.TopicTypeDAG)
		srv.sseMultiplexer.WakeTopicType(sse.TopicTypeDAGHistory)
	case sse.AppEventTypeScheduler:
		srv.sseMultiplexer.WakeTopicType(sse.TopicTypeDAGsList)
	case sse.AppEventTypeQueue:
		srv.sseMultiplexer.WakeTopicType(sse.TopicTypeQueues)
		if event.QueueName != "" {
			srv.sseMultiplexer.WakeTopic(sse.TopicTypeQueueItems, event.QueueName)
		} else {
			srv.sseMultiplexer.WakeTopicType(sse.TopicTypeQueueItems)
		}
	case sse.AppEventTypeWiki:
		srv.wakeWikiTopics()
	case sse.AppEventTypeReset:
		srv.wakeAllMultiplexedFileBackedTopics()
	}
}

func (srv *Server) wakeAllMultiplexedFileBackedTopics() {
	srv.sseMultiplexer.WakeTopicType(sse.TopicTypeDAGRun)
	srv.sseMultiplexer.WakeTopicType(sse.TopicTypeSubDAGRun)
	srv.sseMultiplexer.WakeTopicType(sse.TopicTypeDAG)
	srv.sseMultiplexer.WakeTopicType(sse.TopicTypeDAGHistory)
	srv.sseMultiplexer.WakeTopicType(sse.TopicTypeDAGRuns)
	srv.sseMultiplexer.WakeTopicType(sse.TopicTypeQueueItems)
	srv.sseMultiplexer.WakeTopicType(sse.TopicTypeQueues)
	srv.sseMultiplexer.WakeTopicType(sse.TopicTypeDAGsList)
	srv.wakeWikiTopics()
}

func (srv *Server) wakeWikiTopics() {
	for _, topicType := range wikiSSETopicTypes {
		srv.sseMultiplexer.WakeTopicType(topicType)
	}
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
	for _, topicType := range []sse.TopicType{sse.TopicTypeWikiPage, sse.TopicTypeLegacyDoc} {
		registrar.RegisterFetcher(topicType, srv.apiV1.GetWikiPageContentData)
	}
	for _, topicType := range []sse.TopicType{sse.TopicTypeWikiTree, sse.TopicTypeLegacyDocTree} {
		registrar.RegisterFetcher(topicType, srv.apiV1.GetWikiPageTreeData)
	}

	appStreamAvailable := srv.appStream != nil
	if appStreamAvailable {
		for _, topicType := range wikiSSETopicTypes {
			registrar.SetRefreshMode(topicType, sse.TopicRefreshModeOnDemand)
		}
		registrar.SetPublishOnWake(sse.TopicTypeWikiTree, true)
		registrar.SetPublishOnWake(sse.TopicTypeLegacyDocTree, true)
	}

	// Run-driven topics have an event-store invalidation path. Keeping them on
	// demand avoids repeated history and run-list reads while browsers are
	// connected; DAG-run event collection wakes the exact and aggregate topics.
	if srv.eventService != nil {
		for _, topicType := range []sse.TopicType{
			sse.TopicTypeDAGRun,
			sse.TopicTypeSubDAGRun,
			sse.TopicTypeDAGHistory,
			sse.TopicTypeDAGRuns,
			sse.TopicTypeQueues,
		} {
			registrar.SetRefreshMode(topicType, sse.TopicRefreshModeOnDemand)
		}
		if appStreamAvailable {
			registrar.SetRefreshMode(sse.TopicTypeDAGsList, sse.TopicRefreshModeOnDemand)
			registrar.SetPublishOnWake(sse.TopicTypeDAGsList, true)
		}
		registrar.SetPublishOnWake(sse.TopicTypeDAGRuns, true)
		registrar.SetPublishOnWake(sse.TopicTypeQueues, true)
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

// buildStreamAuthOptions builds auth options for streaming endpoints.
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
