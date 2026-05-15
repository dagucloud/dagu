// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"fmt"
	"log"
	"net"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/adrg/xdg"
	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/spf13/viper"
)

// Service represents the type of service that is loading configuration.
// This allows the loader to only load configuration sections relevant to
// the specific service, improving performance and reducing unnecessary processing.
type Service int

const (
	// ServiceNone indicates no specific service - loads all configuration.
	// Use this for CLI commands or when full configuration is needed.
	ServiceNone Service = iota

	// ServiceServer is the web UI server service.
	// Requires: Core, Server, Paths, UI, Queues, Coordinator, Monitoring
	ServiceServer

	// ServiceScheduler is the scheduler service for automated DAG execution.
	// Requires: Core, Paths, Scheduler, Queues, Coordinator
	ServiceScheduler

	// ServiceWorker is the worker service that polls the coordinator for tasks.
	// Requires: Core, Paths, Worker, Coordinator (peer config)
	ServiceWorker

	// ServiceCoordinator is the coordinator gRPC server for distributed execution.
	// Requires: Core, Paths, Coordinator
	ServiceCoordinator

	// ServiceAgent is for the agent executor (runs DAGs).
	// Requires: Core, Paths, Queues (to check if distributed execution is enabled)
	ServiceAgent

	// ServiceBots is the bot service (Telegram, etc.).
	// Requires: Core, Paths, Bots config, and Proc for agent stores.
	ServiceBots
)

// ConfigLoader reads and merges configuration from various sources.
type ConfigLoader struct {
	v                 *viper.Viper
	configFile        string
	notices           []string
	warnings          []string
	additionalBaseEnv []string
	appHomeDir        string
	service           Service
}

// ConfigLoaderOption defines a functional option for configuring a ConfigLoader.
type ConfigLoaderOption func(*ConfigLoader)

// WithConfigFile returns a ConfigLoaderOption that sets the configuration file path.
func WithConfigFile(configFile string) ConfigLoaderOption {
	return func(l *ConfigLoader) {
		l.configFile = configFile
	}
}

// WithAppHomeDir returns a ConfigLoaderOption that sets the application home directory
// used by the ConfigLoader, overriding the default DAGU_HOME resolution.
func WithAppHomeDir(dir string) ConfigLoaderOption {
	return func(l *ConfigLoader) {
		l.appHomeDir = dir
	}
}

// WithService sets the service type, determining which configuration sections to load.
func WithService(service Service) ConfigLoaderOption {
	return func(l *ConfigLoader) {
		l.service = service
	}
}

// ConfigSection represents a specific section of the configuration using bit flags.
type ConfigSection uint16

const (
	// SectionNone means "always load regardless of service type".
	// Use this for settings that apply universally across all services.
	SectionNone        ConfigSection = 0
	SectionServer      ConfigSection = 1 << iota // 2
	SectionScheduler                             // 4
	SectionWorker                                // 8
	SectionCoordinator                           // 16
	SectionUI                                    // 32
	SectionQueues                                // 64
	SectionMonitoring                            // 128
	SectionGitSync                               // 256
	SectionTunnel                                // 512
	SectionLicense                               // 1024
	SectionProc                                  // 2048
	SectionBots                                  // 4096

	// SectionAll combines all sections (useful for ServiceNone/CLI)
	SectionAll = SectionServer | SectionScheduler | SectionWorker | SectionCoordinator | SectionUI | SectionQueues | SectionMonitoring | SectionGitSync | SectionTunnel | SectionLicense | SectionProc | SectionBots
)

// serviceRequirements maps services to their required config sections using bitwise OR.
var serviceRequirements = map[Service]ConfigSection{
	ServiceNone:        SectionAll,
	ServiceServer:      SectionServer | SectionCoordinator | SectionUI | SectionQueues | SectionMonitoring | SectionGitSync | SectionTunnel | SectionLicense | SectionProc,
	ServiceScheduler:   SectionScheduler | SectionCoordinator | SectionQueues | SectionProc,
	ServiceWorker:      SectionWorker | SectionCoordinator | SectionProc,
	ServiceCoordinator: SectionCoordinator | SectionProc,
	ServiceAgent:       SectionQueues | SectionProc,
	ServiceBots:        SectionBots | SectionProc,
}

// requires checks if the loader's service requires the given config section.
func (l *ConfigLoader) requires(section ConfigSection) bool {
	req, ok := serviceRequirements[l.service]
	if !ok {
		req = SectionAll
	}
	return req&section != 0
}

// resolvePath resolves a path to an absolute path. Empty paths are returned as-is.
func (l *ConfigLoader) resolvePath(fieldName, pathValue string) (string, error) {
	if pathValue == "" {
		return "", nil
	}
	resolved, err := fileutil.ResolvePath(pathValue)
	if err != nil {
		return "", fmt.Errorf("failed to resolve %s path %q: %w", fieldName, pathValue, err)
	}
	return resolved, nil
}

// parseDuration parses a duration string, returning zero and adding a warning if invalid.
func (l *ConfigLoader) parseDuration(fieldName, value string) time.Duration {
	if value == "" {
		return 0
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		l.warnings = append(l.warnings, fmt.Sprintf("Invalid %s value: %s", fieldName, value))
		return 0
	}
	return duration
}

// NewConfigLoader creates a ConfigLoader with the given viper instance and options.
func NewConfigLoader(v *viper.Viper, options ...ConfigLoaderOption) *ConfigLoader {
	loader := &ConfigLoader{v: v}
	for _, opt := range options {
		opt(loader)
	}
	return loader
}

// Load reads configuration files, applies defaults and environment overrides,
// and returns a validated Config instance.
func (l *ConfigLoader) Load() (*Config, error) {
	homeDir, err := getHomeDir()
	if err != nil {
		return nil, err
	}

	xdgConfig := XDGConfig{
		DataHome:   xdg.DataHome,
		ConfigHome: filepath.Join(homeDir, ".config"),
	}

	if l.appHomeDir != "" {
		l.additionalBaseEnv = append(l.additionalBaseEnv, fmt.Sprintf("DAGU_HOME=%s", fileutil.ResolvePathOrBlank(l.appHomeDir)))
	}

	notices, err := l.setupViper(xdgConfig, homeDir, l.configFile, l.appHomeDir)
	if err != nil {
		return nil, err
	}
	l.notices = append(l.notices, notices...)

	if err := l.v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("failed to read config: %w", err)
		}
	}

	if err := checkForLegacyKeys(l.v); err != nil {
		return nil, err
	}

	configFileUsed, err := l.resolvePath("config file", l.v.ConfigFileUsed())
	if err != nil {
		return nil, err
	}

	// Merge legacy admin.yaml for backward compatibility
	l.v.SetConfigName("admin")
	if err := l.v.MergeInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("failed to read admin config: %w", err)
		}
	}

	var def Definition
	if err := l.v.Unmarshal(&def); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	cfg, err := l.buildConfig(def)
	if err != nil {
		return nil, fmt.Errorf("failed to build config: %w", err)
	}

	cfg.Paths.ConfigFileUsed = configFileUsed
	l.finalizeBaseEnv(cfg)
	cfg.Notices = l.notices
	cfg.Warnings = l.warnings

	return cfg, nil
}

