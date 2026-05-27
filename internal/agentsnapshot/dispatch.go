// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agentsnapshot

import (
	"context"
	"errors"

	"github.com/dagucloud/dagu/internal/agent"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/core"
	coreexec "github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/core/spec"
	"github.com/dagucloud/dagu/internal/workspace"
)

type StoreFactory func(context.Context, config.PathsConfig) (agent.SnapshotStores, error)

// BuildFromPaths builds a worker snapshot from fresh filesystem-backed stores.
func BuildFromPaths(
	ctx context.Context,
	dag *core.DAG,
	paths config.PathsConfig,
	dagStore coreexec.DAGStore,
	storeFactory StoreFactory,
) ([]byte, error) {
	var resolve agent.DAGResolver
	if dagStore != nil {
		resolve = func(ctx context.Context, name string) (*core.DAG, error) {
			loadOpts := []spec.LoadOption{
				spec.WithBaseConfig(paths.BaseConfig),
				spec.WithWorkspaceBaseConfigDir(workspace.BaseConfigDir(paths.DAGsDir)),
			}
			return dagStore.GetDetails(ctx, name, loadOpts...)
		}
	}

	needsSnapshot, err := agent.NeedsSnapshotForDAG(ctx, dag, resolve)
	if err != nil {
		return nil, err
	}
	if !needsSnapshot {
		return nil, nil
	}

	if storeFactory == nil {
		return nil, errors.New("agentsnapshot: snapshot store factory is not configured")
	}
	stores, err := storeFactory(ctx, paths)
	if err != nil {
		return nil, err
	}

	return agent.BuildSnapshotForDAG(ctx, dag, stores, agent.SnapshotBuildOptions{
		ResolveDAG: resolve,
		MaxBytes:   agent.DefaultSnapshotMaxBytes,
	})
}

// BuildFromContext builds a worker snapshot from the stores already injected into a runtime context.
func BuildFromContext(ctx context.Context, dag *core.DAG) ([]byte, error) {
	rCtx := coreexec.GetContext(ctx)

	var resolve agent.DAGResolver
	if rCtx.DB != nil {
		resolve = func(ctx context.Context, name string) (*core.DAG, error) {
			return rCtx.DB.GetDAG(ctx, name)
		}
	}

	return agent.BuildSnapshotForDAG(ctx, dag, agent.SnapshotStores{
		ConfigStore: agent.GetConfigStore(ctx),
		ModelStore:  agent.GetModelStore(ctx),
		SoulStore:   agent.GetSoulStore(ctx),
		MemoryStore: agent.GetMemoryStore(ctx),
	}, agent.SnapshotBuildOptions{
		ResolveDAG: resolve,
		MaxBytes:   agent.DefaultSnapshotMaxBytes,
	})
}
