// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/spf13/viper"

	"github.com/dagucloud/dagu/internal/cmn/cmdutil"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/signalctx"
	"github.com/dagucloud/dagu/internal/core"
	exec1 "github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/core/spec"
	"github.com/dagucloud/dagu/internal/persis/filebaseconfig"
	"github.com/dagucloud/dagu/internal/persis/filedag"
	"github.com/dagucloud/dagu/internal/persis/filedagrun"
	"github.com/dagucloud/dagu/internal/persis/filedistributed"
	"github.com/dagucloud/dagu/internal/persis/filequeue"
	"github.com/dagucloud/dagu/internal/persis/fileserviceregistry"
	runtimepkg "github.com/dagucloud/dagu/internal/runtime"
	"github.com/dagucloud/dagu/internal/runtime/agent"
	"github.com/dagucloud/dagu/internal/service/frontend"
	"github.com/dagucloud/dagu/internal/workspace"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

var setupLock sync.Mutex
var builtExecutableOnce sync.Once
var builtExecutablePath string
var builtExecutableErr error

var (
	latestStatusAssertTimeout  = helperLatestStatusAssertTimeout()
	latestStatusAssertInterval = 1 * time.Second
)

func helperLatestStatusAssertTimeout() time.Duration {
	if runtime.GOOS == "windows" {
		return 2 * time.Minute
	}
	return 30 * time.Second
}

// HelperOption defines functional options for Helper
type HelperOption func(*Options)

type Options struct {
	CaptureLoggingOutput bool // CaptureLoggingOutput enables capturing of logging output
	DAGsDir              string
	ServerConfig         *config.Server
	ConfigMutators       []func(*config.Config)
	CoordinatorHost      string
	CoordinatorPort      int
	ServerOptions        []frontend.ServerOption
	UseBuiltExecutable   bool // UseBuiltExecutable builds the current ./cmd binary for subprocess-based tests
	// Coordinator handler options for shared-nothing worker tests
	WithStatusPersistence   bool          // Enable status persistence via DAGRunStore
	WithLogPersistence      bool          // Enable log persistence to filesystem
	WithArtifactPersistence bool          // Enable artifact persistence to filesystem
	StaleHeartbeatThreshold time.Duration // Override for handler's stale heartbeat threshold
	StaleLeaseThreshold     time.Duration // Override for handler's stale lease threshold
}

// WithCaptureLoggingOutput creates a logging capture option
func WithCaptureLoggingOutput() HelperOption {
	return func(opts *Options) {
		opts.CaptureLoggingOutput = true
	}
}

func WithDAGsDir(dir string) HelperOption {
	return func(opts *Options) {
		opts.DAGsDir = dir
	}
}

func WithServerConfig(cfg *config.Server) HelperOption {
	return func(opts *Options) {
		opts.ServerConfig = cfg
	}
}

// WithCoordinatorEnabled re-enables the coordinator in test configuration.
// By default, tests disable the coordinator since no coordinator is running.
func WithCoordinatorEnabled() HelperOption {
	return WithConfigMutator(func(cfg *config.Config) {
		cfg.Coordinator.Enabled = true
	})
}

// WithConfigMutator applies mutations to the loaded configuration after defaults are set.
func WithConfigMutator(mutator func(*config.Config)) HelperOption {
	return func(opts *Options) {
		opts.ConfigMutators = append(opts.ConfigMutators, mutator)
	}
}

// WithStatusPersistence enables status persistence via DAGRunStore on the coordinator handler.
// Use this for testing remote status pushing from workers.
func WithStatusPersistence() HelperOption {
	return func(opts *Options) {
		opts.WithStatusPersistence = true
	}
}

// WithLogPersistence enables log persistence to filesystem on the coordinator handler.
// Use this for testing remote log streaming from workers.
func WithLogPersistence() HelperOption {
	return func(opts *Options) {
		opts.WithLogPersistence = true
	}
}

// WithArtifactPersistence enables artifact persistence to filesystem on the coordinator handler.
// Use this for testing remote artifact uploads from workers.
func WithArtifactPersistence() HelperOption {
	return func(opts *Options) {
		opts.WithArtifactPersistence = true
	}
}

// WithServerOptions appends frontend.ServerOption values to be passed when creating the test server.
func WithServerOptions(serverOpts ...frontend.ServerOption) HelperOption {
	return func(opts *Options) {
		opts.ServerOptions = append(opts.ServerOptions, serverOpts...)
	}
}

// WithStaleThresholds overrides the shared heartbeat and lease staleness
// thresholds used by distributed test helpers. Useful for tests that need
// faster zombie detection or dispatch reservation expiry.
func WithStaleThresholds(heartbeat, lease time.Duration) HelperOption {
	return func(opts *Options) {
		opts.StaleHeartbeatThreshold = heartbeat
		opts.StaleLeaseThreshold = lease
	}
}

