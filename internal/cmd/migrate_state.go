// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"cmp"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"slices"
	"strconv"

	"github.com/dagucloud/dagu/v2/internal/cmn/config"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger"
	"github.com/dagucloud/dagu/v2/internal/cmn/logger/tag"
	"github.com/dagucloud/dagu/v2/internal/dispatch"
	"github.com/dagucloud/dagu/v2/internal/ir"
	"github.com/dagucloud/dagu/v2/internal/persis"
	"github.com/dagucloud/dagu/v2/internal/persis/file"
	"github.com/dagucloud/dagu/v2/internal/persis/file/migration"
	"github.com/dagucloud/dagu/v2/internal/runtime"
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
	migrator := migration.New(cfg, file.NewServiceRegistry(cfg), newV2RunStopper(cfg).stop)
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

// v2RunStopper cancels the runs kept in v2's directories, as dagu stop does:
// each run whose process is live, and each run on a worker, which the
// coordinator stops when the worker next connects.
type v2RunStopper struct {
	runs      *persis.DAGRunRepository
	procs     *persis.ProcRepository
	remote    dispatch.ActiveDistributedRunStore
	manager   runtime.Manager
	requested map[string]struct{}
}

// newV2RunStopper opens the run stores in v2's directories. It does not
// access the filesystem.
func newV2RunStopper(cfg *config.Config) *v2RunStopper {
	runs := file.NewDAGRunRepository(cfg)
	procs := file.NewProcRepository(cfg)
	return &v2RunStopper{
		runs:      runs,
		procs:     procs,
		remote:    file.NewServerStores(cfg).ActiveDistributedRunStore,
		manager:   runtime.NewManager(runs, procs, cfg),
		requested: make(map[string]struct{}),
	}
}

// stop requests the stop of each run not requested before, and returns the
// runs whose process is still live. A run on a worker has no local process.
func (s *v2RunStopper) stop(ctx context.Context) ([]string, error) {
	entries, err := s.procs.ListAllEntries(ctx)
	if err != nil {
		return nil, fmt.Errorf("list live run processes: %w", err)
	}
	var live, stop []ir.DAGRunRef
	for _, entry := range entries {
		if entry.Fresh {
			root := cmp.Or(entry.Meta.Root(), entry.Meta.DAGRun())
			live = append(live, root)
			stop = append(stop, root)
		}
	}
	remote, err := s.remote.ListAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("list runs on workers: %w", err)
	}
	for _, run := range remote {
		if run.Status == ir.Running {
			stop = append(stop, cmp.Or(run.Root, run.DAGRun))
		}
	}
	for _, ref := range stop {
		if _, done := s.requested[ref.String()]; done {
			continue
		}
		s.requested[ref.String()] = struct{}{}
		s.stopRun(ctx, ref)
	}

	names := make([]string, 0, len(live))
	for _, ref := range live {
		names = append(names, ref.String())
	}
	slices.Sort(names)
	return slices.Compact(names), nil
}

// stopRun requests the stop of the root run ref. A run whose record cannot
// be read is left to exit, or for its process heartbeat to go stale.
func (s *v2RunStopper) stopRun(ctx context.Context, ref ir.DAGRunRef) {
	ctx = logger.WithValues(ctx, tag.DAG(ref.Name), tag.RunID(ref.ID))
	attempt, err := s.runs.FindAttempt(ctx, ref)
	if err != nil {
		logger.Warn(ctx, "Cannot cancel a live run before migrating state to projects", tag.Error(err))
		return
	}
	dag, err := attempt.ReadDAG(ctx)
	if err != nil {
		logger.Warn(ctx, "Cannot cancel a live run before migrating state to projects", tag.Error(err))
		return
	}
	if err := s.manager.Stop(ctx, dag, ref.ID); err != nil {
		logger.Warn(ctx, "Failed to cancel a live run before migrating state to projects", tag.Error(err))
		return
	}
	logger.Info(ctx, "Cancelled a live run before migrating state to projects")
}

// healthPortEndpoints returns the endpoint of a health server on port, which
// 0 disables.
func healthPortEndpoints(port int) []healthcheck.GateEndpoint {
	if port == 0 {
		return nil
	}
	return []healthcheck.GateEndpoint{{Addr: fmt.Sprintf(":%d", port), HealthPath: healthcheck.Path}}
}
