// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package dagu

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"slices"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/config"
	coreexec "github.com/dagucloud/dagu/internal/core/exec"
	iengine "github.com/dagucloud/dagu/internal/engine"
	"github.com/dagucloud/dagu/internal/persis/file"
	"github.com/dagucloud/dagu/internal/persis/store"

	_ "github.com/dagucloud/dagu/internal/runtime/builtin" // Register built-in executors for embedded use.
)

// ExecutionMode controls how a DAG run is dispatched.
type ExecutionMode string

const (
	// ExecutionModeLocal runs the DAG in the current process.
	ExecutionModeLocal ExecutionMode = "local"
	// ExecutionModeDistributed dispatches the DAG to configured coordinators.
	ExecutionModeDistributed ExecutionMode = "distributed"
)

// Options configures an embedded Dagu engine.
type Options struct {
	// HomeDir is the Dagu application home used for default config and data paths.
	HomeDir string
	// ConfigFile loads Dagu configuration from an explicit config file.
	ConfigFile string
	// DAGsDir overrides the directory used to resolve named DAGs and sub-DAGs.
	DAGsDir string
	// DataDir overrides the file-backed state directory.
	DataDir string
	// LogDir overrides the run log directory.
	LogDir string
	// ArtifactDir overrides the artifact directory.
	ArtifactDir string
	// BaseConfig points at a base configuration file applied during DAG loading.
	BaseConfig string
	// Logger receives embedded engine logs. A quiet logger is used when nil.
	Logger *slog.Logger

	// DefaultMode is used when a run does not set WithMode.
	DefaultMode ExecutionMode
	// Distributed configures dispatch and worker clients for shared-nothing mode.
	Distributed *DistributedOptions
}

// DistributedOptions configures shared-nothing distributed execution.
type DistributedOptions struct {
	// Coordinators are coordinator gRPC addresses.
	Coordinators []string
	// TLS configures coordinator client TLS.
	TLS TLSOptions
	// WorkerSelector constrains distributed runs to matching workers.
	WorkerSelector map[string]string
	// PollInterval controls distributed run status polling.
	PollInterval time.Duration
	// MaxStatusErrors is the number of consecutive status failures before Wait fails.
	MaxStatusErrors int
}

// TLSOptions configures TLS for coordinator and worker peer clients.
type TLSOptions struct {
	// Insecure explicitly allows plaintext coordinator connections.
	Insecure bool
	// CertFile is the client certificate file for TLS connections.
	CertFile string
	// KeyFile is the client private key file for TLS connections.
	KeyFile string
	// ClientCAFile is the CA file used to verify coordinator certificates.
	ClientCAFile string
	// SkipTLSVerify skips coordinator certificate verification.
	SkipTLSVerify bool
}

// RunRef identifies a DAG run.
type RunRef struct {
	Name string
	ID   string
}

// Status is a stable snapshot of a DAG run.
type Status struct {
	Name        string
	RunID       string
	AttemptID   string
	Status      string
	StartedAt   time.Time
	FinishedAt  time.Time
	Error       string
	LogFile     string
	ArchiveDir  string
	WorkerID    string
	TriggerType string
}

// WorkerOptions configures an embedded shared-nothing worker.
type WorkerOptions struct {
	// ID is the worker identifier. A host and process based ID is generated when empty.
	ID string
	// MaxActiveRuns limits concurrent DAG runs. A default is used when zero or negative.
	MaxActiveRuns int
	// Labels are advertised to coordinators and matched by worker selectors.
	Labels map[string]string
	// Coordinators overrides DistributedOptions.Coordinators when non-empty.
	// If empty, the worker falls back to the engine-level DistributedOptions.
	// The resolved coordinator list must contain at least one non-empty address.
	Coordinators []string
	// TLS overrides DistributedOptions.TLS when non-zero. If zero, the worker
	// falls back to the engine-level DistributedOptions TLS settings.
	TLS TLSOptions
	// HealthPort starts the worker health endpoint on the given port. Zero disables it.
	HealthPort int
}

// Engine is an embedded Dagu engine backed by the configured file stores.
type Engine struct {
	inner *iengine.Engine
}

// Run is a handle for an asynchronous DAG run.
type Run struct {
	inner *iengine.Run
}