// WithBuiltExecutable makes Setup build the current ./cmd binary once and use it
// as cfg.Paths.Executable for subprocess-based tests.
func WithBuiltExecutable() HelperOption {
	return func(opts *Options) {
		opts.UseBuiltExecutable = true
	}
}

// Setup creates and returns a Helper preconfigured for tests.
//
// Setup prepares an isolated test environment: it creates a temporary DAGU_HOME, writes a minimal config file, initializes stores and a runtime manager, sets key environment variables (e.g. DEBUG, CI, TZ, DAGU_EXECUTABLE, DAGU_CONFIG, SHELL), installs a cancellable context, and registers cleanup to restore the working directory and remove the temp directory. Use the returned Helper to interact with the test runtime and stores.
func Setup(t *testing.T, opts ...HelperOption) Helper {
	setupLock.Lock()
	defer setupLock.Unlock()

	// Save the original working directory and restore it after the test.
	// This is important because some tests (via agent) may change the working
	// directory to a temp directory that gets cleaned up, which would cause
	// subsequent tests to fail when they call os.Getwd().
	origWD, err := os.Getwd()
	if err == nil {
		t.Cleanup(func() {
			_ = os.Chdir(origWD)
		})
	}

	var options Options
	for _, opt := range opts {
		opt(&options)
	}

	// Keep test time-dependent subprocess behavior deterministic.
	_ = os.Setenv("TZ", "UTC")

	random := uuid.New().String()
	tmpDir := fileutil.MustTempDir(fmt.Sprintf("dagu-test-%s", random))
	if runtime.GOOS == "windows" {
		if resolved, err := filepath.EvalSymlinks(tmpDir); err == nil {
			tmpDir = resolved
		}
	}
	shellPath := testShellPath(t)

	root := getProjectRoot(t)
	executablePath := filepath.Join(root, ".local", "bin", "dagu")
	if runtime.GOOS == "windows" {
		executablePath += ".exe"
	}
	if options.UseBuiltExecutable {
		executablePath = buildCurrentExecutable(t, root)
	}

	ctx := signalctx.WithOSSignalsDisabled(createDefaultContext())
	// Use a fresh viper instance to avoid any global state issues between tests.
	v := viper.New()
	loader := config.NewConfigLoader(v, config.WithAppHomeDir(tmpDir))
	cfg, loadErr := loader.Load()
	require.NoError(t, loadErr)

	cfg.Core.Debug = true
	cfg.Core.TZ = "UTC"
	cfg.Core.Location = time.UTC
	cfg.Core.TzOffsetInSec = 0
	cfg.Core.DefaultShell = shellPath
	cfg.Paths.Executable = executablePath
	cfg.Paths.LogDir = filepath.Join(tmpDir, "logs")
	dataDir := filepath.Join(tmpDir, "data")
	cfg.Paths.DataDir = dataDir
	cfg.Paths.DAGRunsDir = filepath.Join(dataDir, "dag-runs")
	cfg.Paths.QueueDir = filepath.Join(dataDir, "queue")
	cfg.Paths.ProcDir = filepath.Join(dataDir, "proc")
	cfg.Paths.ServiceRegistryDir = filepath.Join(dataDir, "service-registry")
	cfg.Paths.UsersDir = filepath.Join(dataDir, "users")
	cfg.Paths.SuspendFlagsDir = filepath.Join(tmpDir, "suspend-flags")
	cfg.Paths.AdminLogsDir = filepath.Join(tmpDir, "admin-logs")
	cfg.Paths.EventStoreDir = filepath.Join(cfg.Paths.AdminLogsDir, "events")
	cfg.Coordinator.Enabled = false
	cfg.Coordinator.HealthPort = 0
	// Default to "none" in tests so auth setup (bcrypt, token generation)
	// doesn't slow down tests that don't need authentication.
	// Tests that need auth can override via WithConfigMutator.
	cfg.Server.Auth.Mode = config.AuthModeNone
	// Tests share the host network namespace, so avoid binding the default
	// scheduler health port unless a test explicitly opts in.
	cfg.Scheduler.Port = 0
	cfg.Worker.HealthPort = 0
	if options.DAGsDir != "" {
		cfg.Paths.DAGsDir = options.DAGsDir
	}

	if options.ServerConfig != nil {
		cfg.Server = *options.ServerConfig
	}
	for _, mutate := range options.ConfigMutators {
		mutate(cfg)
	}

	if options.CoordinatorHost != "" {
		cfg.Coordinator.Host = options.CoordinatorHost
	}
	if options.CoordinatorPort != 0 {
		cfg.Coordinator.Port = options.CoordinatorPort
	}

	configFile := filepath.Join(tmpDir, "config.yaml")
	cfg.Paths.ConfigFileUsed = configFile
	cfg.Core.BaseEnv = config.NewBaseEnv(buildHelperChildEnv(cfg.Core.BaseEnv.AsSlice(), tmpDir, configFile, executablePath, cfg.Core.DefaultShell))
	writeHelperConfigFile(t, cfg, configFile)

	ctx = config.WithConfig(ctx, cfg)

	if cfg.Paths.BaseConfig != "" {
		baseConfigStore, err := filebaseconfig.New(
			cfg.Paths.BaseConfig,
			filebaseconfig.WithSkipDefault(cfg.Core.SkipExamples),
		)
		require.NoError(t, err)
		require.NoError(t, baseConfigStore.Initialize())
	}

	dagStore := filedag.New(
		cfg.Paths.DAGsDir,
		filedag.WithFlagsBaseDir(cfg.Paths.SuspendFlagsDir),
		filedag.WithBaseConfig(cfg.Paths.BaseConfig),
		filedag.WithWorkspaceBaseConfigDir(workspace.BaseConfigDir(cfg.Paths.DAGsDir)),
		filedag.WithSkipExamples(true),
	)
	runStore := filedagrun.New(
		cfg.Paths.DAGRunsDir,
		filedagrun.WithArtifactDir(cfg.Paths.ArtifactDir),
	)
	procStore := newProcStore(cfg)
	queueStore := filequeue.New(cfg.Paths.QueueDir)
	serviceMonitor := fileserviceregistry.New(cfg.Paths.ServiceRegistryDir)
	distributedDir := filepath.Join(cfg.Paths.DataDir, "distributed")
	var dispatchStoreOpts []filedistributed.DispatchTaskStoreOption
	if options.StaleLeaseThreshold > 0 {
		dispatchStoreOpts = append(dispatchStoreOpts, filedistributed.WithDispatchReservationTTL(options.StaleLeaseThreshold))
	}
	dispatchTaskStore := filedistributed.NewDispatchTaskStore(distributedDir, dispatchStoreOpts...)
	workerHeartbeatStore := filedistributed.NewWorkerHeartbeatStore(distributedDir)
	dagRunLeaseStore := filedistributed.NewDAGRunLeaseStore(distributedDir)
	activeDistributedRunStore := filedistributed.NewActiveDistributedRunStore(distributedDir)

	drm := runtimepkg.NewManager(runStore, procStore, cfg)

	helper := Helper{
		Context:                   ctx,
		Config:                    cfg,
		ChildEnv:                  cfg.Core.BaseEnv.AsSlice(),
		DAGRunMgr:                 drm,
		DAGStore:                  dagStore,
		DAGRunStore:               runStore,
		ProcStore:                 procStore,
		QueueStore:                queueStore,
		ServiceRegistry:           serviceMonitor,
		DispatchTaskStore:         dispatchTaskStore,
		WorkerHeartbeatStore:      workerHeartbeatStore,
		DAGRunLeaseStore:          dagRunLeaseStore,
		ActiveDistributedRunStore: activeDistributedRunStore,
		SubCmdBuilder:             runtimepkg.NewSubCmdBuilder(cfg),
		ServerOptions:             options.ServerOptions,
		StaleHeartbeatThreshold:   options.StaleHeartbeatThreshold,
		StaleLeaseThreshold:       options.StaleLeaseThreshold,

		tmpDir: tmpDir,
	}

	if options.CaptureLoggingOutput {
		helper.LoggingOutput = &SyncBuffer{buf: new(bytes.Buffer)}
		loggerInstance := logger.NewLogger(
			logger.WithDebug(),
			logger.WithFormat("text"),
			logger.WithWriter(helper.LoggingOutput),
		)
		helper.Context = logger.WithFixedLogger(helper.Context, loggerInstance)
	}

	ctx, cancel := context.WithCancel(helper.Context)
	helper.Context = ctx
	helper.Cancel = cancel

	t.Cleanup(helper.Cleanup)
	return helper
}

