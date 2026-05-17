// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/dagucloud/dagu/api/v1"
	"github.com/dagucloud/dagu/internal/agent"
	"github.com/dagucloud/dagu/internal/agentoauth"
	"github.com/dagucloud/dagu/internal/auth"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/cmn/eval"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/baseconfig"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/license"
	notificationmodel "github.com/dagucloud/dagu/internal/notification"
	"github.com/dagucloud/dagu/internal/remotenode"
	"github.com/dagucloud/dagu/internal/runtime"
	secretpkg "github.com/dagucloud/dagu/internal/secret"
	"github.com/dagucloud/dagu/internal/service/audit"
	authservice "github.com/dagucloud/dagu/internal/service/auth"
	"github.com/dagucloud/dagu/internal/service/coordinator"
	"github.com/dagucloud/dagu/internal/service/eventstore"
	"github.com/dagucloud/dagu/internal/service/frontend/api/pathutil"
	frontendauth "github.com/dagucloud/dagu/internal/service/frontend/auth"
	notificationservice "github.com/dagucloud/dagu/internal/service/notification"
	"github.com/dagucloud/dagu/internal/service/resource"
	"github.com/dagucloud/dagu/internal/service/scheduler"
	"github.com/dagucloud/dagu/internal/tunnel"
	"github.com/dagucloud/dagu/internal/workspace"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/go-chi/chi/v5"
	oapimiddleware "github.com/oapi-codegen/nethttp-middleware"
	"github.com/prometheus/client_golang/prometheus"
)

var _ api.StrictServerInterface = (*API)(nil)

type API struct {
	dagStore             exec.DAGStore
	dagRunStore          exec.DAGRunStore
	dagRunMgr            runtime.Manager
	queueStore           exec.QueueStore
	procStore            exec.ProcStore
	dagRunLeaseStore     exec.DAGRunLeaseStore
	workerHeartbeatStore exec.WorkerHeartbeatStore
	remoteNodeResolver   *remotenode.Resolver
	remoteNodeStore      remotenode.Store
	logEncodingCharset   string
	config               *config.Config
	metricsRegistry      *prometheus.Registry
	coordinatorCli       coordinator.Client
	serviceRegistry      exec.ServiceRegistry
	subCmdBuilder        *runtime.SubCmdBuilder
	resourceService      *resource.Service
	authService          AuthService
	auditService         *audit.Service
	eventService         *eventstore.Service
	notificationService  NotificationService
	syncService          SyncService
	tunnelService        *tunnel.Service
	defaultExecMode      config.ExecutionMode
	dagWritesDisabled    bool // True when git sync read-only mode is active
	agentConfigStore     agent.ConfigStore
	agentModelStore      agent.ModelStore
	agentMemoryStore     agent.MemoryStore
	agentSoulStore       agent.SoulStore
	agentOAuthManager    *agentoauth.Manager
	agentAPI             *agent.API
	docStore             agent.DocStore
	baseConfigStore      baseconfig.Store
	secretStore          secretpkg.Store
	licenseManager       *license.Manager
	workspaceStore       workspace.Store
	leaseStaleThreshold  time.Duration
	schedulerStateStore  scheduler.WatermarkStore
	dagMutationNotifier  func(fileName string)
	docMutationNotifier  func()
}

