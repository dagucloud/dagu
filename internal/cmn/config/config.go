// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"fmt"
	"net/url"
	"slices"
	"time"
)

// Config holds the overall configuration for the application.
type Config struct {
	Core              Core
	Server            Server
	EventStore        EventStoreConfig
	ControlPlaneStore ControlPlaneStoreConfig
	Paths             PathsConfig
	Secrets           SecretsConfig
	UI                UI
	Queues            Queues
	Coordinator       Coordinator
	Worker            Worker
	Proc              Proc
	Scheduler         Scheduler
	Monitoring        MonitoringConfig
	DefaultExecMode   ExecutionMode
	Cache             CacheMode
	GitSync           GitSyncConfig
	Tunnel            TunnelConfig
	Bots              BotsConfig
	License           LicenseConfig
	Notices           []string
	Warnings          []string
}

// BotProvider identifies which bot integration to use.
type BotProvider string

const (
	BotProviderNone     BotProvider = ""
	BotProviderTelegram BotProvider = "telegram"
	BotProviderSlack    BotProvider = "slack"
	BotProviderDiscord  BotProvider = "discord"
)

var DefaultBotInterestedEventTypes = []string{
	"dag.run.waiting",
	"dag.run.succeeded",
	"dag.run.failed",
	"dag.run.aborted",
	"dag.run.rejected",
}

// BotsConfig holds the configuration for bot integrations.
type BotsConfig struct {
	Provider BotProvider
	SafeMode bool
	Telegram TelegramBotConfig
	Slack    SlackBotConfig
	Discord  DiscordBotConfig
}

// TelegramBotConfig holds the Telegram-specific bot configuration.
type TelegramBotConfig struct {
	Token                string
	AllowedChatIDs       []int64
	InterestedEventTypes []string
}

// SlackBotConfig holds the Slack-specific bot configuration.
type SlackBotConfig struct {
	BotToken             string
	AppToken             string
	AllowedChannelIDs    []string
	InterestedEventTypes []string
	RespondToAll         bool // respond to all channel messages, not just @mentions
}

// DiscordBotConfig holds the Discord-specific bot configuration.
type DiscordBotConfig struct {
	Token                string
	AllowedChannelIDs    []string
	InterestedEventTypes []string
	RespondToAll         bool // respond to all channel messages, not just @mentions
}

// GitSyncConfig holds the configuration for Git sync functionality.
type GitSyncConfig struct {
	Enabled     bool
	Repository  string // Format: github.com/org/repo or https://github.com/org/repo.git
	Branch      string
	Path        string // Subdirectory to sync (empty = root)
	Auth        GitSyncAuthConfig
	AutoSync    GitSyncAutoSyncConfig
	PushEnabled bool
	Commit      GitSyncCommitConfig
}

// GitSyncAuthConfig holds authentication configuration for Git operations.
type GitSyncAuthConfig struct {
	Type          string // "token" or "ssh"
	Token         string // Personal access token for HTTPS
	SSHKeyPath    string
	SSHPassphrase string
}

// GitSyncAutoSyncConfig holds configuration for automatic synchronization.
type GitSyncAutoSyncConfig struct {
	Enabled   bool
	OnStartup bool
	Interval  int // Seconds; 0 disables periodic sync
}

// GitSyncCommitConfig holds configuration for Git commits.
type GitSyncCommitConfig struct {
	AuthorName  string // Default: "Dagu"
	AuthorEmail string // Default: "dagu@localhost"
}

// TunnelConfig holds the configuration for tunnel services.
type TunnelConfig struct {
	Enabled       bool
	Tailscale     TailscaleTunnelConfig
	AllowTerminal bool     // Default: false for security
	AllowedIPs    []string // IP allowlist (empty = allow all)
	RateLimiting  TunnelRateLimitConfig
}

// TailscaleTunnelConfig holds Tailscale settings.
type TailscaleTunnelConfig struct {
	AuthKey  string // Empty requires interactive login
	Hostname string // Machine name in tailnet (default: "dagu")
	Funnel   bool   // Enable public internet access
	HTTPS    bool   // Enable HTTPS for tailnet-only access
	StateDir string // Default: $DAGU_HOME/tailscale
}

