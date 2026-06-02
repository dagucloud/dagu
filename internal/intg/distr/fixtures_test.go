// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package distr_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"testing"
	"time"

	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/core"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/launcher"
	"github.com/dagucloud/dagu/internal/persis/file"
	"github.com/dagucloud/dagu/internal/runtime/transform"
	"github.com/dagucloud/dagu/internal/service/coordinator"
	"github.com/dagucloud/dagu/internal/service/scheduler"
	"github.com/dagucloud/dagu/internal/service/worker"
	"github.com/dagucloud/dagu/internal/test"
	"github.com/dagucloud/dagu/internal/test/intgharness"
	coordinatorv1 "github.com/dagucloud/dagu/proto/coordinator/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type workerMode int

const (
	sharedNothingMode workerMode = iota
	sharedFSMode
)

type fixtureConfig struct {
	workerMode              workerMode
	workerCount             int
	workerMaxActiveRuns     int
	workerLabels            map[string]string
	logPersistence          bool
	artifactPersistence     bool
	configMutators          []func(*config.Config)
	dagsDir                 string
	baseConfigPath          string
	workerBaseConfigPath    string // Override worker's base config path (for testing embedded base config)
	procConfig              *procConfig
	staleHeartbeatThreshold time.Duration
	staleLeaseThreshold     time.Duration
	zombieDetectionInterval time.Duration
}

type procConfig struct {
	heartbeatInterval     time.Duration
	heartbeatSyncInterval time.Duration
	staleThreshold        time.Duration
}

type fixtureOption func(*fixtureConfig)

func distrTestTimeout(timeout time.Duration) time.Duration {
	switch {
	case goruntime.GOOS == "windows" && raceEnabled():
		return timeout * 5
	case goruntime.GOOS == "windows":
		return timeout * 5
	case raceEnabled():
		return timeout * 2
	default:
		return timeout
	}
}

func withWorkerMode(mode workerMode) fixtureOption {
	return func(c *fixtureConfig) { c.workerMode = mode }
}

func withWorkerCount(n int) fixtureOption {
	return func(c *fixtureConfig) { c.workerCount = n }
}

func withWorkerMaxActiveRuns(n int) fixtureOption {
	return func(c *fixtureConfig) { c.workerMaxActiveRuns = n }
}

func withLabels(labels map[string]string) fixtureOption {
	return func(c *fixtureConfig) { c.workerLabels = labels }
}

func withLogPersistence() fixtureOption {
	return func(c *fixtureConfig) { c.logPersistence = true }
}

func withArtifactPersistence() fixtureOption {
	return func(c *fixtureConfig) { c.artifactPersistence = true }
}

func withConfigMutator(mutator func(*config.Config)) fixtureOption {
	return func(c *fixtureConfig) {
		c.configMutators = append(c.configMutators, mutator)
	}
}

func withDAGsDir(dir string) fixtureOption {
	return func(c *fixtureConfig) { c.dagsDir = dir }
}

func withBaseConfigPath(path string) fixtureOption {
	return func(c *fixtureConfig) { c.baseConfigPath = path }
}

func withWorkerBaseConfigPath(path string) fixtureOption {
	return func(c *fixtureConfig) { c.workerBaseConfigPath = path }
}

func withProcConfig(heartbeatInterval, heartbeatSyncInterval, staleThreshold time.Duration) fixtureOption {
	return func(c *fixtureConfig) {
		c.procConfig = &procConfig{
			heartbeatInterval:     heartbeatInterval,
			heartbeatSyncInterval: heartbeatSyncInterval,
			staleThreshold:        staleThreshold,
		}
	}
}

func withStaleThresholds(heartbeat, lease time.Duration) fixtureOption {
	return func(c *fixtureConfig) {
		c.staleHeartbeatThreshold = heartbeat
		c.staleLeaseThreshold = lease
	}
}

func withZombieDetectionInterval(interval time.Duration) fixtureOption {
	return func(c *fixtureConfig) {
		c.zombieDetectionInterval = interval
	}
}

type testFixture struct {
	t                   *testing.T
	coord               *test.Coordinator
	h                   intgharness.Harness
	dagWrapper          *test.DAG
	coordinatorClient   coordinator.Client
	workerMaxActiveRuns int
	workers             []*worker.Worker
	scheduler           *scheduler.Scheduler
	schedulerCancel     context.CancelFunc
	schedulerCtx        context.Context
	schedulerErrCh      chan error
	schedulerErr        error
	schedulerErrSet     bool
}