// Worker is a shared-nothing worker connected to configured coordinators.
type Worker struct {
	inner *iengine.Worker
}

// RunOption customizes a single DAG run.
type RunOption func(*runOptions)

type runOptions struct {
	runID             string
	name              string
	params            map[string]string
	paramsList        []string
	defaultWorkingDir string
	mode              ExecutionMode
	workerSelector    map[string]string
	labels            []string
	dryRun            bool
}

// New creates an embedded Dagu engine.
func New(ctx context.Context, opts Options) (*Engine, error) {
	inner, err := iengine.New(ctx, internalOptions(opts))
	if err != nil {
		return nil, err
	}
	return &Engine{inner: inner}, nil
}

// Close releases engine resources.
func (e *Engine) Close(ctx context.Context) error {
	if e == nil || e.inner == nil {
		return nil
	}
	return e.inner.Close(ctx)
}

// RunFile loads a DAG definition from a file and starts it asynchronously.
func (e *Engine) RunFile(ctx context.Context, path string, opts ...RunOption) (*Run, error) {
	if e == nil || e.inner == nil {
		return nil, fmt.Errorf("engine is not initialized")
	}
	runOpts := applyRunOptions(opts)
	inner, err := e.inner.RunFile(ctx, path, internalRunOptions(runOpts))
	if err != nil {
		return nil, err
	}
	return &Run{inner: inner}, nil
}

// RunYAML loads a DAG definition from YAML bytes and starts it asynchronously.
func (e *Engine) RunYAML(ctx context.Context, yaml []byte, opts ...RunOption) (*Run, error) {
	if e == nil || e.inner == nil {
		return nil, fmt.Errorf("engine is not initialized")
	}
	runOpts := applyRunOptions(opts)
	inner, err := e.inner.RunYAML(ctx, yaml, internalRunOptions(runOpts))
	if err != nil {
		return nil, err
	}
	return &Run{inner: inner}, nil
}

// Status reads the latest file-backed status for a local DAG run.
func (e *Engine) Status(ctx context.Context, ref RunRef) (*Status, error) {
	if e == nil || e.inner == nil {
		return nil, fmt.Errorf("engine is not initialized")
	}
	status, err := e.inner.Status(ctx, internalRunRef(ref))
	if err != nil {
		return nil, err
	}
	return publicStatus(status), nil
}

// Stop requests cancellation for a local DAG run.
func (e *Engine) Stop(ctx context.Context, ref RunRef) error {
	if e == nil || e.inner == nil {
		return fmt.Errorf("engine is not initialized")
	}
	return e.inner.Stop(ctx, internalRunRef(ref))
}

// NewWorker creates an embedded worker for shared-nothing distributed execution.
func (e *Engine) NewWorker(opts WorkerOptions) (*Worker, error) {
	if e == nil || e.inner == nil {
		return nil, fmt.Errorf("engine is not initialized")
	}
	inner, err := e.inner.NewWorker(internalWorkerOptions(opts))
	if err != nil {
		return nil, err
	}
	return &Worker{inner: inner}, nil
}

// Ref returns the run reference.
func (r *Run) Ref() RunRef {
	if r == nil || r.inner == nil {
		return RunRef{}
	}
	return publicRunRef(r.inner.Ref())
}

// ID returns the DAG run ID.
func (r *Run) ID() string {
	if r == nil || r.inner == nil {
		return ""
	}
	return r.inner.ID()
}

// Name returns the DAG name.
func (r *Run) Name() string {
	if r == nil || r.inner == nil {
		return ""
	}
	return r.inner.Name()
}

// Wait blocks until the DAG run reaches a terminal state or ctx is canceled.
func (r *Run) Wait(ctx context.Context) (*Status, error) {
	if r == nil || r.inner == nil {
		return nil, fmt.Errorf("run is not initialized")
	}
	status, err := r.inner.Wait(ctx)
	return publicStatus(status), err
}

// Status returns the current run status.
func (r *Run) Status(ctx context.Context) (*Status, error) {
	if r == nil || r.inner == nil {
		return nil, fmt.Errorf("run is not initialized")
	}
	status, err := r.inner.Status(ctx)
	return publicStatus(status), err
}