// TunnelRateLimitConfig holds rate limiting configuration for auth endpoints.
type TunnelRateLimitConfig struct {
	Enabled              bool
	LoginAttempts        int
	WindowSeconds        int
	BlockDurationSeconds int
}

const TunnelProviderTailscale = "tailscale"

// LicenseConfig holds the configuration for license activation.
type LicenseConfig struct {
	Key      string
	CloudURL string
}

// ExecutionMode represents the default execution mode for DAGs.
type ExecutionMode string

const (
	ExecutionModeLocal       ExecutionMode = "local"
	ExecutionModeDistributed ExecutionMode = "distributed"
)

// MonitoringConfig holds the configuration for system monitoring.
// Memory usage: ~4 metrics * (retention / interval) * 16 bytes per point.
type MonitoringConfig struct {
	Retention time.Duration
	Interval  time.Duration
}

// Core contains global configuration settings.
type Core struct {
	Debug                  bool
	LogFormat              string // "json" or "text"
	TZ                     string // e.g., "UTC", "UTC+9", "America/New_York"
	TzOffsetInSec          int
	Location               *time.Location
	DefaultShell           string // Platform default if empty
	SkipExamples           bool   // Skip auto-creation of example DAGs and default base config
	EnvPassthrough         []string
	EnvPassthroughPrefixes []string
	Peer                   Peer
	BaseEnv                BaseEnv
}

// Server contains the API server configuration.
type Server struct {
	Host              string
	Port              int
	BasePath          string // URL path for reverse proxy subpath hosting
	APIBasePath       string
	Headless          bool
	CheckUpdates      bool
	AccessLog         AccessLogMode // "all" (default), "non-public", or "none"
	LatestStatusToday bool
	TLS               *TLSConfig
	Auth              Auth
	RemoteNodes       []RemoteNode
	Permissions       map[Permission]bool
	StrictValidation  bool
	Metrics           MetricsAccess // "private" or "public"
	Terminal          TerminalConfig
	Audit             AuditConfig
	Session           SessionConfig
	SSE               SSEConfig
}

// TerminalConfig contains configuration for the web-based terminal feature.
type TerminalConfig struct {
	Enabled     bool // Default: false
	MaxSessions int  // Default: 5
}

// AuditConfig contains configuration for the audit logging feature.
type AuditConfig struct {
	Enabled       bool // Default: true
	RetentionDays int  // Default: 7; 0 = keep forever
}

// EventStoreConfig contains configuration for the centralized event store.
type EventStoreConfig struct {
	Enabled       bool // Default: true
	RetentionDays int  // Default: 1; 0 = keep forever
}

// ControlPlaneStoreBackend identifies a shared control-plane persistence backend.
type ControlPlaneStoreBackend string

const (
	ControlPlaneStoreBackendFile     ControlPlaneStoreBackend = "file"
	ControlPlaneStoreBackendPostgres ControlPlaneStoreBackend = "postgres"
)

// ControlPlaneStoreConfig holds shared control-plane persistence configuration.
type ControlPlaneStoreConfig struct {
	Backend  ControlPlaneStoreBackend
	Postgres ControlPlaneStorePostgresConfig
}

// ControlPlaneStorePostgresConfig holds PostgreSQL control-plane store configuration.
type ControlPlaneStorePostgresConfig struct {
	Server      ControlPlaneStorePostgresRoleConfig
	Scheduler   ControlPlaneStorePostgresRoleConfig
	Coordinator ControlPlaneStorePostgresRoleConfig
	Agent       ControlPlaneStorePostgresRoleConfig
}

// ControlPlaneStorePostgresRoleConfig holds PostgreSQL settings for one Dagu process role.
type ControlPlaneStorePostgresRoleConfig struct {
	DSN          string
	AutoMigrate  bool
	DirectAccess bool
	Pool         PostgresPoolConfig
}

// SessionConfig contains configuration for agent session cleanup.
type SessionConfig struct {
	MaxPerUser int // Default: 100; 0 = unlimited
}

// SSEConfig contains configuration for multiplexed SSE streaming.
type SSEConfig struct {
	MaxTopicsPerConnection int
	MaxClients             int
	HeartbeatInterval      time.Duration
	WriteBufferSize        int
	SlowClientTimeout      time.Duration
}