func newTestFixture(t *testing.T, yaml string, opts ...fixtureOption) *testFixture {
	t.Helper()

	cfg := &fixtureConfig{
		workerMode:          sharedNothingMode,
		workerCount:         1,
		workerMaxActiveRuns: 10,
		workerLabels:        map[string]string{"test": "true"},
	}
	for _, opt := range opts {
		opt(cfg)
	}

	var coordOpts []test.HelperOption
	coordOpts = append(coordOpts, test.WithStatusPersistence())
	coordOpts = append(coordOpts, test.WithConfigMutator(func(c *config.Config) {
		c.Queues.Enabled = true
		c.Scheduler.Port = 0
		if cfg.procConfig != nil {
			c.Proc.HeartbeatInterval = cfg.procConfig.heartbeatInterval
			c.Proc.HeartbeatSyncInterval = cfg.procConfig.heartbeatSyncInterval
			c.Proc.StaleThreshold = cfg.procConfig.staleThreshold
		}
		if cfg.zombieDetectionInterval > 0 {
			c.Scheduler.ZombieDetectionInterval = cfg.zombieDetectionInterval
		}
	}))
	if cfg.logPersistence {
		coordOpts = append(coordOpts, test.WithLogPersistence())
	}
	if cfg.artifactPersistence {
		coordOpts = append(coordOpts, test.WithArtifactPersistence())
	}
	if cfg.workerMode == sharedFSMode {
		coordOpts = append(coordOpts, test.WithBuiltExecutable())
	}
	if cfg.dagsDir != "" {
		coordOpts = append(coordOpts, test.WithDAGsDir(cfg.dagsDir))
	}
	if cfg.baseConfigPath != "" {
		coordOpts = append(coordOpts, test.WithConfigMutator(func(c *config.Config) {
			c.Paths.BaseConfig = cfg.baseConfigPath
		}))
	}
	if cfg.staleHeartbeatThreshold > 0 || cfg.staleLeaseThreshold > 0 {
		coordOpts = append(coordOpts, test.WithStaleThresholds(cfg.staleHeartbeatThreshold, cfg.staleLeaseThreshold))
	}
	for _, mutate := range cfg.configMutators {
		coordOpts = append(coordOpts, test.WithConfigMutator(mutate))
	}

	coord := test.SetupCoordinator(t, coordOpts...)
	coord.Config.Queues.Enabled = true

	f := &testFixture{
		t:                   t,
		coord:               coord,
		h:                   intgharness.New(t, coord.Helper),
		coordinatorClient:   coord.GetCoordinatorClient(t),
		workerMaxActiveRuns: cfg.workerMaxActiveRuns,
	}

	for i := range cfg.workerCount {
		workerID := fmt.Sprintf("worker-%d", i+1)
		var w *worker.Worker
		switch cfg.workerMode {
		case sharedNothingMode:
			w = f.setupSharedNothingWorker(workerID, cfg.workerLabels, cfg.workerBaseConfigPath)
		case sharedFSMode:
			w = f.setupSharedFSWorker(workerID, cfg.workerLabels)
		}
		f.workers = append(f.workers, w)
	}

	f.dagWrapper = new(coord.DAG(t, yaml))

	return f
}

func (f *testFixture) setupSharedNothingWorker(workerID string, labels map[string]string, workerBaseConfigPath string) *worker.Worker {
	return f.setupSharedNothingWorkerWithAfterAckHook(workerID, labels, workerBaseConfigPath, nil)
}

func (f *testFixture) setupSharedNothingWorkerWithAfterAckHook(
	workerID string,
	labels map[string]string,
	workerBaseConfigPath string,
	afterAckHook func(context.Context, *coordinatorv1.Task) bool,
) *worker.Worker {
	f.t.Helper()

	workerConfig := f.coord.Config
	if workerBaseConfigPath != "" {
		// Create a copy of the config with a different base config path for the worker.
		// This simulates workers that don't have local access to the base config file.
		cfgCopy := *workerConfig
		pathsCopy := cfgCopy.Paths
		pathsCopy.BaseConfig = workerBaseConfigPath
		cfgCopy.Paths = pathsCopy
		workerConfig = &cfgCopy
	}

	handlerCfg := worker.RemoteTaskHandlerConfig{
		WorkerID:          workerID,
		CoordinatorClient: f.coordinatorClient,
		DAGRunStore:       nil,
		DAGStore:          f.coord.DAGStore,
		DAGRunMgr:         f.coord.DAGRunMgr,
		ServiceRegistry:   f.coord.ServiceRegistry,
		PeerConfig:        f.coord.Config.Core.Peer,
		Config:            workerConfig,
	}

	w := worker.NewWorker(workerID, f.workerMaxActiveRuns, f.coordinatorClient, labels, f.coord.Config)
	w.SetHandler(worker.NewRemoteTaskHandler(handlerCfg))
	if afterAckHook != nil {
		w.SetAfterTaskAckHook(afterAckHook)
	}

	return f.startWorker(w, workerID)
}