type NotificationService interface {
	GetByDAGName(ctx context.Context, dagName string) (*notificationmodel.Settings, error)
	Save(ctx context.Context, settings *notificationmodel.Settings, updatedBy string) (*notificationmodel.Settings, error)
	DeleteByDAGName(ctx context.Context, dagName string) error
	GetWorkspaceSettings(ctx context.Context) (*notificationmodel.WorkspaceSettings, error)
	SaveWorkspaceSettings(ctx context.Context, settings *notificationmodel.WorkspaceSettings, updatedBy string) (*notificationmodel.WorkspaceSettings, error)
	GetRouteSet(ctx context.Context, scope notificationmodel.RouteScope, workspace string) (*notificationmodel.RouteSet, error)
	ListRouteSets(ctx context.Context) ([]*notificationmodel.RouteSet, error)
	SaveRouteSet(ctx context.Context, routeSet *notificationmodel.RouteSet, updatedBy string) (*notificationmodel.RouteSet, error)
	ListChannels(ctx context.Context) ([]*notificationmodel.Channel, error)
	GetChannel(ctx context.Context, channelID string) (*notificationmodel.Channel, error)
	SaveChannel(ctx context.Context, channel *notificationmodel.Channel, updatedBy string) (*notificationmodel.Channel, error)
	DeleteChannel(ctx context.Context, channelID string) error
	SendTest(ctx context.Context, dagName, targetID string, eventType eventstore.EventType) ([]notificationservice.TestResult, error)
}

// AuthService defines the interface for authentication operations.
// This allows the API to work with or without auth service being configured.
type AuthService interface {
	Authenticate(ctx context.Context, username, password string) (*auth.User, error)
	GenerateToken(user *auth.User) (*authservice.TokenResult, error)
	GetUserFromToken(ctx context.Context, token string) (*auth.User, error)
	CreateUser(ctx context.Context, input authservice.CreateUserInput) (*auth.User, error)
	GetUser(ctx context.Context, id string) (*auth.User, error)
	ListUsers(ctx context.Context) ([]*auth.User, error)
	UpdateUser(ctx context.Context, id string, input authservice.UpdateUserInput) (*auth.User, error)
	DeleteUser(ctx context.Context, id string, currentUserID string) error
	ChangePassword(ctx context.Context, userID, oldPassword, newPassword string) error
	ResetPassword(ctx context.Context, userID, newPassword string) error
	// API Key management
	CreateAPIKey(ctx context.Context, input authservice.CreateAPIKeyInput, creatorID string) (*authservice.CreateAPIKeyResult, error)
	GetAPIKey(ctx context.Context, id string) (*auth.APIKey, error)
	ListAPIKeys(ctx context.Context) ([]*auth.APIKey, error)
	UpdateAPIKey(ctx context.Context, id string, input authservice.UpdateAPIKeyInput) (*auth.APIKey, error)
	DeleteAPIKey(ctx context.Context, id string) error
	ValidateAPIKey(ctx context.Context, keySecret string) (*auth.APIKey, error)
	HasAPIKeyStore() bool
	// Webhook management
	CreateWebhook(ctx context.Context, dagName, creatorID string) (*authservice.CreateWebhookResult, error)
	GetWebhookByDAGName(ctx context.Context, dagName string) (*auth.Webhook, error)
	ListWebhooks(ctx context.Context) ([]*auth.Webhook, error)
	DeleteWebhook(ctx context.Context, dagName string) error
	RegenerateWebhookToken(ctx context.Context, dagName string) (*authservice.CreateWebhookResult, error)
	EnableWebhookHMAC(ctx context.Context, dagName string, authMode auth.WebhookAuthMode, enforcementMode auth.WebhookHMACEnforcementMode) (*authservice.WebhookHMACSecretResult, error)
	ConfigureWebhookHMAC(ctx context.Context, dagName string, authMode auth.WebhookAuthMode, enforcementMode auth.WebhookHMACEnforcementMode) (*auth.Webhook, error)
	RegenerateWebhookHMACSecret(ctx context.Context, dagName string) (*authservice.WebhookHMACSecretResult, error)
	DisableWebhookHMAC(ctx context.Context, dagName string) (*auth.Webhook, error)
	AuthorizeWebhookRequest(ctx context.Context, dagName, token, signature string, body []byte) (*auth.Webhook, error)
	ToggleWebhook(ctx context.Context, dagName string, enabled bool) (*auth.Webhook, error)
	ValidateWebhookToken(ctx context.Context, dagName, token string) (*auth.Webhook, error)
	HasWebhookStore() bool
	// Setup
	CountUsers(ctx context.Context) (int64, error)
}

// APIOption is a functional option for configuring the API.
type APIOption func(*API)