// Permission represents a permission string used in the application.
type Permission string

const (
	PermissionWriteDAGs Permission = "write_dags"
	PermissionRunDAGs   Permission = "run_dags"
)

// AuthMode represents the authentication mode.
type AuthMode string

const (
	AuthModeNone    AuthMode = "none"
	AuthModeBasic   AuthMode = "basic"
	AuthModeBuiltin AuthMode = "builtin"
)

// AccessLogMode represents the HTTP access log mode.
type AccessLogMode string

const (
	AccessLogAll       AccessLogMode = "all"
	AccessLogNonPublic AccessLogMode = "non-public"
	AccessLogNone      AccessLogMode = "none"
)

// MetricsAccess represents the access mode for the metrics endpoint.
type MetricsAccess string

const (
	MetricsAccessPrivate MetricsAccess = "private"
	MetricsAccessPublic  MetricsAccess = "public"
)

// Auth represents the authentication configuration.
type Auth struct {
	Mode    AuthMode
	Basic   AuthBasic
	OIDC    AuthOIDC
	Builtin AuthBuiltin
}

// AuthBasic represents basic authentication credentials.
type AuthBasic struct {
	Username string
	Password string
}

// AuthBuiltin represents builtin authentication with RBAC.
type AuthBuiltin struct {
	Token        TokenConfig
	InitialAdmin InitialAdmin
}

// TokenConfig represents JWT token configuration.
type TokenConfig struct {
	Secret string
	TTL    time.Duration
}

// InitialAdmin holds optional auto-provisioning credentials for the first admin user.
// When configured and no users exist, the server creates this admin at startup.
type InitialAdmin struct {
	Username string
	Password string
}

// IsConfigured returns true when both username and password are set.
func (ia InitialAdmin) IsConfigured() bool {
	return ia.Username != "" && ia.Password != ""
}

// AuthOIDC represents OIDC authentication configuration.
// OIDC is available as an integration under builtin auth mode (auth.mode=builtin).
type AuthOIDC struct {
	ClientID     string
	ClientSecret string
	ClientURL    string   // Application URL for callback
	Issuer       string   // OIDC provider URL
	Scopes       []string // Default: openid, profile, email
	Whitelist    []string // Email addresses always allowed

	// Builtin-specific fields
	AutoSignup     bool     // Default: true
	AllowedDomains []string // Email domain whitelist
	ButtonLabel    string   // Default: "Login with SSO"
	RoleMapping    OIDCRoleMapping
}

// IsConfigured returns true if all required OIDC fields are set.
func (o AuthOIDC) IsConfigured() bool {
	return o.ClientID != "" && o.ClientSecret != "" && o.ClientURL != "" && o.Issuer != ""
}

// OIDCRoleMapping defines how OIDC claims are mapped to Dagu roles.
type OIDCRoleMapping struct {
	DefaultRole         string            // Default: "viewer"
	GroupsClaim         string            // Default: "groups"
	GroupMappings       map[string]string // IdP group -> Dagu role
	RoleAttributePath   string            // jq expression for role extraction
	RoleAttributeStrict bool              // Deny login if no valid role found
	SkipOrgRoleSync     bool              // Only assign roles on first login
}

// PathsConfig represents the file system paths configuration.
type PathsConfig struct {
	DAGsDir            string
	DocsDir            string
	Executable         string
	LogDir             string
	ArtifactDir        string
	DataDir            string
	SuspendFlagsDir    string
	AdminLogsDir       string
	EventStoreDir      string
	BaseConfig         string
	AltDAGsDir         string
	DAGRunsDir         string
	QueueDir           string
	ProcDir            string
	ServiceRegistryDir string
	UsersDir           string
	APIKeysDir         string
	WebhooksDir        string
	SessionsDir        string
	ContextsDir        string
	RemoteNodesDir     string
	WorkspacesDir      string
	ConfigFileUsed     string
}

// SecretsConfig holds global defaults for external secret providers.
type SecretsConfig struct {
	Vault      VaultSecretsConfig
	Kubernetes KubernetesSecretsConfig
}

// VaultSecretsConfig holds shared HashiCorp Vault client defaults.
type VaultSecretsConfig struct {
	Address string
	Token   string
}