// writeHelperConfigFile writes a minimal YAML configuration to configPath so subprocesses can rely on a stable --config file.
// The written file contains core settings (debug, log format, default shell, and tz if set), paths, and any enabled or configured
// queues, scheduler, coordinator, worker, and ui sections derived from cfg.
// The function fails the test if YAML marshaling or writing the file returns an error.
func writeHelperConfigFile(t *testing.T, cfg *config.Config, configPath string) {
	t.Helper()

	configData := map[string]any{
		"debug":         cfg.Core.Debug,
		"log_format":    cfg.Core.LogFormat,
		"default_shell": cfg.Core.DefaultShell,
	}
	if cfg.Core.TZ != "" {
		configData["tz"] = cfg.Core.TZ
	}

	configData["paths"] = map[string]any{
		"dags_dir":             cfg.Paths.DAGsDir,
		"log_dir":              cfg.Paths.LogDir,
		"artifact_dir":         cfg.Paths.ArtifactDir,
		"data_dir":             cfg.Paths.DataDir,
		"suspend_flags_dir":    cfg.Paths.SuspendFlagsDir,
		"admin_logs_dir":       cfg.Paths.AdminLogsDir,
		"event_store_dir":      cfg.Paths.EventStoreDir,
		"base_config":          cfg.Paths.BaseConfig,
		"dag_runs_dir":         cfg.Paths.DAGRunsDir,
		"queue_dir":            cfg.Paths.QueueDir,
		"proc_dir":             cfg.Paths.ProcDir,
		"service_registry_dir": cfg.Paths.ServiceRegistryDir,
		"users_dir":            cfg.Paths.UsersDir,
		"executable":           cfg.Paths.Executable,
	}

	if cfg.Queues.Enabled || len(cfg.Queues.Config) > 0 {
		qcfg := map[string]any{
			"enabled": cfg.Queues.Enabled,
		}
		if len(cfg.Queues.Config) > 0 {
			var configs []map[string]any
			for _, q := range cfg.Queues.Config {
				entry := map[string]any{"name": q.Name}
				if q.MaxActiveRuns > 0 {
					entry["max_active_runs"] = q.MaxActiveRuns
				}
				configs = append(configs, entry)
			}
			if len(configs) > 0 {
				qcfg["config"] = configs
			}
		}
		configData["queues"] = qcfg
	}

	configData["proc"] = map[string]any{
		"heartbeat_interval":      cfg.Proc.HeartbeatInterval.String(),
		"heartbeat_sync_interval": cfg.Proc.HeartbeatSyncInterval.String(),
		"stale_threshold":         cfg.Proc.StaleThreshold.String(),
	}

	scheduler := map[string]any{}
	// Always write port so that 0 (disable health server) is preserved when loading
	scheduler["port"] = cfg.Scheduler.Port
	if cfg.Scheduler.LockStaleThreshold > 0 {
		scheduler["lock_stale_threshold"] = cfg.Scheduler.LockStaleThreshold.String()
	}
	if cfg.Scheduler.LockRetryInterval > 0 {
		scheduler["lock_retry_interval"] = cfg.Scheduler.LockRetryInterval.String()
	}
	if cfg.Scheduler.ZombieDetectionInterval >= 0 {
		scheduler["zombie_detection_interval"] = cfg.Scheduler.ZombieDetectionInterval.String()
	}
	if cfg.Scheduler.FailureThreshold > 0 {
		scheduler["failure_threshold"] = cfg.Scheduler.FailureThreshold
	}
	if len(scheduler) > 0 {
		configData["scheduler"] = scheduler
	}

	coordData := map[string]any{
		"enabled":     cfg.Coordinator.Enabled,
		"health_port": cfg.Coordinator.HealthPort,
	}
	if cfg.Coordinator.Host != "" {
		coordData["host"] = cfg.Coordinator.Host
	}
	if cfg.Coordinator.Advertise != "" {
		coordData["advertise"] = cfg.Coordinator.Advertise
	}
	if cfg.Coordinator.Port != 0 {
		coordData["port"] = cfg.Coordinator.Port
	}
	configData["coordinator"] = coordData

	workerData := map[string]any{
		"health_port": cfg.Worker.HealthPort,
	}
	if cfg.Worker.ID != "" {
		workerData["id"] = cfg.Worker.ID
	}
	if cfg.Worker.MaxActiveRuns != 0 {
		workerData["max_active_runs"] = cfg.Worker.MaxActiveRuns
	}
	if len(cfg.Worker.Labels) > 0 {
		workerData["labels"] = cfg.Worker.Labels
	}
	if len(cfg.Worker.Coordinators) > 0 {
		workerData["coordinators"] = cfg.Worker.Coordinators
	}
	configData["worker"] = workerData

	ui := map[string]any{}
	if cfg.UI.LogEncodingCharset != "" {
		ui["log_encoding_charset"] = cfg.UI.LogEncodingCharset
	}
	if cfg.UI.NavbarColor != "" {
		ui["navbar_color"] = cfg.UI.NavbarColor
	}
	if cfg.UI.NavbarTitle != "" {
		ui["navbar_title"] = cfg.UI.NavbarTitle
	}
	if cfg.UI.MaxDashboardPageLimit != 0 {
		ui["max_dashboard_page_limit"] = cfg.UI.MaxDashboardPageLimit
	}
	if cfg.UI.DAGs.SortField != "" || cfg.UI.DAGs.SortOrder != "" {
		ui["dags"] = map[string]any{
			"sort_field": cfg.UI.DAGs.SortField,
			"sort_order": cfg.UI.DAGs.SortOrder,
		}
	}
	if len(ui) > 0 {
		configData["ui"] = ui
	}

	// Always write auth section so subprocesses use the same auth mode.
	// Without this, subprocesses would default to "builtin" and auto-generate
	// a different token secret, causing authentication mismatches.
	authMode := string(cfg.Server.Auth.Mode)
	if authMode == "" {
		authMode = "none"
	}
	authData := map[string]any{
		"mode": authMode,
	}
	if cfg.Server.Auth.Builtin.Token.Secret != "" {
		authData["builtin"] = map[string]any{
			"token": map[string]any{
				"secret": cfg.Server.Auth.Builtin.Token.Secret,
			},
		}
	}
	if cfg.Server.Auth.Basic.Username != "" {
		authData["basic"] = map[string]any{
			"username": cfg.Server.Auth.Basic.Username,
			"password": cfg.Server.Auth.Basic.Password,
		}
	}
	configData["auth"] = authData

	content, err := yaml.Marshal(configData)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(configPath, content, 0600))
}