// WithAuthService returns an APIOption that sets the API's AuthService.
// When applied, the provided AuthService will be used by API methods and middleware; passing nil disables authentication.
func WithAuthService(as AuthService) APIOption {
	return func(a *API) {
		a.authService = as
	}
}

// WithAuditService returns an APIOption that sets the API's AuditService.
func WithAuditService(as *audit.Service) APIOption {
	return func(a *API) {
		a.auditService = as
	}
}

// WithEventService returns an APIOption that sets the API's event service.
func WithEventService(es *eventstore.Service) APIOption {
	return func(a *API) {
		a.eventService = es
	}
}

func WithNotificationService(ns NotificationService) APIOption {
	return func(a *API) {
		a.notificationService = ns
	}
}

// WithSyncService returns an APIOption that sets the API's SyncService.
func WithSyncService(ss SyncService) APIOption {
	return func(a *API) {
		a.syncService = ss
	}
}

// WithTunnelService returns an APIOption that sets the API's TunnelService.
func WithTunnelService(ts *tunnel.Service) APIOption {
	return func(a *API) {
		a.tunnelService = ts
	}
}

// WithBaseConfigStore returns an APIOption that sets the API's base config store.
func WithBaseConfigStore(store baseconfig.Store) APIOption {
	return func(a *API) {
		a.baseConfigStore = store
	}
}

// WithSecretStore returns an APIOption that sets the secret registry store.
func WithSecretStore(store secretpkg.Store) APIOption {
	return func(a *API) {
		a.secretStore = store
	}
}

// WithAgentConfigStore returns an APIOption that sets the API's agent config store.
func WithAgentConfigStore(store agent.ConfigStore) APIOption {
	return func(a *API) {
		a.agentConfigStore = store
	}
}

// WithAgentModelStore returns an APIOption that sets the API's agent model store.
func WithAgentModelStore(store agent.ModelStore) APIOption {
	return func(a *API) {
		a.agentModelStore = store
	}
}

// WithAgentMemoryStore returns an APIOption that sets the API's agent memory store.
func WithAgentMemoryStore(store agent.MemoryStore) APIOption {
	return func(a *API) {
		a.agentMemoryStore = store
	}
}

// WithAgentSoulStore returns an APIOption that sets the API's agent soul store.
func WithAgentSoulStore(store agent.SoulStore) APIOption {
	return func(a *API) {
		a.agentSoulStore = store
	}
}

// WithAgentOAuthManager returns an APIOption that sets the API's agent OAuth manager.
func WithAgentOAuthManager(manager *agentoauth.Manager) APIOption {
	return func(a *API) {
		a.agentOAuthManager = manager
	}
}

// WithLicenseManager returns an APIOption that sets the API's license manager.
func WithLicenseManager(m *license.Manager) APIOption {
	return func(a *API) {
		a.licenseManager = m
	}
}

// WithDocStore returns an APIOption that sets the API's doc store.
func WithDocStore(store agent.DocStore) APIOption {
	return func(a *API) {
		a.docStore = store
	}
}

// WithRemoteNodeResolver returns an APIOption that sets the remote node resolver.
func WithRemoteNodeResolver(r *remotenode.Resolver) APIOption {
	return func(a *API) {
		a.remoteNodeResolver = r
	}
}

// WithRemoteNodeStore returns an APIOption that sets the remote node store.
func WithRemoteNodeStore(s remotenode.Store) APIOption {
	return func(a *API) {
		a.remoteNodeStore = s
	}
}

// WithWorkspaceStore returns an APIOption that sets the workspace store.
func WithWorkspaceStore(s workspace.Store) APIOption {
	return func(a *API) {
		a.workspaceStore = s
	}
}

// WithSchedulerStateStore sets the scheduler state store used for next-run projections.
func WithSchedulerStateStore(store scheduler.WatermarkStore) APIOption {
	return func(a *API) {
		a.schedulerStateStore = store
	}
}

