// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"github.com/dagucloud/dagu/internal/cmn/stringutil"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// commandLineFlag defines a CLI flag with its configuration options.
type commandLineFlag struct {
	name         string
	shorthand    string
	defaultValue string
	usage        string
	required     bool
	isBool       bool
	hidden       bool
	bindViper    bool
	viperKey     string // Custom viper key (if different from kebab-to-camel name)
}

// Base flags included in all commands
var (
	configFlag = commandLineFlag{
		name:      "config",
		shorthand: "c",
		usage:     "Path to the configuration file (default: $HOME/.config/dagu/config.yaml)",
		bindViper: true,
	}

	daguHomeFlag = commandLineFlag{
		name:  "dagu-home",
		usage: "Override DAGU_HOME for this command",
	}

	quietFlag = commandLineFlag{
		name:      "quiet",
		shorthand: "q",
		usage:     "Suppress output during dag-run",
		isBool:    true,
	}

	cpuProfileFlag = commandLineFlag{
		name:   "cpu-profile",
		usage:  "Enable CPU profiling (saves to cpu.pprof)",
		isBool: true,
	}
)

// Server and directory flags
var (
	dagsFlag = commandLineFlag{
		name:      "dags",
		shorthand: "d",
		usage:     "Directory containing DAG files (default: $HOME/.config/dagu/dags)",
		bindViper: true,
	}

	hostFlag = commandLineFlag{
		name:         "host",
		shorthand:    "s",
		defaultValue: "localhost",
		usage:        "Server hostname or IP address (default: localhost)",
		bindViper:    true,
	}

	portFlag = commandLineFlag{
		name:         "port",
		shorthand:    "p",
		defaultValue: "8080",
		usage:        "Server port number (default: 8080)",
		bindViper:    true,
	}
)

// DAG execution flags
var (
	paramsFlag = commandLineFlag{
		name:      "params",
		shorthand: "p",
		usage:     "Parameters to pass to the dag-run (overrides DAG defaults; supports positional values and key=value pairs, e.g., P1=foo P2=bar)",
	}

	nameFlag = commandLineFlag{
		name:      "name",
		shorthand: "N",
		usage:     "Override the DAG name (default: name from DAG definition or filename)",
	}

	queueFlag = commandLineFlag{
		name:      "queue",
		shorthand: "u",
		usage:     "Override the DAG-level queue definition",
	}

	labelsFlag = commandLineFlag{
		name:  "labels",
		usage: "Additional labels (comma-separated key=value or key-only, e.g., workspace=foo,env=prod)",
	}

	tagsFlag = commandLineFlag{
		name:  "tags",
		usage: "Deprecated alias for --labels",
	}

	defaultWorkingDirFlag = commandLineFlag{
		name:  "default-working-dir",
		usage: "Default working directory for DAGs without explicit workingDir",
	}

	profileFlag = commandLineFlag{
		name:  "profile",
		usage: "Runtime profile to inject into the DAG run",
	}
)

// DAG run ID flags for different commands
var (
	dagRunIDFlag = commandLineFlag{
		name:      "run-id",
		shorthand: "r",
		usage:     "Unique dag-run ID for starting a new dag-run",
	}

	dagRunIDFlagRetry = commandLineFlag{
		name:      "run-id",
		shorthand: "r",
		usage:     "Unique dag-run ID to retry a dag-run",
		required:  true,
	}

	dagRunIDFlagStop = commandLineFlag{
		name:      "run-id",
		shorthand: "r",
		usage:     "dag-run ID for stopping a dag-run",
	}

	dagRunIDFlagRestart = commandLineFlag{
		name:      "run-id",
		shorthand: "r",
		usage:     "dag-run ID for restarting a dag-run",
	}

	dagRunIDFlagStatus = commandLineFlag{
		name:      "run-id",
		shorthand: "r",
		usage:     "dag-run ID for checking the status of a dag-run",
	}

	subDAGRunIDFlagStatus = commandLineFlag{
		name:      "sub-run-id",
		shorthand: "s",
		usage:     "Sub dag-run ID for checking the status of a nested dag-run (requires --run-id)",
	}

	dagRunFlagDequeue = commandLineFlag{
		name:      "dag-run",
		shorthand: "d",
		usage:     "<DAG-name>:<run-id> to dequeue a dag-run",
	}

	stepNameForRetry = commandLineFlag{
		name:  "step",
		usage: "Retry only the specified step (optional)",
	}
)