// KubernetesSecretsConfig holds shared Kubernetes client defaults.
type KubernetesSecretsConfig struct {
	Namespace  string
	Kubeconfig string
	Context    string
}

// UI holds user interface configuration.
type UI struct {
	LogEncodingCharset    string
	NavbarColor           string
	NavbarTitle           string
	MaxDashboardPageLimit int
	DAGs                  DAGsConfig
}

// DAGsConfig holds DAG list page configuration.
type DAGsConfig struct {
	SortField string
	SortOrder string
}

// RemoteNode represents a remote node configuration.
type RemoteNode struct {
	Name              string
	Description       string
	APIBaseURL        string
	AuthType          string
	BasicAuthUsername string
	BasicAuthPassword string
	AuthToken         string
	SkipTLSVerify     bool
	Timeout           int // seconds; 0 = use default
}

// TLSConfig represents TLS configuration.
type TLSConfig struct {
	CertFile string
	KeyFile  string
	CAFile   string
}

// Queues represents global queue configuration.
type Queues struct {
	Enabled bool
	Config  []QueueConfig
}

// QueueConfig represents individual queue configuration.
type QueueConfig struct {
	Name          string
	MaxActiveRuns int
}

// FindQueueConfig returns the queue config if the queue name is defined in config.
// Returns nil if not found or queues are disabled.
func (c *Config) FindQueueConfig(queueName string) *QueueConfig {
	if !c.Queues.Enabled || c.Queues.Config == nil {
		return nil
	}
	for i := range c.Queues.Config {
		if c.Queues.Config[i].Name == queueName {
			return &c.Queues.Config[i]
		}
	}
	return nil
}

// Coordinator represents the coordinator service configuration.
type Coordinator struct {
	Enabled    bool   // Default: true
	ID         string // Default: hostname@port
	Host       string // gRPC bind address
	Advertise  string // Registry address (auto-detected if empty)
	Port       int
	HealthPort int // HTTP health check port (default: 8091, 0 disables)
}

// Worker represents the worker configuration.
type Worker struct {
	ID            string            // Default: hostname@PID
	MaxActiveRuns int               // Default: 100
	Labels        map[string]string // Capability matching labels
	Coordinators  []string          // Static discovery addresses (host:port)
	HealthPort    int               // HTTP health check port (default: 8092, 0 disables)
	PostgresPool  PostgresPoolConfig
}

// Proc represents local proc-file heartbeat configuration.
type Proc struct {
	HeartbeatInterval     time.Duration // Default: 5s
	HeartbeatSyncInterval time.Duration // Default: 10s
	StaleThreshold        time.Duration // Default: 90s
}

// Scheduler represents the scheduler configuration.
type Scheduler struct {
	Port                    int           // Health check port (default: 8090)
	LockStaleThreshold      time.Duration // Default: 30s
	LockRetryInterval       time.Duration // Default: 5s
	ZombieDetectionInterval time.Duration // Default: 45s; 0 disables
	RetryFailureWindow      time.Duration // Default: 24h; 0 disables DAG-level retry scanning. Current limitation: the window is evaluated from the original DAG-run timestamp/day bucket, not the latest failed attempt timestamp.
	FailureThreshold        int           // Default: 3
}

// PostgresPoolConfig holds PostgreSQL connection pool settings.
type PostgresPoolConfig struct {
	MaxOpenConns    int // Default: 25
	MaxIdleConns    int // Default: 5
	ConnMaxLifetime int // Seconds (default: 300)
	ConnMaxIdleTime int // Seconds (default: 60)
}

// Peer holds the TLS configuration for peer connections over gRPC.
type Peer struct {
	CertFile      string
	KeyFile       string
	ClientCaFile  string
	SkipTLSVerify bool
	Insecure      bool          // Use h2c instead of TLS
	MaxRetries    int           // Default: 10 (exponential backoff, capped at 30s)
	RetryInterval time.Duration // Default: 1s
}

