// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"fmt"
	"path/filepath"

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
	opts filePersistenceOptions,
) (Persistence, error) {
	procRepository := file.NewProcRepository(cfg)
	if err := procRepository.Validate(ctx); err != nil {
		return Persistence{}, fmt.Errorf("failed to validate proc directory %s: %w", cfg.Paths.ProcDir, err)
	}

	cleanupQueue := file.NewAgentSessionCleanupQueue(cfg)
	var dagRunOpts []file.DAGRunRepositoryOption
	if opts.DAGRunStatusCache != nil {
		dagRunOpts = append(dagRunOpts, file.WithDAGRunHistoryFileCache(opts.DAGRunStatusCache))
	}
	dagRunOpts = append(dagRunOpts, file.WithDAGRunRemovalEnqueuer(cleanupQueue))
	dagRunRepository := file.NewDAGRunRepository(cfg, dagRunOpts...)

	distributedDir := filepath.Join(cfg.Paths.DataDir, "distributed")
	dagRunLeaseStore := store.NewDAGRunLeaseStore(
		file.NewCollection(filepath.Join(distributedDir, "leases")),
	)
	activeDistributedRunStore := store.NewActiveDistributedRunStore(
		file.NewCollection(filepath.Join(distributedDir, "active-runs")),
	)
	queueStore := store.NewQueueStore(file.NewCollection(cfg.Paths.QueueDir))
	stateStore := store.NewDAGStateStore(file.NewCollection(cfg.Paths.DAGStateDir))
	schedulerStateStore := store.NewSchedulerStateStore(
		file.NewCollection(filepath.Join(cfg.Paths.DataDir, "scheduler"), file.WithIndentedJSON()),
	)
	serviceRegistry := file.NewServiceRegistry(cfg)
	dispatchTaskStore := store.NewDispatchTaskStore(
		file.NewCollection(distributedDir),
		store.WithDispatchAdmissionLiveness(dagRunLeaseStore, activeDistributedRunStore),
	)
	workerHeartbeatStore := store.NewWorkerHeartbeatStore(
		file.NewCollection(filepath.Join(distributedDir, "workers")),
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
