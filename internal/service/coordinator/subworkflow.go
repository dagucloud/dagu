// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package coordinator

import (
	"context"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/dagrun"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/profile"
	"github.com/dagucloud/dagu/v2/internal/queue"
	"github.com/dagucloud/dagu/v2/internal/runctx"
	"github.com/dagucloud/dagu/v2/internal/runtime"
	rtagent "github.com/dagucloud/dagu/v2/internal/runtime/agent"
	runtimeexec "github.com/dagucloud/dagu/v2/internal/runtime/executor"
	"github.com/dagucloud/dagu/v2/internal/runtime/runstate"
	"github.com/dagucloud/dagu/v2/internal/secret"
	"github.com/dagucloud/dagu/v2/internal/service/coordinator/subflow"
	"github.com/dagucloud/dagu/v2/internal/serviceregistry"
)

// SubWorkflowRunnerConfig contains dependencies for child workflow execution.
type SubWorkflowRunnerConfig struct {
	// Dispatcher is caller-owned and remains live after a child runner is cleaned up.
	Dispatcher        dispatch.Dispatcher
	DAGRunMgr         runtime.Manager
	DAGRepository     *persis.DAGRepository
	DAGRunRepository  *persis.DAGRunRepository
	RunStateStore     runstate.Store
	QueueStore        queue.QueueStore
	StateStore        dagrun.StateStore
	SecretStore       secret.Store
	ProfileStore      profile.Store
	ServiceRegistry   serviceregistry.ServiceRegistry
	PeerConfig        config.Peer
	DefaultExecMode   config.ExecutionMode
	StatusPusher      runtime.StatusPusher
	LogWriterFactory  runctx.LogWriterFactory
	ArtifactFinalizer runtime.ArtifactFinalizer
	RemoteDAGLoader   rtagent.RemoteDAGLoader
	WorkerID          string
	DAGRunLogDir      string
	DAGRunArtifactDir string
}

// NewSubWorkflowRunnerFactory creates recursive child workflow runners.
func NewSubWorkflowRunnerFactory(cfg SubWorkflowRunnerConfig) func(context.Context) (runtimeexec.SubWorkflowRunner, error) {
	var factory func(context.Context) (runtimeexec.SubWorkflowRunner, error)
	factory = func(context.Context) (runtimeexec.SubWorkflowRunner, error) {
		dispatcher := cfg.Dispatcher
		var runnerOpts []subflow.Option
		if dispatcher == nil {
			var err error
			dispatcher, err = NewRuntimeDispatcher(cfg.ServiceRegistry, cfg.PeerConfig)
			if err != nil {
				return nil, err
			}
		} else {
			runnerOpts = append(runnerOpts, subflow.WithoutDispatcherCleanup())
		}
		return subflow.NewRouter(
			subflow.New(dispatcher, cfg.DefaultExecMode, runnerOpts...),
			subflow.NewLocal(
				cfg.DAGRunMgr,
				cfg.DAGRepository,
				subflow.WithLocalDAGRunRepository(cfg.DAGRunRepository),
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
				subflow.WithLocalRemoteDAGLoader(cfg.RemoteDAGLoader),
				subflow.WithLocalWorkerID(cfg.WorkerID),
				subflow.WithLocalDAGRunDirs(cfg.DAGRunLogDir, cfg.DAGRunArtifactDir),
			),
		), nil
	}
	return factory
}