// buildConfig transforms the Definition into a validated Config structure,
// loading only sections required by the configured service.
func (l *ConfigLoader) buildConfig(def Definition) (*Config, error) {
	cfg := Config{
		UI: UI{MaxDashboardPageLimit: 1},
		Server: Server{
			Port: 8080,
			Auth: Auth{Mode: AuthModeNone},
			Terminal: TerminalConfig{
				MaxSessions: 5,
			},
		},
		Webhooks: WebhooksConfig{
			MaxPayloadSize: DefaultWebhookMaxPayloadSize,
		},
	}

	if err := l.loadCoreConfig(&cfg, def); err != nil {
		return nil, err
	}
	if err := l.loadPathsConfig(&cfg, def); err != nil {
		return nil, err
	}
	l.loadSecretsConfig(&cfg, def)
	l.loadEventStoreConfig(&cfg, def)

	// Load service-specific sections
	sectionLoaders := []struct {
		section ConfigSection
		load    func()
	}{
		{SectionServer, func() { l.loadServerConfig(&cfg, def) }},
		{SectionUI, func() { l.loadUIConfig(&cfg, def) }},
		{SectionQueues, func() { l.loadQueuesConfig(&cfg, def) }},
		{SectionCoordinator, func() { l.loadCoordinatorConfig(&cfg, def) }},
		{SectionWorker, func() { l.loadWorkerConfig(&cfg, def) }},
		{SectionProc, func() { l.loadProcConfig(&cfg, def) }},
		{SectionScheduler, func() { l.loadSchedulerConfig(&cfg, def) }},
		{SectionMonitoring, func() { l.loadMonitoringConfig(&cfg, def) }},
		{SectionGitSync, func() { l.loadGitSyncConfig(&cfg, def) }},
		{SectionTunnel, func() { l.loadTunnelConfig(&cfg, def) }},
		{SectionBots, func() { l.loadBotsConfig(&cfg, def) }},
		{SectionLicense, func() { l.loadLicenseConfig(&cfg, def) }},
	}

	for _, sl := range sectionLoaders {
		if l.requires(sl.section) {
			sl.load()
		}
	}

	l.loadCacheConfig(&cfg, def)
	l.loadWebhooksConfig(&cfg, def)
	l.loadExecutionModeConfig(&cfg, def)

	if err := l.LoadLegacyFields(&cfg, def); err != nil {
		return nil, err
	}
	l.loadLegacyEnv(&cfg)
	l.finalizePaths(&cfg)

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func (l *ConfigLoader) loadCoreConfig(cfg *Config, def Definition) error {
	envPassthrough := normalizeEnvEntries(parseStringList(l.v.Get("env_passthrough")))
	envPassthroughPrefixes := normalizeEnvEntries(parseStringList(l.v.Get("env_passthrough_prefixes")))

	baseEnv := LoadBaseEnvWithExtras(envPassthrough, envPassthroughPrefixes)
	baseEnv.variables = append(baseEnv.variables, l.additionalBaseEnv...)

	cfg.Core = Core{
		Debug:                  def.Debug,
		LogFormat:              def.LogFormat,
		TZ:                     def.TZ,
		DefaultShell:           def.DefaultShell,
		SkipExamples:           l.v.GetBool("skip_examples"),
		EnvPassthrough:         envPassthrough,
		EnvPassthroughPrefixes: envPassthroughPrefixes,
		BaseEnv:                baseEnv,
		Peer:                   l.loadPeerConfig(def.Peer),
	}

	if err := setTimezone(&cfg.Core); err != nil {
		return fmt.Errorf("failed to set timezone: %w", err)
	}
	if def.TZ != "" {
		// Only propagate an explicit timezone configuration to child
		// processes. The implicit local-time cfg.TZ value is a legacy
		// display string and must not overwrite an inherited TZ env var.
		baseEnv.variables = withEnvOverrides(baseEnv.variables, fmt.Sprintf("TZ=%s", cfg.Core.TZ))
		cfg.Core.BaseEnv = baseEnv
	}

	return nil
}

func (l *ConfigLoader) finalizeBaseEnv(cfg *Config) {
	if cfg == nil {
		return
	}

	overrides := append([]string{}, l.additionalBaseEnv...)
	if cfg.Paths.ConfigFileUsed != "" {
		overrides = append(overrides, fmt.Sprintf("DAGU_CONFIG=%s", cfg.Paths.ConfigFileUsed))
	}
	if cfg.Paths.Executable != "" {
		overrides = append(overrides, fmt.Sprintf("DAGU_EXECUTABLE=%s", cfg.Paths.Executable))
	}
	if cfg.Core.DefaultShell != "" {
		overrides = append(overrides, fmt.Sprintf("SHELL=%s", cfg.Core.DefaultShell))
	}

	cfg.Core.BaseEnv.variables = withEnvOverrides(cfg.Core.BaseEnv.variables, overrides...)
}

func (l *ConfigLoader) loadPeerConfig(def PeerDef) Peer {
	return Peer{
		CertFile:      def.CertFile,
		KeyFile:       def.KeyFile,
		ClientCaFile:  def.ClientCaFile,
		SkipTLSVerify: def.SkipTLSVerify,
		Insecure:      def.Insecure,
		MaxRetries:    def.MaxRetries,
		RetryInterval: l.parseDuration("peer.retry_interval", def.RetryInterval),
	}
}

// loadPathsConfig resolves all file system paths to absolute paths.
func (l *ConfigLoader) loadPathsConfig(cfg *Config, def Definition) error {
	if def.Paths == nil {
		return nil
	}

	pathMappings := []struct {
		name   string
		target *string
		source string
	}{
		{"DAGsDir", &cfg.Paths.DAGsDir, def.Paths.DAGsDir},
		{"DocsDir", &cfg.Paths.DocsDir, def.Paths.DocsDir},
		{"AltDAGsDir", &cfg.Paths.AltDAGsDir, def.Paths.AltDagsDir},
		{"SuspendFlagsDir", &cfg.Paths.SuspendFlagsDir, def.Paths.SuspendFlagsDir},
		{"DataDir", &cfg.Paths.DataDir, def.Paths.DataDir},
		{"ToolsDir", &cfg.Paths.ToolsDir, def.Paths.ToolsDir},
		{"LogDir", &cfg.Paths.LogDir, def.Paths.LogDir},
		{"ArtifactDir", &cfg.Paths.ArtifactDir, def.Paths.ArtifactDir},
		{"AdminLogsDir", &cfg.Paths.AdminLogsDir, def.Paths.AdminLogsDir},
		{"EventStoreDir", &cfg.Paths.EventStoreDir, def.Paths.EventStoreDir},
		{"BaseConfig", &cfg.Paths.BaseConfig, def.Paths.BaseConfig},
		{"Executable", &cfg.Paths.Executable, def.Paths.Executable},
		{"DAGRunsDir", &cfg.Paths.DAGRunsDir, def.Paths.DAGRunsDir},
		{"QueueDir", &cfg.Paths.QueueDir, def.Paths.QueueDir},
		{"ProcDir", &cfg.Paths.ProcDir, def.Paths.ProcDir},
		{"ServiceRegistryDir", &cfg.Paths.ServiceRegistryDir, def.Paths.ServiceRegistryDir},
		{"UsersDir", &cfg.Paths.UsersDir, def.Paths.UsersDir},
		{"APIKeysDir", &cfg.Paths.APIKeysDir, def.Paths.APIKeysDir},
		{"WebhooksDir", &cfg.Paths.WebhooksDir, def.Paths.WebhooksDir},
		{"SessionsDir", &cfg.Paths.SessionsDir, def.Paths.SessionsDir},
		{"ContextsDir", &cfg.Paths.ContextsDir, def.Paths.ContextsDir},
		{"RemoteNodesDir", &cfg.Paths.RemoteNodesDir, def.Paths.RemoteNodesDir},
		{"WorkspacesDir", &cfg.Paths.WorkspacesDir, def.Paths.WorkspacesDir},
	}

	for _, m := range pathMappings {
		resolved, err := l.resolvePath(m.name, m.source)
		if err != nil {
			return err
		}
		*m.target = resolved
	}

	return nil
}

func (l *ConfigLoader) loadSecretsConfig(cfg *Config, def Definition) {
	if def.Secrets == nil {
		return
	}

	if def.Secrets.Vault != nil {
		cfg.Secrets.Vault = VaultSecretsConfig{
			Address: def.Secrets.Vault.Address,
			Token:   def.Secrets.Vault.Token,
		}
	}

	if def.Secrets.Kubernetes != nil {
		kubeconfig := def.Secrets.Kubernetes.Kubeconfig
		resolved, err := l.resolvePath("secrets.kubernetes.kubeconfig", kubeconfig)
		if err != nil {
			l.warnings = append(l.warnings, err.Error())
		} else {
			kubeconfig = resolved
		}

		cfg.Secrets.Kubernetes = KubernetesSecretsConfig{
			Namespace:  def.Secrets.Kubernetes.Namespace,
			Kubeconfig: kubeconfig,
			Context:    def.Secrets.Kubernetes.Context,
		}
	}
}

func (l *ConfigLoader) loadEventStoreConfig(cfg *Config, def Definition) {
	cfg.EventStore.Enabled = true
	if def.EventStore != nil && def.EventStore.Enabled != nil {
		cfg.EventStore.Enabled = *def.EventStore.Enabled
	}
	cfg.EventStore.RetentionDays = l.v.GetInt("event_store.retention_days")
	if def.EventStore != nil && def.EventStore.RetentionDays != nil {
		cfg.EventStore.RetentionDays = *def.EventStore.RetentionDays
	}
}

func (l *ConfigLoader) loadServerConfig(cfg *Config, def Definition) {
	cfg.Server = Server{
		Host:        def.Host,
		Port:        def.Port,
		BasePath:    def.BasePath,
		APIBasePath: def.APIBasePath,
		Permissions: map[Permission]bool{
			PermissionWriteDAGs: true,
			PermissionRunDAGs:   true,
		},
	}

	l.loadServerPermissions(cfg, def)
	l.loadServerRemoteNodes(cfg, def)
	l.loadServerFlags(cfg, def)
	l.loadServerTLS(cfg, def)
	l.loadServerAuth(cfg, def)
	l.loadServerDefaults(cfg, def)
}

func (l *ConfigLoader) loadServerPermissions(cfg *Config, def Definition) {
	if def.Permissions.WriteDAGs != nil {
		cfg.Server.Permissions[PermissionWriteDAGs] = *def.Permissions.WriteDAGs
	}
	if def.PermissionWriteDAGs != nil {
		cfg.Server.Permissions[PermissionWriteDAGs] = *def.PermissionWriteDAGs
	}
	if def.Permissions.RunDAGs != nil {
		cfg.Server.Permissions[PermissionRunDAGs] = *def.Permissions.RunDAGs
	}
	if def.PermissionRunDAGs != nil {
		cfg.Server.Permissions[PermissionRunDAGs] = *def.PermissionRunDAGs
	}
}

func (l *ConfigLoader) loadServerRemoteNodes(cfg *Config, def Definition) {
	for _, node := range def.RemoteNodes {
		cfg.Server.RemoteNodes = append(cfg.Server.RemoteNodes, RemoteNode(node))
	}
}

func (l *ConfigLoader) loadServerFlags(cfg *Config, def Definition) {
	if def.Headless != nil {
		cfg.Server.Headless = *def.Headless
	}
	cfg.Server.AccessLog = AccessLogAll
	if def.AccessLog != nil {
		switch AccessLogMode(*def.AccessLog) {
		case AccessLogAll, AccessLogNonPublic, AccessLogNone:
			cfg.Server.AccessLog = AccessLogMode(*def.AccessLog)
		default:
			l.warnings = append(l.warnings, fmt.Sprintf(
				"Invalid access_log_mode value: %q, defaulting to 'all'", *def.AccessLog))
		}
	}
	if def.LatestStatusToday != nil {
		cfg.Server.LatestStatusToday = *def.LatestStatusToday
	}
}

func (l *ConfigLoader) loadServerTLS(cfg *Config, def Definition) {
	if def.TLS != nil {
		cfg.Server.TLS = &TLSConfig{
			CertFile: def.TLS.CertFile,
			KeyFile:  def.TLS.KeyFile,
			CAFile:   def.TLS.CAFile,
		}
	}
}

func (l *ConfigLoader) loadServerAuth(cfg *Config, def Definition) {
	l.loadAuthMode(cfg, def)

	if def.Auth == nil {
		l.setAuthDefaults(cfg)
		return
	}

	l.loadBasicAuth(cfg, def.Auth)
	l.loadOIDCAuth(cfg, def.Auth)
	l.loadBuiltinAuth(cfg, def.Auth)
	l.setAuthDefaults(cfg)
}

func (l *ConfigLoader) loadAuthMode(cfg *Config, def Definition) {
	if def.Auth == nil || def.Auth.Mode == nil {
		cfg.Server.Auth.Mode = AuthModeBuiltin
		l.warnings = append(l.warnings, "No auth.mode configured — defaulting to 'builtin'. "+
			"Set auth.mode to 'none' to disable authentication, or complete /setup to create an admin account.")
		return
	}

	mode := AuthMode(*def.Auth.Mode)
	switch mode {
	case AuthModeNone, AuthModeBasic, AuthModeBuiltin:
		cfg.Server.Auth.Mode = mode
	default:
		l.warnings = append(l.warnings, fmt.Sprintf("Invalid auth.mode value: %q, defaulting to 'builtin'", *def.Auth.Mode))
		cfg.Server.Auth.Mode = AuthModeBuiltin
	}
}

func (l *ConfigLoader) loadBasicAuth(cfg *Config, auth *AuthDef) {
	if auth.Basic != nil {
		cfg.Server.Auth.Basic.Username = auth.Basic.Username
		cfg.Server.Auth.Basic.Password = auth.Basic.Password
	}
}

func (l *ConfigLoader) loadOIDCAuth(cfg *Config, auth *AuthDef) {
	if auth.OIDC == nil {
		return
	}

	oidc := auth.OIDC
	cfg.Server.Auth.OIDC.ClientID = oidc.ClientID
	cfg.Server.Auth.OIDC.ClientSecret = oidc.ClientSecret
	cfg.Server.Auth.OIDC.ClientURL = oidc.ClientURL
	cfg.Server.Auth.OIDC.Issuer = oidc.Issuer
	cfg.Server.Auth.OIDC.Scopes = parseStringList(l.v.Get("auth.oidc.scopes"))
	cfg.Server.Auth.OIDC.Whitelist = parseStringList(l.v.Get("auth.oidc.whitelist"))
	cfg.Server.Auth.OIDC.AllowedDomains = parseStringList(l.v.Get("auth.oidc.allowed_domains"))
	cfg.Server.Auth.OIDC.ButtonLabel = oidc.ButtonLabel

	if oidc.AutoSignup != nil {
		cfg.Server.Auth.OIDC.AutoSignup = *oidc.AutoSignup
	} else {
		cfg.Server.Auth.OIDC.AutoSignup = true
	}

	if oidc.RoleMapping != nil {
		l.loadOIDCRoleMapping(cfg, oidc.RoleMapping)
	}
}

func (l *ConfigLoader) loadOIDCRoleMapping(cfg *Config, rm *OIDCRoleMappingDef) {
	cfg.Server.Auth.OIDC.RoleMapping.DefaultRole = rm.DefaultRole
	cfg.Server.Auth.OIDC.RoleMapping.GroupsClaim = rm.GroupsClaim
	cfg.Server.Auth.OIDC.RoleMapping.GroupMappings = rm.GroupMappings
	cfg.Server.Auth.OIDC.RoleMapping.RoleAttributePath = rm.RoleAttributePath

	if rm.RoleAttributeStrict != nil {
		cfg.Server.Auth.OIDC.RoleMapping.RoleAttributeStrict = *rm.RoleAttributeStrict
	}
	if rm.SkipOrgRoleSync != nil {
		cfg.Server.Auth.OIDC.RoleMapping.SkipOrgRoleSync = *rm.SkipOrgRoleSync
	}
}

func (l *ConfigLoader) loadBuiltinAuth(cfg *Config, auth *AuthDef) {
	if auth.Builtin == nil {
		return
	}

	if auth.Builtin.Token != nil {
		cfg.Server.Auth.Builtin.Token.Secret = auth.Builtin.Token.Secret
		cfg.Server.Auth.Builtin.Token.TTL = l.parseDuration("auth.builtin.token.ttl", auth.Builtin.Token.TTL)
	}

	if auth.Builtin.InitialAdmin != nil {
		cfg.Server.Auth.Builtin.InitialAdmin.Username = auth.Builtin.InitialAdmin.Username
		cfg.Server.Auth.Builtin.InitialAdmin.Password = auth.Builtin.InitialAdmin.Password
	}
}

func (l *ConfigLoader) setAuthDefaults(cfg *Config) {
	if cfg.Server.Auth.Builtin.Token.TTL <= 0 {
		cfg.Server.Auth.Builtin.Token.TTL = 24 * time.Hour
	}
	if cfg.Server.Auth.Mode == AuthModeBuiltin {
		// Warn on weak/default token secrets.
		if cfg.Server.Auth.Builtin.Token.Secret != "" {
			l.warnIfWeakValue(cfg.Server.Auth.Builtin.Token.Secret,
				[]string{"changeme", "secret", "password", "test", "dagu"},
				"Token secret is a well-known default value — use a strong random value for production")
		}

		// Warn on weak initial admin passwords.
		if cfg.Server.Auth.Builtin.InitialAdmin.IsConfigured() {
			l.warnIfWeakValue(cfg.Server.Auth.Builtin.InitialAdmin.Password,
				[]string{"password", "changeme", "admin", "dagu", "12345678"},
				"Initial admin password is a well-known default value — use a strong password for production")
		}
	}
	if len(cfg.Server.Auth.OIDC.Scopes) == 0 {
		cfg.Server.Auth.OIDC.Scopes = []string{"openid", "profile", "email"}
	}
	if cfg.Server.Auth.OIDC.RoleMapping.DefaultRole == "" {
		cfg.Server.Auth.OIDC.RoleMapping.DefaultRole = "viewer"
	}
	if cfg.Server.Auth.OIDC.ButtonLabel == "" {
		cfg.Server.Auth.OIDC.ButtonLabel = "Login with SSO"
	}
}

// warnIfWeakValue appends a warning if value matches any entry in weakList (case-insensitive).
func (l *ConfigLoader) warnIfWeakValue(value string, weakList []string, msg string) {
	for _, weak := range weakList {
		if strings.EqualFold(value, weak) {
			l.warnings = append(l.warnings, msg)
			return
		}
	}
}

func (l *ConfigLoader) loadServerDefaults(cfg *Config, def Definition) {
	cfg.Server.BasePath = cleanServerBasePath(cfg.Server.BasePath)
	cfg.Server.CheckUpdates = l.v.GetBool("check_updates")

	cfg.Server.Metrics = MetricsAccessPrivate
	if def.Metrics != nil {
		switch MetricsAccess(*def.Metrics) {
		case MetricsAccessPublic, MetricsAccessPrivate:
			cfg.Server.Metrics = MetricsAccess(*def.Metrics)
		default:
			l.warnings = append(l.warnings, fmt.Sprintf("Invalid server.metrics value: %q, defaulting to 'private'", *def.Metrics))
		}
	}

	cfg.Server.Terminal.Enabled = false
	if def.Terminal != nil && def.Terminal.Enabled != nil {
		cfg.Server.Terminal.Enabled = *def.Terminal.Enabled
	}
	cfg.Server.Terminal.MaxSessions = l.v.GetInt("terminal.max_sessions")
	if def.Terminal != nil && def.Terminal.MaxSessions != nil {
		cfg.Server.Terminal.MaxSessions = *def.Terminal.MaxSessions
	}

	cfg.Server.Audit.Enabled = true
	if def.Audit != nil && def.Audit.Enabled != nil {
		cfg.Server.Audit.Enabled = *def.Audit.Enabled
	}

	cfg.Server.Audit.RetentionDays = l.v.GetInt("audit.retention_days")

	cfg.Server.Session.MaxPerUser = l.v.GetInt("session.max_per_user")

	cfg.Server.SSE.MaxTopicsPerConnection = l.v.GetInt("sse.max_topics_per_connection")
	cfg.Server.SSE.MaxClients = l.v.GetInt("sse.max_clients")
	cfg.Server.SSE.HeartbeatInterval = l.v.GetDuration("sse.heartbeat_interval")
	cfg.Server.SSE.WriteBufferSize = l.v.GetInt("sse.write_buffer_size")
	cfg.Server.SSE.SlowClientTimeout = l.v.GetDuration("sse.slow_client_timeout")

	if def.SSE == nil {
		return
	}

	if def.SSE.MaxTopicsPerConnection != nil {
		cfg.Server.SSE.MaxTopicsPerConnection = *def.SSE.MaxTopicsPerConnection
	}
	if def.SSE.MaxClients != nil {
		cfg.Server.SSE.MaxClients = *def.SSE.MaxClients
	}
	if def.SSE.WriteBufferSize != nil {
		cfg.Server.SSE.WriteBufferSize = *def.SSE.WriteBufferSize
	}
	if def.SSE.HeartbeatInterval != nil {
		if duration, err := time.ParseDuration(*def.SSE.HeartbeatInterval); err == nil {
			cfg.Server.SSE.HeartbeatInterval = duration
		} else {
			l.warnings = append(l.warnings, fmt.Sprintf("Invalid sse.heartbeat_interval value: %q", *def.SSE.HeartbeatInterval))
		}
	}
	if def.SSE.SlowClientTimeout != nil {
		if duration, err := time.ParseDuration(*def.SSE.SlowClientTimeout); err == nil {
			cfg.Server.SSE.SlowClientTimeout = duration
		} else {
			l.warnings = append(l.warnings, fmt.Sprintf("Invalid sse.slow_client_timeout value: %q", *def.SSE.SlowClientTimeout))
		}
	}
}

func (l *ConfigLoader) loadWebhooksConfig(cfg *Config, def Definition) {
	cfg.Webhooks.MaxPayloadSize = l.v.GetInt("webhooks.max_payload_size")
	if def.Webhooks != nil && def.Webhooks.MaxPayloadSize != nil {
		cfg.Webhooks.MaxPayloadSize = *def.Webhooks.MaxPayloadSize
	}
}

func (l *ConfigLoader) loadUIConfig(cfg *Config, def Definition) {
	cfg.UI.MaxDashboardPageLimit = l.v.GetInt("ui.max_dashboard_page_limit")
	cfg.UI.NavbarTitle = l.v.GetString("ui.navbar_title")
	cfg.UI.LogEncodingCharset = l.v.GetString("ui.log_encoding_charset")
	cfg.UI.DAGs.SortField = l.v.GetString("ui.dags.sort_field")
	cfg.UI.DAGs.SortOrder = l.v.GetString("ui.dags.sort_order")

	if def.UI == nil {
		return
	}

	cfg.UI.NavbarColor = def.UI.NavbarColor
	setIfNotEmpty(&cfg.UI.NavbarTitle, def.UI.NavbarTitle)
	setIfNotEmpty(&cfg.UI.LogEncodingCharset, def.UI.LogEncodingCharset)

	if def.UI.MaxDashboardPageLimit > 0 {
		cfg.UI.MaxDashboardPageLimit = def.UI.MaxDashboardPageLimit
	}

	if def.UI.DAGs != nil {
		setIfNotEmpty(&cfg.UI.DAGs.SortField, def.UI.DAGs.SortField)
		setIfNotEmpty(&cfg.UI.DAGs.SortOrder, def.UI.DAGs.SortOrder)
	}
}

func (l *ConfigLoader) loadQueuesConfig(cfg *Config, def Definition) {
	cfg.Queues.Enabled = true
	if def.Queues == nil {
		return
	}

	cfg.Queues.Enabled = def.Queues.Enabled
	for _, qd := range def.Queues.Config {
		qc := QueueConfig{
			Name:          qd.Name,
			MaxActiveRuns: qd.MaxConcurrency,
		}
		if qd.MaxActiveRuns != nil {
			qc.MaxActiveRuns = *qd.MaxActiveRuns
		}
		cfg.Queues.Config = append(cfg.Queues.Config, qc)
	}
}

func (l *ConfigLoader) loadCoordinatorConfig(cfg *Config, def Definition) {
	cfg.Coordinator.Enabled = l.resolveCoordinatorEnabled(def)

	if def.Coordinator != nil {
		cfg.Coordinator.Host = def.Coordinator.Host
		cfg.Coordinator.Advertise = def.Coordinator.Advertise
		cfg.Coordinator.Port = def.Coordinator.Port
		cfg.Coordinator.HealthPort = def.Coordinator.HealthPort
	}

	l.setCoordinatorDefaults(cfg)
}

func (l *ConfigLoader) resolveCoordinatorEnabled(def Definition) bool {
	if l.v.IsSet("coordinator.enabled") {
		return l.v.GetBool("coordinator.enabled")
	}
	if def.Coordinator != nil && def.Coordinator.Enabled != nil {
		return *def.Coordinator.Enabled
	}
	return true // Default: enabled
}

func (l *ConfigLoader) loadWorkerConfig(cfg *Config, def Definition) {
	if def.Worker != nil {
		cfg.Worker.ID = def.Worker.ID
		cfg.Worker.MaxActiveRuns = def.Worker.MaxActiveRuns
		cfg.Worker.HealthPort = def.Worker.HealthPort

		if def.Worker.Labels != nil {
			cfg.Worker.Labels = parseLabels(def.Worker.Labels)
		}

		if def.Worker.Coordinators != nil {
			addresses, addrWarnings := parseCoordinatorAddresses(def.Worker.Coordinators)
			cfg.Worker.Coordinators = addresses
			l.warnings = append(l.warnings, addrWarnings...)
		}

		if def.Worker.PostgresPool != nil {
			l.loadPostgresPoolConfig(&cfg.Worker.PostgresPool, def.Worker.PostgresPool)
		}
	}

	l.setWorkerDefaults(cfg)
	l.setPostgresPoolDefaults(&cfg.Worker.PostgresPool)
}

func (l *ConfigLoader) setCoordinatorDefaults(cfg *Config) {
	if !l.v.IsSet("coordinator.health_port") && cfg.Coordinator.HealthPort <= 0 {
		cfg.Coordinator.HealthPort = 8091
	}
}

func (l *ConfigLoader) setWorkerDefaults(cfg *Config) {
	if !l.v.IsSet("worker.health_port") && cfg.Worker.HealthPort <= 0 {
		cfg.Worker.HealthPort = 8092
	}
}

func (l *ConfigLoader) loadPostgresPoolConfig(pool *PostgresPoolConfig, def *PostgresPoolDef) {
	setIfPositive(&pool.MaxOpenConns, def.MaxOpenConns)
	setIfPositive(&pool.MaxIdleConns, def.MaxIdleConns)
	setIfPositive(&pool.ConnMaxLifetime, def.ConnMaxLifetime)
	setIfPositive(&pool.ConnMaxIdleTime, def.ConnMaxIdleTime)
}

func setIfPositive(target *int, value int) {
	if value > 0 {
		*target = value
	}
}

func (l *ConfigLoader) setPostgresPoolDefaults(pool *PostgresPoolConfig) {
	setDefaultIfZero(&pool.MaxOpenConns, 25)
	setDefaultIfZero(&pool.MaxIdleConns, 5)
	setDefaultIfZero(&pool.ConnMaxLifetime, 300)
	setDefaultIfZero(&pool.ConnMaxIdleTime, 60)
}

func setDefaultIfZero(target *int, defaultValue int) {
	if *target == 0 {
		*target = defaultValue
	}
}

// parseCoordinatorAddresses parses coordinator addresses from comma-separated strings or string slices.
func parseCoordinatorAddresses(input any) ([]string, []string) {
	var addresses, warnings []string

	validateAndAdd := func(addr string) {
		addr = strings.TrimSpace(addr)
		if addr == "" {
			return
		}

		host, portStr, err := net.SplitHostPort(addr)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("Invalid coordinator address %q: %v", addr, err))
			return
		}

		port, err := strconv.Atoi(portStr)
		if err != nil || port <= 0 || port > 65535 {
			warnings = append(warnings, fmt.Sprintf("Invalid coordinator address %q: port must be between 1 and 65535", addr))
			return
		}

		if host == "" {
			warnings = append(warnings, fmt.Sprintf("Invalid coordinator address %q: empty host", addr))
			return
		}

		addresses = append(addresses, addr)
	}

	switch v := input.(type) {
	case string:
		if v != "" {
			for part := range strings.SplitSeq(v, ",") {
				validateAndAdd(part)
			}
		}
	case []any:
		for _, item := range v {
			if addr, ok := item.(string); ok {
				validateAndAdd(addr)
			}
		}
	case []string:
		for _, addr := range v {
			validateAndAdd(addr)
		}
	}

	return addresses, warnings
}