// Helper provides test utilities and configuration
type Helper struct {
	Context                   context.Context
	Cancel                    context.CancelFunc
	Config                    *config.Config
	ChildEnv                  []string
	LoggingOutput             *SyncBuffer
	DAGStore                  exec1.DAGStore
	DAGRunStore               exec1.DAGRunStore
	DAGRunMgr                 runtimepkg.Manager
	ProcStore                 exec1.ProcStore
	QueueStore                exec1.QueueStore
	ServiceRegistry           exec1.ServiceRegistry
	DispatchTaskStore         exec1.DispatchTaskStore
	WorkerHeartbeatStore      exec1.WorkerHeartbeatStore
	DAGRunLeaseStore          exec1.DAGRunLeaseStore
	ActiveDistributedRunStore exec1.ActiveDistributedRunStore
	SubCmdBuilder             *runtimepkg.SubCmdBuilder
	ServerOptions             []frontend.ServerOption
	StaleHeartbeatThreshold   time.Duration
	StaleLeaseThreshold       time.Duration

	tmpDir string
}

// Cleanup removes temporary test directories
func (h Helper) Cleanup() {
	if h.Cancel != nil {
		h.Cancel()
	}
	_ = os.RemoveAll(h.tmpDir)
}

// TempFile creates a temp file with specified name and content.
func (h Helper) TempFile(t *testing.T, name string, data []byte) string {
	t.Helper()

	filename := filepath.Join(h.tmpDir, name)
	err := os.WriteFile(filename, data, 0600)
	require.NoError(t, err)
	return filename
}

