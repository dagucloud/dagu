// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strconv"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/persis/file/migration"
	"github.com/dagucloud/dagu/v2/internal/service/frontend"
	"github.com/dagucloud/dagu/v2/internal/service/healthcheck"
)

// migrateState moves v2 state into the state directories of projects before
// the command opens a store, or waits for the process that does. It does
// nothing while projects are off, for a worker, which keeps no state of its
// own, and for a command that reads no state. Meanwhile a server, scheduler,
// or coordinator answers its health endpoint with the progress, and every
// other request with 503.
func migrateState(ctx context.Context, cmdName string, scope commandScope, cfg *config.Config) error {
	if !cfg.ProjectsEnabled || scope == commandScopeStatic || cmdName == "worker" {
		return nil
	}
	migrator := migration.New(cfg.Paths.DataDir, file.NewServiceRegistry(cfg))
	done, err := migrator.Done()
	if err != nil || done {
		return err
	}

	endpoints, err := migrationGateEndpoints(ctx, cmdName, cfg)
	if err != nil {
		return err
	}
	if len(endpoints) > 0 {
		gate, err := healthcheck.StartMigrationGate(ctx, endpoints, func() any { return migrator.Progress() })
		if err != nil {
			return err
		}
		defer func() {
			if err := gate.Stop(ctx); err != nil {
				logger.Warn(ctx, "Failed to stop the migration health gate", tag.Error(err))
			}
		}()
	}
	return migrator.Run(ctx)
}

// migrationGateEndpoints returns where the command serves its health
// endpoint.
func migrationGateEndpoints(ctx context.Context, cmdName string, cfg *config.Config) ([]healthcheck.GateEndpoint, error) {
	switch cmdName {
	case "server", "start-all":
		healthPath, err := frontend.HealthPath(ctx, cfg)
		if err != nil {
			return nil, err
		}
		endpoint := healthcheck.GateEndpoint{
			Addr:       net.JoinHostPort(cfg.Server.Host, strconv.Itoa(cfg.Server.Port)),
			HealthPath: healthPath,
		}
		if tlsCfg := cfg.Server.TLS; tlsCfg != nil {
			cert, err := tls.LoadX509KeyPair(tlsCfg.CertFile, tlsCfg.KeyFile)
			if err != nil {
				return nil, fmt.Errorf("load TLS certificate: %w", err)
			}
			endpoint.TLS = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
		}
		return []healthcheck.GateEndpoint{endpoint}, nil
	case "scheduler":
		return healthPortEndpoints(cfg.Scheduler.Port), nil
	case "coordinator":
		return healthPortEndpoints(cfg.Coordinator.HealthPort), nil
	default:
		return nil, nil
	}
}

// healthPortEndpoints returns the endpoint of a health server on port, which
// 0 disables.
func healthPortEndpoints(port int) []healthcheck.GateEndpoint {
	if port == 0 {
		return nil
	}
	return []healthcheck.GateEndpoint{{Addr: fmt.Sprintf(":%d", port), HealthPath: healthcheck.Path}}
}