func (f *testFixture) setupSharedFSWorker(workerID string, labels map[string]string) *worker.Worker {
	return f.setupSharedFSWorkerWithAfterAckHook(workerID, labels, nil)
}

func (f *testFixture) setupSharedFSWorkerWithAfterAckHook(
	workerID string,
	labels map[string]string,
	afterAckHook func(context.Context, *coordinatorv1.Task) bool,
) *worker.Worker {
	f.t.Helper()

	w := worker.NewWorker(
		workerID,
		f.workerMaxActiveRuns,
		f.coordinatorClient,
		labels,
		f.coord.Config,
		worker.WithDAGRunStore(f.coord.DAGRunStore),
	)
	w.SetHandler(worker.NewTaskHandlerWithDAGRunStore(f.coord.Config, f.coord.DAGRunStore))
	if afterAckHook != nil {
		w.SetAfterTaskAckHook(afterAckHook)
	}

	return f.startWorker(w, workerID)
}

func (f *testFixture) startWorker(w *worker.Worker, workerID string) *worker.Worker {
	f.t.Helper()

	go func() {
		if err := w.Start(f.coord.Context); err != nil {
			f.t.Logf("Worker stopped: %v", err)
		}
	}()

	f.waitForWorkerRegistration(workerID, 5*time.Second)

	f.t.Cleanup(func() {
		if err := w.Stop(f.coord.Context); err != nil {
			f.t.Logf("Error stopping worker: %v", err)
		}
	})

	return w
}

func (f *testFixture) waitForWorkerRegistration(workerID string, timeout time.Duration) {
	f.t.Helper()
	f.h.Wait.EventuallyEveryWithin(fmt.Sprintf("worker %s should register with coordinator", workerID), distrTestTimeout(timeout), 50*time.Millisecond, func() bool {
		workers, err := f.coordinatorClient.GetWorkers(f.coord.Context)
		if err != nil {
			return false
		}
		for _, w := range workers {
			if w.WorkerId == workerID {
				return true
			}
		}
		return false
	})
}

func (f *testFixture) startScheduler(timeout time.Duration) {
	f.startSchedulerWithClock(timeout, nil)
}

func (f *testFixture) startSchedulerWithClock(timeout time.Duration, clock scheduler.Clock) {
	f.startSchedulerWithOptions(
		timeout,
		clock,
		func() scheduler.WatermarkStore {
			wmBackend, err := file.New(f.coord.Config.Paths.DataDir)
			require.NoError(f.t, err)
			return scheduler.NewWatermarkStore(wmBackend.Collection("scheduler"))
		}(),
	)
}