// DAG creates a test DAG from YAML content
func (h Helper) DAG(t *testing.T, yamlContent string) DAG {
	t.Helper()

	err := os.MkdirAll(h.Config.Paths.DAGsDir, 0750)
	require.NoError(t, err, "failed to create DAGs directory %q", h.Config.Paths.DAGsDir)

	// Generate a unique filename for the test DAG
	filename := fmt.Sprintf("%s.yaml", uuid.New().String())
	testFile := filepath.Join(h.Config.Paths.DAGsDir, filename)
	err = os.WriteFile(testFile, []byte(yamlContent), 0600)
	require.NoError(t, err, "failed to write test DAG")

	loadOpts := []spec.LoadOption{}
	if h.Config.Paths.BaseConfig != "" {
		loadOpts = append(loadOpts, spec.WithBaseConfig(h.Config.Paths.BaseConfig))
	}
	loadOpts = append(loadOpts, spec.WithWorkspaceBaseConfigDir(workspace.BaseConfigDir(h.Config.Paths.DAGsDir)))
	dag, err := spec.Load(h.Context, testFile, loadOpts...)
	require.NoError(t, err, "failed to load test DAG")

	return DAG{
		Helper: &h,
		DAG:    dag,
	}
}

// CreateDAGFile creates a DAG file in a given directory for tests that need separate DAG files
func (h Helper) CreateDAGFile(t *testing.T, dir string, name string, yamlContent []byte) string {
	t.Helper()

	// Create the directory if it doesn't exist
	err := os.MkdirAll(dir, 0750)
	require.NoError(t, err, "failed to create directory %q", dir)

	if !fileutil.IsYAMLFile(name) {
		name = fmt.Sprintf("%s.yaml", name)
	}

	dagFile := filepath.Join(dir, name)
	err = os.WriteFile(dagFile, yamlContent, 0600)
	require.NoError(t, err, "failed to write DAG file %q", name)

	t.Cleanup(func() { _ = os.Remove(dagFile) })

	return dagFile
}

func (h Helper) DAGExpectError(t *testing.T, name string, expectedErr string) {
	t.Helper()

	filePath := TestdataPath(t, name)
	_, err := spec.Load(h.Context, filePath)
	require.Error(t, err, "expected error loading test DAG %q", name)
	require.Contains(t, err.Error(), expectedErr, "expected error %q, got %q", expectedErr, err.Error())
}

