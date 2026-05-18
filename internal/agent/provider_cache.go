// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"

	"github.com/dagucloud/dagu/internal/agentoauth"
	"github.com/dagucloud/dagu/internal/llm"
)

const defaultMaxCacheSize = 64

// ProviderCache caches LLM providers keyed by config hash.
// Safe for concurrent use.
type ProviderCache struct {
	mu      sync.RWMutex
	cache   map[string]cachedProvider
	maxSize int
}

type cachedProvider struct {
	provider llm.Provider
	model    string
}

// ProviderDeps are runtime-only dependencies used during provider creation.
type ProviderDeps struct {
	OAuthManager *agentoauth.Manager
}

// NewProviderCache creates a new empty provider cache.
func NewProviderCache() *ProviderCache {
	return &ProviderCache{
		cache:   make(map[string]cachedProvider),
		maxSize: defaultMaxCacheSize,
	}
}

// Set stores a provider in the cache for the given config.
// Useful for testing to inject mock providers.
func (c *ProviderCache) Set(cfg LLMConfig, provider llm.Provider) {
	hash := HashLLMConfig(cfg)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.evictIfFullLocked()
	c.cache[hash] = cachedProvider{
		provider: provider,
		model:    cfg.Model,
	}
}

// GetOrCreate returns a cached provider for the given config, or creates one.
func (c *ProviderCache) GetOrCreate(cfg LLMConfig, deps ProviderDeps) (llm.Provider, string, error) {
	hash := HashLLMConfig(cfg)

	c.mu.RLock()
	if entry, ok := c.cache[hash]; ok {
		c.mu.RUnlock()
		return entry.provider, entry.model, nil
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check after write lock
	if entry, ok := c.cache[hash]; ok {
		return entry.provider, entry.model, nil
	}

	provider, err := CreateLLMProvider(cfg, deps)
	if err != nil {
		return nil, "", err
	}

	c.evictIfFullLocked()
	c.cache[hash] = cachedProvider{
		provider: provider,
		model:    cfg.Model,
	}
	return provider, cfg.Model, nil
}

// Invalidate removes the cached provider for the given config.
func (c *ProviderCache) Invalidate(cfg LLMConfig) {
	hash := HashLLMConfig(cfg)
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.cache, hash)
}

// InvalidateAll clears the entire provider cache.
func (c *ProviderCache) InvalidateAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache = make(map[string]cachedProvider)
}

// evictIfFullLocked evicts one random entry when the cache is full.
// Uses Go's random map iteration order to avoid the thundering herd
// problem that would occur from clearing all entries at once.
// Must be called with c.mu held for writing.
func (c *ProviderCache) evictIfFullLocked() {
	if len(c.cache) >= c.maxSize {
		for k := range c.cache {
			delete(c.cache, k)
			break
		}
	}
}

// HashLLMConfig creates a deterministic hash of the LLM config for cache keying.
// Uses length-prefixed fields to prevent delimiter injection.
func HashLLMConfig(cfg LLMConfig) string {
	data := fmt.Sprintf("%d:%s|%d:%s|%d:%s|%d:%s",
		len(cfg.Provider), cfg.Provider,
		len(cfg.Model), cfg.Model,
		len(cfg.APIKey), cfg.APIKey,
		len(cfg.BaseURL), cfg.BaseURL,
	)
	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:])
}

// CreateLLMProvider creates an LLM provider from config.
func CreateLLMProvider(agentCfg LLMConfig, deps ProviderDeps) (llm.Provider, error) {
	providerType, err := llm.ParseProviderType(agentCfg.Provider)
	if err != nil {
		return nil, fmt.Errorf("invalid LLM provider: %w", err)
	}

	cfg := llm.Config{DisableRequestTimeout: true}
	cfg.APIKey = agentCfg.APIKey
	cfg.BaseURL = agentCfg.BaseURL
	if providerType == llm.ProviderOpenAICodex && deps.OAuthManager != nil {
		cfg.OAuthCredentialProvider = deps.OAuthManager.CredentialProvider(agentoauth.ProviderOpenAICodex)
	}

	return llm.NewProvider(providerType, cfg)
}