func (l *ConfigLoader) loadSchedulerConfig(cfg *Config, def Definition) {
	if def.Scheduler != nil {
		cfg.Scheduler.Port = def.Scheduler.Port
		cfg.Scheduler.LockStaleThreshold = l.parseDuration("scheduler.lock_stale_threshold", def.Scheduler.LockStaleThreshold)
		cfg.Scheduler.LockRetryInterval = l.parseDuration("scheduler.lock_retry_interval", def.Scheduler.LockRetryInterval)
		cfg.Scheduler.ZombieDetectionInterval = l.parseDuration("scheduler.zombie_detection_interval", def.Scheduler.ZombieDetectionInterval)
		cfg.Scheduler.RetryFailureWindow = l.parseDuration("scheduler.retry_failure_window", def.Scheduler.RetryFailureWindow)
		cfg.Scheduler.FailureThreshold = def.Scheduler.FailureThreshold
	}

	l.setSchedulerDefaults(cfg)
}

func (l *ConfigLoader) setSchedulerDefaults(cfg *Config) {
	// Only default port when not explicitly set (0 = disable health server)
	if !l.v.IsSet("scheduler.port") && cfg.Scheduler.Port <= 0 {
		cfg.Scheduler.Port = 8090
	}
	if cfg.Scheduler.LockStaleThreshold <= 0 {
		cfg.Scheduler.LockStaleThreshold = 30 * time.Second
	}
	if cfg.Scheduler.LockRetryInterval <= 0 {
		cfg.Scheduler.LockRetryInterval = 5 * time.Second
	}
	// Default ZombieDetectionInterval only if not explicitly set (0 disables detection)
	if cfg.Scheduler.ZombieDetectionInterval <= 0 && !l.v.IsSet("scheduler.zombie_detection_interval") {
		cfg.Scheduler.ZombieDetectionInterval = 45 * time.Second
	}
	// Default RetryFailureWindow only if not explicitly set (0 disables scanning)
	if cfg.Scheduler.RetryFailureWindow <= 0 && !l.v.IsSet("scheduler.retry_failure_window") {
		cfg.Scheduler.RetryFailureWindow = 24 * time.Hour
	}
	if cfg.Scheduler.FailureThreshold <= 0 {
		cfg.Scheduler.FailureThreshold = 3
	}
}

