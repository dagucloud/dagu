// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package process

import (
	"context"

	"github.com/dagucloud/dagu/internal/clicontext"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/persis/file"
)

// AgentStores contains the stores and resolvers used by CLI and runtime agent flows.
type AgentStores = file.AgentStores

// NewAgentStores creates the agent store bundle for a command process role.
func NewAgentStores(ctx context.Context, cfg *config.Config, contextStore *clicontext.Store) AgentStores {
	return file.NewAgentStores(
		ctx,
		cfg,
		file.WithAgentContextStore(contextStore),
		file.WithAgentSeedReferences(),
	)
}

// NewRuntimeAgentStores creates the agent stores used by worker/runtime execution.
func NewRuntimeAgentStores(ctx context.Context, cfg *config.Config) AgentStores {
	return file.NewAgentStores(ctx, cfg)
}