// WithDAGMutationNotifier returns an APIOption that is called after successful
// DAG definition mutations that should invalidate live DAG views.
func WithDAGMutationNotifier(fn func(fileName string)) APIOption {
	return func(a *API) {
		a.dagMutationNotifier = fn
	}
}

// WithDocMutationNotifier returns an APIOption that is called after successful
// document mutations that should invalidate live document views.
func WithDocMutationNotifier(fn func()) APIOption {
	return func(a *API) {
		a.docMutationNotifier = fn
	}
}

// WithDAGRunLeaseStore sets the shared distributed run lease store.
func WithDAGRunLeaseStore(store exec.DAGRunLeaseStore) APIOption {
	return func(a *API) {
		a.dagRunLeaseStore = store
	}
}

// WithWorkerHeartbeatStore sets the shared worker heartbeat store used for
// conservative distributed run auto-repair on single-run reads.
func WithWorkerHeartbeatStore(store exec.WorkerHeartbeatStore) APIOption {
	return func(a *API) {
		a.workerHeartbeatStore = store
	}
}

// WithLeaseStaleThreshold overrides the distributed lease stale threshold used
// by queue endpoints when listing running distributed DAG runs.
func WithLeaseStaleThreshold(threshold time.Duration) APIOption {
	return func(a *API) {
		a.leaseStaleThreshold = threshold
	}
}

// WithAgentAPI returns an APIOption that sets the API's agent API instance.
func WithAgentAPI(a *agent.API) APIOption {
	return func(api *API) {
		api.agentAPI = a
	}
}

// New constructs an API configured with the provided stores, runtime manager,
// configuration, coordinator client, service registry, Prometheus registry,
// and resource service. It builds the remote node map and base path, then
// applies any supplied APIOption functions to customize the instance.
func New(
	dr exec.DAGStore,
	drs exec.DAGRunStore,
	qs exec.QueueStore,
	ps exec.ProcStore,
	drm runtime.Manager,
	cfg *config.Config,
	cc coordinator.Client,
	sr exec.ServiceRegistry,
	mr *prometheus.Registry,
	rs *resource.Service,
	opts ...APIOption,
) *API {
	a := &API{
		dagStore:            dr,
		dagRunStore:         drs,
		queueStore:          qs,
		procStore:           ps,
		dagRunMgr:           drm,
		logEncodingCharset:  cfg.UI.LogEncodingCharset,
		subCmdBuilder:       runtime.NewSubCmdBuilder(cfg),
		config:              cfg,
		coordinatorCli:      cc,
		serviceRegistry:     sr,
		metricsRegistry:     mr,
		resourceService:     rs,
		defaultExecMode:     cfg.DefaultExecMode,
		leaseStaleThreshold: exec.DefaultStaleLeaseThreshold,
	}

	for _, opt := range opts {
		opt(a)
	}

	// Set read-only mode flag based on git sync config
	// When enabled with push disabled, DAG write operations are blocked
	a.dagWritesDisabled = cfg.GitSync.Enabled && !cfg.GitSync.PushEnabled

	return a
}

func (a *API) notifyDAGMutation(fileName string) {
	if a.dagMutationNotifier != nil {
		a.dagMutationNotifier(fileName)
	}
}

func (a *API) notifyDocMutation() {
	if a.docMutationNotifier != nil {
		a.docMutationNotifier()
	}
}

func (a *API) webhookMaxPayloadSize() int {
	if a.config == nil || a.config.Webhooks.MaxPayloadSize <= 0 {
		return config.DefaultWebhookMaxPayloadSize
	}
	return a.config.Webhooks.MaxPayloadSize
}

