// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"fmt"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/dagucloud/dagu/v2/internal/auth"
	"github.com/dagucloud/dagu/v2/internal/workspace"
)

// Config holds the overall configuration for the application.
type Config struct {
	Core            Core
	OpenCode        OpenCodeConfig
	Server          Server
	EventStore      EventStoreConfig
	Webhooks        WebhooksConfig
	Paths           PathsConfig
	DAGDiscovery    DAGDiscoveryConfig
	Secrets         SecretsConfig
	UI              UI
	Queues          Queues
	Coordinator     Coordinator
	Worker          Worker
	Proc            Proc
	Scheduler       Scheduler
	Monitoring      MonitoringConfig
	DefaultExecMode ExecutionMode
	Cache           CacheMode
	GitSync         GitSyncConfig
	Tunnel          TunnelConfig
	License         LicenseConfig
	Notices         []string
	Warnings        []string
}

// OpenCodeConfig configures the process-local managed OpenCode service.
type OpenCodeConfig struct {
	Executable     string
	EnvPassthrough []string
}

// DAGDiscoveryConfig controls how DAG definitions are discovered.
type DAGDiscoveryConfig struct {
	Recursive bool
	Symlinks  bool
}

const DefaultWebhookMaxPayloadSize = 1 * 1024 * 1024

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
	PublicURL         string // Absolute external URL used in generated links
	BasePath          string // URL path for reverse proxy subpath hosting
	APIBasePath       string
	Headless          bool
	CheckUpdates      bool
	AccessLog         AccessLogMode // "all", "non-public", or "none" (default)
	LatestStatusToday bool
	TLS               *TLSConfig
	Auth              Auth
	RemoteNodes       []RemoteNode
	Permissions       map[Permission]bool
	StrictValidation  bool
	// CORSAllowedOrigins lists origins allowed to make cross-origin requests.
	// An empty list disables CORS. A literal wildcard explicitly allows every
	// origin without credentials; exact origins enable credentials.
	CORSAllowedOrigins []string
	Metrics            MetricsAccess // "private" or "public"
	Terminal           TerminalConfig
	Audit              AuditConfig
	SSE                SSEConfig
	IPAccess           IPAccessConfig
}