type DAG struct {
	*Helper
	*core.DAG
}

func (d *DAG) AssertLatestStatus(t *testing.T, expected core.Status) {
	t.Helper()

	require.Eventually(t, func() bool {
		latest, err := d.DAGRunMgr.GetLatestStatus(d.Context, d.DAG)
		if err != nil {
			return false
		}
		t.Logf("latest status=%s errors=%v", latest.Status.String(), latest.Errors())
		return latest.Status == expected
	}, latestStatusAssertTimeout, latestStatusAssertInterval)
}

func (d *DAG) AssertDAGRunCount(t *testing.T, expected int) {
	t.Helper()

	// the +1 to the limit is needed to ensure that the number of dag-run
	// entries is exactly the expected number
	runstore := d.DAGRunMgr.ListRecentStatus(d.Context, d.Name, expected+1)
	require.Len(t, runstore, expected)
}

func (d *DAG) AssertCurrentStatus(t *testing.T, expected core.Status) {
	t.Helper()

	assert.Eventually(t, func() bool {
		curr, _ := d.DAGRunMgr.GetCurrentStatus(d.Context, d.DAG, "")
		if curr == nil {
			return false
		}
		t.Logf("current status=%s errors=%v", curr.Status.String(), curr.Errors())
		return curr.Status == expected
	}, latestStatusAssertTimeout, latestStatusAssertInterval)
}

// AssertOutputs checks the given outputs against the actual outputs of the DAG
// Note that this function does not respect dependencies between nodes
// making the outputs with the same key indeterministic
func (d *DAG) AssertOutputs(t *testing.T, outputs map[string]any) {
	t.Helper()

	status, err := d.DAGRunMgr.GetLatestStatus(d.Context, d.DAG)
	require.NoError(t, err)

	// collect the actual outputs from the status
	var actualOutputs = make(map[string]string)
	for _, node := range status.Nodes {
		if node.OutputVariables == nil {
			continue
		}
		value, ok := node.OutputVariables.Load(node.Step.Output)
		if ok {
			actualOutputs[node.Step.Output] = value.(string)
		}
	}

	// compare the actual outputs with the expected outputs
	for key, expected := range outputs {
		if expected == "" {
			_, ok := actualOutputs[key]
			assert.False(t, ok, "expected output %q to be empty", key)
			continue
		}

		if actual, ok := actualOutputs[key]; ok {
			actual = normalizeTestOutput(actual)
			switch expected := expected.(type) {
			case string:
				assert.Equal(t, normalizeTestOutput(fmt.Sprintf("%s=%s", key, expected)), actual)

			case Contains:
				assert.Contains(t, actual, normalizeTestOutput(string(expected)), "expected output %q to include %q", key, expected)

			case []Contains:
				for _, c := range expected {
					assert.Contains(t, actual, normalizeTestOutput(string(c)), "expected output %q to include %q", key, c)
				}

			case NotEmpty:
				_, value, found := strings.Cut(actual, "=")
				assert.True(t, found, "expected output %q to be in the form key=value", key)
				assert.NotEmpty(t, value, "expected output %q to be not empty", key)

			default:
				t.Errorf("unsupported value matcher type %T", expected)

			}
		} else {
			t.Errorf("expected output %q not found", key)
		}
	}
}

func normalizeTestOutput(s string) string {
	return strings.ReplaceAll(s, "\r\n", "\n")
}

// ReadOutputs reads the collected outputs from the outputs.json file.
func (d *DAG) ReadOutputs(t *testing.T) map[string]string {
	t.Helper()

	dagRunsDir := d.Config.Paths.DAGRunsDir
	dagRunDir := filepath.Join(dagRunsDir, d.Name, "dag-runs")

	var outputsPath string
	_ = filepath.Walk(dagRunDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.Name() == filedagrun.OutputsFile {
			outputsPath = path
			return filepath.SkipAll
		}
		return nil
	})

	if outputsPath == "" {
		return nil
	}

	data, err := os.ReadFile(outputsPath) //nolint:gosec // path is constructed from test config
	require.NoError(t, err)

	var outputs exec1.DAGRunOutputs
	require.NoError(t, json.Unmarshal(data, &outputs))

	return outputs.Outputs
}

type NotEmpty struct{}

type Contains string

type AgentOption func(*Agent)

func WithAgentOptions(options agent.Options) AgentOption {
	return func(a *Agent) {
		a.opts = options
	}
}

func WithDAGRunID(dagRunID string) AgentOption {
	return func(a *Agent) {
		a.dagRunID = dagRunID
	}
}

