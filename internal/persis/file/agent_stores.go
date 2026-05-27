// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file

import (
	"context"
	"path/filepath"

	"github.com/dagucloud/dagu/internal/agent"
	"github.com/dagucloud/dagu/internal/agentoauth"
	"github.com/dagucloud/dagu/internal/clicontext"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/cmn/crypto"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/persis/fileagentconfig"
	"github.com/dagucloud/dagu/internal/persis/fileagentmodel"
	"github.com/dagucloud/dagu/internal/persis/fileagentoauth"
	"github.com/dagucloud/dagu/internal/persis/fileagentsoul"
	"github.com/dagucloud/dagu/internal/persis/filememory"
	"github.com/dagucloud/dagu/internal/persis/store"
	"github.com/dagucloud/dagu/internal/secret"
)

// AgentStores contains the stores and resolvers used by runtime agent flows.
type AgentStores struct {
	ConfigStore     agent.ConfigStore
	ModelStore      agent.ModelStore
	MemoryStore     agent.MemoryStore
	SoulStore       agent.SoulStore
	OAuthManager    *agentoauth.Manager
	ContextResolver agent.RemoteContextResolver
	SecretStore     secret.Store
}

// AgentStoresOption configures file-backed agent store wiring.
type AgentStoresOption func(*AgentStoresOptions)

// AgentStoresOptions contains file-backed agent store wiring settings.
type AgentStoresOptions struct {
	ContextStore                  *clicontext.Store
	ResolveContextStoreFromConfig bool
}

// WithAgentContextStore sets the context store used by the remote context resolver.
func WithAgentContextStore(contextStore *clicontext.Store) AgentStoresOption {
	return func(o *AgentStoresOptions) {
		o.ContextStore = contextStore
	}
}

// WithAgentContextResolverFromConfig creates the remote context resolver from config paths.
func WithAgentContextResolverFromConfig() AgentStoresOption {
	return func(o *AgentStoresOptions) {
		o.ResolveContextStoreFromConfig = true
	}
}

// NewAgentStores wires the file-backed stores used by runtime agent flows.
func NewAgentStores(ctx context.Context, cfg *config.Config, opts ...AgentStoresOption) AgentStores {
	var options AgentStoresOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}

	var result AgentStores
	if encKey, encErr := crypto.ResolveKey(cfg.Paths.DataDir); encErr != nil {
		logger.Warn(ctx, "Failed to resolve encryption key for secret store", tag.Error(encErr))
	} else if enc, encErr := crypto.NewEncryptor(encKey); encErr != nil {
		logger.Warn(ctx, "Failed to create encryptor for secret store", tag.Error(encErr))
	} else if backend, backendErr := New(cfg.Paths.DataDir); backendErr != nil {
		logger.Warn(ctx, "Failed to open file backend for secret store", tag.Error(backendErr))
	} else if secretStore, storeErr := store.NewSecretStore(backend.Collection("secrets"), enc); storeErr != nil {
		logger.Warn(ctx, "Failed to create secret store", tag.Error(storeErr))
	} else {
		result.SecretStore = secretStore
	}
	if configStore, err := fileagentconfig.New(cfg.Paths.DataDir); err == nil {
		result.ConfigStore = configStore
	} else {
		logger.Warn(ctx, "Failed to create agent config store", tag.Error(err))
	}
	if modelStore, err := fileagentmodel.New(filepath.Join(cfg.Paths.DataDir, "agent", "models")); err == nil {
		result.ModelStore = modelStore
	} else {
		logger.Warn(ctx, "Failed to create agent model store", tag.Error(err))
	}
	if memoryStore, err := filememory.New(cfg.Paths.DAGsDir); err == nil {
		result.MemoryStore = memoryStore
	} else {
		logger.Warn(ctx, "Failed to create agent memory store", tag.Error(err))
	}
	if soulStore, err := fileagentsoul.New(ctx, filepath.Join(cfg.Paths.DAGsDir, "souls")); err == nil {
		result.SoulStore = soulStore
	} else {
		logger.Warn(ctx, "Failed to create agent soul store", tag.Error(err))
	}
	if manager, err := fileagentoauth.NewManager(cfg.Paths.DataDir); err == nil {
		result.OAuthManager = manager
	} else {
		logger.Warn(ctx, "Failed to create agent OAuth manager", tag.Error(err))
	}

	contextStore := options.ContextStore
	if contextStore == nil && options.ResolveContextStoreFromConfig {
		var err error
		contextStore, err = NewContextStore(cfg)
		if err != nil {
			logger.Warn(ctx, "Failed to create agent remote context resolver", tag.Error(err))
		}
	}
	if contextStore != nil {
		result.ContextResolver = &agent.RemoteContextResolverAdapter{Store: contextStore}
	}
	return result
}

// NewContextStore wires the encrypted file-backed CLI context store from config paths.
func NewContextStore(cfg *config.Config) (*clicontext.Store, error) {
	encKey, err := crypto.ResolveKey(cfg.Paths.DataDir)
	if err != nil {
		return nil, err
	}
	enc, err := crypto.NewEncryptor(encKey)
	if err != nil {
		return nil, err
	}
	return clicontext.NewStore(cfg.Paths.ContextsDir, enc)
}