func (a *API) ConfigureRoutes(ctx context.Context, r chi.Router) error {
	swagger, err := a.loadOpenAPISpec(ctx)
	if err != nil {
		return err
	}
	mountedAPIPath := a.evaluateMountedAPIPath(ctx)

	if a.config.Server.StrictValidation {
		r.Use(a.createValidatorMiddleware(swagger))
	}

	authOptions, err := a.buildAuthOptions(mountedAPIPath)
	if err != nil {
		return err
	}

	r.Group(func(r chi.Router) {
		r.Use(frontendauth.ClientIPMiddleware())
		r.Use(frontendauth.Middleware(authOptions))
		r.Use(WithRemoteNode(a.remoteNodeResolver, mountedAPIPath))
		r.Use(WebhookRequestContextMiddleware(a.webhookMaxPayloadSize()))

		middlewares := []api.StrictMiddlewareFunc{validateDAGFileNameMiddleware}
		options := api.StrictHTTPServerOptions{
			ResponseErrorHandlerFunc: a.handleError,
		}
		handler := api.NewStrictHandlerWithOptions(a, middlewares, options)
		r.Mount("/", api.Handler(handler))
	})

	return nil
}

func validateDAGFileNameMiddleware(
	next api.StrictHandlerFunc,
	_ string,
) api.StrictHandlerFunc {
	return func(ctx context.Context, w http.ResponseWriter, r *http.Request, request any) (any, error) {
		if err := validateDAGFileNameFromRequest(request); err != nil {
			return nil, err
		}
		return next(ctx, w, r, request)
	}
}

func validateDAGFileNameFromRequest(request any) error {
	v := reflect.ValueOf(request)
	if !v.IsValid() {
		return nil
	}
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return nil
	}

	fileName := v.FieldByName("FileName")
	if !fileName.IsValid() || fileName.Kind() != reflect.String {
		return nil
	}

	if err := core.ValidateDAGName(fileName.String()); err != nil {
		return &Error{
			HTTPStatus: http.StatusBadRequest,
			Code:       api.ErrorCodeBadRequest,
			Message:    err.Error(),
		}
	}
	return nil
}

func (a *API) evaluateBasePath(ctx context.Context) string {
	basePath := a.config.Server.BasePath
	if evaluated, err := eval.String(ctx, basePath, eval.WithOSExpansion()); err != nil {
		logger.Warn(ctx, "Failed to evaluate server base path",
			tag.Path(basePath),
			tag.Error(err))
	} else {
		basePath = evaluated
	}
	return basePath
}

func (a *API) evaluateMountedAPIPath(ctx context.Context) string {
	return pathutil.BuildMountedAPIPath(a.evaluateBasePath(ctx), a.config.Server.APIBasePath)
}

func (a *API) loadOpenAPISpec(ctx context.Context) (*openapi3.T, error) {
	swagger, err := api.GetSwagger()
	if err != nil {
		return nil, fmt.Errorf("failed to get swagger: %w", err)
	}

	swagger.Servers = openapi3.Servers{
		&openapi3.Server{URL: a.evaluateMountedAPIPath(ctx)},
	}
	return swagger, nil
}

func (a *API) createValidatorMiddleware(swagger *openapi3.T) func(http.Handler) http.Handler {
	return oapimiddleware.OapiRequestValidatorWithOptions(
		swagger, &oapimiddleware.Options{
			SilenceServersWarning: true,
			Options: openapi3filter.Options{
				AuthenticationFunc: func(_ context.Context, _ *openapi3filter.AuthenticationInput) error {
					return nil
				},
			},
		},
	)
}