func (l *ConfigLoader) loadProcConfig(cfg *Config, def Definition) {
	if l.v.IsSet("proc.heartbeat_interval") {
		cfg.Proc.HeartbeatInterval = l.parseDuration(
			"proc.heartbeat_interval", l.v.GetString("proc.heartbeat_interval"),
		)
	}
	if l.v.IsSet("proc.heartbeat_sync_interval") {
		cfg.Proc.HeartbeatSyncInterval = l.parseDuration(
			"proc.heartbeat_sync_interval", l.v.GetString("proc.heartbeat_sync_interval"),
		)
	}
	if l.v.IsSet("proc.stale_threshold") {
		cfg.Proc.StaleThreshold = l.parseDuration(
			"proc.stale_threshold", l.v.GetString("proc.stale_threshold"),
		)
	}

	l.loadLegacySchedulerProcConfig(cfg, def)
	l.setProcDefaults(cfg)
}

func (l *ConfigLoader) loadLegacySchedulerProcConfig(cfg *Config, def Definition) {
	l.applyDeprecatedProcAlias(
		&cfg.Proc.HeartbeatInterval,
		"proc.heartbeat_interval",
		"scheduler.heartbeat_interval",
		l.schedulerLegacyValue(def, "scheduler.heartbeat_interval", func(sd *SchedulerDef) string { return sd.HeartbeatInterval }),
	)
	l.applyDeprecatedProcAlias(
		&cfg.Proc.HeartbeatSyncInterval,
		"proc.heartbeat_sync_interval",
		"scheduler.heartbeat_sync_interval",
		l.schedulerLegacyValue(def, "scheduler.heartbeat_sync_interval", func(sd *SchedulerDef) string { return sd.HeartbeatSyncInterval }),
	)
	l.applyDeprecatedProcAlias(
		&cfg.Proc.StaleThreshold,
		"proc.stale_threshold",
		"scheduler.stale_threshold",
		l.schedulerLegacyValue(def, "scheduler.stale_threshold", func(sd *SchedulerDef) string { return sd.StaleThreshold }),
	)
}

func (l *ConfigLoader) schedulerLegacyValue(
	def Definition,
	key string,
	getter func(*SchedulerDef) string,
) string {
	if def.Scheduler != nil {
		return getter(def.Scheduler)
	}
	return l.v.GetString(key)
}

func (l *ConfigLoader) applyDeprecatedProcAlias(
	target *time.Duration,
	canonicalKey, legacyKey, legacyValue string,
) {
	if !l.v.IsSet(legacyKey) {
		return
	}
	if l.v.IsSet(canonicalKey) {
		l.warnings = append(l.warnings,
			fmt.Sprintf("%s is deprecated and ignored because %s is set", legacyKey, canonicalKey))
		return
	}

	*target = l.parseDuration(legacyKey, legacyValue)
	l.warnings = append(l.warnings, fmt.Sprintf("%s is deprecated; use %s instead", legacyKey, canonicalKey))
}

func (l *ConfigLoader) setProcDefaults(cfg *Config) {
	if cfg.Proc.HeartbeatInterval <= 0 {
		cfg.Proc.HeartbeatInterval = 5 * time.Second
	}
	if cfg.Proc.HeartbeatSyncInterval <= 0 {
		cfg.Proc.HeartbeatSyncInterval = 10 * time.Second
	}
	if cfg.Proc.StaleThreshold <= 0 {
		cfg.Proc.StaleThreshold = 90 * time.Second
	}
}

func (l *ConfigLoader) loadMonitoringConfig(cfg *Config, def Definition) {
	if def.Monitoring != nil {
		cfg.Monitoring.Retention = l.parseDuration("monitoring.retention", def.Monitoring.Retention)
		cfg.Monitoring.Interval = l.parseDuration("monitoring.interval", def.Monitoring.Interval)
	}

	if cfg.Monitoring.Retention <= 0 {
		cfg.Monitoring.Retention = 24 * time.Hour
	}
	if cfg.Monitoring.Interval <= 0 {
		cfg.Monitoring.Interval = 5 * time.Second
	}
}

func (l *ConfigLoader) loadGitSyncConfig(cfg *Config, def Definition) {
	if def.GitSync == nil {
		return
	}

	cfg.GitSync.Enabled = def.GitSync.Enabled != nil && *def.GitSync.Enabled
	if !cfg.GitSync.Enabled {
		return
	}

	l.setGitSyncDefaults(cfg)
	l.applyGitSyncDefinition(cfg, def.GitSync)
}

func (l *ConfigLoader) setGitSyncDefaults(cfg *Config) {
	cfg.GitSync.Branch = "main"
	cfg.GitSync.PushEnabled = true
	cfg.GitSync.Auth.Type = "token"
	cfg.GitSync.AutoSync.OnStartup = true
	cfg.GitSync.AutoSync.Interval = 300
	cfg.GitSync.Commit.AuthorName = "Dagu"
	cfg.GitSync.Commit.AuthorEmail = "dagu@localhost"
}

