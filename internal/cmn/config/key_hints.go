// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"fmt"
	"strings"

	"github.com/spf13/viper"
)

// legacyToSnakeCaseKey maps lowercased camelCase config keys to their snake_case equivalents.
// Viper lowercases all keys internally, so "basePath" becomes "basepath" while
// our new key "base_path" stays as "base_path". We detect legacy keys by scanning
// Viper's key list for these lowercased forms.
var legacyToSnakeCaseKey = map[string]string{
	// Top-level
	"basepath":               "base_path",
	"apibasepath":            "api_base_path",
	"apibaseurl":             "api_base_url",
	"checkupdates":           "check_updates",
	"defaultshell":           "default_shell",
	"logformat":              "log_format",
	"envpassthrough":         "env_passthrough",
	"envpassthroughprefixes": "env_passthrough_prefixes",
	"permissionwritedags":    "permission_write_dags",
	"permissionrundags":      "permission_run_dags",
	"dagsdir":                "dags_dir",
	"logdir":                 "log_dir",
	"artifactdir":            "artifact_dir",
	"datadir":                "data_dir",
	"suspendflagsdir":        "suspend_flags_dir",
	"adminlogsdir":           "admin_logs_dir",
	"eventstoredir":          "event_store_dir",
	"baseconfig":             "base_config",
	"logencodingcharset":     "log_encoding_charset",
	"navbarcolor":            "navbar_color",
	"navbartitle":            "navbar_title",
	"maxdashboardpagelimit":  "max_dashboard_page_limit",
	"lateststatustoday":      "latest_status_today",
	"remotenodes":            "remote_nodes",
	"defaultexecutionmode":   "default_execution_mode",
	"skipexamples":           "skip_examples",
	"gitsync":                "git_sync",

	// TLS
	"tls.certfile": "tls.cert_file",
	"tls.keyfile":  "tls.key_file",
	"tls.cafile":   "tls.ca_file",

	// Auth OIDC
	"auth.oidc.clientid":                        "auth.oidc.client_id",
	"auth.oidc.clientsecret":                    "auth.oidc.client_secret",
	"auth.oidc.clienturl":                       "auth.oidc.client_url",
	"auth.oidc.autosignup":                      "auth.oidc.auto_signup",
	"auth.oidc.alloweddomains":                  "auth.oidc.allowed_domains",
	"auth.oidc.buttonlabel":                     "auth.oidc.button_label",
	"auth.oidc.rolemapping":                     "auth.oidc.role_mapping",
	"auth.oidc.rolemapping.defaultrole":         "auth.oidc.role_mapping.default_role",
	"auth.oidc.rolemapping.groupsclaim":         "auth.oidc.role_mapping.groups_claim",
	"auth.oidc.rolemapping.groupmappings":       "auth.oidc.role_mapping.group_mappings",
	"auth.oidc.rolemapping.roleattributepath":   "auth.oidc.role_mapping.role_attribute_path",
	"auth.oidc.rolemapping.roleattributestrict": "auth.oidc.role_mapping.role_attribute_strict",
	"auth.oidc.rolemapping.skiporgrolesync":     "auth.oidc.role_mapping.skip_org_role_sync",

	// Permissions
	"permissions.writedags": "permissions.write_dags",
	"permissions.rundags":   "permissions.run_dags",

	// Paths
	"paths.dagsdir":            "paths.dags_dir",
	"paths.logdir":             "paths.log_dir",
	"paths.artifactdir":        "paths.artifact_dir",
	"paths.datadir":            "paths.data_dir",
	"paths.suspendflagsdir":    "paths.suspend_flags_dir",
	"paths.adminlogsdir":       "paths.admin_logs_dir",
	"paths.eventstoredir":      "paths.event_store_dir",
	"paths.baseconfig":         "paths.base_config",
	"paths.altdagsdir":         "paths.alt_dags_dir",
	"paths.dagrunsdir":         "paths.dag_runs_dir",
	"paths.queuedir":           "paths.queue_dir",
	"paths.procdir":            "paths.proc_dir",
	"paths.serviceregistrydir": "paths.service_registry_dir",
	"paths.usersdir":           "paths.users_dir",
	"paths.apikeysdir":         "paths.api_keys_dir",
	"paths.webhooksdir":        "paths.webhooks_dir",
	"paths.sessionsdir":        "paths.sessions_dir",

	// UI
	"ui.logencodingcharset":    "ui.log_encoding_charset",
	"ui.navbarcolor":           "ui.navbar_color",
	"ui.navbartitle":           "ui.navbar_title",
	"ui.maxdashboardpagelimit": "ui.max_dashboard_page_limit",
	"ui.dags.sortfield":        "ui.dags.sort_field",
	"ui.dags.sortorder":        "ui.dags.sort_order",

	// Peer
	"peer.certfile":      "peer.cert_file",
	"peer.keyfile":       "peer.key_file",
	"peer.clientcafile":  "peer.client_ca_file",
	"peer.skiptlsverify": "peer.skip_tls_verify",
	"peer.maxretries":    "peer.max_retries",
	"peer.retryinterval": "peer.retry_interval",

	// Remote nodes (array items use lowercased camelCase too)
	"remotenodes.apibaseurl":        "remote_nodes.api_base_url",
	"remotenodes.authtype":          "remote_nodes.auth_type",
	"remotenodes.authmode":          "remote_nodes.auth_type",
	"remotenodes.isbasicauth":       "remote_nodes.auth_type (use value \"basic\" instead of boolean)",
	"remotenodes.isauthtoken":       "remote_nodes.auth_type (use value \"token\" instead of boolean)",
	"remotenodes.basicauthusername": "remote_nodes.basic_auth_username",
	"remotenodes.basicauthpassword": "remote_nodes.basic_auth_password",
	"remotenodes.authtoken":         "remote_nodes.auth_token",
	"remotenodes.skiptlsverify":     "remote_nodes.skip_tls_verify",

	// Worker
	"worker.maxactiveruns":                "worker.max_active_runs",
	"worker.postgrespool":                 "worker.postgres_pool",
	"worker.postgrespool.maxopenconns":    "worker.postgres_pool.max_open_conns",
	"worker.postgrespool.maxidleconns":    "worker.postgres_pool.max_idle_conns",
	"worker.postgrespool.connmaxlifetime": "worker.postgres_pool.conn_max_lifetime",
	"worker.postgrespool.connmaxidletime": "worker.postgres_pool.conn_max_idle_time",

	// Control-plane store
	"controlplanestore":                                             "control_plane_store",
	"controlplanestore.backend":                                     "control_plane_store.backend",
	"controlplanestore.postgres.server.automigrate":                 "control_plane_store.postgres.server.auto_migrate",
	"controlplanestore.postgres.server.pool.maxopenconns":           "control_plane_store.postgres.server.pool.max_open_conns",
	"controlplanestore.postgres.server.pool.maxidleconns":           "control_plane_store.postgres.server.pool.max_idle_conns",
	"controlplanestore.postgres.server.pool.connmaxlifetime":        "control_plane_store.postgres.server.pool.conn_max_lifetime",
	"controlplanestore.postgres.server.pool.connmaxidletime":        "control_plane_store.postgres.server.pool.conn_max_idle_time",
	"controlplanestore.postgres.scheduler.automigrate":              "control_plane_store.postgres.scheduler.auto_migrate",
	"controlplanestore.postgres.scheduler.pool.maxopenconns":        "control_plane_store.postgres.scheduler.pool.max_open_conns",
	"controlplanestore.postgres.scheduler.pool.maxidleconns":        "control_plane_store.postgres.scheduler.pool.max_idle_conns",
	"controlplanestore.postgres.scheduler.pool.connmaxlifetime":     "control_plane_store.postgres.scheduler.pool.conn_max_lifetime",
	"controlplanestore.postgres.scheduler.pool.connmaxidletime":     "control_plane_store.postgres.scheduler.pool.conn_max_idle_time",
	"controlplanestore.postgres.coordinator.automigrate":            "control_plane_store.postgres.coordinator.auto_migrate",
	"controlplanestore.postgres.coordinator.pool.maxopenconns":      "control_plane_store.postgres.coordinator.pool.max_open_conns",
	"controlplanestore.postgres.coordinator.pool.maxidleconns":      "control_plane_store.postgres.coordinator.pool.max_idle_conns",
	"controlplanestore.postgres.coordinator.pool.connmaxlifetime":   "control_plane_store.postgres.coordinator.pool.conn_max_lifetime",
	"controlplanestore.postgres.coordinator.pool.connmaxidletime":   "control_plane_store.postgres.coordinator.pool.conn_max_idle_time",
	"controlplanestore.postgres.agent.automigrate":                  "control_plane_store.postgres.agent.auto_migrate",
	"controlplanestore.postgres.agent.directaccess":                 "control_plane_store.postgres.agent.direct_access",
	"controlplanestore.postgres.agent.pool.maxopenconns":            "control_plane_store.postgres.agent.pool.max_open_conns",
	"controlplanestore.postgres.agent.pool.maxidleconns":            "control_plane_store.postgres.agent.pool.max_idle_conns",
	"controlplanestore.postgres.agent.pool.connmaxlifetime":         "control_plane_store.postgres.agent.pool.conn_max_lifetime",
	"controlplanestore.postgres.agent.pool.connmaxidletime":         "control_plane_store.postgres.agent.pool.conn_max_idle_time",
	"control_plane_store.postgres.server.automigrate":               "control_plane_store.postgres.server.auto_migrate",
	"control_plane_store.postgres.server.pool.maxopenconns":         "control_plane_store.postgres.server.pool.max_open_conns",
	"control_plane_store.postgres.server.pool.maxidleconns":         "control_plane_store.postgres.server.pool.max_idle_conns",
	"control_plane_store.postgres.server.pool.connmaxlifetime":      "control_plane_store.postgres.server.pool.conn_max_lifetime",
	"control_plane_store.postgres.server.pool.connmaxidletime":      "control_plane_store.postgres.server.pool.conn_max_idle_time",
	"control_plane_store.postgres.scheduler.automigrate":            "control_plane_store.postgres.scheduler.auto_migrate",
	"control_plane_store.postgres.scheduler.pool.maxopenconns":      "control_plane_store.postgres.scheduler.pool.max_open_conns",
	"control_plane_store.postgres.scheduler.pool.maxidleconns":      "control_plane_store.postgres.scheduler.pool.max_idle_conns",
	"control_plane_store.postgres.scheduler.pool.connmaxlifetime":   "control_plane_store.postgres.scheduler.pool.conn_max_lifetime",
	"control_plane_store.postgres.scheduler.pool.connmaxidletime":   "control_plane_store.postgres.scheduler.pool.conn_max_idle_time",
	"control_plane_store.postgres.coordinator.automigrate":          "control_plane_store.postgres.coordinator.auto_migrate",
	"control_plane_store.postgres.coordinator.pool.maxopenconns":    "control_plane_store.postgres.coordinator.pool.max_open_conns",
	"control_plane_store.postgres.coordinator.pool.maxidleconns":    "control_plane_store.postgres.coordinator.pool.max_idle_conns",
	"control_plane_store.postgres.coordinator.pool.connmaxlifetime": "control_plane_store.postgres.coordinator.pool.conn_max_lifetime",
	"control_plane_store.postgres.coordinator.pool.connmaxidletime": "control_plane_store.postgres.coordinator.pool.conn_max_idle_time",
	"control_plane_store.postgres.agent.automigrate":                "control_plane_store.postgres.agent.auto_migrate",
	"control_plane_store.postgres.agent.directaccess":               "control_plane_store.postgres.agent.direct_access",
	"control_plane_store.postgres.agent.pool.maxopenconns":          "control_plane_store.postgres.agent.pool.max_open_conns",
	"control_plane_store.postgres.agent.pool.maxidleconns":          "control_plane_store.postgres.agent.pool.max_idle_conns",
	"control_plane_store.postgres.agent.pool.connmaxlifetime":       "control_plane_store.postgres.agent.pool.conn_max_lifetime",
	"control_plane_store.postgres.agent.pool.connmaxidletime":       "control_plane_store.postgres.agent.pool.conn_max_idle_time",

	// Scheduler
	"scheduler.lockstalethreshold":      "scheduler.lock_stale_threshold",
	"scheduler.lockretryinterval":       "scheduler.lock_retry_interval",
	"scheduler.zombiedetectioninterval": "scheduler.zombie_detection_interval",
	"scheduler.heartbeatinterval":       "scheduler.heartbeat_interval",
	"scheduler.heartbeatsyncinterval":   "scheduler.heartbeat_sync_interval",
	"scheduler.stalethreshold":          "scheduler.stale_threshold",
	"scheduler.failurethreshold":        "scheduler.failure_threshold",

	// Proc
	"proc.heartbeatinterval":     "proc.heartbeat_interval",
	"proc.heartbeatsyncinterval": "proc.heartbeat_sync_interval",
	"proc.stalethreshold":        "proc.stale_threshold",

	// Queue
	"queues.config.maxactiveruns":  "queues.config.max_active_runs",
	"queues.config.maxconcurrency": "queues.config.max_concurrency",

	// Audit
	"audit.retentiondays":      "audit.retention_days",
	"eventstore":               "event_store",
	"eventstore.enabled":       "event_store.enabled",
	"eventstore.retentiondays": "event_store.retention_days",

	// GitSync
	"gitsync.enabled":            "git_sync.enabled",
	"gitsync.pushenabled":        "git_sync.push_enabled",
	"gitsync.autosync":           "git_sync.auto_sync",
	"gitsync.autosync.onstartup": "git_sync.auto_sync.on_startup",
	"gitsync.auth.sshkeypath":    "git_sync.auth.ssh_key_path",
	"gitsync.auth.sshpassphrase": "git_sync.auth.ssh_passphrase",
	"gitsync.commit.authorname":  "git_sync.commit.author_name",
	"gitsync.commit.authoremail": "git_sync.commit.author_email",

	// Tunnel
	"tunnel.allowterminal":                     "tunnel.allow_terminal",
	"tunnel.allowedips":                        "tunnel.allowed_ips",
	"tunnel.ratelimiting":                      "tunnel.rate_limiting",
	"tunnel.ratelimiting.loginattempts":        "tunnel.rate_limiting.login_attempts",
	"tunnel.ratelimiting.windowseconds":        "tunnel.rate_limiting.window_seconds",
	"tunnel.ratelimiting.blockdurationseconds": "tunnel.rate_limiting.block_duration_seconds",
	"tunnel.tailscale.authkey":                 "tunnel.tailscale.auth_key",
	"tunnel.tailscale.statedir":                "tunnel.tailscale.state_dir",
}

// checkForLegacyKeys scans Viper's key list for legacy camelCase keys and returns
// an error with suggestions if any are found.
func checkForLegacyKeys(v *viper.Viper) error {
	var suggestions []string

	for _, key := range v.AllKeys() {
		if snake, ok := legacyToSnakeCaseKey[key]; ok {
			suggestions = append(suggestions, fmt.Sprintf("%s -> %s", key, snake))
		}
	}

	if len(suggestions) == 0 {
		return nil
	}

	return fmt.Errorf(
		"config file uses legacy camelCase keys; migrate to snake_case: %s",
		strings.Join(suggestions, ", "),
	)
}