func (d *DAG) Agent(opts ...AgentOption) *Agent {
	helper := &Agent{Helper: d.Helper, DAG: d.DAG}

	for _, opt := range opts {
		opt(helper)
	}

	var dagRunID string
	if helper.opts.RetryTarget != nil {
		dagRunID = helper.opts.RetryTarget.DAGRunID
	} else if helper.dagRunID != "" {
		dagRunID = helper.dagRunID
	} else {
		dagRunID = genDAGRunID()
	}
	helper.dagRunID = dagRunID

	logDir := d.Config.Paths.LogDir
	logFile := filepath.Join(d.Config.Paths.LogDir, dagRunID+".log")
	root := exec1.NewDAGRunRef(d.Name, dagRunID)

	helper.opts.DAGRunStore = d.DAGRunStore
	helper.opts.QueueStore = d.QueueStore
	helper.opts.ServiceRegistry = d.ServiceRegistry
	helper.opts.RootDAGRun = root
	helper.opts.PeerConfig = d.Config.Core.Peer
	helper.opts.DefaultExecMode = d.Config.DefaultExecMode
	helper.opts.DAGRunLogDir = d.Config.Paths.LogDir
	helper.opts.DAGRunArtifactDir = d.Config.Paths.ArtifactDir

	helper.Agent = agent.New(
		dagRunID,
		d.DAG,
		logDir,
		logFile,
		d.DAGRunMgr,
		d.DAGStore,
		helper.opts,
	)

	return helper
}

type Agent struct {
	*Helper
	*core.DAG
	*agent.Agent
	opts     agent.Options
	dagRunID string // the dag-run ID for this agent
}

func (a *Agent) RunError(t *testing.T) {
	t.Helper()

	err := a.Run(a.Context)
	assert.Error(t, err)

	st := a.Status(a.Context).Status
	require.Equal(t, core.Failed.String(), st.String())
}

func (a *Agent) RunCancel(t *testing.T) {
	t.Helper()

	attemptID := newTestAttemptID(t)
	proc, err := a.ProcStore.Acquire(a.Context, a.ProcGroup(), exec1.ProcMeta{
		StartedAt:    time.Now().Unix(),
		Name:         a.Name,
		DAGRunID:     a.dagRunID,
		AttemptID:    attemptID,
		RootName:     a.Name,
		RootDAGRunID: a.dagRunID,
	})
	require.NoError(t, err, "failed to acquire proc")
	t.Cleanup(func() {
		_ = proc.Stop(a.Context)
	})

	err = a.Run(a.Context)
	assert.NoError(t, err)

	st := a.Status(a.Context).Status
	require.Equal(t, core.Aborted.String(), st.String())
}

func (a *Agent) RunCheckErr(t *testing.T, expectedErr string) {
	t.Helper()

	err := a.Run(a.Context)
	require.Error(t, err, "expected error %q, got nil", expectedErr)
	require.Contains(t, err.Error(), expectedErr)
	st := a.Status(a.Context)
	require.Equal(t, core.Failed.String(), st.Status.String())
}

func (a *Agent) RunSuccess(t *testing.T) {
	t.Helper()

	err := a.Run(a.Context)
	assert.NoError(t, err, "failed to run agent")

	st := a.Status(a.Context).Status
	require.Equal(t, core.Succeeded.String(), st.String(), "expected status %q, got %q", core.Succeeded, st)

	// check all nodes are in success or skipped state
	for _, node := range a.Status(a.Context).Nodes {
		st := node.Status
		if st == core.NodeSkipped || st == core.NodeSucceeded {
			continue
		}
		t.Errorf("expected node %q to be in success state, got %q", node.Step.Name, st.String())
	}
}

func (a *Agent) Abort() {
	a.Signal(a.Context, syscall.SIGTERM)
}

func newTestAttemptID(t *testing.T) string {
	t.Helper()

	b := make([]byte, 3)
	_, err := rand.Read(b)
	require.NoError(t, err)
	return hex.EncodeToString(b)
}

// SyncBuffer provides thread-safe buffer operations
type SyncBuffer struct {
	buf  *bytes.Buffer
	lock sync.Mutex
}

func (b *SyncBuffer) Write(p []byte) (n int, err error) {
	b.lock.Lock()
	defer b.lock.Unlock()
	return b.buf.Write(p)
}

func (b *SyncBuffer) String() string {
	b.lock.Lock()
	defer b.lock.Unlock()
	return b.buf.String()
}

func (b *SyncBuffer) Reset() {
	b.lock.Lock()
	defer b.lock.Unlock()
	b.buf.Reset()
}

// createDefaultContext creates a context with default logger settings
func createDefaultContext() context.Context {
	ctx := context.Background()
	return logger.WithLogger(ctx, logger.NewLogger(
		logger.WithDebug(),
		logger.WithFormat("text"),
	))
}