func (l *ConfigLoader) applyGitSyncDefinition(cfg *Config, def *GitSyncDef) {
	setIfNotEmpty(&cfg.GitSync.Repository, def.Repository)
	setIfNotEmpty(&cfg.GitSync.Branch, def.Branch)
	setIfNotEmpty(&cfg.GitSync.Path, def.Path)

	if def.PushEnabled != nil {
		cfg.GitSync.PushEnabled = *def.PushEnabled
	}

	if def.Auth != nil {
		setIfNotEmpty(&cfg.GitSync.Auth.Type, def.Auth.Type)
		setIfNotEmpty(&cfg.GitSync.Auth.Token, def.Auth.Token)
		setIfNotEmpty(&cfg.GitSync.Auth.SSHKeyPath, def.Auth.SSHKeyPath)
		setIfNotEmpty(&cfg.GitSync.Auth.SSHPassphrase, def.Auth.SSHPassphrase)
	}

	if def.AutoSync != nil {
		if def.AutoSync.Enabled != nil {
			cfg.GitSync.AutoSync.Enabled = *def.AutoSync.Enabled
		}
		if def.AutoSync.OnStartup != nil {
			cfg.GitSync.AutoSync.OnStartup = *def.AutoSync.OnStartup
		}
		if l.v.IsSet("git_sync.auto_sync.interval") {
			cfg.GitSync.AutoSync.Interval = def.AutoSync.Interval
		}
	}

	if def.Commit != nil {
		setIfNotEmpty(&cfg.GitSync.Commit.AuthorName, def.Commit.AuthorName)
		setIfNotEmpty(&cfg.GitSync.Commit.AuthorEmail, def.Commit.AuthorEmail)
	}
}

func setIfNotEmpty(target *string, value string) {
	if value != "" {
		*target = value
	}
}

func (l *ConfigLoader) loadTunnelConfig(cfg *Config, def Definition) {
	cfg.Tunnel.Enabled = l.resolveTunnelEnabled(def)
	if !cfg.Tunnel.Enabled {
		return
	}

	l.loadTailscaleConfig(cfg, def)
	l.applyTunnelCLIOverrides(cfg)

	if def.Tunnel != nil && def.Tunnel.AllowTerminal != nil {
		cfg.Tunnel.AllowTerminal = *def.Tunnel.AllowTerminal
	}
	cfg.Tunnel.AllowedIPs = parseStringList(l.v.Get("tunnel.allowed_ips"))

	l.loadTunnelRateLimiting(cfg, def)

	if cfg.Tunnel.Tailscale.Hostname == "" {
		cfg.Tunnel.Tailscale.Hostname = AppSlug
	}
}

func (l *ConfigLoader) resolveTunnelEnabled(def Definition) bool {
	if l.v.IsSet("tunnel.enabled") {
		return l.v.GetBool("tunnel.enabled")
	}
	if def.Tunnel != nil && def.Tunnel.Enabled != nil {
		return *def.Tunnel.Enabled
	}
	return false
}

func (l *ConfigLoader) loadTailscaleConfig(cfg *Config, def Definition) {
	if def.Tunnel == nil || def.Tunnel.Tailscale == nil {
		return
	}

	ts := def.Tunnel.Tailscale
	cfg.Tunnel.Tailscale.AuthKey = ts.AuthKey
	cfg.Tunnel.Tailscale.Hostname = ts.Hostname
	cfg.Tunnel.Tailscale.StateDir = ts.StateDir

	if ts.Funnel != nil {
		cfg.Tunnel.Tailscale.Funnel = *ts.Funnel
	}
	if ts.HTTPS != nil {
		cfg.Tunnel.Tailscale.HTTPS = *ts.HTTPS
	}
}

func (l *ConfigLoader) applyTunnelCLIOverrides(cfg *Config) {
	if l.v.IsSet("tunnel.tailscale.funnel") {
		cfg.Tunnel.Tailscale.Funnel = l.v.GetBool("tunnel.tailscale.funnel")
	}
	if l.v.IsSet("tunnel.tailscale.https") {
		cfg.Tunnel.Tailscale.HTTPS = l.v.GetBool("tunnel.tailscale.https")
	}
	if l.v.IsSet("tunnel.token") {
		cfg.Tunnel.Tailscale.AuthKey = l.v.GetString("tunnel.token")
	}
}

func (l *ConfigLoader) loadTunnelRateLimiting(cfg *Config, def Definition) {
	if def.Tunnel != nil && def.Tunnel.RateLimiting != nil {
		rl := def.Tunnel.RateLimiting
		if rl.Enabled != nil {
			cfg.Tunnel.RateLimiting.Enabled = *rl.Enabled
		}
		cfg.Tunnel.RateLimiting.LoginAttempts = rl.LoginAttempts
		cfg.Tunnel.RateLimiting.WindowSeconds = rl.WindowSeconds
		cfg.Tunnel.RateLimiting.BlockDurationSeconds = rl.BlockDurationSeconds
	}

	setDefaultIfNotPositive(&cfg.Tunnel.RateLimiting.LoginAttempts, 5)
	setDefaultIfNotPositive(&cfg.Tunnel.RateLimiting.WindowSeconds, 300)
	setDefaultIfNotPositive(&cfg.Tunnel.RateLimiting.BlockDurationSeconds, 900)
}

func setDefaultIfNotPositive(target *int, defaultValue int) {
	if *target <= 0 {
		*target = defaultValue
	}
}

func (l *ConfigLoader) loadBotsConfig(cfg *Config, def Definition) {
	// Default safe mode to true
	cfg.Bots.SafeMode = true
	cfg.Bots.Telegram.InterestedEventTypes = append([]string(nil), DefaultBotInterestedEventTypes...)
	cfg.Bots.Slack.InterestedEventTypes = append([]string(nil), DefaultBotInterestedEventTypes...)
	cfg.Bots.Discord.InterestedEventTypes = append([]string(nil), DefaultBotInterestedEventTypes...)
	cfg.Bots.Slack.RespondToAll = true
	cfg.Bots.Discord.RespondToAll = true

	botsDef := def.Bots
	if botsDef == nil {
		botsDef = &BotsDef{}
	}

	// Check env var override for provider
	if provider := l.v.GetString("bots.provider"); provider != "" {
		cfg.Bots.Provider = BotProvider(provider)
	}

	// Check env var override for token
	if token := l.v.GetString("bots.telegram.token"); token != "" {
		cfg.Bots.Telegram.Token = token
	}
	if raw, ok := lookupInterestedEventTypesEnv("BOTS_TELEGRAM_INTERESTED_EVENT_TYPES"); ok {
		cfg.Bots.Telegram.InterestedEventTypes = parseInterestedEventTypes(raw)
	}

	if cfg.Bots.Provider == BotProviderNone {
		cfg.Bots.Provider = BotProvider(botsDef.Provider)
	}

	if botsDef.SafeMode != nil {
		cfg.Bots.SafeMode = *botsDef.SafeMode
	}

	if botsDef.Telegram != nil {
		if cfg.Bots.Telegram.Token == "" {
			cfg.Bots.Telegram.Token = botsDef.Telegram.Token
		}
		if len(botsDef.Telegram.AllowedChatIDs) > 0 {
			cfg.Bots.Telegram.AllowedChatIDs = botsDef.Telegram.AllowedChatIDs
		}
		if botsDef.Telegram.InterestedEventTypes != nil &&
			!hasInterestedEventTypesEnv("BOTS_TELEGRAM_INTERESTED_EVENT_TYPES") {
			cfg.Bots.Telegram.InterestedEventTypes = parseInterestedEventTypesSlice(botsDef.Telegram.InterestedEventTypes)
		}
	}

	// Check env var override for Slack tokens
	if botToken := l.v.GetString("bots.slack.bot_token"); botToken != "" {
		cfg.Bots.Slack.BotToken = botToken
	}
	if appToken := l.v.GetString("bots.slack.app_token"); appToken != "" {
		cfg.Bots.Slack.AppToken = appToken
	}
	if raw, ok := lookupInterestedEventTypesEnv("BOTS_SLACK_INTERESTED_EVENT_TYPES"); ok {
		cfg.Bots.Slack.InterestedEventTypes = parseInterestedEventTypes(raw)
	}

	if botsDef.Slack != nil {
		if cfg.Bots.Slack.BotToken == "" {
			cfg.Bots.Slack.BotToken = botsDef.Slack.BotToken
		}
		if cfg.Bots.Slack.AppToken == "" {
			cfg.Bots.Slack.AppToken = botsDef.Slack.AppToken
		}
		if len(botsDef.Slack.AllowedChannelIDs) > 0 {
			cfg.Bots.Slack.AllowedChannelIDs = botsDef.Slack.AllowedChannelIDs
		}
		if botsDef.Slack.InterestedEventTypes != nil &&
			!hasInterestedEventTypesEnv("BOTS_SLACK_INTERESTED_EVENT_TYPES") {
			cfg.Bots.Slack.InterestedEventTypes = parseInterestedEventTypesSlice(botsDef.Slack.InterestedEventTypes)
		}
		if botsDef.Slack.RespondToAll != nil {
			cfg.Bots.Slack.RespondToAll = *botsDef.Slack.RespondToAll
		}
	}

	// Check env var override for Discord token
	if token := l.v.GetString("bots.discord.token"); token != "" {
		cfg.Bots.Discord.Token = token
	}
	if _, ok := os.LookupEnv(strings.ToUpper(AppSlug) + "_BOTS_DISCORD_ALLOWED_CHANNEL_IDS"); ok {
		cfg.Bots.Discord.AllowedChannelIDs = parseStringList(l.v.Get("bots.discord.allowed_channel_ids"))
	}
	if raw, ok := lookupInterestedEventTypesEnv("BOTS_DISCORD_INTERESTED_EVENT_TYPES"); ok {
		cfg.Bots.Discord.InterestedEventTypes = parseInterestedEventTypes(raw)
	}
	if _, ok := os.LookupEnv(strings.ToUpper(AppSlug) + "_BOTS_DISCORD_RESPOND_TO_ALL"); ok {
		cfg.Bots.Discord.RespondToAll = l.v.GetBool("bots.discord.respond_to_all")
	}

	if botsDef.Discord != nil {
		if cfg.Bots.Discord.Token == "" {
			cfg.Bots.Discord.Token = botsDef.Discord.Token
		}
		if len(botsDef.Discord.AllowedChannelIDs) > 0 {
			cfg.Bots.Discord.AllowedChannelIDs = botsDef.Discord.AllowedChannelIDs
		}
		if botsDef.Discord.InterestedEventTypes != nil &&
			!hasInterestedEventTypesEnv("BOTS_DISCORD_INTERESTED_EVENT_TYPES") {
			cfg.Bots.Discord.InterestedEventTypes = parseInterestedEventTypesSlice(botsDef.Discord.InterestedEventTypes)
		}
		if botsDef.Discord.RespondToAll != nil {
			cfg.Bots.Discord.RespondToAll = *botsDef.Discord.RespondToAll
		}
	}
}