// Stop requests cancellation for this run.
func (r *Run) Stop(ctx context.Context) error {
	if r == nil || r.inner == nil {
		return fmt.Errorf("run is not initialized")
	}
	return r.inner.Stop(ctx)
}

// Start registers and starts the worker. It blocks until ctx is canceled or the
// worker exits with an error.
func (w *Worker) Start(ctx context.Context) error {
	if w == nil || w.inner == nil {
		return fmt.Errorf("worker is not initialized")
	}
	return w.inner.Start(ctx)
}

// Stop stops the worker.
func (w *Worker) Stop(ctx context.Context) error {
	if w == nil || w.inner == nil {
		return nil
	}
	return w.inner.Stop(ctx)
}

// WaitReady blocks until the worker has registered with a coordinator.
func (w *Worker) WaitReady(ctx context.Context) error {
	if w == nil || w.inner == nil {
		return fmt.Errorf("worker is not initialized")
	}
	return w.inner.WaitReady(ctx)
}

func applyRunOptions(opts []RunOption) runOptions {
	var runOpts runOptions
	for _, opt := range opts {
		opt(&runOpts)
	}
	return runOpts
}

// WithRunID sets an explicit DAG run ID.
func WithRunID(id string) RunOption {
	return func(o *runOptions) {
		o.runID = id
	}
}

// WithName overrides the loaded DAG name.
func WithName(name string) RunOption {
	return func(o *runOptions) {
		o.name = name
	}
}

// WithParams sets DAG parameters from a key-value map.
func WithParams(params map[string]string) RunOption {
	return func(o *runOptions) {
		o.params = cloneMap(params)
	}
}

// WithParamsList sets DAG parameters from Dagu-style KEY=VALUE entries.
func WithParamsList(params []string) RunOption {
	return func(o *runOptions) {
		o.paramsList = cloneSlice(params)
	}
}

// WithDefaultWorkingDir sets the default working directory while loading a DAG.
func WithDefaultWorkingDir(dir string) RunOption {
	return func(o *runOptions) {
		o.defaultWorkingDir = dir
	}
}

// WithMode overrides the engine default execution mode.
func WithMode(mode ExecutionMode) RunOption {
	return func(o *runOptions) {
		o.mode = mode
	}
}

// WithWorkerSelector sets the distributed worker selector for one run.
func WithWorkerSelector(selector map[string]string) RunOption {
	return func(o *runOptions) {
		o.workerSelector = cloneMap(selector)
	}
}

// WithLabels adds labels to one run.
func WithLabels(labels ...string) RunOption {
	return func(o *runOptions) {
		o.labels = cloneSlice(labels)
	}
}

// WithTags adds labels to one run.
//
// Deprecated: use WithLabels.
func WithTags(tags ...string) RunOption {
	return WithLabels(tags...)
}

// WithDryRun enables or disables dry-run mode.
func WithDryRun(enabled bool) RunOption {
	return func(o *runOptions) {
		o.dryRun = enabled
	}
}

func internalOptions(opts Options) iengine.Options {
	out := iengine.Options{
		HomeDir:            opts.HomeDir,
		ConfigFile:         opts.ConfigFile,
		DAGsDir:            opts.DAGsDir,
		DataDir:            opts.DataDir,
		LogDir:             opts.LogDir,
		ArtifactDir:        opts.ArtifactDir,
		BaseConfig:         opts.BaseConfig,
		Logger:             opts.Logger,
		PersistenceFactory: filePersistenceFactory,
		DefaultMode:        iengine.ExecutionMode(opts.DefaultMode),
	}
	if opts.Distributed != nil {
		distributed := internalDistributedOptions(*opts.Distributed)
		out.Distributed = &distributed
	}
	return out
}

