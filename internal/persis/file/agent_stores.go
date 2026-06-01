// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package file

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/dagucloud/dagu/internal/agent"
	"github.com/dagucloud/dagu/internal/agentoauth"
	"github.com/dagucloud/dagu/internal/clicontext"
	"github.com/dagucloud/dagu/internal/cmn/config"
	"github.com/dagucloud/dagu/internal/cmn/crypto"
	"github.com/dagucloud/dagu/internal/cmn/fileutil"
	"github.com/dagucloud/dagu/internal/cmn/logger"
	"github.com/dagucloud/dagu/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/internal/persis/file/agentskill"
	"github.com/dagucloud/dagu/internal/persis/file/agentsoul"
	"github.com/dagucloud/dagu/internal/persis/file/memory"
	"github.com/dagucloud/dagu/internal/persis/store"
	"github.com/dagucloud/dagu/internal/profile"
	"github.com/dagucloud/dagu/internal/secret"
)

// AgentStores contains the stores and resolvers used by runtime agent flows.
type AgentStores = agent.RuntimeStores

// AgentStoresOption configures file-backed agent store wiring.
type AgentStoresOption func(*AgentStoresOptions)

// AgentStoresOptions contains file-backed agent store wiring settings.
type AgentStoresOptions struct {
	ContextStore                  *clicontext.Store
	ResolveContextStoreFromConfig bool
	MemoryCache                   *fileutil.Cache[string]
	SeedReferences                bool
	SeedExampleSouls              bool
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

// WithAgentMemoryCache sets the file cache used by the agent memory store.
func WithAgentMemoryCache(cache *fileutil.Cache[string]) AgentStoresOption {
	return func(o *AgentStoresOptions) {
		o.MemoryCache = cache
	}
}

// WithAgentSeedReferences seeds bundled reference files and records their path.
func WithAgentSeedReferences() AgentStoresOption {
	return func(o *AgentStoresOptions) {
		o.SeedReferences = true
	}
}

// WithAgentSeedExampleSouls seeds bundled example souls before opening the soul store.
func WithAgentSeedExampleSouls() AgentStoresOption {
	return func(o *AgentStoresOptions) {
		o.SeedExampleSouls = true
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
	if cfg == nil {
		return AgentStores{}
	}

	var result AgentStores
	result.SecretStore = NewSecretStore(ctx, cfg)
	result.ProfileStore = NewProfileStore(ctx, cfg)
	if options.SeedReferences {
		result.ReferencesDir = SeedAgentReferences(cfg)
	}
	if configStore, err := newAgentConfigStore(cfg.Paths.DataDir); err == nil {
		result.ConfigStore = configStore
	} else {
		logger.Warn(ctx, "Failed to create agent config store", tag.Error(err))
	}
	if modelStore, err := newAgentModelStore(filepath.Join(cfg.Paths.DataDir, "agent", "models")); err == nil {
		result.ModelStore = modelStore
	} else {
		logger.Warn(ctx, "Failed to create agent model store", tag.Error(err))
	}
	var memoryOpts []memory.Option
	if options.MemoryCache != nil {
		memoryOpts = append(memoryOpts, memory.WithFileCache(options.MemoryCache))
	}
	if memoryStore, err := memory.New(cfg.Paths.DAGsDir, memoryOpts...); err == nil {
		result.MemoryStore = memoryStore
	} else {
		logger.Warn(ctx, "Failed to create agent memory store", tag.Error(err))
	}
	soulsDir := filepath.Join(cfg.Paths.DAGsDir, "souls")
	if options.SeedExampleSouls {
		if _, err := agentsoul.SeedExampleSouls(ctx, soulsDir); err != nil {
			logger.Warn(ctx, "Failed to seed example souls", tag.Error(err))
		}
	}
	if soulStore, err := agentsoul.New(ctx, soulsDir); err == nil {
		result.SoulStore = soulStore
	} else {
		logger.Warn(ctx, "Failed to create agent soul store", tag.Error(err))
	}
	if manager, err := newAgentOAuthManager(cfg.Paths.DataDir); err == nil {
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

// NewSnapshotStores wires the file-backed stores required to build worker snapshots.
func NewSnapshotStores(ctx context.Context, paths config.PathsConfig) (agent.SnapshotStores, error) {
	configStore, err := newAgentConfigStore(paths.DataDir)
	if err != nil {
		return agent.SnapshotStores{}, fmt.Errorf("create agent config store: %w", err)
	}
	modelStore, err := newAgentModelStore(filepath.Join(paths.DataDir, "agent", "models"))
	if err != nil {
		return agent.SnapshotStores{}, fmt.Errorf("create agent model store: %w", err)
	}
	soulStore, err := agentsoul.New(ctx, filepath.Join(paths.DAGsDir, "souls"))
	if err != nil {
		return agent.SnapshotStores{}, fmt.Errorf("create agent soul store: %w", err)
	}
	memoryStore, err := memory.New(paths.DAGsDir)
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

// NewSecretStore wires the encrypted file-backed secret store from config paths.
func NewSecretStore(ctx context.Context, cfg *config.Config) secret.Store {
	if cfg == nil || cfg.Paths.DataDir == "" {
		return nil
	}
	if encKey, encErr := crypto.ResolveKey(cfg.Paths.DataDir); encErr != nil {
		logger.Warn(ctx, "Failed to resolve encryption key for secret store", tag.Error(encErr))
	} else if enc, encErr := crypto.NewEncryptor(encKey); encErr != nil {
		logger.Warn(ctx, "Failed to create encryptor for secret store", tag.Error(encErr))
	} else if secretStore, storeErr := store.NewSecretStore(
		NewCollection(filepath.Join(cfg.Paths.DataDir, "secrets"), WithIndentedJSON()), enc,
	); storeErr != nil {
		logger.Warn(ctx, "Failed to create secret store", tag.Error(storeErr))
	} else {
		return secretStore
	}
	return nil
}

// NewProfileStore wires the file-backed runtime profile store from config paths.
func NewProfileStore(ctx context.Context, cfg *config.Config) profile.Store {
	if cfg == nil || cfg.Paths.DataDir == "" {
		return nil
	}
	profileStore, err := store.NewProfileStore(
		NewCollection(filepath.Join(cfg.Paths.DataDir, "profiles"), WithIndentedJSON()),
	)
	if err != nil {
		logger.Warn(ctx, "Failed to create profile store", tag.Error(err))
		return nil
	}
	return profileStore
}

// NewAgentSessionStore wires the file-backed agent session store from config paths.
// The collection-backed [store.SessionStore] uses hierarchical IDs
// ("{userID}/{sessionID}") that map under the file backend to
// {SessionsDir}/{userID}/{sessionID}.json — byte-identical to the
// pre-refactor on-disk layout.
func NewAgentSessionStore(cfg *config.Config) (agent.SessionStore, error) {
	if cfg == nil {
		return nil, errors.New("file: config cannot be nil")
	}
	dir := cfg.Paths.SessionsDir
	if dir == "" {
		return nil, errors.New("session store: SessionsDir cannot be empty")
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("session store: create directory %s: %w", dir, err)
	}
	return store.NewSessionStore(
		NewCollection(dir, WithIndentedJSON()),
		store.WithMaxPerUser(cfg.Server.Session.MaxPerUser),
	)
}

// SeedAgentReferences writes bundled agent references to the configured data directory.
func SeedAgentReferences(cfg *config.Config) string {
	if cfg == nil || cfg.Paths.DataDir == "" {
		return ""
	}
	return agentskill.SeedReferences(filepath.Join(cfg.Paths.DataDir, "agent", "references"))
}

// NewContextStore wires the encrypted file-backed CLI context store from config paths.
func NewContextStore(cfg *config.Config) (*clicontext.Store, error) {
	if cfg == nil {
		return nil, errors.New("file: config cannot be nil")
	}
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

func newAgentConfigStore(dataDir string) (agent.ConfigStore, error) {
	if dataDir == "" {
		return nil, errors.New("agent config store: dataDir cannot be empty")
	}
	dir := filepath.Join(dataDir, "agent")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("agent config store: create directory %s: %w", dir, err)
	}
	return store.NewAgentConfigStore(NewCollection(dir, WithIndentedJSON())), nil
}

func newAgentModelStore(baseDir string) (agent.ModelStore, error) {
	if baseDir == "" {
		return nil, errors.New("agent model store: baseDir cannot be empty")
	}
	if err := os.MkdirAll(baseDir, 0o750); err != nil {
		return nil, fmt.Errorf("agent model store: create directory %s: %w", baseDir, err)
	}
	return store.NewAgentModelStore(NewCollection(baseDir, WithIndentedJSON()))
}

func newAgentOAuthManager(dataDir string) (*agentoauth.Manager, error) {
	if dataDir == "" {
		return nil, errors.New("agent oauth store: dataDir cannot be empty")
	}
	encKey, err := crypto.ResolveKey(dataDir)
	if err != nil {
		return nil, fmt.Errorf("resolve encryption key for agent OAuth store: %w", err)
	}
	enc, err := crypto.NewEncryptor(encKey)
	if err != nil {
		return nil, fmt.Errorf("create encryptor for agent OAuth store: %w", err)
	}
	dir := filepath.Join(dataDir, "agent", "oauth")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("agent oauth store: create directory %s: %w", dir, err)
	}
	oauthStore, err := store.NewAgentOAuthStore(NewCollection(dir, WithIndentedJSON()), enc)
	if err != nil {
		return nil, fmt.Errorf("create agent OAuth store: %w", err)
	}
	return agentoauth.NewManager(oauthStore), nil
}
