// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/queue"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator"
	"github.com/dagucloud/dagu/v2/internal/service/scheduler"
	"github.com/stretchr/testify/require"
)

// Scheduler represents a test scheduler instance
type Scheduler struct {
	Helper
	EntryReader    scheduler.EntryReader
	QueueStore     queue.QueueStore
	CoordinatorCli dispatch.Dispatcher
}

// SetupScheduler creates a test scheduler instance with all dependencies
func SetupScheduler(t *testing.T, opts ...HelperOption) *Scheduler {
	t.Helper()

	// Create scheduler-specific options
	schedulerOpts := make([]HelperOption, 0, len(opts)+1)

	// Set up a test DAGs directory if not already specified
	var hasDAGsDir bool
	for _, opt := range opts {
		schedulerOpts = append(schedulerOpts, opt)
		// Check if DAGsDir option is already provided
		// This is a simple check, in production code you might want a more robust solution
		if opt != nil {
			hasDAGsDir = true
		}
	}

	// If no DAGsDir specified, use the testdata scheduler directory
	if !hasDAGsDir {
		testdataDir := TestdataPath(t, filepath.Join("scheduler"))
		schedulerOpts = append(schedulerOpts, WithDAGsDir(testdataDir))
	}

	// Create the base helper
	helper := Setup(t, schedulerOpts...)

	// Update config for scheduler-specific settings
	helper.Config.Scheduler.LockStaleThreshold = 30 * time.Second
	helper.Config.Scheduler.LockRetryInterval = 50 * time.Millisecond

	// Create additional stores needed for scheduler
	ds, err := file.NewDAGRepository(helper.Config, file.WithDAGSkipExamples(true))
	require.NoError(t, err)
	dagRunRepository := file.NewDAGRunRepository(helper.Config)
	ps := newProcRepository(helper.Config)
	qs := store.NewQueueStore(helper.Backend.Collection(persis.CollectionQueue))

	// Create DAG run manager
	drm := runtime.NewManager(dagRunRepository, ps, helper.Config)

	// Create entry reader
	coordinatorCli := coordinator.New(helper.ServiceRegistry, CoordinatorClientConfig(helper.Config.Paths.DataDir))
	em := scheduler.NewFileEntryReader(
		helper.Config.Paths.DAGsDir,
		ds,
		helper.Config.DAGDiscovery.Recursive,
	)

	// Update helper with scheduler-specific stores
	helper.DAGRepository = ds
	helper.DAGRunRepository = dagRunRepository
	helper.ProcRepository = ps
	helper.DAGRunMgr = drm

	sch := &Scheduler{
		Helper:         helper,
		EntryReader:    em,
		QueueStore:     qs,
		CoordinatorCli: coordinatorCli,
	}

	return sch
}

// NewSchedulerInstance creates a new scheduler instance for testing
func (s *Scheduler) NewSchedulerInstance(t *testing.T) (*scheduler.Scheduler, error) {
	t.Helper()

	return scheduler.New(s.Config, scheduler.Dependencies{
		EntryReader:       s.EntryReader,
		DAGRunManager:     s.DAGRunMgr,
		DAGRepository:     s.DAGRepository,
		DAGRunRepository:  s.DAGRunRepository,
		QueueStore:        s.QueueStore,
		ProcRepository:    s.ProcRepository,
		ServiceRegistry:   s.ServiceRegistry,
		CoordinatorClient: s.CoordinatorCli,
	})
}

// Start starts the scheduler instance
func (s *Scheduler) Start(t *testing.T, ctx context.Context) (*scheduler.Scheduler, chan error) {
	t.Helper()

	instance, err := s.NewSchedulerInstance(t)
	require.NoError(t, err, "failed to create scheduler instance")

	errCh := make(chan error, 1)
	go func() {
		errCh <- instance.Start(ctx)
	}()

	var startErr error
	var stopped bool
	require.Eventually(t, func() bool {
		select {
		case startErr = <-errCh:
			stopped = true
			return true
		default:
		}
		return instance.IsRunning()
	}, 5*time.Second, 25*time.Millisecond, "scheduler should start")
	require.False(t, stopped, "scheduler exited before it started: %v", startErr)

	return instance, errCh
}

// StartAsync starts the scheduler instance asynchronously
func (s *Scheduler) StartAsync(t *testing.T) (*scheduler.Scheduler, chan error) {
	return s.Start(t, s.Context)
}

// WithSchedulerTestDAGs creates a scheduler option for setting up test DAGs directory
func WithSchedulerTestDAGs(dagsDir string) HelperOption {
	return WithDAGsDir(dagsDir)
}