func (a *API) buildAuthOptions(mountedAPIPath string) (frontendauth.Options, error) {
	authConfig := a.config.Server.Auth

	// Setup endpoint is intentionally public — the handler enforces a one-time
	// guard (returns 403 once an admin user exists).
	publicPaths := []string{
		pathutil.BuildPublicEndpointPath(mountedAPIPath, "health"),
		pathutil.BuildPublicEndpointPath(mountedAPIPath, "auth/login"),
		pathutil.BuildPublicEndpointPath(mountedAPIPath, "auth/setup"),
	}
	if a.config.Server.Metrics == config.MetricsAccessPublic {
		publicPaths = append(publicPaths, pathutil.BuildPublicEndpointPath(mountedAPIPath, "metrics"))
	}

	// When auth mode is "none", disable all authentication entirely.
	// Any leftover token, basic auth, or OIDC settings in the config are ignored.
	if authConfig.Mode == config.AuthModeNone {
		return frontendauth.Options{
			Realm:       "restricted",
			PublicPaths: publicPaths,
			PublicPathPrefixes: []string{
				pathutil.BuildPublicEndpointPath(mountedAPIPath, "webhooks") + "/",
			},
		}, nil
	}

	authOptions := frontendauth.Options{
		Realm:        "restricted",
		AuthRequired: true,
		PublicPaths:  publicPaths,
		PublicPathPrefixes: []string{
			pathutil.BuildPublicEndpointPath(mountedAPIPath, "webhooks") + "/",
		},
	}

	if authConfig.Mode == config.AuthModeBasic {
		authOptions.BasicAuthEnabled = true
		authOptions.Creds = map[string]string{authConfig.Basic.Username: authConfig.Basic.Password}
	}

	if err := a.configureBuiltinAuth(authConfig, &authOptions); err != nil {
		return frontendauth.Options{}, err
	}

	return authOptions, nil
}

func (a *API) configureBuiltinAuth(authConfig config.Auth, opts *frontendauth.Options) error {
	if authConfig.Mode != config.AuthModeBuiltin {
		return nil
	}

	if a.authService == nil {
		return fmt.Errorf("builtin auth mode configured but auth service not initialized")
	}

	opts.JWTValidator = a.authService
	if a.authService.HasAPIKeyStore() {
		opts.APIKeyValidator = a.authService
	}
	return nil
}

func (a *API) handleError(w http.ResponseWriter, r *http.Request, err error) {
	code, message, httpStatusCode := a.resolveError(err)

	if httpStatusCode == http.StatusInternalServerError {
		logger.Errorf(r.Context(), "Internal server error: %v", err)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpStatusCode)
	_ = json.NewEncoder(w).Encode(api.Error{
		Code:    code,
		Message: message,
	})
}

func (a *API) resolveError(err error) (api.ErrorCode, string, int) {
	var apiErr *Error
	if errors.As(err, &apiErr) {
		return apiErr.Code, apiErr.Message, apiErr.HTTPStatus
	}

	if errors.Is(err, exec.ErrDAGNotFound) {
		return api.ErrorCodeNotFound, "DAG not found", http.StatusNotFound
	}
	if errors.Is(err, exec.ErrDAGRunIDNotFound) {
		return api.ErrorCodeNotFound, "dag-run ID not found", http.StatusNotFound
	}
	if errors.Is(err, exec.ErrDAGAlreadyExists) {
		return api.ErrorCodeAlreadyExists, "DAG already exists", http.StatusConflict
	}

	return api.ErrorCodeInternalError, "An unexpected error occurred", http.StatusInternalServerError
}

func (a *API) isAllowed(perm config.Permission) error {
	if !a.config.Server.Permissions[perm] {
		return errPermissionDenied
	}
	return nil
}

// requireAdmin checks if the current user has admin role.
// Returns nil if auth is not enabled (authService is nil).
func (a *API) requireAdmin(ctx context.Context) error {
	if a.authService == nil {
		return nil
	}
	user, ok := auth.UserFromContext(ctx)
	if !ok {
		return errAuthRequired
	}
	if !user.Role.IsAdmin() {
		return errInsufficientPermissions
	}
	return nil
}

// requireManagerOrAbove checks if the current user has manager or admin role.
// Returns nil if auth is not enabled (authService is nil).
func (a *API) requireManagerOrAbove(ctx context.Context) error {
	if a.authService == nil {
		return nil
	}
	user, ok := auth.UserFromContext(ctx)
	if !ok {
		return errAuthRequired
	}
	if !user.Role.CanManageAudit() {
		return errInsufficientPermissions
	}
	return nil
}