func parseInterestedEventTypes(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return []string{}
	}
	parts := strings.Split(raw, ",")
	return parseInterestedEventTypesSlice(parts)
}

func parseInterestedEventTypesSlice(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		result = append(result, value)
	}
	return result
}

func lookupInterestedEventTypesEnv(suffix string) (string, bool) {
	return os.LookupEnv(strings.ToUpper(AppSlug) + "_" + suffix)
}

func hasInterestedEventTypesEnv(suffix string) bool {
	_, ok := lookupInterestedEventTypesEnv(suffix)
	return ok
}

func (l *ConfigLoader) loadLicenseConfig(cfg *Config, def Definition) {
	if def.License == nil {
		return
	}
	cfg.License.Key = def.License.Key
	cfg.License.CloudURL = def.License.CloudURL
}

func (l *ConfigLoader) loadExecutionModeConfig(cfg *Config, _ Definition) {
	mode := ExecutionMode(l.v.GetString("default_execution_mode"))
	if mode == "" {
		mode = ExecutionModeLocal
	}
	cfg.DefaultExecMode = mode
}

func (l *ConfigLoader) loadCacheConfig(cfg *Config, def Definition) {
	cfg.Cache = CacheModeNormal
	if def.Cache == nil {
		return
	}

	mode := CacheMode(*def.Cache)
	if mode.IsValid() {
		cfg.Cache = mode
	} else {
		l.warnings = append(l.warnings, fmt.Sprintf("Invalid cache mode: %q, using 'normal'", *def.Cache))
	}
}

func (l *ConfigLoader) finalizePaths(cfg *Config) {
	derivedPaths := []struct {
		target      *string
		defaultPath string
	}{
		{&cfg.Paths.DAGRunsDir, "dag-runs"},
		{&cfg.Paths.ProcDir, "proc"},
		{&cfg.Paths.QueueDir, "queue"},
		{&cfg.Paths.ServiceRegistryDir, "service-registry"},
		{&cfg.Paths.UsersDir, "users"},
		{&cfg.Paths.APIKeysDir, "apikeys"},
		{&cfg.Paths.WebhooksDir, "webhooks"},
		{&cfg.Paths.ContextsDir, "contexts"},
		{&cfg.Paths.RemoteNodesDir, "remote-nodes"},
		{&cfg.Paths.WorkspacesDir, "workspaces"},
	}

	for _, dp := range derivedPaths {
		if *dp.target == "" {
			*dp.target = filepath.Join(cfg.Paths.DataDir, dp.defaultPath)
		}
	}

	if cfg.Paths.SessionsDir == "" {
		cfg.Paths.SessionsDir = filepath.Join(cfg.Paths.DataDir, "agent", "sessions")
	}
	if cfg.Paths.ToolsDir == "" {
		cfg.Paths.ToolsDir = filepath.Join(cfg.Paths.DataDir, "tools")
	}

	if cfg.Paths.EventStoreDir == "" {
		cfg.Paths.EventStoreDir = filepath.Join(cfg.Paths.AdminLogsDir, "events")
	}
	if cfg.Paths.ArtifactDir == "" {
		cfg.Paths.ArtifactDir = filepath.Join(cfg.Paths.DataDir, "artifacts")
	}

	if cfg.Paths.DocsDir == "" {
		cfg.Paths.DocsDir = filepath.Join(cfg.Paths.DAGsDir, "docs")
	}

	if cfg.Paths.Executable == "" {
		if executable, err := os.Executable(); err == nil {
			cfg.Paths.Executable = executable
		}
	}
}

// LoadLegacyFields applies deprecated configuration fields to the current Config.
func (l *ConfigLoader) LoadLegacyFields(cfg *Config, def Definition) error {
	if l.requires(SectionServer) {
		setIfNotEmpty(&cfg.Server.APIBasePath, def.APIBaseURL)
	}

	if err := l.loadLegacyPaths(cfg, def); err != nil {
		return err
	}

	if l.requires(SectionUI) {
		setIfNotEmpty(&cfg.UI.LogEncodingCharset, def.LogEncodingCharset)
		setIfNotEmpty(&cfg.UI.NavbarColor, def.NavbarColor)
		setIfNotEmpty(&cfg.UI.NavbarTitle, def.NavbarTitle)
		if def.MaxDashboardPageLimit > 0 {
			cfg.UI.MaxDashboardPageLimit = def.MaxDashboardPageLimit
		}
	}

	return nil
}

func (l *ConfigLoader) loadLegacyPaths(cfg *Config, def Definition) error {
	legacyPaths := []struct {
		name   string
		target *string
		source string
	}{
		{"legacy DAGs", &cfg.Paths.DAGsDir, def.DAGs},
		{"legacy DAGsDir", &cfg.Paths.DAGsDir, def.DAGsDir},
		{"legacy Executable", &cfg.Paths.Executable, def.Executable},
		{"legacy LogDir", &cfg.Paths.LogDir, def.LogDir},
		{"legacy ArtifactDir", &cfg.Paths.ArtifactDir, def.ArtifactDir},
		{"legacy DataDir", &cfg.Paths.DataDir, def.DataDir},
		{"legacy SuspendFlagsDir", &cfg.Paths.SuspendFlagsDir, def.SuspendFlagsDir},
		{"legacy AdminLogsDir", &cfg.Paths.AdminLogsDir, def.AdminLogsDir},
		{"legacy EventStoreDir", &cfg.Paths.EventStoreDir, def.EventStoreDir},
		{"legacy BaseConfig", &cfg.Paths.BaseConfig, def.BaseConfig},
	}

	for _, lp := range legacyPaths {
		if lp.source == "" {
			continue
		}
		resolved, err := l.resolvePath(lp.name, lp.source)
		if err != nil {
			return err
		}
		*lp.target = resolved
	}

	return nil
}

// loadLegacyEnv maps deprecated environment variables to current configuration.
func (l *ConfigLoader) loadLegacyEnv(cfg *Config) {
	type legacyEnvMapping struct {
		newKey   string
		setter   func(*Config, string)
		requires ConfigSection
	}

	legacyEnvs := map[string]legacyEnvMapping{
		"DAGU__ADMIN_NAVBAR_COLOR": {
			newKey:   "DAGU_NAVBAR_COLOR",
			setter:   func(c *Config, v string) { c.UI.NavbarColor = v },
			requires: SectionUI,
		},
		"DAGU__ADMIN_NAVBAR_TITLE": {
			newKey:   "DAGU_NAVBAR_TITLE",
			setter:   func(c *Config, v string) { c.UI.NavbarTitle = v },
			requires: SectionUI,
		},
		"DAGU__ADMIN_PORT": {
			newKey: "DAGU_PORT",
			setter: func(c *Config, v string) {
				if i, err := strconv.Atoi(v); err == nil {
					c.Server.Port = i
				}
			},
			requires: SectionServer,
		},
		"DAGU__ADMIN_HOST": {
			newKey:   "DAGU_HOST",
			setter:   func(c *Config, v string) { c.Server.Host = v },
			requires: SectionServer,
		},
		"DAGU__DATA": {
			newKey:   "DAGU_DATA_DIR",
			setter:   func(c *Config, v string) { c.Paths.DataDir = fileutil.ResolvePathOrBlank(v) },
			requires: SectionNone,
		},
		"DAGU__SUSPEND_FLAGS_DIR": {
			newKey:   "DAGU_SUSPEND_FLAGS_DIR",
			setter:   func(c *Config, v string) { c.Paths.SuspendFlagsDir = fileutil.ResolvePathOrBlank(v) },
			requires: SectionNone,
		},
		"DAGU__ADMIN_LOGS_DIR": {
			newKey:   "DAGU_ADMIN_LOG_DIR",
			setter:   func(c *Config, v string) { c.Paths.AdminLogsDir = fileutil.ResolvePathOrBlank(v) },
			requires: SectionNone,
		},
		"DAGU__EVENT_STORE_DIR": {
			newKey:   "DAGU_EVENT_STORE_DIR",
			setter:   func(c *Config, v string) { c.Paths.EventStoreDir = fileutil.ResolvePathOrBlank(v) },
			requires: SectionNone,
		},
	}

	for oldKey, mapping := range legacyEnvs {
		if mapping.requires != SectionNone && !l.requires(mapping.requires) {
			continue
		}

		if value := os.Getenv(oldKey); value != "" {
			log.Printf("%s is deprecated. Use %s instead.", oldKey, mapping.newKey)
			mapping.setter(cfg, value)
		}
	}
}

func (l *ConfigLoader) setupViper(xdgConfig XDGConfig, homeDir, configFile, appHomeOverride string) ([]string, error) {
	var paths Paths
	var err error

	if appHomeOverride != "" {
		paths = setUnifiedPaths(fileutil.ResolvePathOrBlank(appHomeOverride))
	} else {
		paths, err = ResolvePaths("DAGU_HOME", filepath.Join(homeDir, ".dagu"), xdgConfig)
		if err != nil {
			return nil, err
		}
	}

	l.configureViper(paths.ConfigDir, configFile)
	l.bindEnvironmentVariables()
	l.setViperDefaultValues(paths)

	return paths.Notices, nil
}