func (f *testFixture) startSchedulerWithOptions(
	timeout time.Duration,
	clock scheduler.Clock,
	watermarkStore scheduler.WatermarkStore,
) {
	f.t.Helper()

	em := scheduler.NewEntryReader(f.coord.Config.Paths.DAGsDir, f.coord.DAGStore)

	schedulerInst, err := scheduler.New(
		f.coord.Config,
		em,
		f.coord.DAGRunMgr,
		f.coord.DAGRunStore,
		f.coord.QueueStore,
		f.coord.ProcStore,
		f.coord.ServiceRegistry,
		f.coordinatorClient,
		watermarkStore,
	)
	require.NoError(f.t, err)
	schedulerInst.SetDAGRunLeaseStore(f.coord.DAGRunLeaseStore)
	if clock != nil {
		schedulerInst.SetClock(clock)
	}

	startupTimeout := timeout
	if startupTimeout <= 0 {
		startupTimeout = 5 * time.Second
	}
	startupTimeout = distrTestTimeout(startupTimeout)

	schedulerCtx, schedulerCancel := f.schedulerCtx, f.schedulerCancel
	ownsSchedulerCtx := false
	if schedulerCtx == nil || schedulerCancel == nil {
		schedulerCtx, schedulerCancel = context.WithCancel(f.coord.Context)
		ownsSchedulerCtx = true
	}
	schedulerErrCh := make(chan error, 1)

	f.scheduler = schedulerInst
	f.schedulerCtx = schedulerCtx
	f.schedulerCancel = schedulerCancel
	f.schedulerErrCh = schedulerErrCh
	f.schedulerErr = nil
	f.schedulerErrSet = false
	go func(s *scheduler.Scheduler, ctx context.Context, errCh chan<- error) {
		errCh <- s.Start(ctx)
	}(schedulerInst, schedulerCtx, schedulerErrCh)

	var startErr error
	startTicker := time.NewTicker(50 * time.Millisecond)
	defer startTicker.Stop()

	startTimer := time.NewTimer(startupTimeout)
	defer startTimer.Stop()

	for !f.scheduler.IsRunning() {

		startErr = f.pollSchedulerErr()
		if startErr != nil {
			break
		}

		select {
		case <-startTicker.C:
		case <-startTimer.C:
			if ownsSchedulerCtx && schedulerCancel != nil {
				schedulerCancel()
				f.h.Wait.EventuallyEveryWithin("scheduler startup did not stop after cancellation", distrTestTimeout(time.Second), 25*time.Millisecond, func() bool {
					startErr = f.pollSchedulerErr()
					return startErr != nil
				})
			}

			if startErr != nil {
				require.FailNow(f.t, fmt.Sprintf("scheduler did not start in time: %v", startErr))
			}
			require.FailNow(f.t, "scheduler did not start in time")
		}
	}
	require.NoError(f.t, startErr)
}

func (f *testFixture) enqueue() error {
	f.t.Helper()
	return f.enqueueWithParams("")
}

func (f *testFixture) enqueueWithParams(params string) error {
	f.t.Helper()
	subCmdBuilder := launcher.NewSubCmdBuilder(f.coord.Config)
	enqueueSpec := subCmdBuilder.Enqueue(f.dagWrapper.DAG, launcher.EnqueueOptions{
		Quiet:  true,
		Params: params,
	})
	return launcher.Run(f.coord.Context, enqueueSpec)
}

func (f *testFixture) enqueueDirect() error {
	f.t.Helper()

	runID, err := f.coord.DAGRunMgr.GenDAGRunID(f.coord.Context)
	if err != nil {
		return err
	}

	dagCopy := f.dagWrapper.Clone()
	dagCopy.Location = ""

	att, err := f.coord.DAGRunStore.CreateAttempt(f.coord.Context, dagCopy, time.Now(), runID, exec.NewDAGRunAttemptOptions{})
	if err != nil {
		return err
	}

	logFile := filepath.Join(f.coord.Config.Paths.LogDir, dagCopy.Name, runID+".log")
	if err := os.MkdirAll(filepath.Dir(logFile), 0755); err != nil {
		return err
	}

	status := transform.NewStatusBuilder(dagCopy).Create(
		runID,
		core.Queued,
		0,
		time.Time{},
		transform.WithLogFilePath(logFile),
		transform.WithAttemptID(att.ID()),
		transform.WithHierarchyRefs(exec.NewDAGRunRef(dagCopy.Name, runID), exec.DAGRunRef{}),
		transform.WithTriggerType(core.TriggerTypeManual),
	)

	if err := att.Open(f.coord.Context); err != nil {
		return err
	}
	if err := att.Write(f.coord.Context, status); err != nil {
		_ = att.Close(f.coord.Context)
		return err
	}
	if err := att.Close(f.coord.Context); err != nil {
		return err
	}

	return f.coord.QueueStore.Enqueue(
		f.coord.Context,
		dagCopy.ProcGroup(),
		exec.QueuePriorityLow,
		exec.NewDAGRunRef(dagCopy.Name, runID),
	)
}