// requireDeveloperOrAbove checks if the current user has developer, manager, or admin role.
// Returns nil if auth is not enabled (authService is nil).
func (a *API) requireDeveloperOrAbove(ctx context.Context) error {
	if a.authService == nil {
		return nil
	}
	user, ok := auth.UserFromContext(ctx)
	if !ok {
		return errAuthRequired
	}
	if !user.Role.CanWrite() {
		return errInsufficientPermissions
	}
	return nil
}

// Predefined errors for common authorization failures.
var (
	errDAGWritesDisabled = &Error{
		HTTPStatus: http.StatusForbidden,
		Code:       api.ErrorCodeForbidden,
		Message:    "DAG modifications disabled: Git sync is in read-only mode (pushEnabled: false)",
	}
	errAuthRequired = &Error{
		HTTPStatus: http.StatusUnauthorized,
		Code:       api.ErrorCodeUnauthorized,
		Message:    "Authentication required",
	}
	errPermissionDenied = &Error{
		HTTPStatus: http.StatusForbidden,
		Code:       api.ErrorCodeForbidden,
		Message:    "Permission denied",
	}
	errInsufficientPermissions = &Error{
		HTTPStatus: http.StatusForbidden,
		Code:       api.ErrorCodeForbidden,
		Message:    "Insufficient permissions",
	}
	errUserManagementDisabled = &Error{
		HTTPStatus: http.StatusUnauthorized,
		Code:       api.ErrorCodeUnauthorized,
		Message:    "User management is not enabled",
	}
	errRBACNotLicensed = &Error{
		HTTPStatus: http.StatusForbidden,
		Code:       api.ErrorCodeForbidden,
		Message:    "User management requires a Dagu Pro license",
	}
	errAuditNotLicensed = &Error{
		HTTPStatus: http.StatusForbidden,
		Code:       api.ErrorCodeForbidden,
		Message:    "Audit logs require a Dagu Pro license",
	}
	errReusableNotificationChannelsNotLicensed = &Error{
		HTTPStatus: http.StatusForbidden,
		Code:       api.ErrorCodeForbidden,
		Message:    "Notification channels and rules require an active Dagu license or trial",
	}
)

// requireDAGWrite checks all permissions for DAG write operations:
// 1. Server-level permission (PermissionWriteDAGs)
// 2. User role permission (CanWrite)
// 3. Git sync read-only mode (dagWritesDisabled)
func (a *API) requireDAGWrite(ctx context.Context) error {
	if !a.config.Server.Permissions[config.PermissionWriteDAGs] {
		return errPermissionDenied
	}
	if a.authService != nil {
		user, ok := auth.UserFromContext(ctx)
		if !ok {
			return errAuthRequired
		}
		if !user.Role.CanWrite() {
			return errInsufficientPermissions
		}
	}
	if a.dagWritesDisabled {
		return errDAGWritesDisabled
	}
	return nil
}

// requireUserManagement checks if user management is enabled.
func (a *API) requireUserManagement() error {
	if a.authService == nil {
		return errUserManagementDisabled
	}
	return nil
}

// requireLicensedRBAC checks if the RBAC feature is licensed.
func (a *API) requireLicensedRBAC() error {
	if a.licenseManager == nil {
		return errRBACNotLicensed
	}
	if !a.licenseManager.Checker().IsFeatureEnabled(license.FeatureRBAC) {
		return errRBACNotLicensed
	}
	return nil
}

// requireLicensedAudit checks if the audit feature is licensed.
func (a *API) requireLicensedAudit() error {
	if a.licenseManager == nil {
		return errAuditNotLicensed
	}
	if !a.licenseManager.Checker().IsFeatureEnabled(license.FeatureAudit) {
		return errAuditNotLicensed
	}
	return nil
}

func (a *API) requireLicensedReusableNotificationChannels() error {
	if a.licenseManager == nil {
		return errReusableNotificationChannelsNotLicensed
	}
	if !license.HasActiveLicense(a.licenseManager.Checker()) {
		return errReusableNotificationChannelsNotLicensed
	}
	return nil
}

