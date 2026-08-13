// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package process

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/persis/store"
	"github.com/dagucloud/dagu/v2/internal/queue"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
)

// CorePersistence contains the persistence dependencies shared by command process roles.
type CorePersistence struct {
	DAGRepository             *persis.DAGRepository
	DAGRunRepository          *persis.DAGRunRepository
	ProcRepository            *persis.ProcRepository
	QueueStore                queue.QueueStore
	StateStore                dagrun.StateStore
	ServiceRegistry           serviceregistry.ServiceRegistry
	DispatchTaskStore         dispatch.DispatchTaskStore
	WorkerHeartbeatStore      dispatch.WorkerHeartbeatStore
	DAGRunLeaseStore          dispatch.DAGRunLeaseStore
	ActiveDistributedRunStore dispatch.ActiveDistributedRunStore
}

// FileCorePersistenceOptions configures caches used by file-backed repositories.
type FileCorePersistenceOptions struct {
	DAGCache          *fileutil.Cache[*ir.DAG]
	DAGRunStatusCache *fileutil.Cache[*ir.DAGRunStatus]
}

// NewFileCorePersistence creates the file-backed persistence used by command process roles.
func NewFileCorePersistence(
	ctx context.Context,
	cfg *config.Config,
	opts FileCorePersistenceOptions,
) (CorePersistence, error) {
	procRepository := file.NewProcRepository(cfg)
	if err := procRepository.Validate(ctx); err != nil {
		return CorePersistence{}, fmt.Errorf("failed to validate proc directory %s: %w", cfg.Paths.ProcDir, err)
	}

	var dagRunOpts []file.DAGRunRepositoryOption
	if opts.DAGRunStatusCache != nil {
		dagRunOpts = append(dagRunOpts, file.WithDAGRunHistoryFileCache(opts.DAGRunStatusCache))
	}
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
	serviceRegistry := file.NewServiceRegistry(cfg)
	dispatchTaskStore := store.NewDispatchTaskStore(
		file.NewCollection(distributedDir),
		store.WithDispatchAdmissionLiveness(dagRunLeaseStore, activeDistributedRunStore),
	)
	workerHeartbeatStore := store.NewWorkerHeartbeatStore(
		file.NewCollection(filepath.Join(distributedDir, "workers")),
	)
	dagRepository, err := NewDAGRepository(cfg, DAGRepositoryConfig{Cache: opts.DAGCache})
	if err != nil {
		return CorePersistence{}, fmt.Errorf("failed to create DAG store: %w", err)
	}

	return CorePersistence{
		DAGRepository:             dagRepository,
		DAGRunRepository:          dagRunRepository,
		ProcRepository:            procRepository,
		QueueStore:                queueStore,
		StateStore:                stateStore,
		ServiceRegistry:           serviceRegistry,
		DispatchTaskStore:         dispatchTaskStore,
		WorkerHeartbeatStore:      workerHeartbeatStore,
		DAGRunLeaseStore:          dagRunLeaseStore,
		ActiveDistributedRunStore: activeDistributedRunStore,
	}, nil
}
