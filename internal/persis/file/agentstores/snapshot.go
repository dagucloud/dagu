// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agentstores

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/dagucloud/dagu/internal/agent"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/persis/fileagentconfig"
	"github.com/dagucloud/dagu/internal/persis/fileagentmodel"
	"github.com/dagucloud/dagu/internal/persis/fileagentsoul"
	"github.com/dagucloud/dagu/internal/persis/filememory"
)

// NewSnapshotStores wires the file-backed stores required to build worker snapshots.
func NewSnapshotStores(ctx context.Context, paths config.PathsConfig) (agent.SnapshotStores, error) {
	configStore, err := fileagentconfig.New(paths.DataDir)
	if err != nil {
		return agent.SnapshotStores{}, fmt.Errorf("create agent config store: %w", err)
	}
	modelStore, err := fileagentmodel.New(filepath.Join(paths.DataDir, "agent", "models"))
	if err != nil {
		return agent.SnapshotStores{}, fmt.Errorf("create agent model store: %w", err)
	}
	soulStore, err := fileagentsoul.New(ctx, filepath.Join(paths.DAGsDir, "souls"))
	if err != nil {
		return agent.SnapshotStores{}, fmt.Errorf("create agent soul store: %w", err)
	}
	memoryStore, err := filememory.New(paths.DAGsDir)
	if err != nil {
		return agent.SnapshotStores{}, fmt.Errorf("create agent memory store: %w", err)
	}
	return agent.SnapshotStores{
		ConfigStore: configStore,
		ModelStore:  modelStore,
		SoulStore:   soulStore,
		MemoryStore: memoryStore,
	}, nil
}