// Validate performs basic validation on the configuration to ensure required fields are set
// and that numerical values fall within acceptable ranges.
func (c *Config) Validate() error {
	if err := c.validateServer(); err != nil {
		return err
	}
	if err := c.validateUI(); err != nil {
		return err
	}
	if err := c.validateScheduler(); err != nil {
		return err
	}
	if err := c.validateCoordinator(); err != nil {
		return err
	}
	if err := c.validateWorker(); err != nil {
		return err
	}
	if err := c.validateBasicAuth(); err != nil {
		return err
	}
	if err := c.validateBuiltinAuth(); err != nil {
		return err
	}
	if err := c.validateExecutionMode(); err != nil {
		return err
	}
	if err := c.validateGitSync(); err != nil {
		return err
	}
	if err := c.validateTunnel(); err != nil {
		return err
	}
	if err := c.validateRemoteNodes(); err != nil {
		return err
	}
	if err := c.validateLicense(); err != nil {
		return err
	}
	if err := c.validateProc(); err != nil {
		return err
	}
	if err := c.validateEventStore(); err != nil {
		return err
	}
	if err := c.validateControlPlaneStore(); err != nil {
		return err
	}
	if err := c.validateBots(); err != nil {
		return err
	}
	return nil
}

// validateProc validates proc heartbeat settings to prevent
// configurations that would cause false-positive stale detection.
func (c *Config) validateProc() error {
	p := c.Proc
	if p.HeartbeatInterval > 0 && p.StaleThreshold > 0 && p.HeartbeatInterval >= p.StaleThreshold {
		return fmt.Errorf(
			"proc.heartbeat_interval (%s) must be less than proc.stale_threshold (%s)",
			p.HeartbeatInterval, p.StaleThreshold,
		)
	}
	if p.HeartbeatSyncInterval > 0 && p.StaleThreshold > 0 && p.HeartbeatSyncInterval >= p.StaleThreshold {
		return fmt.Errorf(
			"proc.heartbeat_sync_interval (%s) must be less than proc.stale_threshold (%s)",
			p.HeartbeatSyncInterval, p.StaleThreshold,
		)
	}
	return nil
}

func (c *Config) validateScheduler() error {
	if c.Scheduler.Port < 0 || c.Scheduler.Port > 65535 {
		return fmt.Errorf("invalid scheduler.port: %d", c.Scheduler.Port)
	}
	if c.Scheduler.FailureThreshold < 0 {
		return fmt.Errorf("scheduler.failure_threshold must be >= 0")
	}
	if c.Scheduler.LockStaleThreshold < 0 {
		return fmt.Errorf("scheduler.lock_stale_threshold must be >= 0")
	}
	if c.Scheduler.LockRetryInterval < 0 {
		return fmt.Errorf("scheduler.lock_retry_interval must be >= 0")
	}
	if c.Scheduler.ZombieDetectionInterval < 0 {
		return fmt.Errorf("scheduler.zombie_detection_interval must be >= 0")
	}
	if c.Scheduler.RetryFailureWindow < 0 {
		return fmt.Errorf("scheduler.retry_failure_window must be >= 0")
	}
	return nil
}

func (c *Config) validateEventStore() error {
	if c.EventStore.RetentionDays < 0 {
		return fmt.Errorf("event_store.retention_days must be >= 0")
	}
	return nil
}

func (c *Config) validateControlPlaneStore() error {
	switch c.ControlPlaneStore.Backend {
	case "", ControlPlaneStoreBackendFile:
		return nil
	case ControlPlaneStoreBackendPostgres:
		if err := validatePostgresPool("control_plane_store.postgres.server.pool", c.ControlPlaneStore.Postgres.Server.Pool); err != nil {
			return err
		}
		if err := validatePostgresPool("control_plane_store.postgres.scheduler.pool", c.ControlPlaneStore.Postgres.Scheduler.Pool); err != nil {
			return err
		}
		if err := validatePostgresPool("control_plane_store.postgres.coordinator.pool", c.ControlPlaneStore.Postgres.Coordinator.Pool); err != nil {
			return err
		}
		if err := validatePostgresPool("control_plane_store.postgres.agent.pool", c.ControlPlaneStore.Postgres.Agent.Pool); err != nil {
			return err
		}
		if c.ControlPlaneStore.Postgres.Server.DSN == "" &&
			c.ControlPlaneStore.Postgres.Scheduler.DSN == "" &&
			c.ControlPlaneStore.Postgres.Coordinator.DSN == "" &&
			c.ControlPlaneStore.Postgres.Agent.DSN == "" {
			return fmt.Errorf("invalid control_plane_store.backend: postgres selected but no role DSNs configured")
		}
		return nil
	default:
		return fmt.Errorf("invalid control_plane_store.backend: %q", c.ControlPlaneStore.Backend)
	}
}