func (l *ConfigLoader) setViperDefaultValues(paths Paths) {
	// Paths
	l.v.SetDefault("skip_examples", false)
	l.v.SetDefault("paths.dags_dir", paths.DAGsDir)
	l.v.SetDefault("paths.suspend_flags_dir", paths.SuspendFlagsDir)
	l.v.SetDefault("paths.data_dir", paths.DataDir)
	l.v.SetDefault("paths.log_dir", paths.LogsDir)
	l.v.SetDefault("paths.artifact_dir", paths.ArtifactsDir)
	l.v.SetDefault("paths.admin_logs_dir", paths.AdminLogsDir)
	l.v.SetDefault("paths.event_store_dir", paths.EventStoreDir)
	l.v.SetDefault("paths.base_config", paths.BaseConfigFile)

	// Server
	l.v.SetDefault("host", "127.0.0.1")
	l.v.SetDefault("port", 8080)
	l.v.SetDefault("debug", false)
	l.v.SetDefault("base_path", "")
	l.v.SetDefault("api_base_path", "/api/v1")
	l.v.SetDefault("check_updates", true)
	l.v.SetDefault("latest_status_today", false)
	l.v.SetDefault("metrics", "private")
	l.v.SetDefault("cache", "normal")
	l.v.SetDefault("log_format", "text")
	l.v.SetDefault("access_log_mode", "all")

	// Coordinator
	l.v.SetDefault("coordinator.host", "127.0.0.1")
	l.v.SetDefault("coordinator.advertise", "")
	l.v.SetDefault("coordinator.port", 50055)

	// Worker
	l.v.SetDefault("worker.max_active_runs", 100)
	l.v.SetDefault("worker.postgres_pool.max_open_conns", 25)
	l.v.SetDefault("worker.postgres_pool.max_idle_conns", 5)
	l.v.SetDefault("worker.postgres_pool.conn_max_lifetime", 300)
	l.v.SetDefault("worker.postgres_pool.conn_max_idle_time", 60)

	// UI
	l.v.SetDefault("ui.navbar_title", AppName)
	l.v.SetDefault("ui.max_dashboard_page_limit", 100)
	l.v.SetDefault("ui.log_encoding_charset", getDefaultLogEncodingCharset())
	l.v.SetDefault("ui.dags.sort_field", "name")
	l.v.SetDefault("ui.dags.sort_order", "asc")

	// Execution
	l.v.SetDefault("default_execution_mode", string(ExecutionModeLocal))

	// Queues
	l.v.SetDefault("queues.enabled", true)

	// Scheduler
	l.v.SetDefault("scheduler.lock_stale_threshold", "30s")
	l.v.SetDefault("scheduler.lock_retry_interval", "5s")
	l.v.SetDefault("scheduler.retry_failure_window", "24h")

	// Peer
	l.v.SetDefault("peer.insecure", true)

	// Audit
	l.v.SetDefault("audit.retention_days", 7)

	// Event store
	l.v.SetDefault("event_store.enabled", true)
	l.v.SetDefault("event_store.retention_days", 1)

	// Webhooks
	l.v.SetDefault("webhooks.max_payload_size", DefaultWebhookMaxPayloadSize)

	// Terminal
	l.v.SetDefault("terminal.max_sessions", 5)

	// Session
	l.v.SetDefault("session.max_per_user", 100)

	// SSE
	l.v.SetDefault("sse.max_topics_per_connection", 20)
	l.v.SetDefault("sse.max_clients", 1000)
	l.v.SetDefault("sse.heartbeat_interval", "10s")
	l.v.SetDefault("sse.write_buffer_size", 65536)
	l.v.SetDefault("sse.slow_client_timeout", "30s")

	// Monitoring
	l.v.SetDefault("monitoring.retention", "24h")
	l.v.SetDefault("monitoring.interval", "5s")
}

type envBinding struct {
	key    string
	env    string
	isPath bool
}

var envBindings = []envBinding{
	// Server
	{key: "log_format", env: "LOG_FORMAT"},
	{key: "access_log_mode", env: "ACCESS_LOG_MODE"},
	{key: "base_path", env: "BASE_PATH"},
	{key: "api_base_url", env: "API_BASE_URL"},
	{key: "tz", env: "TZ"},
	{key: "host", env: "HOST"},
	{key: "port", env: "PORT"},
	{key: "debug", env: "DEBUG"},
	{key: "headless", env: "HEADLESS"},
	{key: "check_updates", env: "CHECK_UPDATES"},
	{key: "latest_status_today", env: "LATEST_STATUS_TODAY"},
	{key: "metrics", env: "SERVER_METRICS"},
	{key: "cache", env: "CACHE"},

	{key: "terminal.enabled", env: "TERMINAL_ENABLED"},
	{key: "terminal.max_sessions", env: "TERMINAL_MAX_SESSIONS"},
	{key: "audit.enabled", env: "AUDIT_ENABLED"},
	{key: "audit.retention_days", env: "AUDIT_RETENTION_DAYS"},
	{key: "event_store.enabled", env: "EVENT_STORE_ENABLED"},
	{key: "event_store.retention_days", env: "EVENT_STORE_RETENTION_DAYS"},
	{key: "webhooks.max_payload_size", env: "WEBHOOKS_MAX_PAYLOAD_SIZE"},
	{key: "session.max_per_user", env: "SESSION_MAX_PER_USER"},
	{key: "sse.max_topics_per_connection", env: "SSE_MAX_TOPICS_PER_CONNECTION"},
	{key: "sse.max_clients", env: "SSE_MAX_CLIENTS"},
	{key: "sse.heartbeat_interval", env: "SSE_HEARTBEAT_INTERVAL"},
	{key: "sse.write_buffer_size", env: "SSE_WRITE_BUFFER_SIZE"},
	{key: "sse.slow_client_timeout", env: "SSE_SLOW_CLIENT_TIMEOUT"},

	// Core
	{key: "default_shell", env: "DEFAULT_SHELL"},
	{key: "skip_examples", env: "SKIP_EXAMPLES"},
	{key: "env_passthrough", env: "ENV_PASSTHROUGH"},
	{key: "env_passthrough_prefixes", env: "ENV_PASSTHROUGH_PREFIXES"},

	// Secrets
	{key: "secrets.vault.address", env: "SECRETS_VAULT_ADDRESS"},
	{key: "secrets.vault.token", env: "SECRETS_VAULT_TOKEN"},
	{key: "secrets.kubernetes.namespace", env: "SECRETS_KUBERNETES_NAMESPACE"},
	{key: "secrets.kubernetes.kubeconfig", env: "SECRETS_KUBERNETES_KUBECONFIG", isPath: true},
	{key: "secrets.kubernetes.context", env: "SECRETS_KUBERNETES_CONTEXT"},

	// Scheduler
	{key: "scheduler.port", env: "SCHEDULER_PORT"},
	{key: "scheduler.lock_stale_threshold", env: "SCHEDULER_LOCK_STALE_THRESHOLD"},
	{key: "scheduler.lock_retry_interval", env: "SCHEDULER_LOCK_RETRY_INTERVAL"},
	{key: "scheduler.zombie_detection_interval", env: "SCHEDULER_ZOMBIE_DETECTION_INTERVAL"},
	{key: "scheduler.retry_failure_window", env: "SCHEDULER_RETRY_FAILURE_WINDOW"},
	{key: "scheduler.failure_threshold", env: "SCHEDULER_FAILURE_THRESHOLD"},

	// Proc
	{key: "proc.heartbeat_interval", env: "PROC_HEARTBEAT_INTERVAL"},
	{key: "proc.heartbeat_sync_interval", env: "PROC_HEARTBEAT_SYNC_INTERVAL"},
	{key: "proc.stale_threshold", env: "PROC_STALE_THRESHOLD"},
	// Proc (legacy scheduler aliases)
	{key: "scheduler.heartbeat_interval", env: "SCHEDULER_HEARTBEAT_INTERVAL"},
	{key: "scheduler.heartbeat_sync_interval", env: "SCHEDULER_HEARTBEAT_SYNC_INTERVAL"},
	{key: "scheduler.stale_threshold", env: "SCHEDULER_STALE_THRESHOLD"},

	// UI
	{key: "ui.max_dashboard_page_limit", env: "UI_MAX_DASHBOARD_PAGE_LIMIT"},
	{key: "ui.log_encoding_charset", env: "UI_LOG_ENCODING_CHARSET"},
	{key: "ui.navbar_color", env: "UI_NAVBAR_COLOR"},
	{key: "ui.navbar_title", env: "UI_NAVBAR_TITLE"},
	{key: "ui.dags.sort_field", env: "UI_DAGS_SORT_FIELD"},
	{key: "ui.dags.sort_order", env: "UI_DAGS_SORT_ORDER"},
	// UI (legacy)
	{key: "ui.max_dashboard_page_limit", env: "MAX_DASHBOARD_PAGE_LIMIT"},
	{key: "ui.log_encoding_charset", env: "LOG_ENCODING_CHARSET"},
	{key: "ui.navbar_color", env: "NAVBAR_COLOR"},
	{key: "ui.navbar_title", env: "NAVBAR_TITLE"},

	// Auth
	{key: "auth.mode", env: "AUTH_MODE"},
	{key: "auth.basic.username", env: "AUTH_BASIC_USERNAME"},
	{key: "auth.basic.password", env: "AUTH_BASIC_PASSWORD"},
	// Auth OIDC
	{key: "auth.oidc.client_id", env: "AUTH_OIDC_CLIENT_ID"},
	{key: "auth.oidc.client_secret", env: "AUTH_OIDC_CLIENT_SECRET"},
	{key: "auth.oidc.client_url", env: "AUTH_OIDC_CLIENT_URL"},
	{key: "auth.oidc.issuer", env: "AUTH_OIDC_ISSUER"},
	{key: "auth.oidc.scopes", env: "AUTH_OIDC_SCOPES"},
	{key: "auth.oidc.whitelist", env: "AUTH_OIDC_WHITELIST"},
	{key: "auth.oidc.auto_signup", env: "AUTH_OIDC_AUTO_SIGNUP"},
	{key: "auth.oidc.allowed_domains", env: "AUTH_OIDC_ALLOWED_DOMAINS"},
	{key: "auth.oidc.button_label", env: "AUTH_OIDC_BUTTON_LABEL"},
	// Auth OIDC Role Mapping
	{key: "auth.oidc.role_mapping.default_role", env: "AUTH_OIDC_DEFAULT_ROLE"},
	{key: "auth.oidc.role_mapping.groups_claim", env: "AUTH_OIDC_GROUPS_CLAIM"},
	{key: "auth.oidc.role_mapping.group_mappings", env: "AUTH_OIDC_GROUP_MAPPINGS"},
	{key: "auth.oidc.role_mapping.role_attribute_path", env: "AUTH_OIDC_ROLE_ATTRIBUTE_PATH"},
	{key: "auth.oidc.role_mapping.role_attribute_strict", env: "AUTH_OIDC_ROLE_ATTRIBUTE_STRICT"},
	{key: "auth.oidc.role_mapping.skip_org_role_sync", env: "AUTH_OIDC_SKIP_ORG_ROLE_SYNC"},
	// Auth (builtin)
	{key: "auth.builtin.token.secret", env: "AUTH_TOKEN_SECRET"},
	{key: "auth.builtin.token.ttl", env: "AUTH_TOKEN_TTL"},
	{key: "auth.builtin.initial_admin.username", env: "AUTH_BUILTIN_INITIAL_ADMIN_USERNAME"},
	{key: "auth.builtin.initial_admin.password", env: "AUTH_BUILTIN_INITIAL_ADMIN_PASSWORD"},

	// TLS
	{key: "tls.cert_file", env: "CERT_FILE"},
	{key: "tls.key_file", env: "KEY_FILE"},

	// Paths
	{key: "paths.dags_dir", env: "DAGS", isPath: true},
	{key: "paths.dags_dir", env: "DAGS_DIR", isPath: true},
	{key: "paths.docs_dir", env: "DOCS_DIR", isPath: true},
	{key: "paths.alt_dags_dir", env: "ALT_DAGS_DIR", isPath: true},
	{key: "paths.executable", env: "EXECUTABLE", isPath: true},
	{key: "paths.log_dir", env: "LOG_DIR", isPath: true},
	{key: "paths.artifact_dir", env: "ARTIFACT_DIR", isPath: true},
	{key: "paths.data_dir", env: "DATA_DIR", isPath: true},
	{key: "paths.tools_dir", env: "TOOLS_DIR", isPath: true},
	{key: "paths.suspend_flags_dir", env: "SUSPEND_FLAGS_DIR", isPath: true},
	{key: "paths.admin_logs_dir", env: "ADMIN_LOG_DIR", isPath: true},
	{key: "paths.event_store_dir", env: "EVENT_STORE_DIR", isPath: true},
	{key: "paths.base_config", env: "BASE_CONFIG", isPath: true},
	{key: "paths.dag_runs_dir", env: "DAG_RUNS_DIR", isPath: true},
	{key: "paths.proc_dir", env: "PROC_DIR", isPath: true},
	{key: "paths.queue_dir", env: "QUEUE_DIR", isPath: true},
	{key: "paths.service_registry_dir", env: "SERVICE_REGISTRY_DIR", isPath: true},
	{key: "paths.users_dir", env: "USERS_DIR", isPath: true},
	{key: "paths.contexts_dir", env: "CONTEXTS_DIR", isPath: true},
	{key: "paths.workspaces_dir", env: "WORKSPACES_DIR", isPath: true},

	// Execution
	{key: "default_execution_mode", env: "DEFAULT_EXECUTION_MODE"},

	// Queues
	{key: "queues.enabled", env: "QUEUE_ENABLED"},

	// Coordinator
	{key: "coordinator.enabled", env: "COORDINATOR_ENABLED"},
	{key: "coordinator.host", env: "COORDINATOR_HOST"},
	{key: "coordinator.advertise", env: "COORDINATOR_ADVERTISE"},
	{key: "coordinator.port", env: "COORDINATOR_PORT"},
	{key: "coordinator.health_port", env: "COORDINATOR_HEALTH_PORT"},

	// Worker
	{key: "worker.id", env: "WORKER_ID"},
	{key: "worker.max_active_runs", env: "WORKER_MAX_ACTIVE_RUNS"},
	{key: "worker.labels", env: "WORKER_LABELS"},
	{key: "worker.coordinators", env: "WORKER_COORDINATORS"},
	{key: "worker.health_port", env: "WORKER_HEALTH_PORT"},

	// Peer
	{key: "peer.cert_file", env: "PEER_CERT_FILE"},
	{key: "peer.key_file", env: "PEER_KEY_FILE"},
	{key: "peer.client_ca_file", env: "PEER_CLIENT_CA_FILE"},
	{key: "peer.skip_tls_verify", env: "PEER_SKIP_TLS_VERIFY"},
	{key: "peer.insecure", env: "PEER_INSECURE"},

	// Monitoring
	{key: "monitoring.retention", env: "MONITORING_RETENTION"},
	{key: "monitoring.interval", env: "MONITORING_INTERVAL"},

	// Worker PostgreSQL pool
	{key: "worker.postgres_pool.max_open_conns", env: "WORKER_POSTGRES_POOL_MAX_OPEN_CONNS"},
	{key: "worker.postgres_pool.max_idle_conns", env: "WORKER_POSTGRES_POOL_MAX_IDLE_CONNS"},
	{key: "worker.postgres_pool.conn_max_lifetime", env: "WORKER_POSTGRES_POOL_CONN_MAX_LIFETIME"},
	{key: "worker.postgres_pool.conn_max_idle_time", env: "WORKER_POSTGRES_POOL_CONN_MAX_IDLE_TIME"},

	// Tunnel
	{key: "tunnel.enabled", env: "TUNNEL"},
	{key: "tunnel.enabled", env: "TUNNEL_ENABLED"},
	{key: "tunnel.tailscale.auth_key", env: "TUNNEL_TAILSCALE_AUTH_KEY"},
	{key: "tunnel.tailscale.hostname", env: "TUNNEL_TAILSCALE_HOSTNAME"},
	{key: "tunnel.tailscale.funnel", env: "TUNNEL_TAILSCALE_FUNNEL"},
	{key: "tunnel.tailscale.https", env: "TUNNEL_TAILSCALE_HTTPS"},
	{key: "tunnel.tailscale.state_dir", env: "TUNNEL_TAILSCALE_STATE_DIR", isPath: true},
	{key: "tunnel.allow_terminal", env: "TUNNEL_ALLOW_TERMINAL"},
	{key: "tunnel.allowed_ips", env: "TUNNEL_ALLOWED_IPS"},
	{key: "tunnel.rate_limiting.enabled", env: "TUNNEL_RATE_LIMITING_ENABLED"},
	{key: "tunnel.rate_limiting.login_attempts", env: "TUNNEL_RATE_LIMITING_LOGIN_ATTEMPTS"},
	{key: "tunnel.rate_limiting.window_seconds", env: "TUNNEL_RATE_LIMITING_WINDOW_SECONDS"},
	{key: "tunnel.rate_limiting.block_duration_seconds", env: "TUNNEL_RATE_LIMITING_BLOCK_DURATION_SECONDS"},

	// Bots
	{key: "bots.provider", env: "BOTS_PROVIDER"},
	{key: "bots.safe_mode", env: "BOTS_SAFE_MODE"},
	{key: "bots.telegram.token", env: "BOTS_TELEGRAM_TOKEN"},
	{key: "bots.telegram.allowed_chat_ids", env: "BOTS_TELEGRAM_ALLOWED_CHAT_IDS"},
	{key: "bots.telegram.interested_event_types", env: "BOTS_TELEGRAM_INTERESTED_EVENT_TYPES"},
	{key: "bots.slack.bot_token", env: "BOTS_SLACK_BOT_TOKEN"},
	{key: "bots.slack.app_token", env: "BOTS_SLACK_APP_TOKEN"},
	{key: "bots.slack.allowed_channel_ids", env: "BOTS_SLACK_ALLOWED_CHANNEL_IDS"},
	{key: "bots.slack.interested_event_types", env: "BOTS_SLACK_INTERESTED_EVENT_TYPES"},
	{key: "bots.slack.respond_to_all", env: "BOTS_SLACK_RESPOND_TO_ALL"},
	{key: "bots.discord.token", env: "BOTS_DISCORD_TOKEN"},
	{key: "bots.discord.allowed_channel_ids", env: "BOTS_DISCORD_ALLOWED_CHANNEL_IDS"},
	{key: "bots.discord.interested_event_types", env: "BOTS_DISCORD_INTERESTED_EVENT_TYPES"},
	{key: "bots.discord.respond_to_all", env: "BOTS_DISCORD_RESPOND_TO_ALL"},

	// License
	{key: "license.key", env: "LICENSE_KEY"},
	{key: "license.cloud_url", env: "LICENSE_CLOUD_URL"},

	// GitSync
	{key: "git_sync.enabled", env: "GITSYNC_ENABLED"},
	{key: "git_sync.repository", env: "GITSYNC_REPOSITORY"},
	{key: "git_sync.branch", env: "GITSYNC_BRANCH"},
	{key: "git_sync.path", env: "GITSYNC_PATH"},
	{key: "git_sync.push_enabled", env: "GITSYNC_PUSH_ENABLED"},
	{key: "git_sync.auth.type", env: "GITSYNC_AUTH_TYPE"},
	{key: "git_sync.auth.token", env: "GITSYNC_AUTH_TOKEN"},
	{key: "git_sync.auth.ssh_key_path", env: "GITSYNC_AUTH_SSH_KEY_PATH", isPath: true},
	{key: "git_sync.auth.ssh_passphrase", env: "GITSYNC_AUTH_SSH_PASSPHRASE"},
	{key: "git_sync.auto_sync.enabled", env: "GITSYNC_AUTOSYNC_ENABLED"},
	{key: "git_sync.auto_sync.on_startup", env: "GITSYNC_AUTOSYNC_ON_STARTUP"},
	{key: "git_sync.auto_sync.interval", env: "GITSYNC_AUTOSYNC_INTERVAL"},
	{key: "git_sync.commit.author_name", env: "GITSYNC_COMMIT_AUTHOR_NAME"},
	{key: "git_sync.commit.author_email", env: "GITSYNC_COMMIT_AUTHOR_EMAIL"},
}