func (a *API) isAuditLicensed() bool {
	if a.auditService == nil {
		return false
	}
	if a.licenseManager == nil {
		return true
	}
	return a.licenseManager.Checker().IsFeatureEnabled(license.FeatureAudit)
}

// logAudit logs an audit entry with the specified category, action, and details.
// It silently returns if the audit service is not configured.
// User and IP are extracted from context; missing user is allowed (recorded as empty).
func (a *API) logAudit(ctx context.Context, category audit.Category, action string, details any) {
	if !a.isAuditLicensed() {
		return
	}

	var userID, username string
	if user, ok := auth.UserFromContext(ctx); ok && user != nil {
		userID = user.ID
		username = user.Username
	}

	clientIP, _ := auth.ClientIPFromContext(ctx)

	var detailsStr string
	if details != nil {
		detailsJSON, err := json.Marshal(details)
		if err != nil {
			logger.Warn(ctx, "Failed to marshal audit details", tag.Error(err))
			detailsStr = "{}"
		} else {
			detailsStr = string(detailsJSON)
		}
	}

	entry := audit.NewEntry(category, action, userID, username).
		WithDetails(detailsStr).
		WithIPAddress(clientIP)

	if err := a.auditService.Log(ctx, entry); err != nil {
		logger.Warn(ctx, "Failed to write audit log",
			tag.Error(err),
			slog.String("action", action),
			slog.String("category", string(category)),
		)
	}
}

func (a *API) withEventContext(ctx context.Context) context.Context {
	if a == nil || a.eventService == nil {
		return ctx
	}
	return eventstore.WithContext(ctx, a.eventService, eventstore.Source{
		Service: eventstore.SourceServiceServer,
	})
}

func (a *API) updateDAGRunStatus(ctx context.Context, ref exec.DAGRunRef, status exec.DAGRunStatus) error {
	updateStatus := func() error {
		if a != nil && a.dagRunStore != nil {
			var (
				attempt exec.DAGRunAttempt
				err     error
			)
			if ref.ID == status.DAGRunID {
				attempt, err = a.dagRunStore.FindAttempt(ctx, ref)
			} else {
				attempt, err = a.dagRunStore.FindSubAttempt(ctx, ref, status.DAGRunID)
			}
			if err != nil {
				return err
			}
			latest, err := attempt.ReadStatus(ctx)
			if err != nil {
				return err
			}
			if latest != nil && latest.Status == status.Status {
				return a.dagRunMgr.UpdateStatus(ctx, ref, status)
			}
		}
		return a.dagRunMgr.UpdateStatus(a.withEventContext(ctx), ref, status)
	}

	err := updateStatus()
	if err == nil || !isTransientDAGRunStatusUpdateError(err) {
		return err
	}

	const (
		retryWindow   = 3 * time.Second
		retryInterval = 50 * time.Millisecond
	)
	deadline := time.Now().Add(retryWindow)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(retryInterval):
		}

		err = updateStatus()
		if err == nil || !isTransientDAGRunStatusUpdateError(err) {
			return err
		}
	}

	return err
}

func isTransientDAGRunStatusUpdateError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "used by another process") ||
		strings.Contains(msg, "cannot access the file") ||
		strings.Contains(msg, "access is denied") ||
		strings.Contains(msg, "sharing violation")
}

// ptrOf returns a pointer to v, or nil if v is the zero value for its type.
func ptrOf[T any](v T) *T {
	if reflect.ValueOf(v).IsZero() {
		return nil
	}
	return &v
}

func valueOf[T any](ptr *T) T {
	if ptr == nil {
		var zero T
		return zero
	}
	return *ptr
}

// toPagination converts a paginated result to an API pagination object.
func toPagination[T any](paginatedResult exec.PaginatedResult[T]) api.Pagination {
	return api.Pagination{
		CurrentPage:  paginatedResult.CurrentPage,
		NextPage:     paginatedResult.NextPage,
		PrevPage:     paginatedResult.PrevPage,
		TotalPages:   paginatedResult.TotalPages,
		TotalRecords: paginatedResult.TotalCount,
	}
}