func (c *Config) validateBots() error {
	if err := validateInterestedEventTypes("bots.telegram.interested_event_types", c.Bots.Telegram.InterestedEventTypes); err != nil {
		return err
	}
	if err := validateInterestedEventTypes("bots.slack.interested_event_types", c.Bots.Slack.InterestedEventTypes); err != nil {
		return err
	}
	if err := validateInterestedEventTypes("bots.discord.interested_event_types", c.Bots.Discord.InterestedEventTypes); err != nil {
		return err
	}
	return nil
}

func validateInterestedEventTypes(path string, eventTypes []string) error {
	allowed := map[string]struct{}{
		"dag.run.queued":    {},
		"dag.run.running":   {},
		"dag.run.waiting":   {},
		"dag.run.succeeded": {},
		"dag.run.failed":    {},
		"dag.run.aborted":   {},
		"dag.run.rejected":  {},
	}
	for _, eventType := range eventTypes {
		if _, ok := allowed[eventType]; !ok {
			return fmt.Errorf("%s contains unsupported event type %q", path, eventType)
		}
	}
	return nil
}

func (c *Config) validateCoordinator() error {
	if c.Coordinator.Port < 0 || c.Coordinator.Port > 65535 {
		return fmt.Errorf("invalid coordinator.port: %d", c.Coordinator.Port)
	}
	if c.Coordinator.HealthPort < 0 || c.Coordinator.HealthPort > 65535 {
		return fmt.Errorf("invalid coordinator.health_port: %d", c.Coordinator.HealthPort)
	}
	if c.Coordinator.HealthPort != 0 && c.Coordinator.Port == c.Coordinator.HealthPort {
		return fmt.Errorf("coordinator.port and coordinator.health_port must be different when health checks are enabled")
	}
	return nil
}

func (c *Config) validateWorker() error {
	if c.Worker.HealthPort < 0 || c.Worker.HealthPort > 65535 {
		return fmt.Errorf("invalid worker.health_port: %d", c.Worker.HealthPort)
	}
	if err := validatePostgresPool("worker.postgres_pool", c.Worker.PostgresPool); err != nil {
		return err
	}
	return nil
}

func validatePostgresPool(path string, pool PostgresPoolConfig) error {
	if pool.MaxOpenConns < 0 {
		return fmt.Errorf("%s.max_open_conns must be >= 0", path)
	}
	if pool.MaxIdleConns < 0 {
		return fmt.Errorf("%s.max_idle_conns must be >= 0", path)
	}
	if pool.MaxOpenConns > 0 && pool.MaxIdleConns > pool.MaxOpenConns {
		return fmt.Errorf("%s.max_idle_conns must be <= %s.max_open_conns", path, path)
	}
	if pool.ConnMaxLifetime < 0 {
		return fmt.Errorf("%s.conn_max_lifetime must be >= 0", path)
	}
	if pool.ConnMaxIdleTime < 0 {
		return fmt.Errorf("%s.conn_max_idle_time must be >= 0", path)
	}
	return nil
}

// validateServer validates server-related configuration.
func (c *Config) validateServer() error {
	if c.Server.Port < 0 || c.Server.Port > 65535 {
		return fmt.Errorf("invalid port number: %d", c.Server.Port)
	}

	if c.Server.TLS != nil {
		if c.Server.TLS.CertFile == "" || c.Server.TLS.KeyFile == "" {
			return fmt.Errorf("TLS configuration incomplete: both cert and key files are required")
		}
	}

	switch c.Server.Auth.Mode {
	case AuthModeNone, AuthModeBasic, AuthModeBuiltin:
		// Valid modes
	default:
		return fmt.Errorf("invalid auth mode: %q (must be one of: none, basic, builtin)", c.Server.Auth.Mode)
	}

	if c.Server.Session.MaxPerUser < 0 {
		return fmt.Errorf("session.max_per_user must be >= 0")
	}
	if c.Server.Terminal.MaxSessions <= 0 {
		return fmt.Errorf("terminal.max_sessions must be > 0")
	}
	if c.Server.SSE != (SSEConfig{}) {
		if c.Server.SSE.MaxTopicsPerConnection <= 0 {
			return fmt.Errorf("sse.max_topics_per_connection must be > 0")
		}
		if c.Server.SSE.MaxClients <= 0 {
			return fmt.Errorf("sse.max_clients must be > 0")
		}
		if c.Server.SSE.HeartbeatInterval <= 0 {
			return fmt.Errorf("sse.heartbeat_interval must be > 0")
		}
	}
	if c.Server.SSE.WriteBufferSize < 0 {
		return fmt.Errorf("sse.write_buffer_size must be >= 0")
	}
	if c.Server.SSE.SlowClientTimeout < 0 {
		return fmt.Errorf("sse.slow_client_timeout must be >= 0")
	}

	return nil
}