func (f *testFixture) enqueueCatchup(scheduleTime time.Time) (string, error) {
	f.t.Helper()

	runID, err := f.coord.DAGRunMgr.GenDAGRunID(f.coord.Context)
	if err != nil {
		return "", err
	}

	err = scheduler.EnqueueCatchupRun(
		f.coord.Context,
		f.coord.DAGRunStore,
		f.coord.QueueStore,
		f.coord.Config.Paths.LogDir,
		f.coord.Config.Paths.ArtifactDir,
		f.coord.Config.Paths.BaseConfig,
		f.dagWrapper.DAG,
		runID,
		core.TriggerTypeCatchUp,
		scheduleTime,
		"",
	)
	if err != nil {
		return "", err
	}

	return runID, nil
}

func (f *testFixture) start() error {
	f.t.Helper()
	return f.startWithLabels("")
}

func (f *testFixture) startWithLabels(labels string) error {
	f.t.Helper()
	subCmdBuilder := launcher.NewSubCmdBuilder(f.coord.Config)
	startSpec := subCmdBuilder.Start(f.dagWrapper.DAG, launcher.StartOptions{Quiet: true, Labels: labels})
	return launcher.Start(f.coord.Context, startSpec)
}

func (f *testFixture) retry(dagRunID string) error {
	f.t.Helper()
	subCmdBuilder := launcher.NewSubCmdBuilder(f.coord.Config)
	retrySpec := subCmdBuilder.Retry(f.dagWrapper.DAG, dagRunID, "")
	return launcher.Start(f.coord.Context, retrySpec)
}

func (f *testFixture) waitForQueued() {
	f.t.Helper()
	f.requireEventuallyNoSchedulerError("DAG should be enqueued", 5*time.Second, 100*time.Millisecond, func() bool {
		items, err := f.coord.QueueStore.ListByDAGName(f.coord.Context, f.dagWrapper.ProcGroup(), f.dagWrapper.Name)
		return err == nil && len(items) == 1
	})
}

func (f *testFixture) waitForStatus(expected core.Status, timeout time.Duration) exec.DAGRunStatus {
	f.t.Helper()
	var status exec.DAGRunStatus
	f.requireEventuallyNoSchedulerError(fmt.Sprintf("timeout waiting for status %s", expected), timeout, 100*time.Millisecond, func() bool {
		var err error
		status, err = f.latestStoredStatus()
		if err != nil {
			return false
		}
		return status.Status == expected
	})
	return status
}

func (f *testFixture) waitForStatusIn(expected []core.Status, timeout time.Duration) exec.DAGRunStatus {
	f.t.Helper()
	var status exec.DAGRunStatus
	f.requireEventuallyNoSchedulerError(fmt.Sprintf("timeout waiting for status in %v", expected), timeout, 100*time.Millisecond, func() bool {
		var err error
		status, err = f.latestStoredStatus()
		if err != nil {
			return false
		}
		return slices.Contains(expected, status.Status)
	})
	return status
}

func (f *testFixture) waitForRunReleasedFromWorkers(dagRunID string, timeout time.Duration) {
	f.t.Helper()
	f.requireEventuallyNoSchedulerError(fmt.Sprintf("DAG run %s should be released from workers", dagRunID), timeout, 100*time.Millisecond, func() bool {
		workers, err := f.coordinatorClient.GetWorkers(f.coord.Context)
		if err != nil {
			return false
		}
		for _, worker := range workers {
			for _, task := range worker.RunningTasks {
				if task != nil && task.DagRunId == dagRunID {
					return false
				}
			}
		}
		return true
	})
}

func (f *testFixture) requireEventuallyNoSchedulerError(label string, timeout, interval time.Duration, condition func() bool) {
	f.t.Helper()
	var schedulerErr error
	f.h.Wait.EventuallyEveryWithin(label, distrTestTimeout(timeout), interval, func() bool {
		schedulerErr = f.pollSchedulerErr()
		if schedulerErr != nil {
			return true
		}
		return condition()
	})
	require.NoError(f.t, schedulerErr)
}

func (f *testFixture) pollSchedulerErr() error {
	if f.schedulerErrSet {
		return f.schedulerErr
	}
	if f.schedulerErrCh == nil {
		return nil
	}

	select {
	case err := <-f.schedulerErrCh:
		if err == nil {
			err = fmt.Errorf("scheduler exited unexpectedly")
		}
		f.schedulerErr = err
		f.schedulerErrSet = true
	default:
		return nil
	}

	return f.schedulerErr
}

func (f *testFixture) latestStatus() (exec.DAGRunStatus, error) {
	return f.latestStoredStatus()
}

