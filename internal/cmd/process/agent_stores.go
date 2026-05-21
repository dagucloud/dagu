// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package process

import (
	"context"
	"path/filepath"

	"github.com/dagucloud/dagu/internal/agent"
	"github.com/dagucloud/dagu/internal/agentoauth"
	"github.com/dagucloud/dagu/internal/clicontext"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/persis/fileagentconfig"
	"github.com/dagucloud/dagu/internal/persis/fileagentmodel"
	"github.com/dagucloud/dagu/internal/persis/fileagentoauth"
	"github.com/dagucloud/dagu/internal/persis/fileagentsoul"
	"github.com/dagucloud/dagu/internal/persis/filememory"
	"github.com/dagucloud/dagu/internal/persis/filesecret"
	secretpkg "github.com/dagucloud/dagu/internal/secret"
)

// AgentStores contains the stores and resolvers used by CLI and runtime agent flows.
type AgentStores struct {
	ConfigStore     agent.ConfigStore
	ModelStore      agent.ModelStore
	MemoryStore     agent.MemoryStore
	SoulStore       agent.SoulStore
	OAuthManager    *agentoauth.Manager
	ContextResolver agent.RemoteContextResolver
	SecretStore     secretpkg.Store
}

// NewAgentStores creates the agent store bundle for a command process role.
func NewAgentStores(ctx context.Context, cfg *config.Config, contextStore *clicontext.Store) AgentStores {
	var result AgentStores

	if store, err := filesecret.NewFromDataDir(cfg.Paths.DataDir); err != nil {
		logger.Warn(ctx, "Failed to create secret store", tag.Error(err))
	} else {
		result.SecretStore = store
	}

	agentConfigStore, err := fileagentconfig.New(cfg.Paths.DataDir)
	if err != nil {
		logger.Warn(ctx, "Failed to create agent config store", tag.Error(err))
		return result
	}
	if agentConfigStore == nil {
		return result
	}
	result.ConfigStore = agentConfigStore

	agentModelStore, err := fileagentmodel.New(filepath.Join(cfg.Paths.DataDir, "agent", "models"))
	if err != nil {
		logger.Warn(ctx, "Failed to create agent model store", tag.Error(err))
		return result
	}
	result.ModelStore = agentModelStore

	memoryStore, err := filememory.New(cfg.Paths.DAGsDir)
	if err != nil {
		logger.Warn(ctx, "Failed to create agent memory store", tag.Error(err))
		return result
	}
	result.MemoryStore = memoryStore

	soulsDir := filepath.Join(cfg.Paths.DAGsDir, "souls")
	soulStore, err := fileagentsoul.New(ctx, soulsDir)
	if err != nil {
		logger.Warn(ctx, "Failed to create agent soul store", tag.Error(err))
		return result
	}
	result.SoulStore = soulStore

	oauthManager, err := fileagentoauth.NewManager(cfg.Paths.DataDir)
	if err != nil {
		logger.Warn(ctx, "Failed to create agent OAuth store", tag.Error(err))
	} else {
		result.OAuthManager = oauthManager
	}

	if contextStore != nil {
		result.ContextResolver = &agent.RemoteContextResolverAdapter{Store: contextStore}
	}

	return result
}