func testShellPath(t *testing.T) string {
	t.Helper()

	if runtime.GOOS == "windows" {
		for _, name := range []string{"pwsh.exe", "pwsh", "powershell.exe", "powershell"} {
			if shPath, ok := cmdutil.FindExecutable(name); ok {
				return shPath
			}
		}
		if shPath := windowsSystemPowerShellPath(); shPath != "" {
			return shPath
		}
		if shPath := strings.TrimSpace(os.Getenv("COMSPEC")); shPath != "" {
			if _, err := os.Stat(shPath); err == nil {
				return shPath
			}
		}
		for _, name := range []string{"cmd.exe", "cmd", "bash", "sh"} {
			if shPath, ok := cmdutil.FindExecutable(name); ok {
				return shPath
			}
		}
		t.Fatal("no suitable shell found on Windows")
	}

	shPath, err := exec.LookPath("sh")
	require.NoError(t, err, "failed to find sh")
	return shPath
}

func windowsSystemPowerShellPath() string {
	systemRoot := strings.TrimSpace(os.Getenv("SystemRoot"))
	if systemRoot == "" {
		return ""
	}

	for _, candidate := range []string{
		filepath.Join(systemRoot, "System32", "WindowsPowerShell", "v1.0", "powershell.exe"),
		filepath.Join(systemRoot, "Sysnative", "WindowsPowerShell", "v1.0", "powershell.exe"),
	} {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}

	return ""
}

func buildHelperChildEnv(base []string, daguHome, configFile, executablePath, shellPath string) []string {
	env := append([]string{}, base...)
	return withEnvOverrides(
		env,
		fmt.Sprintf("DAGU_HOME=%s", daguHome),
		fmt.Sprintf("DAGU_CONFIG=%s", configFile),
		fmt.Sprintf("DAGU_EXECUTABLE=%s", executablePath),
		fmt.Sprintf("SHELL=%s", shellPath),
		"DEBUG=true",
		"CI=true",
		"TZ=UTC",
	)
}

func withEnvOverrides(base []string, overrides ...string) []string {
	env := append([]string{}, base...)
	indexByKey := make(map[string]int, len(env))
	for i, entry := range env {
		key, _, found := strings.Cut(entry, "=")
		if found {
			indexByKey[key] = i
		}
	}
	for _, entry := range overrides {
		key, _, found := strings.Cut(entry, "=")
		if !found {
			continue
		}
		if idx, ok := indexByKey[key]; ok {
			env[idx] = entry
			continue
		}
		indexByKey[key] = len(env)
		env = append(env, entry)
	}
	return env
}

// genDAGRunID generates a new unique dag-run ID using UUID v7.
func genDAGRunID() string {
	id, err := uuid.NewV7()
	if err != nil {
		panic(err)
	}
	return id.String()
}

// TestdataPath returns the path to a testdata file.
func TestdataPath(t *testing.T, filename string) string {
	t.Helper()

	rootDir := getProjectRoot(t)

	return filepath.Join(rootDir, "internal", "testdata", filepath.Clean(filename))
}

// ReadTestdata reads the content of a testdata file.
func ReadTestdata(t *testing.T, filename string) []byte {
	t.Helper()

	path := TestdataPath(t, filename)
	data, err := os.ReadFile(path) //nolint:gosec
	require.NoError(t, err, "failed to read testdata file %q", filename)

	return data
}

// getProjectRoot returns the root directory of the project.
// This allows to read testdata files from the testdata directory.
func getProjectRoot(t *testing.T) string {
	t.Helper()

	_, filename, _, ok := runtime.Caller(1)
	require.True(t, ok, "failed to get caller information")
	rootDir := filepath.Join(filepath.Dir(filename), "..", "..")

	return filepath.Clean(rootDir)
}

func buildCurrentExecutable(t *testing.T, root string) string {
	t.Helper()

	builtExecutableOnce.Do(func() {
		tmpDir, err := os.MkdirTemp("", "dagu-test-bin-*")
		if err != nil {
			builtExecutableErr = fmt.Errorf("failed to create temp dir for test executable: %w", err)
			return
		}

		path := filepath.Join(tmpDir, "dagu")
		if runtime.GOOS == "windows" {
			path += ".exe"
		}

		//nolint:gosec // Test helper builds a fixed local package into an internally generated temp path.
		cmd := exec.Command("go", "build", "-o", path, "./cmd")
		cmd.Dir = root

		var output bytes.Buffer
		cmd.Stdout = &output
		cmd.Stderr = &output

		if err := cmd.Run(); err != nil {
			builtExecutableErr = fmt.Errorf("failed to build current dagu executable: %w: %s", err, strings.TrimSpace(output.String()))
			return
		}

		builtExecutablePath = path
	})

	require.NoError(t, builtExecutableErr)
	require.NotEmpty(t, builtExecutablePath)

	return builtExecutablePath
}
