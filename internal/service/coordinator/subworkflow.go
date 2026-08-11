// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"context"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/profile"
	"github.com/dagucloud/dagu/v2/internal/queue"
	"github.com/dagucloud/dagu/v2/internal/runctx"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	runtimeexec "github.com/dagucloud/dagu/v2/internal/runtime/executor"
	"github.com/dagucloud/dagu/v2/internal/runtime/runstate"
	"github.com/dagucloud/dagu/v2/internal/secret"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator/subflow"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
)

// DAGRepositoryFactory creates the DAG definition store used by local child workflows.
type DAGRepositoryFactory func(context.Context) (*persis.DAGRepository, error)

// SubWorkflowRunnerConfig contains dependencies for child workflow execution.
type SubWorkflowRunnerConfig struct {
	DAGRunMgr            runtime.Manager
	DAGRepository        *persis.DAGRepository
	DAGRepositoryFactory DAGRepositoryFactory
	DAGRunStore          dagrun.DAGRunStore
	RunStateStore        runstate.Store
	QueueStore           queue.QueueStore
	StateStore           dagrun.StateStore
	SecretStore          secret.Store
	ProfileStore         profile.Store
	ServiceRegistry      serviceregistry.ServiceRegistry
	PeerConfig           config.Peer
	DefaultExecMode      config.ExecutionMode
	StatusPusher         runtime.StatusPusher
	LogWriterFactory     runctx.LogWriterFactory
	ArtifactFinalizer    runtime.ArtifactFinalizer
	WorkerID             string
	DAGRunLogDir         string
	DAGRunArtifactDir    string
}

// NewSubWorkflowRunnerFactory creates recursive child workflow runners.
func NewSubWorkflowRunnerFactory(cfg SubWorkflowRunnerConfig) func(context.Context) (runtimeexec.SubWorkflowRunner, error) {
	var factory func(context.Context) (runtimeexec.SubWorkflowRunner, error)
	factory = func(ctx context.Context) (runtimeexec.SubWorkflowRunner, error) {
		dagRepository, err := subWorkflowDAGRepository(ctx, cfg)
		if err != nil {
			return nil, err
		}
		dispatcher, err := NewRuntimeDispatcher(cfg.ServiceRegistry, cfg.PeerConfig)
		if err != nil {
			return nil, err
		}
		return subflow.NewRouter(
			subflow.New(dispatcher, cfg.DefaultExecMode),
			subflow.NewLocal(
				cfg.DAGRunMgr,
				dagRepository,
				subflow.WithLocalDAGRunStore(cfg.DAGRunStore),
				subflow.WithLocalRunStateStore(cfg.RunStateStore),
				subflow.WithLocalQueueStore(cfg.QueueStore),
				subflow.WithLocalStateStore(cfg.StateStore),
				subflow.WithLocalSecretStore(cfg.SecretStore),
				subflow.WithLocalProfileStore(cfg.ProfileStore),
				subflow.WithLocalServiceRegistry(cfg.ServiceRegistry),
				subflow.WithLocalStatusPusher(cfg.StatusPusher),
				subflow.WithLocalLogWriterFactory(cfg.LogWriterFactory),
				subflow.WithLocalArtifactFinalizer(cfg.ArtifactFinalizer),
				subflow.WithLocalSubWorkflowRunnerFactory(factory),
				subflow.WithLocalWorkerID(cfg.WorkerID),
				subflow.WithLocalDAGRunDirs(cfg.DAGRunLogDir, cfg.DAGRunArtifactDir),
			),
		), nil
	}
	return factory
}

func subWorkflowDAGRepository(ctx context.Context, cfg SubWorkflowRunnerConfig) (*persis.DAGRepository, error) {
	if cfg.DAGRepositoryFactory != nil {
		return cfg.DAGRepositoryFactory(ctx)
	}
	return cfg.DAGRepository, nil
}
