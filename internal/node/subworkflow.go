// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

// Package node wires runtime node adapters.
package node

import (
	"context"

	agentpkg "github.com/dagucloud/dagu/internal/agent"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/dagstate"
	"github.com/dagucloud/dagu/internal/runtime"
	runtimeexec "github.com/dagucloud/dagu/internal/runtime/executor"
	"github.com/dagucloud/dagu/internal/runtime/runstate"
	"github.com/dagucloud/dagu/internal/service/coordinator"
	"github.com/dagucloud/dagu/internal/subflow"
)

// DAGStoreFactory creates the DAG definition store used by local child workflows.
type DAGStoreFactory func(context.Context) (exec.DAGStore, error)

// SubWorkflowRunnerConfig contains dependencies for child workflow execution.
type SubWorkflowRunnerConfig struct {
	DAGRunMgr         runtime.Manager
	DAGStore          exec.DAGStore
	DAGStoreFactory   DAGStoreFactory
	DAGRunStore       exec.DAGRunStore
	RunStateStore     runstate.Store
	QueueStore        exec.QueueStore
	StateStore        dagstate.Store
	AgentStores       agentpkg.RuntimeStores
	ServiceRegistry   exec.ServiceRegistry
	PeerConfig        config.Peer
	DefaultExecMode   config.ExecutionMode
	StatusPusher      runtime.StatusPusher
	LogWriterFactory  exec.LogWriterFactory
	ArtifactFinalizer runtime.ArtifactFinalizer
	WorkerID          string
	DAGRunLogDir      string
	DAGRunArtifactDir string
}

// NewSubWorkflowRunnerFactory creates recursive child workflow runners.
func NewSubWorkflowRunnerFactory(cfg SubWorkflowRunnerConfig) func(context.Context) (runtimeexec.SubWorkflowRunner, error) {
	var factory func(context.Context) (runtimeexec.SubWorkflowRunner, error)
	factory = func(ctx context.Context) (runtimeexec.SubWorkflowRunner, error) {
		dagStore, err := subWorkflowDAGStore(ctx, cfg)
		if err != nil {
			return nil, err
		}
		dispatcher, err := coordinator.NewRuntimeDispatcher(cfg.ServiceRegistry, cfg.PeerConfig)
		if err != nil {
			return nil, err
		}
		return subflow.NewRouter(
			subflow.New(dispatcher, cfg.DefaultExecMode),
			subflow.NewLocal(
				cfg.DAGRunMgr,
				dagStore,
				subflow.WithLocalDAGRunStore(cfg.DAGRunStore),
				subflow.WithLocalRunStateStore(cfg.RunStateStore),
				subflow.WithLocalQueueStore(cfg.QueueStore),
				subflow.WithLocalStateStore(cfg.StateStore),
				subflow.WithLocalSecretStore(cfg.AgentStores.SecretStore),
				subflow.WithLocalProfileStore(cfg.AgentStores.ProfileStore),
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

func subWorkflowDAGStore(ctx context.Context, cfg SubWorkflowRunnerConfig) (exec.DAGStore, error) {
	if cfg.DAGStoreFactory != nil {
		return cfg.DAGStoreFactory(ctx)
	}
	return cfg.DAGStore, nil
}