// validateUI validates UI-related configuration.
func (c *Config) validateUI() error {
	if c.UI.MaxDashboardPageLimit < 1 || c.UI.MaxDashboardPageLimit > 1000 {
		return fmt.Errorf("invalid max dashboard page limit: %d (must be 1-1000)", c.UI.MaxDashboardPageLimit)
	}
	return nil
}

// validateBasicAuth validates the basic authentication configuration.
func (c *Config) validateBasicAuth() error {
	if c.Server.Auth.Mode == AuthModeBasic {
		if c.Server.Auth.Basic.Username == "" || c.Server.Auth.Basic.Password == "" {
			return fmt.Errorf("basic auth requires both username and password to be set")
		}
		return nil
	}

	// Error if basic credentials are set under a non-basic mode.
	if c.Server.Auth.Basic.Username != "" || c.Server.Auth.Basic.Password != "" {
		return fmt.Errorf("auth.basic credentials are set but auth.mode is %q; set auth.mode to 'basic' or remove the auth.basic section", c.Server.Auth.Mode)
	}
	return nil
}

// validateBuiltinAuth validates the builtin authentication configuration.
func (c *Config) validateBuiltinAuth() error {
	if c.Server.Auth.Mode != AuthModeBuiltin {
		return nil
	}

	if c.Paths.UsersDir == "" {
		return fmt.Errorf("builtin auth requires paths.users_dir to be set")
	}
	if c.Server.Auth.Builtin.Token.TTL <= 0 {
		return fmt.Errorf("builtin auth requires a positive token TTL")
	}

	// Validate initial_admin: both fields must be set, or neither.
	ia := c.Server.Auth.Builtin.InitialAdmin
	if (ia.Username == "") != (ia.Password == "") {
		return fmt.Errorf("auth.builtin.initial_admin requires both username and password to be set, or neither")
	}

	if c.Server.Auth.OIDC.IsConfigured() {
		return c.validateOIDCForBuiltin()
	}
	return nil
}

// validateOIDCForBuiltin validates OIDC configuration under builtin auth mode.
func (c *Config) validateOIDCForBuiltin() error {
	oidc := c.Server.Auth.OIDC

	switch oidc.RoleMapping.DefaultRole {
	case "admin", "manager", "developer", "operator", "viewer":
		// Valid roles
	default:
		return fmt.Errorf("OIDC roleMapping.defaultRole must be one of: admin, manager, developer, operator, viewer (got: %q)", oidc.RoleMapping.DefaultRole)
	}

	if !slices.Contains(oidc.Scopes, "email") {
		if len(oidc.Whitelist) > 0 || len(oidc.AllowedDomains) > 0 {
			return fmt.Errorf("OIDC scopes must include 'email' when whitelist or allowedDomains is configured")
		}
		c.Warnings = append(c.Warnings, "OIDC scopes do not include 'email'; access control features will not work if added later")
	}

	return nil
}

// validateExecutionMode validates the default execution mode.
func (c *Config) validateExecutionMode() error {
	switch c.DefaultExecMode {
	case ExecutionModeLocal, ExecutionModeDistributed:
		return nil
	default:
		return fmt.Errorf("invalid default_execution_mode: %q (must be one of: local, distributed)", c.DefaultExecMode)
	}
}

