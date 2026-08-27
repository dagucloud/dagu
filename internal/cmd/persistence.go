// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"fmt"

	"github.com/dagucloud/dagu/v2/internal/agentsession"
	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/queue"
	"github.com/dagucloud/dagu/v2/internal/runtime/workspacebundle"
	"github.com/dagucloud/dagu/v2/internal/schedulerstate"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
)

// Persistence contains the persistence dependencies shared by commands and services.
type Persistence struct {
	DAGRepository             *persis.DAGRepository
	DAGRunRepository          *persis.DAGRunRepository
	ProcRepository            *persis.ProcRepository
	QueueStore                queue.QueueStore
	StateStore                dagrun.StateStore
	SchedulerStateStore       schedulerstate.Store
	ServiceRegistry           serviceregistry.ServiceRegistry
	DispatchTaskStore         dispatch.DispatchTaskStore
	WorkerHeartbeatStore      dispatch.WorkerHeartbeatStore
	DAGRunLeaseStore          dispatch.DAGRunLeaseStore
	ActiveDistributedRunStore dispatch.ActiveDistributedRunStore
	AgentSessionCleanupQueue  *agentsession.CleanupQueue
}

type filePersistenceOptions struct {
	DAGCache          *fileutil.Cache[*ir.DAG]
	DAGRunStatusCache *fileutil.Cache[*ir.DAGRunStatus]
}

func newFilePersistence(
	ctx context.Context,
	cfg *config.Config,
	backend persis.Backend,
	opts filePersistenceOptions,
) (Persistence, error) {
	procRepository := file.NewProcRepository(cfg)
	if err := procRepository.Validate(ctx); err != nil {
		return Persistence{}, fmt.Errorf("failed to validate proc directory %s: %w", cfg.Paths.ProcDir, err)
	}

	cleanupQueue := agentsession.NewCleanupQueue(
		backend.Collection(persis.CollectionAgentSessionCleanups),
	)
	var dagRunOpts []file.DAGRunRepositoryOption
	if opts.DAGRunStatusCache != nil {
		dagRunOpts = append(dagRunOpts, file.WithDAGRunHistoryFileCache(opts.DAGRunStatusCache))
	}
	dagRunOpts = append(dagRunOpts, file.WithDAGRunRemovalEnqueuer(cleanupQueue))
	dagRunRepository := file.NewDAGRunRepository(cfg, dagRunOpts...)

	dagRunLeaseStore := store.NewDAGRunLeaseStore(
		backend.Collection(persis.CollectionDAGRunLeases),
	)
	activeDistributedRunStore := store.NewActiveDistributedRunStore(
		backend.Collection(persis.CollectionActiveDistributedRuns),
	)
	queueStore := store.NewQueueStore(backend.Collection(persis.CollectionQueue))
	stateStore := store.NewDAGStateStore(backend.Collection(persis.CollectionDAGState))
	schedulerStateStore := store.NewSchedulerStateStore(
		backend.Collection(persis.CollectionSchedulerState),
	)
	serviceRegistry := file.NewServiceRegistry(cfg)
	bundleStore := workspacebundle.NewStore(
		workspacebundle.StoreDir(cfg.Paths.DataDir),
		workspacebundle.DefaultLimits(),
	)
	dispatchTaskStore := store.NewDispatchTaskStore(
		backend.Collection(persis.CollectionDispatchTasks),
		store.WithDispatchAdmissionLiveness(dagRunLeaseStore, activeDistributedRunStore),
		store.WithDispatchTransitionLock(bundleStore.WithLock),
	)
	workerHeartbeatStore := store.NewWorkerHeartbeatStore(
		backend.Collection(persis.CollectionWorkerHeartbeats),
	)
	dagRepository, err := newDAGRepository(cfg, dagRepositoryConfig{Cache: opts.DAGCache})
	if err != nil {
		return Persistence{}, fmt.Errorf("failed to create DAG store: %w", err)
	}

	return Persistence{
		DAGRepository:             dagRepository,
		DAGRunRepository:          dagRunRepository,
		ProcRepository:            procRepository,
		QueueStore:                queueStore,
		StateStore:                stateStore,
		SchedulerStateStore:       schedulerStateStore,
		ServiceRegistry:           serviceRegistry,
		DispatchTaskStore:         dispatchTaskStore,
		WorkerHeartbeatStore:      workerHeartbeatStore,
		DAGRunLeaseStore:          dagRunLeaseStore,
		ActiveDistributedRunStore: activeDistributedRunStore,
		AgentSessionCleanupQueue:  cleanupQueue,
	}, nil
}