func (l *ConfigLoader) bindEnvironmentVariables() {
	prefix := strings.ToUpper(AppSlug) + "_"

	for _, b := range envBindings {
		fullEnv := prefix + b.env

		if b.isPath {
			if val := os.Getenv(fullEnv); val != "" {
				if abs, err := filepath.Abs(val); err == nil && abs != val {
					_ = os.Setenv(fullEnv, abs)
				}
			}
		}

		_ = l.v.BindEnv(b.key, fullEnv)
	}
}

func (l *ConfigLoader) configureViper(configDir, configFile string) {
	if configFile == "" {
		l.v.AddConfigPath(configDir)
		l.v.SetConfigName("config")
	} else {
		l.v.SetConfigFile(configFile)
	}
	l.v.SetConfigType("yaml")
	l.v.SetEnvPrefix(strings.ToUpper(AppSlug))
	l.v.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	l.v.AutomaticEnv()
}

// parseLabels parses labels from comma-separated strings or map types.
func parseLabels(input any) map[string]string {
	labels := make(map[string]string)

	switch v := input.(type) {
	case string:
		if v == "" {
			return labels
		}
		for pair := range strings.SplitSeq(v, ",") {
			pair = strings.TrimSpace(pair)
			if pair == "" {
				continue
			}
			if key, value, found := strings.Cut(pair, "="); found {
				key = strings.TrimSpace(key)
				value = strings.TrimSpace(value)
				if key != "" {
					labels[key] = value
				}
			}
		}
	case map[string]any:
		for key, val := range v {
			if strVal, ok := val.(string); ok {
				labels[key] = strVal
			}
		}
	case map[any]any:
		for key, val := range v {
			if keyStr, ok := key.(string); ok {
				if valStr, ok := val.(string); ok {
					labels[keyStr] = valStr
				}
			}
		}
	}

	return labels
}

// parseStringList parses comma-separated strings or string slices, filtering empty entries.
func parseStringList(input any) []string {
	var result []string

	switch v := input.(type) {
	case string:
		if v != "" {
			for s := range strings.SplitSeq(v, ",") {
				if trimmed := strings.TrimSpace(s); trimmed != "" {
					result = append(result, trimmed)
				}
			}
		}
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok {
				if trimmed := strings.TrimSpace(s); trimmed != "" {
					result = append(result, trimmed)
				}
			}
		}
	case []string:
		for _, s := range v {
			if trimmed := strings.TrimSpace(s); trimmed != "" {
				result = append(result, trimmed)
			}
		}
	}

	return result
}

func cleanServerBasePath(s string) string {
	if s == "" {
		return ""
	}

	// Clean and ensure absolute path
	cleanPath := path.Clean(s)
	if !path.IsAbs(cleanPath) {
		cleanPath = path.Join("/", cleanPath)
	}

	// Root path is equivalent to no base path
	if cleanPath == "/" {
		return ""
	}
	return cleanPath
}

func getHomeDir() (string, error) {
	dir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("could not determine home directory: %w", err)
	}
	return dir, nil
}