// Sub DAG run flags
var (
	rootDAGRunFlag = commandLineFlag{
		name:   "root",
		usage:  "[only for sub dag-runs] reference for the root dag-run",
		hidden: true,
	}

	parentDAGRunFlag = commandLineFlag{
		name:   "parent",
		usage:  "[only for sub dag-runs] reference for the parent dag-run",
		hidden: true,
	}
)

// Coordinator flags
var (
	coordinatorHostFlag = commandLineFlag{
		name:         "coordinator.host",
		shorthand:    "H",
		defaultValue: "127.0.0.1",
		usage:        "Coordinator gRPC server host (default: 127.0.0.1)",
		bindViper:    true,
	}

	coordinatorPortFlag = commandLineFlag{
		name:         "coordinator.port",
		shorthand:    "P",
		defaultValue: "50055",
		usage:        "Coordinator gRPC server port (default: 50055)",
		bindViper:    true,
	}

	coordinatorHealthPortFlag = commandLineFlag{
		name:         "coordinator.health-port",
		defaultValue: "8091",
		usage:        "Coordinator HTTP health check server port (default: 8091, 0 disables)",
		bindViper:    true,
		viperKey:     "coordinator.health_port",
	}

	coordinatorAdvertiseFlag = commandLineFlag{
		name:      "coordinator.advertise",
		shorthand: "A",
		usage:     "Address to advertise in service registry (default: auto-detected hostname)",
		bindViper: true,
	}
)

// Worker flags
var (
	workerIDFlag = commandLineFlag{
		name:      "worker.id",
		shorthand: "w",
		usage:     "Worker instance ID (default: hostname@PID)",
		bindViper: true,
	}

	workerMaxActiveRunsFlag = commandLineFlag{
		name:         "worker.max-active-runs",
		shorthand:    "m",
		defaultValue: "100",
		usage:        "Maximum number of active runs (default: 100)",
		bindViper:    true,
		viperKey:     "worker.max_active_runs",
	}

	workerLabelsFlag = commandLineFlag{
		name:      "worker.labels",
		shorthand: "l",
		usage:     "Worker labels for capability matching (format: key1=value1,key2=value2)",
		bindViper: true,
	}

	workerHealthPortFlag = commandLineFlag{
		name:         "worker.health-port",
		defaultValue: "8092",
		usage:        "Worker HTTP health check server port (default: 8092, 0 disables)",
		bindViper:    true,
		viperKey:     "worker.health_port",
	}

	workerCoordinatorsFlag = commandLineFlag{
		name:      "worker.coordinators",
		usage:     "Coordinator addresses for static discovery (format: host1:port1,host2:port2)",
		bindViper: true,
	}
)

// Peer TLS flags
var (
	peerInsecureFlag = commandLineFlag{
		name:      "peer.insecure",
		usage:     "Use insecure connection (h2c) instead of TLS",
		isBool:    true,
		bindViper: true,
	}

	peerCertFileFlag = commandLineFlag{
		name:      "peer.cert-file",
		usage:     "Path to TLS certificate file for mutual TLS",
		bindViper: true,
		viperKey:  "peer.cert_file",
	}

	peerKeyFileFlag = commandLineFlag{
		name:      "peer.key-file",
		usage:     "Path to TLS key file for mutual TLS",
		bindViper: true,
		viperKey:  "peer.key_file",
	}

	peerClientCAFileFlag = commandLineFlag{
		name:      "peer.client-ca-file",
		usage:     "Path to CA certificate file for server verification",
		bindViper: true,
		viperKey:  "peer.client_ca_file",
	}

	peerSkipTLSVerifyFlag = commandLineFlag{
		name:      "peer.skip-tls-verify",
		usage:     "Skip TLS certificate verification (insecure)",
		isBool:    true,
		bindViper: true,
		viperKey:  "peer.skip_tls_verify",
	}
)

// Cleanup and utility flags
var (
	retentionDaysFlag = commandLineFlag{
		name:         "retention-days",
		defaultValue: "0",
		usage:        "Number of days to retain history (0 = delete all, except active runs)",
	}

	dryRunFlag = commandLineFlag{
		name:   "dry-run",
		usage:  "Preview what would be deleted without actually deleting",
		isBool: true,
	}

	yesFlag = commandLineFlag{
		name:      "yes",
		shorthand: "y",
		usage:     "Skip confirmation prompt",
		isBool:    true,
	}
)

