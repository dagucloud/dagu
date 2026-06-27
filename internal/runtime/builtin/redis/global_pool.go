// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package redis

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

// GlobalPoolConfig holds configuration for the global Redis pool manager.
type GlobalPoolConfig struct {
	// MaxClients is the maximum number of Redis clients to maintain.
	MaxClients int

	// ClientTimeout is the timeout for client operations.
	ClientTimeout time.Duration
}

// GlobalRedisPoolManager manages Redis client pools across all DAG executions.
// It is designed for workers where multiple DAGs run concurrently in a single
// process and share Redis connections.
type GlobalRedisPoolManager struct {
	mu     sync.RWMutex
	pools  map[string]*redisPoolEntry // Config hash -> pool entry
	config GlobalPoolConfig
	closed bool
}

// redisPoolEntry represents a single Redis client entry.
type redisPoolEntry struct {
	client   redis.UniversalClient
	cfgHash  string
	refCount int64
	created  time.Time
}

// NewGlobalRedisPoolManager creates a new global Redis pool manager.
func NewGlobalRedisPoolManager(cfg GlobalPoolConfig) *GlobalRedisPoolManager {
	return &GlobalRedisPoolManager{
		pools:  make(map[string]*redisPoolEntry),
		config: cfg,
	}
}

// GetOrCreateClient returns an existing client or creates a new one for the config.
func (m *GlobalRedisPoolManager) GetOrCreateClient(ctx context.Context, cfg *Config) (redis.UniversalClient, error) {
	key := hashConfig(cfg)

	// Try read lock first for existing client
	m.mu.RLock()
	if entry, ok := m.pools[key]; ok {
		atomic.AddInt64(&entry.refCount, 1)
		m.mu.RUnlock()
		return entry.client, nil
	}
	m.mu.RUnlock()

	// Acquire write lock to create new client
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return nil, fmt.Errorf("pool manager is closed")
	}

	// Double-check after acquiring write lock
	if entry, ok := m.pools[key]; ok {
		atomic.AddInt64(&entry.refCount, 1)
		return entry.client, nil
	}

	// Create new client
	client, err := createClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create redis client: %w", err)
	}

	// Verify connection with ping
	pingCtx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("failed to ping redis: %w", err)
	}

	m.pools[key] = &redisPoolEntry{
		client:   client,
		cfgHash:  key,
		refCount: 1,
		created:  time.Now(),
	}

	return client, nil
}

// ReleaseClient decrements the reference count for a config's client.
// The client is kept open for reuse; it will be closed when the manager is closed.
func (m *GlobalRedisPoolManager) ReleaseClient(cfg *Config) {
	key := hashConfig(cfg)

	m.mu.RLock()
	defer m.mu.RUnlock()

	if entry, ok := m.pools[key]; ok {
		atomic.AddInt64(&entry.refCount, -1)
		// Don't close immediately - client can be reused by other DAGs
	}
}

// Stats returns statistics about the pool manager.
func (m *GlobalRedisPoolManager) Stats() map[string]any {
	m.mu.RLock()
	defer m.mu.RUnlock()

	poolStats := make(map[string]any)
	for hash, entry := range m.pools {
		poolStats[hash] = map[string]any{
			"refCount": atomic.LoadInt64(&entry.refCount),
			"created":  entry.created,
		}
	}

	return map[string]any{
		"clientCount": len(m.pools),
		"closed":      m.closed,
		"clients":     poolStats,
	}
}

// Close closes all clients and the manager.
// This should be called when the worker shuts down.
func (m *GlobalRedisPoolManager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return nil
	}
	m.closed = true

	var errs []error
	for hash, entry := range m.pools {
		if err := entry.client.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close error for client %s: %w", hash, err))
		}
	}
	m.pools = nil

	if len(errs) > 0 {
		return fmt.Errorf("errors closing clients: %v", errs)
	}
	return nil
}

// hashConfig creates a short hash of the config for use as a map key.
// This avoids storing sensitive connection details as keys while ensuring
// different configs (including credentials and TLS settings) don't collide.
func hashConfig(cfg *Config) string {
	// Build a unique identifier from all connection parameters
	identifier := cfg.URL
	if identifier == "" {
		identifier = fmt.Sprintf("%s:%d:%d:%s:%s",
			cfg.Host, cfg.Port, cfg.DB, cfg.Mode, cfg.SentinelMaster)
		if len(cfg.SentinelAddrs) > 0 {
			for _, addr := range cfg.SentinelAddrs {
				identifier += ":" + addr
			}
		}
		if len(cfg.ClusterAddrs) > 0 {
			for _, addr := range cfg.ClusterAddrs {
				identifier += ":" + addr
			}
		}
	}

	// Include credentials in hash to differentiate connections with different auth
	identifier += fmt.Sprintf(":%s:%s:%d", cfg.Username, cfg.Password, cfg.MaxRetries)

	// Include TLS settings to differentiate secure vs insecure connections
	if cfg.TLS {
		identifier += fmt.Sprintf(":tls:%s:%s:%s:%v",
			cfg.TLSCert, cfg.TLSKey, cfg.TLSCA, cfg.TLSSkipVerify)
	}

	h := sha256.Sum256([]byte(identifier))
	return hex.EncodeToString(h[:8])
}