func (f *testFixture) latestStoredStatus() (exec.DAGRunStatus, error) {
	store := file.NewDAGRunStore(f.coord.Config)

	attempt, err := store.LatestAttempt(f.coord.Context, f.dagWrapper.Name)
	if err != nil {
		return exec.DAGRunStatus{}, err
	}

	status, err := attempt.ReadStatus(f.coord.Context)
	if err != nil {
		return exec.DAGRunStatus{}, err
	}
	if status == nil {
		return exec.DAGRunStatus{}, exec.ErrCorruptedStatusFile
	}

	return *status, nil
}

func (f *testFixture) stop(dagRunID string) error {
	return f.coord.DAGRunMgr.Stop(f.coord.Context, f.dagWrapper.DAG, dagRunID)
}

func (f *testFixture) cleanup() {
	f.t.Helper()

	f.stopScheduler()
	f.schedulerErr = nil
	f.schedulerErrSet = false
}

func (f *testFixture) stopScheduler() {
	f.t.Helper()

	schedulerInst := f.scheduler
	schedulerCancel := f.schedulerCancel
	schedulerErrCh := f.schedulerErrCh
	schedulerErrSet := f.schedulerErrSet

	f.scheduler = nil
	f.schedulerCtx = nil
	f.schedulerCancel = nil
	f.schedulerErrCh = nil

	if schedulerCancel != nil {
		schedulerCancel()
	}
	if schedulerInst != nil {
		schedulerInst.Stop(context.Background())
	}
	if schedulerErrCh != nil && !schedulerErrSet {
		select {
		case err := <-schedulerErrCh:
			if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
				f.t.Logf("scheduler stopped with error: %v", err)
			}
		case <-time.After(5 * time.Second):
			f.t.Log("scheduler did not stop within 5 seconds")
		}
	}
}

func (f *testFixture) assertAllNodesSucceeded(status exec.DAGRunStatus) {
	f.t.Helper()
	for _, node := range status.Nodes {
		require.Equal(f.t, core.NodeSucceeded, node.Status, "step %s should have succeeded", node.Step.Name)
	}
}

func (f *testFixture) assertWorkerID(status exec.DAGRunStatus, expected string) {
	f.t.Helper()
	require.Equal(f.t, expected, status.WorkerID, "unexpected worker ID")
}

func (f *testFixture) logDir() string {
	return f.coord.LogDir()
}

func (f *testFixture) artifactDir() string {
	return f.coord.Config.Paths.ArtifactDir
}

func findLogFiles(t *testing.T, logDir, dagName, dagRunID, stepName, suffix string) []string {
	t.Helper()

	baseDir := filepath.Join(logDir, dagName, dagRunID)
	var matches []string

	err := filepath.Walk(baseDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() {
			filename := fmt.Sprintf("%s.%s.log", stepName, suffix)
			if filepath.Base(path) == filename {
				matches = append(matches, path)
			}
		}
		return nil
	})
	if err != nil {
		t.Logf("Error walking log directory: %v", err)
	}

	return matches
}

func assertLogContains(t *testing.T, logDir, dagName, dagRunID, stepName, expected string) {
	t.Helper()

	matches := findLogFiles(t, logDir, dagName, dagRunID, stepName, "stdout")
	require.NotEmpty(t, matches, "no stdout log file found for step %s", stepName)

	content, err := os.ReadFile(matches[0])
	require.NoError(t, err, "failed to read log file %s", matches[0])
	assert.Contains(t, string(content), expected, "log file should contain expected content")
}

func assertLogExists(t *testing.T, logDir, dagName, dagRunID, stepName string) string {
	t.Helper()

	matches := findLogFiles(t, logDir, dagName, dagRunID, stepName, "stdout")
	require.NotEmpty(t, matches, "no stdout log file found for step %s", stepName)
	return matches[0]
}

func getLogContent(t *testing.T, logPath string) string {
	t.Helper()
	content, err := os.ReadFile(logPath)
	require.NoError(t, err, "failed to read log file")
	return string(content)
}

func assertArtifactContains(t *testing.T, archiveDir, relativePath, expected string) {
	t.Helper()

	artifactPath := filepath.Join(archiveDir, relativePath)
	require.FileExists(t, artifactPath, "artifact should exist: %s", relativePath)

	content, err := os.ReadFile(artifactPath)
	require.NoError(t, err, "failed to read artifact file %s", artifactPath)
	assert.Contains(t, string(content), expected, "artifact file should contain expected content")
}