// Tunnel flags
var (
	tunnelFlag = commandLineFlag{
		name:      "tunnel",
		shorthand: "t",
		usage:     "Enable tunnel mode for remote access",
		isBool:    true,
		bindViper: true,
		viperKey:  "tunnel.enabled",
	}

	tunnelTokenFlag = commandLineFlag{
		name:      "tunnel-token",
		usage:     "Tailscale auth key for headless authentication",
		bindViper: true,
		viperKey:  "tunnel.token",
	}

	tunnelFunnelFlag = commandLineFlag{
		name:      "tunnel-funnel",
		usage:     "Enable Tailscale Funnel for public internet access (requires Tailscale provider)",
		isBool:    true,
		bindViper: true,
		viperKey:  "tunnel.tailscale.funnel",
	}

	tunnelHTTPSFlag = commandLineFlag{
		name:      "tunnel-https",
		usage:     "Use HTTPS for Tailscale (requires enabling HTTPS in Tailscale admin panel)",
		isBool:    true,
		bindViper: true,
		viperKey:  "tunnel.tailscale.https",
	}
)

// History command flags
var (
	historyFromFlag = commandLineFlag{
		name:  "from",
		usage: "Start date/time for filtering runs in UTC (format: 2006-01-02 or 2006-01-02T15:04:05Z)",
	}

	historyToFlag = commandLineFlag{
		name:  "to",
		usage: "End date/time for filtering runs in UTC (format: 2006-01-02 or 2006-01-02T15:04:05Z)",
	}

	historyLastFlag = commandLineFlag{
		name:  "last",
		usage: "Relative time period (examples: 7d, 24h, 1w, 30d). Cannot be combined with --from or --to",
	}

	historyStatusFlag = commandLineFlag{
		name:  "status",
		usage: "Filter by execution status; accepts a single value or comma-separated values (running, succeeded, failed, aborted, queued, waiting, rejected, not_started, partially_succeeded)",
	}

	historyRunIDFlag = commandLineFlag{
		name:  "run-id",
		usage: "Filter by run ID (supports partial match)",
	}

	historyLabelsFlag = commandLineFlag{
		name:  "labels",
		usage: "Filter by DAG labels, comma-separated with AND logic (e.g., 'prod,critical')",
	}

	historyTagsFlag = commandLineFlag{
		name:  "tags",
		usage: "Deprecated alias for --labels",
	}

	historyFormatFlag = commandLineFlag{
		name:         "format",
		shorthand:    "f",
		defaultValue: "table",
		usage:        "Output format: table, json, or csv (default: table)",
	}

	historyLimitFlag = commandLineFlag{
		name:         "limit",
		shorthand:    "l",
		defaultValue: "100",
		usage:        "Maximum number of results to display (default: 100, max: 1000)",
	}
)

// baseFlags are included in every command.
var baseFlags = []commandLineFlag{configFlag, daguHomeFlag, quietFlag, cpuProfileFlag}

// initFlags registers CLI flags on the provided Cobra command.
// Base flags (config, dagu-home, quiet, cpu-profile) are always included.
func initFlags(cmd *cobra.Command, additionalFlags ...commandLineFlag) {
	allFlags := append(baseFlags, additionalFlags...)

	for _, flag := range allFlags {
		registerFlag(cmd, flag)
	}
}

// registerFlag adds a single flag to the command and marks it required if needed.
func registerFlag(cmd *cobra.Command, flag commandLineFlag) {
	if flag.isBool {
		cmd.Flags().BoolP(flag.name, flag.shorthand, false, flag.usage)
	} else {
		cmd.Flags().StringP(flag.name, flag.shorthand, flag.defaultValue, flag.usage)
	}

	if flag.required {
		_ = cmd.MarkFlagRequired(flag.name)
	}
	if flag.hidden {
		_ = cmd.Flags().MarkHidden(flag.name)
	}
}

// bindFlags binds command-line flags to Viper for configuration lookup.
// Only flags with bindViper=true are bound.
func bindFlags(v *viper.Viper, cmd *cobra.Command, additionalFlags ...commandLineFlag) {
	allFlags := append([]commandLineFlag{configFlag}, additionalFlags...)

	for _, flag := range allFlags {
		if !flag.bindViper {
			continue
		}

		key := flag.viperKey
		if key == "" {
			key = stringutil.KebabToCamel(flag.name)
		}
		_ = v.BindPFlag(key, cmd.Flags().Lookup(flag.name))
	}
}
