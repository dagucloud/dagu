// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package sql

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// GlobalPoolConfig holds configuration for the global pool manager.
type GlobalPoolConfig struct {
	// MaxOpenConns is the maximum total open connections across all DSNs.
	MaxOpenConns int

	// MaxIdleConns is the maximum idle connections per DSN.
	MaxIdleConns int

	// ConnMaxLifetime is the maximum lifetime of a connection.
	ConnMaxLifetime time.Duration

	// ConnMaxIdleTime is the maximum idle time for a connection.
	ConnMaxIdleTime time.Duration
}

// GlobalPoolManager manages PostgreSQL connection pools across all DAG executions.
// It is designed for workers where multiple DAGs run concurrently in a single
// process and share database connections.
type GlobalPoolManager struct {
	mu     sync.RWMutex
	pools  map[string]*poolEntry // DSN hash -> pool entry
	config GlobalPoolConfig
	closed bool
}

// poolEntry represents a single connection pool for a specific DSN.
type poolEntry struct {
	db       *sql.DB
	dsnHash  string
	cleanup  func() error
	refCount int64
	created  time.Time
}

// poolManagerKey is the context key for the global pool manager.
type poolManagerKey struct{}

// NewGlobalPoolManager creates a new global pool manager.
func NewGlobalPoolManager(cfg GlobalPoolConfig) *GlobalPoolManager {
	return &GlobalPoolManager{
		pools:  make(map[string]*poolEntry),
		config: cfg,
	}
}

// WithPoolManager returns a context with the global pool manager.
func WithPoolManager(ctx context.Context, pm *GlobalPoolManager) context.Context {
	return context.WithValue(ctx, poolManagerKey{}, pm)
}

// GetPoolManager retrieves the global pool manager from context.
// Returns nil if no pool manager is configured.
func GetPoolManager(ctx context.Context) *GlobalPoolManager {
	pm, _ := ctx.Value(poolManagerKey{}).(*GlobalPoolManager)
	return pm
}

// GetOrCreatePool returns an existing pool or creates a new one for the DSN.
// The pool is configured with the global limits.
func (m *GlobalPoolManager) GetOrCreatePool(ctx context.Context, driver Driver, cfg *Config) (*sql.DB, error) {
	dsnHash := hashDSN(cfg.DSN)

	// Try read lock first for existing pool
	m.mu.RLock()
	if entry, ok := m.pools[dsnHash]; ok {
		atomic.AddInt64(&entry.refCount, 1)
		m.mu.RUnlock()
		return entry.db, nil
	}
	m.mu.RUnlock()

	// Acquire write lock to create new pool
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return nil, fmt.Errorf("pool manager is closed")
	}

	// Double-check after acquiring write lock
	if entry, ok := m.pools[dsnHash]; ok {
		atomic.AddInt64(&entry.refCount, 1)
		return entry.db, nil
	}

	// Create new connection using the driver
	db, cleanup, err := driver.Connect(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	// Configure with global limits
	// Each DSN gets a fraction of the total connections
	// Calculate limit for all pools including this new one
	poolCount := len(m.pools) + 1
	perDSNLimit := m.config.MaxOpenConns / poolCount
	if perDSNLimit < 1 && m.config.MaxOpenConns > 0 {
		perDSNLimit = 1
	}

	// Redistribute limits to ALL existing pools
	// This ensures total connections never exceed MaxOpenConns
	for _, entry := range m.pools {
		entry.db.SetMaxOpenConns(perDSNLimit)
	}

	db.SetMaxOpenConns(perDSNLimit)
	db.SetMaxIdleConns(m.config.MaxIdleConns)
	db.SetConnMaxLifetime(m.config.ConnMaxLifetime)
	db.SetConnMaxIdleTime(m.config.ConnMaxIdleTime)

	// Verify connection with ping
	pingCtx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		if cleanup != nil {
			_ = cleanup()
		}
		_ = db.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	m.pools[dsnHash] = &poolEntry{
		db:       db,
		dsnHash:  dsnHash,
		cleanup:  cleanup,
		refCount: 1,
		created:  time.Now(),
	}

	return db, nil
}

// ReleasePool decrements the reference count for a DSN's pool.
// The pool is kept open for reuse; it will be closed when the manager is closed.
func (m *GlobalPoolManager) ReleasePool(dsn string) {
	dsnHash := hashDSN(dsn)

	m.mu.RLock()
	defer m.mu.RUnlock()

	if entry, ok := m.pools[dsnHash]; ok {
		atomic.AddInt64(&entry.refCount, -1)
		// Don't close immediately - pool can be reused by other DAGs
	}
}

// Stats returns statistics about the pool manager.
func (m *GlobalPoolManager) Stats() map[string]any {
	m.mu.RLock()
	defer m.mu.RUnlock()

	poolStats := make(map[string]any)
	for hash, entry := range m.pools {
		stats := entry.db.Stats()
		poolStats[hash] = map[string]any{
			"refCount":     atomic.LoadInt64(&entry.refCount),
			"created":      entry.created,
			"openConns":    stats.OpenConnections,
			"inUse":        stats.InUse,
			"idle":         stats.Idle,
			"maxOpenConns": stats.MaxOpenConnections,
		}
	}

	return map[string]any{
		"poolCount": len(m.pools),
		"closed":    m.closed,
		"config": map[string]any{
			"maxOpenConns":    m.config.MaxOpenConns,
			"maxIdleConns":    m.config.MaxIdleConns,
			"connMaxLifetime": m.config.ConnMaxLifetime.String(),
			"connMaxIdleTime": m.config.ConnMaxIdleTime.String(),
		},
		"pools": poolStats,
	}
}

// Close closes all pools and the manager.
// This should be called when the worker shuts down.
func (m *GlobalPoolManager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return nil
	}
	m.closed = true

	var errs []error
	for hash, entry := range m.pools {
		// Call cleanup function if provided
		if entry.cleanup != nil {
			if err := entry.cleanup(); err != nil {
				errs = append(errs, fmt.Errorf("cleanup error for pool %s: %w", hash, err))
			}
		}
		// Close the database connection
		if err := entry.db.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close error for pool %s: %w", hash, err))
		}
	}
	m.pools = nil

	if len(errs) > 0 {
		return fmt.Errorf("errors closing pools: %v", errs)
	}
	return nil
}

// hashDSN creates a short hash of the DSN for use as a map key.
// This avoids storing the full DSN (which may contain credentials) as a key.
func hashDSN(dsn string) string {
	h := sha256.Sum256([]byte(dsn))
	return hex.EncodeToString(h[:8])
}
