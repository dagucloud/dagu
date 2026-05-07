// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package postgres

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	cmncrypto "github.com/dagucloud/dagu/internal/cmn/crypto"
	"github.com/dagucloud/dagu/internal/core/exec"
	"github.com/dagucloud/dagu/internal/persis/controlplanestore/postgres/db"
)

var _ exec.DAGRunStore = (*Store)(nil)

// PoolConfig configures the PostgreSQL connection pool.
type PoolConfig struct {
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime int
	ConnMaxIdleTime int
}

// Config configures the PostgreSQL control-plane store.
type Config struct {
	DSN               string
	LocalWorkDirBase  string
	AutoMigrate       bool
	LatestStatusToday bool
	Location          *time.Location
	Pool              PoolConfig
	WebhookEncryptor  *cmncrypto.Encryptor
}

// Store persists control-plane data in PostgreSQL.
type Store struct {
	pool              *pgxpool.Pool
	queries           *db.Queries
	localWorkDirBase  string
	latestStatusToday bool
	location          *time.Location
	webhookEncryptor  *cmncrypto.Encryptor
	serviceMu         sync.Mutex
	services          map[exec.ServiceName]*postgresServiceRegistration
}

// DAGRuns returns the DAG-run sub-store implemented by this PostgreSQL control-plane store.
func (s *Store) DAGRuns() exec.DAGRunStore {
	return s
}

// New creates a PostgreSQL-backed control-plane store.
func New(ctx context.Context, cfg Config) (*Store, error) {
	if cfg.DSN == "" {
		return nil, errors.New("postgres control-plane store DSN must not be empty")
	}
	if cfg.AutoMigrate {
		if err := RunMigrations(ctx, cfg.DSN); err != nil {
			return nil, err
		}
	}

	poolCfg, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("parse postgres control-plane store DSN: %w", err)
	}
	applyPoolConfig(poolCfg, cfg.Pool)

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("open postgres control-plane store pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres control-plane store: %w", err)
	}

	location := cfg.Location
	if location == nil {
		location = time.Local
	}

	return &Store{
		pool:              pool,
		queries:           db.New(pool),
		localWorkDirBase:  cfg.LocalWorkDirBase,
		latestStatusToday: cfg.LatestStatusToday,
		location:          location,
		webhookEncryptor:  cfg.WebhookEncryptor,
		services:          make(map[exec.ServiceName]*postgresServiceRegistration),
	}, nil
}

// Close closes the underlying PostgreSQL connection pool.
func (s *Store) Close() error {
	if s != nil && s.pool != nil {
		s.Unregister(context.Background())
		s.pool.Close()
	}
	return nil
}

func applyPoolConfig(poolCfg *pgxpool.Config, cfg PoolConfig) {
	if cfg.MaxOpenConns > 0 {
		poolCfg.MaxConns = int32(cfg.MaxOpenConns) //nolint:gosec
	}
	if cfg.MaxIdleConns > 0 {
		minIdleConns := int32(cfg.MaxIdleConns) //nolint:gosec
		if poolCfg.MaxConns > 0 && minIdleConns > poolCfg.MaxConns {
			minIdleConns = poolCfg.MaxConns
		}
		poolCfg.MinIdleConns = minIdleConns
	}
	if cfg.ConnMaxLifetime > 0 {
		poolCfg.MaxConnLifetime = time.Duration(cfg.ConnMaxLifetime) * time.Second
	}
	if cfg.ConnMaxIdleTime > 0 {
		poolCfg.MaxConnIdleTime = time.Duration(cfg.ConnMaxIdleTime) * time.Second
	}
}