// validateGitSync validates the Git sync configuration.
func (c *Config) validateGitSync() error {
	if !c.GitSync.Enabled {
		return nil
	}
	if c.GitSync.Repository == "" {
		return fmt.Errorf("git sync requires repository to be set (git_sync.repository)")
	}
	if c.GitSync.Branch == "" {
		return fmt.Errorf("git sync requires branch to be set (git_sync.branch)")
	}

	switch c.GitSync.Auth.Type {
	case "", "token", "ssh":
		// Valid (empty defaults to token)
	default:
		return fmt.Errorf("git sync auth type must be 'token' or 'ssh' (got: %q)", c.GitSync.Auth.Type)
	}

	if c.GitSync.Auth.Type == "ssh" && c.GitSync.Auth.SSHKeyPath == "" {
		return fmt.Errorf("git sync SSH auth requires ssh_key_path to be set")
	}
	if c.GitSync.AutoSync.Interval < 0 {
		return fmt.Errorf("git sync auto_sync.interval must be non-negative (got: %d)", c.GitSync.AutoSync.Interval)
	}
	return nil
}

// validateTunnel validates the tunnel configuration.
func (c *Config) validateTunnel() error {
	if !c.Tunnel.Enabled {
		return nil
	}
	// Public tunnels (Tailscale Funnel) require authentication
	if c.Tunnel.Tailscale.Funnel && c.Server.Auth.Mode == AuthModeNone {
		return fmt.Errorf("tunnel with public access requires authentication; set server.auth.mode=builtin or disable tailscale funnel for private access")
	}
	return c.validateTunnelRateLimiting()
}

// validateTunnelRateLimiting validates rate limiting configuration for tunnels.
func (c *Config) validateTunnelRateLimiting() error {
	rl := c.Tunnel.RateLimiting
	if !rl.Enabled {
		return nil
	}
	if rl.LoginAttempts <= 0 {
		return fmt.Errorf("tunnel rate limiting login_attempts must be positive")
	}
	if rl.WindowSeconds <= 0 {
		return fmt.Errorf("tunnel rate limiting window_seconds must be positive")
	}
	if rl.BlockDurationSeconds <= 0 {
		return fmt.Errorf("tunnel rate limiting block_duration_seconds must be positive")
	}
	return nil
}

// IsTunnelPublic returns true if the tunnel exposes the service to the public internet.
func (c *Config) IsTunnelPublic() bool {
	return c.Tunnel.Enabled && c.Tunnel.Tailscale.Funnel
}

// validateRemoteNodes validates the remote node configuration.
func (c *Config) validateRemoteNodes() error {
	for i, n := range c.Server.RemoteNodes {
		if n.Name == "" {
			continue
		}
		if n.APIBaseURL == "" {
			return fmt.Errorf("remote_nodes[%d] (%q): api_base_url is required", i, n.Name)
		}
		switch n.AuthType {
		case "", "none", "basic", "token":
			// valid
		default:
			return fmt.Errorf("remote_nodes[%d] (%q): invalid auth_type %q (must be one of: none, basic, token)", i, n.Name, n.AuthType)
		}
		if n.AuthType == "basic" {
			if n.BasicAuthUsername == "" || n.BasicAuthPassword == "" {
				return fmt.Errorf("remote_nodes[%d] (%q): basic auth requires both basic_auth_username and basic_auth_password", i, n.Name)
			}
		}
		if n.AuthType == "token" {
			if n.AuthToken == "" {
				return fmt.Errorf("remote_nodes[%d] (%q): token auth requires auth_token", i, n.Name)
			}
		}
		if n.Timeout < 0 {
			return fmt.Errorf("remote_nodes[%d] (%q): timeout must not be negative", i, n.Name)
		}
	}
	return nil
}

// validateLicense validates the license configuration.
func (c *Config) validateLicense() error {
	if c.License.CloudURL != "" {
		u, err := url.Parse(c.License.CloudURL)
		if err != nil {
			return fmt.Errorf("invalid license cloud URL: %w", err)
		}
		if u.Scheme == "" || u.Host == "" {
			return fmt.Errorf("license cloud URL must include scheme and host (e.g., https://cloud.example.com)")
		}
		if u.Scheme != "https" {
			return fmt.Errorf("license cloud URL must use HTTPS")
		}
	}
	return nil
}