// IPAccessConfig restricts HTTP access by client network address.
type IPAccessConfig struct {
	AllowedIPs     []string
	TrustedProxies []string
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

// WebhooksConfig contains configuration for webhook trigger endpoints.
type WebhooksConfig struct {
	MaxPayloadSize int // Default: 1MiB
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
	Proxy   AuthTrustedProxy
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

const maxBuiltinAuthTokenTTL = 365 * 24 * time.Hour

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

// OIDCPolicy contains the OIDC settings evaluated for each login.
type OIDCPolicy struct {
	AutoSignup     bool
	AllowedDomains []string
	Whitelist      []string
	RoleMapping    OIDCRoleMapping
}

// Policy returns the login-time policy from the OIDC configuration.
func (o AuthOIDC) Policy() OIDCPolicy {
	return OIDCPolicy{
		AutoSignup:     o.AutoSignup,
		AllowedDomains: o.AllowedDomains,
		Whitelist:      o.Whitelist,
		RoleMapping:    o.RoleMapping,
	}
}

// IsConfigured returns true if all required OIDC fields are set.
func (o AuthOIDC) IsConfigured() bool {
	return o.ClientID != "" && o.ClientSecret != "" && o.ClientURL != "" && o.Issuer != ""
}

// OIDCRoleMapping defines how OIDC claims are mapped to Dagu roles.
type OIDCRoleMapping struct {
	DefaultRole            string                          // Default: "viewer"
	GroupsClaim            string                          // Default: "groups"
	GroupMappings          map[string]string               // IdP group -> Dagu role
	WorkspaceMappings      map[string][]OIDCWorkspaceGrant // IdP group -> workspace grants
	DefaultWorkspaceAccess string                          // Default: "all"; required with workspace mappings
	RoleAttributePath      string                          // jq expression for role extraction
	RoleAttributeStrict    bool                            // Deny login if no global or workspace mapping matches
	SkipOrgRoleSync        bool                            // Keep first-login role and workspace access assignments
}

// OIDCWorkspaceGrant assigns an OIDC group member a role in one workspace.
type OIDCWorkspaceGrant struct {
	Workspace string `mapstructure:"workspace" json:"workspace"`
	Role      string `mapstructure:"role" json:"role"`
}

const (
	// OIDCDefaultWorkspaceAccessAll grants unmatched users access to every workspace.
	OIDCDefaultWorkspaceAccessAll = "all"
	// OIDCDefaultWorkspaceAccessNone denies unmatched users access to named workspaces.
	OIDCDefaultWorkspaceAccessNone = "none"
)

// WorkspaceAccessPolicyActive reports whether OIDC login manages workspace access.
func (m OIDCRoleMapping) WorkspaceAccessPolicyActive() bool {
	return len(m.WorkspaceMappings) > 0 || m.DefaultWorkspaceAccess == OIDCDefaultWorkspaceAccessNone
}

// AuthTrustedProxy configures authentication delegated to an authenticating reverse proxy.
type AuthTrustedProxy struct {
	Enabled     bool
	Source      string
	ButtonLabel string
	Headers     TrustedProxyHeaders
	AutoSignup  bool
	RoleMapping TrustedProxyRoleMapping
}

// TrustedProxyHeaders identifies the headers populated by the authenticating proxy.
type TrustedProxyHeaders struct {
	User   string
	Groups string
}

// TrustedProxyRoleMapping defines how proxy groups map to Dagu authorization.
type TrustedProxyRoleMapping struct {
	DefaultRole            string
	GroupMappings          map[string]string
	WorkspaceMappings      map[string][]TrustedProxyWorkspaceGrant
	DefaultWorkspaceAccess string
	RequireMapping         bool
	SkipOrgRoleSync        bool
}

// TrustedProxyWorkspaceGrant assigns a proxy group member a role in one workspace.
type TrustedProxyWorkspaceGrant struct {
	Workspace string `mapstructure:"workspace" json:"workspace" yaml:"workspace"`
	Role      string `mapstructure:"role" json:"role" yaml:"role"`
}

const (
	// TrustedProxyDefaultWorkspaceAccessAll grants unmatched users access to every workspace.
	TrustedProxyDefaultWorkspaceAccessAll = "all"
	// TrustedProxyDefaultWorkspaceAccessNone denies unmatched users access to named workspaces.
	TrustedProxyDefaultWorkspaceAccessNone = "none"
)

// PathsConfig represents the file system paths configuration.
type PathsConfig struct {
	DAGsDir            string
	WikiDir            string
	WikiDirLegacy      bool
	Executable         string
	LogDir             string
	ArtifactDir        string
	DAGStateDir        string
	DataDir            string
	ToolsDir           string
	SuspendFlagsDir    string
	AdminLogsDir       string
	EventStoreDir      string
	BaseConfig         string
	AltDAGsDir         string
	DAGRunsDir         string
	DAGRunWorkDir      string
	QueueDir           string
	ProcDir            string
	ServiceRegistryDir string
	UsersDir           string
	APIKeysDir         string
	WebhooksDir        string
	ContextsDir        string
	RemoteNodesDir     string
	WorkspacesDir      string
	ViewsDir           string
	ConfigFileUsed     string
	ConfigFilesUsed    []string
}

// SecretsConfig holds global defaults for external secret providers.
type SecretsConfig struct {
	Vault      VaultSecretsConfig
	Kubernetes KubernetesSecretsConfig
	AWS        AWSSecretsConfig
	GCP        GCPSecretsConfig
	Azure      AzureSecretsConfig
	Alibaba    AlibabaSecretsConfig
}

// AWSSecretsConfig holds shared AWS Secrets Manager client defaults.
type AWSSecretsConfig struct {
	Region string
}

// GCPSecretsConfig holds shared GCP Secret Manager client defaults.
type GCPSecretsConfig struct {
	ProjectID string
	Location  string
}

// AzureSecretsConfig holds shared Azure Key Vault client defaults.
type AzureSecretsConfig struct {
	VaultURL string
}

// AlibabaSecretsConfig holds shared Alibaba Cloud KMS client defaults.
type AlibabaSecretsConfig struct {
	Region   string
	Endpoint string
	CAFile   string
}

// VaultSecretsConfig holds shared HashiCorp Vault client defaults.
type VaultSecretsConfig struct {
	Address    string
	Token      string
	CACert     string
	ClientCert string
	ClientKey  string
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
	HeartbeatInterval time.Duration // Default: 5s
	StaleThreshold    time.Duration // Default: 90s
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

// PostgresPoolConfig holds PostgreSQL connection pool settings for workers.
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
	if err := c.validateOpenCode(); err != nil {
		return err
	}
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
	if err := c.validateTrustedProxyAuth(); err != nil {
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
	if err := c.validateWebhooks(); err != nil {
		return err
	}
	return nil
}

func (c *Config) validateOpenCode() error {
	for _, key := range c.OpenCode.EnvPassthrough {
		normalized := strings.ToUpper(strings.TrimSpace(key))
		if normalized == "OPENCODE_SERVER_USERNAME" || normalized == "OPENCODE_SERVER_PASSWORD" || strings.HasPrefix(normalized, "_DAGU_INTERNAL_") {
			return fmt.Errorf("opencode.env_passthrough must not include reserved variable %q", key)
		}
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

func (c *Config) validateWebhooks() error {
	if c.Webhooks.MaxPayloadSize <= 0 {
		return fmt.Errorf("webhooks.max_payload_size must be > 0")
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
	return nil
}

// validateServer validates server-related configuration.
func (c *Config) validateServer() error {
	if c.Server.Port < 0 || c.Server.Port > 65535 {
		return fmt.Errorf("invalid port number: %d", c.Server.Port)
	}
	if c.Server.PublicURL != "" {
		normalized, err := NormalizePublicURL(c.Server.PublicURL)
		if err != nil {
			return err
		}
		c.Server.PublicURL = normalized
	}
	if err := validateIPAccessEntries("ip_access.allowed_ips", c.Server.IPAccess.AllowedIPs); err != nil {
		return err
	}
	if err := validateIPAccessEntries("ip_access.trusted_proxies", c.Server.IPAccess.TrustedProxies); err != nil {
		return err
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

func validateIPAccessEntries(path string, entries []string) error {
	for i, entry := range entries {
		var err error
		if strings.Contains(entry, "/") {
			prefix, parseErr := netip.ParsePrefix(entry)
			err = parseErr
			if err == nil && prefix.Addr().Is4In6() && prefix.Bits() < 96 {
				err = fmt.Errorf("mapped IPv4 prefix length must be at least 96")
			}
		} else {
			_, err = netip.ParseAddr(entry)
		}
		if err != nil {
			return fmt.Errorf("invalid %s[%d] %q: %w", path, i, entry, err)
		}
	}
	return nil
}

// NormalizePublicURL validates and normalizes the externally reachable UI URL.
func NormalizePublicURL(rawURL string) (string, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "", nil
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("invalid public_url: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("public_url must use http or https")
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("public_url must include a host")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("public_url must not include query or fragment")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return parsed.String(), nil
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
	if c.Server.Auth.Builtin.Token.TTL > maxBuiltinAuthTokenTTL {
		return fmt.Errorf("builtin auth token TTL must not exceed 8760h (365 days)")
	}

	// Validate initial_admin: both fields must be set, or neither.
	ia := c.Server.Auth.Builtin.InitialAdmin
	if (ia.Username == "") != (ia.Password == "") {
		return fmt.Errorf("auth.builtin.initial_admin requires both username and password to be set, or neither")
	}
	if err := validateOIDCWorkspaceMappings(c.Server.Auth.OIDC.RoleMapping); err != nil {
		return err
	}

	if c.Server.Auth.OIDC.IsConfigured() {
		return c.validateOIDCForBuiltin()
	}
	return nil
}

// validateOIDCForBuiltin validates OIDC configuration under builtin auth mode.
func (c *Config) validateOIDCForBuiltin() error {
	oidc := c.Server.Auth.OIDC

	if _, err := auth.ParseRole(oidc.RoleMapping.DefaultRole); err != nil {
		return fmt.Errorf("OIDC roleMapping.defaultRole: %w", err)
	}

	if !slices.Contains(oidc.Scopes, "email") {
		if len(oidc.Whitelist) > 0 || len(oidc.AllowedDomains) > 0 {
			return fmt.Errorf("OIDC scopes must include 'email' when whitelist or allowedDomains is configured")
		}
		c.Warnings = append(c.Warnings, "OIDC scopes do not include 'email'; access control features will not work if added later")
	}

	return nil
}

func validateOIDCWorkspaceMappings(mapping OIDCRoleMapping) error {
	if mapping.DefaultWorkspaceAccess == "" && len(mapping.WorkspaceMappings) > 0 {
		return fmt.Errorf(
			"OIDC roleMapping.defaultWorkspaceAccess must be explicitly set to all or none when workspaceMappings is configured",
		)
	}

	switch mapping.DefaultWorkspaceAccess {
	case "", OIDCDefaultWorkspaceAccessAll, OIDCDefaultWorkspaceAccessNone:
	default:
		return fmt.Errorf(
			"OIDC roleMapping.defaultWorkspaceAccess must be one of: all, none (got: %q)",
			mapping.DefaultWorkspaceAccess,
		)
	}

	groups := make([]string, 0, len(mapping.WorkspaceMappings))
	for group := range mapping.WorkspaceMappings {
		groups = append(groups, group)
	}
	slices.Sort(groups)

	for _, group := range groups {
		if strings.TrimSpace(group) == "" {
			return fmt.Errorf("OIDC roleMapping.workspaceMappings contains a blank group name")
		}

		grants := mapping.WorkspaceMappings[group]
		if len(grants) == 0 {
			return fmt.Errorf("OIDC roleMapping.workspaceMappings[%q] must contain at least one grant", group)
		}

		seenWorkspaces := make(map[string]struct{}, len(grants))
		for i, grant := range grants {
			if err := workspace.ValidateName(grant.Workspace); err != nil {
				return fmt.Errorf(
					"OIDC roleMapping.workspaceMappings[%q][%d].workspace %q is invalid: %w",
					group, i, grant.Workspace, err,
				)
			}
			if _, exists := seenWorkspaces[grant.Workspace]; exists {
				return fmt.Errorf(
					"OIDC roleMapping.workspaceMappings[%q] contains duplicate workspace %q",
					group, grant.Workspace,
				)
			}
			seenWorkspaces[grant.Workspace] = struct{}{}

			role, err := auth.ParseRole(grant.Role)
			if err != nil {
				return fmt.Errorf(
					"OIDC roleMapping.workspaceMappings[%q][%d].role: %w",
					group, i, err,
				)
			}
			if role == auth.RoleAdmin {
				return fmt.Errorf(
					"OIDC roleMapping.workspaceMappings[%q][%d].role must not be admin",
					group, i,
				)
			}
		}
	}

	return nil
}

func (c *Config) validateTrustedProxyAuth() error {
	trustedProxy := c.Server.Auth.Proxy
	if err := validateTrustedProxySource(trustedProxy.Source); err != nil {
		return err
	}
	if !trustedProxy.Enabled {
		return nil
	}
	if c.Server.Auth.Mode != AuthModeBuiltin {
		return fmt.Errorf("auth.proxy.enabled requires auth.mode to be builtin")
	}
	if c.Server.Headless {
		return fmt.Errorf("auth.proxy.enabled is not supported when headless is true")
	}
	if c.Tunnel.Enabled {
		return fmt.Errorf("auth.proxy.enabled is not supported when tunnel.enabled is true")
	}
	if err := validateTrustedProxyHeaderName("auth.proxy.headers.user", trustedProxy.Headers.User); err != nil {
		return err
	}
	hasMappings := len(trustedProxy.RoleMapping.GroupMappings) > 0 || len(trustedProxy.RoleMapping.WorkspaceMappings) > 0
	if trustedProxy.RoleMapping.RequireMapping && !hasMappings {
		return fmt.Errorf("auth.proxy.role_mapping.require_mapping requires at least one group_mappings or workspace_mappings entry")
	}
	if hasMappings {
		if trustedProxy.Headers.Groups == "" {
			return fmt.Errorf("auth.proxy.headers.groups is required when role mappings are configured")
		}
	}
	if trustedProxy.Headers.Groups != "" {
		if err := validateTrustedProxyHeaderName("auth.proxy.headers.groups", trustedProxy.Headers.Groups); err != nil {
			return err
		}
		if strings.EqualFold(trustedProxy.Headers.User, trustedProxy.Headers.Groups) {
			return fmt.Errorf("auth.proxy.headers.user and auth.proxy.headers.groups must be different")
		}
	}
	if err := validateProxyButtonLabel(trustedProxy.ButtonLabel); err != nil {
		return err
	}
	return validateTrustedProxyRoleMapping(trustedProxy.RoleMapping)
}

func validateTrustedProxySource(source string) error {
	const maxSourceRunes = 128
	if source == "" {
		return nil
	}
	if !utf8.ValidString(source) {
		return fmt.Errorf("auth.proxy.source must be valid UTF-8")
	}
	if strings.TrimSpace(source) != source {
		return fmt.Errorf("auth.proxy.source must not have surrounding whitespace")
	}
	if utf8.RuneCountInString(source) > maxSourceRunes {
		return fmt.Errorf("auth.proxy.source must not exceed %d characters", maxSourceRunes)
	}
	for _, r := range source {
		if unicode.IsControl(r) {
			return fmt.Errorf("auth.proxy.source must not contain control characters")
		}
	}
	return nil
}

func validateTrustedProxyHeaderName(path, name string) error {
	if name == "" {
		return fmt.Errorf("%s is required", path)
	}
	if !isHTTPFieldName(name) {
		return fmt.Errorf("%s must be a valid HTTP header field name", path)
	}
	switch {
	case strings.EqualFold(name, "Authorization"),
		strings.EqualFold(name, "Cookie"),
		strings.EqualFold(name, "Host"):
		return fmt.Errorf("%s must not use the reserved header %q", path, name)
	default:
		return nil
	}
}

func isHTTPFieldName(name string) bool {
	if name == "" {
		return false
	}
	for i := range len(name) {
		c := name[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			continue
		}
		switch c {
		case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
			continue
		default:
			return false
		}
	}
	return true
}

func validateProxyButtonLabel(label string) error {
	const maxButtonLabelRunes = 128
	if strings.TrimSpace(label) == "" {
		return fmt.Errorf("auth.proxy.button_label must not be empty")
	}
	if !utf8.ValidString(label) {
		return fmt.Errorf("auth.proxy.button_label must be valid UTF-8")
	}
	if utf8.RuneCountInString(label) > maxButtonLabelRunes {
		return fmt.Errorf("auth.proxy.button_label must not exceed %d characters", maxButtonLabelRunes)
	}
	for _, r := range label {
		if unicode.IsControl(r) {
			return fmt.Errorf("auth.proxy.button_label must not contain control characters")
		}
	}
	return nil
}

func validateTrustedProxyRoleMapping(mapping TrustedProxyRoleMapping) error {
	if _, err := auth.ParseRole(mapping.DefaultRole); err != nil {
		return fmt.Errorf("auth.proxy.role_mapping.default_role: %w", err)
	}
	switch mapping.DefaultWorkspaceAccess {
	case TrustedProxyDefaultWorkspaceAccessAll, TrustedProxyDefaultWorkspaceAccessNone:
	default:
		return fmt.Errorf(
			"auth.proxy.role_mapping.default_workspace_access must be one of: all, none (got: %q)",
			mapping.DefaultWorkspaceAccess,
		)
	}

	groups := make([]string, 0, len(mapping.GroupMappings))
	for group := range mapping.GroupMappings {
		groups = append(groups, group)
	}
	slices.Sort(groups)
	for _, group := range groups {
		path := fmt.Sprintf("auth.proxy.role_mapping.group_mappings[%q]", group)
		if err := validateTrustedProxyMappingGroup(path, group); err != nil {
			return err
		}
		if _, err := auth.ParseRole(mapping.GroupMappings[group]); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	}

	groups = groups[:0]
	for group := range mapping.WorkspaceMappings {
		groups = append(groups, group)
	}
	slices.Sort(groups)
	for _, group := range groups {
		path := fmt.Sprintf("auth.proxy.role_mapping.workspace_mappings[%q]", group)
		if err := validateTrustedProxyMappingGroup(path, group); err != nil {
			return err
		}
		grants := mapping.WorkspaceMappings[group]
		if len(grants) == 0 {
			return fmt.Errorf("%s must contain at least one grant", path)
		}
		seenWorkspaces := make(map[string]struct{}, len(grants))
		for i, grant := range grants {
			grantPath := fmt.Sprintf("%s[%d]", path, i)
			if err := workspace.ValidateName(grant.Workspace); err != nil {
				return fmt.Errorf("%s.workspace %q is invalid: %w", grantPath, grant.Workspace, err)
			}
			if _, exists := seenWorkspaces[grant.Workspace]; exists {
				return fmt.Errorf("%s contains duplicate workspace %q", path, grant.Workspace)
			}
			seenWorkspaces[grant.Workspace] = struct{}{}
			role, err := auth.ParseRole(grant.Role)
			if err != nil {
				return fmt.Errorf("%s.role: %w", grantPath, err)
			}
			if role == auth.RoleAdmin {
				return fmt.Errorf("%s.role must not be admin", grantPath)
			}
		}
	}

	return nil
}

func validateTrustedProxyMappingGroup(path, group string) error {
	if group == "" {
		return fmt.Errorf("%s must not use an empty group name", path)
	}
	if strings.Trim(group, " \t") != group {
		return fmt.Errorf("%s group name must not have surrounding whitespace", path)
	}
	if !utf8.ValidString(group) {
		return fmt.Errorf("%s group name must be valid UTF-8", path)
	}
	if len(group) > 512 {
		return fmt.Errorf("%s group name must not exceed 512 bytes", path)
	}
	for _, r := range group {
		if unicode.IsControl(r) {
			return fmt.Errorf("%s group name must not contain control characters", path)
		}
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
		if err := validateRemoteNodeAPIBaseURL(n.APIBaseURL); err != nil {
			return fmt.Errorf("remote_nodes[%d] (%q): %w", i, n.Name, err)
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

func validateRemoteNodeAPIBaseURL(rawURL string) error {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return fmt.Errorf("invalid api_base_url: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("api_base_url must use http or https")
	}
	if parsed.Host == "" {
		return fmt.Errorf("api_base_url must include a host")
	}
	if parsed.User != nil {
		return fmt.Errorf("api_base_url must not include credentials")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("api_base_url must not include query parameters or fragments")
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