func filePersistenceFactory(ctx context.Context, cfg *config.Config) (iengine.Persistence, error) {
	if err := os.MkdirAll(cfg.Paths.DataDir, 0o750); err != nil {
		return iengine.Persistence{}, fmt.Errorf("create data directory: %w", err)
	}
	if err := os.MkdirAll(cfg.Paths.DAGStateDir, 0o750); err != nil {
		return iengine.Persistence{}, fmt.Errorf("create DAG state directory: %w", err)
	}

	procStore := file.NewProcStore(cfg)
	dagStore, err := fileEngineDAGStore(ctx, cfg, iengine.DAGStoreFactoryOptions{})
	if err != nil {
		return iengine.Persistence{}, err
	}

	return iengine.Persistence{
		DAGStore:        dagStore,
		DAGRunStore:     file.NewDAGRunStore(cfg, file.WithDAGRunLatestStatusToday(false)),
		ProcStore:       procStore,
		StateStore:      store.NewDAGStateStore(file.NewCollection(cfg.Paths.DAGStateDir)),
		ServiceRegistry: file.NewServiceRegistry(cfg),

		DAGStoreFactory:      fileEngineDAGStore,
		AgentStoresFactory:   fileEngineAgentStores,
		SnapshotStoreFactory: file.NewSnapshotStores,
	}, nil
}

func fileEngineDAGStore(_ context.Context, cfg *config.Config, opts iengine.DAGStoreFactoryOptions) (coreexec.DAGStore, error) {
	var fileOpts []file.DAGStoreOption
	if len(opts.SearchPaths) > 0 {
		fileOpts = append(fileOpts, file.WithDAGSearchPaths(opts.SearchPaths))
	}
	return file.NewDAGStore(cfg, fileOpts...)
}

func fileEngineAgentStores(ctx context.Context, cfg *config.Config) iengine.AgentStores {
	return file.NewAgentStores(ctx, cfg, file.WithAgentContextResolverFromConfig())
}

func internalDistributedOptions(opts DistributedOptions) iengine.DistributedOptions {
	return iengine.DistributedOptions{
		Coordinators:    cloneSlice(opts.Coordinators),
		TLS:             internalTLSOptions(opts.TLS),
		WorkerSelector:  cloneMap(opts.WorkerSelector),
		PollInterval:    opts.PollInterval,
		MaxStatusErrors: opts.MaxStatusErrors,
	}
}

func internalTLSOptions(opts TLSOptions) iengine.TLSOptions {
	return iengine.TLSOptions{
		Insecure:      opts.Insecure,
		CertFile:      opts.CertFile,
		KeyFile:       opts.KeyFile,
		ClientCAFile:  opts.ClientCAFile,
		SkipTLSVerify: opts.SkipTLSVerify,
	}
}

func internalRunOptions(opts runOptions) iengine.RunOptions {
	return iengine.RunOptions{
		RunID:             opts.runID,
		Name:              opts.name,
		Params:            opts.params,
		ParamsList:        opts.paramsList,
		DefaultWorkingDir: opts.defaultWorkingDir,
		Mode:              iengine.ExecutionMode(opts.mode),
		WorkerSelector:    opts.workerSelector,
		Labels:            opts.labels,
		DryRun:            opts.dryRun,
	}
}

func internalRunRef(ref RunRef) iengine.RunRef {
	return iengine.RunRef{Name: ref.Name, ID: ref.ID}
}

func publicRunRef(ref iengine.RunRef) RunRef {
	return RunRef{Name: ref.Name, ID: ref.ID}
}

func internalWorkerOptions(opts WorkerOptions) iengine.WorkerOptions {
	return iengine.WorkerOptions{
		ID:            opts.ID,
		MaxActiveRuns: opts.MaxActiveRuns,
		Labels:        cloneMap(opts.Labels),
		Coordinators:  cloneSlice(opts.Coordinators),
		TLS:           internalTLSOptions(opts.TLS),
		HealthPort:    opts.HealthPort,
	}
}

func publicStatus(status *iengine.Status) *Status {
	if status == nil {
		return nil
	}
	return &Status{
		Name:        status.Name,
		RunID:       status.RunID,
		AttemptID:   status.AttemptID,
		Status:      status.Status,
		StartedAt:   status.StartedAt,
		FinishedAt:  status.FinishedAt,
		Error:       status.Error,
		LogFile:     status.LogFile,
		ArchiveDir:  status.ArchiveDir,
		WorkerID:    status.WorkerID,
		TriggerType: status.TriggerType,
	}
}

func cloneMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	maps.Copy(out, values)
	return out
}

func cloneSlice(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	return slices.Clone(values)
}
